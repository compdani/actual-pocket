// Migration 1795220500 creates `actual_secrets`, the bank sync credentials
// store (Node's `secrets`). The name is either `<key>` (global) or
// `<key>:<fileId>` (per budget), so it needs its own unique index.
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualSecretsUp, createActualSecretsDown)
}

func createActualSecretsUp(app core.App) error {
	col := core.NewBaseCollection("actual_secrets")

	col.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
	})
	// Node stores a BLOB, but every caller writes a string.
	col.Fields.Add(&core.TextField{
		Name:   "value",
		Hidden: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_secrets_name", true, "`name`", "")

	return app.Save(col)
}

func createActualSecretsDown(app core.App) error {
	return deleteCollection(app, "actual_secrets")
}
