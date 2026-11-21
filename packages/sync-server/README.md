> This package is part of Actual Pocket, Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). Pull requests will most likely not be merged, and issues may or may not be fixed. See the [fork policy](../../CONTRIBUTING.md). Upstream package downloads and community links refer to the original project.

This is the legacy Node sync server inherited from [Actual Budget](https://github.com/actualbudget/actual). Actual Pocket's default Docker deployment uses the [Go/PocketBase server](../sync-server-go/README.md). The npm commands below install the upstream Node server.

### Getting Started

Actual is a local-first personal finance tool. It is 100% free and open-source, written in NodeJS, it has a synchronization element so that all your changes can move between devices without any heavy lifting.

See the [fork contribution policy](../../CONTRIBUTING.md) and [Docker quickstart](../../README.md#docker-quickstart).

Want to say thanks? Click the ⭐ at the top of the page.

### Using the CLI tool

Node.js v22 or higher is required for the @actual-app/sync-server npm package

**Install globally with npm:**

```bash
npm install --location=global @actual-app/sync-server
```

After installing, you can execute actual-server commands directly in your terminal.

**Usage**

```bash
actual-server [options]
```

**Available options**

| Command             | Description                  |
| ------------------- | ---------------------------- |
| `-h` or `--help`    | Print this list and exit.    |
| `-v` or `--version` | Print this version and exit. |
| `--config`          | Path to the config file.     |
| `--reset-password`  | Reset your password          |

**Examples**

Run with default configuration

```bash
actual-server
```

Run with custom configuration

```bash
actual-server --config ./config.json
```

Reset your password

```bash
actual-server --reset-password
```

### Documentation

We have a wide range of documentation on how to use Actual. This is all available in our [Community Documentation](https://actualbudget.org/docs/), including topics on [installing](https://actualbudget.org/docs/install/), [Budgeting](https://actualbudget.org/docs/budgeting/), [Account Management](https://actualbudget.org/docs/accounts/), [Tips & Tricks](https://actualbudget.org/docs/getting-started/tips-tricks) and some documentation for developers.

### Feature Requests

Fork-specific issues and feature requests belong in [Actual Pocket](https://github.com/compdani/actual-pocket/issues). They may or may not be addressed. The upstream issue tracker is for the original Actual Budget project.
