package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"ganigeorgiev.com/pocketbase/ozzo-validation/v4"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/ratelimit"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

type okResponse struct {
	Status string `json:"status"`
}

type okDataResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data"`
}

type loginMethod struct {
	Method      string `json:"method"`
	Provider    string `json:"provider,omitempty"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

type loginMethodsResponse struct {
	Status  string        `json:"status"`
	Methods []loginMethod `json:"methods"`
}

type needsBootstrapData struct {
	Bootstrapped          bool          `json:"bootstrapped"`
	LoginMethod           string        `json:"loginMethod"`
	AvailableLoginMethods []loginMethod `json:"availableLoginMethods"`
	Multiuser             bool          `json:"multiuser"`
}

type needsBootstrapResponse struct {
	Status string             `json:"status"`
	Data   needsBootstrapData `json:"data"`
}

type tokenData struct {
	Token string `json:"token"`
}

type validateData struct {
	Validated   bool              `json:"validated"`
	UserName    string            `json:"userName"`
	Permission  string            `json:"permission"`
	UserID      string            `json:"userId"`
	DisplayName string            `json:"displayName"`
	LoginMethod string            `json:"loginMethod"`
	Prefs       map[string]string `json:"prefs"`
}

type validateAPIKeyData struct {
	Validated   bool   `json:"validated"`
	LoginMethod string `json:"loginMethod"`
}

type bootstrapRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	LoginMethod string `json:"loginMethod"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Provider    string `json:"provider"`
	ReturnURL   string `json:"returnUrl"`
}

type tokenRequest struct {
	Token string `json:"token"`
}

type changePasswordRequest struct {
	Token           string `json:"token"`
	Password        string `json:"password"`
	CurrentPassword string `json:"currentPassword"`
}

type serverPrefsRequest struct {
	Prefs map[string]any `json:"prefs"`
}

// errAlreadyBootstrapped unwinds the bootstrap transaction.
var errAlreadyBootstrapped = errors.New("already-bootstrapped")

func registerAccount(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	account := actual.Group("/account")
	account.Bind(apis.BodyLimit(int64(opts.Config.Upload.FileSizeLimitMB) << 20))

	// Node limits bootstrap/login/change-password to 5 failed attempts per 15
	// minutes per IP.
	authLimiter := ratelimit.New(ratelimit.Config{
		Limit:                  5,
		Window:                 15 * time.Minute,
		Message:                httpx.ErrorBody{Status: "error", Reason: "too-many-requests"},
		SkipSuccessfulRequests: true,
	})
	authLimiter.Identity(func(r *http.Request) string {
		return httpx.ClientIP(r, opts.Config.TrustedProxies)
	})

	account.GET("/needs-bootstrap", opts.needsBootstrap)
	account.POST("/bootstrap", opts.bootstrap).Bind(authLimiter.Handler())
	account.GET("/login-methods", opts.loginMethods)
	account.POST("/login", opts.login).Bind(authLimiter.Handler())
	account.POST("/logout", opts.logout)
	account.POST("/change-password", opts.changePassword).Bind(authLimiter.Handler())
	account.POST("/server-prefs", opts.serverPrefs)
	account.GET("/validate", opts.validate)
}

func (o Options) needsBootstrap(e *core.RequestEvent) error {
	count, err := o.Store.CountUsers()
	if err != nil {
		return httpx.Error(e)
	}

	methods, err := o.availableLoginMethods()
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, needsBootstrapResponse{
		Status: "ok",
		Data: needsBootstrapData{
			Bootstrapped:          count > 0,
			LoginMethod:           "password",
			AvailableLoginMethods: methods,
			Multiuser:             true,
		},
	})
}

func (o Options) loginMethods(e *core.RequestEvent) error {
	methods, err := o.availableLoginMethods()
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, loginMethodsResponse{Status: "ok", Methods: methods})
}

// availableLoginMethods lists password auth (when enabled) and one entry per
// OAuth2 provider configured on the users collection in the admin UI.
func (o Options) availableLoginMethods() ([]loginMethod, error) {
	users, err := o.App.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, fmt.Errorf("find users collection: %w", err)
	}

	methods := make([]loginMethod, 0, 1+len(users.OAuth2.Providers))

	if users.PasswordAuth.Enabled {
		methods = append(methods, loginMethod{
			Method:      "password",
			DisplayName: "Email & password",
			Active:      true,
		})
	}

	for _, provider := range users.OAuth2.Providers {
		displayName := provider.DisplayName
		if displayName == "" {
			displayName = provider.Name
		}
		methods = append(methods, loginMethod{
			Method:      "openid",
			Provider:    provider.Name,
			DisplayName: displayName,
			Active:      true,
		})
	}

	return methods, nil
}

func (o Options) bootstrap(e *core.RequestEvent) error {
	body := bootstrapRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	err := o.App.RunInTransaction(func(txApp core.App) error {
		users, err := txApp.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		// Re-check inside the transaction so two bootstraps cannot both win.
		count, err := txApp.CountRecords("users")
		if err != nil {
			return err
		}
		if count > 0 {
			return errAlreadyBootstrapped
		}

		record := core.NewRecord(users)
		record.Set("email", body.Email)
		record.Set("name", "")
		record.Set("role", "ADMIN")
		record.Set("enabled", true)
		record.Set("owner", true)
		record.Set("verified", true)

		// The password is validated when the record is saved.
		store.SetPassword(record, body.Password)

		return txApp.Save(record)
	})

	switch {
	case errors.Is(err, errAlreadyBootstrapped):
		return httpx.Fail(e, http.StatusBadRequest, "already-bootstrapped")
	case err != nil:
		if reason := fieldReason(err); reason != "" {
			return httpx.Fail(e, http.StatusBadRequest, reason)
		}
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct{}{}})
}

func (o Options) login(e *core.RequestEvent) error {
	body := loginRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	// OAuth2 login is wired in Phase 9. A fresh install has no providers, so
	// this is the truthful answer until then.
	if body.LoginMethod == "openid" {
		return httpx.Fail(e, http.StatusBadRequest, "provider-not-configured")
	}

	record, err := o.Store.UserByEmail(body.Email)
	if err != nil {
		// Spend the same time as a real check so an unknown email cannot be
		// told apart from a wrong password.
		o.Auth.CheckDummyPassword(body.Password)
		return httpx.Fail(e, http.StatusBadRequest, "invalid-password")
	}

	if !record.ValidatePassword(body.Password) {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-password")
	}

	if !record.GetBool("enabled") {
		return httpx.Fail(e, http.StatusBadRequest, "user-disabled")
	}

	token, err := o.Auth.IssueToken(record)
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: tokenData{Token: token}})
}

func (o Options) logout(e *core.RequestEvent) error {
	body := tokenRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	if body.Token == "" {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-token")
	}

	if _, err := o.Auth.FromToken(body.Token); err != nil {
		if errors.Is(err, auth.ErrTokenExpired) {
			return httpx.Fail(e, http.StatusUnauthorized, "token-expired")
		}
		return httpx.FailDetails(e, http.StatusUnauthorized, "unauthorized", "token-not-found")
	}

	if err := o.Store.RevokeToken(store.HashToken(body.Token), tokenExpiry(o.App, body.Token)); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct{}{}})
}

func (o Options) changePassword(e *core.RequestEvent) error {
	body := changePasswordRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, body.Token)
	if session == nil {
		return nil
	}

	// An OAuth2-only user has a random password nobody knows; there is nothing
	// to change.
	if !session.Record.GetBool(store.FieldPasswordSet) {
		return httpx.Fail(e, http.StatusBadRequest, "no-password-method")
	}

	if !session.Record.ValidatePassword(body.CurrentPassword) {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-current-password")
	}

	store.SetPassword(session.Record, body.Password)

	// Saving validates the new password and rotates tokenKey, which logs the
	// user out everywhere...
	if err := o.App.Save(session.Record); err != nil {
		if reason := fieldReason(err); reason != "" {
			return httpx.Fail(e, http.StatusBadRequest, reason)
		}
		return httpx.Error(e)
	}

	// ...so hand the current device a token signed with the new key.
	token, err := o.Auth.IssueToken(session.Record)
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: tokenData{Token: token}})
}

func (o Options) serverPrefs(e *core.RequestEvent) error {
	body := serverPrefsRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}

	if body.Prefs == nil {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-prefs")
	}

	if err := o.Store.SetServerPrefs(body.Prefs); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct{}{}})
}

func (o Options) validate(e *core.RequestEvent) error {
	session := o.Auth.ResolveBudgetSession(e, "")
	if session == nil {
		return nil
	}

	if session.IsAPIKey {
		return httpx.JSON(e, http.StatusOK, okDataResponse{
			Status: "ok",
			Data:   validateAPIKeyData{Validated: true, LoginMethod: "api-key"},
		})
	}

	prefs, err := o.Store.ServerPrefs()
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{
		Status: "ok",
		Data: validateData{
			Validated:   true,
			UserName:    session.Record.GetString("email"),
			Permission:  session.Record.GetString("role"),
			UserID:      session.UserID,
			DisplayName: session.Record.GetString("name"),
			LoginMethod: loginMethodFor(session.Record),
			Prefs:       prefs,
		},
	})
}

// loginMethodFor reports how the user can log in: a user without a chosen
// password was created through an OAuth2 provider.
func loginMethodFor(record *core.Record) string {
	if record.GetBool(store.FieldPasswordSet) {
		return "password"
	}
	return "openid"
}

// readJSONPayload returns the raw JSON body, or ok=false when the request has
// no JSON body (another content type or an empty body), like express.json().
func readJSONPayload(e *core.RequestEvent) ([]byte, bool, error) {
	contentType := e.Request.Header.Get("Content-Type")
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return nil, false, nil
		}
	}

	raw, err := io.ReadAll(e.Request.Body)
	if err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return nil, false, httpx.Fail(e, http.StatusRequestEntityTooLarge, "request-entity-too-large")
		}
		return nil, false, httpx.Error(e)
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false, nil
	}

	return raw, true, nil
}

// bindJSON parses an application/json body the way express.json() does: an
// empty body stays zero-valued, other content types are ignored, and malformed
// JSON is a 400.
func bindJSON(e *core.RequestEvent, dst any) error {
	raw, ok, err := readJSONPayload(e)
	if err != nil || !ok {
		return err
	}

	if err := json.Unmarshal(raw, dst); err != nil {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
	}

	return nil
}

// bindJSONMap parses the body into dst and also returns it as a raw map, for
// PocketBase rule checks that read @request.body.<field>.
func bindJSONMap(e *core.RequestEvent, dst any) (map[string]any, error) {
	raw, ok, err := readJSONPayload(e)
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if !ok {
		return body, nil
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return nil, httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
	}

	return body, nil
}

// fieldReason maps a PocketBase validation error onto the Node server's reason
// strings.
func fieldReason(err error) string {
	var validationErrors validation.Errors
	if errors.As(err, &validationErrors) {
		if _, ok := validationErrors["password"]; ok {
			return "invalid-password"
		}
		if _, ok := validationErrors["email"]; ok {
			return "invalid-email"
		}
	}

	return ""
}

// tokenExpiry reads the JWT's exp claim, falling back to the users
// collection's token duration.
func tokenExpiry(app core.App, token string) int64 {
	if expiresAt, ok := auth.TokenExpiry(token); ok && expiresAt > 0 {
		return expiresAt
	}

	duration := int64(365 * 24 * 60 * 60)
	if users, err := app.FindCollectionByNameOrId("users"); err == nil && users.AuthToken.Duration > 0 {
		duration = users.AuthToken.Duration
	}

	return time.Now().Unix() + duration
}
