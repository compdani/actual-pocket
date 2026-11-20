package migrations

import (
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// newApp bootstraps a PocketBase app in a throwaway data dir without running
// the app migrations.
func newApp(t *testing.T) core.App {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  filepath.Join(t.TempDir(), "pb_data"),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() {
		_ = app.ResetBootstrapState()
	})

	return app
}

func migratedApp(t *testing.T) core.App {
	t.Helper()

	app := newApp(t)
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return app
}

func fieldNames(col *core.Collection) []string {
	names := make([]string, 0, len(col.Fields))
	for _, field := range col.Fields {
		names = append(names, field.GetName())
	}
	return names
}

func assertFieldNames(t *testing.T, col *core.Collection, want ...string) {
	t.Helper()

	got := fieldNames(col)
	wantSet := map[string]bool{}
	for _, name := range want {
		wantSet[name] = true
	}
	for _, name := range got {
		if !wantSet[name] {
			t.Errorf("%s: unexpected field %q (fields: %v)", col.Name, name, got)
		}
		delete(wantSet, name)
	}
	for name := range wantSet {
		t.Errorf("%s: missing field %q (fields: %v)", col.Name, name, got)
	}
}

func assertSuperuserOnly(t *testing.T, col *core.Collection) {
	t.Helper()

	rules := map[string]*string{
		"list":   col.ListRule,
		"view":   col.ViewRule,
		"create": col.CreateRule,
		"update": col.UpdateRule,
		"delete": col.DeleteRule,
	}
	for name, rule := range rules {
		if rule != nil {
			t.Errorf("%s: %sRule = %q, want nil (superuser only)", col.Name, name, *rule)
		}
	}
}

func assertIndex(t *testing.T, col *core.Collection, name string, unique bool, columns ...string) {
	t.Helper()

	found := false
	for _, index := range col.Indexes {
		if !strings.Contains(index, "`"+name+"`") {
			continue
		}
		found = true
		if got := strings.Contains(index, "UNIQUE"); got != unique {
			t.Errorf("%s: index %s unique = %v, want %v (%s)", col.Name, name, got, unique, index)
		}
		for _, column := range columns {
			if !strings.Contains(index, "`"+column+"`") {
				t.Errorf("%s: index %s is missing column %s (%s)", col.Name, name, column, index)
			}
		}
	}
	if !found {
		t.Errorf("%s: missing index %s (indexes: %v)", col.Name, name, col.Indexes)
	}
}

func TestCollections(t *testing.T) {
	app := migratedApp(t)

	type expectation struct {
		name    string
		fields  []string
		indexes func(t *testing.T, col *core.Collection)
	}

	expectations := []expectation{
		{
			name:   "actual_files",
			fields: []string{"id", "group_id", "sync_version", "encrypt_meta", "encrypt_keyid", "encrypt_salt", "encrypt_test", "deleted", "name", "owner", "blob", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_files_owner", false, "owner")
				assertIndex(t, col, "idx_actual_files_group_id", false, "group_id")
			},
		},
		{
			name:   "actual_user_access",
			fields: []string{"id", "user_id", "file_id", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_user_access_unique", true, "user_id", "file_id")
			},
		},
		{
			name:   "actual_revoked_tokens",
			fields: []string{"id", "token_hash", "expires_at", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_revoked_tokens_hash", true, "token_hash")
				assertIndex(t, col, "idx_actual_revoked_tokens_expires_at", false, "expires_at")
			},
		},
		{
			name:   "actual_pending_oauth",
			fields: []string{"id", "state", "provider", "code_verifier", "return_url", "expires_at", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_pending_oauth_state", true, "state")
			},
		},
		{
			name:   "actual_secrets",
			fields: []string{"id", "name", "value", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_secrets_name", true, "name")
			},
		},
		{
			name:   "actual_server_prefs",
			fields: []string{"id", "key", "value", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_server_prefs_key", true, "key")
			},
		},
		{
			name:   "actual_api_keys",
			fields: []string{"id", "user_id", "name", "key_hash", "created_at", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_api_keys_hash", true, "key_hash")
				assertIndex(t, col, "idx_actual_api_keys_user_id", false, "user_id")
			},
		},
		{
			name:   "actual_api_key_budgets",
			fields: []string{"id", "key_id", "file_id", "created", "updated"},
			indexes: func(t *testing.T, col *core.Collection) {
				assertIndex(t, col, "idx_actual_api_key_budgets_unique", true, "key_id", "file_id")
			},
		},
	}

	for _, expectation := range expectations {
		t.Run(expectation.name, func(t *testing.T) {
			col, err := app.FindCollectionByNameOrId(expectation.name)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if col.Type != core.CollectionTypeBase {
				t.Errorf("type = %q, want base", col.Type)
			}
			assertFieldNames(t, col, expectation.fields...)
			assertSuperuserOnly(t, col)
			expectation.indexes(t, col)
		})
	}
}

func TestActualFilesFieldDetails(t *testing.T) {
	app := migratedApp(t)

	col, err := app.FindCollectionByNameOrId("actual_files")
	if err != nil {
		t.Fatal(err)
	}

	id, ok := col.Fields.GetByName("id").(*core.TextField)
	if !ok {
		t.Fatal("id field is not a text field")
	}
	if !id.PrimaryKey {
		t.Error("id.PrimaryKey = false, want true")
	}
	if id.Pattern != `^[a-zA-Z0-9_-]+$` {
		t.Errorf("id.Pattern = %q", id.Pattern)
	}
	if id.Min != 1 || id.Max != 100 {
		t.Errorf("id min/max = %d/%d, want 1/100", id.Min, id.Max)
	}
	if id.AutogeneratePattern == "" {
		t.Error("id.AutogeneratePattern is empty, want the default generator kept")
	}

	owner, ok := col.Fields.GetByName("owner").(*core.RelationField)
	if !ok {
		t.Fatal("owner field is not a relation field")
	}
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	if owner.CollectionId != users.Id {
		t.Errorf("owner.CollectionId = %q, want the users collection", owner.CollectionId)
	}
	if owner.CascadeDelete {
		t.Error("owner.CascadeDelete = true, want false: deleting a user must not delete budgets")
	}
	if owner.MaxSelect != 1 {
		t.Errorf("owner.MaxSelect = %d, want 1", owner.MaxSelect)
	}

	blob, ok := col.Fields.GetByName("blob").(*core.FileField)
	if !ok {
		t.Fatal("blob field is not a file field")
	}
	if !blob.Protected {
		t.Error("blob.Protected = false, want true")
	}
	if blob.MaxSelect != 1 {
		t.Errorf("blob.MaxSelect = %d, want 1", blob.MaxSelect)
	}
	if want := int64(50) << 20; blob.MaxSize != want {
		t.Errorf("blob.MaxSize = %d, want %d", blob.MaxSize, want)
	}
	if len(blob.MimeTypes) != 0 {
		t.Errorf("blob.MimeTypes = %v, want any mime type", blob.MimeTypes)
	}

	// The sync_version is a plain number.
	if _, ok := col.Fields.GetByName("sync_version").(*core.NumberField); !ok {
		t.Error("sync_version is not a number field")
	}

	// The group id and encrypt_* fields are text.
	for _, name := range []string{"group_id", "encrypt_meta", "encrypt_keyid", "encrypt_salt", "encrypt_test", "name"} {
		if _, ok := col.Fields.GetByName(name).(*core.TextField); !ok {
			t.Errorf("%s is not a text field", name)
		}
	}
	if _, ok := col.Fields.GetByName("deleted").(*core.BoolField); !ok {
		t.Error("deleted is not a bool field")
	}
}

func TestUserAccessAndAPIKeyRelations(t *testing.T) {
	app := migratedApp(t)

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	files, err := app.FindCollectionByNameOrId("actual_files")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := app.FindCollectionByNameOrId("actual_api_keys")
	if err != nil {
		t.Fatal(err)
	}

	access, err := app.FindCollectionByNameOrId("actual_user_access")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"user_id": users.Id, "file_id": files.Id} {
		field := access.Fields.GetByName(name).(*core.RelationField)
		if field.CollectionId != want {
			t.Errorf("actual_user_access.%s points at %q, want %q", name, field.CollectionId, want)
		}
		if !field.CascadeDelete {
			t.Errorf("actual_user_access.%s.CascadeDelete = false, want true", name)
		}
	}

	budgets, err := app.FindCollectionByNameOrId("actual_api_key_budgets")
	if err != nil {
		t.Fatal(err)
	}
	keyField := budgets.Fields.GetByName("key_id").(*core.RelationField)
	if keyField.CollectionId != keys.Id {
		t.Errorf("actual_api_key_budgets.key_id points at %q, want actual_api_keys", keyField.CollectionId)
	}
	if !keyField.CascadeDelete {
		t.Error("actual_api_key_budgets.key_id.CascadeDelete = false, want true")
	}
	// file_id intentionally stays plain text: Node has no foreign key there.
	if _, ok := budgets.Fields.GetByName("file_id").(*core.TextField); !ok {
		t.Error("actual_api_key_budgets.file_id is not a text field")
	}

	apiKeys, err := app.FindCollectionByNameOrId("actual_api_keys")
	if err != nil {
		t.Fatal(err)
	}
	userField := apiKeys.Fields.GetByName("user_id").(*core.RelationField)
	if userField.CollectionId != users.Id || !userField.CascadeDelete {
		t.Errorf("actual_api_keys.user_id = %+v, want a cascade relation to users", userField)
	}
	if !apiKeys.Fields.GetByName("key_hash").(*core.TextField).Hidden {
		t.Error("actual_api_keys.key_hash should be hidden")
	}
}

func TestHiddenSecrets(t *testing.T) {
	app := migratedApp(t)

	secrets, err := app.FindCollectionByNameOrId("actual_secrets")
	if err != nil {
		t.Fatal(err)
	}
	if !secrets.Fields.GetByName("value").(*core.TextField).Hidden {
		t.Error("actual_secrets.value should be hidden")
	}

	oauth, err := app.FindCollectionByNameOrId("actual_pending_oauth")
	if err != nil {
		t.Fatal(err)
	}
	if !oauth.Fields.GetByName("code_verifier").(*core.TextField).Hidden {
		t.Error("actual_pending_oauth.code_verifier should be hidden")
	}
}

func TestUsersAuthOptions(t *testing.T) {
	app := migratedApp(t)

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}

	assertSuperuserOnly(t, users)

	if users.AuthRule == nil || *users.AuthRule != "enabled = true" {
		t.Errorf("AuthRule = %v, want \"enabled = true\"", users.AuthRule)
	}
	if !users.PasswordAuth.Enabled {
		t.Error("PasswordAuth.Enabled = false, want true")
	}
	if got := users.PasswordAuth.IdentityFields; len(got) != 1 || got[0] != "email" {
		t.Errorf("IdentityFields = %v, want [email]", got)
	}
	if users.OTP.Enabled {
		t.Error("OTP.Enabled = true, want false")
	}
	if users.MFA.Enabled {
		t.Error("MFA.Enabled = true, want false")
	}
	if users.AuthAlert.Enabled {
		t.Error("AuthAlert.Enabled = true, want false: Node never emailed on new device logins")
	}
	if got := users.AuthToken.Duration; got != 31536000 {
		t.Errorf("AuthToken.Duration = %d, want 31536000 (365 days)", got)
	}
	if !users.OAuth2.Enabled {
		t.Error("OAuth2.Enabled = false, want true")
	}
	if len(users.OAuth2.Providers) != 0 {
		t.Errorf("OAuth2.Providers = %v, want none configured by default", users.OAuth2.Providers)
	}

	password, ok := users.Fields.GetByName("password").(*core.PasswordField)
	if !ok {
		t.Fatal("password field is not a password field")
	}
	if password.Min != 8 {
		t.Errorf("password.Min = %d, want 8", password.Min)
	}

	role, ok := users.Fields.GetByName("role").(*core.SelectField)
	if !ok {
		t.Fatal("role field is not a select field")
	}
	if strings.Join(role.Values, ",") != "ADMIN,BASIC" {
		t.Errorf("role.Values = %v, want ADMIN,BASIC", role.Values)
	}
	if role.MaxSelect != 1 || !role.Required {
		t.Errorf("role = %+v, want a required single select", role)
	}

	for _, name := range []string{"enabled", "owner"} {
		if _, ok := users.Fields.GetByName(name).(*core.BoolField); !ok {
			t.Errorf("%s is not a bool field", name)
		}
	}

	// Actual maps display_name to the built-in name and user_name to email.
	for _, name := range []string{"name", "email", "verified"} {
		if users.Fields.GetByName(name) == nil {
			t.Errorf("users is missing the built-in %s field", name)
		}
	}
}

func TestSettings(t *testing.T) {
	app := migratedApp(t)

	settings := app.Settings()

	if !settings.RateLimits.Enabled {
		t.Error("RateLimits.Enabled = false, want true")
	}

	// The password and OAuth2 endpoints get Node's 5-per-15-minutes budget.
	for _, label := range []string{"*:authWithPassword", "*:authWithOAuth2"} {
		rule, ok := settings.RateLimits.FindRateLimitRule([]string{label})
		if !ok {
			t.Errorf("no %s rate limit rule (rules: %v)", label, settings.RateLimits.Rules)
			continue
		}
		if rule.MaxRequests != 5 || rule.Duration != 900 {
			t.Errorf("%s rule = %d requests / %ds, want 5 / 900", label, rule.MaxRequests, rule.Duration)
		}
	}

	// `*:auth` also covers auth-refresh, so it keeps PocketBase's default.
	authRule, ok := settings.RateLimits.FindRateLimitRule([]string{"*:auth"})
	if !ok {
		t.Fatalf("no *:auth rate limit rule (rules: %v)", settings.RateLimits.Rules)
	}
	if authRule.MaxRequests != 2 || authRule.Duration != 3 {
		t.Errorf("*:auth rule = %d requests / %ds, want PocketBase's default 2 / 3", authRule.MaxRequests, authRule.Duration)
	}

	// PocketBase's other default rules are kept.
	for _, label := range []string{"*:create", "/api/batch", "/api/"} {
		if _, ok := settings.RateLimits.FindRateLimitRule([]string{label}); !ok {
			t.Errorf("missing default rate limit rule %q", label)
		}
	}

	if settings.Backups.Cron != "0 3 * * *" {
		t.Errorf("Backups.Cron = %q, want 0 3 * * *", settings.Backups.Cron)
	}
	if settings.Backups.CronMaxKeep != 7 {
		t.Errorf("Backups.CronMaxKeep = %d, want 7", settings.Backups.CronMaxKeep)
	}
	if settings.Backups.S3.Enabled {
		t.Error("Backups.S3.Enabled = true, want false")
	}
}

func TestDownUpRoundTrip(t *testing.T) {
	app := migratedApp(t)

	list := core.MigrationsList{}
	list.Copy(core.AppMigrations)

	if _, err := core.NewMigrationsRunner(app, list).Down(len(list.Items())); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := app.ReloadCachedCollections(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"actual_files",
		"actual_user_access",
		"actual_revoked_tokens",
		"actual_pending_oauth",
		"actual_secrets",
		"actual_server_prefs",
		"actual_api_keys",
		"actual_api_key_budgets",
	} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Errorf("%s still exists after migrate down", name)
		}
	}

	// The users collection is PocketBase's own, so it survives; the Actual
	// fields are gone.
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection disappeared: %v", err)
	}
	for _, name := range []string{"role", "enabled", "owner"} {
		if users.Fields.GetByName(name) != nil {
			t.Errorf("users still has %q after migrate down", name)
		}
	}
	if users.ListRule == nil {
		t.Error("users.ListRule is still nil after migrate down")
	}

	// And back up again.
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	if err := app.ReloadCachedCollections(); err != nil {
		t.Fatal(err)
	}

	col, err := app.FindCollectionByNameOrId("actual_files")
	if err != nil {
		t.Fatalf("actual_files missing after re-applying: %v", err)
	}
	assertSuperuserOnly(t, col)

	users, err = app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	if users.AuthRule == nil || *users.AuthRule != "enabled = true" {
		t.Errorf("AuthRule = %v after re-applying, want \"enabled = true\"", users.AuthRule)
	}
	if users.Fields.GetByName("role") == nil {
		t.Error("users.role is missing after re-applying")
	}
}

func TestBlobMaxSizeFollowsConfig(t *testing.T) {
	t.Cleanup(func() { SetSyncEncryptedFileSizeLimitMB(defaultSyncEncryptedFileSizeLimitMB) })

	SetSyncEncryptedFileSizeLimitMB(123)
	app := migratedApp(t)

	col, err := app.FindCollectionByNameOrId("actual_files")
	if err != nil {
		t.Fatal(err)
	}
	blob := col.Fields.GetByName("blob").(*core.FileField)
	if want := int64(123) << 20; blob.MaxSize != want {
		t.Errorf("blob.MaxSize = %d, want %d", blob.MaxSize, want)
	}

	// Zero keeps the previous value instead of producing an unusable field.
	SetSyncEncryptedFileSizeLimitMB(0)
	if got := blobMaxSize(); got != int64(123)<<20 {
		t.Errorf("blobMaxSize after zero = %d, want the previous limit", got)
	}
}
