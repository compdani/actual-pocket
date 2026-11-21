package syncdb

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt"
)

const zeroSince = "1970-01-01T00:00:00.000Z-0000-0000000000000000"

func newEngine(t *testing.T) *Engine {
	t.Helper()

	engine, err := New(filepath.Join(t.TempDir(), "actual_groups"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return engine
}

func message(timestamp string, encrypted bool, content string) Message {
	return Message{Timestamp: timestamp, IsEncrypted: encrypted, Content: []byte(content)}
}

func TestSyncCreatesTheGroupDatabase(t *testing.T) {
	engine := newEngine(t)

	result, err := engine.Sync("group1", nil, zeroSince)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if result.Trie.Value() != 0 {
		t.Errorf("trie hash = %d, want 0 for an empty sync", result.Trie.Value())
	}
	if len(result.Messages) != 0 {
		t.Errorf("messages = %v, want none", result.Messages)
	}

	path := filepath.Join(engine.Dir(), "group-group1.sqlite")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("group database was not created: %v", err)
	}
}

func TestSyncStoresAndReturnsMessages(t *testing.T) {
	engine := newEngine(t)

	first := message("2024-01-01T00:00:00.000Z-0000-0000000000000001", false, "one")
	second := message("2024-01-02T00:00:00.000Z-0000-0000000000000002", true, "two")

	result, err := engine.Sync("group1", []Message{first, second}, zeroSince)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// The clients get what was already there, not what they just pushed.
	if len(result.Messages) != 0 {
		t.Errorf("first sync returned %d messages, want 0", len(result.Messages))
	}
	if result.Trie.Value() == 0 {
		t.Error("first sync produced an empty merkle")
	}

	// Since the newest message, nothing comes back.
	result, err = engine.Sync("group1", nil, second.Timestamp)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("second sync returned %d messages, want 0", len(result.Messages))
	}

	// Since before both, both come back in timestamp order.
	result, err = engine.Sync("group1", nil, zeroSince)
	if err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("third sync returned %d messages, want 2", len(result.Messages))
	}
	if result.Messages[0].Timestamp != first.Timestamp || result.Messages[1].Timestamp != second.Timestamp {
		t.Errorf("messages out of order: %v", result.Messages)
	}
	if result.Messages[1].IsEncrypted != true {
		t.Error("is_encrypted did not round-trip as 1")
	}
	if string(result.Messages[0].Content) != "one" {
		t.Errorf("content = %q, want %q", result.Messages[0].Content, "one")
	}
}

func TestSyncDuplicateIsIdempotent(t *testing.T) {
	engine := newEngine(t)

	msg := message("2024-01-01T00:00:00.000Z-0000-0000000000000001", false, "one")

	first, err := engine.Sync("group1", []Message{msg}, zeroSince)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	second, err := engine.Sync("group1", []Message{msg}, zeroSince)
	if err != nil {
		t.Fatalf("duplicate sync: %v", err)
	}

	if first.Trie.Value() != second.Trie.Value() {
		t.Errorf("duplicate changed the merkle: %d -> %d", first.Trie.Value(), second.Trie.Value())
	}
	if len(second.Messages) != 1 {
		t.Errorf("duplicate sync returned %d messages, want the stored one", len(second.Messages))
	}
}

func TestSyncRejectsInvalidTimestampsAndRollsBack(t *testing.T) {
	engine := newEngine(t)

	valid := message("2024-01-01T00:00:00.000Z-0000-0000000000000001", false, "valid")
	invalid := message("not-a-timestamp", false, "invalid")

	if _, err := engine.Sync("group1", []Message{valid, invalid}, zeroSince); !errors.Is(err, ErrInvalidTimestamp) {
		t.Fatalf("err = %v, want ErrInvalidTimestamp", err)
	}

	// The whole batch rolled back: the valid message is not stored either.
	result, err := engine.Sync("group1", nil, zeroSince)
	if err != nil {
		t.Fatalf("sync after rollback: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("stored %d messages after a rejected batch, want 0", len(result.Messages))
	}
	if result.Trie.Value() != 0 {
		t.Errorf("merkle hash = %d after a rejected batch, want 0", result.Trie.Value())
	}

	// Retrying the bad batch fails again instead of being treated as a duplicate.
	if _, err := engine.Sync("group1", []Message{valid, invalid}, zeroSince); !errors.Is(err, ErrInvalidTimestamp) {
		t.Fatalf("retry err = %v, want ErrInvalidTimestamp", err)
	}
}

func TestSyncClockDriftBoundary(t *testing.T) {
	engine := newEngine(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	engine.now = func() time.Time { return now }

	atBoundary := crdtTimestamp(t, now.Add(maxFutureDrift))
	if _, err := engine.Sync("group1", []Message{atBoundary}, zeroSince); err != nil {
		t.Fatalf("message exactly 5 minutes ahead: %v", err)
	}

	tooFar := crdtTimestamp(t, now.Add(maxFutureDrift+time.Millisecond))
	if _, err := engine.Sync("group1", []Message{tooFar}, zeroSince); !errors.Is(err, ErrClockDrift) {
		t.Fatalf("err = %v, want ErrClockDrift", err)
	}
}

// TestDuplicateMessageSkipsTimestampValidation ports the Node test "accepts a
// resent far-future message the server already has": once stored, a message
// is never re-validated, because INSERT OR IGNORE affects no rows.
func TestDuplicateMessageSkipsTimestampValidation(t *testing.T) {
	engine := newEngine(t)
	realNow := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	// Store a message an hour in the "future" by pretending the clock is later.
	engine.now = func() time.Time { return realNow.Add(2 * time.Hour) }
	stored := crdtTimestamp(t, realNow.Add(time.Hour))
	if _, err := engine.Sync("group1", []Message{stored}, zeroSince); err != nil {
		t.Fatalf("seed sync: %v", err)
	}

	// Move the clock back: the stored message is now an hour ahead.
	engine.now = func() time.Time { return realNow }
	if _, err := engine.Sync("group1", []Message{stored}, zeroSince); err != nil {
		t.Fatalf("resending an already stored message failed: %v", err)
	}
}

func TestSyncRejectsInvalidGroupIDs(t *testing.T) {
	engine := newEngine(t)

	for _, groupID := range []string{"", "..", "../escape", "a/b", "a b", "group.sqlite"} {
		if _, err := engine.Sync(groupID, nil, zeroSince); !errors.Is(err, ErrInvalidGroupID) {
			t.Errorf("Sync(%q) err = %v, want ErrInvalidGroupID", groupID, err)
		}
	}

	entries, err := os.ReadDir(engine.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid ids created files: %v", entries)
	}
}

func TestSyncSeparatesGroups(t *testing.T) {
	engine := newEngine(t)

	one := message("2024-01-01T00:00:00.000Z-0000-0000000000000001", false, "one")
	if _, err := engine.Sync("group-a", []Message{one}, zeroSince); err != nil {
		t.Fatalf("sync group-a: %v", err)
	}

	result, err := engine.Sync("group-b", nil, zeroSince)
	if err != nil {
		t.Fatalf("sync group-b: %v", err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("group-b sees group-a's messages")
	}
}

func TestSyncConcurrently(t *testing.T) {
	engine := newEngine(t)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	const workers = 8
	const perWorker = 20

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < perWorker; index++ {
				millis := base.Add(time.Duration(worker*perWorker+index) * time.Minute)
				msg := crdtTimestamp(t, millis)
				if _, err := engine.Sync("shared", []Message{msg}, zeroSince); err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent sync failed: %v", err)
	}

	result, err := engine.Sync("shared", nil, zeroSince)
	if err != nil {
		t.Fatalf("final sync: %v", err)
	}
	if len(result.Messages) != workers*perWorker {
		t.Errorf("stored %d messages, want %d", len(result.Messages), workers*perWorker)
	}
}

func TestDeleteRemovesTheGroupFile(t *testing.T) {
	engine := newEngine(t)

	msg := message("2024-01-01T00:00:00.000Z-0000-0000000000000001", false, "one")
	if _, err := engine.Sync("group1", []Message{msg}, zeroSince); err != nil {
		t.Fatalf("sync: %v", err)
	}

	path := filepath.Join(engine.Dir(), "group-group1.sqlite")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	if err := engine.Delete("group1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still exists after Delete: %v", err)
	}

	// Deleting again is a no-op, and the next sync starts from scratch.
	if err := engine.Delete("group1"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	result, err := engine.Sync("group1", nil, zeroSince)
	if err != nil {
		t.Fatalf("sync after delete: %v", err)
	}
	if len(result.Messages) != 0 || result.Trie.Value() != 0 {
		t.Error("group state survived a delete")
	}

	if err := engine.Delete("bad id"); !errors.Is(err, ErrInvalidGroupID) {
		t.Errorf("Delete(\\\"bad id\\\") err = %v, want ErrInvalidGroupID", err)
	}
}

func crdtTimestamp(t *testing.T, at time.Time) Message {
	t.Helper()

	timestamp := at.UTC().Format("2006-01-02T15:04:05.000Z") + "-0000-0123456789ABCDEF"
	if _, ok := crdt.Parse(timestamp); !ok {
		t.Fatalf("generated an invalid timestamp: %s", timestamp)
	}

	return message(timestamp, false, "")
}
