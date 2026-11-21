package routes

import "github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"

// syncStatsRecorder updates actual_sync_stats off the sync hot path. The
// bounded queue drops summaries rather than blocking a client request.
type syncStatsRecorder struct {
	store *store.Store
	ch    chan store.SyncStats
}

func newSyncStatsRecorder(s *store.Store) *syncStatsRecorder {
	recorder := &syncStatsRecorder{
		store: s,
		ch:    make(chan store.SyncStats, 64),
	}
	go recorder.run()
	return recorder
}

func (r *syncStatsRecorder) run() {
	for stats := range r.ch {
		if err := r.store.UpsertSyncStats(stats); err != nil {
			// Stats are advisory; a failed write must not affect sync.
			continue
		}
	}
}

func (r *syncStatsRecorder) Enqueue(stats store.SyncStats) {
	select {
	case r.ch <- stats:
	default:
	}
}
