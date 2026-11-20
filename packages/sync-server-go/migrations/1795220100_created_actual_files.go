// Migration 1795220100 creates `actual_files`, the Node server's `files` table
// plus the budget blob (D4, D9).
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(createActualFilesUp, createActualFilesDown)
}

func createActualFilesUp(app core.App) error {
	usersID, err := collectionID(app, "users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	col := core.NewBaseCollection("actual_files")

	// D9: the client generates the budget id and sends it in x-actual-file-id,
	// so the id has to accept what clients already use.
	id := col.Fields.GetByName("id").(*core.TextField)
	id.Pattern = `^[a-zA-Z0-9_-]+$`
	id.Min = 1
	id.Max = 100

	col.Fields.Add(&core.TextField{
		Name: "group_id",
	})
	col.Fields.Add(&core.NumberField{
		Name: "sync_version",
	})
	col.Fields.Add(&core.TextField{
		Name: "encrypt_meta",
	})
	col.Fields.Add(&core.TextField{
		Name: "encrypt_keyid",
	})
	col.Fields.Add(&core.TextField{
		Name: "encrypt_salt",
	})
	col.Fields.Add(&core.TextField{
		Name: "encrypt_test",
	})
	col.Fields.Add(&core.BoolField{
		Name: "deleted",
	})
	col.Fields.Add(&core.TextField{
		Name: "name",
	})
	// The owner is a user; deleting a user must not delete their budgets.
	col.Fields.Add(relationTo("owner", usersID, false))
	col.Fields.Add(&core.FileField{
		Name:      "blob",
		MaxSelect: 1,
		MaxSize:   blobMaxSize(),
		Protected: true,
	})
	addAutodateFields(col)

	col.AddIndex("idx_actual_files_owner", false, "`owner`", "")
	col.AddIndex("idx_actual_files_group_id", false, "`group_id`", "")

	return app.Save(col)
}

func createActualFilesDown(app core.App) error {
	return deleteCollection(app, "actual_files")
}
