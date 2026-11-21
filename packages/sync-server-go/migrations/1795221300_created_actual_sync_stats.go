// Migration 1795221300 creates `actual_sync_stats`, one row per group with the
// latest successful sync summary. It is superuser-only, like every actual_*
// collection.
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualSyncStatsUp, createActualSyncStatsDown)
}

func createActualSyncStatsUp(app core.App) error {
	col := core.NewBaseCollection("actual_sync_stats")

	col.Fields.Add(&core.TextField{
		Name:     "group_id",
		Required: true,
	})
	col.Fields.Add(&core.TextField{
		Name: "file_id",
	})
	col.Fields.Add(&core.NumberField{
		Name:    "last_sync_at",
		OnlyInt: true,
	})
	col.Fields.Add(&core.NumberField{
		Name:    "last_message_count",
		OnlyInt: true,
	})
	col.Fields.Add(&core.TextField{
		Name: "merkle_hash",
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_sync_stats_group", true, "`group_id`", "")
	col.AddIndex("idx_actual_sync_stats_file", false, "`file_id`", "")

	return app.Save(col)
}

func createActualSyncStatsDown(app core.App) error {
	return deleteCollection(app, "actual_sync_stats")
}
