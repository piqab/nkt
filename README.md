# NetKnownsThat

**English** | [Русский](README.ru.md)

Looks at a Linux host and answers the question that's usually answered by
hand through half a dozen commands: **what's actually listening on the
network here, does it match the configuration, and what's broken.**

Parses **nginx**, **haproxy**, **docker/compose**, **podman**, **LXD**,
**libvirt**, **iptables**, and **ufw** — and cross-checks what it read
against what's actually happening on the machine: `ss` output, packet
counters, live containers, a real TLS connection to the service's own
socket. Discrepancies become a list of problems, the connections between
configs become a resource map, and a history of checks becomes an
availability schedule. And it fixes all of this right there: a config
editor with validation and auto-rollback, service and container
management, firewall rules, certificate issuance and renewal.

One static binary (~16 MB), three interfaces:

```
nkt          web dashboard and background data collection
nkt tui      terminal interface for working over SSH
nkt scan     one-off check, exit code 2 on critical findings
nkt hub      control center for multiple hosts
```

No Python, no Node, no separate static files needed on the host: the web UI
is embedded in the binary.

---

## Launch on a host

**Target platform is Linux only.** The production binary is built for
Linux and doesn't run anywhere else: trying to turn on `NKT_MODE=local` on
Windows or macOS fails with a clear error, not a half-working dashboard.

### 1. Get the binary

The fastest path is a prebuilt binary from the
[Releases](https://github.com/piqab/nkt/releases) page: for every `vX.Y.Z`
tag, `.github/workflows/release.yml` builds and publishes a static ELF for
`linux/amd64`, `linux/arm64`, and `linux/arm` (armv6+), along with
`SHA256SUMS`.

```bash
# swap in your architecture and the version you want
curl -fsSLO https://github.com/piqab/nkt/releases/download/v1.8.43/nkt-linux-amd64
curl -fsSLO https://github.com/piqab/nkt/releases/download/v1.8.43/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x nkt-linux-amd64
sudo mv nkt-linux-amd64 /usr/local/bin/nkt
```

Building from source (needs only `make` — it decides on its own whether to
build via Docker or `native-build` on a bare host) is covered in
[DEVELOPMENT.md](DEVELOPMENT.md).

### 2. Check the host is readable

Before installing the service, confirm the app can actually see the
configuration at all:

```bash
sudo nkt scan
```

The command prints the listeners, containers, firewall rules, and problems
it found. Root is required: `iptables-save` and `systemctl` aren't
available to a regular user.

Production mode is on by default on Linux — no need to set `NKT_MODE`.
State is written to `/var/lib/netknownsthat`, the same place the systemd
unit writes to: running `nkt tui` by hand reads the same database the
service fills, rather than starting an empty one alongside it.

### 3. Install

The binary is already in place (step 1) — what's left is the config and
the systemd unit. There's no need to clone the repo; both files can be
grabbed straight from GitHub:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/nkt.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/nkt.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat.service
sudo $EDITOR /etc/netknownsthat/nkt.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat
sudo journalctl -u netknownsthat -n 30     # the admin password will be here
```

If you built from source instead (see [DEVELOPMENT.md](DEVELOPMENT.md)) —
`sudo make install` does all of the same, including copying the binary, in
one command; it won't overwrite an existing `nkt.env`.

The admin password is printed to the journal **once**, on first launch,
and is never stored anywhere in plain text again. To set it in advance,
put `NKT_BOOTSTRAP_ADMIN_PASSWORD` in `nkt.env` before the first start.
After that, passwords are changed with `nkt passwd` (see
[Accounts and passwords](#accounts-and-passwords)).

### 4. Open it

By default the service listens on `127.0.0.1:8077`. Expose it externally
**only** through a reverse proxy with TLS: `NKT_COOKIE_SECURE=true` is on
by default, and the browser simply won't accept the session cookie without
HTTPS.

Quick access without a proxy — an SSH tunnel:

```bash
ssh -L 8077:127.0.0.1:8077 user@host
```

A tunnel hands the browser plain HTTP, so add `NKT_COOKIE_SECURE=false` to
`nkt.env` to log in through it.

A third option — let `nkt`/`nkt hub` serve HTTPS itself, with no separate
nginx/Caddy in front:

```
NKT_TLS_ENABLED=true
```

On first launch the app generates and saves a self-signed certificate
under `NKT_DATA_DIR/tls/` (the browser will show a "not secure" warning
once — you'll need to accept it/add an exception, same as with any
self-signed certificate) and reuses it on later launches instead of
reissuing it every time. By default the certificate covers `127.0.0.1`,
`::1`, and this machine's hostname — enough for an SSH tunnel or local
access; to open it at a different address (a LAN name, a VPS's public IP)
without a name mismatch in the certificate, set it explicitly:
`NKT_TLS_HOSTS=127.0.0.1,::1,vps.example.internal`. Bring your own
certificate (e.g. one already issued) with `NKT_TLS_CERT`/`NKT_TLS_KEY`
instead of the self-signed one.

> **Access to the app is equivalent to root on the host** — it edits
> configs, manages services, and changes the firewall. Don't expose it to
> the internet without a separate authentication layer.

---

## Launch the hub in Docker

A prebuilt image (`Dockerfile.hub`) is published by
`.github/workflows/release.yml` for every `vX.Y.Z` tag, to
`ghcr.io/piqab/nkt-hub` — you can deploy it without cloning the repo, no
`docker login` needed.

### Docker Compose

```bash
curl -fsSLO https://raw.githubusercontent.com/piqab/nkt/main/deploy/docker-compose.hub.release.yml
docker compose -f docker-compose.hub.release.yml up -d
docker compose -f docker-compose.hub.release.yml logs hub    # the admin password
```

Building from source with the same `docker compose`, but locally
(`make hub`), uses `deploy/docker-compose.hub.yml` — more on the hub itself
in the [section below](#hub-many-hosts-from-one-place).

### Kubernetes

The manifest is [deploy/k8s/hub.yaml](deploy/k8s/hub.yaml): a `Namespace`,
a `PersistentVolumeClaim` for the hub's database, a `Deployment` (**strictly
one replica** — the hub keeps its host registry in SQLite on a
ReadWriteOnce volume and generates its own encryption key on first start; a
second pod sharing the same volume has no way to coordinate with the
first), and a `Service` on port 8077.

```bash
kubectl apply -f deploy/k8s/hub.yaml
kubectl logs -n netknownsthat deploy/nkt-hub       # the admin password
kubectl port-forward -n netknownsthat svc/nkt-hub 8443:8077
```

Access from outside the cluster goes through your own Ingress with TLS in
front of this `Service` (deliberately not included in the manifest — every
cluster has its own controller). The fallback channel (see
[Hub, item 5](#hub-many-hosts-from-one-place)) needs no separate
Ingress/network setup of its own — the hub dials out from the pod to each
host itself, ordinary egress traffic.

---

## Try it locally first

The fastest way to see a working product is `fixtures` mode. It doesn't
read your computer — it reads a **snapshot** of a real production server
from the `fixtures/host` directory, deliberately seeded with problems.
Works on Windows, macOS, and Linux, requires nothing from the system, and
touches nothing on it.

```bash
make build-dev        # a native binary for your OS
./nkt                 # on Windows: .\nkt.exe
```

This mode turns on by itself on Windows and macOS. If you're trying it
**on Linux**, set it explicitly, or the app will try to read the real
host:

```bash
NKT_MODE=fixtures ./nkt
```

The admin password is printed to the console on first launch. Open
<http://127.0.0.1:8077> — the dashboard immediately shows 25 findings: a
Redis exposed to the world, a port 8443 conflict, a container stuck in a
restart loop, an haproxy panel with no password, stale TLS.

**About the synthetic data.** The sockets described in the snapshot don't
exist on your machine, and its counters are frozen — so probes and metrics
are simulated, and 14 days of history is seeded once on first launch. The
UI says so with a banner, the API returns `"simulated": true`, and it can
be turned off with `NKT_DEMO_BACKFILL=false`. In production mode
everything is measured for real.

If you'd rather have real nginx, haproxy, and docker instead of a
snapshot, there's a [test rig](DEVELOPMENT.md#проверочный-стенд) on docker
compose.

---

## Terminal interface

If you're already on the host over SSH, a browser and port-forwarding are
an extra step you don't need:

```bash
sudo nkt tui
```

The same data and the same actions as in the web UI. Screens switch with
digits `1`…`9`, `0`, or `Tab`:

| Screen | What it shows and does |
|---|---|
| Overview | status tiles, problem list, services, firewall, availability |
| Findings | findings table with details; `f` filters by severity |
| Map | "external network → listener → pool → backend → container → network" tree |
| Availability | a downtime heatmap by hour of the week, graphs, an outage list; `p` checks now, `space` pauses |
| Usage | trends, a ranking of top consumers, a usage schedule; `m` switches the metric |
| Configs | view and edit; `e` editor, `v` version history, `u` rollback |
| Services | systemd, docker/podman, LXD, libvirt; `s` `x` `t` `l` start, stop, restart, reload; `c` validate config |
| Firewall | ufw rules and the packet filter; `a` add, `x` delete |
| Certificates | expiry, an expiry schedule, auto-renewal, socket cross-check; `Enter` shows the file, `r` renews, `g` self-signed, `c` builds a PEM for haproxy |
| Audit | every change, with the user and the outcome |

Common keys: `F5` refresh, `r` rescan the host, `?` help, `q` quit. In the
editor, `Ctrl+S` saves — with the same service-side validation and
auto-rollback on failure as in the web UI.

Two things worth knowing up front:

* **The TUI doesn't collect history.** Probes, metrics, and log parsing
  are done by the background scheduler — that is, the running
  `netknownsthat` service. The terminal reads the same database; without
  the service, the availability and usage screens will be empty. A one-off
  check of a resource is the `p` key.
* **There's no password login.** If you're on the host, you're already
  root. Actions are logged under the name `tui:<login>`, and under `sudo`
  it's the original user that's recorded, not `root`. The
  `NKT_ALLOW_MUTATIONS=false` restriction applies here too.

---

## Hub: many hosts from one place

A plain `nkt` manages one host. `nkt hub` is the same binary in a
different mode: it's deployed on a separate VPS and manages the rest
**over SSH**, showing the same dashboard for each one that a plain `nkt`
would — just with a host picker in the UI instead of a separate deployment
for every one.

```bash
make hub                # in Docker: docker compose -f deploy/docker-compose.hub.yml up -d --build
```

Without Docker, the hub normally cross-compiles an `nkt` binary for
each newly added host itself, from this repo's own source checked out
on the machine running the hub:

```bash
git clone https://github.com/piqab/nkt.git /opt/netknownsthat
cd /opt/netknownsthat
make build
sudo make hub-install
```

`deploy/hub.env.example` (copied to `/etc/netknownsthat/hub.env` on
first install) already points `NKT_HUB_SOURCE_ROOT` at
`/opt/netknownsthat` — keep the clone there. It lives outside the
systemd unit's `StateDirectory`, so it survives independently of the
hub's own data directory; update it together with the hub itself
(`git pull && make build && sudo make hub-install`).

This clone isn't strictly required, though: if the hub itself was
installed from the [prebuilt binary](#1-get-the-binary) instead — no
checkout at all, so `NKT_HUB_SOURCE_ROOT` has nothing to point at no
matter how it's set — the hub falls back to downloading a matching
prebuilt `nkt` binary and systemd unit straight from this project's own
GitHub Releases the first time a host of a given architecture is
installed, verifying the binary's checksum before using it. That needs
outbound access to `github.com`; the clone above avoids that dependency
entirely and works offline. See [HUB.md](HUB.md) if a host install
fails with "nkt sources not found" even so.

The admin password is generated on first start and printed to the log —
same as with a plain `nkt`.

From there, on the "Hosts" page you enter a name, address, and SSH user,
and the hub itself:

1. connects over SSH and detects the architecture (`uname`);
2. builds an `nkt` binary for it — cross-compilation is cached, so a
   second host of the same architecture is provisioned without a rebuild;
3. uploads the binary, the systemd unit, and a generated `nkt.env` over
   SFTP;
4. brings `nkt` up as a plain systemd service — exactly the same
   `deploy/` as a manual install;
5. logs in with the generated admin account over the SSH tunnel and
   remembers it, encrypted, so it can proxy requests to that host's API
   from then on without logging in again.

By default the hub **generates its own keypair** for each host: you only
paste the public half into `authorized_keys`, the private half never
leaves the hub. Your own existing key or password remain a fallback
option.

What else the hub can do besides proxying:

* **scans the machine it's running on itself** — a "localhost" row is
  pinned first in the "Hosts" table, with no install and no SSH (see
  HUB.md, section 4); requires the hub itself to run with the same
  privilege profile (root + capabilities) as a plain `nkt`, not an
  isolated `DynamicUser`;
* **start/stop `nkt`** on one host, or all of them at once;
* **web terminal** — a checkbox on the host turns on a full root shell in
  the browser; changing it immediately reinstalls `nkt` on the host so the
  setting actually takes effect;
* **a fallback channel for when SSH is unreachable** — on by default for
  new hosts: the hub opens another port on the host itself (`8078` by
  default) and dials into it itself — using the same host address as SSH,
  so there's no need to configure a separate address for the hub even if
  the hub itself sits in a private network with no public address. If
  something blocks inbound not entirely but specifically port 22 on the
  host (a down or misconfigured sshd, stale credentials), the dashboard,
  terminal, and "update"/"reinstall" keep working over this channel; if
  the firewall blocks inbound entirely, the fallback channel goes the same
  direction as SSH and hits the same block. The hub always tries plain SSH
  first, switching to this channel only when the dial fails — except for
  a host's very first install, which always needs real SSH. The hosts
  table shows a badge for whether the channel is connected and whether
  it's actually in use right now — see HUB.md, section 5;
* **OS package updates** — a live `apt-get upgrade` in a terminal window,
  deliberately without `-y`: you give the `[Y/n]` confirmation yourself,
  looking at the real output. The process survives closing the window
  instead of cutting a dpkg transaction off partway through;
* **config export and import** — the host registry with every secret,
  optionally along with the master key, so secrets decrypt themselves when
  moving to a new VPS (re-encrypted with the new hub's key on import);
* **the hub updating itself** — the "About" section (next to "Hosts")
  background-checks GitHub Releases and shows a badge when a newer version
  exists; "Update" downloads it, verifies its checksum, and restarts the
  hub itself, no SSH needed. Bare-systemd installs only (`sudo make
  hub-install`) — a Docker/Kubernetes deployment updates by pulling a new
  image instead, same as any container.

The network between the hub and its managed hosts isn't exposed
externally — only whatever port of the hub you chose to publish is
visible.

Known limitations:

* SSH secrets are stored on the hub, encrypted with a master key
  (`NKT_HUB_MASTER_KEY` — generated on first start and kept in the data
  volume if not set explicitly). The hub is a single point of access to
  every managed host: treat it like any machine with production access.
  `sudo nkt hub delete` irreversibly wipes this key and the whole database
  (with a mandatory offer to save a keyed export first — and encrypt the
  export file itself with a password — for recovery via `nkt hub import`)
  — see HUB.md, "Stop / delete".
* An SSH host key is accepted on first connection with no `known_hosts`
  cross-check — the same trust-on-first-use as an interactive `ssh`.
* Management operations go through an SSH tunnel to the host's own API,
  which fully applies its own rules (`NKT_ALLOW_MUTATIONS`, the user's
  role); the hub doesn't override them.

**Step-by-step launch, adding your first host, and troubleshooting —
[HUB.md](HUB.md).**

---

## What it finds

Configs aren't just read — they're cross-checked against each other and
against the host's real state. Every finding includes an explanation, a
link to the file and line, and a concrete action to fix it.

**Network and firewall**

| Rule | Severity | What it finds |
|---|---|---|
| `port-conflict` | high | Two services declare the same port on overlapping addresses |
| `declared-not-listening` | high | A port is described in a config, but nothing is listening on it |
| `listening-not-declared` | medium/info | A process listens on a port not described in any config |
| `no-default-deny` | high | The INPUT policy is ACCEPT and ufw is off |
| `public-port-blocked` | medium | A service listens on 0.0.0.0, but the firewall blocks it |
| `docker-bypasses-firewall` | critical/high | A container publishes a port on 0.0.0.0 — DNAT bypasses INPUT and ufw |
| `stale-firewall-rule` | low | A rule allows a port nothing is listening on |
| `sensitive-port-public` | critical/high | Redis, PostgreSQL, MongoDB, etc. listening on all interfaces |

**TLS and certificates**

| Rule | Severity | What it finds |
|---|---|---|
| `weak-tls` | medium | TLSv1 / TLSv1.1 left in `ssl_protocols` |
| `missing-hsts` | low | A TLS server doesn't send Strict-Transport-Security |
| `tls-cert-missing` | high | `listen ... ssl` with no `ssl_certificate` |
| `tls-cert-expired` / `-expiring` | critical / high-medium | Expired, or expiring within 7–30 days |
| `tls-cert-not-yet-valid` | high | Not yet valid — usually the host's clock is wrong |
| `tls-cert-unreadable` | high | The file a config points to can't be read |
| `tls-cert-name-mismatch` | high | The certificate doesn't cover the name the server answers as |
| `tls-cert-renewal-not-automatic` | medium | certbot knows about the certificate, but neither a timer nor cron will renew it |
| `tls-cert-orphan-lineage` | high | Sits in `/etc/letsencrypt/live`, but has no renewal config |
| `tls-cert-self-signed` / `-weak-key` / `-weak-signature` | low / medium | Self-signed, RSA shorter than 2048, or a SHA-1/MD5 signature |
| `tls-cert-not-reloaded` | high | The socket serves a different certificate than the one in the config — the service hasn't reread the file |
| `public-plaintext-proxy` | medium | A public HTTP listener proxies traffic with no encryption |

**Pools and containers**

| Rule | Severity | What it finds |
|---|---|---|
| `upstream-undefined` / `-orphan` | high / low | A route references a pool that doesn't exist; a pool is declared but never used |
| `upstream-member-down` | high | A pool's local backend isn't listening on its own port |
| `all-backends-disabled` | critical | Every server in a pool is marked down/backup |
| `single-backend` / `backend-no-healthcheck` | info / medium | No redundancy; no health check with multiple servers |
| `container-restarting` | high | A container stuck in a restart loop |
| `container-not-running` / `-undeclared` / `-no-restart-policy` | medium / low / low | Declared but not running; running but not declared; no restart policy |
| `admin-interface-open` | high/medium | haproxy's stats panel is reachable with no password |

### Resource map

A graph where traffic reads left to right:

```
external network → service → listener → pool → backend address → container → docker network
```

Connections come from configs (`proxy_pass`, `upstream`, `use_backend`,
`default_backend`, published ports); node state comes from real listeners,
container states, and findings. Laid out by column rather than a
force-directed layout: it stays stable between scans and reads like a
diagram of request flow. The terminal interface shows the same map as a
tree.

### Availability and usage

**Availability.** Every declared listener and every pool backend is
checked on a schedule — a TCP connect or an HTTP request with the correct
`Host` header. History becomes an "hour of week × downtime" heatmap,
availability and latency graphs, and an outage list.

**Usage.** Increments of iptables counters, `docker stats`, and parsed
nginx/haproxy access logs. Log entries are sorted by their own record
timestamp, so the chart shows when the load actually happened, not when
it was collected.

### Certificates: more than expiry dates

Paths come from `ssl_certificate` in nginx and `crt` in haproxy; files are
parsed as X.509: expiry date, covered names, issuer, key algorithm and
length, self-signed or not. haproxy's `crt` can point at a directory — it
gets expanded into a list of individual certificates by SNI.

But an expired certificate is almost never a matter of forgetfulness — it's
**broken automation** — so renewal itself is checked separately: does
certbot know about the certificate, is `certbot.timer` or a cron job
active, and is the file a derived copy for haproxy (certbot doesn't write
to haproxy directly — a deploy hook glues `fullchain.pem`+`privkey.pem`
into a separate file that may not be mentioned anywhere in the config;
such copies are found by cross-checking SHA-256 fingerprints and inherit
the original's status instead of a false "manual" one).

Fixed right there, from the UI:

* **renew** an existing lineage — always via `--standalone` (a saved
  authenticator turned out to be too fragile — a broken webroot fails with
  a bare "code 1"), stopping nginx/haproxy before and guaranteeing they
  start again after, regardless of the renewal's outcome; before the call,
  it checks whether port 80 is free, so an error names the blocking
  process instead of a bare "Problem binding to port 80";
* **issue a new** Let's Encrypt certificate for a domain certbot doesn't
  know about yet (no wildcard form — `--standalone` only proves ownership
  of the exact name);
* **build a PEM for haproxy** from an already-issued lineage — overwriting
  the specific file and `reload`ing, or writing into a directory
  `bind ... crt` if one is configured;
* **self-signed** certificate, when TLS isn't set up at all: RSA
  2048/3072/4096, multiple names, including wildcard and Unicode
  (`испытание.рф` → punycode automatically).

All of this takes minutes (a round trip to Let's Encrypt, stopping
services), so the button doesn't block the UI with a spinner: the
operation goes to the server in the background, and the window shows a
live line-by-line log. The window can be closed — nothing on the host
stops.

Auto-renewal (`NKT_AUTO_RENEW_CERTS=true`) does the same thing on a
schedule. Keep in mind: this means a background job briefly stops the site
by itself, with no human involved, on every renewal.

**Cross-checking with the wire.** A file on disk can be healthy — and
still not what clients actually see: `certbot renew` swapped the file, but
the service never did a `reload` and is still serving the old certificate
from memory. The difference is invisible anywhere except the TLS
connection itself, so on every scan against a real host the app
**opens a real TLS connection** to the first known address of each
certificate, with the matching SNI, and compares fingerprints. A
connection error doesn't count as a mismatch — it's marked "not checked",
so a network problem doesn't turn into a false accusation against the
service.

### Management

* **systemd**: `start` / `stop` / `restart` / `reload`, config validation.
* **docker** and **Podman**: container lifecycle, create and remove — via
  the Engine API and its own unix socket respectively, on separate pages.
* **LXD**: containers and virtual machines with one tool
  (`lxc list --format json`), including `lxc launch` and removal.
* **libvirt/QEMU**: VM lifecycle via `virsh`, an autostart toggle, removing
  a definition (disks separately). Creating and editing VMs deliberately
  didn't get their own API: a domain is defined through the same config
  editor — the path `/etc/libvirt/qemu/<name>.xml` is recognized
  automatically, the XML is validated with `virt-xml-validate`, and saving
  with the "apply" flag registers the domain via `virsh define`.
* **Configs**: an editor with versioning. Before writing, content is
  validated by the service itself (`nginx -t`, `haproxy -c -f`,
  `docker compose config -q`); **if validation fails, the file is
  automatically restored to its previous state**. Any version can be
  viewed, compared (unified diff), and rolled back to.
* **firewall**: adding and removing rules via `ufw`. Editing iptables
  directly from the UI is deliberately not supported.
* **Web terminal** (`NKT_TERMINAL_ENABLED=true`): a full interactive shell
  on the host, right in the browser. Off by default — this is direct shell
  access, not a bounded action.
* **OS package updates**: a live `apt-get update && apt-get upgrade` in a
  terminal window, deliberately without `-y`.

Every change is logged with the user, the outcome, and the command's
output.

---

## Accounts and passwords

Passwords are stored only in the app's own database (`argon2id`,
irreversibly) — there's nowhere to recover the "current" password from,
not even from the database directly. A reset always goes through issuing
a new one.

```bash
sudo nkt passwd                      # change the admin password
sudo nkt passwd ops -role viewer     # create a read-only account
sudo nkt passwd -random              # generate a password instead of typing one
sudo nkt users                       # who exists, who logged in when, who's disabled
echo 'new-password' | sudo nkt passwd ops    # non-interactive, for scripts
```

`nkt passwd` sets the password directly in the database, without logging
into an existing session — this is also the fix for "I forgot my
password and can't log in".

From the web UI: a "Change password" button at the bottom of the sidebar
(after changing it, every session for that account ends, including the
current one), and a "Users" page for `admin` — create, change role,
disable, delete. The page can't reset someone else's forgotten password —
only `sudo nkt passwd <login>` on the host can. You can't demote, disable,
or delete your own account — this rules out accidentally losing access.
The minimum password length everywhere is 10 characters.

Every password change and account creation is logged, tagged
`cli:<host login>`.

---

## Configuration

Every setting is an `NKT_*` environment variable; the full list with
explanations is in [deploy/nkt.env.example](deploy/nkt.env.example). The
essentials:

| Variable | Meaning |
|---|---|
| `NKT_MODE` | `local` — read the real host, `fixtures` — a snapshot. Defaults to `local` on Linux, `fixtures` everywhere else |
| `NKT_DATA_DIR` | Where to store the database and config history. Defaults to `/var/lib/netknownsthat` in `local`, `./data` in `fixtures` |
| `NKT_ADDR` | Listen address, defaults to `127.0.0.1:8077` |
| `NKT_ALLOW_MUTATIONS` | `false` puts the whole app into read-only mode |
| `NKT_COOKIE_SECURE` | `true` by default (the cookie only travels over HTTPS); `false` for plain HTTP, e.g. an SSH tunnel |
| `NKT_TLS_ENABLED` | `true` — serve HTTPS itself instead of plain HTTP (see [Open it](#4-open-it)), with a self-signed certificate unless `NKT_TLS_CERT`/`NKT_TLS_KEY` are set. `false` by default |
| `NKT_TLS_HOSTS` | Which names/IPs the generated self-signed certificate should cover. Defaults to `127.0.0.1,::1,<hostname>` |
| `NKT_TLS_CERT`, `NKT_TLS_KEY` | Your own certificate/key instead of the self-signed one — both must be set together |
| `NKT_COMPOSE_FILES` | A comma-separated list of compose files |
| `NKT_PODMAN_SOCKET` | Podman's socket, defaults to `/run/podman/podman.sock` |
| `NKT_LIBVIRT_URI` | libvirt connection URI, defaults to `qemu:///system` |
| `NKT_PROBE_INTERVAL` | How often to check availability; a bare number means seconds |
| `NKT_AUTO_RENEW_CERTS` | `true` — renews certbot certificates on a schedule (`NKT_AUTO_RENEW_INTERVAL`, 6h by default) no later than `NKT_AUTO_RENEW_WITHIN` (30 days) before expiry. `false` by default |
| `NKT_CERTBOT_TIMEOUT` | How long to wait for `certbot renew` — separate from `NKT_COMMAND_TIMEOUT`, since this is a network call to Let's Encrypt, not a quick command. Defaults to `3m` |
| `NKT_TERMINAL_ENABLED` | `true` turns on the web terminal — a full shell on the host in the browser, on top of the already-required admin role and `NKT_ALLOW_MUTATIONS=true`. `false` by default for a plain `nkt` and for hub-managed hosts (a separate checkbox per host — see HUB.md); `true` by default for the hub itself (`NKT_MODE=hub`) — that's the same terminal for the "localhost" row, i.e. the same machine the hub is already running on, so it isn't extra access |
| `NKT_TERMINAL_IDLE_TIMEOUT` | Close the terminal after this long with no input/output — a forgotten tab doesn't hold a shell open forever. Defaults to `30m` |

---

## Security

* Passwords are argon2id (64 MiB, t=3, p=2). Sessions are random,
  revocable tokens in SQLite; the cookie is `HttpOnly`, `SameSite=Lax`.
  After five failed attempts, login is blocked with an increasing delay.
* Roles: `viewer` — read-only, `admin` — changes. Plus a global
  `NKT_ALLOW_MUTATIONS=false` kill switch.
* Editing configs is limited to an allowlist of directories (nginx and
  haproxy roots, the compose files listed, `/etc/libvirt/qemu`). Paths
  with `..` are rejected.
* Actions on services and the firewall only accept values from fixed
  lists — a service name or action can't be substituted into a command.
* Removing a ufw rule is cross-checked against the exact text the
  operator saw: numbers shift after every change, and deleting the
  "wrong" rule can cut off SSH.
* Optimistic locking on config save, by SHA-256: a file that changed on
  disk after being opened in the editor won't be silently overwritten.
* Every mutating action is logged to `audit_log`.
* The systemd unit in `deploy/` narrows privileges: `ProtectSystem=strict`
  with an explicit list of writable directories, a trimmed
  `CapabilityBoundingSet`, `SystemCallFilter=@system-service`.

  **The web terminal, OS package updates, and self-update over the
  fallback channel are the one deliberate exception.** They run through
  `systemd-run --pty` in a separate, entirely unrestricted transient unit,
  rather than as child processes of `nkt` itself: otherwise an interactive
  root shell, or `apt-get`'s own internal privilege drop (setuid/setgid to
  `_apt`), would run into the same restrictions that protect the daemon.
  The daemon's own restrictions exist to limit the blast radius of **its
  own** compromise, not the actions of an already-authenticated admin who
  explicitly asked for root access. This only fires when `nkt` is actually
  running as a unit (`INVOCATION_ID` is in the environment) and
  `systemd-run` is on PATH.

  `systemd-run` itself needs a live D-Bus/systemd-manager — on a host
  without one (some minimal images, e.g. Debian 11, don't install dbus by
  default) it fails with a bus-connection error. The fallback path is
  `CAP_SYS_ADMIN` in the unit's `CapabilityBoundingSet` and
  `nsenter --mount` straight into PID 1's namespace, with no D-Bus at all;
  `RestrictNamespaces` is narrowed to exactly `mnt`, nothing wider is
  allowed even with that capability. The "Terminal" page reports it if
  neither path is available, and offers an install button for dbus
  whenever the `nsenter` path would work.

---

## If something goes wrong

| Symptom | Cause and what to do |
|---|---|
| A "frontend not built" page | The binary was built before the UI. Run `npm run build`, then `go build` **again**: `go:embed` bakes the files in at compile time |
| `production mode only works on Linux` | Production mode doesn't run on Windows or macOS. For development, use `NKT_MODE=fixtures` |
| `snapshot directory ... not found` | `fixtures` is on, but there's no samples directory next to it. A production host needs `local` (it's already the default on Linux — so the variable is set explicitly somewhere). For development, run from the repo root or set `NKT_FIXTURES_ROOT` |
| `could not create data directory /var/lib/netknownsthat` | Not running as root. Either `sudo`, or point `NKT_DATA_DIR` somewhere of your own |
| `failed to connect to the docker API` | Docker isn't running |
| The `firewall` and `services` sources are unavailable | Root is required: `iptables-save` and `systemctl` don't work for a regular user. Run it through the systemd unit in `deploy/` |
| Lost the password | `sudo nkt passwd`. As a last resort, stop the service and delete `/var/lib/netknownsthat/netknownsthat.db` |
| `bind: address already in use` | The port is taken — the error itself names the process holding it (`port already held by: nginx (pid 812)`), best-effort via `ss`; either stop that process or set a different port with `NKT_ADDR` |
| Config edits are rejected with `Read-only file system` | The directory is mounted read-only, or there's no write access to the log directory `nginx -t` opens |
| `nginx -t`/`haproxy -c` rejects a valid config with `open() "/var/log/nginx/error.log" failed (13: Permission denied)` | The host's `netknownsthat.service` predates `/var/log/{nginx,haproxy,caddy}` being added to `ReadWritePaths`/`CAP_DAC_OVERRIDE` being added to `CapabilityBoundingSet` — reinstall (`sudo make hub-install` again, or "reinstall" from the hub) to pick up the current unit, or manually re-copy [deploy/netknownsthat.service](deploy/netknownsthat.service) and `systemctl daemon-reload && systemctl restart netknownsthat` |
| Buttons are disabled, changes are refused | Either the `viewer` role, or `NKT_ALLOW_MUTATIONS=false` |
| Garbled characters in the Windows console | The code page isn't UTF-8: run `chcp 65001` |
| The native frontend build broke after `make build` | `make web` installs dependencies inside a Linux container over the same directory, filling `node_modules` with Linux builds. Run `npm install` again inside `web/` |

Hub troubleshooting has its own section in [HUB.md](HUB.md).

---

## Development

Building from source, the test rig, tests, and the internals of each
package are covered in [DEVELOPMENT.md](DEVELOPMENT.md).

---

## API

Everything is under `/api`, authenticated by a cookie session. On the hub,
the same paths are also available prefixed with `/api/hosts/{id}/...` —
they're proxied to the matching host over SSH.

| Method | Path | Role | Purpose |
|---|---|---|---|
| POST | `/auth/login`, `/auth/logout`, `/auth/password` | — / any | Log in, log out, change your own password |
| GET | `/overview` | viewer | Dashboard summary in one request |
| GET | `/inventory`, `/findings`, `/topology` | viewer | Full snapshot, findings, graph |
| GET | `/services`, `/containers`, `/firewall` | viewer | Service, container, and rule state |
| GET | `/podman/containers`, `/lxd/instances`, `/vms` | viewer | Podman, LXD, libvirt/QEMU |
| GET | `/certificates` | viewer | Certificates, expiry, and auto-renewal state |
| GET | `/configs`, `/configs/file`, `/configs/versions*` | viewer | Files, content, history, diff |
| GET | `/monitor/targets`, `/monitor/heatmap`, `/monitor/outages`, `/monitor/usage*` | viewer | Availability and usage |
| GET | `/audit`, `/monitor/jobs`, `/snapshots` | viewer | Audit log, background jobs, scan history |
| GET | `/updates/status` | viewer | Whether an OS package update is currently running |
| POST | `/inventory/refresh` | admin | Rescan the host |
| POST | `/services/{name}/{action}` | admin | start / stop / restart / reload |
| POST | `/containers/{name}/{action}` | admin | Manage a docker container |
| POST/DELETE | `/podman/containers*`, `/lxd/instances*`, `/vms/{name}/*` | admin | Podman, LXD, libvirt |
| PUT | `/configs/file` | admin | Edit, with validation and auto-rollback |
| POST | `/configs/versions/{id}/rollback` | admin | Roll back to a version |
| POST/DELETE | `/firewall/rules` | admin | Add / remove a ufw rule |
| POST | `/certificates/self-signed`, `/certificates/issue` | admin | Issue a certificate |
| GET/POST/PATCH/DELETE | `/users*` | admin | Account management |
| WS | `/terminal/ws`, `/updates/ws` | admin | Web terminal and OS package updates |

---

## Limitations

* Each `nkt` manages one host: its own map, its own configs, its own
  database. Multiple hosts from one place only happens through the hub,
  by proxying to each one separately, not a shared data model.
* Only `ufw` rules are written; editing iptables directly isn't
  supported, deliberately. Supported backends are iptables/ip6tables and
  ufw — plain `nftables` (with no iptables-nft layer) isn't parsed.
* The `declared-not-listening` rule needs `ss` to be available. It stays
  silent if the socket table doesn't confirm even one declared port —
  that means it's reading a different network namespace. Published
  container ports aren't checked by this rule at all: with
  `userland-proxy: false`, docker forwards them via plain DNAT, and no
  listener exists on the host at all.
* Config validation in `fixtures` mode always "succeeds" — the failure
  path is tested on the test rig or a real host.
* Supported web servers and load balancers are nginx and haproxy. Caddy,
  Traefik, Envoy — would need a new file in `internal/parse`.
* Of virtualization tools: classic LXC (`lxc-ls`/`lxc-info`, no JSON) isn't
  supported — only LXD. Podman Quadlet isn't parsed — only the runtime
  container list is visible.
* Cross-checking "what the socket actually serves" is one TLS dial per
  certificate, to its first known address. If several certificates are
  multiplexed by SNI on one port, only the primary one is checked
  (`Sites[0]`). Not run at all in `fixtures` mode.
* A self-signed certificate doesn't remove the browser warning — it's a
  stopgap for internal and test services. The app doesn't edit the config
  automatically: the directives need to be pasted in by hand through the
  editor, where the edit goes through the service's own validation with
  auto-rollback.
* OS package updates are only supported for Debian/Ubuntu (`apt-get`).
