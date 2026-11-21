package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
)

// --- helpers -------------------------------------------------------------

func (h *harness) groupFilePath(groupID string) string {
	return filepath.Join(h.engine.Dir(), "group-"+groupID+".sqlite")
}

// --- user-get-key --------------------------------------------------------

func TestUserGetKey(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "file-1", KeyID: ptr("key-id"), EncryptSalt: ptr("salt"), EncryptTest: ptr("test"),
		SyncVersion: ptr(2.0), Owner: h.admin.Id,
	})

	response := h.postJSON(t, "/sync/user-get-key", map[string]string{"fileId": "file-1"}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	data := jsonBody(t, response)["data"].(map[string]any)
	if data["id"] != "key-id" || data["salt"] != "salt" || data["test"] != "test" {
		t.Errorf("data = %v", data)
	}
}

func TestUserGetKeyErrors(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "other-file", KeyID: ptr("key-id"), SyncVersion: ptr(2.0), Owner: h.other.Id,
	})

	cases := []struct {
		name     string
		token    string
		fileID   string
		wantCode int
		wantBody string
	}{
		{"unauthenticated", "", "file-1", 401, ""},
		{"not found", h.adminToken, "missing", 400, "file-not-found"},
		{"invalid id", h.adminToken, "budget@2026", 400, "invalid fileId"},
		{"not owner", h.userToken, "other-file", 403, "file-access-not-allowed"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.postJSON(t, "/sync/user-get-key", map[string]string{"fileId": testCase.fileID}, testCase.token)
			if response.Code != testCase.wantCode {
				t.Fatalf("status = %d, want %d", response.Code, testCase.wantCode)
			}
			if testCase.wantBody != "" && response.Body.String() != testCase.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), testCase.wantBody)
			}
		})
	}

	// An admin may read another user's key.
	if response := h.postJSON(t, "/sync/user-get-key", map[string]string{"fileId": "other-file"}, h.adminToken); response.Code != 200 {
		t.Errorf("admin status = %d", response.Code)
	}
}

// --- user-create-key -----------------------------------------------------

func TestUserCreateKey(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "old-key", h.admin.Id)

	response := h.postJSON(t, "/sync/user-create-key", map[string]string{
		"fileId":      "file-1",
		"keyId":       "new-key",
		"keySalt":     "new-salt",
		"testContent": "new-test",
	}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	record := h.fileRecord(t, "file-1")
	if record.GetString("encrypt_keyid") != "new-key" || record.GetString("encrypt_salt") != "new-salt" || record.GetString("encrypt_test") != "new-test" {
		t.Errorf("record not updated: %v", record)
	}
}

func TestUserCreateKeyDenied(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	// A shared user has access but is not the owner.
	response := h.postJSON(t, "/sync/user-create-key", map[string]string{
		"fileId": "file-1", "keyId": "x",
	}, h.userToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}

	// An admin may do it.
	response = h.postJSON(t, "/sync/user-create-key", map[string]string{
		"fileId": "file-1", "keyId": "admin-key",
	}, h.adminToken)
	if response.Code != 200 {
		t.Errorf("admin status = %d, body = %q", response.Code, response.Body.String())
	}
}

// --- reset-user-file -----------------------------------------------------

func TestResetUserFile(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	// Create the group database, then reset.
	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	if response := h.postSync(t, request, h.adminToken); response.Code != 200 {
		t.Fatalf("sync status = %d", response.Code)
	}
	if _, err := os.Stat(h.groupFilePath("group-1")); err != nil {
		t.Fatalf("group database missing: %v", err)
	}

	response := h.postJSON(t, "/sync/reset-user-file", map[string]string{"fileId": "file-1"}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	record := h.fileRecord(t, "file-1")
	if record.GetString("group_id") != "" {
		t.Errorf("group_id = %q, want empty", record.GetString("group_id"))
	}
	if _, err := os.Stat(h.groupFilePath("group-1")); !os.IsNotExist(err) {
		t.Errorf("group database still exists: %v", err)
	}

	// Resetting again is fine (the file has no group).
	if response := h.postJSON(t, "/sync/reset-user-file", map[string]string{"fileId": "file-1"}, h.adminToken); response.Code != 200 {
		t.Errorf("second reset status = %d", response.Code)
	}
}

func TestResetUserFileErrors(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	missing := h.postJSON(t, "/sync/reset-user-file", map[string]string{"fileId": "missing"}, h.adminToken)
	if missing.Code != 400 || missing.Body.String() != "User or file not found" {
		t.Errorf("missing = %d %q", missing.Code, missing.Body.String())
	}

	shared := h.postJSON(t, "/sync/reset-user-file", map[string]string{"fileId": "file-1"}, h.userToken)
	if shared.Code != http.StatusForbidden {
		t.Errorf("shared user status = %d, want 403", shared.Code)
	}

	admin := h.postJSON(t, "/sync/reset-user-file", map[string]string{"fileId": "file-1"}, h.adminToken)
	if admin.Code != 200 {
		t.Errorf("admin status = %d, body = %q", admin.Code, admin.Body.String())
	}
}

// --- upload --------------------------------------------------------------

func TestUploadValidation(t *testing.T) {
	h := newHarness(t)

	cases := []struct {
		name     string
		params   uploadParams
		wantCode int
		wantBody string
	}{
		{
			name:     "missing name",
			params:   uploadParams{FileID: "file-1", Token: h.adminToken, Content: []byte("x")},
			wantCode: 400,
			wantBody: "single x-actual-name is required",
		},
		{
			name:     "missing file id",
			params:   uploadParams{Name: "budget", Token: h.adminToken, Content: []byte("x")},
			wantCode: 400,
			wantBody: "fileId is required",
		},
		{
			name:     "invalid file id",
			params:   uploadParams{FileID: "budget@2026", Name: "budget", Token: h.adminToken, Content: []byte("x")},
			wantCode: 400,
			wantBody: "invalid fileId",
		},
		{
			name:     "invalid group id",
			params:   uploadParams{FileID: "file-1", Name: "budget", Token: h.adminToken, HasGroupID: true, GroupID: "group@2026", Content: []byte("x")},
			wantCode: 400,
			wantBody: "invalid groupId",
		},
		{
			name:     "empty group id",
			params:   uploadParams{FileID: "file-1", Name: "budget", Token: h.adminToken, HasGroupID: true, GroupID: "", Content: []byte("x")},
			wantCode: 400,
			wantBody: "invalid groupId",
		},
		{
			name: "duplicate encrypt meta",
			params: uploadParams{
				FileID: "file-1", Name: "budget", Token: h.adminToken,
				EncryptMeta: `{"keyId":"k"}`, Duplicate: true, Content: []byte("x"),
			},
			wantCode: 400,
			wantBody: "Duplicate headers encountered for key x-actual-encrypt-meta",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.upload(t, testCase.params)
			if response.Code != testCase.wantCode {
				t.Fatalf("status = %d, want %d", response.Code, testCase.wantCode)
			}
			if got := response.Body.String(); got != testCase.wantBody {
				t.Errorf("body = %q, want %q", got, testCase.wantBody)
			}
		})
	}
}

func TestUploadNewFile(t *testing.T) {
	h := newHarness(t)

	response := h.upload(t, uploadParams{
		FileID:      "file-1",
		Name:        "My Budget",
		Format:      "2",
		EncryptMeta: `{"keyId":"key-id"}`,
		Content:     []byte("blob content"),
		Token:       h.adminToken,
	})
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	body := jsonBody(t, response)
	groupID, _ := body["groupId"].(string)
	if body["status"] != "ok" || groupID == "" {
		t.Fatalf("body = %v", body)
	}

	record := h.fileRecord(t, "file-1")
	if record.GetString("group_id") != groupID {
		t.Errorf("group_id = %q, want %q", record.GetString("group_id"), groupID)
	}
	if record.GetString("name") != "My Budget" {
		t.Errorf("name = %q", record.GetString("name"))
	}
	if record.GetString("encrypt_meta") != `{"keyId":"key-id"}` {
		t.Errorf("encrypt_meta = %q", record.GetString("encrypt_meta"))
	}
	if record.GetInt("sync_version") != 2 {
		t.Errorf("sync_version = %d", record.GetInt("sync_version"))
	}
	if record.GetString("owner") != h.admin.Id {
		t.Errorf("owner = %q", record.GetString("owner"))
	}
	if record.GetString("blob") == "" {
		t.Error("blob was not stored")
	}

	// The blob round-trips.
	download := h.download(t, "file-1", h.adminToken)
	if download.Code != 200 || download.Body.String() != "blob content" {
		t.Errorf("download = %d %q", download.Code, download.Body.String())
	}
	if got := download.Header().Get("Content-Disposition"); got != "attachment;filename=file-1" {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := download.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestUploadUpdatesExistingFile(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "file-1", KeyID: ptr("key-id"), EncryptMeta: ptr(`{"keyId":"key-id"}`),
		SyncVersion: ptr(1.0), Owner: h.admin.Id, Name: "Old",
	})

	// No group header: the sync state was reset, so a new group is assigned.
	response := h.upload(t, uploadParams{
		FileID:      "file-1",
		Name:        "New",
		Format:      "2",
		EncryptMeta: `{"keyId":"key-id","sentinel":1}`,
		Content:     []byte("new content"),
		Token:       h.adminToken,
	})
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	groupID, _ := jsonBody(t, response)["groupId"].(string)
	record := h.fileRecord(t, "file-1")
	if record.GetString("group_id") != groupID {
		t.Errorf("group_id = %q, want %q", record.GetString("group_id"), groupID)
	}
	if record.GetString("name") != "New" {
		t.Errorf("name = %q", record.GetString("name"))
	}
	if record.GetInt("sync_version") != 2 {
		t.Errorf("sync_version = %d", record.GetInt("sync_version"))
	}

	if download := h.download(t, "file-1", h.adminToken); download.Body.String() != "new content" {
		t.Errorf("blob = %q", download.Body.String())
	}
}

func TestUploadKeepsTheGroupWhenProvided(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	response := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "group-1",
		Name: "Renamed", Format: "2", EncryptMeta: `{"keyId":"key-id"}`,
		Content: []byte("fresh"), Token: h.adminToken,
	})
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := jsonBody(t, response)["groupId"]; got != "group-1" {
		t.Errorf("groupId = %v, want group-1", got)
	}
}

func TestUploadValidationErrors(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "file-1", GroupID: ptr("current-group"), KeyID: ptr("current-key"),
		EncryptMeta: ptr(`{"keyId":"current-key"}`), SyncVersion: ptr(2.0), Owner: h.admin.Id,
	})

	oldGroup := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "old-group",
		Name: "x", Format: "2", Content: []byte("x"), Token: h.adminToken,
	})
	if oldGroup.Code != 400 || oldGroup.Body.String() != "file-has-reset" {
		t.Errorf("old group = %d %q", oldGroup.Code, oldGroup.Body.String())
	}

	newKey := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "current-group",
		Name: "x", Format: "2", EncryptMeta: `{"keyId":"other-key"}`,
		Content: []byte("x"), Token: h.adminToken,
	})
	if newKey.Code != 400 || newKey.Body.String() != "file-has-new-key" {
		t.Errorf("new key = %d %q", newKey.Code, newKey.Body.String())
	}
}

func TestUploadAccess(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)

	denied := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "group-1",
		Name: "x", Format: "2", Content: []byte("x"), Token: h.userToken,
	})
	if denied.Code != http.StatusForbidden {
		t.Errorf("non-owner status = %d, want 403", denied.Code)
	}

	admin := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "group-1",
		Name: "x", Format: "2", EncryptMeta: `{"keyId":"key"}`,
		Content: []byte("x"), Token: h.adminToken,
	})
	if admin.Code != 200 {
		t.Errorf("admin status = %d, body = %q", admin.Code, admin.Body.String())
	}
}

func TestUploadBodyLimit(t *testing.T) {
	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.Upload.SyncEncryptedFileSizeLimitMB = 1
	}, true)

	response := h.upload(t, uploadParams{
		FileID: "file-1", Name: "big", Format: "2",
		Content: make([]byte, 2<<20), Token: h.adminToken,
	})
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
}

// --- download ------------------------------------------------------------

func TestDownloadErrors(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)
	// A record without a blob.
	h.createFile(t, fileSpec{
		ID: "no-blob", KeyID: ptr("key"), SyncVersion: ptr(2.0), Owner: h.admin.Id,
	})

	cases := []struct {
		name     string
		fileID   string
		token    string
		wantCode int
		wantBody string
	}{
		{"unauthenticated", "file-1", "", 401, ""},
		{"missing", "missing", h.adminToken, 400, "User or file not found"},
		{"invalid id", "budget@2026", h.adminToken, 400, "invalid fileId"},
		{"no blob", "no-blob", h.adminToken, 404, "file-not-found"},
		{"non-owner", "file-1", h.userToken, 403, "file-access-not-allowed"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.download(t, testCase.fileID, testCase.token)
			if response.Code != testCase.wantCode {
				t.Fatalf("status = %d, want %d", response.Code, testCase.wantCode)
			}
			if testCase.wantBody != "" && response.Body.String() != testCase.wantBody {
				t.Errorf("body = %q, want %q", response.Body.String(), testCase.wantBody)
			}
		})
	}
}

func TestDownloadSharedAndAdmin(t *testing.T) {
	h := newHarness(t)
	h.upload(t, uploadParams{
		FileID: "file-1", Name: "Shared", Format: "2",
		Content: []byte("shared blob"), Token: h.otherToken,
	})
	h.grantAccess(t, h.user.Id, "file-1")

	if response := h.download(t, "file-1", h.userToken); response.Code != 200 || response.Body.String() != "shared blob" {
		t.Errorf("shared user download = %d %q", response.Code, response.Body.String())
	}
	if response := h.download(t, "file-1", h.adminToken); response.Code != 200 {
		t.Errorf("admin download = %d", response.Code)
	}
}

// --- update-user-filename ------------------------------------------------

func TestUpdateUserFilename(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)

	// A shared user may rename; a stranger may not.
	denied := h.postJSON(t, "/sync/update-user-filename", map[string]string{"fileId": "file-1"}, h.userToken)
	if denied.Code != http.StatusForbidden {
		t.Errorf("non-owner status = %d", denied.Code)
	}

	h.grantAccess(t, h.user.Id, "file-1")

	response := h.postJSON(t, "/sync/update-user-filename", map[string]string{
		"fileId": "file-1", "name": "Renamed",
	}, h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := h.fileRecord(t, "file-1").GetString("name"); got != "Renamed" {
		t.Errorf("name = %q", got)
	}

	missing := h.postJSON(t, "/sync/update-user-filename", map[string]string{"fileId": "missing"}, h.adminToken)
	if missing.Code != 400 || missing.Body.String() != "file-not-found" {
		t.Errorf("missing = %d %q", missing.Code, missing.Body.String())
	}
}

// --- list-user-files / get-user-file-info --------------------------------

func TestListUserFiles(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "owned", "group-1", "key", h.user.Id)
	h.standardFile(t, "shared", "group-2", "key", h.other.Id)
	h.createFile(t, fileSpec{
		ID: "deleted", GroupID: ptr("group-3"), SyncVersion: ptr(2.0), Owner: h.user.Id, Deleted: true,
	})
	h.grantAccess(t, h.user.Id, "shared")

	response := h.getJSON("/sync/list-user-files", h.userToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	data := jsonBody(t, response)["data"].([]any)
	ids := map[string]bool{}
	for _, entry := range data {
		ids[entry.(map[string]any)["fileId"].(string)] = true
	}
	if !ids["owned"] || !ids["shared"] {
		t.Errorf("files = %v, want owned and shared", ids)
	}
	if ids["deleted"] {
		t.Error("deleted budget was listed")
	}

	// The access list includes the owner with the owner flag.
	for _, entry := range data {
		file := entry.(map[string]any)
		if file["fileId"] != "shared" {
			continue
		}
		users := file["usersWithAccess"].([]any)
		if len(users) != 2 {
			t.Fatalf("usersWithAccess = %v, want 2", users)
		}
		last := users[len(users)-1].(map[string]any)
		if last["userId"] != h.other.Id || last["owner"] != true || last["userName"] != "other@example.com" {
			t.Errorf("owner row = %v", last)
		}
		if file["deleted"] != float64(0) {
			t.Errorf("deleted = %v, want 0", file["deleted"])
		}
	}
}

func TestGetUserFileInfo(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "file-1", GroupID: ptr("group-1"), KeyID: ptr("key"),
		EncryptMeta: ptr(`{"keyId":"key","extra":"x"}`), SyncVersion: ptr(2.0),
		Owner: h.other.Id, Name: "Budget",
	})
	h.grantAccess(t, h.user.Id, "file-1")

	response := h.getWithHeaders("/sync/get-user-file-info", h.userToken, map[string]string{"X-Actual-File-Id": "file-1"})
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	data := jsonBody(t, response)["data"].(map[string]any)
	if data["fileId"] != "file-1" || data["groupId"] != "group-1" || data["name"] != "Budget" {
		t.Errorf("data = %v", data)
	}
	meta, ok := data["encryptMeta"].(map[string]any)
	if !ok || meta["keyId"] != "key" || meta["extra"] != "x" {
		t.Errorf("encryptMeta = %v", data["encryptMeta"])
	}

	missing := h.getWithHeaders("/sync/get-user-file-info", h.adminToken, map[string]string{"X-Actual-File-Id": "missing"})
	if missing.Code != 400 {
		t.Fatalf("missing status = %d", missing.Code)
	}
	if body := jsonBody(t, missing); body["reason"] != "file-not-found" {
		t.Errorf("missing body = %v", body)
	}

	invalid := h.getWithHeaders("/sync/get-user-file-info", h.adminToken, map[string]string{"X-Actual-File-Id": "a b"})
	if invalid.Code != 400 || invalid.Body.String() != "invalid fileId" {
		t.Errorf("invalid = %d %q", invalid.Code, invalid.Body.String())
	}

	denied := h.getWithHeaders("/sync/get-user-file-info", h.userToken, map[string]string{"X-Actual-File-Id": "file-1"})
	if denied.Code != 200 {
		t.Errorf("shared user status = %d", denied.Code)
	}

	stranger := h.getWithHeaders("/sync/get-user-file-info", h.otherToken, map[string]string{"X-Actual-File-Id": "file-1"})
	if stranger.Code != 200 {
		t.Errorf("owner status = %d", stranger.Code)
	}
}

// --- delete-user-file ----------------------------------------------------

func TestDeleteUserFile(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)

	// Create the group db and two secrets, one for this budget and one global.
	if response := h.postSync(t, syncRequest("file-1", "group-1", "key", zeroSince), h.adminToken); response.Code != 200 {
		t.Fatalf("sync status = %d", response.Code)
	}
	if err := h.store.SetSecret("simplefin_token", "file-1", "budget-secret"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSecret("simplefin_token", "", "global-secret"); err != nil {
		t.Fatal(err)
	}

	response := h.postJSON(t, "/sync/delete-user-file", map[string]string{"fileId": "file-1"}, h.adminToken)
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	record := h.fileRecord(t, "file-1")
	if !record.GetBool("deleted") {
		t.Error("file was not marked deleted")
	}
	if record.GetString("blob") != "" {
		t.Error("the blob should be kept for a restore")
	}
	if _, err := os.Stat(h.groupFilePath("group-1")); err != nil {
		t.Errorf("the group database should be kept: %v", err)
	}

	if _, exists, _ := h.store.Secret("simplefin_token", "file-1"); exists {
		t.Error("the budget secret was not cleared")
	}
	if value, exists, _ := h.store.Secret("simplefin_token", ""); !exists || value != "global-secret" {
		t.Error("the global secret was cleared")
	}

	// A deleted budget is no longer visible.
	if listed := jsonBody(t, h.getJSON("/sync/list-user-files", h.adminToken))["data"].([]any); len(listed) != 0 {
		t.Errorf("deleted budget listed: %v", listed)
	}
}

func TestDeleteUserFileErrors(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	missing := h.postJSON(t, "/sync/delete-user-file", map[string]string{}, h.adminToken)
	if missing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing fileId status = %d", missing.Code)
	}
	if body := jsonBody(t, missing); body["details"] != "fileId-required" {
		t.Errorf("body = %v", body)
	}

	unknown := h.postJSON(t, "/sync/delete-user-file", map[string]string{"fileId": "missing"}, h.adminToken)
	if unknown.Code != 400 || unknown.Body.String() != "file-not-found" {
		t.Errorf("unknown = %d %q", unknown.Code, unknown.Body.String())
	}

	shared := h.postJSON(t, "/sync/delete-user-file", map[string]string{"fileId": "file-1"}, h.userToken)
	if shared.Code != http.StatusForbidden {
		t.Errorf("shared user status = %d", shared.Code)
	}
}

// TestSyncAfterUpload exercises the full client flow: upload, sync, download.
func TestSyncAfterUpload(t *testing.T) {
	h := newHarness(t)

	uploaded := h.upload(t, uploadParams{
		FileID: "file-1", Name: "Budget", Format: "2",
		Content: []byte("v1"), Token: h.adminToken,
	})
	groupID := jsonBody(t, uploaded)["groupId"].(string)

	message := messageAt(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), false, "payload")
	request := syncRequest("file-1", groupID, "", zeroSince)
	request.Messages = []*pb.MessageEnvelope{message}
	if response := h.postSync(t, request, h.adminToken); response.Code != 200 {
		t.Fatalf("sync status = %d, body = %q", response.Code, response.Body.String())
	}

	read := h.postSync(t, syncRequest("file-1", groupID, "", zeroSince), h.adminToken)
	messages := decodeSyncResponse(t, read).GetMessages()
	if len(messages) != 1 || messages[0].GetTimestamp() != message.Timestamp {
		t.Errorf("messages = %v", messages)
	}

	if response := h.download(t, "file-1", h.adminToken); strings.TrimSpace(response.Body.String()) != "v1" {
		t.Errorf("download = %q", response.Body.String())
	}
}
