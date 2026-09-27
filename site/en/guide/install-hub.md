---
title: Installing the hub
---

# Installing the hub

`nkt hub` is the same binary in another mode. The hub goes on one machine
and from there installs and maintains nkt on the other hosts over SSH;
each host opens in the hub's UI as the same panel a standalone nkt has.
What the hub can do — on the [Hub](/en/guide/hub) page.

By default the hub is a regular systemd service with the same
least-privilege principle as nkt. Docker and Kubernetes are
self-contained alternatives.

## Requirements

- **A machine for the hub** — Linux. The hub also scans and manages this
  very machine (the "localhost" row in the host list), so its unit needs
  the same privilege profile as nkt: root with a restricted capability
  set. Installing Go is optional: if the hub builds binaries from source
  and there is no system `go`, it downloads Go from go.dev into its data
  directory.
- **SSH from the hub to every host**: address, port, `root` or a user
  with passwordless `sudo`, a password or a key. Installation puts files
  into `/usr/local/bin`, `/etc/systemd/system`, `/etc/netknownsthat`.
- **Hosts** — Linux amd64, arm64 or 32-bit ARM (Raspberry Pi —
  armv6l/armv7l). Nothing needs to be installed on them in advance. The
  hub's own architecture does not matter: the host binary is built or
  downloaded for the host's architecture.
- **The hub needs no public address.** It connects to the hosts itself;
  incoming connections are only needed from your browser (and from CI if
  webhooks go straight to it — see [nkt-edge](/en/guide/edge)).

## Option 1. systemd from the prebuilt binary

The binary is the same as for a [host](/en/guide/getting-started#_1-binary);
the unit and env file are the hub's own:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/hub.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/hub.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat-hub.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat-hub.service
sudo $EDITOR /etc/netknownsthat/hub.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat-hub
sudo journalctl -u netknownsthat-hub -n 30     # admin password
```

There is no source on such a machine, so the hub downloads the nkt binary
for a new host from GitHub Releases, the same version as the hub itself,
and verifies `SHA256SUMS`. Outbound access to `github.com` is required.

## Option 2. systemd from source

The hub cross-compiles nkt for every host from a clone of the repository
— this works without GitHub access and with any version, even an
unreleased one:

```bash
git clone https://github.com/piqab/nkt.git /opt/netknownsthat
cd /opt/netknownsthat
make build                    # or make native-build without Docker
sudo make hub-install
sudo systemctl enable --now netknownsthat-hub
```

`hub-install` puts the binary into `/usr/local/bin/nkt`, the
`netknownsthat-hub.service` unit and a template
`/etc/netknownsthat/hub.env`, where `NKT_HUB_SOURCE_ROOT` already points
to `/opt/netknownsthat`. The clone lives outside the data directory;
update it together with the hub:
`git pull && make build && sudo make hub-install`. Builds are cached — a
second host of the same architecture installs without a rebuild.

Check that it came up:

```bash
curl -s http://127.0.0.1:8077/api/health
journalctl -u netknownsthat-hub -f
```

## Option 3. Docker Compose

The `ghcr.io/piqab/nkt-hub` image is published for every release, no
`docker login` needed:

```bash
curl -fsSLO https://raw.githubusercontent.com/piqab/nkt/main/deploy/docker-compose.hub.release.yml
docker compose -f docker-compose.hub.release.yml up -d
docker compose -f docker-compose.hub.release.yml logs hub    # admin password
curl -s http://127.0.0.1:8443/api/health
```

- The UI port is `8443` on the host (inside the container `8077`, the hub
  listens on `0.0.0.0`, otherwise publishing the port wouldn't work);
  `8446` — [port forwards](/en/guide/ports#_8446-kubernetes-port-forwards).
  Put your own reverse proxy with TLS in front of it unless it's just a
  local test.
- Data lives in the `nkt-hub-data` volume (`/var/lib/netknownsthat` in
  the container): the database, the encryption key, caches.
- The hub in the container runs **not as root** (user `nkt`, uid 1000).
  A volume from an old version created by root has to be handed over to
  that user once — the hub exits on start with a clear error and the
  command:
  ```bash
  docker run --rm -v nkt-hub-data:/data alpine chown -R 1000:1000 /data
  ```
  (the volume name — in `docker volume ls`).
- Every release ships `docker-compose.hub.release.yml` with the **exact
  version** of the image instead of `:latest` (its checksum is in
  `SHA256SUMS`): a mutable tag can be moved, and the volume holds the
  master key and host secrets.
- Building from source with the same compose: `make hub` (file
  `deploy/docker-compose.hub.yml`).
- The "localhost" row in a container shows the container itself, not the
  machine it runs on.

## Option 4. Kubernetes

The manifest is [`deploy/k8s/hub.yaml`](https://github.com/piqab/nkt/blob/main/deploy/k8s/hub.yaml):
Namespace, PersistentVolumeClaim, Deployment and Service (ports 8077 and
8446).

```bash
kubectl apply -f https://raw.githubusercontent.com/piqab/nkt/main/deploy/k8s/hub.yaml
kubectl logs -n netknownsthat deploy/nkt-hub       # admin password
kubectl port-forward -n netknownsthat svc/nkt-hub 8443:8077
```

- **Exactly one replica**: the host registry is SQLite on a
  ReadWriteOnce volume, the encryption key is generated on first start;
  a second pod on the same volume cannot agree with the first. The
  strategy is `Recreate`.
- `fsGroup: 1000` hands the volume to the hub user.
- Outside access — your own Ingress with TLS in front of the Service
  (not in the manifest: every cluster has its own controller).
- The fallback channel to hosts and the package cache need no network
  setup: the hub dials out from the pod with ordinary outbound traffic.
- Every release ships `k8s-hub.yaml` with the exact image version.

## First login

On first start the hub creates an administrator and prints the password
to the log:

```
=== Hub admin account created ===
  username: admin
  password: <string>
```

It is not shown anywhere else — save it. To set it in advance —
`NKT_BOOTSTRAP_ADMIN_USER` and `NKT_BOOTSTRAP_ADMIN_PASSWORD` in
`hub.env`. How to open the UI from outside — the same three ways as for
a [host](/en/guide/getting-started#_4-open-in-a-browser): an SSH tunnel,
`NKT_TLS_ENABLED=true` or a reverse proxy.

## The "localhost" row

The first row in the host list is always **localhost** — the machine the
hub runs on. It is scanned by the same code as a standalone nkt, without
SSH or a separate installation, and opens right after login. That's why
`netknownsthat-hub.service` runs with the same privilege profile as
`netknownsthat.service` (root, `CapabilityBoundingSet`, write access to
`/etc/nginx`, `/etc/haproxy`, `/etc/caddy`, `/etc/letsencrypt`); without
those privileges (an old unit) it sees very little. It has no install,
delete or SSH fields — the only action is "open".

## Updating the hub

- **systemd** — "About" → "Update to vX.Y.Z": the hub downloads the
  binary and unit of that tag, verifies checksums and restarts (details —
  in [Updates](/en/guide/hub-updates)). Or by hand, as during
  installation.
- **Docker** — `docker compose pull && docker compose up -d`.
- **Kubernetes** — a new image tag in the Deployment.

After a hub update the hosts are brought to its version: when an
outdated host is opened or with the "update all" button.

## Stop and remove

```bash
# systemd
sudo systemctl disable --now netknownsthat-hub

# Docker
docker compose -f docker-compose.hub.release.yml down      # the volume stays
docker compose -f docker-compose.hub.release.yml down -v   # wipe hub data
```

Stopping the hub does not touch nkt on the hosts: it keeps running as a
regular service; remove it with `systemctl disable --now netknownsthat`
on the host itself.

`down -v` deletes files, but their remains can be recovered from the disk
or a snapshot. To irreversibly remove the encryption key and the secrets
of every host there is a separate command:

```bash
sudo nkt hub delete
```

It asks whether to save an export before deleting and **insists** on
offering to encrypt it with a password (AES-256-GCM, a key derived from
the password with PBKDF2 — the same format as the export from the UI).
Then — confirmation with the word "delete", after which the command:

1. stops and disables `netknownsthat-hub` (skipped if there is no unit);
2. **shreds** (overwrites with random data, then deletes) the encryption
   key, the database with all secrets, config history and the TLS key;
   the binary cache and Go are simply deleted — they hold no secrets.

The binary and the unit stay: `sudo systemctl start netknownsthat-hub`
immediately brings up a new clean hub. Without a terminal (`-yes` or not
a TTY) you must pass `-export <file>` or `-no-export` explicitly; the
export password comes only from `NKT_HUB_EXPORT_PASSWORD` (passing it as
a flag is not allowed — it would show in `ps`).

Restore on a new hub:

```bash
sudo nkt hub import -file nkt-hub-export.json
```

An encrypted file is recognized automatically and the password is asked
for (or taken from `NKT_HUB_EXPORT_PASSWORD`); decryption happens in
memory only.

::: warning A caveat on shredding
On SSDs (wear leveling) and on copy-on-write file systems (btrfs, ZFS)
old blocks may survive the overwrite. Shredding protects against ordinary
recovery of deleted files, not against a lab with physical access to the
drive.
:::
