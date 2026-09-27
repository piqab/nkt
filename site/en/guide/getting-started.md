---
title: Install on a host
---

# Install on a host

nkt is a single static binary for Linux. It goes on the host you want to
see and manage, runs as a systemd service and listens on
`127.0.0.1:8077` only. No dependencies: no database, no agents, no
Docker.

**Linux only** (amd64, arm64, 32-bit ARM from armv6). The working mode
does not start on Windows or macOS — there only the
[snapshot mode](/en/guide/intro#quick-start) is available for a look.

::: tip Many hosts?
If you have several hosts, install the [hub](/en/guide/install-hub): it
installs nkt on each one itself and shows them in one window.
:::

## 1. Binary

Prebuilt binaries for `linux/amd64`, `linux/arm64` and `linux/arm` are in
[Releases](https://github.com/piqab/nkt/releases) together with
`SHA256SUMS`:

```bash
# substitute your architecture; V is the latest version
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/nkt-linux-amd64
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x nkt-linux-amd64
sudo mv nkt-linux-amd64 /usr/local/bin/nkt
```

**From source** you only need `make`: it decides whether to build in
Docker or on the bare host (then, if Go or Node is missing, it offers to
install them into `~/.local` without sudo):

```bash
git clone https://github.com/piqab/nkt.git && cd nkt
make build                  # dist/nkt
sudo make install           # binary, unit and nkt.env (an existing nkt.env is kept)
```

More in [Development](/en/guide/development).

## 2. Check that the host is readable

```bash
sudo nkt scan
```

The command prints the listeners, containers, firewall rules and
findings it found — the same the web UI will show; exit code 2 on
critical findings. Root is needed: `iptables-save` and `systemctl` are
not available to a regular user.

The working mode (`NKT_MODE=local`) is the default on Linux. State goes
to `/var/lib/netknownsthat` — where the service writes too, so `nkt tui`
started by hand reads the same database.

## 3. Service

The config and the systemd unit come straight from GitHub, no need to
clone the repository:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/nkt.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/nkt.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat.service
sudo $EDITOR /etc/netknownsthat/nkt.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat
sudo journalctl -u netknownsthat -n 30     # the admin password is here
```

The admin password is printed to the journal **once**, on first start,
and is never stored in plain text. To set it in advance, put
`NKT_BOOTSTRAP_ADMIN_PASSWORD` into `nkt.env` before the first start;
afterwards passwords are changed with `nkt passwd` (see
[Access](/en/guide/access)).

The unit runs nkt as root but sandboxed: `ProtectSystem=strict` with an
explicit list of writable directories, a reduced capability set, a
system call filter. Details — in [Security](/en/guide/reference-security).

## 4. Open in a browser

The service listens on `127.0.0.1:8077`, and the session cookie is
HTTPS-only by default (`NKT_COOKIE_SECURE=true`). Three ways to reach it:

- **SSH tunnel** — the quickest:
  ```bash
  ssh -L 8077:127.0.0.1:8077 user@host
  ```
  and open `http://127.0.0.1:8077`. The tunnel serves plain HTTP, so
  `nkt.env` needs `NKT_COOKIE_SECURE=false`.
- **Its own HTTPS** — `NKT_TLS_ENABLED=true`. On first start nkt issues a
  self-signed certificate into `NKT_DATA_DIR/tls/` and reuses it (the
  browser warns once). By default it covers `127.0.0.1`, `::1` and the
  host name; other names and addresses —
  `NKT_TLS_HOSTS=127.0.0.1,::1,vps.example.internal`. Your own
  certificate — `NKT_TLS_CERT` and `NKT_TLS_KEY` (both together).
- **A reverse proxy** with TLS (nginx, Caddy) in front of
  `127.0.0.1:8077`.

Which other ports exist and which of them to open — on the
[Ports and access](/en/guide/ports) page.

::: danger Access to nkt equals root on the host
It edits configs, manages services and changes the firewall. Do not
expose it to the internet without a separate authentication layer.
:::

::: tip UI windows
Any window is resized by its bottom-right corner; a double click on the
corner restores the default size. The button in the title bar maximizes
the window. The size of log, job, editor, terminal and VM screen windows
is remembered in the browser.
:::

## 5. Terminal UI

If you are already on the host over SSH, you don't need a browser:

```bash
sudo nkt tui
```

The same data and actions: overview, findings, the map as a tree,
availability, usage, configs with an editor (`Ctrl+S` — with the same
validation and automatic rollback), services, firewall, certificates,
audit log. Screens — digits `1`…`9`, `0` or `Tab`; `F5` refreshes, `r`
rescans, `?` help, `q` quit.

The TUI does not collect history itself: probes and metrics come from the
`netknownsthat` service; without it the availability and usage screens
are empty. There is no password login — you are already root; actions go
into the audit log as `tui:<login>` (under `sudo`, the original user).
`NKT_ALLOW_MUTATIONS=false` applies here too.

## 6. Updating

- **By hand** — download the new binary (step 1) over
  `/usr/local/bin/nkt` and `sudo systemctl restart netknownsthat`.
- **A host under a hub** is updated from the hub: "update" in the host
  row or "update all" (see [Hub hosts](/en/guide/hub-hosts)).

The unit in `deploy/` changes now and then (new directories in
`ReadWritePaths`, capabilities). After a major update reinstall it too —
step 3 or `sudo make install`; a host under a hub gets the new unit with
every update automatically.

## What next

- [Ports and access](/en/guide/ports)
- [Hub](/en/guide/install-hub) — if you have more than one host.
- [Configuration](/en/guide/reference-config) — every `NKT_*` variable.
- [Features](/en/features) — what every section has.
