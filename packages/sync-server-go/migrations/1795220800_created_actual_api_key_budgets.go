// Migration 1795220800 creates `actual_api_key_budgets`, the budgets an API
// key may sync. Node has no foreign key on file_id, so it stays plain text
// here too (a budget can be reset and recreated).
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualAPIKeyBudgetsUp, createActualAPIKeyBudgetsDown)
}

func createActualAPIKeyBudgetsUp(app core.App) error {
	keysID, err := collectionID(app, "actual_api_keys")
	if err != nil {
		return fmt.Errorf("find actual_api_keys collection: %w", err)
	}

	col := core.NewBaseCollection("actual_api_key_budgets")
	col.Fields.Add(relationTo("key_id", keysID, true))
	col.Fields.Add(&core.TextField{
		Name:     "file_id",
		Required: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_api_key_budgets_unique", true, "`key_id`, `file_id`", "")

	return app.Save(col)
}

func createActualAPIKeyBudgetsDown(app core.App) error {
	return deleteCollection(app, "actual_api_key_budgets")
}
