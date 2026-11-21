package routes

import (
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

type secretRequest struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func registerSecrets(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	limit := apis.BodyLimit(int64(opts.Config.Upload.FileSizeLimitMB) << 20)

	secret := actual.Group("/secret")
	secret.Bind(limit)
	secret.POST("", opts.setSecret)
	secret.GET("/{name}", opts.getSecret)
	secret.DELETE("/{name}", opts.deleteSecret)
}

func (o Options) setSecret(e *core.RequestEvent) error {
	body := secretRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !store.IsSecretName(body.Name) {
		return httpx.FailDetails(e, http.StatusBadRequest, "invalid-secret-name", "Unknown secret name")
	}

	fileID := e.Request.Header.Get("X-Actual-File-Id")
	if fileID != "" {
		if !fileIDPattern.MatchString(fileID) {
			return httpx.FailDetails(e, http.StatusBadRequest, "invalid-file-id", "invalid fileId")
		}
		if !o.canManageFileSecrets(session, fileID) {
			return httpx.FailDetails(e, http.StatusForbidden, "file-access-denied",
				"You don't have permissions over this file")
		}
	} else if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "not-admin",
			"You have to be admin to manage global secrets")
	}

	if err := o.Store.SetSecret(body.Name, fileID, body.Value); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

func (o Options) getSecret(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	name := e.Request.PathValue("name")
	if !store.IsSecretName(name) {
		return httpx.Text(e, http.StatusNotFound, "key not found")
	}

	fileID := e.Request.Header.Get("X-Actual-File-Id")
	if fileID != "" {
		if !fileIDPattern.MatchString(fileID) {
			return httpx.FailDetails(e, http.StatusBadRequest, "invalid-file-id", "invalid fileId")
		}
		if !o.canManageFileSecrets(session, fileID) {
			return httpx.FailDetails(e, http.StatusForbidden, "file-access-denied",
				"You don't have permissions over this file")
		}
	} else if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "not-admin",
			"You have to be admin to manage global secrets")
	}

	_, exists, err := o.Store.Secret(name, fileID)
	if err != nil {
		return httpx.Error(e)
	}
	if !exists {
		return httpx.Text(e, http.StatusNotFound, "key not found")
	}

	// The value itself is never returned; 204 means "the secret is set".
	return e.NoContent(http.StatusNoContent)
}

func (o Options) deleteSecret(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	name := e.Request.PathValue("name")
	if !store.IsSecretName(name) {
		return httpx.Text(e, http.StatusNotFound, "key not found")
	}

	fileID := e.Request.Header.Get("X-Actual-File-Id")
	if fileID != "" {
		if !fileIDPattern.MatchString(fileID) {
			return httpx.FailDetails(e, http.StatusBadRequest, "invalid-file-id", "invalid fileId")
		}
		if !o.canManageFileSecrets(session, fileID) {
			return httpx.FailDetails(e, http.StatusForbidden, "file-access-denied",
				"You don't have permissions over this file")
		}
	} else if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "not-admin",
			"You have to be admin to manage global secrets")
	}

	if err := o.Store.ResetSecret(name, fileID); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

// canManageFileSecrets is admin or the budget's owner.
func (o Options) canManageFileSecrets(session *auth.Session, fileID string) bool {
	if o.Store.IsAdmin(session.UserID) {
		return true
	}

	file, err := o.Store.File(fileID)
	if err != nil {
		return false
	}

	return file.Owner == session.UserID
}
