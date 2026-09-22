# What's new in NetKnownsThat

Short notes per version in English — what the user sees, not the list of
commits (that is [CHANGELOG.md](CHANGELOG.md)). The Russian original is
[WHATSNEW.md](WHATSNEW.md); both files get a section with every bump of
`VERSION`, headed `## vX.Y.Z` exactly like the release tag. On a release
`.github/workflows/release.yml` appends the English sections to the
release body after a `<!-- en -->` marker, and the hub shows the reader
their language in "About" when a newer version appears. Newest first.

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
