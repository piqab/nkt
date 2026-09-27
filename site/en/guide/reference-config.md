---
title: Configuration
---

# Configuration: every variable

Every setting is an environment variable. For the host service they live
in `/etc/netknownsthat/nkt.env`, for the hub — in
`/etc/netknownsthat/hub.env`, in Docker — in the compose file's
`environment` or a `.env` next to it. Commented templates —
[`deploy/nkt.env.example`](https://github.com/piqab/nkt/blob/main/deploy/nkt.env.example)
and [`deploy/hub.env.example`](https://github.com/piqab/nkt/blob/main/deploy/hub.env.example).

- **Durations** — `30s`, `5m`, `6h`, `720h`; a bare number means seconds.
- **Lists** — comma-separated.
- **Booleans** — `true` / `false`.
- An empty value is the same as an unset variable — the default applies.

::: warning A host under a hub
The hub rewrites a managed host's `nkt.env` on every install and update.
Such a host's settings (API port, terminal, fallback channel) are changed
in the host form on the hub, not by editing the file.
:::

## Mode and data

| Variable | Default | Meaning |
|---|---|---|
| `NKT_MODE` | `local` on Linux, otherwise `fixtures` | `local` — read the real host, `fixtures` — a snapshot, `hub` — hub mode |
| `NKT_DATA_DIR` | `/var/lib/netknownsthat`; for the hub `/var/lib/netknownsthat-hub`; in `fixtures` — `./data` | Database, config history, TLS, caches |
| `NKT_FIXTURES_ROOT` | `./fixtures/host` | Snapshot directory for `fixtures` |
| `NKT_DEMO_BACKFILL` | `true` | In `fixtures`, seed 14 days of synthetic history |

## Web and access

| Variable | Default | Meaning |
|---|---|---|
| `NKT_ADDR` | `127.0.0.1:8077` | UI and API address |
| `NKT_COOKIE_SECURE` | `true` | Session cookie over HTTPS only; `false` — for an SSH tunnel over HTTP |
| `NKT_SESSION_TTL` | `12h` | Session lifetime |
| `NKT_CORS_ORIGINS` | `http://localhost:5173` | Extra allowed origins (for state-changing requests and CORS) |
| `NKT_TLS_ENABLED` | `false` | Serve HTTPS itself |
| `NKT_TLS_HOSTS` | `127.0.0.1,::1,<hostname>` | Names and addresses in the self-signed certificate |
| `NKT_TLS_CERT`, `NKT_TLS_KEY` | empty | Your own certificate and key (both) instead of a self-signed one |
| `NKT_FORWARD_ADDR` | the host part of `NKT_ADDR`, port `8446` | A separate address for Kubernetes port forwards in the browser; `off` — disable (forwards by path, sandboxed) |
| `NKT_FORWARD_PUBLIC_URL` | empty | How the browser sees the forward address behind a proxy: `https://fwd.example.com` |

## Accounts

| Variable | Default | Meaning |
|---|---|---|
| `NKT_BOOTSTRAP_ADMIN_USER` | `admin` | The first administrator's login |
| `NKT_BOOTSTRAP_ADMIN_PASSWORD` | empty — generate | The first administrator's password (only on first start) |
| `NKT_SYSTEM_LOGIN` | `true` in `local`, otherwise `false` | Login with a host system account (needs `unix_chkpwd`, group `sudo`/`wheel`/`admin` or root). Not available on the hub |

## Control

| Variable | Default | Meaning |
|---|---|---|
| `NKT_ALLOW_MUTATIONS` | `true` | `false` — the whole application is read-only |
| `NKT_COMMAND_TIMEOUT` | `30s` | Limit for host commands (`systemctl`, `nginx -t`…) |
| `NKT_TERMINAL_ENABLED` | `false`; `true` on the hub | The web terminal (a root shell in the browser). On the hub — for the "localhost" row, i.e. the hub's own machine |
| `NKT_TERMINAL_IDLE_TIMEOUT` | `30m` | Close a terminal with no input or output |
| `NKT_TERMINAL_USER` | empty — the service's user | Who the host terminal opens as; the hub sets its SSH user here |

## What to read on the host

| Variable | Default | Meaning |
|---|---|---|
| `NKT_NGINX_ROOT`, `NKT_NGINX_MAIN_CONFIG` | `/etc/nginx`, `/etc/nginx/nginx.conf` | nginx configuration |
| `NKT_HAPROXY_ROOT`, `NKT_HAPROXY_MAIN_CONFIG` | `/etc/haproxy`, `/etc/haproxy/haproxy.cfg` | haproxy configuration |
| `NKT_CADDY_ROOT`, `NKT_CADDY_MAIN_CONFIG` | `/etc/caddy`, `/etc/caddy/Caddyfile` | Caddy configuration |
| `NKT_FAIL2BAN_ROOT` | `/etc/fail2ban` | fail2ban configuration |
| `NKT_SYSTEMD_UNIT_ROOT` | `/etc/systemd/system` | Units in "Configs" |
| `NKT_NETPLAN_ROOT` | `/etc/netplan` | Netplan |
| `NKT_SSH_ROOT` | `/etc/ssh` | sshd |
| `NKT_SYSCTL_ROOT` | `/etc/sysctl.d` | sysctl |
| `NKT_CRON_ROOT` | `/etc/cron.d` | cron |
| `NKT_COMPOSE_FILES` | `/srv/docker/docker-compose.yml,/opt/stacks/docker-compose.yml` | Compose files to show and edit |
| `NKT_FILES_ROOTS` | `/home,/srv,/opt,/var/www` | Roots of the "Disks → Files" browser (only existing ones are shown; `/tmp` — always) |
| `NKT_NGINX_ACCESS_LOGS` | `/var/log/nginx/access.log` | nginx access logs for usage |
| `NKT_HAPROXY_ACCESS_LOGS` | `/var/log/haproxy.log` | haproxy access logs |
| `NKT_DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket |
| `NKT_PODMAN_SOCKET` | `/run/podman/podman.sock` | Podman socket |
| `NKT_LIBVIRT_URI` | `qemu:///system` | libvirt connection |

## Monitoring

| Variable | Default | Meaning |
|---|---|---|
| `NKT_SCHEDULER_ENABLED` | `true` | Background probes, metrics, scans; no history without it |
| `NKT_PROBE_INTERVAL` | `1m` | How often to check availability |
| `NKT_PROBE_TIMEOUT` | `5s` | Limit for one probe |
| `NKT_METRICS_INTERVAL` | `1m` | Usage collection (counters, containers, machines) |
| `NKT_LOG_SCAN_INTERVAL` | `5m` | Access log parsing |
| `NKT_INVENTORY_INTERVAL` | `5m` | A full host scan |
| `NKT_DRIFT_INTERVAL` | `1h` | Checking the host against profiles; `0` — don't |
| `NKT_RETENTION` | `720h` (30 days) | How long to keep probe and metric history |

## Certificates

| Variable | Default | Meaning |
|---|---|---|
| `NKT_AUTO_RENEW_CERTS` | `false` | Renew certbot certificates on a schedule (the site stops for the renewal) |
| `NKT_AUTO_RENEW_INTERVAL` | `6h` | How often to check |
| `NKT_AUTO_RENEW_WITHIN` | `720h` (30 days) | How long before expiry to renew |
| `NKT_CERTBOT_TIMEOUT` | `3m` | Limit for `certbot` (a network call to Let's Encrypt) |
| `NKT_CERTBOT_EMAIL` | empty | E-mail for Let's Encrypt when issuing |

## Hub

| Variable | Default | Meaning |
|---|---|---|
| `NKT_HUB_MASTER_KEY` | empty — generate into `NKT_DATA_DIR/hub.key` | Secret encryption key (AES-256, base64: `openssl rand -base64 32`) |
| `NKT_HUB_SOURCE_ROOT` | working directory | A source clone for cross-compiling nkt for hosts; no source — the hub downloads the release |
| `NKT_HUB_GO_BIN` | `go` | Which Go to build with; if it doesn't work, the hub installs its own into `NKT_DATA_DIR/go-toolchain` |
| `NKT_HUB_RELEASE_REPO` | `piqab/nkt` | Where to download releases from (for forks) |
| `NKT_HUB_GITHUB_API` | `https://api.github.com` | The GitHub API (for GitHub Enterprise or a test stand) |
| `NKT_HUB_HOST_API_PORT` | `8077` | The nkt API port on hosts (loopback) |
| `NKT_HUB_TUNNEL_PORT` | `8078` | The fallback channel port on hosts |
| `NKT_HUB_FINDINGS_POLL_INTERVAL` | `60s` | Host polling: availability and finding counts |
| `NKT_HUB_UPDATE_CHECK_INTERVAL` | `6h` | Checking for new releases (badge only) |
| `NKT_HUB_VULNDB_REFRESH_INTERVAL` | `12h` | Checking the shared trivy database freshness |
| `NKT_HUB_CLAMDB_REFRESH_INTERVAL` | `24h` | Refreshing the ClamAV database copy; `0` — never |
| `NKT_HUB_APTCACHE_MAX_GB` | `20` | Package cache limit; `0` — no limit (the value set in the UI wins) |
| `NKT_HUB_APTCACHE_PORT` | `3142` | The cache port on hosts (loopback) |
| `NKT_HUB_EXPORT_PASSWORD` | empty | File password for `nkt hub delete -export` and `nkt hub import` without a terminal |

The hub also reads the common variables above (`NKT_ADDR`, TLS,
accounts, the terminal for "localhost", forwards).

## Internal

Set by nkt or the hub itself — not set by hand:

| Variable | Set by |
|---|---|
| `NKT_HUB_TUNNEL_LISTEN_ADDR`, `NKT_HUB_TUNNEL_TOKEN` | the hub, in `nkt.env` of a host with the fallback channel |
| `NKT_GIT_SECRET`, `NKT_CONSOLE_USER` | nkt, for child processes (git, container consoles) |
| `NKT_FAKE_BIN`, `NKT_FAKE_VM_BIN`, `NKT_TEST_*` | tests and stands, see [Development](/en/guide/development#tests) |

## nkt-edge

The `EDGE_*` variables — on the [nkt-edge](/en/guide/edge#edge-settings)
page.
