---
title: Install on a host
---

# Install on a host

nkt is one static Linux binary. It is installed on the host you want to see
and manage, runs as a systemd service and listens on `127.0.0.1:8077` only.
No dependencies: no database, no agents, no Docker.

::: tip Several hosts?
With more than one host, install the [hub](/en/guide/hub): it installs nkt
on every host itself and shows them all in one window.
:::

## 1. The binary

Prebuilt binaries for `linux/amd64`, `linux/arm64` and `linux/arm` are on
[Releases](https://github.com/piqab/nkt/releases) together with `SHA256SUMS`:

```bash
# pick your architecture and the version you need
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/nkt-linux-amd64
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x nkt-linux-amd64
sudo mv nkt-linux-amd64 /usr/local/bin/nkt
```

Building from source is described in
[DEVELOPMENT.md](https://github.com/piqab/nkt/blob/main/DEVELOPMENT.md):
only `make` is required.

## 2. Check that the host is readable

```bash
sudo nkt scan
```

Prints the listeners, containers, firewall rules and findings — the same
things the web UI shows. Root is required: `iptables-save` and `systemctl`
are not available to a regular user.

## 3. The service

The config and the systemd unit come straight from GitHub, no clone needed:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/nkt.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/nkt.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat.service
sudo $EDITOR /etc/netknownsthat/nkt.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat
sudo journalctl -u netknownsthat -n 30     # the admin password is printed here
```

The admin password is printed to the journal **once**, on the first start.
To set it in advance, put `NKT_BOOTSTRAP_ADMIN_PASSWORD` into `nkt.env`
before the first start; later, passwords are changed with `nkt passwd`.

## 4. Open it in the browser

The service listens on `127.0.0.1:8077`. Three ways to reach it:

- **SSH tunnel** — the quickest:
  ```bash
  ssh -L 8077:127.0.0.1:8077 user@host
  ```
  then open `http://127.0.0.1:8077`. The tunnel carries plain HTTP, so set
  `NKT_COOKIE_SECURE=false` in `nkt.env`.
- **Its own HTTPS** — `NKT_TLS_ENABLED=true`: nkt issues a self-signed
  certificate (names via `NKT_TLS_HOSTS`) or takes yours from
  `NKT_TLS_CERT`/`NKT_TLS_KEY`.
- **A reverse proxy** with TLS (nginx, Caddy) in front of `127.0.0.1:8077`.

::: danger Access to nkt equals root on the host
It edits configs, controls services and changes the firewall. Do not expose
it to the internet without a separate authentication layer.
:::

## Next

- [Hub](/en/guide/hub) — for more than one host.
- [Features](/en/features) — what every section offers.
