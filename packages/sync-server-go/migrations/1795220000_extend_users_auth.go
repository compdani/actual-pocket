// Migration 1795220000 extends PocketBase's built-in `users` auth collection
// with the fields and auth options Actual needs (D2, D8).
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// authTokenDuration is 365 days; the owner can change it in the admin UI.
const authTokenDuration = 365 * 24 * 60 * 60

func init() {
	pbmigrations.Register(extendUsersUp, extendUsersDown)
}

func extendUsersUp(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	// D8: no user management through PocketBase's own API. The admin UI and our
	// own /admin routes (superuser-only) manage records.
	users.ListRule = nil
	users.ViewRule = nil
	users.CreateRule = nil
	users.UpdateRule = nil
	users.DeleteRule = nil

	// Only enabled users may authenticate, through password or OAuth2.
	users.AuthRule = types.Pointer("enabled = true")

	users.PasswordAuth.Enabled = true
	users.PasswordAuth.IdentityFields = []string{"email"}
	users.OTP.Enabled = false
	users.MFA.Enabled = false

	// Node never emailed users on a new device; keep PocketBase quiet too.
	users.AuthAlert.Enabled = false

	users.AuthToken.Duration = authTokenDuration

	// OAuth2 stays enabled with no providers: the owner adds them in the
	// admin UI and /account/login-methods picks them up.
	users.OAuth2.Enabled = true

	if password, ok := users.Fields.GetByName("password").(*core.PasswordField); ok {
		password.Min = 8
	}

	// The built-in `name` field is Actual's display_name; `email` is its
	// user_name.
	users.Fields.Add(&core.SelectField{
		Name:      "role",
		Values:    []string{"ADMIN", "BASIC"},
		MaxSelect: 1,
		Required:  true,
	})
	users.Fields.Add(&core.BoolField{
		Name: "enabled",
	})
	users.Fields.Add(&core.BoolField{
		Name: "owner",
	})

	return app.Save(users)
}

func extendUsersDown(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	users.Fields.RemoveByName("role")
	users.Fields.RemoveByName("enabled")
	users.Fields.RemoveByName("owner")

	// Restore PocketBase's defaults for a freshly created auth collection.
	ownerRule := types.Pointer("id = @request.auth.id")
	users.ListRule = ownerRule
	users.ViewRule = ownerRule
	users.CreateRule = types.Pointer("")
	users.UpdateRule = ownerRule
	users.DeleteRule = ownerRule
	users.AuthRule = types.Pointer("")

	users.PasswordAuth.Enabled = true
	users.PasswordAuth.IdentityFields = []string{"email"}
	users.OTP.Enabled = false
	users.MFA.Enabled = false
	users.AuthToken.Duration = 432000
	users.OAuth2.Enabled = false
	users.AuthAlert.Enabled = true

	if password, ok := users.Fields.GetByName("password").(*core.PasswordField); ok {
		password.Min = 8
	}

	return app.Save(users)
}
