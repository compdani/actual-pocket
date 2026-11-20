package routes

import (
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// TestBackupHooksPauseSyncs proves the OnBackupCreate/OnBackupRestore handlers
// take the write lock, so PocketBase never copies a group file mid-write.
func TestBackupHooksPauseSyncs(t *testing.T) {
	h := newHarness(t)
	h.standardFile(t, "file-1", "group-1", "key-id", h.admin.Id)

	for _, backup := range []struct {
		name  string
		event func(release chan struct{}) error
	}{
		{
			name: "create",
			event: func(release chan struct{}) error {
				return h.app.OnBackupCreate().Trigger(
					&core.BackupEvent{App: h.app, Name: "test"},
					func(event *core.BackupEvent) error {
						<-release
						return event.Next()
					},
				)
			},
		},
		{
			name: "restore",
			event: func(release chan struct{}) error {
				return h.app.OnBackupRestore().Trigger(
					&core.BackupEvent{App: h.app, Name: "test"},
					func(event *core.BackupEvent) error {
						<-release
						return event.Next()
					},
				)
			},
		},
	} {
		t.Run(backup.name, func(t *testing.T) {
			release := make(chan struct{})
			backupDone := make(chan struct{})

			go func() {
				defer close(backupDone)
				if err := backup.event(release); err != nil {
					t.Errorf("backup hook: %v", err)
				}
			}()

			// Give the hook a moment to take the write lock.
			time.Sleep(50 * time.Millisecond)

			syncDone := make(chan struct{})
			go func() {
				defer close(syncDone)
				response := h.postSync(t, syncRequest("file-1", "group-1", "key-id", zeroSince), testToken)
				if response.Code != 200 {
					t.Errorf("sync during backup = %d", response.Code)
				}
			}()

			select {
			case <-syncDone:
				t.Fatal("sync finished while a backup held the lock")
			case <-time.After(150 * time.Millisecond):
			}

			close(release)
			<-backupDone

			select {
			case <-syncDone:
			case <-time.After(10 * time.Second):
				t.Fatal("sync did not resume after the backup")
			}
		})
	}
}
