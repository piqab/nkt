---
title: Command line and configuration
---

# Command line, TUI, API, configuration

## Commands

| Command | What it does |
|---|---|
| `nkt serve` (or just `nkt`) | the web UI and API |
| `nkt scan` | a one-off host scan as JSON — for scripts and a check before installing |
| `nkt tui` | the terminal UI |
| `nkt users`, `nkt passwd` | accounts and passwords without the web UI |
| `nkt hub` | the [hub](/en/guide/hub); `nkt hub import` restores the registry, `nkt hub delete` removes the hub's data completely |
| `nkt version` | the version |

## Terminal UI

`nkt tui` shows the same data in the terminal: overview, findings, the
resource map as a tree, services, containers, certificates, configs,
availability and load. Service and container control, certificate
renewal, config rollback. In English and Russian.

## API

Everything visible in the UI is available as a JSON API with the same
authorization: `POST /api/auth/login` → a session cookie, then
`GET /api/overview`, `/api/findings`, `/api/services`… Through the hub —
the same paths prefixed with `/api/hosts/{id}/`.

## Configuration

Every setting is an `NKT_*` environment variable in
`/etc/netknownsthat/nkt.env`; the full annotated list is in
[deploy/nkt.env.example](https://github.com/piqab/nkt/blob/main/deploy/nkt.env.example).
The essentials:

| Variable | Meaning |
|---|---|
| `NKT_ADDR` | listen address, `127.0.0.1:8077` by default |
| `NKT_DATA_DIR` | database and config history, `/var/lib/netknownsthat` by default |
| `NKT_ALLOW_MUTATIONS` | `false` — read-only |
| `NKT_COOKIE_SECURE` | `false` for plain HTTP (an SSH tunnel) |
| `NKT_TLS_ENABLED`, `NKT_TLS_HOSTS`, `NKT_TLS_CERT`, `NKT_TLS_KEY` | its own HTTPS |
| `NKT_TERMINAL_ENABLED` | the web terminal, off by default |
| `NKT_COMPOSE_FILES` | compose files, comma-separated |
| `NKT_FILES_ROOTS` | file browser roots |
| `NKT_PROBE_INTERVAL` | how often to probe availability |
| `NKT_AUTO_RENEW_CERTS` | certbot auto-renewal |
| `NKT_SCHEDULER_ENABLED` | background checks and metrics collection |

The hub is configured the same way through `hub.env`; the `NKT_HUB_*`
variables are described in
[HUB.md](https://github.com/piqab/nkt/blob/main/HUB.md).
