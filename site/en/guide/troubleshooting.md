---
title: Troubleshooting
---

# If something went wrong

First — the service log: `journalctl -u netknownsthat -n 100` on a host,
`journalctl -u netknownsthat-hub -n 100` on the hub, `docker compose logs
hub` in Docker. Job errors — in the "Jobs" section with the full log;
failed actions — in the audit log.

## Host

| Symptom | Cause and fix |
|---|---|
| Login doesn't stick, the cookie isn't kept | Opened over HTTP while `NKT_COOKIE_SECURE=true`. For an SSH tunnel — `NKT_COOKIE_SECURE=false`, otherwise HTTPS (`NKT_TLS_ENABLED` or a proxy) |
| `403 cross-origin request refused` | The request came from another origin: the proxy doesn't pass the original `Host`, or the UI is opened under another name. Pass `Host`; extra origins go into `NKT_CORS_ORIGINS` |
| A "frontend not built" page | The binary was built before the UI: `npm run build` in `web/`, then `go build` **again** — `go:embed` bakes the files in at compile time |
| `production mode only works on Linux` | The working mode is Linux-only; for a look use `NKT_MODE=fixtures` |
| `snapshot directory ... not found` | `fixtures` is on but there's no snapshot next to it. A server needs `local` (it's the default — so the variable is set explicitly somewhere); for development — run from the repository root or set `NKT_FIXTURES_ROOT` |
| `could not create data directory /var/lib/netknownsthat` | Not running as root: use `sudo` or your own `NKT_DATA_DIR` |
| The `firewall` and `services` sources are unavailable | Root is needed: run as the service from `deploy/` |
| `failed to connect to the docker API` | Docker isn't running or uses another socket — `NKT_DOCKER_SOCKET` |
| Forgotten password | `sudo nkt passwd` (or `sudo nkt passwd <login>`). As a last resort — stop the service and delete `/var/lib/netknownsthat/netknownsthat.db` |
| `bind: address already in use` | The port is taken — the error names the process (`port already held by: nginx (pid 812)`). Stop it or change `NKT_ADDR` |
| `nginx -t` rejects a valid config: `open() "/var/log/nginx/error.log" failed (13: Permission denied)` | An old unit without `/var/log/{nginx,haproxy,caddy}` in `ReadWritePaths` and `CAP_DAC_OVERRIDE`. Reinstall the unit (`sudo make install`, "update" from the hub, or `deploy/netknownsthat.service` by hand) and `systemctl daemon-reload && systemctl restart netknownsthat` |
| Config edits: `Read-only file system` | The directory is read-only, or there's no write access to the log directory `nginx -t` opens |
| Buttons disabled, changes rejected | The `viewer` role or `NKT_ALLOW_MUTATIONS=false` |
| Terminal: `nsenter: reassociate to namespaces failed: Operation not permitted` | util-linux 2.41 (Debian 13) enters the namespace via a pidfd, which the unit sandbox rejects. Fixed in nkt 1.8.83 — update; installing `dbus` also works around it |
| The terminal or installs can't write to the system | No D-Bus and no fallback path. The "Terminal" page offers "Install dbus"; on a host under a hub — "update" to push the current unit |
| System account login doesn't work | Needs `unix_chkpwd`, and the account must be in `sudo`/`wheel`/`admin` (root always works). Disable with `NKT_SYSTEM_LOGIN=false`; not available on the hub |
| Availability and usage charts are empty | The scheduler isn't running: the service isn't started or `NKT_SCHEDULER_ENABLED=false`. The TUI doesn't collect history |
| "Files" shows empty folders | An old unit with `ProtectHome=yes` or `PrivateTmp=yes` — update nkt on the host, the unit gets rewritten |
| Garbled characters in the Windows console | `chcp 65001` |
| The native UI build broke after `make build` | `make web` puts Linux builds into `node_modules`. Run `npm install` again in `web/` |

## Hub and hosts

| Symptom | Cause and fix |
|---|---|
| SSH login fails when adding a host | Check by hand with `ssh -p <port> <user>@<address>` and the same credentials. With "hub generates a key" the key usually isn't in the right user's `authorized_keys` yet |
| Key error: "not PEM", "PuTTY format", "passphrase protected" | A `.pub` pasted, a `.ppk` (export to OpenSSH with puttygen) or a key with a passphrase (`ssh-keygen -p`). Simpler — "hub generates a key" |
| "SSH host key … changed" | The host was reinstalled — "forget host key" in the "edit" form. If it wasn't, find out who answers at that address |
| `permission denied` on upload, a mention of a password or sudoers | The user has no `NOPASSWD`. Run the commands from the error text on the host: `echo 'NAME ALL=(ALL) NOPASSWD:ALL' \| sudo tee /etc/sudoers.d/nkt-hub` and `sudo chmod 0440 /etc/sudoers.d/nkt-hub` — or use `root` |
| "nkt source not found (no go.mod …)" and the download failed | The hub didn't find a clone (`NKT_HUB_SOURCE_ROOT`) and couldn't download the release from GitHub (no access to `github.com` or the version isn't released). Set the clone path in `hub.env` or allow outbound access |
| The configured `go` doesn't run (snap under the unit) | Nothing to do: the hub downloads its own Go into `NKT_DATA_DIR/go-toolchain` (needs access to go.dev). In an isolated network — install Go and set `NKT_HUB_GO_BIN` |
| "unsupported architecture" | The host isn't Linux amd64/arm64/arm |
| "Job for netknownsthat.service failed" | The message includes the `journalctl` tail from the host. Common: `status=226/NAMESPACE` because of a missing `/etc/haproxy` in an old unit — update the hub, the unit gets re-uploaded |
| Installation hangs on "Waiting for the service to answer…" | No systemd on the host or the API port is taken — `systemctl status netknownsthat` on the host; a custom "API port" in the host form |
| A host is "unreachable" but SSH answers | The nkt service didn't come back after a package upgrade — the error includes diagnostics (unit, port, journal). "Update" reinstalls and restarts |
| A host is stuck on "installing" | "Cancel" next to the button. After a hub restart it fixes itself |
| The "Channel" column is always gray "not connected" | The hub can't reach `8078` (`NKT_HUB_TUNNEL_PORT`): open the port for the hub's address, check that nkt on the host is running and the port is free |
| File upload: some files weren't saved | The connection to the host died in the pool (NAT, sshd restart). The hub replaces a dead connection and retries a file up to three times; the rest — "retry failed". The "skip hidden" checkbox doesn't send `.git`, `.env` and ignored files |
| Forgotten hub admin password | Docker: `docker compose exec hub nkt passwd -random`. systemd: `sudo systemctl stop netknownsthat-hub && sudo env NKT_MODE=hub NKT_DATA_DIR=/var/lib/netknownsthat-hub nkt passwd -random && sudo systemctl start netknownsthat-hub` |
| The hub in Docker doesn't start: the volume belongs to root | The hub in the container is uid 1000: `docker run --rm -v <volume>:/data alpine chown -R 1000:1000 /data` |
| No "Update" button in "About" | The hub doesn't run as a systemd unit (Docker, Kubernetes or started by hand) — update the image or install the hub as a service |

## Jobs

| Symptom | Cause and fix |
|---|---|
| "The service restarted — continuing from the saved point." | The hub or host restarted in the middle of a job; jobs that can resume (installs, clusters, scripts) continue by themselves |
| "the job was stopped: the service restarted in the middle of it more than 3 times" | The job seems to crash the service itself — the hub stopped resuming it to avoid a restart loop. Check the service log (`journalctl -u netknownsthat-hub`) and report the bug; retry with "try again" after an update |
| "internal runner error: …" | A bug in nkt while running a step: the job is stopped, the service keeps running. The error text is in the job log; please report it |
| No "try again" offered | This kind of job can't resume from where it stopped — start the action again |
| "Request failed. Failed to fetch" | The service is restarting or unreachable — wait and reload the page |

## Kubernetes and deployments

| Symptom | Cause and fix |
|---|---|
| "Open in browser" on a pod: a page without scripts or styles | The separate forward port is off (`NKT_FORWARD_ADDR=off`) — the application is sandboxed. Turn port 8446 on and forward it through the tunnel or proxy (`NKT_FORWARD_PUBLIC_URL`) |
| A forward doesn't open through an SSH tunnel | Forward 8446 too: `ssh -L 8077:127.0.0.1:8077 -L 8446:127.0.0.1:8446 …` |
| `Helm is not installed on the host` | The node's Kubernetes tab → Helm → "Install Helm" |
| A deployment doesn't trigger | The table on the [Deployments](/en/guide/hub-deploy#if-nothing-deploys) page |
| nkt-edge "disconnected" | The table on the [nkt-edge](/en/guide/edge#if-it-doesn-t-work) page |

## API, webhooks and bots

| Symptom | Cause and what to do |
|---|---|
| 401 "Invalid API token or request signature" | The token was revoked or replaced ("New secret"), part of the string is missing, or the signature is computed over the wrong path: the path is what the hub sees, with the `?query` |
| 401 "The request signature is stale" | The client and hub clocks differ by more than 5 minutes; turn on time sync |
| 403 "The API token cannot call" | The call is closed to tokens (hub management, terminal, files) or needs the admin role |
| 403 "The API token is not allowed from" | The address is not on the token's list. Behind a reverse proxy the hub takes the address from `X-Forwarded-For` only from loopback, so the proxy must be on the same machine |
| 403 "not allowed through nkt-edge" | The token lacks the "through nkt-edge" box |
| Outgoing webhook: "error" in the last delivery | The hint is on the tag: the recipient did not answer 2xx, is unreachable or rejected the signature (after "New secret", paste the secret again) |
| Telegram bot "not connected" | The error is in the card: a wrong token, no access to `api.telegram.org`, or another hub polls with the same token |
| The Telegram bot is silent in a chat | The chat is not on the list (the bot tells its number on `/start`), or the command lacks `/` |
| Slack: "dispatch_failed" or "operation_timeout" | Slack could not get through: no edge with the callbacks role (or it is not connected), a wrong Request URL, the hub is unreachable |
| Slack: 401 "bad signature" | A wrong Signing Secret, or the hub clock is off |
