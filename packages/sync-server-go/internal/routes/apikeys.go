package routes

import (
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
)

type createAPIKeyRequest struct {
	Name    string   `json:"name"`
	FileIDs []string `json:"fileIds"`
}

func registerAPIKeys(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	limit := apis.BodyLimit(int64(opts.Config.Upload.FileSizeLimitMB) << 20)

	keys := actual.Group("/api-keys")
	keys.Bind(limit)
	keys.POST("", opts.createAPIKey)
	keys.GET("", opts.listAPIKeys)
	keys.DELETE("/{id}", opts.deleteAPIKey)
}

func (o Options) createAPIKey(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")

	body := createAPIKeyRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !validAPIKeyRequest(body) {
		return httpx.Fail(e, http.StatusBadRequest, "invalid-api-key")
	}

	for _, fileID := range body.FileIDs {
		canAccess, err := o.Store.CanAccessActiveFile(fileID, session.UserID)
		if err != nil {
			return httpx.Error(e)
		}
		if !canAccess {
			return httpx.Fail(e, http.StatusForbidden, "file-access-denied")
		}
	}

	key, secret, err := o.Store.CreateAPIKey(session.UserID, body.Name, body.FileIDs)
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusCreated, okDataResponse{
		Status: "ok",
		Data: struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			FileIDs   []string `json:"fileIds"`
			CreatedAt int64    `json:"createdAt"`
			Key       string   `json:"key"`
		}{
			ID:        key.ID,
			Name:      key.Name,
			FileIDs:   key.FileIDs,
			CreatedAt: key.CreatedAt,
			Key:       secret,
		},
	})
}

func (o Options) listAPIKeys(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	keys, err := o.Store.APIKeys(session.UserID)
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: keys})
}

func (o Options) deleteAPIKey(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	deleted, err := o.Store.DeleteAPIKey(session.UserID, e.Request.PathValue("id"))
	if err != nil {
		return httpx.Error(e)
	}
	if !deleted {
		return httpx.Fail(e, http.StatusNotFound, "api-key-not-found")
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

// validAPIKeyRequest mirrors the checks in app-api-keys.ts.
func validAPIKeyRequest(body createAPIKeyRequest) bool {
	if body.Name == "" || len(body.Name) > 100 {
		return false
	}
	if len(body.FileIDs) == 0 {
		return false
	}

	seen := make(map[string]bool, len(body.FileIDs))
	for _, fileID := range body.FileIDs {
		if !fileIDPattern.MatchString(fileID) {
			return false
		}
		if seen[fileID] {
			return false
		}
		seen[fileID] = true
	}

	return true
}
