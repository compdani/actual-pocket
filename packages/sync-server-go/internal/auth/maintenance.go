package auth

import (
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/store"
)

// RegisterMaintenance prunes expired logout denylist rows and stale OAuth2
// login state once an hour.
func RegisterMaintenance(app core.App, s *store.Store) {
	app.Cron().MustAdd("actualPruneRevoked", "0 * * * *", func() {
		now := time.Now().Unix()

		revoked, err := s.PruneRevokedTokens(now)
		if err != nil {
			app.Logger().Error("failed to prune revoked tokens", "error", err)
		} else if revoked > 0 {
			app.Logger().Info("pruned revoked tokens", "count", revoked)
		}

		pending, err := s.PrunePendingOAuth(now)
		if err != nil {
			app.Logger().Error("failed to prune pending oauth requests", "error", err)
		} else if pending > 0 {
			app.Logger().Info("pruned pending oauth requests", "count", pending)
		}
	})
}
