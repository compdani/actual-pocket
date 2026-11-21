package routes

import (
	"errors"
	"net/http"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/router"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

type adminUserRequest struct {
	ID          string  `json:"id"`
	UserName    string  `json:"userName"`
	DisplayName string  `json:"displayName"`
	Role        string  `json:"role"`
	Enabled     bool    `json:"enabled"`
	Password    *string `json:"password"`
}

type accessRequest struct {
	FileID string `json:"fileId"`
	UserID string `json:"userId"`
}

type transferOwnershipRequest struct {
	FileID    string `json:"fileId"`
	NewUserID string `json:"newUserId"`
}

func registerAdmin(actual *router.RouterGroup[*core.RequestEvent], opts Options) {
	limit := apis.BodyLimit(int64(opts.Config.Upload.FileSizeLimitMB) << 20)

	admin := actual.Group("/admin")
	admin.Bind(limit)

	// Express matches both the slash and the non-slash form; Go's mux does
	// not, and the client uses both.
	registerBoth(admin, http.MethodGet, "/owner-created/", opts.ownerCreated)
	registerBoth(admin, http.MethodGet, "/users/", opts.adminUsers)
	registerBoth(admin, http.MethodPost, "/users/", opts.adminCreateUser)
	registerBoth(admin, http.MethodPatch, "/users/", opts.adminUpdateUser)
	registerBoth(admin, http.MethodDelete, "/users/", opts.adminDeleteUsers)
	registerBoth(admin, http.MethodGet, "/access/", opts.adminGetAccess)
	registerBoth(admin, http.MethodPost, "/access/", opts.adminAddAccess)
	registerBoth(admin, http.MethodDelete, "/access/", opts.adminDeleteAccess)
	registerBoth(admin, http.MethodGet, "/access/users/", opts.adminAccessUsers)
	registerBoth(admin, http.MethodPost, "/access/transfer-ownership/", opts.adminTransferOwnership)
}

// registerBoth binds a path with and without its trailing slash.
func registerBoth(group *router.RouterGroup[*core.RequestEvent], method, path string, action func(e *core.RequestEvent) error) {
	group.Route(method, path, action)

	trimmed := strings.TrimSuffix(path, "/")
	if trimmed != path {
		group.Route(method, trimmed, action)
	}
}

func (o Options) ownerCreated(e *core.RequestEvent) error {
	return httpx.JSON(e, http.StatusOK, o.Store.OwnerID() != "")
}

func (o Options) adminUsers(e *core.RequestEvent) error {
	if o.Auth.ResolveSession(e, "") == nil {
		return nil
	}

	users, err := o.Store.AdminUsers()
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, users)
}

func (o Options) adminCreateUser(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}
	if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}

	body := adminUserRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	if reason := validateUserInput(body); reason != "" {
		return httpx.Fail(e, http.StatusBadRequest, reason)
	}

	if o.Store.EmailTakenByAnotherUser(body.UserName, "") {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-already-exists",
			"User "+body.UserName+" already exists")
	}

	id, err := o.Store.CreateUser(store.CreateUserParams{
		Email:       body.UserName,
		DisplayName: body.DisplayName,
		Role:        body.Role,
		Enabled:     body.Enabled,
		Password:    body.Password,
	})
	if err != nil {
		if reason := fieldReason(err); reason != "" {
			return httpx.Fail(e, http.StatusBadRequest, reason)
		}
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
		ID string `json:"id"`
	}{ID: id}})
}

func (o Options) adminUpdateUser(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}
	if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}

	body := adminUserRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	if reason := validateUserInput(body); reason != "" {
		return httpx.Fail(e, http.StatusBadRequest, reason)
	}

	record, err := o.App.FindRecordById("users", body.ID)
	if err != nil {
		return httpx.FailDetails(e, http.StatusBadRequest, "cannot-find-user-to-update",
			"Cannot find user "+body.UserName+" to update")
	}

	// The owner can never be demoted or disabled.
	if record.GetBool("owner") && (body.Role != "ADMIN" || !body.Enabled) {
		return httpx.FailDetails(e, http.StatusBadRequest, "cannot-modify-owner",
			"The server owner cannot be demoted or disabled")
	}

	if o.Store.EmailTakenByAnotherUser(body.UserName, body.ID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-already-exists",
			"User "+body.UserName+" already exists")
	}

	if err := o.Store.UpdateUser(store.UpdateUserParams{
		ID:          body.ID,
		Email:       body.UserName,
		DisplayName: body.DisplayName,
		Role:        body.Role,
		Enabled:     body.Enabled,
		Password:    body.Password,
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.FailDetails(e, http.StatusBadRequest, "cannot-find-user-to-update",
				"Cannot find user "+body.UserName+" to update")
		}
		if reason := fieldReason(err); reason != "" {
			return httpx.Fail(e, http.StatusBadRequest, reason)
		}
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
		ID string `json:"id"`
	}{ID: body.ID}})
}

func (o Options) adminDeleteUsers(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}
	if !o.Store.IsAdmin(session.UserID) {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}

	body := struct {
		IDs []string `json:"ids"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	ownerID := o.Store.OwnerID()
	deleted := 0

	for _, id := range body.IDs {
		if id == "" || id == ownerID {
			continue
		}
		if !o.Store.UserExists(id) {
			continue
		}

		// Files do not cascade, so hand them to the owner before the delete.
		if ownerID == "" {
			continue
		}
		err := o.App.RunInTransaction(func(txApp core.App) error {
			txStore := o.Store.With(txApp)
			if err := txStore.TransferFiles(id, ownerID); err != nil {
				return err
			}
			if err := txStore.DeleteAllUserAccess(id); err != nil {
				return err
			}
			ok, err := txStore.DeleteUser(id)
			if err != nil || !ok {
				return errors.New("user not deleted")
			}
			return nil
		})
		if err != nil {
			continue
		}
		deleted++
	}

	if deleted == len(body.IDs) {
		return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
			SomeDeletionsFailed bool `json:"someDeletionsFailed"`
		}{SomeDeletionsFailed: false}})
	}

	return httpx.Fail(e, http.StatusBadRequest, "not-all-deleted")
}

func (o Options) adminGetAccess(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	fileID := e.Request.URL.Query().Get("fileId")
	if ok := o.requireAccessAdmin(e, fileID, session); !ok {
		return nil
	}

	users, err := o.Store.FileAccessUsers(fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
		}
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, users)
}

func (o Options) adminAddAccess(e *core.RequestEvent) error {
	body := accessRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !o.isFileOwnerOrAdmin(session, body.FileID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "file-denied",
			"You don't have permissions over this file")
	}
	if !o.Store.FileExists(body.FileID) {
		return httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
	}
	if body.UserID == "" {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-cant-be-empty", "User cannot be empty")
	}

	count, err := o.Store.CountUserAccess(body.FileID, body.UserID)
	if err != nil {
		return httpx.Error(e)
	}
	if count > 0 {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-already-have-access", "User already have access")
	}

	if err := o.Store.AddUserAccess(body.UserID, body.FileID); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct{}{}})
}

func (o Options) adminDeleteAccess(e *core.RequestEvent) error {
	fileID := e.Request.URL.Query().Get("fileId")
	body := struct {
		IDs []string `json:"ids"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !o.isFileOwnerOrAdmin(session, fileID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "file-denied",
			"You don't have permissions over this file")
	}
	if !o.Store.FileExists(fileID) {
		return httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
	}

	deleted, err := o.Store.DeleteUserAccess(body.IDs, fileID)
	if err != nil {
		return httpx.Error(e)
	}

	if deleted == len(body.IDs) {
		return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
			SomeDeletionsFailed bool `json:"someDeletionsFailed"`
		}{SomeDeletionsFailed: false}})
	}

	return httpx.Fail(e, http.StatusBadRequest, "not-all-deleted")
}

func (o Options) adminAccessUsers(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	fileID := e.Request.URL.Query().Get("fileId")
	if !o.isFileOwnerOrAdmin(session, fileID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "file-denied",
			"You don't have permissions over this file")
	}
	if !o.Store.FileExists(fileID) {
		return httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
	}

	users, err := o.Store.AllUserAccess(fileID)
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, users)
}

func (o Options) adminTransferOwnership(e *core.RequestEvent) error {
	body := transferOwnershipRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	if !o.isFileOwnerOrAdmin(session, body.FileID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "file-denied",
			"You don't have permissions over this file")
	}
	if !o.Store.FileExists(body.FileID) {
		return httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
	}
	if body.NewUserID == "" {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-cant-be-empty", "Username cannot be empty")
	}
	if !o.Store.UserExists(body.NewUserID) {
		return httpx.FailDetails(e, http.StatusBadRequest, "new-user-not-found", "New user not found")
	}

	if err := o.Store.SetFileOwner(body.FileID, body.NewUserID); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct{}{}})
}

// requireAccessAdmin is the GET /access permission check: the caller must own
// the file or be an admin.
func (o Options) requireAccessAdmin(e *core.RequestEvent, fileID string, session *auth.Session) bool {
	if !o.isFileOwnerOrAdmin(session, fileID) {
		_ = httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
		return false
	}

	if !o.Store.FileExists(fileID) {
		_ = httpx.FailDetails(e, http.StatusNotFound, "invalid-file-id", "File not found at server")
		return false
	}

	return true
}

func (o Options) isFileOwnerOrAdmin(session *auth.Session, fileID string) bool {
	if o.Store.IsAdmin(session.UserID) {
		return true
	}

	file, err := o.Store.File(fileID)
	if err != nil {
		return false
	}

	return file.Owner == session.UserID
}

// validateUserInput returns the Node error reason for a missing field.
func validateUserInput(body adminUserRequest) string {
	if body.UserName == "" {
		return "user-cant-be-empty"
	}
	if body.Role == "" {
		return "role-cant-be-empty"
	}
	if body.Role != "ADMIN" && body.Role != "BASIC" {
		return "role-does-not-exists"
	}
	return ""
}
