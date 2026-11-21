// Migration 1795221200 adds the read-only `actual_files_overview` view used by
// MCP.
package migrations

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualFilesOverviewUp, createActualFilesOverviewDown)
}

const actualFilesOverviewName = "actual_files_overview"

func createActualFilesOverviewUp(app core.App) error {
	col := core.NewViewCollection(actualFilesOverviewName)
	col.ViewQuery = `
		SELECT
			f.id AS id,
			f.name AS name,
			f.owner AS owner,
			u.email AS owner_email,
			u.name AS owner_name,
			f.deleted AS deleted,
			f.group_id AS group_id,
			f.sync_version AS sync_version,
			CAST((SELECT COUNT(*) FROM actual_user_access a WHERE a.file_id = f.id) AS INTEGER) AS access_count
		FROM actual_files f
		LEFT JOIN users u ON u.id = f.owner
	`

	return app.Save(col)
}

func createActualFilesOverviewDown(app core.App) error {
	if err := app.DeleteView(actualFilesOverviewName); err != nil {
		return nil
	}
	return deleteCollection(app, actualFilesOverviewName)
}
