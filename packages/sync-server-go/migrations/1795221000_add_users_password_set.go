// Migration 1795221000 adds `password_set` to the users collection, so the
// server can tell a user who chose a password from an OAuth2-only user whose
// password is random and unknown.
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

func init() {
	pbmigrations.Register(addUsersPasswordSetUp, addUsersPasswordSetDown)
}

func addUsersPasswordSetUp(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	users.Fields.Add(&core.BoolField{
		Name: "password_set",
	})

	if err := app.Save(users); err != nil {
		return err
	}

	// Every user that existed before this migration was created with a real
	// password (bootstrap and /admin users), so backfill them as such.
	records, err := app.FindAllRecords("users")
	if err != nil {
		return err
	}
	for _, record := range records {
		record.Set("password_set", true)
		if err := app.Save(record); err != nil {
			return err
		}
	}

	return nil
}

func addUsersPasswordSetDown(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	users.Fields.RemoveByName("password_set")

	return app.Save(users)
}
