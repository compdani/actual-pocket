# Actual Pocket Desktop (Wails)

This first Wails desktop build runs the existing React app and browser budgeting
engine in a native WebView. It does not need Electron or Node at runtime.

From the repository root:

```sh
yarn build:desktop:wails
yarn start:desktop:wails
go -C packages/desktop-wails test -race ./...
go -C packages/desktop-wails vet ./...
```

Both Yarn commands build the frontend and Go host. `start:desktop:wails` then
launches the result; it is not a live-reload development server. Node, Yarn, Go,
and the [Wails platform build prerequisites](https://wails.io/quick-start/installation/)
are required. A separately installed Wails CLI is not needed. Existing local
translations are included; the script does not update the translations checkout.

The macOS output is `dist/Actual Pocket.app`, with a local ad-hoc signature.
Windows produces `dist/actual-pocket.exe`; Linux produces `dist/actual-pocket`.
Build on the target operating system. Release installers, signing, notarization,
and store submission are not configured yet.

## Budgets and Sync

Choose **Don't use a server** and **View demo** to try an offline budget, or
enter the URL of your Go/PocketBase deployment. Password and OAuth sign-in use
the existing server API. OAuth opens the system browser and returns through a
temporary, one-use loopback listener.

Budgets use browser storage: SQLite in OPFS when available, with the existing
IndexedDB fallback when it is not. The app flushes pending storage writes before
quitting. Service-worker app updates are disabled because the frontend ships
inside the desktop executable. Native file dialogs handle imports and exports;
budget calculations and synchronization continue to run in the browser worker.

This build has its own persistent WebView profile, separate from Electron and
your regular browser. Keep the macOS bundle identifier `com.actualpocket.desktop`
and the Windows WebView profile path stable across updates. On Windows that path
is `%APPDATA%/Actual Pocket Wails/WebView2`.

To move an Electron budget, export it there and import it in Wails, or download
an already-synced budget from your server. Existing Electron budget directories
are not opened or modified. Export backups before uninstalling or clearing the
WebView's storage.

## First-Version Scope

- Offline budgets and external Go/PocketBase sync.
- Native import/export dialogs, external links, and basic application/edit menus.
- One application window and one running instance.

The built-in sync server, Electron's filesystem budget folders, full menu parity,
and automatic desktop updates are not part of this first version. Existing
`yarn start:desktop` and `yarn build:desktop` commands still use Electron while
the Wails version is being validated.
