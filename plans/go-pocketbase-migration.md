# Plan: Port `packages/sync-server` (Node/Express) to Go + PocketBase

Target: `ganigeorgiev.com/pocketbase/pocketbase v0.41.1`, Go ≥ 1.27.
PocketBase moved to this vanity path. Never import the old
`github.com/pocketbase/pocketbase` path. Every import below is
`ganigeorgiev.com/pocketbase/pocketbase/...` (`core`, `apis`,
`plugins/migratecmd`, `tools/auth`, `tools/security`, `tools/filesystem`, …).

**Scope: a fresh install only.** No data will ever be imported from an existing
Actual/Node server, and nothing will ever be exported back to one. So:

- **Wire compatibility** with the web client and `@actual-app/api` in this repo
  is required (HTTP routes, status codes, bodies, headers, protobuf), **except
  for login**, which is redesigned around PocketBase users (Phase 6 changes the
  client to match).
- **Storage compatibility** with the Node server is **not** required: no
  importer, no Node password hashes, no Node rollback, no preserved IDs.

**Identity is PocketBase's.** Actual users are records in PocketBase's built-in
`users` **auth collection**. They log in with **email + password** or a PocketBase
**OAuth2 provider**, and every token the server issues or accepts is a **PocketBase
auth token**. Actual's shared server password, `sessions` table, header auth, and
its own OpenID config are gone.

This plan is written for an implementing agent. Choices marked **DECIDED** are
fixed: implement them as written and don't reopen them. Every phase ends with acceptance
criteria; do not start the next phase until they pass.

---

## 0. Ground rules for the implementer

1. **The TypeScript is the spec** for every non-auth endpoint. Open the Node
   handler and port it line by line: status codes, response bodies, header names,
   and quirks (for example, `file-not-found` returns **400**, not 404). The web client
   (`packages/loot-core`, `packages/desktop-client`) and `@actual-app/api` must keep
   working, and **the only client changes allowed are the ones listed in Phase 6**.
   If you find a bug in the Node code, port it as-is and add a
   `// NODE-PARITY:` comment. Don't fix it.
2. **Express response semantics you must mimic** (put them in `internal/httpx`):
   - `res.send(object)` → `application/json; charset=utf-8`.
   - `res.send('string')` → `text/html; charset=utf-8` with the raw string body.
     Many errors are plain strings (`'file-not-found'`, `'invalid fileId'`,
     `'file-access-not-allowed'`). Use `httpx.Text(e, status, s)` for these, never JSON.
   - Unhandled error → `500 {"status":"error","reason":"internal-error"}`.
   - `express.json()` only parses `application/json`; any other content type
     → body is `{}`. Malformed JSON → 400.
3. **Never write your own password hashing, session table, or token format.**
   Use `record.SetPassword`, `record.ValidatePassword`, `record.NewAuthToken()`,
   and `app.FindAuthRecordByToken(token, core.TokenTypeAuth)`. PocketBase owns all of it.
4. One PR per phase (or sub-phase). Each PR title starts with `[AI]`. Leave the
   PR template unfilled (see `.github/agents/pr-and-commit-rules.md`). Client
   changes (Phase 6) follow `AGENTS.md`: `Trans`/`t()` for every user-facing string,
   `yarn typecheck`, `yarn lint:fix`, and the workspace tests.
5. Before saying a Go phase is done, run `go vet ./... && go test ./...` from
   `packages/sync-server-go`. Keep the Node server in the repo until Phase 15,
   but only as a reference implementation and fixture generator, never for data.
6. The PocketBase APIs named here were checked against v0.41.1 source
   (`BodyLimit`, `Static`, `NewWithConfig`, `migratecmd.MustRegister`,
   `RunInTransaction`, `OnRecordEnrich`, `NewBaseCollection`/`NewViewCollection`,
   custom `id` patterns, `FindAuthRecordByToken`, `NewAuthToken`, `SetPassword`,
   `SetRandomPassword`, `ValidatePassword`, `RefreshTokenKey`,
   `OAuth2Config.GetProviderConfig`, `OAuth2ProviderConfig.InitProvider`,
   `FindFirstExternalAuthByExpr`, `NewExternalAuth`, `security.ParseUnverifiedJWT`,
   `OnBackupCreate`). Stay on v0.41.1, even though v0.42.x exists. If something
   doesn't compile, run `go doc ganigeorgiev.com/pocketbase/pocketbase/<pkg>` and adapt.
   Don't swap in a different library.

---

## 1. Locked architecture decisions

| #   | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                          | Why                                                                                                                                                                                                                                                                                                            |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | PocketBase is used **as a Go framework** (`pocketbase.New()` + `OnServe` routes), not as the standalone binary + JS hooks.                                                                                                                                                                                                                                                                                                                        | Typed code, and one binary.                                                                                                                                                                                                                                                                                    |
| D2  | **Users are PocketBase's `users` auth collection** (created by PB's init migration). Extend it with `role`, `enabled`, `owner`. Everything else Actual-specific (files, user access, secrets, api keys, prefs, pending OAuth2 logins, revoked tokens) is a **base collection** prefixed `actual_`, with relations pointing to `users`.                                                                                                            | One identity system, so pocketbase_mcp and the admin UI at `/_/` manage real users, and PB handles hashing, tokens, OAuth2, and password reset emails.                                                                                                                                                         |
| D3  | **CRDT message logs are per-group SQLite files** at `pb_data/actual_groups/group-<id>.sqlite`, **not** PB collections.                                                                                                                                                                                                                                                                                                                            | These logs are high-volume, append-only binary data, and PB has no binary field type, so a collection would mean base64 overhead. Reset deletes the whole file. Each sync is a hot-path write that shouldn't contend with PB's main DB writer. Keeping them in `pb_data` still puts them in PB backups (§6.1). |
| D4  | **Budget blobs go in a PB `file` field** (`actual_files.blob`, protected, maxSize = `syncEncryptedFileSizeLimitMB`). The upload handler wraps the raw body with `filesystem.NewFileFromBytes` and saves the record. Download streams it from `app.NewFilesystem()`.                                                                                                                                                                               | PB handles atomic replacement and old-file cleanup, and gives local or S3 storage plus built-in backups for free.                                                                                                                                                                                              |
| D5  | **Tokens are PB auth tokens (JWTs)**, still sent in the `X-ACTUAL-TOKEN` header (or the `token` JSON body field), so the ~30 client call sites don't change. `Authorization: <token>` is accepted too, so a token from the PB SDK or PB's own `auth-with-password` endpoint works everywhere. Lifetime = the `users` collection's `authToken.duration` (default **365 days**, editable in the admin UI).                                          | Stateless, standard, and interchangeable with PB's own auth APIs.                                                                                                                                                                                                                                              |
| D6  | **Logout revokes only that token**, via a denylist collection `actual_revoked_tokens` (SHA-256 of the token + its `exp`). Expired entries are pruned hourly by a PB cron job. **Changing a password** invalidates **all** of that user's tokens (PB rotates `tokenKey` automatically). Every token is minted through `auth.Resolver.IssueToken`, which keeps the `exp` strictly increasing per user so two logins in the same second cannot share a token. | Matches Node's per-session logout, and PB has no per-token revocation. PB tokens only carry whole-second expiries, so without the bump two same-second logins would be revoked together.                                                                                                                        |
| D7  | **Login methods:** email + password (PB `passwordAuth`, identity field `email`) and any **OAuth2 provider enabled on the `users` collection** in the PB admin UI (OIDC, Google, GitHub, …). Header auth and Actual's own OpenID configuration are **removed**.                                                                                                                                                                                    | One place to configure auth, PB's provider catalogue, and no shared password.                                                                                                                                                                                                                                  |
| D8  | API rules: `actual_*` collections have **all rules nil** (superuser-only). `users` encodes the Actual permission hierarchy in PB itself (owner > ADMIN > BASIC, self-service profile edits, `owner`/`password_set` server-owned); the Go `/admin/users` routes check the same rules with `CanAccessRecord`, and model hooks keep the owner invariants even for superusers. `authRule = "enabled = true"`; password auth enabled; OTP and MFA off. | One permission model for PB's REST API, the admin UI, MCP and our routes, so they cannot drift. No self-signup through `/api`; PB's own auth endpoint works for enabled users only.                                                                                                                            |
| D9  | PB auto IDs everywhere **except `actual_files`**. The client generates the budget's file id and sends it in `x-actual-file-id`, so that collection's `id` field uses `Pattern: ^[a-zA-Z0-9_-]+$`, `Min:1, Max:100`, and keeps the default `AutogeneratePattern`. Natural keys (`secrets.name`, which contains `:`, `key`, `state`, `token_hash`, and the join pairs) are separate fields with **unique indexes**.                                 | Only file ids come from outside the server.                                                                                                                                                                                                                                                                    |
| D10 | SQLite driver: whatever PB already registers (`modernc.org/sqlite`, CGO-free). The group DBs use the same driver through `database/sql`.                                                                                                                                                                                                                                                                                                          | One driver, and cross-compiles without CGO.                                                                                                                                                                                                                                                                    |
| D11 | **Bank sync**: port providers one at a time. Until a provider is ported, its `/status` endpoint returns `{"status":"ok","data":{"configured":false}}` and its other endpoints return 404. Port order: SimpleFIN → Enable Banking → Pluggy.ai → Akahu. **GoCardless is not ported**: it closed to new sign-ups in July 2025 and is winding down, so it keeps the not-configured stub. | No Node sidecar. A sidecar can't share users or secrets once they live in PB. Porting 4.5k LOC of a dying provider is not worth the risk. |
| D12 | Custom TLS certs (`ACTUAL_HTTPS_KEY/CERT`) are **dropped**. Run behind a reverse proxy, or use PB's `serve --https` with autocert. If either env var is set, log a clear error and exit 1.                                                                                                                                                                                                                                                        | PB owns the listener. Supporting custom certs means forking `apis.Serve`.                                                                                                                                                                                                                                      |
| D13 | Rate limiting on Actual routes uses our own in-memory limiter (`golang.org/x/time/rate`, keyed by client IP). PB's settings rate limiter is **enabled** for `/api/*` (including PB's own auth endpoints) in a settings migration.                                                                                                                                                                                                                 | Actual routes must match express-rate-limit, including `skipSuccessfulRequests`. PB's auth endpoints now accept real user passwords, so they need limits too.                                                                                                                                                  |
| D14 | Go code lives at `packages/sync-server-go/`, module `github.com/compdani/actual-pocket/packages/sync-server-go`. Don't add a `package.json`, so Yarn and lage ignore it.                                                                                                                                                                                                                                                                          | Keeps the code beside the Node server it replaces.                                                                                                                                                                                                                                                             |

---

## 2. Target layout

```
packages/sync-server-go/
  go.mod  go.sum
  main.go                      # wires everything; default-command shim (§3.4)
  Makefile                     # build, test, proto, fixtures, smoke
  internal/
    config/                    # config.json + ACTUAL_* env loader (§3.2)
    httpx/                     # express-compatible responses, body parsing, client IP
    ratelimit/
    crdt/                      # timestamp.go, murmur3.go, merkle.go
      pb/sync.pb.go            # generated, committed
    syncdb/                    # per-group sqlite + merkle persistence
    store/                     # typed accessors over users + actual_* collections
    auth/                      # token resolve/revoke, login, bootstrap, oauth2
    ssrf/                      # dialer that blocks private ranges
    routes/
      sync.go account.go admin.go secret.go apikeys.go openid.go
      corsproxy.go meta.go     # /mode /info /health /metrics
      static.go                # SPA + COOP/COEP/CSP + dev proxy
    banks/
      notported.go simplefin/ enablebanking/ pluggyai/ akahu/ gocardless/
    cli/                       # cobra subcommands
  migrations/                  # PB Go migrations (collections, indexes, settings)
  testdata/crdt/               # golden fixtures generated by JS
  scripts/gen-crdt-fixtures.mjs
```

---

## 3. Phase 1: Skeleton that boots

### 3.1 Module

- `go mod init github.com/compdani/actual-pocket/packages/sync-server-go`
- `go get ganigeorgiev.com/pocketbase/pocketbase@v0.41.1`
- Add deps only as needed: `golang.org/x/time/rate`, `golang.org/x/oauth2`
  (PKCE options only), `google.golang.org/protobuf`, `github.com/google/uuid`
  (group ids). Don't add a murmur3, argon2, bcrypt, or OIDC library.

### 3.2 Config (`internal/config`)

Port `src/load-config.js`: the same `ACTUAL_*` env names, defaults, and
`config.json` lookup order (read that file first), **minus the auth keys**. Expose a
typed `Config` struct. Notes:

- `dataDir` default: Node uses a projectRoot or appdata path. Use `./data`
  when unset, and log the resolved absolute path at startup.
- Keep `serverFiles` / `ACTUAL_SERVER_FILES` (default `<dataDir>/server-files`); it
  only locates `pb_data`. **PB data dir** = `<serverFiles>/pb_data`.
- **Dropped keys** (log a warning naming each one that's set, then ignore it):
  `userFiles`/`ACTUAL_USER_FILES`, `loginMethod`, `allowedLoginMethods`,
  `trustedAuthProxies`, `openId.*` (all `ACTUAL_OPENID_*`), `token_expiration`,
  `enforceOpenId`.
- **Kept:** `userCreationMode` (`manual` | `login`). It controls whether an OAuth2
  login with no matching user creates one (§11).
- Port `src/config-file-env.ts` (the `_FILE` env var support) too.

### 3.3 `main.go`

```go
app := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: cfg.PBDataDir})
migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})
cli.Register(app, cfg)                     // §13
app.OnServe().BindFunc(func(se *core.ServeEvent) error {
    routes.Register(se, app, cfg)          // all Actual routes
    return se.Next()
})
app.Start()
```

Set `Automigrate: false`. Collections are defined **only** in hand-written Go migrations.

### 3.4 Default-command shim

When `os.Args` has no subcommand, inject
`serve --http=<host>:<port>` from config. Hostname `::` becomes `[::]:5006`.
Users then run `./actual-server` with no arguments, like today.
Log `Listening on <addr>:<port>...`. Electron and Docker health checks rely on that line.

### 3.5 Global middleware (bind on `se.Router`)

- CORS: PB already binds permissive CORS, which matches `cors()`. Leave it.
- Body limits: PB binds a global 32 MB `BodyLimit`. Override **per route** with
  `apis.BodyLimit(n)`. Limits come from config:
  - `/sync/sync`: `upload.fileSizeSyncLimitMB` (20)
  - `/sync/upload-user-file`: `upload.syncEncryptedFileSizeLimitMB` (50)
  - all other JSON routes: `upload.fileSizeLimitMB` (20)
- Global limiter: 500 req/min/IP on Actual routes, disabled when `NODE_ENV=development`.
  Return the express-rate-limit body and `RateLimit-*` standard headers.
- Request log: one line per request, `METHOD STATUS PATH`. Never log bodies or tokens.
- Client IP (`httpx.ClientIP`): if `RemoteAddr` is inside `trustedProxies` CIDRs,
  walk `X-Forwarded-For` from the right and take the first untrusted hop.
  Otherwise use `RemoteAddr`.

### 3.6 Meta routes (`routes/meta.go`)

- `GET /mode` → text body `cfg.Mode`.
- `GET /health` → `{"status":"UP"}`.
- `GET /info` → `{"build":{"name":"@actual-app/sync-server","description":"actual syncing server","version":<ldflags Version>}}`.
  Keep the name exactly as is. Set the version with `-ldflags "-X main.Version=$(jq -r .version ../sync-server/package.json)"`.
- `GET /metrics` → `{"mem":{"rss","heapTotal","heapUsed","external","arrayBuffers"},"uptime":seconds}`.
  Map these from `runtime.MemStats`: `Sys`, `HeapSys`, `HeapAlloc`, `0`, `0`.

### 3.7 Static frontend (`routes/static.go`)

- Production: `apis.Static(os.DirFS(cfg.WebRoot), true)` on `GET /{path...}`.
  `index.html` is the SPA fallback. Check that `/api/*` and `/_/*` still resolve
  to PB, because the more specific routes must win.
- Set COOP `same-origin`, COEP `require-corp`, and the CSP string from `src/app.ts`
  **only on these static/SPA responses**. Never set them on `/_/` (they break
  the PB admin UI) or on API routes. Node sets them only on static responses too.
- Development (`NODE_ENV=development`): `httputil.NewSingleHostReverseProxy` to
  `http://localhost:3001` with websocket upgrade. Use the dev CSP variant.
- Optional build tag `embedweb`: `//go:embed` the web build, for a single-binary release.

**Acceptance:** `go run .` boots. `/health`, `/mode`, and `/info` match Node byte for byte
(compare with `curl -i` against the Node server). The PB admin UI loads at `/_/`.
The web app loads from `ACTUAL_WEB_ROOT=packages/desktop-client/build`.

---

## 4. Phase 2: Collections and settings (Go migrations)

One migration per collection, in `migrations/`. Add `autodate` fields `created`
and `updated` to every new collection, which helps MCP. Field names are snake_case.
Every migration includes a `down` that undoes it.

### 4.1 Extend the built-in `users` auth collection

`app.FindCollectionByNameOrId("users")`, then add fields and save:

| Field     | Type                                             | Notes                                                                                  |
| --------- | ------------------------------------------------ | -------------------------------------------------------------------------------------- |
| `role`    | select [`ADMIN`, `BASIC`], maxSelect 1, required | Node `users.role`                                                                      |
| `enabled` | bool                                             | Node `users.enabled`. New users default to true (set in code; PB bools default false). |
| `owner`   | bool                                             | The server owner. Exactly one user, the first one created.                             |

The built-in `name` field is Actual's `display_name`, and `email` is Actual's `user_name`.
Also set on the collection:

- rules per D8, applied by migration `1795221100_secure_users_rules.go`.
  They are evaluated with `@request.auth` being the acting user:
  - list/view: `@request.auth.collectionName = "users" && @request.auth.enabled = true && (@request.auth.role = "ADMIN" || @request.auth.owner = true || id = @request.auth.id)`
  - create: `@request.auth.collectionName = "users" && @request.auth.enabled = true && @request.body.owner:isset = false && @request.body.password_set:isset = false && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && @request.body.role = "BASIC"))`
  - update: `@request.auth.collectionName = "users" && @request.auth.enabled = true && @request.body.owner:changed = false && @request.body.password_set:changed = false && ((@request.auth.owner = true && (owner = false || (@request.body.role:changed = false && @request.body.enabled:changed = false))) || (@request.auth.role = "ADMIN" && owner = false && (id = @request.auth.id || role = "BASIC") && (@request.body.role:changed = false || @request.body.role = "BASIC") && (id != @request.auth.id || @request.body.enabled:changed = false)) || (id = @request.auth.id && @request.body.role:changed = false && @request.body.enabled:changed = false))`
  - delete: `@request.auth.collectionName = "users" && @request.auth.enabled = true && owner = false && id != @request.auth.id && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && role = "BASIC"))`
  - manage (cross-user email/password changes): `@request.auth.collectionName = "users" && @request.auth.enabled = true && id != @request.auth.id && owner = false && (@request.auth.owner = true || (@request.auth.role = "ADMIN" && role = "BASIC"))`

  `owner` and `password_set` are server-owned: create rejects the fields when
  present (`:isset = false`), update rejects changing them (`:changed =
  false`). PocketBase has no `:changed` form for create, so the `:isset` checks
  are the create-side equivalent. `owner` may only be true on the very first
  user, and an owner cannot be demoted, disabled or deleted; those invariants
  live in model hooks (`internal/store/user_hooks.go`) because superusers bypass
  rules.

- `PasswordAuth.Enabled = true`, `IdentityFields = ["email"]`;
  `OTP.Enabled = false`, `MFA.Enabled = false`;
- `AuthToken.Duration = 31536000` (365 days);
- `OAuth2.Enabled = true` with **no providers**. The owner adds providers in the admin UI.
- password field `Min = 8`.

### 4.2 New collections (all rules nil)

| Collection               | Node table              | id          | Fields                                                                                                                                                                                                                                                               | Indexes                                     |
| ------------------------ | ----------------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- |
| `actual_files`           | files                   | custom (D9) | `group_id`, `sync_version` number, `encrypt_meta`, `encrypt_keyid`, `encrypt_salt`, `encrypt_test` text, `deleted` bool, `name` text, `owner` relation→users (maxSelect 1, cascadeDelete false), `blob` file (maxSelect 1, protected, maxSize from config, any mime) | index `owner`, index `group_id`             |
| `actual_user_access`     | user_access             | auto        | `user_id` relation→users (cascade), `file_id` relation→actual_files (cascade)                                                                                                                                                                                        | **unique** (`user_id`,`file_id`)            |
| `actual_revoked_tokens`  | (new)                   | auto        | `token_hash` text (SHA-256 hex), `expires_at` number (unix seconds, from the JWT `exp`)                                                                                                                                                                              | **unique** `token_hash`, index `expires_at` |
| `actual_pending_oauth`   | pending_openid_requests | auto        | `state` text, `provider` text, `code_verifier` text hidden, `return_url` text, `expires_at` number (unix seconds)                                                                                                                                                    | **unique** `state`                          |
| `actual_secrets`         | secrets                 | auto        | `name` text, `value` text **hidden**                                                                                                                                                                                                                                 | unique `name`                               |
| `actual_server_prefs`    | server_prefs            | auto        | `key` text, `value` text                                                                                                                                                                                                                                             | unique `key`                                |
| `actual_api_keys`        | api_keys                | auto        | `user_id` relation→users (cascade), `name` text, `key_hash` text hidden, `created_at` number (copy Node's unit; check `app-api-keys.ts`)                                                                                                                             | unique `key_hash`, index `user_id`          |
| `actual_api_key_budgets` | api_key_budgets         | auto        | `key_id` relation→actual_api_keys (cascade), `file_id` text (**not** a relation; Node has no FK)                                                                                                                                                                     | unique (`key_id`,`file_id`)                 |

Node's `auth`, `sessions`, and `users` tables have **no** equivalent collection.

### 4.3 Settings migration

- Rate limits: `Settings().RateLimits.Enabled = true`. Keep PB's default rules, and
  make sure the `*:auth` rule (auth endpoints) is at most 5 requests per 15 min per IP equivalent.
- Backups: `Settings().Backups.Cron = "0 3 * * *"`, `CronMaxKeep = 7`. S3 stays off.

Notes:

- `secrets.value` is a BLOB in Node, but every caller stores strings. Use text.
- Skip the Node migrations that only exist for old data (`rename-nordigen-secrets`,
  `backfill-files-owner`, `multiuser`, `openid`). Collections start at the final schema.
- Hidden fields are still visible to superusers in v0.41.1 (`apis/record_crud.go`).
  So **MCP can read secrets** unless Phase 14's redaction toggle is on (it is by default).
- Create `<pbDataDir>/actual_groups/` at startup (`os.MkdirAll`, mode 0700),
  not in a migration.

**Acceptance:** `./actual-server migrate up` on an empty data dir creates every
collection and the `users` fields. `migrate down` + `up` round-trips. A Go test
asserts each collection's field names, rules, and the `users` auth options above.

---

## 5. Phase 3: CRDT core (the riskiest part, so test it hardest)

### 5.1 Protobuf

- Source: `packages/crdt/src/proto/sync.proto`. **Don't edit it.**
- Generate with
  `protoc --go_out=internal/crdt/pb --go_opt=paths=source_relative --go_opt=Msync.proto=github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb -I ../crdt/src/proto sync.proto`
- Put it behind `//go:generate` and `make proto`. Commit the generated file.
- Field names in Go: `FileId`, `GroupId`, `KeyId`, `Since`, `Messages`; `MessageEnvelope{Timestamp, IsEncrypted, Content}`; `SyncResponse{Messages, Merkle}`.

### 5.2 Murmur3 (`crdt/murmur3.go`)

Implement MurmurHash3 x86_32, seed 0, over the UTF-8 bytes of the string
(~30 lines, no dependency). The JS side uses `murmurhash@2` `v3()`. Golden-test it (§5.5).

### 5.3 Timestamp (`crdt/timestamp.go`)

Port **only** what the server uses: `Parse(string) (*Timestamp, bool)`,
`Millis()`, `String()`, `Hash() int32`.

- Port `Timestamp.parse` from `packages/crdt/src/crdt/timestamp.ts` exactly:
  split on `-` into 5 parts, `Date.parse(parts[0..2] joined by '-')`, counter is
  hex and must be ≤ 0xFFFF, and node length must be ≤ `MAX_NODE_LENGTH` (read the constant).
- `String()` must **re-format** from parsed state, not return the input:
  `time.UnixMilli(m).UTC().Format("2006-01-02T15:04:05.000Z")` + `-` +
  counter as 4 uppercase hex digits + `-` + node left-padded with `0` to 16 and
  truncated to the last 16 characters.
- `Hash()` = `int32(murmur3(String()))`.

### 5.4 Merkle (`crdt/merkle.go`). Read every line.

JS quirks you **must** reproduce:

1. **Hashes are signed int32.** `murmurhash.v3` returns uint32, but JS `^`
   produces int32. Store `Hash *int32`. Every XOR is int32 XOR.
2. **`hash` can be absent.** A fresh trie from the DB is `{}` with no `hash` key.
   `insert` treats a missing hash as 0. Serialise a nil hash by **omitting** the key.
3. **JSON key order** must be `"0","1","2","hash"`, skipping absent ones. That's
   JS's integer-keys-first rule. Write a custom `MarshalJSON`. Don't use a map.
   No spaces in the output.
4. Key = `strconv.FormatInt(millis/1000/60, 3)`. Integer minutes in base 3.
5. `prune(trie, n=2)`: if `hash` is **0 or absent**, return the trie
   **unchanged, including all its children**. JS `!trie.hash` treats 0 as falsy.
   Otherwise keep the `hash` plus the **last n** children by key order, recursively.
6. `insert` returns a new trie (it's immutable in JS). In Go you can mutate a
   deep copy, but the observable output must be identical.

Server only needs `Insert`, `Prune`, and JSON (un)marshal. Skip `diff` and `build`.

### 5.5 Golden fixtures (do this before writing merkle.go)

`scripts/gen-crdt-fixtures.mjs`, run with `node` from the repo root using
`@actual-app/crdt`:

- `murmur.json`: 200 random ASCII strings → `murmurhash.v3` value.
- `timestamps.json`: 200 timestamp strings (valid ones plus malformed:
  bad counter, long node, bad date, wrong part count) → `{ok, millis, str, hash}`.
- `merkle-*.json`: sequences of 1, 10, 1,000, and 10,000 timestamps spread across
  years, including duplicates and hashes that cancel to 0. After each insert
  batch, record `JSON.stringify(trie)` and `JSON.stringify(prune(trie))`.
  Go tests must match these **as exact strings**. `make fixtures` regenerates them,
  and the generated JSON is committed.

**Acceptance:** all golden tests pass. No sync-route work starts before then.

---

## 6. Phase 4: Group sync engine (`internal/syncdb`) + `/sync/sync`

### 6.1 Group DB

- Validate `groupId` with `^[a-zA-Z0-9_-]+$` first, then check that the resolved
  path stays inside `actual_groups/`.
- **Location: `<pbDataDir>/actual_groups/group-<id>.sqlite`.** It's inside `pb_data`,
  so PB's built-in backups (manual, cron, or S3) cover it. One backup then holds
  everything: PB data, blobs, and change logs.
- If the file doesn't exist, create it and run `src/sql/messages.sql` verbatim
  (`//go:embed`). The file is created lazily on the first `/sync` for a group.
  Nothing else creates it.
- **Backup consistency:** PB copies non-PB files in `pb_data` without locking them.
  Add a global `sync.RWMutex`: every `Sync` and group-file delete holds `RLock`
  (in addition to the per-group mutex), and
  `app.OnBackupCreate().BindFunc(func(e){ mu.Lock(); defer mu.Unlock(); return e.Next() })`
  pauses syncs for the backup's duration. Do the same on `OnBackupRestore`.
- Open it per request and close it after, like Node does. Use SQLite's default
  rollback journal. **Don't enable WAL.** WAL adds `-wal`/`-shm` side files that
  `reset-user-file` would have to remove too, and that backups could catch half-written.
- **Concurrency (new requirement, not in Node):** Node is single-threaded, so
  the merkle read-modify-write never raced. In Go it will. Keep a
  `map[groupId]*sync.Mutex` (via `sync.Map`) and hold the group lock for the
  whole `Sync` call. Also use `BEGIN IMMEDIATE` so another process can't race.

### 6.2 `Sync(messages, since, groupId)`: port `src/sync-simple.js` exactly

1. `SELECT * FROM messages_binary WHERE timestamp > ? ORDER BY timestamp`. This
   runs **before** inserting the incoming messages, and the order matters.
2. In one transaction: load the merkle (`{}` if no row), then for each message
   run `INSERT OR IGNORE`. **Only if rows-affected > 0**: parse the timestamp,
   then validate it (`invalid-timestamp`, or `clock-drift` when it's more than 5 min
   in the future). On error, **roll back everything** and return the reason string.
   Otherwise `trie = Insert(trie, ts)`.
3. `trie = Prune(trie)`. Upsert row `id=1`.
4. Return the trie and the pre-insert messages. `is_encrypted` is stored as
   1 or 0 and read back as `== 1`.

### 6.3 Route `POST /sync/sync`. Port `app.post('/sync')` from `src/app-sync.ts`.

Until Phase 5 lands, guard it with a test-only token check so it can be tested.

- Body is raw `application/actual-sync`. On protobuf decode failure, return
  `500 {"status":"error","reason":"internal-error"}`.
- `since` empty → `422 {"details":"since-required","reason":"unprocessable-entity","status":"error"}`. JSON key order doesn't matter here.
- Then file-exists, file-access, and `validateSyncedFile` checks (port `app-sync/validation.ts`).
- Sync error → `400` text with the reason.
- Success: `Content-Type: application/actual-sync`, `X-ACTUAL-SYNC-METHOD: simple`,
  body is the marshalled `SyncResponse{Merkle: json(trie), Messages}`.

**Acceptance:** Go tests port the `/sync` cases from `src/app-sync.test.ts`
(lines ~1459–1757). A cross-implementation test pushes the same message
sequence to the Node and Go servers and compares the response merkle strings.

---

## 7. Phase 5: Server-side auth on PocketBase users (`internal/auth`, `/account`)

### 7.1 Resolving a request's user (`auth.Resolve`), replacing `util/validate-user.ts`

Token lookup order: JSON body `token`, then `X-ACTUAL-TOKEN`, then `Authorization`
(strip an optional `Bearer ` prefix). Then:

1. No token → `401 {"status":"error","reason":"unauthorized","details":"token-not-found"}`.
2. `security.ParseUnverifiedJWT(token)`. If it parses and `exp` ≤ now →
   `401 {"status":"error","reason":"token-expired"}`. The client relies on telling
   expired apart from invalid.
3. SHA-256 of the token is in `actual_revoked_tokens` → same 401 as step 1.
4. `app.FindAuthRecordByToken(token, core.TokenTypeAuth)`. On error, or if the record
   isn't in `users`, → same 401 as step 1.
5. `enabled == false` → same 401 as step 1.
6. `e.Set("user", rec)`. Handlers read the user id, role, and owner flag from it.
   This replaces `res.locals`.

Helpers: `IsAdmin(rec) = rec.GetString("role") == "ADMIN"`.

Issuing a token: always `auth.Resolver.IssueToken(rec)`, never
`rec.NewAuthToken()` directly. It signs with `rec.NewStaticAuthToken(...)` using the
`users` collection's `authToken.duration`, but remembers the last `exp` per user in
memory (mutex-guarded, pruned by the hourly cron) and pushes the next `exp` forward
by whole seconds when two logins happen in the same second. PocketBase's tokens have
one-second exp granularity, so otherwise they would be byte-identical and logging
out one session would revoke the other.

**API keys** (`util/validate-budget-session.ts`): port this exactly, including
`apiKeyFileIds`. An API key resolves to its `user_id` record, and the same
`enabled` check applies. `/sync` decides between user-only and API-key-capable
auth per route, using the exact list at the top of `src/app-sync.ts`.

### 7.2 `/account/*` routes

Response envelopes stay as in `src/app-account.js` (`{status:'ok', data}` /
`{status:'error', reason}`). Behaviour changes:

| Route                                                        | New behaviour                                                                                                                                                                                                                                                                                                                                                   |
| ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /needs-bootstrap`                                       | `{status:'ok', data:{bootstrapped: <users count > 0>, loginMethod:'password', availableLoginMethods: <same list as login-methods>, multiuser: true}}`                                                                                                                                                                                                           |
| `POST /bootstrap` `{email, password}`                        | Only when there are **no** users, otherwise `400 already-bootstrapped`. Create a user: email, password (PB validates min 8 → `400 invalid-password`), `name: ""`, `role: ADMIN`, `enabled: true`, `owner: true`, `verified: true`. Respond `{status:'ok', data:{}}`. Use `RunInTransaction` and re-check the count inside it, so two bootstraps can't both win. |
| `GET /login-methods`                                         | `{status:'ok', methods:[…]}`. Include `{method:'password', displayName:'Email & password', active:true}` if password auth is enabled, plus one `{method:'openid', provider:<name>, displayName:<provider display name>, active:true}` per OAuth2 provider configured on `users`.                                                                                |
| `POST /login` `{loginMethod, email, password}`               | Password: `FindAuthRecordByEmail("users", email)`, then `ValidatePassword`. Unknown email and wrong password both → `400 invalid-password`. Disabled → `400 user-disabled`. Success → `{status:'ok', data:{token: auth.IssueToken(rec)}}`. Always run a password check, even for an unknown email (validate against a dummy record), to avoid a timing oracle.    |
| `POST /login` `{loginMethod:'openid', provider, returnUrl}`  | §11.                                                                                                                                                                                                                                                                                                                                                            |
| `POST /logout` `{token}`                                     | Validate it as in §7.1, then insert `{token_hash, expires_at: exp}` into `actual_revoked_tokens`. Keep Node's error responses (`400 invalid-token`).                                                                                                                                                                                                            |
| `POST /change-password` `{token, password, currentPassword}` | Require `currentPassword` to be valid (`invalid-current-password`). `SetPassword` + save, which rotates `tokenKey` and logs the user out everywhere. Respond `{status:'ok', data:{token: <auth.IssueToken(rec)>}}` so the current device stays logged in (Phase 6 stores it).                                                                                              |
| `POST /server-prefs`                                         | Unchanged, admin only.                                                                                                                                                                                                                                                                                                                                          |
| `GET /validate`                                              | `data: {userName: email, displayName: name, userId: id, permission: role, loginMethod: 'password' or 'openid' (from the JWT's issue path; default 'password'), prefs}`. Check the exact field set in Node's handler.                                                                                                                                            |

- Auth limiter on `bootstrap`, `login`, `change-password`: 5 per 15 min per IP,
  **failed responses only** (`skipSuccessfulRequests`). Count a request as failed
  if status ≥ 400. Body `{"status":"error","reason":"too-many-requests"}`, status 429.
- Hourly cron (`app.Cron().MustAdd("actualPruneRevoked", "0 * * * *", …)`): delete
  `actual_revoked_tokens` rows whose `expires_at` has passed, `actual_pending_oauth`
  rows past `expires_at`, and the in-memory last-token `exp` entries whose tokens
  have expired.

**Acceptance:** Go tests cover every row of the table: bootstrap race, unknown vs
wrong password, disabled user, expired vs invalid vs revoked tokens, change-password
invalidating old tokens while the returned token works, two logins in the same
second getting distinct tokens (logging out one leaves the other valid), and a token
from PB's `/api/collections/users/auth-with-password` working on `/sync/list-user-files`.

---

## 8. Phase 6: Client changes for PocketBase login (the only client changes)

Keep each change minimal. Don't refactor surrounding code.

1. **`packages/loot-core/src/server/auth/app.ts`**
   - `signIn`: accept `{email, password, loginMethod?}` or
     `{loginMethod:'openid', provider, returnUrl}`, and post it unchanged.
   - `bootstrap`: accept `{email, password}`. Remove the `openId` branch.
   - `changePassword`: after success, store `data.token` in `user-token`.
   - Remove the `enable-openid`, `get-openid-config`, and `enable-password` handlers,
     and their entries in the handler types.
2. **`packages/loot-core/src/server/main.ts`** (API `init`): password sign-in also
   requires `email`. Add `email?: string` to the init config type (find it by searching for
   `sessionToken`) and pass it to `subscribe-sign-in`. If `password` is given without
   `email`, throw `Authentication failed: email-required`.
3. **`packages/desktop-client/src/components/manager/subscribe/`**
   - `Login.tsx`: an email input above the password for the password method. One
     button per `openid` method from `login-methods`, labelled with its `displayName`,
     which calls `signIn({loginMethod:'openid', provider, returnUrl})`.
     Remove the header-auth path.
   - `Bootstrap.tsx` / `ConfirmPasswordForm.tsx`: add an email field. Remove the OpenID bootstrap option (`OpenIdForm.tsx` use in bootstrap).
   - `OpenIdCallback.ts`: no change. The server still redirects to `<returnUrl>/openid-cb?token=…`.
4. **Settings**: in `components/settings/AuthSettings.tsx`, replace the OpenID and
   password enable/disable controls with a short text and a link to
   `<server>/_/#/collections?collection=users` ("Users and sign-in methods are
   managed in the PocketBase admin"). Delete `OpenIDEnableModal.tsx`,
   `PasswordEnableModal.tsx`, and their registrations in `Modals.tsx` and `modalsSlice.ts`.
5. **User directory** (`components/admin/UserDirectory`): the "username" input
   becomes **Email** (type `email`), plus an optional **Password** input on create
   and edit (empty = leave unchanged; on create, empty = OAuth2-only user).
6. Regenerate i18n (`yarn generate:i18n`), then run `yarn typecheck`,
   `yarn lint:fix`, and the loot-core and desktop-client tests. Update any
   tests that assumed password-only login.
7. Add a release note in `upcoming-release-notes/` (use the `writing-release-notes` skill).

**Acceptance:** against the Go server, the browser can bootstrap (email + password),
log out, log in, change password (and stay logged in), and log in through one OAuth2
provider. `@actual-app/api` `init({serverURL, email, password})` downloads a budget.

---

## 9. Phase 7: Files API (rest of `/sync`)

Port `app-sync/services/files-service.ts` into `store/files.go`, and every remaining
route in `src/app-sync.ts`: `user-get-key`, `user-create-key`, `reset-user-file`,
`upload-user-file`, `download-user-file`, `update-user-filename`,
`list-user-files`, `get-user-file-info`, `delete-user-file`.

- `FileUpdate` semantics: only fields that are **defined** change. In Go use
  pointer fields, where nil means don't touch and a pointer to nil (for example
  `groupId: null`) means set NULL. Use a `Nullable[T]{Set bool; Val *string}` helper.
- Upload headers: `x-actual-name` (URI-decode it), `x-actual-file-id`,
  `x-actual-group-id`, `x-actual-encrypt-meta` (JSON → `keyId`), `x-actual-format`.
  Duplicate header → `400 "Duplicate headers encountered for key <k>"`.
- Blob storage (D4): `rec.Set("blob", filesystem.NewFileFromBytes(body, fileId+".blob"))`
  in the **same `app.Save`** as the metadata changes. PB writes the new file, commits,
  then deletes the old one, so don't port `util/atomic-write.ts`. A failed save
  → `500 {"status":"error"}` (Node's write-failure response).
- Download: open `rec.BaseFilesPath()+"/"+rec.GetString("blob")` with
  `app.NewFilesystem()` → `GetReader`, then stream it with
  `Content-Disposition: attachment;filename=<fileId>` and
  `Content-Type: application/octet-stream`. If there's no blob yet, return the same
  error Node gives for a missing file (check `res.sendFile` behaviour: 404).
- The client always uploads with `Content-Type: application/encrypted-file`, even for
  unencrypted budgets, so the 50 MB limit applies to every upload.
- `delete-user-file` only sets `deleted=true` and clears that file's secrets. It
  keeps the blob and the group DB, so the soft-delete stays restorable. Same as Node.
  `reset-user-file` unlinks only the group DB and ignores errors. The next upload
  creates a new group id.
- `list-user-files` / `get-user-file-info`: `owner` and `usersWithAccess[].userId`
  are PB user ids. `usersWithAccess[].userName` is the email and `displayName` is `name`.
- `isAdmin` and `countUserAccess` come from `store`.

**Acceptance:** all of `app-sync.test.ts`, `api-key-budget-access.test.ts`,
and `deleted-budget-bank-sync.test.ts` ported and passing, with PB users in place of sessions.

---

## 10. Phase 8: `/admin`, `/secret`, `/api-keys`

Port `src/app-admin.js`, `src/app-secrets.js`, `src/app-api-keys.ts`,
`services/user-service.ts`, and `services/secrets-service.js`. Admin user routes
now operate on PB `users`:

- `GET /admin/owner-created` → whether a user with `owner = true` exists.
- `GET /admin/users` → Node's response shape, mapped: `id`, `userName` = email,
  `displayName` = name, `role`, `enabled`, `owner`.
- `POST /admin/users` `{userName (email), displayName, role, enabled, password?}` →
  create the user. If there's no password, `SetRandomPassword()` (an OAuth2-only user).
  Set `verified = true`. Duplicate email → Node's `user-already-exists` error.
- `PATCH /admin/users` → update those fields. A non-empty `password` calls `SetPassword`.
  Never allow changing `owner`, or demoting or disabling the owner.
- `DELETE /admin/users` → copy whatever Node does with files owned by the deleted
  user. The relation is `cascadeDelete:false`, so reassign or clear the owner
  **before** deleting the user, or the delete fails. Never delete the owner.
- `/admin/access*` (including `transfer-ownership`) → port as-is against
  `actual_user_access` and `actual_files.owner`.
- Secret key = `name` or `name:fileId` (`getSecretKey`).
- API key generation and hashing: copy the key format and prefix from
  `app-api-keys.ts`, because the client and `@actual-app/api` may check it. The
  hash only has to work inside Go: SHA-256 hex of the full key is fine.

**Acceptance:** ported tests from `app-admin.test.js`, `secrets.test.js`,
`app-api-keys.test.ts`, and `api-key-bank-sync.test.ts`, adapted to PB users.

---

## 11. Phase 9: OAuth2 login through PocketBase providers

Providers are configured **only** in the PB admin UI (`users` → Options → OAuth2).
The server drives the flow, so the client's existing `/openid-cb?token=` callback
keeps working. Don't port `accounts/openid.ts` or `app-openid.ts` beyond the
pieces named here. `/openid/enable`, `/openid/disable`, and `/openid/config` return 404.

1. **Start** (`POST /account/login` with `loginMethod:'openid'`):
   - Port `isValidRedirectUrl` from `accounts/openid.ts` exactly. It's a security
     boundary. The reference hostname comes from `Settings().Meta.AppURL` (PB has
     no per-provider `server_hostname`): the return URL's hostname must equal the
     application's hostname or be `localhost`, ignoring scheme and port exactly
     like Node. Invalid → `400 {"status":"error","reason":"Invalid redirect URL"}`.
   - `col := users`; `cfg, ok := col.OAuth2.GetProviderConfig(provider)`. Missing, or
     `!col.OAuth2.Enabled` → `400 provider-not-configured`.
   - `p, _ := cfg.InitProvider()`; `p.SetRedirectURL(appURL + "/openid/callback")`,
     where `appURL = app.Settings().Meta.AppURL`. If it's empty → `400 app-url-not-set`,
     and log that the owner must set _Application URL_ in the admin UI.
   - `verifier := oauth2.GenerateVerifier()`, `state := security.RandomString(32)`.
     Insert into `actual_pending_oauth` with a 10 min expiry.
   - Respond `{status:'ok', data:{returnUrl: p.BuildAuthURL(state, oauth2.S256ChallengeOption(verifier))}}`.
2. **Callback** (`GET /openid/callback?code&state`):
   - Load and **delete** the pending row by `state` (single use). Missing or expired →
     redirect the way Node does on error (check `app-openid.ts`).
   - `tok := p.FetchToken(code, oauth2.VerifierOption(verifier))`, then `au := p.FetchAuthUser(tok)`.
   - Find the user, in order: an `_externalAuths` link
     (`FindFirstExternalAuthByExpr` on `collectionRef`, `provider`, `providerId = au.Id`);
     otherwise a user whose email equals `au.Email` (only if the email is non-empty);
     otherwise create one if `userCreationMode == "login"` **or** there are no users yet
     (the first user becomes `ADMIN` + `owner`), with `SetRandomPassword()`,
     `email`, `name = au.Name`, `enabled = true`, `verified = true`. If none of these
     apply → error redirect with `user-not-found`.
   - Create the `_externalAuths` link if missing (`core.NewExternalAuth`).
   - Disabled user → error redirect.
   - Success → `302 <return_url>/openid-cb?token=<auth.IssueToken(rec)>` (same distinct-token helper as password login).

Callback errors use Node's response shape: `400 {"status":"error","reason":…}`,
not a browser redirect (`app-openid.ts` relays the reason from `accounts/openid.ts`;
`invalid-or-expired-state`, `openid-grant-failed`, `user-not-found`, …). A 302 to
`<returnUrl>/openid-cb?token=…` is only sent on success. `/openid/enable`,
`/openid/disable` and `/openid/config` are not registered and return 404.

**Acceptance:** Go tests drive the flow against a fake OIDC provider (`httptest` serving
discovery, token, and userinfo) through PB's `oidc` provider. A Playwright run configures the
fake provider in the admin API, clicks "Sign in with …", completes the flow, lands logged in,
and checks the user menu hides "Change password" for the OAuth2-only user. Run one manual
login against a real provider (Authelia, Keycloak, or Google) and note the result in the PR.

---

## 12. Phase 10: CORS proxy (`/cors-proxy`, only when `corsProxy.enabled`)

Port `src/app-cors-proxy.js`. For SSRF, **don't** copy the Node approach of
resolving and then checking. Use an `http.Transport` whose `net.Dialer.Control`
rejects private, loopback, link-local, ULA, CGNAT, and metadata IPs **at connect
time**. That also blocks DNS rebinding. Keep the Node allow and deny semantics from
`util/ssrf.ts`, and port `ssrf.test.ts` and `app-cors-proxy.test.js`. SimpleFIN uses
`simplefinAllowedNetworks` as an exception list, so build that in as a parameter.

---

## 13. Phase 11: CLI (`internal/cli`, registered on `app.RootCmd`)

| Command                  | Behaviour                                                                                  |
| ------------------------ | ------------------------------------------------------------------------------------------ |
| `reset-password <email>` | Prompt twice with no echo (`golang.org/x/term`), then `SetPassword` + save.                |
| `create-owner <email>`   | For headless installs: same as `/account/bootstrap`, refuses if any user exists.           |
| `health-check`           | Port of `scripts/health-check.ts` (GET `/health`, exit 0/1). Used by Docker `HEALTHCHECK`. |

`enable-openid` / `disable-openid` are dropped. PB's built-in `superuser`,
`migrate`, and `serve` commands stay available.

---

## 14. Phase 12: Fresh-install flow and backups

A new server starts empty. The owner either opens the web app (bootstrap screen:
email + password) or runs `create-owner`. Then they create or upload budgets from
the browser's local copy.

1. On first `serve`, create `actual_groups/` and run migrations. No other seeding.
2. Backups are scheduled by the settings migration (§4.3). Document how to switch
   on S3 in the admin UI.
3. Add `make smoke`: start an empty server, bootstrap, upload a budget with the API
   package, sync two clients, download, and compare.

**Acceptance:** `make smoke` passes on an empty data dir. A backup made while
syncs are running restores (with `./actual-server` pointed at the restored dir)
to a state where a client can log in, download, and sync every budget.

---

## 15. Phase 13: Bank sync providers

Start with `banks/notported.go`: for each provider prefix (`/gocardless`,
`/simplefin`, `/pluggyai`, `/akahu`, `/enablebanking`), register `POST /status`
→ `{"status":"ok","data":{"configured":false}}`. Check the exact shape in each
provider's Node `/status` handler first. With that in place the app runs, and
bank sync shows as not set up.

Then, per provider, as a **separate PR**:

1. Port the HTTP client (`*-service.*`) with an `http.Client` timeout of 60s.
2. Port the transaction normalisation. This is where the bugs hide. Port the
   provider's Node unit tests **first**, including fixtures, then make them pass.
3. Port the routes and replace the not-ported stub.
4. Port `util/payee-name.ts` and `util/title/*` once, shared (`banks/internal/text`).

Order: **SimpleFIN** (548 LOC) → **Enable Banking** (1.3k, JWT RS256 signing
via `golang-jwt/jwt/v5`, which PB already depends on) → **Pluggy.ai** → **Akahu**
(uses `util/mutex.ts`, so use a `sync.Mutex`). **GoCardless is not ported**:
it closed to new sign-ups in July 2025 and is winding down. Its `/status`
endpoint stays the not-configured stub; the other GoCardless routes stay 404.

---

## 16. Phase 14: MCP readiness (pocketbase_mcp)

1. Create a dedicated superuser: `./actual-server superuser upsert mcp@localhost <pw>`.
   Point pocketbase_mcp at `http://<host>:5006` with those credentials. For
   long-lived access, use a superuser **impersonation token**
   (`POST /api/collections/_superusers/impersonate/{id}` with `duration`).
2. Add a read-only **view collection** `actual_files_overview` that joins files,
   owner email and name, and access count. MCP gets one useful table instead of three.
3. Add `actual_sync_stats` (base collection, superuser-only), updated at the end
   of each successful `/sync/sync` via an upsert keyed on `group_id`:
   `group_id`, `file_id`, `last_sync_at`, `last_message_count`, `merkle_hash`.
   Do this off the hot path, in a goroutine with a bounded queue. Drop updates rather than block.
4. Env toggle `ACTUAL_MCP_REDACT_SECRETS=true` (default **true**): add an
   `OnRecordEnrich("actual_secrets", "actual_api_keys", "actual_pending_oauth")` hook
   that blanks hidden fields in API responses, **even for superusers**. Internal Go
   code reads through `store` and isn't affected.
5. Write `packages/sync-server-go/MCP.md`: which collections exist, and what's safe
   to edit through MCP. Safe: `users` (create, disable, change `role`, reset
   password), access grants, prefs, deleting revoked-token rows. Not safe: changing
   `owner`, or editing `actual_files.group_id`, `encrypt_*`, or `id`.

Budget contents (transactions, categories) are **not** visible to MCP in this
plan. For **unencrypted** budgets they are technically readable server-side: the blob
is a zip of `db.sqlite` + `metadata.json`, and each group-DB message is a protobuf
`Message{dataset,row,column,value}`. For encrypted budgets both are opaque. A
read-only "budget insight" layer for MCP is a possible follow-up project. Don't
build it here.

---

## 17. Phase 15: Packaging and cutover

1. **Build**: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=…"`
   for linux/amd64, linux/arm64, darwin/arm64, and windows/amd64.
2. **Docker**: new `sync-server-go.Dockerfile`. Stage 1 builds the web client with
   Yarn (reuse the stages from `sync-server.Dockerfile`), stage 2 builds Go, and the
   final stage is `gcr.io/distroless/static`. Mount one `/data` volume (PB data ends
   up at `/data/server-files/pb_data`), keep the non-auth `ACTUAL_*` env vars, and use
   `HEALTHCHECK CMD ["/actual-server","health-check"]`. Update `docker-compose.yml` to use it.
3. **Electron** (optional, last): in `packages/desktop-electron/index.ts`, replace the
   `utilityProcess` fork of the Node server with `child_process.spawn` of the bundled Go
   binary (`extraResources`). Detect readiness by polling `/health` every 200 ms for up to 15 s.
   This replaces the `parentPort` `server-started` message.
4. **Parity run**: point the Playwright e2e suite (`yarn e2e`) at the Go server
   with a synced budget, updating login steps for email + password. Run
   `@actual-app/api` against it with a script that downloads a budget, adds a
   transaction, and syncs.
5. **No rollback path to Node.** Recovery means restoring a PB backup. Say this
   plainly in `packages/sync-server-go/README.md`.
6. Delete `packages/sync-server` in a later, separate PR, once every provider you
   use is ported and the CRDT golden fixtures are committed (they're generated from
   the JS `@actual-app/crdt` package, which stays).

---

## 18. Known gotchas checklist (re-read before each PR)

- [ ] String errors are `text/html`, not JSON. Status codes copied exactly (400, not 404).
- [ ] Merkle hashes are **int32**, keys ordered `0,1,2,hash`, missing hash omitted, `prune` skips when hash is 0.
- [ ] `Timestamp.String()` re-formats; hash the re-formatted string.
- [ ] New messages are selected **before** insert. A bad timestamp rolls back the whole batch.
- [ ] Per-group mutex around sync, plus the global backup lock.
- [ ] Token order: body `token`, `X-ACTUAL-TOKEN`, `Authorization`. Expired → `token-expired`; invalid, revoked, or disabled → `unauthorized`.
- [ ] Never hash passwords or mint tokens yourself. Use PB's record auth methods.
- [ ] Changing a password rotates `tokenKey`; return a fresh token from `change-password`.
- [ ] Unknown email and wrong password give the same error and take the same time.
- [ ] Never demote, disable, or delete the owner.
- [ ] Body limits are per route. PB's default is 32 MB and the upload needs 50 MB.
- [ ] COOP/COEP/CSP only on SPA responses, never on `/_/` or `/api`.
- [ ] Inside `RunInTransaction`, use the `tx` app, never the outer `app` (or it deadlocks).
- [ ] `users` rules per D8; no PB API rule other than nil on any `actual_*` collection.
- [ ] Logs never include tokens, passwords, secrets, or bodies.
