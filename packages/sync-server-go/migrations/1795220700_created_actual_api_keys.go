// Migration 1795220700 creates `actual_api_keys`, long-lived budget sync
// credentials owned by a user.
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualAPIKeysUp, createActualAPIKeysDown)
}

func createActualAPIKeysUp(app core.App) error {
	usersID, err := collectionID(app, "users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	col := core.NewBaseCollection("actual_api_keys")

	col.Fields.Add(relationTo("user_id", usersID, true))
	col.Fields.Add(&core.TextField{
		Name:     "name",
		Required: true,
	})
	col.Fields.Add(&core.TextField{
		Name:     "key_hash",
		Hidden:   true,
		Required: true,
	})
	// Unix seconds, the same unit Node's app-api-keys.ts stores.
	col.Fields.Add(&core.NumberField{
		Name:    "created_at",
		OnlyInt: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_api_keys_hash", true, "`key_hash`", "")
	col.AddIndex("idx_actual_api_keys_user_id", false, "`user_id`", "")

	return app.Save(col)
}

func createActualAPIKeysDown(app core.App) error {
	return deleteCollection(app, "actual_api_keys")
}
