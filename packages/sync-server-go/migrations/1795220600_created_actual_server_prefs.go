// Migration 1795220600 creates `actual_server_prefs`, the global server
// preferences key/value store (Node's `server_prefs`).
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualServerPrefsUp, createActualServerPrefsDown)
}

func createActualServerPrefsUp(app core.App) error {
	col := core.NewBaseCollection("actual_server_prefs")

	col.Fields.Add(&core.TextField{
		Name:     "key",
		Required: true,
	})
	col.Fields.Add(&core.TextField{
		Name: "value",
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_server_prefs_key", true, "`key`", "")

	return app.Save(col)
}

func createActualServerPrefsDown(app core.App) error {
	return deleteCollection(app, "actual_server_prefs")
}
