---
title: 'Docker'
sidebar_position: 2
---

Actual Pocket is Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). Build from this repository to deploy the fork. Images published by `actualbudget` contain the upstream project.

## Prerequisites

Install Docker Engine with the Docker Compose plugin, or Docker Desktop. No local Node.js or Yarn installation is required. The first build downloads dependencies and translations, and can take several minutes. The builder allows up to 8 GB of Node.js memory; provide enough memory for the build.

Clone the fork and run all commands from its root directory:

```sh
git clone https://github.com/compdani/actual-pocket.git
cd actual-pocket
```

## Deploy with the Dockerfile

```sh
docker build -t actual-pocket:local .
docker volume create actual-pocket-data
docker run -d \
  --name actual-pocket \
  --restart unless-stopped \
  -p 5006:5006 \
  -v actual-pocket-data:/data \
  actual-pocket:local
```

Open **http://localhost:5006**, create the owner account with an email address and password, and create or import a budget.

View startup logs:

```sh
docker logs -f actual-pocket
```

The production image includes the built web app and Go/PocketBase sync server. It starts `/actual-server` directly and needs no source mount at `/app`. `sync-server-go.Dockerfile` uses the same Go/PocketBase build recipe.

### Update the Container

Back up the data volume, pull the source, rebuild, and replace the container with the same volume:

```sh
git pull
docker build -t actual-pocket:local .
docker stop actual-pocket
docker rm actual-pocket
docker run -d \
  --name actual-pocket \
  --restart unless-stopped \
  -p 5006:5006 \
  -v actual-pocket-data:/data \
  actual-pocket:local
```

## Deploy with Docker Compose

The root `docker-compose.yml` builds this fork and uses a named volume for `/data`:

```sh
docker compose up -d --build
docker compose logs -f actual-server
```

Open **http://localhost:5006** and complete the initial setup.

### Update or Stop Compose

Back up your data before updating:

```sh
git pull
docker compose up -d --build
```

Stop and remove containers while keeping the data volume:

```sh
docker compose down
```

:::warning
`docker compose down -v` deletes the stored server data. The Dockerfile-only and Compose examples use separate volumes. Use one method consistently or configure an existing shared volume explicitly.
:::

## PocketBase Accounts and Data

The first-run page creates an application owner with an email address and password. PocketBase's administration UI is at `http://localhost:5006/_/`; its superuser credentials are separate from application users.

The image contains a Go binary and the built web client; it does not run a Node sync server or include a shell. Check health with:

```sh
docker exec actual-pocket /actual-server health-check
```

For Compose, use `docker compose exec actual-server /actual-server health-check`.

Persistent state lives in `/data/server-files/pb_data`, including the `actual_groups` synchronization logs. This backend is for fresh installations. Existing Node server data cannot be mounted as a PocketBase database; use a separate volume. See the Go/PocketBase server guide for backup and recovery instructions.

## Storage, Ports, and HTTPS

All server settings and synchronized budgets are stored in `/data`. Keep this directory on persistent storage and back it up before updating. The runtime runs as UID/GID `65532`; host directories mounted at `/data` must be writable by this user.

Change the first port in `5006:5006` to choose a different host port. Mount a configuration file at `/data/config.json`, or set `ACTUAL_*` environment variables; see the [Go/PocketBase server guide](https://github.com/compdani/actual-pocket/tree/master/packages/sync-server-go). Inherited Node configuration pages include settings this backend no longer accepts.

For access from another device, terminate HTTPS at a [reverse proxy](../config/reverse-proxies.md). The Go server rejects the legacy `ACTUAL_HTTPS_KEY` and `ACTUAL_HTTPS_CERT` settings. HTTP localhost works for local use; remote browser access requires a secure context.

## Troubleshooting

If startup reports `cannot open ./bin/docker-start`, rebuild the image from this checkout and remove any command or entrypoint override that invokes `bin/docker-start`. That script belongs to the development setup.

For live frontend development on port 3001, use:

```sh
docker compose -f docker-compose.dev.yml up --build
```

See the [development guide](../contributing/development-setup.md) for details.

## Support Policy

This is a personal fork. Pull requests will most likely not be merged, and issues may or may not be fixed. See the [contribution policy](../contributing/index.md).

## Upstream Images

Images published by `actualbudget` run the original project's server, rather than this fork's Go/PocketBase backend.

### `nightly` Tag

The upstream `nightly` Docker tag is described in the [original Docker guide](https://actualbudget.org/docs/install/docker/#nightly-tag). To test this fork, build its source using the commands above.
