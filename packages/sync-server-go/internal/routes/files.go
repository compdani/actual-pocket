package routes

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/auth"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/httpx"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// fileListEntry is one item of list-user-files, in the Node server's field
// order.
type fileListEntry struct {
	Deleted         int             `json:"deleted"`
	FileID          string          `json:"fileId"`
	GroupID         *string         `json:"groupId"`
	Name            *string         `json:"name"`
	EncryptKeyID    *string         `json:"encryptKeyId"`
	Owner           string          `json:"owner"`
	UsersWithAccess []fileAccessRow `json:"usersWithAccess"`
}

type fileAccessRow struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	UserName    string `json:"userName"`
	Owner       bool   `json:"owner"`
}

type fileInfoData struct {
	Deleted         int             `json:"deleted"`
	FileID          string          `json:"fileId"`
	GroupID         *string         `json:"groupId"`
	Name            *string         `json:"name"`
	EncryptMeta     json.RawMessage `json:"encryptMeta"`
	UsersWithAccess []fileAccessRow `json:"usersWithAccess"`
}

type uploadResponse struct {
	Status  string `json:"status"`
	GroupID string `json:"groupId"`
}

type fileIDRequiredBody struct {
	Details string `json:"details"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
}

// verifyFile ports verifyFileExists: API key grants are checked before the id,
// then the file lookup. A missing file is a 400, not a 404 (NODE-PARITY: the
// Node server has a FIXME about this).
func (o Options) verifyFile(e *core.RequestEvent, fileID string, apiKeyFileIDs []string, notFoundBody any) (*store.File, bool) {
	if apiKeyFileIDs != nil && !contains(apiKeyFileIDs, fileID) {
		_ = httpx.Text(e, http.StatusForbidden, "file-access-not-allowed")
		return nil, false
	}

	if !fileIDPattern.MatchString(fileID) {
		_ = httpx.Text(e, http.StatusBadRequest, "invalid fileId")
		return nil, false
	}

	file, err := o.Store.LiveFile(fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			o.writeNotFound(e, notFoundBody)
			return nil, false
		}
		_ = httpx.Error(e)
		return nil, false
	}

	return file, true
}

func (o Options) writeNotFound(e *core.RequestEvent, body any) {
	if text, ok := body.(string); ok {
		_ = httpx.Text(e, http.StatusBadRequest, text)
		return
	}

	_ = httpx.JSON(e, http.StatusBadRequest, body)
}

// requireFileAccess ports requireFileAccess: owner, server admin, or an
// explicit grant.
func (o Options) requireFileAccess(e *core.RequestEvent, file *store.File, session *auth.Session) bool {
	if file.Owner == session.UserID || o.Store.IsAdmin(session.UserID) {
		return true
	}

	count, err := o.Store.CountUserAccess(file.ID, session.UserID)
	if err != nil {
		_ = httpx.Error(e)
		return false
	}
	if count > 0 {
		return true
	}

	_ = httpx.Text(e, http.StatusForbidden, "file-access-not-allowed")
	return false
}

// requireFileOwner is requireFileAccess without the sharing grants.
func (o Options) requireFileOwner(e *core.RequestEvent, file *store.File, session *auth.Session) bool {
	if file.Owner == session.UserID || o.Store.IsAdmin(session.UserID) {
		return true
	}

	_ = httpx.Text(e, http.StatusForbidden, "file-access-not-allowed")
	return false
}

func (o Options) userGetKey(e *core.RequestEvent) error {
	body := syncFileRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveBudgetSession(e, body.Token)
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, body.FileID, session.APIKeyFileIDs, "file-not-found")
	if !ok {
		return nil
	}
	if !o.requireFileAccess(e, file, session) {
		return nil
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{
		Status: "ok",
		Data: struct {
			ID   *string `json:"id"`
			Salt *string `json:"salt"`
			Test *string `json:"test"`
		}{ID: file.EncryptKeyID, Salt: file.EncryptSalt, Test: file.EncryptTest},
	})
}

func (o Options) userCreateKey(e *core.RequestEvent) error {
	body := createKeyRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, body.Token)
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, body.FileID, nil, "file-not-found")
	if !ok {
		return nil
	}
	if !o.requireFileOwner(e, file, session) {
		return nil
	}

	update := store.FileUpdate{}
	if body.KeyID != nil {
		update.EncryptKeyID = store.Value(*body.KeyID)
	}
	if body.KeySalt != nil {
		update.EncryptSalt = store.Value(*body.KeySalt)
	}
	if body.TestContent != nil {
		update.EncryptTest = store.Value(*body.TestContent)
	}

	if _, err := o.Store.PutFile(file.ID, update, nil); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

func (o Options) resetUserFile(e *core.RequestEvent) error {
	body := syncFileRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, body.Token)
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, body.FileID, nil, "User or file not found")
	if !ok {
		return nil
	}
	if !o.requireFileOwner(e, file, session) {
		return nil
	}

	groupID := ""
	if file.GroupID != nil {
		groupID = *file.GroupID
	}

	if _, err := o.Store.PutFile(file.ID, store.FileUpdate{GroupID: store.Null[string]()}, nil); err != nil {
		return httpx.Error(e)
	}

	if groupID != "" {
		// A missing group file is fine; the next upload starts a new group.
		_ = o.Sync.Delete(groupID)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

func (o Options) uploadUserFile(e *core.RequestEvent) error {
	names := e.Request.Header.Values("X-Actual-Name")
	if len(names) != 1 {
		return httpx.Text(e, http.StatusBadRequest, "single x-actual-name is required")
	}
	name, err := url.PathUnescape(names[0])
	if err != nil {
		return httpx.Error(e)
	}

	fileIDs := e.Request.Header.Values("X-Actual-File-Id")
	if len(fileIDs) != 1 || fileIDs[0] == "" {
		return httpx.Text(e, http.StatusBadRequest, "fileId is required")
	}
	fileID := fileIDs[0]
	if !fileIDPattern.MatchString(fileID) {
		return httpx.Text(e, http.StatusBadRequest, "invalid fileId")
	}

	groupHeader := e.Request.Header.Values("X-Actual-Group-Id")
	var groupID *string
	if len(groupHeader) > 0 {
		if len(groupHeader) != 1 || !fileIDPattern.MatchString(groupHeader[0]) {
			return httpx.Text(e, http.StatusBadRequest, "invalid groupId")
		}
		groupID = &groupHeader[0]
	}

	encryptMeta, ok, err := extractSingleHeader(e, "X-Actual-Encrypt-Meta")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	syncFormat, ok, err := extractSingleHeader(e, "X-Actual-Format")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	keyID := ""
	if encryptMeta != nil {
		var meta struct {
			KeyID string `json:"keyId"`
		}
		if err := json.Unmarshal([]byte(*encryptMeta), &meta); err != nil {
			return httpx.Error(e)
		}
		keyID = meta.KeyID
	}

	session := o.Auth.ResolveSession(e, "")
	if session == nil {
		return nil
	}

	currentFile, err := o.Store.LiveFile(fileID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return httpx.Error(e)
		}
		currentFile = nil
	}

	if currentFile != nil && !o.requireFileAccess(e, currentFile, session) {
		return nil
	}

	requestGroupID := ""
	if groupID != nil {
		requestGroupID = *groupID
	}
	if validationError := validateUploadedFile(requestGroupID, keyID, currentFile); validationError != "" {
		return httpx.Text(e, http.StatusBadRequest, validationError)
	}

	body, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return httpx.Error(e)
	}

	blob, err := filesystem.NewFileFromBytes(body, fileID+".blob")
	if err != nil {
		return httpx.JSON(e, http.StatusInternalServerError, struct {
			Status string `json:"status"`
		}{Status: "error"})
	}

	update := store.FileUpdate{
		Name:        store.Value(name),
		EncryptMeta: nullableString(encryptMeta),
		SyncVersion: nullableVersion(syncFormat),
	}

	if currentFile == nil {
		// A new budget: Node always gives it a fresh group.
		newGroupID := store.NewGroupID()
		update.GroupID = store.Value(newGroupID)
		update.Owner = store.Value(session.UserID)
		update.Deleted = store.Value(false)
		groupID = &newGroupID

		if _, err := o.Store.PutFile(fileID, update, blob); err != nil {
			return httpx.JSON(e, http.StatusInternalServerError, struct {
				Status string `json:"status"`
			}{Status: "error"})
		}

		return httpx.JSON(e, http.StatusOK, uploadResponse{Status: "ok", GroupID: newGroupID})
	}

	if groupID == nil {
		// The sync state was reset: start a new group with this upload.
		newGroupID := store.NewGroupID()
		update.GroupID = store.Value(newGroupID)
		groupID = &newGroupID
	}

	if _, err := o.Store.PutFile(fileID, update, blob); err != nil {
		return httpx.JSON(e, http.StatusInternalServerError, struct {
			Status string `json:"status"`
		}{Status: "error"})
	}

	return httpx.JSON(e, http.StatusOK, uploadResponse{Status: "ok", GroupID: *groupID})
}

func (o Options) downloadUserFile(e *core.RequestEvent) error {
	fileIDs := e.Request.Header.Values("X-Actual-File-Id")
	if len(fileIDs) != 1 || fileIDs[0] == "" {
		return httpx.Text(e, http.StatusBadRequest, "Single file ID is required")
	}
	fileID := fileIDs[0]
	if !fileIDPattern.MatchString(fileID) {
		return httpx.Text(e, http.StatusBadRequest, "invalid fileId")
	}

	session := o.Auth.ResolveBudgetSession(e, "")
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, fileID, session.APIKeyFileIDs, "User or file not found")
	if !ok {
		return nil
	}
	if !o.requireFileAccess(e, file, session) {
		return nil
	}

	record, err := o.Store.FileRecord(file.ID)
	if err != nil {
		return httpx.Error(e)
	}

	blobName := record.GetString("blob")
	if blobName == "" {
		return httpx.Text(e, http.StatusNotFound, "file-not-found")
	}

	system, err := o.App.NewFilesystem()
	if err != nil {
		return httpx.Error(e)
	}
	defer system.Close()

	reader, err := system.GetReader(record.BaseFilesPath() + "/" + blobName)
	if err != nil {
		return httpx.Text(e, http.StatusNotFound, "file-not-found")
	}
	defer reader.Close()

	e.Response.Header().Set("Content-Disposition", "attachment;filename="+fileID)
	e.Response.Header().Set("Content-Type", "application/octet-stream")
	e.Response.Header().Set("Content-Length", strconv.FormatInt(reader.Size(), 10))
	e.Response.WriteHeader(http.StatusOK)

	_, err = io.Copy(e.Response, reader)
	return err
}

func (o Options) updateUserFilename(e *core.RequestEvent) error {
	body := updateFilenameRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	session := o.Auth.ResolveSession(e, body.Token)
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, body.FileID, nil, "file-not-found")
	if !ok {
		return nil
	}
	if !o.requireFileAccess(e, file, session) {
		return nil
	}

	update := store.FileUpdate{}
	if body.Name.Set {
		update.Name = body.Name
	}
	if _, err := o.Store.PutFile(file.ID, update, nil); err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

func (o Options) listUserFiles(e *core.RequestEvent) error {
	session := o.Auth.ResolveBudgetSession(e, "")
	if session == nil {
		return nil
	}

	files, err := o.Store.ListFiles(session.UserID, 1000)
	if err != nil {
		return httpx.Error(e)
	}

	entries := make([]fileListEntry, 0, len(files))
	for _, file := range files {
		if session.APIKeyFileIDs != nil && !contains(session.APIKeyFileIDs, file.ID) {
			continue
		}

		users, err := o.Store.FileUsers(file.ID)
		if err != nil {
			return httpx.Error(e)
		}

		rows := make([]fileAccessRow, 0, len(users))
		for _, user := range users {
			rows = append(rows, fileAccessRow{
				UserID:      user.UserID,
				DisplayName: user.DisplayName,
				UserName:    user.UserName,
				Owner:       user.UserID == file.Owner,
			})
		}

		entries = append(entries, fileListEntry{
			Deleted:         0,
			FileID:          file.ID,
			GroupID:         file.GroupID,
			Name:            file.Name,
			EncryptKeyID:    file.EncryptKeyID,
			Owner:           file.Owner,
			UsersWithAccess: rows,
		})
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{Status: "ok", Data: entries})
}

func (o Options) getUserFileInfo(e *core.RequestEvent) error {
	session := o.Auth.ResolveBudgetSession(e, "")
	if session == nil {
		return nil
	}

	fileIDs := e.Request.Header.Values("X-Actual-File-Id")
	fileID := ""
	if len(fileIDs) == 1 {
		fileID = fileIDs[0]
	}

	file, ok := o.verifyFile(e, fileID, session.APIKeyFileIDs, httpx.ErrorBody{
		Status: "error",
		Reason: "file-not-found",
	})
	if !ok {
		return nil
	}
	if !o.requireFileAccess(e, file, session) {
		return nil
	}

	users, err := o.Store.FileUsers(file.ID)
	if err != nil {
		return httpx.Error(e)
	}

	rows := make([]fileAccessRow, 0, len(users))
	for _, user := range users {
		rows = append(rows, fileAccessRow{
			UserID:      user.UserID,
			DisplayName: user.DisplayName,
			UserName:    user.UserName,
			Owner:       user.UserID == file.Owner,
		})
	}

	encryptMeta := json.RawMessage("null")
	if file.EncryptMeta != nil {
		encryptMeta = json.RawMessage(*file.EncryptMeta)
	}

	return httpx.JSON(e, http.StatusOK, okDataResponse{
		Status: "ok",
		Data: fileInfoData{
			Deleted:         0,
			FileID:          file.ID,
			GroupID:         file.GroupID,
			Name:            file.Name,
			EncryptMeta:     encryptMeta,
			UsersWithAccess: rows,
		},
	})
}

func (o Options) deleteUserFile(e *core.RequestEvent) error {
	body := syncFileRequest{}
	if err := bindJSON(e, &body); err != nil {
		return err
	}

	if body.FileID == "" {
		return httpx.JSON(e, http.StatusUnprocessableEntity, fileIDRequiredBody{
			Details: "fileId-required",
			Reason:  "unprocessable-entity",
			Status:  "error",
		})
	}

	session := o.Auth.ResolveSession(e, body.Token)
	if session == nil {
		return nil
	}

	file, ok := o.verifyFile(e, body.FileID, nil, "file-not-found")
	if !ok {
		return nil
	}
	if !o.requireFileOwner(e, file, session) {
		return nil
	}

	// Soft delete: keep the blob and the group database so a restore is
	// possible, and clear only this budget's bank sync secrets.
	err := o.App.RunInTransaction(func(txApp core.App) error {
		txStore := o.Store.With(txApp)

		if _, err := txStore.PutFile(file.ID, store.FileUpdate{Deleted: store.Value(true)}, nil); err != nil {
			return err
		}

		for _, name := range store.SecretNames {
			if err := txStore.ResetSecret(name, file.ID); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return httpx.Error(e)
	}

	return httpx.JSON(e, http.StatusOK, okResponse{Status: "ok"})
}

// validateUploadedFile is the upload half of app-sync/validation.ts.
func validateUploadedFile(groupID, keyID string, file *store.File) string {
	if file == nil {
		return ""
	}

	// An unset group id and an empty one are the same here, matching JS null
	// comparison.
	currentGroupID := ""
	if file.GroupID != nil {
		currentGroupID = *file.GroupID
	}
	if groupID != currentGroupID {
		return "file-has-reset"
	}

	registeredKeyID := ""
	if file.EncryptKeyID != nil {
		registeredKeyID = *file.EncryptKeyID
	}
	if keyID != registeredKeyID {
		return "file-has-new-key"
	}

	return ""
}

// extractSingleHeader mirrors Node's extractSingleHeader: a repeated header is
// a 400 naming the (lowercase) key, and the response is already written.
func extractSingleHeader(e *core.RequestEvent, key string) (*string, bool, error) {
	values := e.Request.Header.Values(key)
	if len(values) == 0 {
		return nil, true, nil
	}
	if len(values) > 1 {
		return nil, false, httpx.Text(e, http.StatusBadRequest,
			"Duplicate headers encountered for key "+strings.ToLower(key))
	}

	return &values[0], true, nil
}

func nullableString(value *string) store.Nullable[string] {
	if value == nil {
		return store.Null[string]()
	}
	return store.Value(*value)
}

func nullableVersion(value *string) store.Nullable[float64] {
	if value == nil || *value == "" {
		return store.Null[float64]()
	}

	parsed, err := strconv.ParseFloat(*value, 64)
	if err != nil {
		return store.Null[float64]()
	}

	return store.Value(parsed)
}

type syncFileRequest struct {
	Token  string `json:"token"`
	FileID string `json:"fileId"`
}

type createKeyRequest struct {
	Token       string  `json:"token"`
	FileID      string  `json:"fileId"`
	KeyID       *string `json:"keyId"`
	KeySalt     *string `json:"keySalt"`
	TestContent *string `json:"testContent"`
}

type updateFilenameRequest struct {
	Token  string
	FileID string
	Name   store.Nullable[string]
}

// UnmarshalJSON keeps "name" absent distinct from "name": null.
func (r *updateFilenameRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Token  string          `json:"token"`
		FileID string          `json:"fileId"`
		Name   json.RawMessage `json:"name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.Token = raw.Token
	r.FileID = raw.FileID

	if len(raw.Name) > 0 {
		if string(raw.Name) == "null" {
			r.Name = store.Null[string]()
		} else {
			var name string
			if err := json.Unmarshal(raw.Name, &name); err != nil {
				return err
			}
			r.Name = store.Value(name)
		}
	}

	return nil
}
