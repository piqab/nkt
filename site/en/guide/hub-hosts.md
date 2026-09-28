---
title: Hub hosts
---

# Hosts: adding, installing, access

![Hosts on the hub](/screens/en/hub-hosts.png)

## Add a host

"Add host" on the "Hosts" page:

- **Name** — a label in the UI.
- **Address** — an IP or a DNS name.
- **SSH port** — usually 22.
- **SSH user** — `root` or a user with passwordless `sudo` (`NOPASSWD`) —
  that's how typical VPS images are set up (`ubuntu`, `debian`, `admin`
  with cloud-init). For a non-root user the installer adds `sudo -n`
  itself; a `sudo` that asks for a password fails with a clear error
  instead of hanging.
- **Login method**:
  - **the hub generates a key** (default, recommended) — after saving,
    the hub shows the public key and ready commands with a "copy"
    button: one to run on the host itself (creates `~/.ssh`, appends the
    key to `authorized_keys`, sets permissions), another — from your
    machine over `ssh`. The private half never leaves the hub. The key
    can be shown again with the "key" button in the host row.
  - **your own private key** — the contents of `id_ed25519`/`id_rsa` (not
    `.pub`), OpenSSH/PEM format, without a passphrase. The form checks the
    key right away and names the reason for a rejection: a `.pub` pasted,
    PuTTY `.ppk` format (export to OpenSSH with puttygen), a key with a
    passphrase (remove it with `ssh-keygen -p`).
  - **password** — a regular SSH password.

### Prepare a new host

For a fresh server that has only root and a password (on by default for
password login, optional with the hub's key). Runs once, before
installation; Debian/Ubuntu only — via `apt-get`; on another system it
refuses honestly.

1. **Packages** — `dbus sudo iproute2 procps ca-certificates curl tmux btop`
   (the list is editable in the form). Without `dbus` the terminal won't
   open and installs from the UI won't work, without `iproute2` the
   socket list is empty, without `procps` processes aren't linked to
   ports.
2. **User** (`nkt` by default) — with a home directory and a `NOPASSWD`
   rule in `/etc/sudoers.d/`. The file is first checked with `visudo -cf`
   in a temporary location: a broken file in `sudoers.d` breaks `sudo`
   for everyone at once.
3. **Key instead of password** — the hub generates a pair, **appends** the
   public half to `authorized_keys` and verifies login with a new
   connection (for non-root — `sudo -n` too). Only then is the host record
   switched to the key; if the check fails, the password keeps working.
4. **Disable password login** (optional) — a drop-in
   `/etc/ssh/sshd_config.d/99-nkt-no-password.conf`, checked with
   `sshd -t`, a reload without dropping sessions and one more key login
   check. If the daemon stops letting in after the reload, the drop-in is
   removed. A failure here doesn't abort the installation.

### Switches in the host form

- **API port** — what nkt on the host listens on (loopback only). Empty —
  the hub-wide `NKT_HUB_HOST_API_PORT` (8077). Changing it reinstalls
  nkt.
- **Web terminal** — a root shell in the browser; off by default. Stored
  in the hub's database and passed to the host on every install — a
  manual edit of `nkt.env` on the host is lost on the next update.
- **Fallback channel** — on by default, see below.
- **Packages through the hub cache** — on by default, see
  [Package cache](/en/guide/hub-cache).

## Installation

After "Add host" press "install". Installation, like every update, is a
**hub job**: a live log, the "Jobs" section, survives a page reload,
cancellable with a button in the host row (really interrupts the current
step). A transient SSH error (refused, dropped, timeout) is retried three
times.

1. SSH connection, architecture detection.
2. The nkt binary for it — built from the hub's source or taken from the
   cache; without source — downloaded from GitHub Releases.
3. Delivery of the binary, unit and `nkt.env`. On every delivery the hub
   measures both paths — the host downloading the release from GitHub
   itself and an SFTP upload from the hub — and picks the faster one
   ("GitHub from the host X MB/s, SFTP from the hub Y MB/s"). GitHub is
   used only if the release asset is byte-for-byte identical to the
   hub's binary; the checksum from `SHA256SUMS` is verified on the host.
   If the download fails, SFTP takes over in the same job.
4. `systemctl daemon-reload && systemctl enable netknownsthat && systemctl restart netknownsthat`
   — `restart` specifically, so an already running service picks up the
   new binary and environment.
5. A `/health` check and login with the host administrator account the
   hub keeps encrypted. If the host already had an account with another
   password (a previous manual install), the hub resets it with
   `nkt passwd` over the same SSH.

Once the status is "connected", "open" leads to the host's panel; on top
there is a narrow hub strip with the host name and "back to hosts". A
closed install log reopens with the "install log" button.

A hub restarted in the middle of an installation turns stuck "installing"
entries into "error" on start — the buttons are available again.

## Host list

- **Availability icon** before the name: green — answered the last poll,
  red — unreachable (the tooltip says when it last answered), gray — not
  polled yet. Polling — every `NKT_HUB_FINDINGS_POLL_INTERVAL` (60 s)
  over the already open SSH connection.
- **Findings** — finding counters; for an unreachable host — numbers
  from the last successful poll (dimmed) or "no data".
- **nkt version** — if it differs from the hub, "on hub: …" appears next
  to it and the button becomes "update". "Open" on an outdated host
  updates it first and goes to the panel only after success. The
  comparison uses the hub version from the same response, so an old tab
  doesn't reinstall hosts in a loop after the hub is updated.
- **"Update all"** — a job for each outdated host, at most three at a
  time (otherwise dozens of SSH connections hit sshd's `MaxStartups` or a
  jump host). The confirmation window shows who gets updated and who
  doesn't: hosts where an update is already running or queued are left
  alone ("updating: N" next to the button), unreachable ones are skipped,
  and ones that failed last time only with the "retry failed" checkbox.
  While updates run, the host list refreshes every few seconds.
- **Sudo** — what the last installation found: "passwordless", "password
  required" or "unknown". With "passwordless" there is a **"remove
  NOPASSWD"** button — it deletes `/etc/sudoers.d/nkt-hub` when permanent
  access is no longer needed (after that the hub needs sudo or root again
  for updates).
- **Channel** — the fallback channel state: gray — off; green
  "connected" — held, regular SSH is used; yellow "via fallback" — SSH is
  unavailable right now, everything goes through the channel.
- **"Edit"** — name, address, port, user, login method; an empty secret
  field keeps the old one. "Forget host key" is there too.
- **"Start all" / "stop all"** — `systemctl start|stop netknownsthat` over
  SSH on every host; not a reinstall.

If nkt on a host is restarting (package upgrade, self-update), the hub
waits up to 20 s, trying to log in every 2 s, instead of failing. If the
API never comes up, the error says so plainly: "SSH answers, but the nkt
API on the host is not up" — with diagnostics gathered over SSH:
the unit state, who listens on the API port, the last journal lines.
"Update" from the hub reinstalls and restarts the service over SSH.

## Host SSH key

On the first connection the hub remembers the host's SSH key and requires
the same one afterwards, like `known_hosts`: a substituted key gives the
error "SSH host key of … changed: known SHA256:…, presented SHA256:…"
instead of logging in with a password to someone else's machine. After
reinstalling a host press **"forget host key"** in the "edit" form — the
next connection remembers the new one. The fingerprint is shown there
too.

## Fallback channel

On by default for new hosts. The hub opens one more port on the host
(`8078`, `NKT_HUB_TUNNEL_PORT`) and dials it itself at the same address
as SSH — no separate hub address is needed, even if the hub is on a home
or office network.

- Helps when SSH specifically is unavailable: sshd crashed or is
  misconfigured, credentials expired, only port 22 is closed. A firewall
  that closes **all** incoming ports closes this one too.
- The hub always tries SSH first; the channel is used only on failure.
- The panel and terminal go through the channel as is. Updates differ:
  SFTP and sudo don't go through the channel, so the hub sends the built
  binary and `nkt.env`/unit to the host's `POST /api/self-update`, and
  the host puts them in place and restarts via `systemd-run`. The channel
  never does the very first installation — only updates of an installed
  host.
- Authentication — a random per-host token, new on every install, passed
  over SSH; the hub keeps it encrypted. Five wrong attempts from one
  address — a temporary block. The host certificate is self-signed; the
  hub pins its fingerprint on the first connection and checks it every
  time. On a mismatch the connection is refused and logged; a reinstall
  through the hub resets the pin.

## Groups and profiles

Hosts are grouped by drag and drop. A group can have a **profile** (set
when the group is created): machines created in it follow the profile,
and their rows are tinted with the profile's color. Hosts moved into a
group are not touched. A host's drift from the profile shows in its
"Findings". Hub profiles are edited in the "Profiles" section.

## Machines inside a host

A **new machine** is created on a host right from the hub: cloud-init,
nkt inside, the group's profile. **Find machines on the host** (a tab in
the same window) registers libvirt domains created outside nkt: each with
all its addresses (libvirt lease, guest agent, host ARP; without
loopback, link-local and the guest's internal bridges), its own SSH
credentials under the row and **"check access"** — route, ping, SSH port
from the host and from the hub, with a one-line diagnosis. A machine on
**macvtap** is marked: the host can't see its own macvtap guests, such a
machine needs a direct connection.

**Machine link**: *auto* — the hub tries the machine's SSH directly
(1.5 s, the result is remembered for 10 minutes) and bypasses the host if
it answers, otherwise goes through the host; *direct*; *via host*.

## Export and import

"Export" saves everything that takes long to set up again: hosts with
encrypted SSH and admin secrets and settings, groups (empty ones too),
machine-to-host links, Kubernetes clusters (nodes, API address,
kubeconfig, WireGuard plan), profiles and scripts with history, machine
templates, hub settings (alerts, package cache, model analysis with the
API key, beta channel), saved cluster form presets, fallback channel
switches and tokens. The file is JSON version 3; versions 1 and 2 import
too.

- **The hub's encryption key travels in the file**: the receiving hub
  decrypts the secrets with it and re-encrypts them with its own right
  away. That's why the export **insists** on encrypting the file with a
  password (Web Crypto, PBKDF2 + AES-256-GCM, the password never leaves
  the browser); an empty password only through a separate confirmation.
  The format is shared with `nkt hub delete -export` and
  `nkt hub import`.
- **Import appends**: a host, cluster, profile, script or template whose
  name is taken is skipped with a message; settings are written only
  where they haven't been set yet.
- **Not carried over**: cluster images (only their list — the import says
  which to copy into `cluster-images` by hand), the package cache, jobs,
  the alert log, user accounts, deployment pipelines and nkt-edge
  settings, the fallback channel certificate pin (the new hub pins its
  own).
