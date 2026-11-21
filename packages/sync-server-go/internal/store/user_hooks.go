package store

import (
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
)

// ErrOwnerInvariant is returned when a change would break the owner guarantees.
var ErrOwnerInvariant = errors.New("the server owner must stay an enabled ADMIN")

// RegisterUserHooks installs the user invariants as model hooks, so they hold
// no matter who saves the record: the Go routes, PocketBase's REST API, the
// admin UI, MCP or the CLI. Superusers bypass the API rules but not these.
func (s *Store) RegisterUserHooks(app core.App) {
	app.OnRecordCreate("users").BindFunc(func(e *core.RecordEvent) error {
		// Users created with a chosen password through PocketBase's API or
		// admin UI can't send password_set themselves (the create rule forbids
		// it), so derive it here. SetRandomPassword clears the plain value, so
		// OAuth2-only users stay false.
		applyPasswordSetFlag(e.Record)

		if !e.Record.GetBool("owner") {
			return e.Next()
		}

		// The very first user is the owner; nobody else can become one.
		count, err := e.App.CountRecords("users")
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if count > 0 {
			return errors.New("only the first user can be the server owner")
		}

		return e.Next()
	})

	app.OnRecordUpdate("users").BindFunc(func(e *core.RecordEvent) error {
		record := e.Record
		original := record.Original()

		if original.GetBool("owner") {
			if !record.GetBool("owner") || record.GetString("role") != "ADMIN" || !record.GetBool("enabled") {
				return ErrOwnerInvariant
			}
		} else if record.GetBool("owner") {
			return errors.New("the owner flag cannot be changed")
		}

		applyPasswordSetFlag(record)

		return e.Next()
	})

	app.OnRecordDelete("users").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetBool("owner") {
			return errors.New("the server owner cannot be deleted")
		}

		return e.Next()
	})

	app.OnRecordDeleteExecute("users").Bind(&hook.Handler[*core.RecordEvent]{
		// PocketBase v0.41.1 opens its delete/cascade transaction at priority 99.
		Priority: 100,
		Func: func(e *core.RecordEvent) error {
			// Files do not cascade, so hand them to the owner before the delete.
			txStore := s.With(e.App)
			if ownerID := txStore.OwnerID(); ownerID != "" {
				if err := txStore.TransferFiles(e.Record.Id, ownerID); err != nil {
					return fmt.Errorf("transfer files from deleted user: %w", err)
				}
			}
			return e.Next()
		},
	})
}

// applyPasswordSetFlag marks a user who chose a password. SetPassword already
// does this, but PocketBase's own endpoints (the admin UI, password reset) set
// a plaintext password directly, so mirror it here. Random passwords are
// cleared by SetRandomPassword, which the OAuth2-only paths use.
func applyPasswordSetFlag(record *core.Record) {
	current, ok := record.GetRaw(core.FieldNamePassword).(*core.PasswordFieldValue)
	if !ok || current.Plain == "" {
		return
	}

	record.Set(FieldPasswordSet, true)
}
