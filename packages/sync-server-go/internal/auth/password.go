package auth

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// FieldNamePasswordSet marks users who chose a password. OAuth2-only users get
// a random password nobody knows, so `password_set` stays false and the client
// hides the change-password form.
const FieldNamePasswordSet = "password_set"

// SetPassword sets a real password and marks the user as having one.
func SetPassword(record *core.Record, password string) {
	record.SetPassword(password)
	record.Set(FieldNamePasswordSet, true)
}

// SetRandomPassword sets an unusable random password (an OAuth2-only user) and
// clears password_set.
func SetRandomPassword(record *core.Record) {
	record.SetRandomPassword()
	record.Set(FieldNamePasswordSet, false)
}
