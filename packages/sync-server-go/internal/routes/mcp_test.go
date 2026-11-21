package routes

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

func TestActualFilesOverviewView(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)
	h.grantAccess(t, h.user.Id, "file-1")

	record, err := h.app.FindFirstRecordByFilter(
		"actual_files_overview",
		"id = {:id}",
		map[string]any{"id": "file-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.GetString("owner") != h.admin.Id {
		t.Errorf("owner = %q", record.GetString("owner"))
	}
	if record.GetString("owner_email") != "admin@example.com" || record.GetString("owner_name") != "" {
		t.Errorf("owner columns = %q %q", record.GetString("owner_email"), record.GetString("owner_name"))
	}
	if record.GetInt("access_count") != 1 {
		t.Errorf("access_count = %d", record.GetInt("access_count"))
	}
	if record.GetString("group_id") != "group-1" || record.GetBool("deleted") {
		t.Errorf("file columns = %v", record)
	}
}

func TestSyncStatsRecorded(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.adminToken)
	if response.Code != 200 {
		t.Fatalf("sync status = %d, body = %q", response.Code, response.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		record, err := h.app.FindFirstRecordByFilter(
			"actual_sync_stats",
			"group_id = {:group}",
			map[string]any{"group": "group-1"},
		)
		if err == nil {
			if record.GetString("file_id") != "file-1" || record.GetString("merkle_hash") == "" {
				t.Errorf("stats = %v", record)
			}
			if record.GetInt("last_sync_at") == 0 {
				t.Error("last_sync_at = 0")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("sync stats were not recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMCPRedactionHidesSecretsFromSuperusers(t *testing.T) {
	h := newHarness(t)

	if err := h.store.SetSecret("simplefin_token", "", "super-secret-token"); err != nil {
		t.Fatal(err)
	}
	secretRecord, err := h.app.FindFirstRecordByFilter("actual_secrets", "name = {:name}", map[string]any{"name": "simplefin_token"})
	if err != nil {
		t.Fatal(err)
	}

	_, secret, err := h.store.CreateAPIKey(h.admin.Id, "MCP key", nil)
	if err != nil {
		t.Fatal(err)
	}
	apiKeyRecord, err := h.app.FindFirstRecordByFilter("actual_api_keys", "name = {:name}", map[string]any{"name": "MCP key"})
	if err != nil {
		t.Fatal(err)
	}

	pendingCollection, err := h.app.FindCollectionByNameOrId("actual_pending_oauth")
	if err != nil {
		t.Fatal(err)
	}
	pending := core.NewRecord(pendingCollection)
	pending.Set("state", "mcp-state")
	pending.Set("code_verifier", "super-secret-verifier")
	if err := h.app.Save(pending); err != nil {
		t.Fatal(err)
	}

	superusers, err := h.app.FindCollectionByNameOrId("_superusers")
	if err != nil {
		t.Fatal(err)
	}
	superuser := core.NewRecord(superusers)
	superuser.Set("email", "mcp@example.com")
	superuser.SetPassword("mcp-password-123")
	if err := h.app.Save(superuser); err != nil {
		t.Fatal(err)
	}
	token, err := superuser.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		path       string
		field      string
		value      string
		collection string
	}{
		{"/api/collections/actual_secrets/records/" + secretRecord.Id, "value", "super-secret-token", "actual_secrets"},
		{"/api/collections/actual_api_keys/records/" + apiKeyRecord.Id, "key_hash", store.HashToken(secret), "actual_api_keys"},
		{"/api/collections/actual_pending_oauth/records/" + pending.Id, "code_verifier", "super-secret-verifier", "actual_pending_oauth"},
	} {
		response := h.pbRequest(t, "GET", testCase.path, nil, token)
		if response.Code != 200 {
			t.Fatalf("%s: status = %d, body = %q", testCase.path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), testCase.value) {
			t.Errorf("%s was exposed in the record", testCase.field)
		}
		list := "/api/collections/" + testCase.collection + "/records"
		for _, query := range []url.Values{
			{"filter": {testCase.field + " != ''"}},
			{"sort": {"-" + testCase.field}},
		} {
			response := h.pbRequest(t, http.MethodGet, list+"?"+query.Encode(), nil, token)
			if response.Code != http.StatusBadRequest {
				t.Errorf("list on hidden field %s = %d", testCase.field, response.Code)
			}
		}

		response = h.pbRequest(t, http.MethodPatch, testCase.path, map[string]string{testCase.field: ""}, token)
		if response.Code != http.StatusOK {
			t.Errorf("saving redacted field %s = %d %s", testCase.field, response.Code, response.Body)
		}
		id := testCase.path[strings.LastIndex(testCase.path, "/")+1:]
		saved, err := h.app.FindRecordById(testCase.collection, id)
		if err != nil || saved.GetString(testCase.field) != testCase.value {
			t.Errorf("saving redacted field %s changed the stored value: %v", testCase.field, err)
		}
	}

	for _, query := range []url.Values{
		{"filter": {"name = 'simplefin_token'"}, "sort": {"-created"}},
		{"filter": {"name = 'value'"}},
	} {
		response := h.pbRequest(t, http.MethodGet, "/api/collections/actual_secrets/records?"+query.Encode(), nil, token)
		if response.Code != http.StatusOK {
			t.Errorf("safe list query = %d %s", response.Code, response.Body)
		}
	}

	// Internal reads are not enriched, so the server still sees the value.
	if value, ok, err := h.store.Secret("simplefin_token", ""); err != nil || !ok || value != "super-secret-token" {
		t.Errorf("internal secret no longer matches: found %v, error %v", ok, err)
	}
}
