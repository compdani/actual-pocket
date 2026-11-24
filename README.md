# Actual Pocket

Actual Pocket is a personal fork of [Actual Budget](https://github.com/actualbudget/actual), maintained by **Danilo Urrutia** ([compdani](https://github.com/compdani)). Actual Budget was originally created by James Long and is developed by the Actual Budget community.

This fork follows Danilo's personal needs and priorities. **Pull requests will most likely not be merged. Issues may or may not be fixed.** Reviews, responses, releases, and support are not guaranteed. See [Contributing](CONTRIBUTING.md).

Actual is a free, open-source, local-first personal finance tool with optional synchronization between devices.

<p align="center">
  <img src="/demo.png" alt="Actual Budget interface" />
</p>

## Docker Quickstart

Install Docker Engine with the Docker Compose plugin, or Docker Desktop. Run these commands from the repository root. Docker builds the web app and Go/PocketBase sync server from this fork; no local Node.js or pnpm installation is required. The first build downloads dependencies and translations and may take several minutes. The builder allows up to 8 GB for Node.js, so provide enough memory for the build.

```sh
git clone https://github.com/compdani/actual-pocket.git
cd actual-pocket
```

Choose one of the following deployment methods.

### Dockerfile Only

Build the image, create persistent storage, and start the server:

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

Open **http://localhost:5006**, create the owner account with an email address and password, and create or import a budget. For a sample budget, choose **View demo** where offered.

Check startup and stop the container:

```sh
docker logs -f actual-pocket
docker stop actual-pocket
```

To update, back up your data, pull the latest source, rebuild, and replace the container using the same volume:

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

`sync-server-go.Dockerfile` provides the same Go/PocketBase build for deployments that specify `-f sync-server-go.Dockerfile`.

### Docker Compose

The root `docker-compose.yml` builds this fork and stores server data in a named volume:

```sh
docker compose up -d --build
docker compose logs -f actual-server
```

Open **http://localhost:5006** and complete the initial setup. To stop the service:

```sh
docker compose down
```

To update, back up your data and run:

```sh
git pull
docker compose up -d --build
```

Compose preserves the data volume when recreating containers or running `down`. **Do not use `docker compose down -v` unless you intend to delete the stored server data.** The Dockerfile-only and Compose examples use separate data volumes; choose one method consistently or explicitly configure them to share an existing volume.

## PocketBase Accounts and Data

The first-run page creates an application owner with an email address and password. PocketBase's administration UI is at `http://localhost:5006/_/`; its superuser credentials are separate from application users.

The image contains a Go binary and the built web client; it does not run a Node sync server or include a shell. Check health with:

```sh
docker exec actual-pocket /actual-server health-check
```

For Compose, use `docker compose exec actual-server /actual-server health-check`.

Persistent state lives in `/data/server-files/pb_data`, including the `actual_groups` synchronization logs. This backend is for fresh installations. Existing Node server data cannot be mounted as a PocketBase database; use a separate volume. See the Go/PocketBase server guide for backup and recovery instructions.

### Storage and Configuration

Mount persistent storage at `/data`; it contains server settings and synchronized budgets. Back up that volume before updating. To change the host port, change the first `5006` in `5006:5006`.

You can mount a configuration file at `/data/config.json` or pass `ACTUAL_*` environment variables. See the [Go/PocketBase server guide](packages/sync-server-go/README.md) for supported settings; the inherited Node configuration reference includes settings this backend no longer accepts. The runtime runs as UID/GID `65532`; if you use a host-directory mount instead of a named volume, make sure that directory is writable by this user.

For access from other devices, terminate HTTPS at a reverse proxy. The Go server does not accept the legacy `ACTUAL_HTTPS_KEY` and `ACTUAL_HTTPS_CERT` settings. The browser features required by Actual work on HTTP localhost; remote access requires a secure context. See the upstream [HTTPS guide](https://actualbudget.org/docs/config/https/).

### Troubleshooting Startup

If you see `cannot open ./bin/docker-start`, you are using the old development image or an old command override. Rebuild using the Dockerfile in this checkout and remove any deployment command or entrypoint override that invokes `bin/docker-start`. The production image starts the Go/PocketBase binary directly and needs no source mount at `/app`.

For frontend development with live reload, use the separate development setup:

```sh
docker compose -f docker-compose.dev.yml up --build
```

The development frontend runs at **http://localhost:3001**. This is separate from the production deployment above.

## Desktop App

The desktop app is built with Wails. It reuses the browser budgeting engine and
connects to the Go/PocketBase server. With the development dependencies
installed, build and launch it from the repository root:

```sh
pnpm run start:desktop
```

Use `pnpm run build:desktop` to build without launching. See the
[desktop app guide](packages/desktop-wails/README.md) for platform prerequisites,
budget migration, output locations, and current limits. The Electron app has
been removed.

## Documentation

The [documentation source](packages/docs/docs/index.md) is inherited from Actual Budget. Budgeting guides and technical references remain useful, but upstream downloads, hosted services, releases, community links, and historical announcements refer to the original project. They do not distribute or provide support for this fork.

- [Fork contribution and support policy](CONTRIBUTING.md)
- [Local development setup](packages/docs/docs/contributing/development-setup.md)
- [Upstream documentation](https://actualbudget.org/docs)
- [Upstream repository](https://github.com/actualbudget/actual)

## Attribution and License

Actual Pocket remains available under the **MIT license**. Copyright for the original work remains with James Long and the respective contributors. Fork modifications are copyright Danilo Urrutia. See [LICENSE.txt](LICENSE.txt).

Upstream sponsors, community services, translations, and release history belong to the original Actual Budget project; they do not imply sponsorship or endorsement of this fork.
