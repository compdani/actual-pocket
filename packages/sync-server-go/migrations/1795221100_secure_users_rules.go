// Migration 1795221100 replaces the ad-hoc user permission checks with one set
// of PocketBase API rules on the users collection, so PocketBase's own
// /api/collections/users endpoints and the Go admin routes enforce the same
// hierarchy: owner > ADMIN > BASIC.
package migrations

import (
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	pbmigrations "ganigeorgiev.com/pocketbase/pocketbase/migrations"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// userAuthPrefix is true for an enabled user of the users collection.
const userAuthPrefix = `@request.auth.collectionName = "users" && @request.auth.enabled = true`

// Hierarchy:
//   - an owner may edit/delete anyone, but cannot demote, disable, delete
//     themselves or let anyone else become the owner;
//   - an ADMIN may edit/delete BASIC users and edit their own profile, but
//     cannot touch other admins or the owner, or promote anyone to ADMIN;
//   - a BASIC user may only edit their own profile (never role or enabled).
var (
	userListRule   = userAuthPrefix + ` && (@request.auth.role = "ADMIN" || @request.auth.owner = true || id = @request.auth.id)`
	userCreateRule = userAuthPrefix + ` && @request.body.owner:isset = false && @request.body.password_set:isset = false && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && @request.body.role = "BASIC"))`
	userUpdateRule = userAuthPrefix + ` && @request.body.owner:changed = false && @request.body.password_set:changed = false && (` +
		`(@request.auth.owner = true && (owner = false || (@request.body.role:changed = false && @request.body.enabled:changed = false))) || ` +
		`(@request.auth.role = "ADMIN" && owner = false && (id = @request.auth.id || role = "BASIC") && (@request.body.role:changed = false || @request.body.role = "BASIC") && (id != @request.auth.id || @request.body.enabled:changed = false)) || ` +
		`(id = @request.auth.id && @request.body.role:changed = false && @request.body.enabled:changed = false)` +
		`)`
	userDeleteRule = userAuthPrefix + ` && owner = false && id != @request.auth.id && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && role = "BASIC"))`
	// ManageRule allows changing another user's email or password without their
	// current password.
	userManageRule = userAuthPrefix + ` && id != @request.auth.id && owner = false && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && role = "BASIC"))`
)

func init() {
	pbmigrations.Register(secureUsersRulesUp, secureUsersRulesDown)
}

func secureUsersRulesUp(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	users.ListRule = types.Pointer(userListRule)
	users.ViewRule = types.Pointer(userListRule)
	users.CreateRule = types.Pointer(userCreateRule)
	users.UpdateRule = types.Pointer(userUpdateRule)
	users.DeleteRule = types.Pointer(userDeleteRule)
	users.ManageRule = types.Pointer(userManageRule)

	return app.Save(users)
}

func secureUsersRulesDown(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("find users collection: %w", err)
	}

	users.ListRule = nil
	users.ViewRule = nil
	users.CreateRule = nil
	users.UpdateRule = nil
	users.DeleteRule = nil
	users.ManageRule = nil

	return app.Save(users)
}
