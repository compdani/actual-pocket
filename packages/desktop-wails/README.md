# Actual Pocket Desktop (Wails)

The desktop app runs the existing React app and budgeting engine in a native
WebView, with a small Go host for what a browser cannot do: budget files on
disk, the built-in sync server, native dialogs and menus. It does not need Node
at runtime, and it replaces the former Electron app.

From the repository root:

```sh
pnpm run build:desktop
pnpm run start:desktop
go -C packages/desktop-wails test -race ./...
go -C packages/desktop-wails vet ./...
```

Both pnpm commands build the frontend, the Go host and the bundled sync server.
`start:desktop` then launches the result; it is not a live-reload development
server. Add `--skip-server` (`node bin/package-wails.mjs --run --skip-server`)
for a faster build without the sync server. Node, pnpm, Go, and the
[Wails platform build prerequisites](https://wails.io/quick-start/installation/)
are required. A separately installed Wails CLI is not needed. Existing local
translations are included; the script does not update the translations checkout.

The macOS output is `dist/Actual Pocket.app` (the sync server is
`Contents/MacOS/actual-server`, the web app it serves `Contents/Resources/web`),
with a local ad-hoc signature. Windows produces `dist/actual-pocket.exe` and
Linux `dist/actual-pocket`, each with `actual-server` and `resources/` beside
it. Build on the target operating system. The `Desktop App` workflow builds all
three on pull requests, nightly and for releases, and signs and notarizes the
macOS app when the signing secrets are configured.

## Budget Folder

Budgets are files on disk, as in the Electron app: by default
`Documents/Actual`, or the folder the Electron app was set to use, so its
budgets open in place. Each budget is a directory with `db.sqlite`,
`metadata.json` and `backups/`. Choose or move the folder from the settings
button on the budget list; if the folder becomes unusable, the startup error
screen offers to pick another.

The engine keeps an open budget in memory and saves it to disk shortly after
each change, when it closes, and before the app quits. A save sends only the
pages that changed since the last one; the host applies them all or nothing
through a journal next to the file (`db.sqlite.actual-journal`), which it
finishes on the next access if the app stopped mid-save. The first save of a
budget, a change to more than half of it, or a file changed by another program
is written whole instead, atomically (temporary file, then rename). The host
refuses incomplete writes, and serves these file operations only to the app's
own WebView, confined to the budget folder. Set `ACTUAL_WAILS_DEBUG=1` to log
every file request.

A backup of the open budget is taken every 15 minutes (the last 10 are kept)
and can be restored from **Load Backup…** in the sidebar.

Budgets created with the first Wails builds, which lived in the WebView's
storage, are copied into the budget folder on the first launch. Their copies in
the WebView are left as they were.

## Sync Server

Connect to any Go/PocketBase server, or run the bundled one from **Configure
your server**: it listens on `localhost` on the chosen port, keeps its data in
the app's data folder, and starts with the app until it is stopped. It also
serves the web app, so other devices can use it through a reverse proxy.

For a server with a self-signed certificate, **select it here** on the server
screen (or the budget list settings) adds the certificate to your user trust
store after the system asks you to confirm (macOS keychain, Windows certificate
store). On Linux, add it to the system's trusted certificates instead.

Password and OAuth sign-in use the existing server API. OAuth opens the system
browser and returns through a temporary, one-use loopback listener.

## Other Features

- Native import/export dialogs; the import dialog reopens in the last folder.
- Links in notes to local files open in Finder or Explorer.
- Menus with app-level undo/redo, reload, zoom, full screen and developer tools.
- The window's size and position are remembered. On macOS the app keeps running
  when its window is closed and returns from the Dock.
- One running instance; launching it again focuses the window.

Host settings (budget folder, sync server, window position) are in
`Actual Pocket Wails/settings.json` in the user configuration folder. The app's
own preferences stay in its WebView profile; keep the macOS bundle identifier
`com.actualpocket.desktop` and the Windows WebView profile path
(`%APPDATA%/Actual Pocket Wails/WebView2`) stable across updates.

Automatic updates are not available; install a newer release over the old one.
