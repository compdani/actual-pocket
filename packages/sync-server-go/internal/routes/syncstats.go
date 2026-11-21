package routes

import (
	"sync"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/syncdb"
)

// syncStatsRecorder updates actual_sync_stats off the sync hot path. The
// bounded queue drops summaries rather than blocking a client request.
type syncStatsRecorder struct {
	store   *store.Store
	engine  *syncdb.Engine
	mu      sync.Mutex
	stopped bool
	ch      chan store.SyncStats
	done    chan struct{}
}

func newSyncStatsRecorder(s *store.Store, engine *syncdb.Engine) *syncStatsRecorder {
	recorder := &syncStatsRecorder{
		store:  s,
		engine: engine,
		ch:     make(chan store.SyncStats, 64),
		done:   make(chan struct{}),
	}
	go recorder.run()
	return recorder
}

func (r *syncStatsRecorder) run() {
	defer close(r.done)
	for stats := range r.ch {
		r.engine.TryOutsideBackup(func() {
			// Stats are advisory; a failed write must not affect sync.
			_ = r.store.UpsertSyncStats(stats)
		})
	}
}

func (r *syncStatsRecorder) Enqueue(stats store.SyncStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	select {
	case r.ch <- stats:
	default:
	}
}

func (r *syncStatsRecorder) Stop() {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.ch)
	}
	r.mu.Unlock()
	<-r.done
}
