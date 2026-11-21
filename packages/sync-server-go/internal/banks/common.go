package banks

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"regexp"
	"slices"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// Options is everything the provider modules need.
type Options struct {
	Store  *store.Store
	Auth   *auth.Resolver
	Config *config.Config
}

var fileIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// bankResponse is the Node provider envelope: every provider answers 200 with
// `{status:'ok', data}` even for provider-level errors.
type bankResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data"`
}

func respond(e *core.RequestEvent, data any) error {
	return httpx.JSON(e, http.StatusOK, bankResponse{Status: "ok", Data: data})
}

// budgetSession resolves a bank-sync session. When allowAPIKey is true it
// accepts budget-scoped API keys, otherwise it requires an ordinary session.
// It returns the session, the file id whose credentials to use, and ok=false
// when a response was already written.
func (o Options) budgetSession(e *core.RequestEvent, allowAPIKey bool, credentialNames []string) (*auth.Session, string, bool) {
	var session *auth.Session
	if allowAPIKey {
		session = o.Auth.ResolveBudgetSession(e, "")
	} else {
		session = o.Auth.ResolveSession(e, "")
	}
	if session == nil {
		return nil, "", false
	}

	fileID := e.Request.Header.Get("X-Actual-File-Id")

	// An API key names one of its granted budgets; a missing or ungranted id
	// is denied before the generic checks, like validateBankSyncSession.
	if session.IsAPIKey && !slices.Contains(session.APIKeyFileIDs, fileID) {
		_ = httpx.Fail(e, http.StatusForbidden, "file-access-denied")
		return nil, "", false
	}

	if fileID != "" {
		if !fileIDPattern.MatchString(fileID) {
			_ = httpx.Fail(e, http.StatusBadRequest, "invalid-file-id")
			return nil, "", false
		}
		canAccess, err := o.Store.CanAccessActiveFile(fileID, session.UserID)
		if err != nil {
			_ = httpx.Error(e)
			return nil, "", false
		}
		if !canAccess {
			_ = httpx.Fail(e, http.StatusForbidden, "file-access-denied")
			return nil, "", false
		}
	}

	credentialFileID := ""
	if fileID != "" {
		if session.IsAPIKey {
			credentialFileID = fileID
		} else {
			for _, name := range credentialNames {
				if _, exists, err := o.Store.Secret(name, fileID); err != nil {
					_ = httpx.Error(e)
					return nil, "", false
				} else if exists {
					credentialFileID = fileID
					break
				}
			}
		}
	}

	return session, credentialFileID, true
}

func secretOrEmpty(o Options, name, fileID string) (string, error) {
	value, exists, err := o.Store.Secret(name, fileID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return value, nil
}

// validFileID is exported to the provider files.
func validFileID(id string) bool {
	return fileIDPattern.MatchString(id)
}

// bankInternalError mirrors handleError: unexpected failures are a 200 with a
// data-level INTERNAL_ERROR, not a 500.
func bankInternalError(e *core.RequestEvent, err error) error {
	message := "internal-error"
	if err != nil && err.Error() != "" {
		message = err.Error()
	}
	return respond(e, struct {
		ErrorCode string `json:"error_code"`
		ErrorType string `json:"error_type"`
	}{
		ErrorCode: "INTERNAL_ERROR",
		ErrorType: message,
	})
}

func registerStatusStub(actual *router.RouterGroup[*core.RequestEvent], opts Options, provider string) {
	actual.POST("/"+provider+"/status", func(e *core.RequestEvent) error {
		if opts.Auth.ResolveSession(e, "") == nil {
			return nil
		}
		return respond(e, struct {
			Configured bool `json:"configured"`
		}{Configured: false})
	})
}

// bindJSON mirrors routes.bindJSON for provider request bodies.
func bindJSON(e *core.RequestEvent, dst any) error {
	contentType := e.Request.Header.Get("Content-Type")
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return nil
		}
	}

	raw, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return httpx.Error(e)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
	}
	return nil
}
