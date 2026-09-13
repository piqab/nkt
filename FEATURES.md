# NetKnownsThat features

Everything nkt can do — by UI section, in menu order. One line, one
feature. How to install and run it is in [README.md](README.md), the hub
in detail is in [HUB.md](HUB.md), version-by-version changes are in
[WHATSNEW.md](WHATSNEW.md). Русская версия: [FEATURES.ru.md](FEATURES.ru.md).

## General

- One static binary: web UI, API, terminal UI and hub.
- Three modes: `local` (a real host), `fixtures` (a host snapshot for development, works on Windows too), `hub`.
- Russian and English UI: the language follows the browser, switches at the bottom of the menu, and every server message arrives in the UI language.
- Light, dark and system theme; the sidebar collapses to icons (by button or automatically on narrow screens), the state is remembered.
- Accounts with `admin` and `viewer` roles, argon2id passwords, sessions, brute-force protection, login with a host system account (PAM).
- Read-only mode (`NKT_ALLOW_MUTATIONS=false`) that disables every change.
- Audit log: every change is recorded with the user, the outcome and the command output.
- Runs under systemd in a sandbox (`ProtectSystem=strict`) with a controlled escape for commands that need system access.
- "Allow writing" button opens a directory to the unit (a `ReadWritePaths` drop-in) and restarts the service when a config lives outside the allowed paths.
- Sandbox diagnostics: why the terminal or the sandbox escape does not work, with commands to fix it.
- Background jobs with a log, steps, cancellation and resumption after a service restart; the "Jobs" section.
- Scheduler for background checks and metric collection (`NKT_SCHEDULER_ENABLED`).
- Host self-update through the hub or with a manually uploaded binary.
- Own TLS for the web UI (`NKT_TLS_*`) with an automatic self-signed certificate.
- JSON API to everything visible in the UI, with the same authentication.

## State

### Overview
- Host summary: OS, kernel, uptime, load, memory, disks, network.
- What changed since the last visit: new and gone problems, services, containers, certificates.
- Finding counters by severity linking into "Findings".
- Rescan button.
- List of known helper packages (htop, ncdu, jq…) with an "installed" mark and one-click install.

### Findings
- Analysis of nginx, haproxy, caddy, docker compose, firewall and certificate configs, cross-checked against the host's real state.
- Every finding: severity, explanation, file and line, a concrete fix, documentation links.
- Network and firewall rules: port conflicts, declared-but-not-listening, listening-but-not-declared, no default deny, public port blocked by the firewall, Docker bypassing the firewall, stale rules, sensitive services on all interfaces.
- TLS rules: weak protocols, missing HSTS, certificate not set, expired, expiring, not yet valid, unreadable, name mismatch, not renewed automatically, orphan certbot lineage, self-signed, weak key or signature, service did not reload the certificate, public plaintext proxy.
- Pool and container rules: undefined or orphan upstream, backend down, all backends disabled, single backend, no health check, container restarting, not running, undeclared, no restart policy, haproxy stats panel without a password.
- Host drift from an applied profile — as a finding.
- Search and filters by severity, service and text.

### Vulnerabilities
- Scan of installed OS packages for CVEs with trivy (the database downloads automatically or comes from the hub).
- Scan of Docker/Podman container images on the host.
- trivy is installed automatically on the first scan.
- Vulnerability list with severity, package, fixed version and links; filters.
- Scan progress in real time.

### Resource map
- Graph "external network → service → listener → pool → backend → container → docker network" built from configs and the real state.
- Node status from live listeners, containers and findings.
- Stable column layout, zoom and drag with the mouse, node details on hover.

## Monitoring

### Availability
- Every declared listener and pool backend is checked on a schedule: a TCP connection or an HTTP request with the right Host header.
- "Hour of week × downtime" heatmap, availability and latency charts, outage list.
- Custom check targets in addition to the discovered ones.

### Load
- Load charts from iptables counters, `docker stats` and nginx/haproxy access logs for a chosen period, a rating of the busiest resources, a load schedule by hour.
- Live `btop` in a terminal window, installed if missing, with key hints.

### Logs
- journald logs by unit and files from `/var/log`, including archived and compressed ones.
- Live log following over WebSocket with a filter, highlighting, case sensitivity and autoscroll.
- Detach the log into a separate browser window.

### Jobs
- List of the host's background jobs: kind, step, status, author, duration.
- Job log in real time, cancellation; jobs interrupted by a restart resume or get marked.

### Audit log
- Every change made through the UI and API: who, what, when, with what outcome and command output; filters by action and outcome.
- State of the scheduler's background tasks: interval, last run, processed, errors.

## Host

### Services
- systemd units with state, autostart, description and ports.
- Actions: start, stop, restart, reload, enable/disable autostart, unit log.
- Service configuration check (`nginx -t`, `haproxy -c`, …) before an action.
- "Other services": processes started by hand or from a container, with their sockets and exposure; terminate with SIGTERM/SIGKILL.
- Install a missing service as a package with a live apt log.
- Open port probe: TCP, HTTP/HTTPS, TLS handshake, arbitrary `curl` — with body, headers, rendering of the received page and response download.

### Containers & VMs
- **Docker**: containers (state, image, ports, networks), start/stop/restart/remove, logs, container creation, compose stack scanning.
- **Docker → images**: list with size, date and usage, removal, saving to a tar on the host, pruning dangling layers.
- **Docker → stacks**: the host's compose files, `up`/`down`/`restart`, compose editing through the config editor, a new stack from a template.
- Docker installation from the official docker.com repository (or the get.docker.com script) with a live log.
- **Podman**: containers over its own socket, the same lifecycle.
- **LXD**: containers and virtual machines, `launch`, start/stop, removal.
- **Virtual machines (libvirt/QEMU)**: domain list, start/shutdown/force-off/reboot, autostart, removal with or without disks, machine addresses.
- Machine creation from a cloud image: name, cores, memory, disk, network, user and SSH key via cloud-init; missing tools are installed automatically; machine templates for repeat creation.
- Cloud image catalog (Ubuntu, Debian, …) and own images: download with checksum verification, upload of an own file, move into the disk directory.
- libvirt networks: list, NAT network creation with DHCP and autostart, subnet overlap check against host networks and interfaces, bridge onto a host interface.
- Domain XML editing through the config editor with `virt-xml-validate` and `virsh define` on apply.
- **Profiles**: the host's desired state in YAML — packages, services, files, firewall rules, accounts, system settings, compose stacks.
- Apply plan with risky items marked, application as a job with a log, scheduled drift check.
- Profile export and import, profile versions, a format guide in both languages right in the UI.

### Packages
- apt package search ranked by match, installation of several packages in one transaction.
- Installed packages as a grid with search and removal.
- Available updates and `apt-get upgrade` in a live terminal window (no `-y`, with confirmation).
- snap and flatpak packages: list, update, removal.
- Package description on hover.

### Configs
- Every discovered config of nginx, haproxy, caddy, docker compose, systemd, cron, sshd, netplan, libvirt — by category, with jump-to-line from a finding.
- Editor with line numbers; the config is checked by the service itself before writing, and on failure the file is restored automatically.
- Version history of every file: view, diff, rollback to any version, a note per change.
- Apply after writing: service `reload`/`restart`, `docker compose up`, `virsh define`, `netplan` — with a check.
- Block editor: a tree of nginx/haproxy blocks (`server`, `location`, `frontend`, `backend`), add, edit and remove a block without breaking its neighbours.
- sshd guard: before writing it checks whether a way back to the host remains if sshd does not come up; after writing, that sshd accepts connections — otherwise rollback.
- New file in a category's directory, browsing only down from the root.
- Writing into directories outside the sandbox with an "Allow writing" offer.

### Disks
- File systems with usage, pseudo file systems hidden by default.
- "What takes space": subdirectory sizes on click, one level at a time.
- Swap, physical disks and partitions with expansion.
- **Files**: a browser over allowed roots (`/home`, `/srv`, `/opt`, `/var/www`, `/tmp`; configurable) — folders, rename, delete, download.
- Upload of files and whole folders via the dialog or drag-and-drop, with an overall progress bar; the folder tree is recreated on the host.
- zip/tar archive extraction in place with protection against escaping paths.
- `git clone` as a job with a log, private repositories included: a token over HTTPS or the host's deploy key over SSH.
- File editor with line numbers, renaming, mode preservation and protection against overwriting someone else's edit.

### Hardware
- Machine, CPU, memory, batteries, temperatures (lm-sensors), PCI and USB devices.

### System settings
- Machine name, time zone (with search), NTP flag, state of automatic security updates.
- Locales: everything the system knows as cells with search, generation of the selected ones, choosing the default.
- Time synchronization: which service is installed (systemd-timesyncd, chrony, ntpsec), what it syncs with, stratum and offset; NTP servers from a list or your own; "synchronize now"; service installation if none is present.
- NetworkManager: connections, devices, Wi-Fi networks, connecting with a password.

### Terminal
- A full interactive shell on the host in the browser (`NKT_TERMINAL_ENABLED`), tmux sessions, detaching into a separate window.
- Toolbar: copy, clear, font size, search.
- Installation of tmux and dbus helpers if missing.

## Network

### Network interfaces
- Interfaces with addresses, state, counters and errors; interface kind (bridge, veth, WireGuard, tunnel, libvirt) and the containers attached to it.
- Listening sockets with processes; ports open on `0.0.0.0` get a one-click port probe.

### Firewall
- **ufw**: state, numbered rules, adding and removing rules (port/protocol/source/comment), enabling with port 22 protection.
- **firewalld**: zones, services, ports, adding and removing temporarily and permanently, `reload`.
- Installation of ufw or firewalld as a package with a live log when the host has neither.
- iptables view as is (no editing).

### Certificates
- Every certificate from nginx, haproxy, caddy configs and `/etc/letsencrypt`: expiry, names, issuer, algorithm, key, self-signed or not.
- haproxy `crt` directories expand by SNI, derived copies are found by fingerprint.
- Check against the real TLS socket: the service serves the same certificate that lies on disk.
- Auto-renewal state: whether certbot knows it, whether the timer or cron is active.
- certbot lineage renewal via `--standalone`, stopping and restoring the services and processes holding 80/443 (including manually started ones), with their relaunch.
- Issuing a new Let's Encrypt certificate; certbot check and installation if missing.
- Assembling a haproxy PEM from a certbot lineage with a haproxy reload.
- Self-signed certificate: RSA 2048/3072/4096, several names, wildcard, Unicode domains.
- nginx, haproxy and caddy config snippets for the issued certificate with copy to clipboard.
- Scheduled auto-renewal (`NKT_AUTO_RENEW_CERTS`).

## Access

### Users
- nkt accounts: creation, role, disabling, password change, session reset.

### Host accounts
- System users with shell, home, groups and sudo.
- User creation with an SSH key, passwordless sudo, removal.

## Hub

- One hub manages many hosts: a host connects over SSH (password or key), nkt is installed on it automatically for the right architecture.
- Hosts are grouped; each has a status, nkt version, problems, reachability, sudo and fallback channel.
- Open any host in the same UI — every section above works through the hub.
- The "localhost" row — the hub's own machine without SSH.
- Install, update, reinstall, stop and start nkt on a host; "update all" for hosts that lag behind.
- A host whose version differs from the hub's (older or newer) is brought to the hub's version when opened.
- Fallback channel (a reverse TLS tunnel with certificate pinning) for when SSH is unavailable.
- Revoking passwordless sudo, address diagnostics, complete nkt removal from a host (restoring password login).
- Machines inside a host: creating a virtual machine on a host from the hub, automatic nkt installation into it, profile application; discovery of existing machines and adding them to the list; start/shutdown through the parent host; removal together with disks.
- A "Profiles" section on the hub; a profile is set on a group at creation, and every host that joins the group is brought to it as a job; drift shows in the host's Findings.
- Alerts: unreachable, responding again, serious problems appeared, resolved, job failed; an alert journal with settings for what to record and what to notify about, collapsing short episodes; browser notifications.
- Hub jobs with a log.
- Whole-hub export and import: hosts with secrets, groups, machines with parents, profiles with history, machine templates, settings; the file is password-encrypted.
- A centralized trivy vulnerability database for all hosts, refreshed on a schedule and by button.
- "About": hub version, GitHub release check, update to the latest, rollback to the previous, the new version's notes before installing.
- Configuration via `hub.env`, running as a systemd unit, in Docker Compose or Kubernetes.

## Terminal UI (`nkt tui`)

- The same data in a terminal: overview, findings, resource map as a tree, services, containers, certificates, configs, availability and load.
- Service and container control, certificate renewal, config rollback.
- In Russian and English.

## Command line

- `nkt serve` — start the web UI; `nkt scan` — a one-off scan to JSON; `nkt version`.
- `nkt users` and `nkt passwd` — accounts and passwords without the web UI.
- `nkt hub` — start the hub; `nkt hub import` — restore the registry; `nkt hub delete` — complete removal of the hub's data with an export offer.
