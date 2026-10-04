---
title: Features
outline: [2, 3]
---

# Features

Everything nkt can do, by UI section — one line, one feature. How to install it — in the [guide](/en/guide/getting-started); what changed from version to version — in [WHATSNEW.md](https://github.com/piqab/nkt/blob/main/WHATSNEW.md).

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
- Long operations — installing packages and engines, the system upgrade, creating LXD instances and Podman containers, downloading images, backups, changing guest passwords — run as jobs: the log window opens right away with percentages, closing it stops nothing; on the host and through the hub.
- UI windows resize with a corner and maximize to the full screen; the size of log, job, editor, terminal and machine screen windows is remembered.
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
- Action buttons on a finding: "Open at line N" (the line highlighted in "Configs"), "Go to certificate", "Go to service", "Go to container", "Logs" and "Restart"/"Start" for a container, "Firewall", "Port N in Firewall" and "On the resource map" for an undeclared listener (the port's rules and socket highlighted, the map node opened), "Go to malware" with the card highlighted, "Open in files"; the same buttons in "What's broken" on the overview, and a resource map node links to its service, config or container.
- Network and firewall rules: port conflicts, declared-but-not-listening, listening-but-not-declared, no default deny, public port blocked by the firewall, Docker bypassing the firewall, stale rules, sensitive services on all interfaces.
- TLS rules: weak protocols, missing HSTS, certificate not set, expired, expiring, not yet valid, unreadable, name mismatch, not renewed automatically, orphan certbot lineage, self-signed, weak key or signature, service did not reload the certificate, public plaintext proxy.
- Pool and container rules: undefined or orphan upstream, backend down, all backends disabled, single backend, no health check, container restarting, not running, undeclared, no restart policy, haproxy stats panel without a password.
- Kubernetes pod image vulnerabilities (containerd of the node or the registry), with the pods using each image.
- Kubernetes rules (on a control plane): pod in CrashLoopBackOff/ImagePullBackOff, a pod restarted within the last hour, long Pending, NotReady node, Deployment/StatefulSet missing replicas, stuck PVC, expiring API server certificate, privileged/hostNetwork pods, NodePort/LoadBalancer bypassing the firewall, containers without limits, images without a version, cluster-admin bindings, namespaces without a NetworkPolicy.
- Host drift from an applied profile — as a finding.
- Search and filters by severity, service and text; findings that appeared since the last review are tagged “new”, with an “only new” filter.

### Vulnerabilities
- Scan of installed OS packages for CVEs with trivy (the database downloads automatically or comes from the hub).
- Scan of Docker/Podman container images on the host.
- Packages inside guests: LXD instances (via lxd-agent for LXD VMs) and libvirt machines (via qemu-guest-agent) — locally and on the hub; a finding's origin is "LXD name" or "VM name".
- trivy is installed automatically on the first scan.
- Vulnerability list with severity, package, fixed version and links; filters.
- "By danger" order: first what is reachable from the network (the package's service listens beyond loopback, a container publishes a port; with the ports), then severity, then whether a fix exists; "only reachable from the network" and "only with a fix" filters.
- Scan progress in real time.
- The “Malware” tab: a heuristic check for miners and signs of a break-in with no third-party tools — processes named like miners, pool connections, binaries deleted from disk or started from /tmp, long CPU load by a process not from any package, /etc/ld.so.preload, cron with “curl | sh”, units from temporary directories, foreign SUID files, blocks in /etc/hosts; on every scan, results go to Findings and hub alerts.
- ClamAV: package install, signature database updates, scans of chosen host directories and container images (one, selected or all) — as host jobs, one at a time, in the standard job log window, with a cancel that also stops clamscan itself; quarantine of infected files with restore and purge; hits link to the file browser row, "Configs" or the container.
- Every vulnerability links to where it is fixed: "To package" (the cell highlighted in "Packages") or "To containers" (the rows of containers running that image).

### Resource map
- Graph "external network → service → listener → pool → backend → container or machine → network" built from configs and the real state.
- libvirt machines and LXD instances are linked to their networks and to backends pointing at their address; forwarded LXD ports are an entry from the host into the instance.
- A machine node shows its address, ping, current CPU and memory and the vulnerabilities of its packages; no ping reply turns it red, critical vulnerabilities yellow.
- Kubernetes: Ingress → Service → pods → cluster node → the host machine it runs on.
- Node status from live listeners, containers and findings.
- Stable column layout, zoom and drag with the mouse at any zoom without selecting text, node details on hover.
- Ports of one service above a threshold (set above the map, 4 by default) fold into one node "service · N ports"; its window lists the ports, each with a link to where it is configured, and "Expand on the map".
- Opening a node by a link from a finding.

## Monitoring

### Availability
- Web server listeners and backends, published Docker and Podman ports and forwarded LXD ports are checked on a schedule: a TCP connection or an HTTP request with the right Host header.
- Running LXD instances and libvirt machines are checked by ping at their address.
- "Hour of week × downtime" heatmap, availability and latency charts, outage list.
- On a Kubernetes control plane, cluster targets: Ingresses, NodePort and LoadBalancer services, nodes (ping).
- Your own targets with "+ target": ping, TCP, HTTP or HTTPS to any address; deleted from the list, scans never touch them.

### Load
- Load charts from iptables counters and nginx/haproxy access logs for a chosen period, a rating of the busiest resources, a load schedule by hour.
- CPU, memory and network of containers and machines with a source picker: Docker, Podman, LXD, Libvirt (`virsh domstats`), Kubernetes and "Whole host"; CPU and the first source with data open by default.
- Live `btop` in a terminal window, installed if missing, with key hints.
- Kubernetes: pods and cluster nodes (workers included) as separate sources; node and namespace filters; an object picker on the chart instead of an anonymous “Other”.
- Charts from hourly summaries kept as samples are written: the page does not hold the host's database; per-minute samples for three days, summaries for at least 90 days.

### Logs
- journald logs by unit of installed services and files from `/var/log`, including archived and compressed ones.
- Live log following over WebSocket with a filter, highlighting, case sensitivity and autoscroll.
- Detach the log into a separate browser window.

### Jobs
- List of the host's background jobs: kind, step, status, author, duration.
- Filters by state and kind, a text search across all jobs, pages of 20/50/100 and date order, like the alert journal; the hub's "Jobs" work the same way.
- Job log in real time, cancellation (also right from the log window); jobs interrupted by a restart resume or get marked.

### Audit log
- Every change made through the UI and API: who, what, when, with what outcome and command output; filters by action and outcome; the action kinds come from the log itself, so new sections appear in the filter automatically.
- State of the scheduler's background tasks: interval, last run, processed, errors.

## Host

### Services
- systemd units with state, autostart, description and ports.
- Actions: start, stop, restart, reload, enable/disable autostart, unit log.
- Service configuration check (`nginx -t`, `haproxy -c`, …) before an action.
- "Other services": processes started by hand or from a container, with their sockets and exposure; terminate with SIGTERM/SIGKILL.
- The table shows every installed service, running and stopped (with "start"); services that aren't installed are installed via Packages.
- Open port probe: TCP, HTTP/HTTPS, TLS handshake, arbitrary `curl` — with body, headers, rendering of the received page and response download.

### Containers & VMs

- Deleting in bulk by checkboxes (containers, instances, snapshots, machines, images, disk files, LXD networks, Kubernetes objects); long deletions run as a job with the row locked until done and related tables rebuilt afterwards.
- **Docker**: containers (state, image, ports, networks), start/stop/restart/remove, logs, a console inside, container creation, compose stack scanning; container backup and restore (a compose stack as a whole).
- **Docker → images**: list with size, date and usage, removal, saving to a tar on the host, pruning dangling layers.
- **Image archives** for Docker, Podman and LXD: save or export to an archive on the host, download to your computer, upload from it (with progress, via the hub too), load into the engine or import into LXD, as background jobs; machine images get "Download to computer"; installing Docker and libvirt/KVM with a button right in the tab.
- **Docker → stacks**: the host's compose files, `up`/`down`/`restart`, compose editing through the config editor, a new stack from a template.
- Docker installation from the official docker.com repository (or the get.docker.com script) with a live log; when only the client is missing (Debian 13: docker.io without docker-cli) — just docker-cli and the compose plugin are installed, with a finding pointing it out.
- **Podman**: containers over its own socket, the same lifecycle, console, backup; creation as a background job.
- **LXD**: containers and virtual machines — creation as a background job with an image picker (`images:`, `ubuntu:`, local) and an optional login password, start/stop/pause, resources and limits, autostart, logs, console, the VM screen over SPICE.
- **LXD → snapshots and backup**: take/restore/delete a snapshot, backup via `lxc export` and restore as a copy or over the original.
- **LXD → configuration**: `lxc config edit` in a window with limit fields, a diff and version history; port forwarding (proxy devices) through the same edit; the configuration with profiles for reference.
- **LXD → host resources**: networks (create a bridge, delete), images on the host (delete, download in advance), storage pools.
- **Libvirt (KVM virtual machines)**: domain list, start/shutdown/force-off/reboot, autostart, removal with or without disks, machine addresses, backup with disks without stopping the machine.
- The machine screen in the browser: VNC (noVNC) or SPICE (spice-html5), Ctrl+Alt+Del; a SPICE-only machine gets an "Add VNC" button — an XML edit with a diff. The serial console (`virsh console`); on connect nkt presses Enter itself.
- Guest login and password: a "Login" line in console and screen windows, "show" (to an administrator, audited), "set password" as a job (LXD — `chpasswd`, libvirt — qemu-guest-agent); the password is stored in nkt encrypted.
- Machine creation from a cloud image: name, cores, memory, disk, network, user, SSH key and an optional password (cloud-init gets only a bcrypt hash); missing tools are installed automatically; machine templates for repeat creation.
- Cloud image catalog (Ubuntu, Debian, …) and own images: download with checksum verification, upload of an own file, move into the disk directory.
- libvirt networks: list, NAT network creation with DHCP and autostart, subnet overlap check against host networks and interfaces, bridge onto a host interface.
- **Kubernetes**: a tab on a cluster node — flavor, role, version, nodes (roles, Ready, IP), kubeconfig, node removal; cluster objects by section (workloads with HPA, pods, network with NetworkPolicy, configuration, storage, access — RBAC with the roles bound to each ServiceAccount, nodes, namespaces, events, Custom Resources) with a shared namespace filter, secret values for an administrator with audit; actions — describe, pod logs and `kubectl exec` console, scale, rollout restart, rollout history and rollback, CronJob run now/suspend, a pod or service opened in the browser via port-forward, cordon/uncordon, drain as a job, namespace create/delete, deletion — all audited; object YAML with a text diff and `kubectl diff` before `kubectl apply`, version history with rollback, "new object" from templates; block mode in the YAML editors (objects, containers, ports, rules, keys); Helm — releases, history and rollback, values editing with a diff and upgrade, uninstall, chart install from a repository or oci://, helm installation — all as jobs; node upgrade (k3s binary, kubeadm upgrade) as a job; k3s/kubeadm role installation as a host job.
- Domain XML editing in a window (text and blocks) with a diff, version history, `virt-xml-validate` and `virsh define` on apply.
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
- Editor with line numbers; the written config is checked by the service itself right away; on failure the file is restored automatically and the service does not reload it.
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
- Uploads with a plan (new / changed with a diff / identical / protected, a checkbox per file), a folder's protected files, upload history with rollback as a job, version history of any file; history storage with limits, manual cleanup and an alert at 80%.
- File edits as in Configs: an edit comment, a diff before writing, version history with a diff against the current file and rollback; a file that is a service config is written with the service's check, sharing the history with Configs.

### Hardware
- Machine, CPU, memory, batteries, temperatures (lm-sensors), PCI and USB devices.

### System settings
- Host reboot from "System settings" and the "reboot required" bar: a window with a count of what is running and a list of what will not come back by itself (containers without a restart policy, machines and LXD without autostart, disabled services), with a required checkbox.
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

### fail2ban
- A section right below Firewall: state and version, running jails (log files or journald, rules, failures and bans now/total, a missing log highlighted), banned addresses with ban times, unbanning per row, with checkboxes or all at once, a manual ban into the `nkt-manual` jail with a chosen time.
- Editing a jail in a window with a form (enabled, maxretry, findtime, bantime, bantime.increment, backend, logpath, ignoreip) and the text: the nkt file `jail.d/nkt-<jail>.local`, a diff before writing, a `fail2ban-client -t` check with rollback, reload, version history; enabling and disabling a jail the same way.
- Exceptions (ignoreip): the common `[DEFAULT]` list edited with a diff of every file and version history, the hub address pinned, the effective list of every jail (common or own); edits of fail2ban files in Configs rebuild the hub protection file.
- Event log for 1/3/7 days from `fail2ban.log` and its rotations (including `.gz`) or journald: search, filters by jail and event.
- Templates: sshd, nginx-http-auth, nginx-botsearch, nginx-limit-req, haproxy-http-auth, postfix, dovecot, recidive, offered when the program is on the host; custom templates with a jail, a filter, a `fail2ban-regex` test against a host log and version history, stored on the hub; applying through a diff of every file.
- Installation as a package in the standard job window and setup: the hub's address in `[DEFAULT] ignoreip`, the manual bans jail, sshd via journald where there is no log file.
- Lock-out protection: the hub passes the host its external address as the host sees it (`SSH_CONNECTION`), the host keeps it in `ignoreip`; banning the hub's address or your own is refused.
- Findings: SSH exposed without fail2ban, fail2ban not running, no sshd jail, a jail without logs, the hub not in `ignoreip`.

### Certificates
- Every certificate from nginx, haproxy, caddy configs and `/etc/letsencrypt`: expiry, names, issuer, algorithm, key, self-signed or not.
- haproxy `crt` directories expand by SNI, derived copies are found by fingerprint.
- Check against the real TLS socket: the service serves the same certificate that lies on disk.
- Auto-renewal state: whether certbot knows it, whether the timer or cron is active.
- certbot lineage renewal via `--standalone`, stopping and restoring the services and processes holding 80/443 (including manually started ones), with their relaunch.
- Issuing a new Let's Encrypt certificate; certbot check and installation if missing; before running certbot the name is checked: does it resolve and point at this host (a foreign address is refused unless “host behind NAT or a proxy” is ticked; one that does not answer ping is always refused).
- Assembling a haproxy PEM from a certbot lineage with a haproxy reload.
- Self-signed certificate: RSA 2048/3072/4096, several names, wildcard, Unicode domains.
- nginx, haproxy and caddy config snippets for the issued certificate with copy to clipboard.
- Scheduled auto-renewal (`NKT_AUTO_RENEW_CERTS`).

## Access

### Users
- nkt accounts: creation, role, disabling, password change, session reset.

### Host accounts
- System users with shell, home, groups and sudo.
- Creation and editing in one window with a diff: shell, passwordless sudo (nkt rule), groups as checkboxes (`docker`, `sudo`/`wheel`, `adm` and other common ones first, then all with search; `docker` comes with a warning), keys (add, remove a single one).
- Deletion (`userdel`, optionally with the home directory); root and the hub user cannot be deleted, and cutting off the hub's key or sudo needs a confirmation.

## Hub

- One hub manages many hosts: a host connects over SSH (password or key), nkt is installed on it automatically for the right architecture.
- Hosts are grouped; each has a status, nkt version, problems, reachability, sudo and fallback channel; one line per host, with the same column widths in every group.
- Open any host in the same UI — every section above works through the hub.
- The "localhost" row — the hub's own machine without SSH.
- An empty hub (no hosts besides localhost) shows a "Where to start" card: add a host or import from another hub, open the hub's machine, try a deployment on an example, help on a demo without servers.
- Install, update, reinstall, stop and start nkt on a host; "update all" for hosts that lag behind.
- A host whose version differs from the hub's (older or newer) is brought to the hub's version when opened.
- Fallback channel (a reverse TLS tunnel with certificate pinning) for when SSH is unavailable.
- Narrow sudo: after the first installation the hub may only run `nkt hub-sudo` without a password: operations from a fixed list signed by the hub's key (installing and updating nkt against signed hashes, the service, its journal, the admin password, the apt proxy); a replayed request is rejected. Hosts installed earlier get "narrow sudo" and a "what the hub may do" window (operations and the rule) that checks the host live when opened: the mark corrects itself if sudoers was edited by hand; other lines granting passwordless full sudo are shown (file:line), and a separate line about just this user can be disabled with a visudo check.
- Revoking passwordless sudo, address diagnostics, complete nkt removal from a host (restoring password login).
- Installing nkt on selected hosts as a hub job (three at a time, with a log) in "About" next to removing nkt from all hosts; after an import the hub finds hosts without nkt by itself and offers installation; moving narrow-sudo hosts to a new hub, with the previous hub's signing key from a full export and a host key change (`hub-sudo rekey`).
- Machines inside a host: creating a virtual machine on a host from the hub, automatic nkt installation into it, profile application; discovery of existing machines and adding them to the list; start/shutdown through the parent host; removal together with disks.
- Kubernetes clusters on a host's virtual machines: “new cluster” — k3s or kubeadm, topology (one machine; 1 control plane + N workers; 3 control planes + N workers for k3s), sizes, image, network, Cilium (default, with kube-proxy replacement by a checkbox) or flannel, forwarding of host ports (API/HTTP/HTTPS configurable); the job creates machines with nkt, installs roles, joins nodes, waits for Ready, fetches the kubeconfig; the clusters card — nodes, kubeconfig, “+ worker”, deletion with disks; the “Clusters” section: placement across several hosts (machines or the host itself as a node, roles, sizes) with a network between hosts (NAT / bridge / a WireGuard tunnel with a separate machine network per host); a “dry run” — a checklist of host checks and the machine plan without changes, with preparation (image and packages into caches); manifests — one YAML into several clusters with a text diff and `kubectl diff` per cluster before `kubectl apply`, a per-cluster outcome and a revision history on the hub; cluster upgrade node by node with drain, Ready wait and uncordon; Kubernetes findings across all clusters; a Helm release into several clusters as a hub job; picking clusters by host group.
- Deployments from Git: pipelines (manifest into clusters, Helm with values from Git, hub script, a compose stack deployed host by host with waiting, a dry run and a docker check on the hosts (installed with a button), including from a link to a compose file on GitHub, GitLab or Codeberg) triggered by a button, a signed webhook (GitHub, Gitea, GitLab, CI), repository polling or registry tags; encrypted repository/registry access, revision history, per-deployment jobs and rollback; "Sites": a domain on a host with DNS and port checks from outside, an nginx/HAProxy/Caddy proxy, a Let's Encrypt certificate and a link to a compose stack service, in the wizard or with a site: block right in the pipeline.
- Compose stack deployment: pipeline examples (httpbin, Uptime Kuma, umami, n8n, Forgejo, WordPress, Plausible) from a drop-down; stack ports are published on 127.0.0.1 only (`bind`, `ports` overrides); secrets live in the pipeline's `.env` with version history and an `env_keys` check; YAML errors in the description come with the line number and an explanation; deleting a pipeline removes the stack from the hosts as a job; for a site, certbot and the firewall rule are set up automatically.
- A compose stack dry run with selectable checks (the boxes are remembered on the pipeline): docker and its daemon, `compose config`, variables without a value, images in the registry and their architecture, busy ports, memory and disk, healthcheck, a foreign stack on the host, the site's DNS and ports from outside, who holds 80/443, the certificate and nginx, the nkt version on the host, a container name taken by another project, one host port published twice on overlapping addresses (0.0.0.0 and 127.0.0.1), what happens to the old stack on a move or rename; problems and warnings are colour-coded in the log.
- Changing the host or the stack name in the description: the hub remembers where the stack is deployed; a renamed stack is replaced on the same host (the old one stopped while the new one starts, restarted on failure), the stack is removed from the previous host after success, an unreachable previous host gets a “stack left” mark with Remove and Forget; the Hosts and Stack fields in the editor edit the description right away; host names are case-insensitive, with similar names suggested.
- Pipeline deletion by plan: only where this pipeline deployed the stack; a stack shared with another pipeline (the same name on the same host) is left alone; hosts are picked with ticks; “Only remove from selected” keeps the pipeline without those hosts in its description.
- nkt-edge: a separate small webhook receiver for a VPS (Let's Encrypt via certbot standalone, issued during installation with a name ↔ IP check; filters, rate limit) connected to the hub by a tunnel the hub keeps (pinned TLS, token) — webhooks without exposing the hub; installed from the hub as a job with a busy-port check, can run behind an existing nginx/Caddy on the VPS and be removed from the VPS completely.
- Scripts (experimental): a line-based deployment language (group, hosts, nkt install, packages, services, firewall, Docker and compose stacks, machines, profiles, files, user accounts, system settings, certificates, git clone, k8s clusters; several hosts in one command) with run-time parameters, waiting for a port/HTTP/service, a check, a dry run, a reference and execution as a hub job.
- A "Profiles" section on the hub, each profile has a color; a profile is set on a group at creation: machines created in the group are built from it and their rows are tinted with its color; moving a host into a group applies nothing.
- Model analysis (AI) on the hub: Anthropic or an OpenAI-compatible provider, including local ones (Ollama, vLLM, LM Studio); the model is picked from the provider's list by “Get models” (searchable, release date or size, non-chat models hidden) or typed by hand; a live “Test”, a daily limit, anonymization, editable instructions; “show request” shows the whole request (instruction, message, what was replaced).
- Alerts: unreachable, responding again, serious problems appeared, resolved, job failed, new fail2ban bans; an AI check with its own instruction for every external IP in an alert, with “Ban on all hosts” in the answer window; an alert journal (the "Journal" tab; settings, webhooks and bots are on the "Settings" tab) with settings for what to record, what to notify about and what to hide, filters by kind (all kinds, from the hub) and host and a text search over the whole journal, collapsing short episodes; each event links straight to the host's section with the item highlighted (new findings, bans, disk, container, availability target); browser notifications.
- Hub "Monitoring" (below "Alerts"): availability and load of all hosts from the hub history (hourly summaries from hosts once an hour; hours for 90 days, days for a year), hosts laid out like the hub's host list (groups, CPU, memory and disk bars by thresholds, Kubernetes “control plane”/“worker” and cluster labels), cluster nodes without a hub host separately, each container, machine, pod (with its node) and Kubernetes node separately with cluster, node and namespace filters, heatmaps by hour of week; trend forecasts (disks filling up, memory and CPU, likely leaks, availability drops, latency growth), hints linking to the host section, the quiet window of the week, workload rebalancing, model analysis; "forecast" alerts with thresholds.
- fail2ban across hosts: where it is installed and how many are banned, banned addresses and where (on how many hosts, in which jails), banning and unbanning on all hosts as a hub job with a per-host log (banning an internal address asks for a separate confirmation; "Undo" for 15 seconds after success), jail templates (standard and custom) applied to selected hosts: a required check as a hub job (per host, a diff and a check of the whole fail2ban configuration with the template, plus the hub ban protection), then a hub job three hosts at a time: the hub protection first, a rollback on the host if the configuration fails or a jail does not start, lifting a ban on the hub; all hub fail2ban jobs run in one queue; an “f2b” column in the host list.
- Hub jobs with a log.
- Hub API tokens for automation (n8n, CI, scripts): a read or admin role, a host and group scope, an address and subnet list and an expiry; Bearer or a signed request (HMAC, a one-time nonce, the secret never crosses the network); hub management, the terminal, files and websockets are closed to tokens; the secret is shown once, a new secret and revocation take effect at once; edits come with a diff, and everything goes to the audit log.
- nkt-edge with roles: deployment webhooks and/or the API, meaning signed API token requests from outside (Bearer, cookies, password login and websockets do not pass, and the token secret never reaches the VPS; the token needs the "through nkt-edge" box); several edges on one hub, on different VPSes and names; roles change by reinstalling.
- Outgoing webhooks: the hub sends events out by itself (n8n, a chat bot, your own system): a host going down and coming back, findings, failed jobs, reboots, bans and deployment outcomes; a choice of events, a host and group scope and a text language; an HMAC signature like on incoming webhooks, delivery retries, "Test", the last delivery outcome and edits with a diff.
- n8n nodes (`integrations/n8n`, the n8n-nodes-nkt package): the nkt node for hosts, findings, vulnerabilities, services and containers, deploys and dry runs with waiting for the result, hub jobs, fail2ban, alerts and any token API call; nkt Trigger for the hub's outgoing webhooks with a signature check; alert polling; signed requests (also through nkt-edge); example workflows.
- nkt-edge with the "outside checks" role: a hub behind NAT checks sites as the internet sees them (DNS, ports 80/443, HTTPS and the certificate from the VPS); the "Sites" wizard, the site check and the dry run use such an edge by themselves, and "Check from outside" works for any name.
- A Telegram bot: alerts with buttons ("Overview", "Findings", "Log", "Retry") and chat commands: /status, /hosts, /alerts, /pipelines, /deploy and /ban with a confirmation button, /dryrun; the hub polls Telegram by itself, so no way into the hub is needed; chats with a read or admin role and a list of people allowed to act.
- A Slack bot: alerts with buttons in channels and the /nkt command (status, hosts, alerts, pipelines, deploy and ban with confirmation, dryrun); Slack callbacks go through an nkt-edge with the callbacks role or straight to the hub, and every request is checked with the Slack signature; the command core is shared with the Telegram bot.
- Whole-hub export and import: hosts with secrets, groups, machines with parents, clusters, profiles and scripts with history, machine templates, deployment pipelines with history, secrets, webhook address, dry run checks and the .env history, deployment sites, fail2ban templates with history, nkt-edge with roles, API tokens, outgoing webhooks and bots, settings, the help site address and every AI instruction, web interface accounts by choice; the file is password-encrypted, secrets are re-encrypted with the receiving hub's key; import through a plan with a per-item “skip / replace” choice and a per-section report.
- A centralized trivy vulnerability database for all hosts, refreshed on a schedule and by button.
- A package cache: hosts download .deb files through the hub over a reverse SSH forward (every package comes from the internet once), apt with Proxy-Auto-Detect goes direct when the hub is away; a checkbox in the host form, a card in “About” with a limit and clearing.
- A copy of the ClamAV signature database on the hub (created by a button in “About”, then refreshed on a schedule, only what changed is downloaded) and its upload to a host as a job — “database from hub” in the “Malware” tab.
- "About": hub version, GitHub release check, update to the latest, rollback to the previous, the new version's notes before installing.
- A "Danger zone" in "About": remove nkt from all (selected) hosts after a required full export, choosing hosts (unticked, "select all / clear all"), a typed-word confirmation, a hub job three hosts at a time; cleaned hosts leave the hub.
- Menu layout in "About": the hub section order by drag and drop; host sections, ordered and hidden for all hosts at once; a diff, version history, reset to default; carried in the hub export.
- Configuration via `hub.env`, running as a systemd unit, in Docker Compose or Kubernetes.

## Terminal UI (`nkt tui`)

- The same data in a terminal: overview, findings, resource map as a tree, services, containers, certificates, configs, availability and load.
- Service and container control, certificate renewal, config rollback.
- In Russian and English.

## Command line

- In-app help: the "?" icon next to a section title opens the documentation section for the current page in a window (detachable into a separate window); the site address — the project site or your own — is set in "About".
- `nkt serve` — start the web UI; `nkt scan` — a one-off scan to JSON; `nkt version`.
- `nkt users` and `nkt passwd` — accounts and passwords without the web UI.
- `nkt hub` — start the hub; `nkt hub import` — restore the registry; `nkt hub delete` — complete removal of the hub's data with an export offer.
