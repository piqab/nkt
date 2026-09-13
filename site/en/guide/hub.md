---
title: Hub — many hosts
---

# Hub: many hosts from one place

A regular `nkt` manages one host. `nkt hub` is the same binary in another
mode: it lives on a separate machine and manages the others **over SSH**,
showing for each of them the same panel a standalone nkt would — with a
host picker in the UI instead of a separate install on every host.

![Hosts on the hub](/screens/en/hub-hosts.png)

## 1. Install

The same binary as for a host ([how to get it](/en/guide/getting-started#_1-the-binary)),
a different unit and env file:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/hub.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/hub.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat-hub.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat-hub.service
sudo $EDITOR /etc/netknownsthat/hub.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat-hub
```

From a clone of the repository `sudo make hub-install` does the same. There
is also a prebuilt image for Docker Compose and Kubernetes — manifests in
[`deploy/`](https://github.com/piqab/nkt/tree/main/deploy), details in
[HUB.md](https://github.com/piqab/nkt/blob/main/HUB.md).

Check that it is up:

```bash
curl -s http://127.0.0.1:8077/api/health
journalctl -u netknownsthat-hub -f
```

Like nkt, the hub listens on `127.0.0.1` only — from outside use an SSH
tunnel, `NKT_TLS_ENABLED=true` or a reverse proxy with TLS (see
[install on a host](/en/guide/getting-started#_4-open-it-in-the-browser)).

## 2. First login

On the first start the hub creates an administrator and prints the password
to the journal:

```
=== Hub administrator account created ===
  login:    admin
  password: <string>
```

It is shown nowhere else — save it. The first row of the host list is always
**localhost** — the hub machine itself, without SSH or an install: it can be
opened right away.

## 3. Add a host

“Add host” → name, address, SSH port and user (`root` or a user with
passwordless `sudo`, the way typical VPS images are set up). Login method:

- **the hub generates a key** (recommended) — after saving, the hub shows the
  public key; add it to `~/.ssh/authorized_keys` on the host and only then
  click “install”. The private half never leaves the hub.
- **your own private key** — the contents of `id_ed25519`/`id_rsa` (not
  `.pub`).
- **password** — the plain SSH password. For a fresh server with only root
  and a password, enable **“Prepare a new host”**: the hub installs helper
  packages, creates a user with `NOPASSWD`, switches the login to a key and
  optionally disables password login — every step checked and reverted on
  failure.

Then “install”: the hub copies the binary for the right architecture, puts
the unit in place and starts the service; progress is in a live log. The
host shows up in the list with its version, findings and availability —
“open” leads to the same panel a standalone nkt has.

Useful switches in the host form:

- **web terminal** — a root shell in the browser, off by default;
- **fallback channel** — a reverse TLS tunnel for when SSH gets firewalled:
  the panel, the terminal and updates keep working through it.

## 4. What the hub offers

- **Groups** — hosts are grouped by drag and drop; a group may have a
  profile: machines created in it are built from it.
- **Machines inside a host** — a virtual machine is created on the host
  straight from the hub, nkt is installed into it automatically, a profile
  is applied.
- **Profiles** — the desired state of a host in YAML (packages, services,
  files, firewall, accounts, compose stacks) with a plan and application as
  a job; drift shows up as findings.
- **Scripts** (experimental) — a line-based deployment language: create a
  group and hosts, install nkt, packages, Docker, stacks, machines; dry run,
  reference and a scheme right in the UI.
- **Alerts** — host down / back / serious findings / job failed; settings for
  what to record and what to notify about via the browser.
- Hub **jobs** with logs; **export and import** of the whole hub (an
  encrypted file); **update** and **rollback** of the hub and all hosts from
  “About”; a central trivy vulnerability database.

Every feature is covered in detail in
[HUB.md](https://github.com/piqab/nkt/blob/main/HUB.md); the full list is
on the [features](/en/features#hub) page.
