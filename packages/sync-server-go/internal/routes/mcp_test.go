package routes

import (
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
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
		path   string
		field  string
		value  string
		record string
	}{
		{"/api/collections/actual_secrets/records/" + secretRecord.Id, "value", "super-secret-token", secretRecord.Id},
		{"/api/collections/actual_api_keys/records/" + apiKeyRecord.Id, "key_hash", secret, apiKeyRecord.Id},
		{"/api/collections/actual_pending_oauth/records/" + pending.Id, "code_verifier", "super-secret-verifier", pending.Id},
	} {
		response := h.pbRequest(t, "GET", testCase.path, nil, token)
		if response.Code != 200 {
			t.Fatalf("%s: status = %d, body = %q", testCase.path, response.Code, response.Body.String())
		}
		body := string(response.Body.Bytes())
		if strings.Contains(body, testCase.value) {
			t.Errorf("%s leaked %q: %s", testCase.field, testCase.value, body)
		}
	}

	// Internal reads are not enriched, so the server still sees the value.
	if value, ok, err := h.store.Secret("simplefin_token", ""); err != nil || !ok || value != "super-secret-token" {
		t.Errorf("internal secret = %q, %v, %v", value, ok, err)
	}
}
