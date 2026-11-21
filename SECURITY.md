# Security Policy

Actual Pocket is Danilo Urrutia's personal fork of [Actual Budget](https://github.com/actualbudget/actual). The default Docker deployment runs the Go/PocketBase backend. Upstream Node server security assumptions and configuration do not necessarily apply.

## Reporting a Vulnerability

Use this fork's [Security tab](https://github.com/compdani/actual-pocket/security) to report a vulnerability privately, if private reporting is enabled. Include the affected revision, deployment setup, and reproduction steps. If private reporting is unavailable, do not post sensitive details publicly.

Issues may or may not be fixed. This personal fork has no guaranteed response, fix schedule, or security maintenance policy. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Deployment and Recovery

Terminate HTTPS at a reverse proxy for remote access. Application owners and PocketBase superusers have separate credentials. Keep the PocketBase data directory and its backups private.

Back up `/data/server-files/pb_data`, including `actual_groups`, before updating. This backend is for fresh installs and cannot use an existing Node server database. See the [Go/PocketBase server guide](packages/sync-server-go/README.md) for backup and restore instructions.
