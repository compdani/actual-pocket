# MCP Access to the Go Sync Server

This guide is for Actual Pocket, Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). Pull requests will most likely not be merged, and issues may or may not be fixed. See the [fork policy](../../CONTRIBUTING.md).

Use a non-superuser account for MCP where the client and the required operations
support it. Some clients require a PocketBase superuser. To create one for a
trusted client:

```bash
./actual-server superuser upsert mcp@localhost '<password>'
```

For long-lived agents, mint an impersonation token instead of storing the
password:

```bash
curl -X POST http://localhost:5006/api/collections/_superusers/impersonate/<id> \
  -H "Authorization: <superuser token>" \
  -H 'Content-Type: application/json' \
  -d '{"duration": 86400}'
```

All `actual_*` collections have `nil` API rules, which means **superusers
only**. For ordinary user accounts, the `users` collection enforces the
owner > ADMIN > BASIC hierarchy. PocketBase superusers bypass collection API
rules. The server's user model hooks still enforce owner invariants.

## Collections

| Collection              | Purpose                                        | Notes                                             |
| ----------------------- | ---------------------------------------------- | ------------------------------------------------- |
| `users`                 | Accounts and roles                             | Owner > ADMIN > BASIC; the owner is server-owned. |
| `actual_files`          | Budget blobs and metadata                      | `blob` is protected; group logs are not here.     |
| `actual_user_access`    | Budget shares                                  | One row per user/file pair.                       |
| `actual_secrets`        | Provider credentials                           | `value` is redacted from API responses.           |
| `actual_api_keys`       | Budget-scoped API keys                         | `key_hash` is redacted.                           |
| `actual_pending_oauth`  | In-flight OAuth2 state                         | `code_verifier` is redacted.                      |
| `actual_revoked_tokens` | Logged-out token hashes                        | Safe to delete expired rows.                      |
| `actual_server_prefs`   | Global preferences                             | Safe to edit.                                     |
| `actual_files_overview` | Read-only join of files, owner and share count | View collection.                                  |
| `actual_sync_stats`     | Last successful sync per group                 | Written off the sync hot path.                    |

## `actual_files_overview`

Read-only columns: `id`, `name`, `owner`, `owner_email`, `owner_name`,
`deleted`, `group_id`, `sync_version`, `access_count`.

## Redaction

`ACTUAL_MCP_REDACT_SECRETS` defaults to **true**. It hides
`actual_secrets.value`, `actual_api_keys.key_hash`, and
`actual_pending_oauth.code_verifier` from record responses, including those
sent to superusers. It also rejects direct record-list filters and sorts
that reference those fields. Saving a record with a blank redacted field
preserves its stored value. The Go server still reads the values it needs.

Redaction prevents accidental display. A full superuser can still access
secrets through direct SQL where enabled, custom view collections, or backup
downloads. Use a non-superuser account with only the required permissions
where possible. Set `ACTUAL_MCP_REDACT_SECRETS=false` only if a trusted client
needs to inspect the fields in record responses.

## Safe edits

- `users`: create, disable, change `role`, reset a password.
- `actual_user_access`: add or remove shares.
- `actual_server_prefs`: change values.
- `actual_revoked_tokens`: delete expired rows.

## Not safe

- Changing `owner`, demoting/disabling/deleting the owner, or flipping
  `password_set` on the owner.
- Editing `actual_files.id`, `group_id`, `encrypt_*`, `blob`, `owner` or
  `deleted` directly. Use the sync/delete APIs so the group database and files
  stay consistent.
- Editing `actual_sync_stats` (advisory only) or `actual_files_overview` (view).

Budget contents (transactions, categories, payees) are not exposed as
collections. For unencrypted budgets they are technically readable in the
uploaded blob and group logs; for encrypted budgets they are opaque.
