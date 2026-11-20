package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
)

const zeroSince = "1970-01-01T00:00:00.000Z-0000-0000000000000000"

func TestSyncUnauthorized(t *testing.T) {
	h := newHarness(t)

	response := h.postSync(t, syncRequest("file", "group", "key", zeroSince), "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if got, want := response.Body.String(), `{"status":"error","reason":"unauthorized","details":"token-not-found"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestSyncSuccess(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.adminToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/actual-sync" {
		t.Errorf("Content-Type = %q, want application/actual-sync", got)
	}
	if got := response.Header().Get("X-Actual-Sync-Method"); got != "simple" {
		t.Errorf("X-Actual-Sync-Method = %q, want simple", got)
	}

	decoded := decodeSyncResponse(t, response)
	if decoded.GetMerkle() == "" {
		t.Error("merkle is empty")
	}
	if len(decoded.GetMessages()) != 0 {
		t.Errorf("messages = %d, want 0 on a first sync", len(decoded.GetMessages()))
	}
}

func TestSyncInvalidBody(t *testing.T) {
	h := newHarness(t)

	request, err := http.NewRequest(http.MethodPost, "/sync/sync", strings.NewReader("invalid-body"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/actual-sync")
	request.Header.Set("X-Actual-Token", h.adminToken)

	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if got, want := recorder.Body.String(), `{"status":"error","reason":"internal-error"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSyncSinceRequired(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", ""), h.adminToken)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}

	body := jsonBody(t, response)
	if body["details"] != "since-required" || body["reason"] != "unprocessable-entity" || body["status"] != "error" {
		t.Errorf("body = %v", body)
	}
}

func TestSyncFileNotFound(t *testing.T) {
	h := newHarness(t)

	response := h.postSync(t, syncRequest("missing-file", "group-1", "key-id", zeroSince), h.adminToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got, want := response.Body.String(), "file-not-found"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want Express's string type", got)
	}
}

func TestSyncInvalidFileID(t *testing.T) {
	h := newHarness(t)

	response := h.postSync(t, syncRequest("../escape", "group-1", "key-id", zeroSince), h.adminToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got, want := response.Body.String(), "invalid fileId"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSyncValidation(t *testing.T) {
	cases := []struct {
		name        string
		file        fileSpec
		request     *pb.SyncRequest
		wantStatus  int
		wantMessage string
	}{
		{
			name: "old sync version",
			file: fileSpec{
				ID: "file-1", GroupID: ptr("group-1"), KeyID: ptr("key-id"),
				EncryptMeta: ptr(`{"keyId":"key-id"}`), SyncVersion: ptr(1.0),
			},
			wantStatus:  400,
			wantMessage: "file-old-version",
		},
		{
			name: "no sync version",
			file: fileSpec{
				ID: "file-1", GroupID: ptr("group-1"), KeyID: ptr("key-id"),
				EncryptMeta: ptr(`{"keyId":"key-id"}`),
			},
			wantStatus:  400,
			wantMessage: "file-old-version",
		},
		{
			name: "needs upload",
			file: fileSpec{
				ID: "file-1", KeyID: ptr("key-id"),
				EncryptMeta: ptr(`{"keyId":"key-id"}`), SyncVersion: ptr(2.0),
			},
			wantStatus:  400,
			wantMessage: "file-needs-upload",
		},
		{
			name: "key mismatch",
			file: fileSpec{
				ID: "file-1", GroupID: ptr("group-1"), KeyID: ptr("key-id"),
				EncryptMeta: ptr(`{"keyId":"other-key"}`), SyncVersion: ptr(2.0),
			},
			wantStatus:  400,
			wantMessage: "file-key-mismatch",
		},
		{
			name: "file has reset",
			file: fileSpec{
				ID: "file-1", GroupID: ptr("new-group"), KeyID: ptr("key-id"),
				EncryptMeta: ptr(`{"keyId":"key-id"}`), SyncVersion: ptr(2.0),
			},
			request:     syncRequest("file-1", "old-group", "key-id", zeroSince),
			wantStatus:  400,
			wantMessage: "file-has-reset",
		},
		{
			name: "file has new key",
			file: fileSpec{
				ID: "file-1", GroupID: ptr("group-1"), KeyID: ptr("old-key"),
				EncryptMeta: ptr(`{"keyId":"old-key"}`), SyncVersion: ptr(2.0),
			},
			request:     syncRequest("file-1", "group-1", "new-key", zeroSince),
			wantStatus:  400,
			wantMessage: "file-has-new-key",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			spec := testCase.file
			spec.Owner = h.admin.Id
			h.createFile(t, spec)

			request := testCase.request
			if request == nil {
				groupID := ""
				if spec.GroupID != nil {
					groupID = *spec.GroupID
				}
				keyID := ""
				if spec.KeyID != nil {
					keyID = *spec.KeyID
				}
				request = syncRequest(spec.ID, groupID, keyID, zeroSince)
			}

			response := h.postSync(t, request, h.adminToken)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", response.Code, testCase.wantStatus, response.Body.String())
			}
			if got := response.Body.String(); got != testCase.wantMessage {
				t.Errorf("body = %q, want %q", got, testCase.wantMessage)
			}
		})
	}
}

func TestSyncAccessDenied(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.other.Id)

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.userToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if got, want := response.Body.String(), "file-access-not-allowed"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSyncAdminCanSyncAnotherUsersFile(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.other.Id)

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.adminToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestSyncSharedAccess(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.other.Id)

	access, err := h.app.FindCollectionByNameOrId("actual_user_access")
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(access)
	grant.Set("user_id", h.user.Id)
	grant.Set("file_id", "file-1")
	if err := h.app.Save(grant); err != nil {
		t.Fatal(err)
	}

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.userToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestSyncRejectsBadTimestamps(t *testing.T) {
	cases := []struct {
		name      string
		timestamp string
		want      string
	}{
		{"invalid", "invalid", "invalid-timestamp"},
		{"too far in the future", timestampFor(time.Now().Add(48 * time.Hour)), "clock-drift"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

			request := syncRequest("file-1", "group-1", "key-id", zeroSince)
			request.Messages = []*pb.MessageEnvelope{{
				Timestamp: testCase.timestamp,
				Content:   []byte("payload"),
			}}

			response := h.postSync(t, request, h.adminToken)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			if got := response.Body.String(); got != testCase.want {
				t.Errorf("body = %q, want %q", got, testCase.want)
			}

			// Nothing was stored, so a retry fails the same way.
			retry := h.postSync(t, request, h.adminToken)
			if retry.Code != http.StatusBadRequest || retry.Body.String() != testCase.want {
				t.Errorf("retry = %d %q, want 400 %q", retry.Code, retry.Body.String(), testCase.want)
			}
		})
	}
}

func TestSyncClockDriftBoundary(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{{
		Timestamp: timestampFor(time.Now().Add(5 * time.Minute)),
	}}
	if response := h.postSync(t, request, h.adminToken); response.Code != http.StatusOK {
		t.Errorf("5 minutes ahead: status = %d, want 200 (%q)", response.Code, response.Body.String())
	}

	h.standardFile(t, "file-2", "group-2", "key-id", h.admin.Id)
	request = syncRequest("file-2", "group-2", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{{
		Timestamp: timestampFor(time.Now().Add(5*time.Minute + time.Second)),
	}}
	if response := h.postSync(t, request, h.adminToken); response.Code != http.StatusBadRequest {
		t.Errorf("5 minutes and a second ahead: status = %d, want 400", response.Code)
	}
}

func TestSyncReturnsMessagesNewerThanSince(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	first := messageAt(base, false, "first")
	second := messageAt(base.Add(time.Minute), true, "second")

	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{first, second}

	response := h.postSync(t, request, h.adminToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if messages := decodeSyncResponse(t, response).GetMessages(); len(messages) != 0 {
		t.Fatalf("first sync returned %d messages, want 0", len(messages))
	}

	// Since before both: both come back, ordered by timestamp.
	response = h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.adminToken)
	decoded := decodeSyncResponse(t, response)
	if len(decoded.GetMessages()) != 2 {
		t.Fatalf("messages = %d, want 2", len(decoded.GetMessages()))
	}
	if decoded.GetMessages()[0].GetTimestamp() != first.Timestamp {
		t.Errorf("first message = %q", decoded.GetMessages()[0].GetTimestamp())
	}
	if !decoded.GetMessages()[1].GetIsEncrypted() {
		t.Error("is_encrypted did not round-trip")
	}
	if string(decoded.GetMessages()[0].GetContent()) != "first" {
		t.Errorf("content = %q", decoded.GetMessages()[0].GetContent())
	}

	// Since the first: only the second.
	response = h.postSync(t, syncRequest("file-1", "group-1", "key-id", first.Timestamp), h.adminToken)
	if messages := decodeSyncResponse(t, response).GetMessages(); len(messages) != 1 || messages[0].GetTimestamp() != second.Timestamp {
		t.Errorf("messages since the first = %v, want only the second", messages)
	}
}

func TestSyncDuplicateDoesNotChangeTheMerkle(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	message := messageAt(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), false, "payload")
	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{message}

	first := decodeSyncResponse(t, h.postSync(t, request, h.adminToken))
	second := decodeSyncResponse(t, h.postSync(t, request, h.adminToken))

	if first.GetMerkle() != second.GetMerkle() {
		t.Errorf("merkle changed on a duplicate:\n%s\n%s", first.GetMerkle(), second.GetMerkle())
	}
	if len(second.GetMessages()) != 1 {
		t.Errorf("duplicate sync returned %d messages, want 1", len(second.GetMessages()))
	}
}

func TestSyncInvalidGroupID(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	// Node compares group ids as strings first, so a different (even invalid)
	// group id is reported as a reset file. The engine's own group id check is
	// covered by internal/syncdb.
	response := h.postSync(t, syncRequest("file-1", "../escape", "key-id", zeroSince), h.adminToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got, want := response.Body.String(), "file-has-reset"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSyncBodyLimit(t *testing.T) {
	h := newHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.Upload.FileSizeSyncLimitMB = 1
	}, true)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{{
		Timestamp: timestampFor(time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)),
		Content:   make([]byte, 2<<20),
	}}

	response := h.postSync(t, request, h.adminToken)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", response.Code, response.Body.String())
	}
}

func TestSyncPayloadRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	content := []byte{0x00, 0xff, 0x10, 0x7f}
	at := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	request := syncRequest("file-1", "group-1", "key-id", zeroSince)
	request.Messages = []*pb.MessageEnvelope{{
		Timestamp:   timestampFor(at),
		IsEncrypted: true,
		Content:     content,
	}}

	if response := h.postSync(t, request, h.adminToken); response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), h.adminToken)
	messages := decodeSyncResponse(t, response).GetMessages()
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if got := messages[0].GetContent(); !bytesEqual(got, content) {
		t.Errorf("content = %v, want %v", got, content)
	}
	if !messages[0].GetIsEncrypted() {
		t.Error("is_encrypted = false, want true")
	}
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
