# Actual Pocket Sync Server (Go + PocketBase)

This is part of **Actual Pocket**, Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). Pull requests will most likely not be merged, and issues may or may not be fixed. See the [fork contribution policy](../../CONTRIBUTING.md) and [MIT license](../../LICENSE.txt).

This is the Go/PocketBase replacement for `packages/sync-server`. It serves the
same HTTP API as the Node server, plus PocketBase's admin UI at `/_/`.

## Run

```bash
make build
ACTUAL_DATA_DIR=./data ./actual-server
```

The server listens on `0.0.0.0:5006` by default. State lives under
`$ACTUAL_DATA_DIR/server-files/pb_data`; the per-budget CRDT logs live in
`$ACTUAL_DATA_DIR/server-files/pb_data/actual_groups`.

On a fresh install, create the owner in the browser (`/`), or headlessly:

```bash
./actual-server create-owner owner@example.com
```

Reset a password (prompts twice, without echo):

```bash
./actual-server reset-password owner@example.com
```

PocketBase's own `superuser`, `migrate` and `serve` commands are still
available.

## Backups

Backups contain the PocketBase data directory **and** the `actual_groups/`
directory. PocketBase's backup hooks pause CRDT writes while a backup is
copied, so a backup taken during syncing is consistent.

Nightly local backups are enabled by migration (`0 3 * * *`, keeping 7). Change
or disable the schedule in the admin UI: `/_/` → **Settings** → **Backups**.

### S3

Backups can be stored in S3 instead of the local `pb_data/backups` directory.
In `/_/` → **Settings** → **Backups**:

1. Turn on **Store backups in S3**.
2. Fill in:
   - **Bucket** — bucket name.
   - **Region** — e.g. `us-east-1`.
   - **Endpoint** — the S3 endpoint; for AWS use
     `https://s3.<region>.amazonaws.com`, for MinIO/R2 the provider's endpoint.
   - **Access key** and **Secret** — credentials with permission to
     read/write the bucket.
   - **Force path style** — enable for MinIO and most S3-compatible services;
     leave off for AWS virtual-hosted buckets.
3. Save. Use **Create backup** to test it once.

The credentials are stored in PocketBase's settings database, not in
`config.json`, so they are included in a local backup if you keep one. Use a
dedicated bucket/credential pair.

### Restore

From the admin UI: **Settings** → **Backups** → **Restore**. Or, with the
server stopped, unzip a backup into `server-files/pb_data` (the zip contains
`data.db`, `auxiliary.db`, `storage/` and `actual_groups/`), then start the
server with `ACTUAL_DATA_DIR` pointing at the restored data directory.

There is no rollback path to the Node server. Recovery means restoring a
PocketBase backup.

## Docker

From the repository root:

```bash
docker build -t actual-pocket:local .
docker run -d --name actual-pocket --restart unless-stopped -p 5006:5006 -v actual-pocket-data:/data actual-pocket:local
```

For Docker Compose, run `docker compose up -d --build` from the repository root. See the [Docker quickstart](../../README.md#docker-quickstart) for setup, updates, and storage. `sync-server-go.Dockerfile` is an equivalent explicit build recipe.

The image is `gcr.io/distroless/static:nonroot`; it contains the web client and
the Go binary, and its health check is `/actual-server health-check`.

## Cross-platform builds

```bash
make release
```

Builds `CGO_ENABLED=0` binaries for `linux/amd64`, `linux/arm64`,
`darwin/arm64` and `windows/amd64` into `dist/`. The version comes from
`packages/sync-server/package.json`.

Electron packaging is intentionally **not** part of this change. The desktop
app still forks the Node server; switching it to spawn the bundled Go binary is
optional Phase 15 work and needs its own installer/updater testing.

## Smoke test

```bash
make smoke
```

`make smoke` starts an empty server, bootstraps an owner, uploads a budget with
`@actual-app/api`, syncs two independent clients, downloads and compares their
transactions, creates a backup while a sync loop is running, restores that
backup into a fresh data directory, and verifies a restored client can log in,
download and sync again.
