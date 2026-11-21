package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

func TestSecretsGlobal(t *testing.T) {
	h := newHarness(t)

	// Only admins manage global secrets.
	denied := h.postJSON(t, "/secret", map[string]string{
		"name": "simplefin_token", "value": "abc",
	}, h.userToken)
	if denied.Code != http.StatusForbidden || jsonBody(t, denied)["reason"] != "not-admin" {
		t.Fatalf("non-admin = %d %v", denied.Code, jsonBody(t, denied))
	}

	set := h.postJSON(t, "/secret", map[string]string{
		"name": "simplefin_token", "value": "abc",
	}, h.adminToken)
	if set.Code != 200 || set.Body.String() != `{"status":"ok"}` {
		t.Fatalf("set = %d %q", set.Code, set.Body.String())
	}

	// GET only reports existence.
	if got := h.getJSON("/secret/simplefin_token", h.adminToken); got.Code != http.StatusNoContent {
		t.Errorf("exists status = %d, want 204", got.Code)
	}
	if got := h.getJSON("/secret/gocardless_secretId", h.adminToken); got.Code != 404 {
		t.Errorf("missing status = %d, want 404", got.Code)
	}
	if got := h.getJSON("/secret/unknown_name", h.adminToken); got.Code != 404 || got.Body.String() != "key not found" {
		t.Errorf("unknown name = %d %q", got.Code, got.Body.String())
	}

	// Empty strings still count as set.
	h.postJSON(t, "/secret", map[string]string{"name": "akahu_userToken", "value": ""}, h.adminToken)
	if got := h.getJSON("/secret/akahu_userToken", h.adminToken); got.Code != http.StatusNoContent {
		t.Errorf("empty secret status = %d, want 204", got.Code)
	}

	// Delete resets it.
	if got := h.deleteJSON(t, "/secret/simplefin_token", nil, h.adminToken); got.Code != 200 {
		t.Errorf("delete status = %d", got.Code)
	}
	if got := h.getJSON("/secret/simplefin_token", h.adminToken); got.Code != 404 {
		t.Errorf("after delete = %d", got.Code)
	}

	// An unknown name cannot be stored.
	invalid := h.postJSON(t, "/secret", map[string]string{"name": "nope", "value": "x"}, h.adminToken)
	if invalid.Code != 400 || jsonBody(t, invalid)["reason"] != "invalid-secret-name" {
		t.Errorf("invalid name = %d %v", invalid.Code, jsonBody(t, invalid))
	}
}

func TestSecretsPerBudget(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.other.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	// A shared user cannot manage the budget's credentials.
	denied := h.postJSONWithFile(t, "/secret", map[string]string{
		"name": "simplefin_token", "value": "abc",
	}, h.userToken, "file-1")
	if denied.Code != http.StatusForbidden || jsonBody(t, denied)["reason"] != "file-access-denied" {
		t.Fatalf("shared user = %d %v", denied.Code, jsonBody(t, denied))
	}

	// The owner can.
	response := h.postJSONWithFile(t, "/secret", map[string]string{
		"name": "simplefin_token", "value": "budget-token",
	}, h.otherToken, "file-1")
	if response.Code != 200 {
		t.Fatalf("owner set = %d %q", response.Code, response.Body.String())
	}

	// The budget secret is scoped: an admin sees no global one.
	if got := h.getJSON("/secret/simplefin_token", h.adminToken); got.Code != 404 {
		t.Errorf("global read = %d, want 404", got.Code)
	}
	scoped := h.getWithHeaders("/secret/simplefin_token", h.otherToken, map[string]string{"X-Actual-File-Id": "file-1"})
	if scoped.Code != http.StatusNoContent {
		t.Errorf("scoped read = %d, want 204", scoped.Code)
	}

	// The stored value round-trips through the store.
	if value, exists, _ := h.store.Secret("simplefin_token", "file-1"); !exists || value != "budget-token" {
		t.Errorf("stored secret = %q, %v", value, exists)
	}

	// An invalid file id is a 400.
	invalid := h.getWithHeaders("/secret/simplefin_token", h.adminToken, map[string]string{"X-Actual-File-Id": "a b"})
	if invalid.Code != 400 || jsonBody(t, invalid)["reason"] != "invalid-file-id" {
		t.Errorf("invalid file = %d %v", invalid.Code, jsonBody(t, invalid))
	}
}

func TestSecretsUnauthenticated(t *testing.T) {
	h := newHarness(t)

	if got := h.getJSON("/secret/simplefin_token", ""); got.Code != 401 {
		t.Errorf("status = %d, want 401", got.Code)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)

	response := h.postJSON(t, "/api-keys", map[string]any{
		"name": "Import", "fileIds": []string{"file-1"},
	}, h.adminToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}

	data := jsonBody(t, response)["data"].(map[string]any)
	key, _ := data["key"].(string)
	id, _ := data["id"].(string)
	if key == "" || id == "" {
		t.Fatalf("data = %v", data)
	}
	if len(key) != len("actual_")+43 || key[:len("actual_")] != "actual_" {
		t.Errorf("key = %q, want the actual_ prefix plus 43 url-safe characters", key)
	}
	if data["name"] != "Import" || data["createdAt"].(float64) <= 0 {
		t.Errorf("data = %v", data)
	}

	// Only the digest is stored.
	record := h.mustRecord(t, "actual_api_keys", id)
	if record.GetString("key_hash") == key {
		t.Error("the key was stored in clear text")
	}
	if record.GetString("key_hash") != hashForTest(key) {
		t.Errorf("key_hash = %q", record.GetString("key_hash"))
	}

	// Listing never returns the secret.
	list := h.getJSON("/api-keys", h.adminToken)
	entries := jsonBody(t, list)["data"].([]any)
	if len(entries) != 1 {
		t.Fatalf("keys = %v", entries)
	}
	entry := entries[0].(map[string]any)
	if entry["key"] != nil {
		t.Error("the list leaked the secret")
	}
	if fileIDs := entry["fileIds"].([]any); len(fileIDs) != 1 || fileIDs[0] != "file-1" {
		t.Errorf("fileIds = %v", fileIDs)
	}

	// Another user cannot see or revoke it.
	if got := jsonBody(t, h.getJSON("/api-keys", h.userToken))["data"].([]any); len(got) != 0 {
		t.Errorf("other user keys = %v", got)
	}
	if got := h.deleteJSON(t, "/api-keys/"+id, nil, h.userToken); got.Code != 404 {
		t.Errorf("other user delete = %d, want 404", got.Code)
	}

	if got := h.deleteJSON(t, "/api-keys/"+id, nil, h.adminToken); got.Code != 200 {
		t.Errorf("delete = %d", got.Code)
	}
	if got := h.deleteJSON(t, "/api-keys/"+id, nil, h.adminToken); got.Code != 404 {
		t.Fatalf("second delete = %d", got.Code)
	}
	if got := jsonBody(t, h.getJSON("/api-keys", h.adminToken))["data"].([]any); len(got) != 0 {
		t.Errorf("keys after delete = %v", got)
	}
}

func TestAPIKeyValidation(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"fileIds": []string{"file-1"}}},
		{"long name", map[string]any{"name": strings.Repeat("x", 101), "fileIds": []string{"file-1"}}},
		{"no budgets", map[string]any{"name": "x", "fileIds": []string{}}},
		{"invalid budget id", map[string]any{"name": "x", "fileIds": []string{"a b"}}},
		{"duplicate budgets", map[string]any{"name": "x", "fileIds": []string{"file-1", "file-1"}}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.postJSON(t, "/api-keys", testCase.body, h.adminToken)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if got := jsonBody(t, response)["reason"]; got != "invalid-api-key" {
				t.Errorf("reason = %v", got)
			}
		})
	}

	// Admins can grant a key for any budget; a basic user cannot grant a
	// budget they cannot access.
	h.standardFile(t, "not-mine", "group-2", "key", h.other.Id)
	adminKey := h.postJSON(t, "/api-keys", map[string]any{"name": "x", "fileIds": []string{"not-mine"}}, h.adminToken)
	if adminKey.Code != http.StatusCreated {
		t.Errorf("admin status = %d, want 201", adminKey.Code)
	}

	denied := h.postJSON(t, "/api-keys", map[string]any{"name": "x", "fileIds": []string{"not-mine"}}, h.userToken)
	if denied.Code != http.StatusForbidden || jsonBody(t, denied)["reason"] != "file-access-denied" {
		t.Errorf("inaccessible budget = %d %v", denied.Code, jsonBody(t, denied))
	}

	// A deleted budget cannot be granted either.
	h.createFile(t, fileSpec{ID: "deleted", GroupID: ptr("group-3"), SyncVersion: ptr(2.0), Owner: h.admin.Id, Deleted: true})
	deleted := h.postJSON(t, "/api-keys", map[string]any{"name": "x", "fileIds": []string{"deleted"}}, h.adminToken)
	if deleted.Code != http.StatusForbidden {
		t.Errorf("deleted budget = %d %v", deleted.Code, jsonBody(t, deleted))
	}
}

// TestAPIKeyScopesTheSyncRoutes covers the api-key-budget-access behaviours:
// the key can use the budget routes for its grants only, and cannot reach
// session-only endpoints.
func TestAPIKeyScopesTheSyncRoutes(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key", h.admin.Id)
	h.standardFile(t, "file-2", "group-2", "key", h.admin.Id)
	h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "group-1",
		Name: "Budget", Format: "2", EncryptMeta: `{"keyId":"key"}`,
		Content: []byte("blob"), Token: h.adminToken,
	})

	created := h.postJSON(t, "/api-keys", map[string]any{
		"name": "Key", "fileIds": []string{"file-1"},
	}, h.adminToken)
	secret := jsonBody(t, created)["data"].(map[string]any)["key"].(string)

	// /account/validate reports the api-key method.
	validated := h.getJSON("/account/validate", secret)
	data := jsonBody(t, validated)["data"].(map[string]any)
	if data["validated"] != true || data["loginMethod"] != "api-key" {
		t.Errorf("validate = %v", data)
	}

	// list-user-files is scoped to the grant.
	listed := jsonBody(t, h.getJSON("/sync/list-user-files", secret))["data"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["fileId"] != "file-1" {
		t.Errorf("listed = %v", listed)
	}

	// The granted budget works.
	if got := h.download(t, "file-1", secret); got.Code != 200 || got.Body.String() != "blob" {
		t.Errorf("download = %d %q", got.Code, got.Body.String())
	}
	info := h.getWithHeaders("/sync/get-user-file-info", secret, map[string]string{"X-Actual-File-Id": "file-1"})
	if info.Code != 200 {
		t.Errorf("info = %d", info.Code)
	}
	key := h.postJSON(t, "/sync/user-get-key", map[string]string{"token": secret, "fileId": "file-1"}, "")
	if key.Code != 200 {
		t.Errorf("user-get-key = %d %q", key.Code, key.Body.String())
	}
	sync := h.postSync(t, syncRequest("file-1", "group-1", "key", zeroSince), secret)
	if sync.Code != 200 {
		t.Errorf("sync = %d %q", sync.Code, sync.Body.String())
	}

	// The other budget is refused on every route.
	for name, response := range map[string]*httptest.ResponseRecorder{
		"download": h.download(t, "file-2", secret),
		"info":     h.getWithHeaders("/sync/get-user-file-info", secret, map[string]string{"X-Actual-File-Id": "file-2"}),
		"key":      h.postJSON(t, "/sync/user-get-key", map[string]string{"token": secret, "fileId": "file-2"}, ""),
		"sync":     h.postSync(t, syncRequest("file-2", "group-2", "key", zeroSince), secret),
	} {
		if response.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", name, response.Code)
		}
	}

	// Session-only routes reject the key.
	for _, path := range []string{"/admin/users/", "/api-keys"} {
		if got := h.getJSON(path, secret); got.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with api key = %d, want 401", path, got.Code)
		}
	}
	if got := h.postJSON(t, "/account/server-prefs", map[string]any{"prefs": map[string]string{"a": "b"}}, secret); got.Code != http.StatusUnauthorized {
		t.Errorf("server-prefs with api key = %d, want 401", got.Code)
	}
	upload := h.upload(t, uploadParams{
		FileID: "file-1", HasGroupID: true, GroupID: "group-1", Name: "x",
		Format: "2", Content: []byte("x"), Token: secret,
	})
	if upload.Code != http.StatusUnauthorized {
		t.Errorf("upload with api key = %d, want 401", upload.Code)
	}

	// A revoked key stops working.
	h.deleteJSON(t, "/api-keys/"+jsonBody(t, created)["data"].(map[string]any)["id"].(string), nil, h.adminToken)
	if got := h.getJSON("/account/validate", secret); got.Code != http.StatusUnauthorized {
		t.Errorf("revoked key = %d, want 401", got.Code)
	}
}

func (h *harness) mustRecord(t *testing.T, collection, id string) *core.Record {
	t.Helper()

	record, err := h.app.FindRecordById(collection, id)
	if err != nil {
		t.Fatalf("find %s/%s: %v", collection, id, err)
	}
	return record
}

func hashForTest(value string) string {
	return store.HashToken(value)
}
