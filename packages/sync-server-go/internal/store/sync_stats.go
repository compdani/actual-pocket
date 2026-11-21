package store

import (
	"fmt"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// SyncStats is the summary written after a successful /sync/sync.
type SyncStats struct {
	GroupID          string
	FileID           string
	LastSyncAt       int64
	LastMessageCount int
	MerkleHash       string
}

// UpsertSyncStats records one successful sync per group.
func (s *Store) UpsertSyncStats(stats SyncStats) error {
	if stats.LastSyncAt == 0 {
		stats.LastSyncAt = time.Now().Unix()
	}

	return s.app.RunInTransaction(func(txApp core.App) error {
		collection, err := txApp.FindCollectionByNameOrId("actual_sync_stats")
		if err != nil {
			return fmt.Errorf("find actual_sync_stats: %w", err)
		}

		record, err := txApp.FindFirstRecordByFilter(
			"actual_sync_stats",
			"group_id = {:group}",
			map[string]any{"group": stats.GroupID},
		)
		if err != nil || record == nil {
			record = core.NewRecord(collection)
			record.Set("group_id", stats.GroupID)
		}

		record.Set("file_id", stats.FileID)
		record.Set("last_sync_at", stats.LastSyncAt)
		record.Set("last_message_count", stats.LastMessageCount)
		record.Set("merkle_hash", stats.MerkleHash)

		return txApp.Save(record)
	})
}
