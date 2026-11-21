// Migration 1795220400 creates `actual_pending_oauth`, the single-use state
// store for OAuth2 logins (Node's `pending_openid_requests`).
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualPendingOAuthUp, createActualPendingOAuthDown)
}

func createActualPendingOAuthUp(app core.App) error {
	col := core.NewBaseCollection("actual_pending_oauth")

	col.Fields.Add(&core.TextField{
		Name:     "state",
		Required: true,
	})
	col.Fields.Add(&core.TextField{
		Name: "provider",
	})
	col.Fields.Add(&core.TextField{
		Name:   "code_verifier",
		Hidden: true,
	})
	col.Fields.Add(&core.TextField{
		Name: "return_url",
	})
	// Unix seconds; rows are pruned by the hourly cron.
	col.Fields.Add(&core.NumberField{
		Name:    "expires_at",
		OnlyInt: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_pending_oauth_state", true, "`state`", "")

	return app.Save(col)
}

func createActualPendingOAuthDown(app core.App) error {
	return deleteCollection(app, "actual_pending_oauth")
}
