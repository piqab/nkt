# What's new in NetKnownsThat

Short notes per version in English — what the user sees, not the list of
commits (that is [CHANGELOG.md](CHANGELOG.md)). The Russian original is
[WHATSNEW.md](WHATSNEW.md); both files get a section with every bump of
`VERSION`, headed `## vX.Y.Z` exactly like the release tag. On a release
`.github/workflows/release.yml` appends the English sections to the
release body after a `<!-- en -->` marker, and the hub shows the reader
their language in "About" when a newer version appears. Newest first.

## v1.11.4 — 2026-09-25

- **Code scanning: the last three findings are closed.** A string from the
  request no longer reaches a command or a path — only the inventory or
  on-disk value it matched:
  - the container and machine console takes the object name from the
    inventory snapshot, the program is a constant, and the user for
    `docker exec -u` is passed as an environment variable to a fixed
    script;
  - starting a container takes the name from the inventory; removing snap
    and flatpak packages takes names from the installed list (an unknown
    name is an error);
  - the backup list and the Podman fixtures in demo mode use directories
    and files from the disk listing.
  Checked with a local CodeQL run of the same rule set as CI: nothing new,
  only the long-dismissed places remain.

## v1.11.3 — 2026-09-25

- **The site screenshots were retaken in both languages.** The "Libvirt"
  tab, usage with the source picker, availability with "+ target" and
  machine targets, the resource map with machines and networks. New
  screens: LXD with networks, images and pools, the LXD configuration
  window and the job window — they are placed in the guide.
- **Demo mode** fills load history for Podman, LXD and libvirt, not only
  Docker: the source picker on "Usage" shows data in fixtures too.
- **The screenshot script** knows the "Libvirt" tab and the new screens and
  removes the synthetic metrics banner. DEVELOPMENT.md explains when the
  stand needs a fresh database and where the job for the screenshot comes
  from.

## v1.11.2 — 2026-09-25

- **The documentation caught up with the code.** README in both languages:
  rewrote the resource map, availability and usage, management (LXD,
  libvirt, jobs, firewalld, Caddy), the API table (by group, with jobs,
  backups, LXD, guests, the screen) and the limitations (dropped the wrong
  "only nginx and haproxy" and "only ufw", added the limits of guests,
  SPICE and the serial console); a license section linking
  THIRD_PARTY_NOTICES.
- **FEATURES and the site's features page** — everything from 1.10.x: LXD,
  the machine screen, guest passwords, jobs, machine load, availability
  and vulnerabilities, machines on the map, window size.
- **Every environment variable is documented:** Caddy and config
  directories, `NKT_CERTBOT_EMAIL`, `NKT_TERMINAL_USER`, the hub tunnel
  variables — in `deploy/nkt.env.example`, the live test variables — in
  DEVELOPMENT.md. The unused `NKT_DEV_PROXY_UI` was removed.

## v1.11.1 — 2026-09-25

- **Code scanning review.**
  - The libvirt machine password hash for cloud-init is bcrypt instead of
    SHA-512-crypt.
  - The password is no longer a field of the shared machine description:
    it exists only in the incoming request and can never reach templates
    or job parameters.
  - The backup path check is `Clean` plus a directory prefix; the backup
    kind comes from the list of constants.
  - The LXD configuration editor escapes a backslash in a value and every
    special character of a key.
  - A machine name is stripped of line breaks before it goes to the log.
- **A guest password** is now 8 to 72 bytes: that is the bcrypt limit.
- **CodeQL skips the third-party spice-html5 and the UI build.** The
  spice-html5 files ship unmodified under the LGPL, and the build is
  checked through its sources in `web/src`.

## v1.11.0 — 2026-09-25

- Minor version: 1.10.x brought LXD on par with Docker and Libvirt (backup,
  snapshots, configuration, ports, networks, images), the SPICE machine
  screen, long operations as background jobs, guest passwords, load,
  availability and vulnerabilities of machines and containers, machines
  on the resource map and resizable windows — enough changes for a new
  line. No code in this release, only the number.

## v1.10.123 — 2026-09-25

- **The libvirt machine console shows "login:" right away.** On connect nkt
  presses Enter itself — before, the screen stayed empty after "Connected
  to domain" until you pressed Enter by hand.
- **Windows resize with the corner.** The antd window wrapper disabled
  clicks (`pointer-events: none`), so the bottom-right corner did not
  drag — only the "maximize" button worked. The corner is a bit larger.

## v1.10.122 — 2026-09-25

- **Fixed the 32-bit ARM build.** A constant in the demo load metrics did
  not fit into int, and the linux/arm release build failed.

## v1.10.121 — 2026-09-25

- **Machines on the resource map.** An nginx/HAProxy/Caddy backend that
  points at a libvirt machine's or an LXD instance's address is linked to
  it — you see which site lives on which machine. Machines are linked to
  their networks (a libvirt bridge, an LXD network); forwarded LXD ports
  are an entry from the host into the instance, like published container
  ports.
- **Machine node details:** address, ping (latency and 24h availability),
  current CPU and memory, the vulnerabilities of packages inside by
  severity. No ping reply turns the node red, critical vulnerabilities
  turn it yellow. The "Networks" column is now shared by Docker, LXD and
  libvirt.
- **Fixed:** an LXD instance's addresses included 127.0.0.1 from the lo
  interface — the availability ping could go to the host's loopback.

## v1.10.120 — 2026-09-25

- **Installs and upgrades run as jobs.** Installing and removing apt,
  snap and flatpak packages, the system upgrade, installing engines (LXD,
  Podman), btop, tmux, ufw and firewalld, and downloading LXD images now
  run as a host background job: the standard job log window opens right
  away with percentages (apt's come from its status), the job shows in
  "Jobs", closing the window stops nothing. Through the hub — the same.
- **The system upgrade as a job** runs with `-y` and keeps local
  configuration files: nobody can answer prompts in the background.
- **Compatibility.** An older host does not know these jobs — the UI falls
  back to the previous live output by itself. Installing dbus still uses
  live output: jobs cannot start without dbus.

## v1.10.119 — 2026-09-25

- **Guest login and password.** When creating an LXD instance or a
  machine in the libvirt wizard you can set a user and password or
  generate one — for logging in to the VNC/SPICE screen and the console.
  Empty — as before, key login.
- **The "Login" line** in console and screen windows: the login, when and
  by whom the password was set; "show" reveals it to an administrator
  (audited); "set password" runs a background job: via `lxc exec` and
  `chpasswd` in LXD (a missing user is created with sudo), via
  qemu-guest-agent in libvirt.
- **Storage.** The password is encrypted with a key in the host's data
  directory; it never goes into job parameters, a command line or a log —
  the job takes it from the store into a temporary script. A machine's
  cloud-init gets only a SHA-512 hash. Deleting a guest forgets its
  password.

## v1.10.118 — 2026-09-25

- **Creating an LXD instance or a Podman container is a background job.**
  The standard job log window opens right away: the command, its output,
  image download percentages. Closing the window does not stop the
  creation; the job is visible in "Jobs" (with retry). Before, the window
  just spun, and a long image download could hit the command timeout.
- **A shared "run commands" job** — the base that package and engine
  installs move to next.
- **The job log through the hub** is now a live stream, not polling.

## v1.10.117 — 2026-09-25

- **Windows can be resized.** Every window has a corner at the bottom
  right: drag it to change width and height, double-click for the normal
  size. The button in the title maximizes the window. Logs, the job log,
  editors, the terminal and the machine screen stretch with the window,
  and the size of these kinds of windows is remembered in the browser.
- **The service log window** is wider and taller by default.

## v1.10.116 — 2026-09-25

- **Container and machine load.** For the network, CPU and memory charts
  you pick the source next to the metric: Docker, Podman, LXD or Libvirt.
  LXD is measured from instance state, libvirt machines from `virsh
  domstats`: CPU time, the qemu process memory on the host, network. A
  chart of sent traffic was added.
- **Machine and port availability.** Found and checked automatically:
  published Podman ports, forwarded LXD ports, running LXD instances and
  libvirt machines — ping at their address (a machine's address comes
  from the libvirt DHCP lease or ARP).
- **Your own availability targets** — the "+ target" button: ping, TCP,
  HTTP or HTTPS to any address; deleted from the list, scans never touch
  them.
- **Vulnerabilities inside guests.** The scan checks Debian/Ubuntu
  packages inside LXD instances (via lxd-agent for LXD VMs) and libvirt
  machines (via qemu-guest-agent) — locally and on the hub. A finding's
  origin is "LXD name" or "VM name"; without an agent the scan says so in
  its warnings.

## v1.10.115 — 2026-09-25

- **Container and machine console on hosts under a hub.** It used to run
  as the hub's SSH user: lxc answered "LXD unix socket … permission
  denied", and virsh did not see system machines. Now the Docker, Podman,
  LXD and libvirt console runs as root, like logs and the other actions;
  only administrators can open it, and every connection is audited. The
  host's own terminal still runs as the SSH user.
- **The libvirt console** connects to `qemu:///system` explicitly.

## v1.10.114 — 2026-09-25

- **Screen over SPICE.** For a libvirt machine with SPICE graphics only,
  the "Screen" window uses the spice-html5 client, with Ctrl+Alt+Del. A
  machine with both VNC and SPICE can switch between them.
- **"Add VNC"** for machines without VNC: the machine XML editor opens
  with VNC graphics on 127.0.0.1 already added, and writing goes through
  a diff. The new graphics work after a full shutdown and start.
- **LXD virtual machine screen** — a "screen" icon on a running LXD VM,
  also over SPICE.
- **Third-party licenses** are in `THIRD_PARTY_NOTICES.md` and
  `THIRD_PARTY_NOTICES.en.md`. spice-html5 (LGPL-3.0) ships as separate
  unmodified files with the license texts and loads only when a SPICE
  window opens.

## v1.10.113 — 2026-09-25

- **LXD port forwarding.** "+" in the ports column opens a form (tcp/udp,
  host address and port, instance port), the bin next to a port removes
  it. Writing goes through the configuration window: you see the added
  or removed `proxy` device, a diff before writing, a version in history.
  If the device comes from a profile, the window says so.
- **LXD networks** — a card below the instances: bridges with addresses,
  NAT and who uses them; creating a bridge (`auto`, `none` or your own
  CIDR), deleting a managed network.
- **Images on the host** — a list with size and fingerprint, delete and
  download in advance (`lxc image copy … local: --auto-update`) with live
  output: a new instance from such an image starts without waiting for a
  download.
- **LXD storage pools** — driver, source, size, who uses them.

## v1.10.112 — 2026-09-25

- **LXD instance configuration in a window.** The "edit" icon in the row
  opens what `lxc config edit` does: config (limits, autostart,
  cloud-init), devices, profiles. The `limits.cpu` and `limits.memory`
  fields edit the text; a "saved → draft" diff comes before writing. A
  YAML error shows before writing; if the configuration was changed
  meanwhile, the write is rejected.
- **LXD configuration version history** — the same as for files: every
  edit is kept, diff against the current one, rollback to any version.
  Next to it — the effective configuration with profiles (read-only).

## v1.10.111 — 2026-09-25

- **LXD instance backup.** The "backup" icon in an instance row opens the
  same window as for Docker and Libvirt: an archive via `lxc export`
  (root filesystem or VM disk, configuration and snapshots) with
  percentages in the job, download, restore as a copy under a new name
  (`lxc import`) or over the original — only when the instance is stopped.
- **LXD snapshots.** The "snapshots" icon (with their count) opens the
  list: take one, optionally with memory, restore (`lxc restore`) after a
  confirmation, delete.

## v1.10.110 — 2026-09-25

- **The "Virtual machines" tab is renamed to "Libvirt"**: LXD has virtual
  machines too, and the old name was confusing. The section title is
  "Libvirt — KVM virtual machines".
- **LXD gets closer to Docker and Libvirt.** An instance row shows memory
  and limits (`limits.memory`, `limits.cpu`), used disk, forwarded ports
  (`proxy` devices) with "probe port", autostart as one button
  (`boot.autostart`) and **logs** — the journal inside the instance
  (`journalctl`, syslog when absent) or LXD's own log.

## v1.10.109 — 2026-09-25

- **A virtual machine's screen in the browser (VNC).** A "screen" icon in
  the row of a running machine opens its display right in an nkt window
  (noVNC): an installer, BIOS, a desktop, a machine without network —
  everything a serial console cannot show. Ctrl+Alt+Del, a "view only"
  mode, a VNC password prompt when one is set. Works through the hub too:
  the machine's VNC port listens on the host's 127.0.0.1, and nkt forwards
  it over a WebSocket. The machine needs VNC graphics
  (`graphics type=vnc` — nkt creates machines that way) and the web
  terminal must be enabled.

## v1.10.108 — 2026-09-25

- **A console inside containers and machines.** A "console" icon in the
  row of a running object opens a live terminal in a window (the same as
  "Terminal": resize, copy, search, through the hub too):
  - **Docker / Podman** — `exec` into the container: bash if the image has
    it, otherwise sh; a user can be set (`-u`);
  - **LXD** — `lxc exec` into the instance;
  - **virtual machine** — the guest's serial console (`virsh console`),
    exit with Ctrl+]; if it stays silent, the guest needs a getty on ttyS0
    (the window shows how).
  The web terminal must be enabled (NKT_TERMINAL_ENABLED); on a host under
  the hub the console opens as the host user — it needs the docker/lxd/
  libvirt groups.

## v1.10.107 — 2026-09-25

- **snap and flatpak packages work like "Installed packages".** A filter,
  a grid with checkboxes (the package kind as a tag, channel and origin
  in a tooltip), "Remove selected (N)" and "Clear selection". Removing
  and updating snap/flatpak runs in the command window with live output
  instead of silently.

## v1.10.106 — 2026-09-25

- **LXD from snap now works from nkt.** After installing LXD, the image
  list and instances failed with "exec: "lxc": executable file not found
  in $PATH": snap puts `lxc` into `/snap/bin`, which is not in the
  service PATH, and the snap wrapper itself runs through a setuid helper
  that does not work inside the unit sandbox (NoNewPrivileges). Now
  `/snap/bin` is in the service PATH and `lxc` runs outside the sandbox
  by its full path, like `virsh`. Update nkt on the host.

## v1.10.105 — 2026-09-25

- **Backup and restore progress as a bar.** The job window (and the
  "Step" column in "Jobs") shows the percentage of the current operation
  and what is going on: copying a disk (`qemu-img`), packing volumes, the
  project directory and the archive, unpacking on restore (`tar`). Steps
  without a percentage (saving an image, `virsh define`) show a busy
  indicator with a name. Every job with steps (installing and updating
  hosts and others) got the bar too.

## v1.10.104 — 2026-09-25

- **A machine backup failed right away** ("unary operator expected",
  "Option argument is empty"): the script went as a string through
  systemd-run, and systemd substitutes `${…}` in arguments itself — the
  disk list and snapshot options arrived empty. The script now runs from
  a file. The failed attempt did not touch the machine: no snapshot had
  been created.
- **A safety net for a running machine's backup**: if copying fails after
  the snapshot, the changes are still merged back into the disks
  (`blockcommit`), so the machine is not left on temporary overlay files.

## v1.10.103 — 2026-09-25

- **Backup and restore of machines and containers.** A "backup" icon in
  the row of a libvirt machine, a Docker or Podman container opens a
  window: archives on the host, "create backup" (a background job with a
  live log), download (through the hub too), delete, restore — as a copy
  with a new name or over the original.
  - **Machine:** XML and disks (compressed qcow2). A running one is not
    stopped: disks move to an external snapshot while copying, then the
    changes are merged back (`blockcommit`); consistent with
    qemu-guest-agent. A copy gets new UUID and MACs.
  - **Container:** an image of the current state (`commit` + `save`),
    run options and named volumes. A copy gets its own volumes and no
    published ports.
  - **Compose stack** (a container from compose): the project directory,
    the project volumes and, if checked, the images; restore —
    `compose up`.

## v1.10.102 — 2026-09-25

- **LXD and Podman install from their tabs.** When the engine is missing,
  a banner with an "install" button runs in the command window: Podman
  via apt, LXD via snap; snapd is installed first if missing (Debian has
  none by default), and after LXD — `lxd init --auto` (default storage
  and bridge).
- **Images for "New LXD instance" come as a list.** Three sources:
  `images:` (the LXD image server — Debian, Alpine, Rocky, Fedora,
  Arch…), `ubuntu:` (official Ubuntu) and those already on the host; a
  "container / VM" filter, search, size. The host fetches the remote list
  itself and caches it for a day; without internet the local images and
  a hint on adding an image from a file are shown. A VM image launches
  with `--vm`.

## v1.10.101 — 2026-09-25

- **Creation happens in modal windows**: a new virtual machine, a new
  Podman container, a new LXD instance.
- **Clicking a block opens the edit window** (machine XML, compose,
  nginx, haproxy, caddy): the block text in the editor, "Save" through
  the diff, "delete" right there — instead of a card under the tree.
- **"+ new file" in "Configs"** shows only with a category selected:
  without one the window had nothing to start the path from and spun
  forever. A category with no directory for new files now says so
  instead of spinning.

## v1.10.100 — 2026-09-25

- **Every edit happens in a window, with a diff before writing and the
  history.** "Configs": the page shows the file, "Edit" opens a window
  with the editor, a note, "show changes" and a "History" tab (versions,
  diff, rollback); "Save" shows the diff first and only "Write" writes.
  The same for profiles, hub scripts, model instructions (prompts) and
  the file editor in the browser. The machine configuration window and
  the compose window on the Docker page get a "History" tab.
- **Deleting a machine is one button.** The separate "delete with disks"
  icon is gone: the confirmation window has a "delete the machine's disks
  too" checkbox, off by default.

## v1.10.99 — 2026-09-25

- **One button instead of a "start / stop" pair.** Services, Docker,
  Podman, LXD, virtual machines and the nkt service in the host list now
  have a single power button by state: running — "stop", stopped —
  "start", paused — "resume", in transition — busy. Restart, reload,
  pause and force power-off show only when they make sense (on a running
  one).
- **Autostart is one button too**: services get a single button by the
  current state instead of an "enable / disable autostart" pair
  (machines and machine networks already had it).

## v1.10.98 — 2026-09-25

- **Editing a block works again.** The block edit window (machine XML,
  docker compose, nginx, haproxy, caddy) sent the operation "edit", which
  the server did not know — "unknown operation \"edit\"". Fixed.

## v1.10.97 — 2026-09-25

- **Starting a container runs in nkt's command window, with the reason.**
  A failure used to show "HTTP 500" with no explanation (the Docker API
  answers with a code and puts the reason in the body). The reason is
  now in the message ("port is already allocated", "no such image"…),
  and start/restart run in the standard window: the `docker start`
  output, the state a few seconds later and, if the container is not
  running, the tail of its logs where the cause usually is. Same for
  Podman.
- **Container logs** — an icon in the row (Docker and Podman): live
  `docker logs` in a window, tail 200/1000/5000 lines, "follow",
  "timestamps".
- **Docker compose as blocks: not only services.** The `networks`,
  `volumes`, `secrets` and `configs` sections as entries: edit, delete,
  `+ network`/`+ volume`/`+ secret`/`+ config` with a template; a missing
  section is created together with its first entry.
- **A diff before writing a block** for every service (nginx, haproxy,
  caddy, compose, libvirt): "Save" shows "on disk → after the edit",
  "Write" applies — as in the machine XML editor.

## v1.10.96 — 2026-09-25

- **AI help with a configuration.** In "Configs" a bulb sits in the header
  of every open file (any service) and next to the selected program with
  a "what do you need?" field: the answer is "what is configured / what
  to fix / example" with a fragment for the task. The instruction is the
  third editable one in "About". Passwords, keys, tokens and password
  hashes (WireGuard, kubeconfig, htpasswd, shadow…) are cut from the
  text; with "hide addresses and names" on, so are addresses and names.
- **Block mode for a machine's XML.** The virtual machine configuration
  editor gets a "blocks" tab: domain elements (`name`, `memory`, `vcpu`,
  `os`…) and devices one by one (disk, network interface, graphics…) —
  edit, delete, `+ disk`/`+ interface`/`+ graphics` with a template;
  writes go through `virt-xml-validate` and version history. The same
  mode is available for the machine XML in "Configs".

## v1.10.95 — 2026-09-25

- **Disks → Files: `/tmp` is back, and only existing folders are
  listed.** The host unit now has `PrivateTmp=no`, so the browser shows
  the real `/tmp` rather than the service's private one (on a host with
  the old unit `/tmp` stays out of the list until the host is updated).
  Roots missing on disk (`/srv`, `/var/www`…) are no longer shown.

## v1.10.94 — 2026-09-25

- Port probe: when the response is a single-page JavaScript application
  (SvelteKit, React, Vue…), the empty "render" frame now says why it is
  empty: scripts and assets from the address are deliberately off in the
  sandbox, the port answers and the HTML arrived — see "text".

## v1.10.93 — 2026-09-25

- **Large uploads no longer fail with "i/o timeout".** A cluster image on
  the hub and a custom machine image to a host (through the hub) sat
  under the common 30-second body read and the 2-minute ceiling; image
  uploads now have their own limit — 6 hours.
- **"Add your own image" on a host through the hub** answered "Unknown
  API method: /api/vm/images/upload": the request went to the hub
  itself without the host prefix. Fixed.

## v1.10.92 — 2026-09-24

- Resource map: a condition made redundant by the early "no data" return
  above it is removed (CodeQL #158).

## v1.10.91 — 2026-09-24

- **Hosts no longer look "behind" after a hub self-update.** An open tab
  compared host versions with the hub version remembered at page load;
  after the hub updated, every host looked behind, "update all (N)"
  counted them and each "open" reinstalled the very same version (a
  "done, 6 s" job again and again). The hub version now comes with the
  host list response, and the header re-reads the hub info once it
  notices a version change.

## v1.10.90 — 2026-09-24

- **The hub key comes with ready-made commands.** The public-key window
  (when adding a host, via the "key" button in the row) shows, next to
  the key, a command to run on the host itself (creates `~/.ssh`, appends
  the key to `authorized_keys`, sets permissions) and a command from your
  machine over `ssh` with the host's address, port and user; each has a
  "copy" button.
- **"The nkt API is not up" — with a diagnosis.** When SSH answers but the
  host API does not, the hub collects over SSH the unit state, who
  listens on the port and the last journal lines of the service, and adds
  them to the error and to the "unreachable" label: it shows whether nkt
  restarts in a loop, the port is taken or the listener is just not up
  yet.

## v1.10.89 — 2026-09-24

- **An nkt restart on a host no longer fails requests.** After "upgrade
  packages" (or a self-update) the service is not listening for a few
  seconds, and pages failed with "connection refused". Now, if the host
  answered over SSH recently, the hub waits up to 20 s and retries the
  login; if the API never comes back it says so plainly: "SSH answers,
  but the nkt API is not up — 'update' from the hub reinstalls and
  restarts the service". The same hint sits on the "unreachable" label
  in the host list.
- **"Update all" no longer loses hosts.** No more than three are updated
  at once (the rest wait — visible in the job log): dozens of SSH
  connections at the same time hit sshd or jump-host limits and hosts
  ended up "unreachable" although "open" updated them fine. A transient
  SSH error (refused, reset, timeout) is retried three times with a
  pause. After a successful update the host is polled immediately — the
  "unreachable" label does not linger until the next tick.

## v1.10.88 — 2026-09-24

- Resource map: a redundant condition when handing the graph to the
  architecture review is removed (CodeQL #157).

## v1.10.87 — 2026-09-24

- Row action icons in the standard blue (like links), dangerous ones red:
  Services, Containers & VMs, Hosts and the other tables; "probe port"
  and the AI bulb without an answer (it used to be grey) too. A bulb with
  an answer is orange, with an answer from another host — filled blue.

## v1.10.86 — 2026-09-24

- **The AI bulb on any configuration edit error.** It used to appear only
  after a validation rollback; now also when the write never happened (a
  request error: path, version conflict, validator) — in the config
  editor, the new-file form, the block editor and the machine XML editor.

## v1.10.85 — 2026-09-24

- **The machine configuration editor is a modal.** The pencil in a
  machine's row opens the XML editor as a window instead of a card at the
  very bottom of the page; the pre-write diff opens on top of it.
- **The architecture review looks at what is on the map.** Nodes hidden
  by "hide stopped services" and "problems only" are left out of the
  request; the review card says how many nodes out of how many are
  reviewed.
- **AI on a failed configuration edit.** When a write fails validation or
  apply and is rolled back, the failure banner gets a bulb: the model
  receives the check output and the diff of the edit, with the task to
  explain and show a corrected fragment.
- **Secrets never reach the model**: passwords, tokens, API keys, private
  keys, `Authorization` headers and credentials in URLs are cut from the
  request regardless of the "hide addresses and names" checkbox.

## v1.10.84 — 2026-09-24

- **The AI answer stays with the finding.** A bulb with a saved answer is
  orange and opens it without a new request; under the answer — the date,
  "ask again" and "delete answer". If the same finding was already
  analysed on another host, the bulb is blue: that answer is shown first
  with a note where it came from, and a request for this host is a
  separate button.
- **Model instructions (prompts)** are editable in "About": for finding
  analysis and for the architecture review, in Russian and English.
  Saving goes through a window with a diff against the default; "restore
  default" removes the edit.
- **Diff before writing a machine configuration**: the libvirt XML editor
  shows the "on disk → draft" changes in a modal, and `virsh define` runs
  only after confirmation. The config editor gets a "show changes" button.
- **Export/import** carries the AI settings (the API key is re-encrypted
  with the new hub's master key), edited instructions and the beta update
  channel.

## v1.10.83 — 2026-09-24

- **Beta releases.** The tag `vX.Y.Z-beta` builds a beta: the release is
  marked pre-release, the binary carries version `X.Y.Z-beta`, the hub
  image is published as `:X.Y.Z-beta` and `:beta` (`:latest` is left
  alone). "About" gets a **"use beta versions"** checkbox: with it betas
  count as updates; without it a hub on a beta updates to the stable
  build of the same version once it is out. A beta build shows a "beta"
  badge in the header.
- **AI analysis no longer hangs until an error.** The cause was the UI's
  general 30 s request timeout and the hub's 90 s limit: a local model
  with a long analysis could not make it, while "Test" with its short
  question could. AI now has its own **"answer wait time"** setting
  (10–1800 s), the analysis window shows a seconds counter, and a clear
  refusal with a hint arrives when the time is up.

## v1.10.82 — 2026-09-24

- **"What's new" no longer disappears after updating.** While no next
  version is out, "About" keeps showing the installed version's notes —
  what just arrived is more useful than an empty space. Once a newer
  version appears, the block switches to its notes by itself.
- **A "Test" button in the AI settings.** A short probe request using
  whatever is in the form right now (no need to retype the key — an
  empty field means "use the stored one"): it shows the model's reply
  and how long it took. A wrong address or key shows up immediately
  instead of at the first analysis; the daily limit does not block the
  test.
- **The AI bulb where it was missing**: the "Miners and signs of
  compromise" hits (the button used to be only on ClamAV findings) and
  the "What's broken" card on the overview.

## v1.10.81 — 2026-09-24

- Security checks: the cookie-attribute rule (G124) moved into the
  config with an explanation — it looks at any `http.Cookie`, while on
  the hub these are outgoing requests to a host's API where
  `Secure`/`HttpOnly` mean nothing; the browser session cookie is
  unchanged — `HttpOnly`, `SameSite=Lax`, `Secure` per
  `NKT_COOKIE_SECURE`.
- Resource map: dropped a redundant condition when passing the host id
  to the architecture review.

## v1.10.80 — 2026-09-24

- **AI analysis of findings and architecture.** One setting for the
  whole installation — a card in "About" on the hub: provider
  **Anthropic** or **OpenAI-compatible** (local ones included — Ollama,
  vLLM, LM Studio), address, model, key. The key is stored encrypted on
  the hub and never handed out, requests go from the hub — **hosts need
  no internet**. Everything is off by default.
- A bulb button **"explain"** in the row of a finding, a vulnerability,
  a malware hit, a hub alert and next to a job error: what it means, why
  it matters here (what is nearby on the host — ports, containers,
  firewall — is added to the request) and what to do, step by step with
  commands. **The model runs nothing** — commands are shown, you apply
  them.
- **Architecture review** on the "Resource map": single points of
  failure, needless exposure, inconsistencies, where to start — over the
  whole map; on the hub the same across all hosts. Reviews are kept with
  their date.
- What leaves the machine: by default host names, IPs, domains and
  e-mail are replaced with aliases and restored in the answer; "show
  request" shows what was sent verbatim. Answers are cached (one finding
  across ten hosts is one request) and spending is capped by a daily
  limit.

## v1.10.79 — 2026-09-24

- **The host API port is configurable** — globally on the hub
  (`NKT_HUB_HOST_API_PORT`, 8077 by default) and per host (the "API
  port" field in its form, empty means the hub's). Needed when 8077 is
  taken on the host; the port is still localhost-only and never exposed.
  Changing it on an existing host restarts the install to rewrite
  `nkt.env`. The port is part of export/import.
- The host address in the list is **selectable with the mouse** again —
  dragging a row into another group swallowed the selection, so the
  address could not be copied.
- Every libvirt machine now has a **"machine configuration (XML)"**
  icon: it opens `/etc/libvirt/qemu/<name>.xml` in the config editor
  with version history and `virsh define` on save.

## v1.10.78 — 2026-09-22

- The write-path check (absolute, cleaned, no `..`) now sits in both
  write points instead of a shared helper — visible right where it
  matters, to a reader of the code and to the CI code scanner alike.

## v1.10.77 — 2026-09-22

- Go toolchain extraction on the hub now goes through `os.Root`:
  **nothing** escapes the extraction directory any more — not `..` in a
  name, not an absolute path, not a write through a symlink the archive
  itself created (the last one slipped past the previous name check,
  which was purely lexical). A test for that escape was added.
- Extraction of the trivy archive is size-capped (512 MB per file) — in
  case the source ever turns out not to be the one expected.
- `collect.WriteFile` validates the path itself: absolute, cleaned, free
  of `..`. Who may write into a given directory is still decided by the
  callers.

## v1.10.76 — 2026-09-22

- The release workflow failed while uploading assets ("read
  dist/deploy: is a directory"): the manifests with the exact image
  version now sit next to the binaries instead of a subdirectory, and
  their checksums land in `SHA256SUMS` —
  `docker-compose.hub.release.yml` and `k8s-hub.yaml`.

## v1.10.75 — 2026-09-22

- CI security checks now recognize the fixes from earlier versions
  (CodeQL could not connect the check with the action): the symlink
  target from an archive is now **returned** by the checking function
  (`safeSymlinkTarget`), the log line cap sits right at the allocation,
  and the hub cache file path is built in a single place (`cacheFile`)
  that refuses anything escaping the cache directory — used by every
  access to it (packages, files by URL, the registry mirror).
- A `Close` error when writing a file is no longer lost even on the
  error path: it is returned alongside (`errors.Join`).

## v1.10.74 — 2026-09-22

- **File upload: lost files.** A failed file is retried up to three
  times (0.5 → 2 → 5 s) — a single drop no longer loses it silently; the
  rest go with the **"retry failed"** button. Before sending a file the
  hub checks that the connection to the host is alive and replaces a
  dead one: a request with a body has no second attempt, and that is
  exactly where "host unreachable" came from. Two upload streams instead
  of three.
- **A "skip hidden" checkbox** next to "upload folder", **on by
  default**: hidden files and folders (`.git`, `.env`, `.venv/…`) and
  whatever the uploaded folder's `.gitignore` lists are not uploaded
  (nested ones included, with `!` negations, `**` and directory
  anchoring). The summary shows how many were skipped; uncheck it to
  upload everything as is.

## v1.10.73 — 2026-09-22

- Frontend dependencies: `vite` 5 → 8, `@vitejs/plugin-react` 6,
  `react-router-dom` 6 → 7 — closes the dev-server advisories (`vite`,
  `esbuild`, `launch-editor`) and two in `react-router` (open redirect
  and constructor execution during hydration; neither applied to nkt —
  navigation targets are hardcoded and SSR is not used).
- Releases now carry `docker-compose.hub.release.yml` and `hub.yaml`
  with the **exact image version** instead of `:latest` — a mutable tag
  can be repointed, and the hub volume holds the master key. In the
  repository the manifests stay on `:latest` so `docker compose pull`
  keeps working.

## v1.10.72 — 2026-09-22

- The host overview now shows **uptime** ("Up 27d 4h") under the
  summary. The color compares it with your previous visit to that page:
  **green** if the machine has not rebooted (uptime grew by exactly the
  elapsed time), **red** if it has (uptime dropped), no color on the
  first visit or a clock jump. The previous value is remembered in the
  browser per host name.
- The hub records an **alert** for it — "host rebooted: up 5 min,
  previously up 27 d" — a new `rebooted` kind with its own
  record/notify setting. No false positives: uptime only drops after a
  boot.
- `/api/overview` serves `uptime_s`; hosts with an older nkt do not know
  the field — then no uptime is shown and no alerts are recorded.

## v1.10.71 — 2026-09-22

- **"Files" on a host: empty folders and "uploads do not appear".** The
  host unit was built with `ProtectHome=yes` — the nkt service could not
  see `/home` at all: the browser showed empty folders, and a file
  written there from outside the sandbox never appeared in the listing.
  It is now `ProtectHome=read-only` (update nkt on the host and the unit
  is rewritten); until then the "Files" section warns about it and an
  upload into an invisible directory answers with a clear error instead
  of "ok".
- `/tmp` is no longer a default browser root: the unit has its own
  `/tmp` (`PrivateTmp=yes`), which is not the directory files are put
  into from outside. Add it to `NKT_FILES_ROOTS` if you need it.
- The listing refreshes **while** an upload runs (every 1.5 s), not only
  at the end, and right after `git clone`; next to "new folder" there is
  a **"refresh"** button.

## v1.10.70 — 2026-09-22

- **The SSH host key is recorded** on the first connection and required
  on every later one (like `known_hosts`): a swapped key fails with "SSH
  host key changed: known …, presented …" instead of logging in with the
  password to a possibly foreign machine. The host form shows the
  fingerprint and has "forget host key" (after a reinstall). The key is
  part of export/import. Existing hosts record their key on the first
  connection after the update.
- **The hub container runs as non-root** (uid 1000): `Dockerfile.hub`
  with `USER nkt`, the Go build cache in the data volume, a
  `HEALTHCHECK`; the Kubernetes manifest — `runAsNonRoot`, `fsGroup`,
  `readOnlyRootFilesystem`, `drop: [ALL]`, `seccomp`, resource limits. A
  data volume created by an earlier root-run version must be handed to
  uid 1000 once — the hub exits at start with the `chown` command
  (HUB.md, section 2.2). The plain nkt image stays root: it needs the
  host's /etc, systemd and Docker socket.

## v1.10.69 — 2026-09-22

- From the CodeQL/gosec/Trivy findings in the Security tab:
  - **Firewall**: the confirmation for a critical port (22 and others)
    did not work — the dialog appeared, but the rule was applied without
    waiting for the answer (a missing `await`); declining now really
    stops adding or deleting the rule.
  - **Registry mirror** in the hub cache: `?ns=` accepts only a host
    name — before, `..` in it could write a file outside the cache
    directory and request an arbitrary address.
  - Go toolchain extraction on the hub: symlink targets from the archive
    are checked against escaping the directory.
  - Logs: `?lines=` capped at 20000 (it sized a buffer), the unit name
    for `journalctl -u` is validated by characters.
  - A `Close` error when writing a file is no longer lost (trivy, config
    files).
  - `react-router-dom` 6.30.6 (CVE-2026-53668).

## v1.10.68 — 2026-09-22

- CI: the `trivy` job failed at setup — the `trivy-action@0.28.0` tag
  does not exist; now `v0.36.0`. CodeQL bumped to v4 (v3 is deprecated).

## v1.10.67 — 2026-09-22

- Security checks in CI instead of a third-party bot: govulncheck and
  gitleaks (blocking), CodeQL for Go and TypeScript, gosec and Trivy
  (into the Security tab), Dependabot for dependencies —
  `.github/workflows/security.yml`, `.github/dependabot.yml`,
  `.gitleaks.toml`, `.gosec.json`.
- Go 1.26.8 and `golang.org/x/crypto` 0.56: closes 33 known
  vulnerabilities of the standard library and the ssh package that
  govulncheck found in called code on 1.25.0 (release binaries were
  built with them).
- Fallback channel: TLS session resumption is explicitly off — the
  certificate pin is checked on every connection.

## v1.10.66 — 2026-09-22

- Security: key files (`ssh_host_*_key`, `id_*`, `*.key`, `*.pem`) can no
  longer be read through the config editor by typing a path under an
  allowed root (e.g. `/etc/ssh`) — before, they were only hidden from the
  listing. Refused for read, blocks, version history and version content
  (Aikido finding, PR #5).

## v1.10.65 — 2026-09-19

- **Installing and updating nkt on a host is a hub job.** The log is in
  the same window as every other job (live stream, in the reader's
  language), listed in "Jobs", survives a page reload and a hub restart;
  cancel — with the button in the host row. "Update all" starts a job
  per outdated host and returns right away — progress is in "Jobs" and
  in the row statuses. Starting again on a host whose install is already
  running opens its log instead of starting a second one. Installing a
  node while creating a machine or a cluster is a nested job whose log
  is copied into the parent.

## v1.10.64 — 2026-09-19

- Binary delivery to a host: the "GitHub from host vs SFTP from hub"
  probe now runs on **every** install and update — the previous result
  is not remembered, whichever is faster and available right now wins.
- The privacy mode checkbox moved to "About" (a "Privacy mode" card); on
  a standalone nkt without a hub it stays at the bottom of the menu,
  after the theme.

## v1.10.63 — 2026-09-19

- Privacy mode: a single checkbox right after the theme selector, no
  caption (the description is in the tooltip); the "nkt" brand in the
  header is a blue badge, orange in privacy mode.
- Documentation site: a new "Kubernetes clusters" page (network modes
  between hosts and what each needs, dry run, images, presets, hub cache,
  kubectl, "try again"), updated "Hub", "Alerts, jobs, updates" (retry,
  log language, privacy mode, file and registry cache) and the feature
  list — in both languages.
- TODO: the plan for connecting nodes across hosts that cannot see each
  other (WireGuard through the hub), Cilium parameters for ClusterMesh and
  "connect clusters".

## v1.10.62 — 2026-09-19

- Privacy mode now covers **modal windows** too — titles with a
  host/machine/domain name, the name, address, user and key fields of
  the host and machine forms, "find machines" credentials, install and
  renewal logs, service logs, certificate configuration snippets,
  confirmation texts, the host picker in the cluster form. **Domains**
  are blurred in free text and paths (including
  `/etc/letsencrypt/live/<domain>/…` and `<domain>.pem`), certificate
  domains always; the host name in the bar above the page and the
  "What's broken" card on the overview as well. The mode mark is the
  colored "nkt" brand in the header instead of a corner badge.

## v1.10.61 — 2026-09-19

- **Job logs, notifications and release notes in the reader's
  language.** Lines of job logs, job titles, current step and error, and
  the details of hub notifications are now stored as message keys with
  arguments, and are rendered in the language of whoever reads them —
  a job run under a Russian UI reads in English for an English reader and
  vice versa. Older lines stay as they were written; raw tool output
  (apt, kubeadm, ssh) is shown as is.
- "About" shows the release notes of the newer version in the UI
  language: releases now carry both `WHATSNEW.md` and `WHATSNEW.en.md`
  (this file), separated by `<!-- en -->` in the release body.
- Cluster preflight plan lines, HTTP codes in image errors and the
  script template comment are localized too.

## v1.10.60 — 2026-09-19

- **Privacy mode** — a "hide sensitive" checkbox in the sidebar: for
  screen sharing, addresses and names of hosts and machines, users, keys
  and tokens, cluster API addresses, certificate domains and IP/MAC
  addresses are blurred on every page; in logs, notifications,
  "Findings" and audit — IPv4/IPv6, MAC, e-mail and the host names from
  the hub's list; terminal, logs and topology — as a whole. Remembered in
  the browser, a "privacy mode" badge in the corner.

## v1.10.59 — 2026-09-19

- "New cluster on several hosts": **saved presets** — "save preset"
  remembers the whole form under a name on the hub, picking a preset from
  the list fills the form (hosts are matched by name, the form reports
  missing ones), "delete preset". Presets are part of hub export/import.

## v1.10.58 — 2026-09-19

- Cluster dry run: an inactive libvirt network is no longer a warning but
  "will be started on creation"; with "prepare as well" the network is
  brought up (or created as NAT) right away.
- If the host has the hub cache open, the hub checks links itself and
  puts the files into its cache ("in hub cache: … (N MB)") instead of
  the host checking through the tunnel — spurious "unavailable: deadline
  exceeded" on GitHub redirects are gone. With preparation the hub
  downloads the big things in advance too: the k3s binary with checksums
  and airgap images, Cilium CLI, the machine image. Checks from a host
  without the cache use a 30 s timeout.

## v1.10.57 — 2026-09-19

- Hub export/import — format 3: **Kubernetes clusters** are carried over
  (record, nodes with roles, API address, encrypted kubeconfig and
  WireGuard plan — re-encrypted with the built-in key like host
  secrets), new host fields (machine connection, binary delivery), script
  revision history, package cache limit and the list of cluster images
  (files are copied by hand — import lists the missing ones). Files of
  formats 1 and 2 still load; a cluster whose name is taken is skipped,
  not duplicated.

## v1.10.56 — 2026-09-19

- "Find machines on host": SSH credentials (user, port, password) are
  per checked machine, under its row, prefilled from the shared defaults
  above; "check access" uses that machine's port and each machine is
  added with its own credentials.

## v1.10.55 — 2026-09-19

- "Add host": the "Prepare new host" block is visible on both tabs; on
  "Manual" (hub key) it is off by default, on "Auto setup" it is on.
  Before, the block was hidden on "Manual" while the preparation still
  ran.
- Install log: the probe summary line ("GitHub from host … SFTP from
  hub …") is no longer overwritten by the upload progress line — progress
  replaces only its own previous mark.

## v1.10.54 — 2026-09-19

- The hub connects to a machine **directly** when its SSH port answers
  the hub without the host ("auto" mode; the probe result is remembered
  for 10 minutes), otherwise through the host as before. The machine form
  has a "Connection to machine" field: auto / direct / via host. This
  fixes machines on **macvtap**: the host does not see its own macvtap
  guests ("No route to host"), while the hub on the same network does.
- "Find machines on host": every machine lists all its addresses (only
  from the machine's own NICs, without loopback/link-local and the
  guest's internal bridges), with a choice when there are several; the
  **"check access"** button probes the chosen address from the host and
  from the hub and writes a one-line diagnosis (including macvtap); the
  chosen address and connection mode go into the record on add.

## v1.10.53 — 2026-09-19

- A cluster no longer creates a host group with its name — nodes are
  visible under their host and in the cluster card anyway; such old
  groups are removed when the hub starts.
- Removing a machine from the host list: all checkboxes ("delete
  machine", "disks" and the rest) are off by default — "remove" deletes
  only the hub record, the machine is touched only by an explicit
  checkbox.
- "Add host": the "Manual" tab is first and default.
- Fixed: machines found on a host (and machines on a bridge) could get
  `127.0.0.1` as their address — the guest agent lists `lo` first;
  loopback and link-local are no longer counted as machine addresses.

## v1.10.52 — 2026-09-18

- "kubectl" terminal: `KUBECONFIG` is passed to the shell explicitly via
  the environment (not only through the rc file — `sudo` resets the
  environment and a foreign bashrc could override it), the admin config
  is checked to exist on the node before launch (a clear refusal on a
  worker), and the greeting shows whether the config is readable.
  Before, kubectl could silently go to localhost:8080.

## v1.10.51 — 2026-09-18

- A **"kubectl"** button on a ready cluster (and on the Kubernetes tab of
  a control plane): opens, in a separate window, a terminal on the first
  control plane with `kubectl` ready — `KUBECONFIG` pointing at the
  admin config, completion, the `k` alias. It is the ordinary host
  terminal in `?kubectl=1` mode; no external API access needed.

## v1.10.50 — 2026-09-18

- Both cluster creation dialogs have a **"Kubernetes version"** field:
  the current stable (default) and the three previous branches; for
  kubeadm this is the `pkgs.k8s.io` branch, for k3s the channel
  (`v1.36`), one for all nodes. The hint explains that the API port
  inside the node is always 6443 and the port in "publish outside"
  belongs to the host.

## v1.10.49 — 2026-09-18

- Clusters: before installing a role the node is updated to the hub's
  nkt version — the installation steps are run by nkt on the node
  itself, and a machine created by an older hub (or a lagging bare-metal
  host) would otherwise run old scripts (visible in the log as "keeping
  the existing config.toml" and "already exists" on retry).

## v1.10.48 — 2026-09-18

- **"Try again"** in the log window of a failed job (hub and host) and
  **"resume"** on a cluster in the "error" state: a new job is started
  with the same parameters and the saved state — created machines, the
  tunnel and installed roles are skipped, the failed step is redone; the
  old log stays. Resumable: cluster creation, scripts (answers to
  questions are kept for an hour after a failure), Kubernetes install on
  a host (done steps are skipped), machine creation, profile
  application. If the cluster record is already gone (preflight failure)
  the job says so.

## v1.10.47 — 2026-09-18

- Fixed: kubeadm init failed at the last phase (`addon/coredns`, API
  timeout) — the containerd config from the Debian package was kept as
  is, without `SystemdCgroup = true`, and kubelet (systemd driver)
  disagreed with containerd (cgroupfs). Now a custom `config.toml` is
  kept only if it already has runc settings with `SystemdCgroup`; the
  distribution stub and Docker's config are replaced by the default
  config (copy in `config.toml.nkt-bak`), `SystemdCgroup` is always
  enabled, and the `pause` image is the one kubeadm of this version
  expects.
- `kubeadm init` takes the version from kubeadm itself instead of
  fetching `stable-1.txt` from the internet (the node may not have it);
  on retry the leftovers of a failed init/join are removed with `kubeadm
  reset`, a live node is not touched.

## v1.10.46 — 2026-09-18

- "Clusters" section: a **"Cluster images"** card — your own
  qcow2/img/raw is uploaded to the hub once (up to 10 GB) and chosen in
  the placement as "from hub: …" (`hub:<file>`; in scripts — `image
  hub:<file>`). On creation the hub uploads the image to every cluster
  host that lacks it, with progress in the log; the dry run shows whether
  the image is in the library and on the hosts. The same list is
  available in the host's "new cluster" dialog.

## v1.10.45 — 2026-09-18

- Fixed: kubeadm on Debian 13 failed with "The repository
  'https://pkgs.k8s.io/core:/stable:/v1.31/deb InRelease' is not
  signed" — apt there verifies signatures with `sqv`, and the old v1.31
  branch is signed in a way `sqv` rejects ("Signature Packet v3 is not
  considered secure"). The Kubernetes version is no longer hardcoded: on
  creation the hub learns the current stable from
  `dl.k8s.io/release/stable.txt` (1.37 now; 1.34 without access), stores
  it in the cluster and installs the same one on all nodes, including
  workers added later. In scripts — `version 1.34`, in the API —
  `k8s_version`.

## v1.10.44 — 2026-09-18

- Host without internet access: everything clusters and machines need
  now goes through the hub cache (the same `127.0.0.1:3142` port as apt)
  and **stays on the hub**:
  - **files by URL** (`/nkt/artifact?url=…`) — the k3s installer and
    binary with airgap images (a k3s node needs no internet at all),
    Cilium CLI, the `pkgs.k8s.io` key, the Flannel manifest, cloud
    machine images; versioned URLs are kept forever, the rest is
    refreshed every 6 hours, and with the source down the copy is
    served;
  - **registry mirror** (`/v2/…`, pull-through cache) for docker.io,
    quay.io, registry.k8s.io, ghcr.io, gcr.io — containerd on k3s and
    kubeadm nodes is configured for it automatically; layers and
    manifests are kept forever, a node without internet pulls the images
    the hub has already seen.
  A host with internet uses the cache too: a file or image is downloaded
  from GitHub/registry once to the hub, not once per node. With the port
  closed ("apt via hub" unchecked or the hub not connected) everything
  goes direct as before; a foreign proxy on 3142 is not mistaken for
  ours.
- The cluster dry run checks links from the host through the hub cache
  when it is there and says downloads will go through the hub.

## v1.10.43 — 2026-09-18

- Kubernetes install on a host: when a step fails, the log and the error
  text carry the real cause from stderr (`E: …` of apt/dpkg) instead of
  the last stdout line like "Processing triggers for libc-bin".
- Every `apt-get` call in the steps waits up to 10 minutes for the dpkg
  lock (`DPkg::Lock::Timeout`): on a fresh machine it is held by
  `apt-daily`/`unattended-upgrades` and the install failed with exit
  100.

## v1.10.42 — 2026-09-18

- Fixed: a kubeadm node failed at the preparation step with `containerd:
  not found` — containerd was configured before it was installed. It is
  now a separate step after the packages, taking into account what the
  machine already has: on a VM from a cloud image the distribution
  package is installed; on a bare-metal host an existing containerd
  (including Docker's `containerd.io`) is not reinstalled — CRI is
  enabled (Docker disables it in config.toml) along with
  `SystemdCgroup`, the previous config is saved as
  `config.toml.nkt-bak`, Docker containers are not stopped by the
  restart. `/etc/default/kubelet` is written after the package install.

## v1.10.41 — 2026-09-18

- Fixed: real cluster creation always failed the "cluster already
  exists" check — it found the record of the cluster being created. The
  own record no longer counts as a taken name.
- If the checks fail before the first machine, the cluster record and the
  empty host group are removed automatically — no empty "cluster" is
  left in the list and a rerun does not trip over the name. The job
  error lists the failed items ("checks failed: 2 — no /dev/kvm; port 80
  in use") instead of just "fix first".
- Job log: checklist lines are colored — ✓ green, ✗ red, ! yellow.

## v1.10.40 — 2026-09-18

- Installing and updating nkt on a host: the binary is now delivered the
  fastest way. On the first delivery the hub measures both — GitHub
  Releases from the host itself (`curl`) and SFTP from the hub — and
  logs "GitHub from host X MB/s, SFTP from hub Y MB/s — …"; the choice
  is remembered per host and the probe is not repeated. From GitHub the
  host takes exactly the file in the hub cache (the `SHA256SUMS` sum is
  verified on the host); if the download fails, the binary is uploaded
  over SFTP in the same job and the choice is reset. Progress "Host
  downloads the binary from GitHub… N%" is shown in the window.

## v1.10.39 — 2026-09-18

- Installing and updating nkt on a host: the binary is uploaded over
  SFTP with concurrent packets instead of one at a time waiting for each
  reply — on a distant host that was a minute of "Uploading binary… N%"
  on an idle link. The line is now "Uploading binary to host over
  SSH…" so it is not confused with the download.
- Downloading a release binary from GitHub Releases on the hub shows
  "N% (X of Y MB)" progress instead of staying silent.

## v1.10.38 — 2026-09-18

- Cluster dry run (in "Clusters" and in the host dialog): the log opens
  over the form, not instead of it — after the checks the settings are
  still there, ready to adjust and create.

## v1.10.37 — 2026-09-18

- The "New cluster on several hosts" dialog reworked: the placement row
  fits in two lines without horizontal scrolling; the image is a list
  from the host catalogue (marked "will be downloaded"), the bridge is a
  list of the host's bridges (or a note that there are none); the "What"
  column became "Node" (virtual machines / the host itself) with
  explanations; hints for the flavor and every network mode; the form
  itself reports a wrong number of control planes, three control planes
  with kubeadm and a missing bridge.
- WireGuard: "host address for peers" per host (when the hub reaches the
  host by an address the peers cannot see); the dry run rejects
  `127.0.0.1`/`localhost` and the same address on two hosts; after the
  tunnel is up, if a peer does not answer, the handshake tells "UDP 51820
  closed or wrong address" from a plain refusal. In scripts — `endpoint
  ADDRESS` in the placement host line.
- Removed the double dash for not-yet-downloaded images in the cluster
  dialogs.

## v1.10.36 — 2026-09-18

- Scripts: `k8s create` can place across several hosts like the table in
  "Clusters" — `nodes "hv1: cp 1, w 2; hv2: w 2; hv3: host w"` (`cp`/`w
  N` — machines with a role, `host cp|w` — the host itself as a node,
  `bridge BRIDGE` per host or shared), `network nat|bridge|wireguard` —
  the network between hosts. Help and example updated; a script dry run
  now fails when the cluster checks do not pass.

## v1.10.35 — 2026-09-18

- "Clusters": **WireGuard** as the network between hosts — hosts need
  not share a segment. The hub generates keys (stored encrypted),
  installs `wireguard-tools` on the hosts, brings up `wg-quick@nktwg<N>`
  and creates a separate libvirt network for the cluster machines on
  every host; peer subnets are routed through the tunnel, nodes see each
  other by their real addresses (no libvirt masquerade), a host node
  gets `--node-ip` inside the tunnel. Before creating machines the hub
  checks the hosts answer each other through the tunnel; deleting the
  cluster removes the tunnel and the network.
- Dry run in WireGuard mode: the host address as endpoint,
  `wireguard-tools` present (with preparation — downloaded into the
  cache), ping between hosts.
- Host API: `GET/POST/DELETE /api/vm/wgmesh[/{name}]`, `GET
  /api/net/ping?ip=`; `/api/vm/preflight` has a `wireguard` field.

## v1.10.34 — 2026-09-18

- The script test with an unreachable host no longer depends on how fast
  SSH refuses (time margin).

## v1.10.33 — 2026-09-18

- "Clusters" section on the hub: all clusters and creating a cluster on
  several hosts — a placement table (host → role → N machines or the
  host itself → sizes, image, network/bridge), network between hosts
  (NAT for one host, bridge for several; WireGuard in the next version),
  Cilium, publishing from the first control plane's host, "Dry run" with
  a per-host checklist (host node — Kubernetes not yet installed, bridge
  present) and preparation. A host node stays in the list when the
  cluster is deleted.
