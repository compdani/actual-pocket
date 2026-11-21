// Package auth resolves the acting user for a request.
//
// Every token the server issues or accepts is a PocketBase auth token (D5).
// PocketBase owns password hashing, token signing and OAuth2; this package only
// maps a token onto a users record, plus the two things PocketBase does not
// provide: a per-token logout denylist (D6) and budget-scoped API keys.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/golang-jwt/jwt/v5"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// Errors returned by FromToken. The HTTP layer turns them into the exact
// response bodies the client expects.
var (
	// ErrTokenMissing means no token was sent at all.
	ErrTokenMissing = errors.New("token missing")
	// ErrTokenExpired means the JWT's exp has passed.
	ErrTokenExpired = errors.New("token expired")
	// ErrTokenInvalid covers a bad signature, an unknown record, a revoked
	// token and a disabled user.
	ErrTokenInvalid = errors.New("token invalid")
)

// apiKeyPattern is Node's shape check from validate-budget-session.ts.
var apiKeyPattern = regexp.MustCompile(`^actual_[A-Za-z0-9_-]{43}$`)

// Session is the authenticated identity for one request.
type Session struct {
	// UserID is the PocketBase users record id.
	UserID string
	// Record is the users record.
	Record *core.Record
	// APIKeyFileIDs is non-nil only for API-key sessions, where an empty list
	// means "no budgets allowed".
	APIKeyFileIDs []string
	// IsAPIKey reports that the request used a budget-scoped API key.
	IsAPIKey bool
}

// Resolver maps tokens onto users records.
type Resolver struct {
	app   core.App
	store *store.Store

	dummyOnce     sync.Once
	dummyPassword *core.Record

	// tokensMu guards lastTokenExp, the exp of the last token issued per user.
	// It keeps two logins in the same second from sharing a token.
	tokensMu     sync.Mutex
	lastTokenExp map[string]int64
}

// NewResolver creates a resolver.
func NewResolver(app core.App, s *store.Store) *Resolver {
	return &Resolver{app: app, store: s}
}

// TokenFrom applies the lookup order from the plan: JSON body token, then
// X-ACTUAL-TOKEN, then Authorization (with an optional Bearer prefix).
func TokenFrom(e *core.RequestEvent, bodyToken string) string {
	if bodyToken != "" {
		return bodyToken
	}

	if value := e.Request.Header.Get("X-Actual-Token"); value != "" {
		return value
	}

	authorization := e.Request.Header.Get("Authorization")
	if authorization == "" {
		return ""
	}

	const bearer = "bearer "
	if len(authorization) > len(bearer) && strings.EqualFold(authorization[:len(bearer)], bearer) {
		return authorization[len(bearer):]
	}

	return authorization
}

// TokenExpiry reads the JWT's exp claim without verifying the signature.
//
// PocketBase's security.ParseUnverifiedJWT also validates the claims, which
// turns an expired token into an error and hides the exp we need to tell an
// expired token apart from a forged one.
func TokenExpiry(token string) (int64, bool) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	if _, _, err := parser.ParseUnverified(token, claims); err != nil {
		return 0, false
	}

	switch value := claims["exp"].(type) {
	case float64:
		return int64(value), true
	case int64:
		return value, true
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	}

	return 0, false
}

// FromToken resolves a PocketBase auth token.
func (r *Resolver) FromToken(token string) (*Session, error) {
	if token == "" {
		return nil, ErrTokenMissing
	}

	// An expired token must be reported as expired, not as invalid: the client
	// reacts differently (it prompts for the password instead of logging out).
	if expiresAt, ok := TokenExpiry(token); ok && expiresAt <= time.Now().Unix() {
		return nil, ErrTokenExpired
	}

	revoked, err := r.store.IsTokenRevoked(store.HashToken(token))
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, ErrTokenInvalid
	}

	record, err := r.app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if record.Collection().Name != "users" {
		return nil, ErrTokenInvalid
	}
	if !record.GetBool("enabled") {
		return nil, ErrTokenInvalid
	}

	return &Session{UserID: record.Id, Record: record}, nil
}

// FromAPIKey resolves a budget-scoped API key, as validate-budget-session.ts
// does. The key's grants are filtered to budgets the user can still access.
func (r *Resolver) FromAPIKey(token string) (*Session, error) {
	if !apiKeyPattern.MatchString(token) {
		return nil, ErrTokenInvalid
	}

	key, user, err := r.store.APIKeyByHash(store.HashToken(token))
	if err != nil {
		return nil, ErrTokenInvalid
	}

	fileIDs, err := r.store.APIKeyFileIDs(key.Id)
	if err != nil {
		return nil, err
	}

	// Grants restrict access, but do not decide whether the key is valid.
	allowed := make([]string, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		canAccess, err := r.store.CanAccessActiveFile(fileID, user.Id)
		if err != nil {
			return nil, err
		}
		if canAccess {
			allowed = append(allowed, fileID)
		}
	}

	return &Session{UserID: user.Id, Record: user, APIKeyFileIDs: allowed, IsAPIKey: true}, nil
}

// ResolveSession authenticates a route that only accepts normal sessions and
// writes the error response itself. It returns nil when the response was
// written.
func (r *Resolver) ResolveSession(e *core.RequestEvent, bodyToken string) *Session {
	session, err := r.FromToken(TokenFrom(e, bodyToken))
	if err != nil {
		_ = writeTokenError(e, err)
		return nil
	}

	e.Set("user", session.Record)
	return session
}

// ResolveBudgetSession mirrors Node's validateBudgetSession: a token that looks
// like an API key is treated as one, anything else as a session.
func (r *Resolver) ResolveBudgetSession(e *core.RequestEvent, bodyToken string) *Session {
	token := TokenFrom(e, bodyToken)

	if strings.HasPrefix(token, "actual_") {
		session, err := r.FromAPIKey(token)
		if err != nil {
			// API key failures are always the short unauthorized body.
			_ = httpx.Fail(e, http.StatusUnauthorized, "unauthorized")
			return nil
		}
		e.Set("user", session.Record)
		return session
	}

	return r.ResolveSession(e, bodyToken)
}

// CheckDummyPassword spends the same time as a real password check, so an
// unknown email is not distinguishable from a wrong password by timing.
func (r *Resolver) CheckDummyPassword(password string) {
	record := r.dummyRecord()
	if record == nil {
		return
	}
	_ = record.ValidatePassword(password)
}

func (r *Resolver) dummyRecord() *core.Record {
	r.dummyOnce.Do(func() {
		users, err := r.app.FindCollectionByNameOrId("users")
		if err != nil {
			return
		}
		record := core.NewRecord(users)
		record.SetPassword(security.RandomString(32))
		r.dummyPassword = record
	})

	return r.dummyPassword
}

func writeTokenError(e *core.RequestEvent, err error) error {
	if errors.Is(err, ErrTokenExpired) {
		return httpx.Fail(e, http.StatusUnauthorized, "token-expired")
	}

	return httpx.FailDetails(e, http.StatusUnauthorized, "unauthorized", "token-not-found")
}
