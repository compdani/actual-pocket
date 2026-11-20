// Migration 1795220300 creates `actual_revoked_tokens`, the per-token logout
// denylist (D6).
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualRevokedTokensUp, createActualRevokedTokensDown)
}

func createActualRevokedTokensUp(app core.App) error {
	col := core.NewBaseCollection("actual_revoked_tokens")

	col.Fields.Add(&core.TextField{
		Name:     "token_hash",
		Required: true,
	})
	// Unix seconds taken from the revoked JWT's exp claim.
	col.Fields.Add(&core.NumberField{
		Name:    "expires_at",
		OnlyInt: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_revoked_tokens_hash", true, "`token_hash`", "")
	col.AddIndex("idx_actual_revoked_tokens_expires_at", false, "`expires_at`", "")

	return app.Save(col)
}

func createActualRevokedTokensDown(app core.App) error {
	return deleteCollection(app, "actual_revoked_tokens")
}
