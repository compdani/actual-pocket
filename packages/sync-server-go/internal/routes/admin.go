package routes

import (
	"encoding/json"
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
	DisplayName *string `json:"displayName"`
	Role        string  `json:"role"`
	Enabled     *bool   `json:"enabled"`
	Password    *string `json:"password"`
}

// bindAdminUser builds the rule body from the same values the save uses.
func bindAdminUser(e *core.RequestEvent, dst *adminUserRequest) (map[string]any, error) {
	raw, ok, err := readJSONPayload(e)
	if err != nil || !ok {
		return nil, err
	}

	fields := map[string]any{}
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, dst) != nil {
		return nil, httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
	}

	body := map[string]any{"email": dst.UserName, "role": dst.Role}
	if dst.DisplayName != nil {
		body["name"] = *dst.DisplayName
	}
	if dst.Enabled != nil {
		body["enabled"] = *dst.Enabled
	}
	if dst.Password != nil {
		body["password"] = *dst.Password
	}

	for key, value := range fields {
		switch strings.ToLower(key) {
		case "owner", store.FieldPasswordSet:
			canonical := strings.ToLower(key)
			if _, exists := body[canonical]; exists {
				return nil, httpx.Fail(e, http.StatusBadRequest, "invalid-request-body")
			}
			body[canonical] = value
		}
	}
	return body, nil
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

	body := adminUserRequest{}
	ruleBody, err := bindAdminUser(e, &body)
	if err != nil || e.Written() {
		return err
	}

	if reason := validateUserInput(body); reason != "" {
		return httpx.Fail(e, http.StatusBadRequest, reason)
	}

	if o.Store.EmailTakenByAnotherUser(body.UserName, "") {
		return httpx.FailDetails(e, http.StatusBadRequest, "user-already-exists",
			"User "+body.UserName+" already exists")
	}

	collection, err := o.App.FindCachedCollectionByNameOrId("users")
	if err != nil {
		return httpx.Error(e)
	}
	requestInfo := &core.RequestInfo{
		Auth:   session.Record,
		Body:   ruleBody,
		Method: http.MethodPost,
	}

	// PocketBase checks the create rule on the saved record, so do the same
	// inside one transaction and roll back when it denies.
	created, denyErr := o.createUserRecord(body, requestInfo, collection)
	if errors.Is(denyErr, errUserRuleDenied) {
		return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
	}
	if denyErr != nil {
		if reason := fieldReason(denyErr); reason != "" {
			return httpx.Fail(e, http.StatusBadRequest, reason)
		}
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: struct {
		ID string `json:"id"`
	}{ID: created.Id}})
}

// errUserRuleDenied unwinds the create transaction when the create rule denies
// the new record.
var errUserRuleDenied = errors.New("user rule denied")

func (o Options) createUserRecord(body adminUserRequest, requestInfo *core.RequestInfo, collection *core.Collection) (*core.Record, error) {
	var created *core.Record

	err := o.App.RunInTransaction(func(txApp core.App) error {
		var displayName string
		if body.DisplayName != nil {
			displayName = *body.DisplayName
		}
		record, err := o.Store.With(txApp).CreateUser(store.CreateUserParams{
			Email:       body.UserName,
			DisplayName: displayName,
			Role:        body.Role,
			Enabled:     body.Enabled != nil && *body.Enabled,
			Password:    body.Password,
		})
		if err != nil {
			return err
		}

		allowed, err := txApp.CanAccessRecord(record, requestInfo, collection.CreateRule)
		if err != nil {
			return err
		}
		if !allowed {
			return errUserRuleDenied
		}

		created = record

		return nil
	})

	return created, err
}

func (o Options) adminUpdateUser(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	body := adminUserRequest{}
	ruleBody, err := bindAdminUser(e, &body)
	if err != nil || e.Written() {
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

	collection := record.Collection()
	requestInfo := &core.RequestInfo{
		Auth:   session.Record,
		Body:   ruleBody,
		Method: http.MethodPatch,
	}

	// The rules run against the record as it currently is, exactly like
	// PocketBase's own update endpoint (the :changed modifier compares the
	// request body with these values).
	allowed, err := o.App.CanAccessRecord(record, requestInfo, collection.UpdateRule)
	if err != nil {
		return httpx.Error(e)
	}
	if !allowed {
		return o.userDenied(e, record)
	}

	if body.UserName != record.Email() || body.Password != nil {
		if record.Id == session.UserID {
			return httpx.FailDetails(e, http.StatusForbidden, "forbidden",
				"Use /account/change-password to change your password or PocketBase's email confirmation flow to change your email")
		}
		allowed, err := o.App.CanAccessRecord(record, requestInfo, collection.ManageRule)
		if err != nil {
			return httpx.Error(e)
		}
		if !allowed {
			return o.userDenied(e, record)
		}
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
		if errors.Is(err, store.ErrOwnerInvariant) {
			return httpx.FailDetails(e, http.StatusBadRequest, "cannot-modify-owner", err.Error())
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

// userDenied writes the forbid response, keeping the friendlier reason for the
// owner.
func (o Options) userDenied(e *core.RequestEvent, record *core.Record) error {
	if record.GetBool("owner") {
		return httpx.FailDetails(e, http.StatusForbidden, "cannot-modify-owner",
			"The server owner cannot be changed this way")
	}

	return httpx.FailDetails(e, http.StatusForbidden, "forbidden", "permission-not-found")
}

func (o Options) adminDeleteUsers(e *core.RequestEvent) error {
	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	body := struct {
		IDs []string `json:"ids"`
	}{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	collection, err := o.App.FindCachedCollectionByNameOrId("users")
	if err != nil {
		return httpx.Error(e)
	}
	requestInfo := &core.RequestInfo{
		Auth:   session.Record,
		Body:   map[string]any{},
		Method: http.MethodDelete,
	}

	deleted := 0

	for _, id := range body.IDs {
		if id == "" {
			continue
		}

		record, err := o.App.FindRecordById("users", id)
		if err != nil {
			continue
		}

		allowed, err := o.App.CanAccessRecord(record, requestInfo, collection.DeleteRule)
		if err != nil {
			return httpx.Error(e)
		}
		if !allowed {
			return o.userDenied(e, record)
		}

		// The delete hook moves this user's files to the owner.
		if err := o.App.Delete(record); err != nil {
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
