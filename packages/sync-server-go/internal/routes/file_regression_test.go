package routes

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

func TestDeletedFileCannotSyncOrBeReused(t *testing.T) {
	h := newHarness(t)
	h.createFile(t, fileSpec{
		ID: "deleted", GroupID: ptr("group-1"), KeyID: ptr("key"),
		SyncVersion: ptr(2.0), EncryptMeta: ptr(`{"keyId":"key"}`), Owner: h.other.Id, Deleted: true,
	})
	sync := h.postSync(t, syncRequest("deleted", "group-1", "key", zeroSince), h.otherToken)
	if sync.Code != http.StatusBadRequest || sync.Body.String() != "file-not-found" {
		t.Errorf("sync = %d %s", sync.Code, sync.Body)
	}
	upload := h.upload(t, uploadParams{
		FileID: "deleted", Name: "Reused", Format: "2", Content: []byte("data"), Token: h.userToken,
	})
	if upload.Code != http.StatusInternalServerError || jsonBody(t, upload)["reason"] != "internal-error" {
		t.Errorf("upload = %d %s", upload.Code, upload.Body)
	}
	record := h.fileRecord(t, "deleted")
	if record.GetString("owner") != h.other.Id || !record.GetBool("deleted") {
		t.Fatal("upload modified the deleted record")
	}
}

func TestUnknownLengthUploadBodyLimit(t *testing.T) {
	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.Upload.SyncEncryptedFileSizeLimitMB = 1
	}, true)

	request := httptest.NewRequest(http.MethodPost, "/sync/upload-user-file", bytes.NewReader(make([]byte, 2<<20)))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/encrypted-file")
	request.Header.Set("X-Actual-File-Id", "large-file")
	request.Header.Set("X-Actual-Name", "Large")
	request.Header.Set("X-Actual-Token", h.adminToken)
	response := h.do(request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload = %d %s", response.Code, response.Body)
	}
}

func TestFailedUserDeleteRollsBackFileTransfer(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "owned", "group-1", "key", h.user.Id)
	failure := errors.New("delete failed")
	h.app.OnRecordDeleteExecute("users").Bind(&hook.Handler[*core.RecordEvent]{
		Priority: 101,
		Func:     func(e *core.RecordEvent) error { return failure },
	})

	token := h.adminToken
	response := h.pbRequest(t, http.MethodDelete, "/api/collections/users/records/"+h.user.Id, nil, token)
	if response.Code < 400 {
		t.Fatalf("delete = %d %s", response.Code, response.Body)
	}
	h.mustUser(t, h.user.Id)
	if owner := h.fileRecord(t, "owned").GetString("owner"); owner != h.user.Id {
		t.Fatalf("failed delete moved file to %q", owner)
	}
}

func TestAdminDeletePreflightsAuthorization(t *testing.T) {
	h := newHarness(t)
	admin := h.createUser(t, "peer-admin@example.com", "admin-password", "ADMIN", true, false)
	peer := h.createUser(t, "target-admin@example.com", "admin-password", "ADMIN", true, false)
	response := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{h.user.Id, peer.Id}}, h.token(t, admin))
	if response.Code != http.StatusForbidden {
		t.Errorf("delete = %d %s", response.Code, response.Body)
	}
	h.mustUser(t, h.user.Id)
	h.mustUser(t, peer.Id)
}

func TestAdminDeleteMixedListMatchesNode(t *testing.T) {
	h := newHarness(t)
	response := h.deleteJSON(t, "/admin/users", map[string]any{"ids": []string{h.admin.Id, h.user.Id, "missing"}}, h.adminToken)
	if response.Code != http.StatusBadRequest || jsonBody(t, response)["reason"] != "not-all-deleted" {
		t.Errorf("delete = %d %s", response.Code, response.Body)
	}
	if _, err := h.app.FindRecordById("users", h.user.Id); err == nil {
		t.Fatal("eligible user was not deleted")
	}
	h.mustUser(t, h.admin.Id)
}

func TestSyncStatsDoNotWriteDuringBackup(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "owned", "group-1", "key", h.user.Id)
	recorder := newSyncStatsRecorder(h.store, h.engine)
	t.Cleanup(recorder.Stop)
	writeStarted := make(chan struct{}, 1)
	h.app.OnRecordCreate("actual_sync_stats").BindFunc(func(e *core.RecordEvent) error {
		writeStarted <- struct{}{}
		return e.Next()
	})
	err := h.app.OnBackupCreate().Trigger(&core.BackupEvent{App: h.app, Name: "test"}, func(e *core.BackupEvent) error {
		recorder.Enqueue(store.SyncStats{GroupID: "group-1", FileID: "owned"})
		select {
		case <-writeStarted:
			t.Error("stats write started while backup held the lock")
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSyncStatsRecorderDrainsOnStop(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "owned", "group-1", "key", h.user.Id)
	recorder := newSyncStatsRecorder(h.store, h.engine)
	t.Cleanup(recorder.Stop)
	recorder.Enqueue(store.SyncStats{GroupID: "group-1", FileID: "owned", LastMessageCount: 1})
	recorder.Enqueue(store.SyncStats{GroupID: "group-1", FileID: "owned", LastMessageCount: 2})
	recorder.Stop()
	recorder.Enqueue(store.SyncStats{GroupID: "group-1", FileID: "owned", LastMessageCount: 3})

	stats, err := h.app.FindFirstRecordByFilter("actual_sync_stats", "group_id = 'group-1'")
	if err != nil || stats.GetInt("last_message_count") != 2 {
		t.Fatalf("queued stats were not drained before stopping: %v", err)
	}
}
