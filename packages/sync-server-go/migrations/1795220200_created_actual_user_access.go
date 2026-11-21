// Migration 1795220200 creates `actual_user_access`, the budget sharing join
// table (Node's `user_access`).
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualUserAccessUp, createActualUserAccessDown)
}

func createActualUserAccessUp(app core.App) error {
	usersID, err := collectionID(app, "users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}
	filesID, err := collectionID(app, "actual_files")
	if err != nil {
		return fmt.Errorf("find actual_files collection: %w", err)
	}

	col := core.NewBaseCollection("actual_user_access")
	col.Fields.Add(relationTo("user_id", usersID, true))
	col.Fields.Add(relationTo("file_id", filesID, true))
	addAutodateFields(col)

	col.AddIndex("idx_actual_user_access_unique", true, "`user_id`, `file_id`", "")

	return app.Save(col)
}

func createActualUserAccessDown(app core.App) error {
	return deleteCollection(app, "actual_user_access")
}
