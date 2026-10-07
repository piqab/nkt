# What's new in NetKnownsThat

Short notes per version in English — what the user sees, not the list of
commits (that is [CHANGELOG.md](CHANGELOG.md)). The Russian original is
[WHATSNEW.md](WHATSNEW.md); both files get a section with every bump of
`VERSION`, headed `## vX.Y.Z` exactly like the release tag. On a release
`.github/workflows/release.yml` appends the English sections to the
release body after a `<!-- en -->` marker, and the hub shows the reader
their language in "About" when a newer version appears. Newest first.

## v1.11.167 — 2026-10-07

- CI: the mobile app build (Android and iOS) runs only when the app itself
  (`mobile/`) changes, not on every commit — it used to be triggered by the
  `VERSION` file, i.e. by any push, together with the costly macOS build.
  The signed APK for a release is still built by `release.yml`.

## v1.11.166 — 2026-10-07

- The "Interface has been updated — reload the page" bar shows on top of an
  open window too (the update log window used to hide it).
- Verified in a browser: after a hub update the page reloads by itself —
  with the log window open or closed; if the hub has not answered with the
  new version within 3 minutes, the page says so.
- Dependencies: `source-map-js` 1.2.2 in the web interface and the docs
  site (a denial-of-service issue in source map parsing, build tooling
  only).
- Docs: fixed the site build (the Android beta release name).

## v1.11.165 — 2026-10-06

- **After a hub update the page reloads by itself again.** The update log
  window in About asked for the job from the hub itself rather than from
  the hub's machine, where jobs live — and saw neither the lines nor the
  end of the job, so the page never started waiting for the restart (the
  hub did update). Now the window looks where the job is, and waiting for
  the restart relies on the job itself, even with the window closed. If
  the hub has not answered with the new version within 3 minutes, the page
  says so and tells where to look for the install log.

## v1.11.164 — 2026-10-06

- **Uploading from the computer for longer than two minutes no longer
  "breaks off".** The file arrived whole, but the host's reply could not
  be sent anymore: the server has a common two-minute write limit from the
  start of the request, and the upload only extended the read limit. Now
  both are extended — for image archives, your own machine images and
  uploads as jobs. If the connection still breaks after the transfer, the
  interface takes the result from the job instead of saying "failed".
- **"The interface has been updated — reload the page".** A tab opened
  before the hub or a host was updated runs the old interface code (and,
  for example, uploaded files without a job); now it notices the new build
  on the server by itself and shows a bar with a button.
- If nkt on the host is older than the hub and cannot upload as a job, the
  upload window says so and suggests updating the host.
- The upload job's bar ends the transfer at 100%, not 99%.

## v1.11.163 — 2026-10-06

- **More long operations run as jobs** (with a log and in the background
  operations indicator): extracting an archive in the file browser,
  generating locales, deleting a user together with the home directory; on
  the hub — applying a manifest to several clusters (one step per cluster,
  the `kubectl` output in the log) and "Start all" / "Stop all" for nkt on
  hosts.
- Read-only operations that go over hosts and clusters one by one
  (comparing a manifest across clusters, scanning a pipeline repository,
  finding and checking machines to import, the host's sudo details and
  changes) wait up to two minutes instead of being cut off by the browser
  after 30 seconds.
- Closing the tab while a cluster image or a file browser batch is
  uploading now asks first.

## v1.11.162 — 2026-10-06

- **The hub update no longer hangs at 50%.** Downloading the release from
  GitHub had no time limit: a stalled connection held the job forever.
  Now, if not a byte arrives for 60 seconds, the download is retried once,
  and then the job ends with "the download stopped at N%"; the whole
  update is limited to 30 minutes. Downloading the release when installing
  nkt on hosts gets the same limit.
- **The hub update log shows steps:** the checksum file, downloading the
  binary with percentages (one already downloaded by an earlier attempt is
  taken from the cache if its checksum matches), the systemd unit, install
  and restart.
- The app's beta release is called "Android <version> (beta)"; running the
  workflow again renames an already published release too.

## v1.11.161 — 2026-10-06

- **Background operations indicator** — an "N operations" button in the
  bottom right corner of any section while something is running: on a host
  — its jobs, on the hub — the jobs of the hub and of every online host,
  with the host name, step and percentage; a click opens the log. A long
  operation no longer "disappears" when you leave the section or go to the
  hub.
- **Uploading from the computer is a job from the first byte** (Docker,
  Podman and LXD image archives and your own machine image): the job is
  created before the transfer starts, its log opens at once, and the host
  writes the percentage of the bytes that have arrived — the progress is
  visible in the host's "Jobs" and in the indicator. After the transfer the
  same job runs `docker load` (with the checkbox) or moves the machine image
  to the libvirt directory. Moving between sections and to the hub does not
  interrupt the transfer; a broken-off transfer (closed tab, lost
  connection) is a job error with the percentage where it stopped, and no
  partial file is kept.

## v1.11.160 — 2026-10-06

- **Long operations that still waited for a request reply now run as
  jobs** with the log window; closing it interrupts nothing:
  - removing Kubernetes from a node — the request used to break off after
    30 seconds, leaving `kubeadm reset` / `k3s-uninstall` half done;
  - stopping and restarting an LXD instance, taking a snapshot and
    restoring it — a machine shuts down longer than the command limit;
  - "Save" for Docker images — `docker save` one step per image, the
    archives show up in the "Image archives" card;
  - updating and removing snap/flatpak packages;
  - updating and rolling back the hub itself — the browser no longer
    breaks off the release download; the page waits until the hub answers
    with the new version;
  - "Update all" on hosts — one hub job instead of a loop in the browser:
    leaving the page no longer leaves some hosts without the update;
  - deleting a host with cleanup on it.
- **AI:** a model answer that takes longer than two minutes is no longer
  cut off — requests to the model live as long as the settings allow (up
  to 30 minutes).
- An LXD instance name is checked against LXD's rules in its actions too.

## v1.11.159 — 2026-10-06

- **Docker and Podman images by link.** In the "Image archives" card the
  "Add image…" button opens a window: by link (http/https) the host itself
  downloads the `docker save` archive as a background job — the window can
  be closed, and after a dropped connection or an nkt restart the download
  resumes where it stopped; an optional sha256 checksum is verified. Or
  as a file from the computer, as before.
- **"Load into Docker/Podman right away" checkbox** (on by default): `load`
  runs as a job right after the download or transfer. For a file from the
  computer the server starts it as soon as the file has arrived — nothing
  depends on the window after that; while the file is being sent, the
  browser asks before the tab is closed.

## v1.11.158 — 2026-10-05

- **The mobile app is rewritten in Kotlin Multiplatform** (`mobile/`):
  one codebase for Android and iOS, Clean Architecture, Koin, navigation
  with breadcrumbs (`Hosts › web-1 › Containers › acme-app`, each link
  goes back to its level), details as separate pages. iOS is only built in
  CI for now and is not published to the App Store.
- **Terminal:** typing on the phone keyboard no longer doubles letters,
  Backspace works on an empty line too; the keyboard no longer covers the
  screen; the “tmux ▾” button is a reliable menu of tmux actions (windows,
  splits, scrolling, detach); after a dropped connection the app
  reconnects to the same tmux session by itself.
- Sign-in: a wrong password shows the hub's answer instead of “the
  session has ended”; the form scrolls above the keyboard.
- Empty lists from the server (`null`) no longer break screens (e.g. “Hub
  jobs” on a fresh hub); sizes and times in configurations are readable.
- The old `android/` app is removed.

## v1.11.157 — 2026-10-05

- **The GitHub APK build tolerates line breaks in the key:** spaces and line
  breaks in the `ANDROID_KEYSTORE_BASE64` secret (left by copying from a
  terminal) no longer break the build; a key that does not open with the
  password is reported right away and clearly.
- The signing key alias is the repository variable `ANDROID_KEY_ALIAS`
  (`nkt` by default), not a secret: as a secret, GitHub masked the word
  “nkt” across the whole build log. A separate key password is needed only
  when it differs from the keystore password.

## v1.11.156 — 2026-10-05

- **A beta release of the Android app apart from nkt:** a manual
  “android-beta-release” run in Actions builds a signed APK and publishes it
  as a pre-release `android-v<version>-beta`; the app shows its version as
  `<version>-beta`. Hubs do not offer such a release as an update.
- With an nkt beta tag (`vX.Y.Z-beta`) the app in the release is marked
  `-beta` too.

## v1.11.155 — 2026-10-05

- **`nkt passwd -random` prints the password only to a terminal.** With
  redirected output (a file, CI, a journal, `docker compose exec -T`) the
  command refuses before changing the password — the generated password
  does not end up in logs in clear text.

## v1.11.154 — 2026-10-05

- Security check (gosec): the command-line message catalogs are marked as
  texts, not credentials — like the other catalogs.

## v1.11.153 — 2026-10-05

- **English without Russian leftovers.** The English interface no longer
  shows Russian text:
  - finding captions on the Map;
  - the “same certificate as…” note in Certificates and Problems;
  - snapshot-mode notes in Overview;
  - tool explanations in Virtual machines;
  - a host's install error reason on the hub;
  - the host details sent to the model (AI);
  - hints in the `.env` templates of the deployment examples.
- **The service journal is in English** (`journalctl`), error texts
  included.
- **A bilingual command line:** help, `scan`, `users`/`passwd`,
  `hub delete`/`hub import` — Russian with `LANG=ru*`, English otherwise.
- A test walks every GET route of the host and the hub in English and
  fails on any untranslated string.
- Fixed: the kubeconfig request on the fixtures stand failed with a
  server error.

## v1.11.152 — 2026-10-05

- **Android app in English:** the whole interface is translated; the
  language follows the phone or the choice (“Русский / English”) on the
  sign-in screen and in About, and switches instantly. The server answers
  the app in the chosen language — errors, job logs, alerts.

## v1.11.151 — 2026-10-05

- **Android app — host management:** “+” in the host list adds a host
  (password, own key or a hub key — then the `authorized_keys` line is
  shown) and optionally installs nkt right away; a row's “⋮” menu has
  install, update to the hub's version or reinstall of nkt as a hub job
  with a live log, the install log, moving to a group, and deleting (from
  the hub only or cleaning the host). The list is grouped and an outdated
  nkt version is visible at once; groups are created, renamed and deleted
  from the app.

## v1.11.150 — 2026-10-05

- **Android app in releases:** every release carries a signed
  `nkt-android-<version>.apk` (when the repository secrets hold a signing
  key); the app version now matches the nkt version and is shown in the
  app's About screen.
- **Docs:** a new “Android app” page — installation, sign-in, sections,
  notifications, building and signing; the app is added to Features.
- The app's About screen has “Forget certificate” for a hub reinstalled
  with a new self-signed certificate.

## v1.11.149 — 2026-10-05

- **Android app — on par with the web UI:**
  - findings have jump buttons: to the certificate, container, service,
    Firewall, Fail2ban, the map; “Open file” opens the config right away;
  - a Docker container has “Inspect”: variables with their origin
    (container, over the image, from the image), values only for an admin
    via “Show values” (audited), image, command, ports, volumes, networks;
    when Docker is not installed, the screen says so;
  - Usage is reworked: source (Docker, Podman, LXD, Libvirt, Kubernetes
    pods and nodes, the host itself), CPU first, node and namespace
    filters, up to 8 picked subjects on the chart;
  - Availability has a filter by target source, Kubernetes targets apart;
  - on the map, listeners of one service beyond 4 fold into one node with
    a list of ports;
  - a new “Malware (ClamAV)” section: install, signature update, host and
    image scans — as jobs with a live log;
  - the host list shows the group, Kubernetes role and sudo state
    (passwordless in red);
  - server messages always come in Russian, matching the app's UI.

## v1.11.148 — 2026-10-05

- **Android app — hub sections:** the host list's menu button opens
  Alerts, Hub jobs, Monitoring, Fail2ban and Deployments. Each event has a
  “To host” button that opens the host right at the relevant section. A
  bell shows the unread count.
- **Phone notifications:** a switch in Alerts; every 15 minutes the app
  checks the hub journal and shows events of the kinds the hub notifies
  about (same as Slack/Telegram). Tapping one opens the host.
- **Jobs:** a list with a status filter, a live log with steps and a
  “Cancel job” button — for a host and for the hub.
- **Fail2ban:** on a host — jails, bans, “Ban IP” and “Unban”; on the
  hub — every banned address in the fleet, ban and unban on all hosts as a
  job with a log.
- **Hub monitoring:** what needs attention (disks, leaks, availability
  drops) with a jump to the host, CPU/memory/disks per host, Kubernetes
  cluster nodes.
- **Deployments:** pipelines with their last deployment, “Deploy”, “Dry
  run”, log, history with rollback, old stacks left on a previous host —
  “Remove” or “Forget”.
- **Host reboot:** in the host's “⋮” menu — first shows what is running
  and what will not come back by itself.

## v1.11.147 — 2026-10-05

- **Android app:** API responses in the tests are recaptured from the
  current server — the app's models are compatible with this nkt version.

## v1.11.146 — 2026-10-05

- **Site:** the home page has 21 tiles instead of 11 — each with a
  “Learn more” link to its docs page: the resource map, containers,
  virtual machines, Kubernetes, availability and load, system, alerts,
  hub monitoring, clusters, deployments and more.

## v1.11.145 — 2026-10-05

- **Docs:** “Hub monitoring” has its own page in the site menu; the hub
  page lists the sections in hub menu order, including Monitoring,
  fail2ban and Jobs.

## v1.11.144 — 2026-10-05

- **Docs: screenshots refreshed.** All sections are reshot (ru and en),
  with new shots of container Inspect, image archives, ClamAV and the host
  reboot window; the screenshot script also covers the deployment windows
  (description, examples, deletion by plan).

## v1.11.143 — 2026-10-04

- **Docker container “Inspect”** — an icon in the container row: all
  environment variables with their source (container, over image, image),
  the image and whether it is current, state, restarts, command, limits,
  ports, volumes, networks, labels and the full JSON. Variable values are
  hidden; an administrator reveals them after a confirmation, and the
  reveal is recorded in the audit log.

## v1.11.142 — 2026-10-04

- **Deployments and `.env`.** If `.env` changed since the last deployment,
  containers are force-recreated, so new values arrive with podman as well
  as docker. The dry run names `.env` variables that reach no container
  (compose uses `.env` only to substitute `${NAME}`). After saving `.env`
  in “Access”, a hint says it reaches the hosts with the next deployment.

## v1.11.141 — 2026-10-04

- **Deployments: port conflicts inside a stack.** Two publications of the
  same host port on overlapping addresses (`0.0.0.0:8080` and
  `127.0.0.1:8080`) are now a dry run problem, and the deployment refuses
  before touching any host — before, it surfaced as “port is already
  allocated” on `compose up`.
- “Deployments” shows an “experimental feature” banner, like “Clusters”.

## v1.11.140 — 2026-10-04

- **Pipeline deletion by plan.** The window shows where this pipeline's
  stack is deployed and what happens to it: “removed”, “kept — the same
  stack belongs to another pipeline”, “host unreachable”. A stack shared
  with another pipeline is no longer wiped; hosts the pipeline never
  deployed to are not touched; hosts are picked with ticks.
- **“Only remove from selected”** removes the stack from the ticked hosts
  while the pipeline stays and runs on the rest (the hosts leave its
  description).

## v1.11.139 — 2026-10-04

- **Deployments: changing the host and the stack name.** The Hosts and
  Stack fields in the pipeline editor edit the description right away —
  Save no longer keeps the old host. The hub remembers where the stack is
  deployed: a new host is deployed without the previous one, the old stack
  is removed there after success, and if the previous host is unreachable
  the pipeline shows “stack left” with Remove and Forget. A new stack name
  on the same host is a replacement: the old one stops while the new one
  starts and is restarted on failure. Data is not carried over — the dry
  run warns about it.
- Dry run: a container name taken by another project is a problem before
  deploying; a site moving while DNS points to the old host gets a clear
  message.
- Host names in the description are case-insensitive; an unknown one
  comes with suggestions.

## v1.11.138 — 2026-10-04

- **Narrow sudo:** when the hub's key is gone from a host, the hub no
  longer advises full passwordless sudo — it says the key is missing and
  gives a command that puts only the hub's public key there (once, with
  your own password). If an operation does not support narrow sudo, the
  hub names it.
- **“Containers & VMs”:** the “install” banner no longer lingers for
  installed Docker, Podman, LXD and libvirt — the host checks this right
  away instead of relying on the last scan.
- **Hub “Monitoring”:** a long disk name no longer overflows the window —
  it is truncated, the full name is in the tooltip.

## v1.11.137 — 2026-10-04

- **“Load” and “Availability” no longer stall the host.** Charts, the
  ranking and the schedule read hourly summaries instead of millions of
  per-minute samples (tens of times faster on a host with Kubernetes);
  long reads use a separate pool and do not hold the database, so the
  host stops dropping off the hub meanwhile. Per-minute samples are kept
  for three days, summaries for at least 90 days; the first start after
  the update builds the summaries from history once.
- **Kubernetes availability.** A control plane probes Ingresses,
  NodePort and LoadBalancer services and cluster nodes — the section used
  to be empty on a host running only a cluster.
- **Kubernetes nodes.** CPU and memory of every node, workers included;
  “Load” gets a “Kubernetes · nodes” source, node and namespace filters
  and an object picker instead of an anonymous “Other”.
- **Hub “Monitoring”:** hosts look like the host list (groups, bars,
  “k8s · control plane / worker” and cluster labels), nodes without a hub
  host are shown separately; pods carry their node, with cluster, node
  and namespace filters.
- **Alerts:** the kind filter lists every kind, “forecast” included.
- Target availability “over 24 hours” took in up to a day too much — fixed.

## v1.11.136 — 2026-10-04

- Security: outside the sandbox and without systemd, only programs from a
  closed list are started; background actions (reboot, stopping ClamAV)
  take their context from the request or job instead of an empty one.

## v1.11.135 — 2026-10-04

- **Audit log**: the kind filter now lists every kind present in the
  host's log (`clamav.*`, `fail2ban.*`, `system.*`, `image.*`…) instead of
  the old nine — the list comes from the entries themselves.

## v1.11.134 — 2026-10-04

- **Links to where a problem is fixed.** An “undeclared listener” has
  “Port N in Firewall” (the port's rules and socket highlighted) and “On
  the resource map”. Vulnerabilities have “To package” and “To
  containers”; malware hits have “Open in files” (the file's row in the
  browser), “Configs” and the container; the target row or card is
  highlighted.
- **ClamAV runs as jobs.** Install, database update and scans are host
  jobs in the standard log window; “Cancel” now really stops clamscan.
  Any job can be cancelled right from its log window.
- **Hub alerts lead to the host**: a button opens the right section of
  the host with the item highlighted — new findings, banned addresses, a
  disk, a container, an availability target.
- **Resource map**: dragging no longer selects text or stutters at high
  zoom; ports of one service above a threshold (set above the map, 4 by
  default) fold into one node whose window lists the ports, each linking
  to its own section.
- **Load**: CPU opens by default, then memory; the source is the first
  one with data; a new “Whole host” source.
- **Logs**: the unit list shows installed services only.

## v1.11.133 — 2026-10-04

- **Installing Docker and libvirt/KVM** with a bar and a button right in
  the "Containers and VMs" tabs, like Podman and LXD.
- **Image archives** for Docker, Podman and LXD: save (export) an image to
  an archive on the host, download it to your computer, upload an archive
  from your computer (with progress, via the hub too), load it into
  Docker/Podman or import it into LXD, as background jobs. Machine images
  can be downloaded to your computer.

## v1.11.132 — 2026-10-04

- **Host reboot**, always in "System settings" and in the "Reboot
  required" bar on "Overview" and in "Packages". The window shows how much
  is running now (containers, LXD, machines, services) and lists what will
  not come back by itself after the reboot; the reboot runs only after a
  confirmation checkbox.
- **The "Hosts without nkt" bar can be closed**; it comes back only if a
  new such host appears.
- **"Create group"** in the host toolbar now comes after "Update all".

## v1.11.131 — 2026-10-04

- **"Monitoring" docs** with screenshots of both tabs.

## v1.11.130 — 2026-10-04

- **"Monitoring" history in the hub export**, behind a separate checkbox
  (the file gets noticeably larger). The import plan has its own section:
  hosts by name; history is added where there is none, and where there is
  some, it is skipped or merged (missing hours and days are added,
  existing ones are left alone).

## v1.11.129 — 2026-10-04

- **"Monitoring" on the hub**: a section below "Alerts" with
  "Availability" and "Load" tabs across all hosts, from the hub history
  (summaries from hosts once an hour, hours for 90 days, days for a
  year); each container, machine and pod separately, charts on click,
  heatmaps by hour of week.
- **Forecasts and hints**: when a disk fills up, memory and CPU at their
  limit and their growth, a likely memory leak, a target availability
  drop, latency growth, what to move off an overloaded host, a quiet
  window for maintenance; a button to the host section where it is
  handled, and model analysis.
- **"Forecast" alerts** with configurable thresholds (a window with a
  diff); they also go to Telegram, Slack and webhooks.

## v1.11.128 — 2026-10-04

- **The host collects its own load series**: CPU, memory, load and the
  usage of each file system, once a minute along with the other metrics,
  and serves hourly summaries (host, disks, each container and machine,
  availability targets) via `/monitor/summary`. This is the base of the
  upcoming hub "Monitoring" section with forecasts.

## v1.11.127 — 2026-10-04

- **"Installing nkt on hosts" in "About"**, next to the "Danger zone":
  pick hosts (checkboxes cleared, "Select all / Clear all", each host's
  state) and a regular installation runs on each as a hub job, three hosts
  at a time; a host with another hub's nkt is skipped.
- **After an import, the hub checks the hosts by itself**: where nkt is
  missing, the host is "not installed" (gray, with installation), and a
  "Hosts without nkt: N → Install" bar appears above the list.
- **Moving narrow-sudo hosts.** Importing a full export carries over the
  previous hub's signing key; hosts narrowed by it keep working with the
  new hub, and on an nkt install or update the hub replaces their key
  with its own (the new `hub-sudo rekey` operation signed with the
  previous key).

## v1.11.126 — 2026-10-04

- **An unreachable host: check now.** A click on the red (or gray "not
  polled") icon in the hub's host list polls the host right away rather
  than in a minute; the icon spins while polling.
- **Messages switch language with the interface.** Blue and red messages
  after actions (update started, service restart, firewall rule and
  others) used to stay in the language they appeared in; now they are
  translated when shown. Texts that come from the server (errors) stay as
  they came.
- **Resource map: node details in a window.** A click on a node opens a
  window with its kind, state, findings, all parameters in full and a
  button to the service; the panel next to the title is gone.

## v1.11.125 — 2026-10-03

- **nkt repairs its outdated fail2ban filter by itself.** The `nkt-manual`
  jail filter from nkt 1.11.x (without `<HOST>`) passed the configuration
  check but broke the whole fail2ban reload, leaving the server without
  jails, `sshd` included ("jail 'sshd' does not exist"). Now the host
  replaces such a filter when nkt starts and every half hour and reloads
  fail2ban; the audit log records it.

## v1.11.124 — 2026-10-03

- **The fail2ban configuration check writes only jail and filter files**
  into its temporary copy: a path outside `jail.d/` and `filter.d/` or
  with `..` is rejected (per the CodeQL scanner).
- **The reason a jail did not start** no longer includes the multi-kilobyte
  fail2ban command lines: the start and the error itself remain.
- **The site docs** are brought up to date: the sudo mark from a live
  check and the "hub's sudo" window, how the hub address is passed for
  fail2ban, and the hub fail2ban screenshot with "To hosts".

## v1.11.123 — 2026-10-03

- **fail2ban templates to hosts are safer.** “Check” is a hub job: the
  host checks its whole fail2ban configuration together with the
  template, so a configuration that was broken before the template (a
  jail without a filter) shows up right away and such a host is left
  out. Before the jail, the hub's ban protection is set (the hub address
  as the host sees it); after the reload the host checks that all jails
  are running and rolls the files back otherwise; a banned hub is
  unbanned right away. All hub fail2ban jobs run in one queue. A host
  with an old nkt is skipped with a request to update.
- **The hub sends its address to a host right away** if the host has no
  protection (for example, fail2ban was just installed), not after six
  hours.
- **Jails that fail2ban did not start** are shown separately, with the
  reason from the log; previously they were listed as “disabled”, and
  “enable” changed nothing. A new finding: the fail2ban configuration
  fails its check.
- **Editing the manual `nkt-manual` jail** opened an empty draft instead
  of its file; fixed (version history too).

## v1.11.122 — 2026-10-03

- **fail2ban on the hub: templates to selected hosts.** The hub's
  template panel shows standard and custom templates, each with “To
  hosts”: pick hosts (checkboxes cleared, “Select all / Clear all”), a
  required “Check” with a file diff per host, then “Apply”, a hub job
  running three hosts at a time that applies exactly what was checked;
  on error the host rolls its files back. Each host provides its own text
  of a standard template; hosts without the required program are skipped.

## v1.11.121 — 2026-10-03

- **The "narrow sudo" mark is finally saved.** The `narrow` state failed
  the hub database check and was silently not written: after narrowing,
  the mark stayed as it was, red or "?". It is now stored in a separate
  column, carried over by export and import, and a write error goes to the
  service log. Hosts already narrowed get the green mark on the first
  check: open the "hub's sudo" window or update nkt on the host.

## v1.11.120 — 2026-10-03

- **The old hub-sudo instructions fit on one line.** The commands for a
  host narrowed by nkt v1.11.111–1.11.117 are now joined with `&&` and can
  be copied from the error window as a whole; there is also a `sudo sh -c`
  variant for signing in as a user with a password.

## v1.11.119 — 2026-10-03

- **Narrow sudo: `nkt.env` in the signed request.** When installing and
  updating via `hub-sudo`, the file with the admin password is no longer
  placed in a temporary directory on the host and checked by hash: its
  content arrives in the request signed by the hub.
- **Hosts narrowed by nkt v1.11.111–1.11.117** are not compatible: an
  update from the hub fails with instructions. On the host, as root,
  temporarily restore full sudo, then press "Reinstall" and "Narrow sudo".

## v1.11.118 — 2026-10-03

- **The sudo mark follows a check of the host after every operation.**
  After installation, update, narrowing and rule removal, the hub looks
  at what is really allowed without a password. Previously, reinstalling
  a host with the hub key turned the mark green even if another rule
  still granted full sudo, and removing the rule left it "unknown".

## v1.11.117 — 2026-10-03

- **Temporary files on hosts are owner-only.** The staging directories
  for nkt installation, the ClamAV database, the apt proxy and nkt-edge
  are created by a single `mkdir -m 700` (a taken name is an error rather
  than someone else's directory), and `nkt.env` with the admin password
  is `0600`. sudoers rules and `authorized_keys` edits go through
  `mktemp` instead of fixed names in `/tmp`, and k3s downloads go to
  `/var/lib/nkt-k3s`, which only root can write to, so a file can no
  longer be swapped between the check and the installation.

## v1.11.116 — 2026-10-03

- **Fixes from the security scanners.** Deployment git: the "--" before
  the repository address and ref is now right in the git call, and the
  checkout commit is written to HEAD instead of being passed as an
  argument. hub-sudo: a file close error after a failed copy is no longer
  lost.

## v1.11.115 — 2026-10-03

- **The "hub's sudo" window checks the host live.** When it opens and on
  "Check again", the hub looks at what is really allowed without a
  password: if sudoers was edited by hand, the mark in the host list
  corrects itself.
- **Where full sudo comes from.** If another rule grants it, the window
  shows the sudoers lines (file:line, user or group). On a narrowed host,
  a separate line about just this user can be disabled, with a visudo
  check and a `.nkt-bak` copy; the window warns if the user has no
  password. Group and shared rules are left alone.
- **Pressing "Narrow sudo" again** on an already narrowed host no longer
  asks for passwordless full sudo; it records "already narrowed".

## v1.11.114 — 2026-10-03

- **Host accounts: groups, editing, deletion.** "Add" and "Edit" open one
  window with a diff before saving: shell, passwordless sudo, groups as
  checkboxes (`docker`, `sudo`, `adm` and other common ones first, then
  all with search; `docker` warns that it amounts to root), keys (remove
  a single one or add several). The list gains a groups column and a
  "hub" mark. Deletion can take the home directory too; root and the hub
  user cannot be deleted, and cutting off the hub's key or sudo needs a
  confirmation.

## v1.11.113 — 2026-10-03

- **A "Danger zone" in "About": remove nkt from all hosts.** A full export
  comes first and is required (the hub checks too), then the host list,
  unticked, with "Select all / Clear all", what to remove (as when deleting
  one host), the typed word "delete" and a hub job three hosts at a time;
  where the cleanup succeeds, the host leaves the hub.

## v1.11.112 — 2026-10-03

- **Narrow sudo: cleanup and ClamAV.** A narrow-sudo host now supports
  cleanup when the host is deleted and the ClamAV database from the hub,
  as signed `hub-sudo` operations (cleanup in one call, step by step in the
  report). Installing nkt-edge on such a host is refused right away, with
  instructions, instead of failing mid-job. The docs are corrected: hub
  scripts are not affected by narrow sudo.

## v1.11.111 — 2026-10-03

- **"Narrow sudo" for hosts installed earlier.** The Sudo column has a
  "narrow sudo" button for full sudo; it, and a click on the mark, open
  "The hub's sudo" window: the state, the `hub-sudo` operations, the
  sudoers rule, "Narrow sudo" and "Remove the nkt rule". Before replacing
  the rule the hub checks that `hub-sudo` exists on the host (an old nkt
  has to be updated first).

## v1.11.110 — 2026-10-03

- **Narrow sudo.** The first installation of nkt as a user with
  `NOPASSWD: ALL` narrows it right away: without a password the hub may
  only run `nkt hub-sudo`, which runs as root only requests signed by the
  hub's key (derived from the master key) and only operations from a fixed
  list: installing and updating nkt (files checked against signed hashes),
  the service, its journal, the nkt admin password, the apt proxy. An old
  request cannot be replayed. It works for any build and without GitHub.
  The host list shows a green "narrow sudo" shield.

## v1.11.109 — 2026-10-03

- **Documentation:** the hub hosts screenshot now has polled hosts and
  the red passwordless sudo mark.

## v1.11.108 — 2026-10-03

- **Passwordless sudo in red.** In the host list, "passwordless" is now a
  red ⚠ (dangerous: signing in as this user means root at once), next to
  the "remove NOPASSWD" button; "password required" is a green check and
  "unknown" a grey question mark.

## v1.11.107 — 2026-10-03

- **One description window for new and saved pipelines.** A saved compose
  pipeline ("Description") gets the same "from a link" block, filled from
  the description: the link, hosts, stack name and site service; "Fill in
  the description" rebuilds it with a diff before saving. The link is kept
  as a `# compose: …` line in the description (older ones are rebuilt from
  `repo:`). The pipeline name is shown next to the edit note. The docs
  explain what the stack name means.

## v1.11.106 — 2026-10-03

- **Updating the hub, then the hosts.** The "Update the hub" confirmation
  has an "After the hub update, update nkt on all hosts" checkbox, on by
  default: after the hub restarts, the page opens "Hosts" with the "Update
  all" window.

## v1.11.105 — 2026-10-03

- **A "login:token" token and sign-in like ordinary git.** In "Access" the
  token can be entered as `login:token`; before, the whole string went as
  the password and Forgejo rejected it. The hub signs in after the
  server's request, like `git push`, instead of a header sent in advance;
  the system's saved passwords (`credential.helper`) are no longer mixed
  in. The access check shows the last four characters of the saved token.
  Verified on Forgejo 16.0.5.

## v1.11.104 — 2026-10-03

- **Repository access, explained.** "Access" in the new pipeline window
  takes the compose link already pasted (not the template placeholder
  address); if the description is still the template, the check says so.
  Instead of git output, the reason is given in words: private or missing,
  token rejected (with the permission needed in Forgejo/Gitea, GitHub,
  GitLab), ssh key does not fit, server unreachable. The GitLab token uses
  the login `oauth2`. After keys are saved, the old "private" note goes
  away and the "set" marks refresh.

## v1.11.103 — 2026-10-03

- **"Fill in the description" from the file itself.** For your own link
  the hub fetches the compose file (a private repository with the keys
  from "Access") and builds the description from it: the site is the web
  service by image ports (databases skipped), with a "Site: auto / service
  / no site" choice, `files:` for files next to it, `images:` for services
  built from source, notes on unversioned images and outside publications,
  and a `.env` template from `${…}`. A private repository without keys gets
  a hint to set "Access".

## v1.11.102 — 2026-10-03

- **"Access" straight from the pipeline window, with an access check.**
  The description window has an "Access" button: for a new pipeline it
  first saves it under the entered name (disabled without a name) and opens
  the same window as in the list. On opening and after saving the keys,
  the "Access" window checks the repository (`git ls-remote`: access,
  branch) and the registry if one is set; the result is shown in place.

## v1.11.101 — 2026-10-03

- **Forgejo example:** the `.env` template notes that Forgejo forbids
  `admin`, `api`, `user`, `org`, `login` and other service names for
  `FORGEJO_ADMIN_USER` (the deployment failed with `name is reserved`).

## v1.11.100 — 2026-10-03

- **A Forgejo example instead of Gitea, without the install wizard.**
  Forgejo 16.0 (rootless) + PostgreSQL 17: settings from the environment,
  registration closed, and the administrator created from `.env` by the
  one-shot `forgejo-admin` service, so you can sign in right after the
  deployment. SSH is built in, on 2222. The old gitea example's data does
  not move over: remove that stack and deploy the new one.
- **One-shot services in a stack.** If `up --wait` only trips over
  services with `restart: "no"` that exited with code 0 while everything
  else runs and is healthy, the deployment succeeds (the log lists them).

## v1.11.99 — 2026-10-03

- **Go to whatever is responsible.** Findings get "Go to service nginx"
  ("Services" with the row highlighted) and "Go to container"
  ("Containers" with the row highlighted); the same buttons now appear in
  "What's broken" on the overview. The selected resource map node has a
  button to its service, config, container or tab (LXD, machines,
  Kubernetes), and an undeclared listener links to "Firewall".

## v1.11.98 — 2026-10-03

- **Hub "Alerts" has two tabs:** "Journal" (always opened first) and
  "Settings": what to record and notify about, outgoing webhooks, the
  Telegram and Slack bots. The journal no longer sits below the settings.

## v1.11.97 — 2026-10-02

- **Dry run: the site port without false errors.** If the image is not
  pulled on the host yet, its ports (`EXPOSE`) come from the registry
  without pulling (Docker Hub, ghcr.io, quay.io), not from the compose
  publications: the gitea example with `ports: gitea: ["0.0.0.0:2222:22"]`
  and `site.port: 3000` no longer fails with "image listens on 22". If the
  registry does not answer, it is a warning ("the site is checked after the
  deployment"), not an error. The text is more precise ("image declares
  only port …"), and other publications of the site service (gitea's SSH)
  are a plain note without "?".
- **`.env`: references to variables further down.** If a value uses
  `${X}` and `X` is set below it, docker compose substitutes nothing; the
  dry run now names the line and says what to move up.

## v1.11.96 — 2026-10-02

- **Documentation:** a screenshot of the "Edit host sections" window in
  the "Menu" section of the hub updates page (ru, en).

## v1.11.95 — 2026-10-02

- **Menu layout.** The hub's "About" has a "Menu" card: "Hub section
  order" (drag or arrows; hiding is not allowed) and "Edit host sections",
  with the order and a "show" checkbox for all hosts opened through the
  hub at once ("Overview" cannot be hidden). A hidden section only leaves
  the menu; a link still opens it. A diff is shown before saving, with a
  version history and "Reset to default"; the layouts are carried in the
  hub export.

## v1.11.94 — 2026-10-02

- **"Jobs" work like the alert journal**, on the hub and on a host:
  filters by state and kind, a text search (title, host, author, error)
  across all jobs rather than only the latest hundred, pages of 20, 50 or
  100, and date order by clicking "Started"; the choice is remembered in
  the browser. API: `GET /jobs` accepts `q`, `status`, `kind`, `limit`,
  `offset`, `order=asc` and returns `total` and `kinds`.
- Tables, pickers and sort hints now follow the interface language.

## v1.11.93 — 2026-10-02

- **Hosts on the hub:** only the first two or three characters of the
  address are visible, so neither the user nor the IP can be read (in full
  in the tooltip and via copy); the "Address" column moved next to "nkt",
  the freed space went to "Findings", and an empty gap now separates the
  action icons from "Findings".

## v1.11.92 — 2026-10-02

- **The hub export caught up with the hub.** The file now carries the
  deployment sites (the host and the pipeline by name; a site whose host
  is missing is not imported), the selected dry run checks and the `.env`
  history of pipelines (re-encrypted with the receiving hub's key), and
  the help site address with its history. The import plan has a "Sites"
  section with "skip / replace". The format is version 5; files of
  versions 1 to 4 import as before.

## v1.11.91 — 2026-10-02

- **Hosts on the hub: an even table.** Each host takes one line: a version
  mismatch and "unreachable" are icons with a tooltip, not a second line;
  action icons and finding counters do not wrap. Column widths are shared
  by all groups, so the columns line up and do not jump when the finding
  numbers change. The "Architecture" column is gone, and the headers are
  shorter: "f2b", "nkt", "Seen" (the full name is in the tooltip).

## v1.11.90 — 2026-10-02

- **Hosts on the hub:** the address is now always short, with only the
  beginning visible (`alex@127.…`); in full in the tooltip and via the
  copy icon. A short address used to fit the column in full. Machines
  inside a host show it the same way.

## v1.11.89 — 2026-10-02

- **Documentation:** the site's home page mentions the finding buttons and
  vulnerabilities "network-reachable first"; config checks are described
  more precisely (the written file is checked by the service and restored
  if rejected, and the service does not reload it); the quick start gives
  the real number of demo findings (over 70); new screenshots of Findings
  and the hub's hosts.

## v1.11.88 — 2026-10-02

- **Vulnerabilities by real danger.** The new default order is "by
  danger": vulnerabilities reachable from the network come first, then
  severity, and among equals the ones an update fixes. The "Network"
  column shows the ports: the package's service listens beyond loopback
  (the package is found from the process's binary), or a container with
  the image publishes a port. Filters "only reachable from the network"
  and "only with a fix". Works on the hub too. The previous order is "by
  severity".

## v1.11.87 — 2026-10-02

- **An empty hub suggests where to start.** While there are no hosts
  besides localhost, a card above the list offers three paths: add a host
  or import from another hub, open the hub's machine, try a deployment on
  an example (hello-app, httpbin). Next to it is help on a demo without
  servers.

## v1.11.86 — 2026-10-02

- **fail2ban on the hub: "Undo" and a guard against banning your own.**
  After a successful ban or unban on hosts, an "Undo" bar stays at the
  bottom of the screen for 15 seconds; it runs the reverse job on the same
  hosts. Banning an internal address (a private network, loopback,
  link-local, CGNAT) now asks for a separate confirmation listing such
  addresses.

## v1.11.85 — 2026-10-02

- **Action buttons on findings.** Below a finding is a way to where it
  gets fixed: "Open at line N" opens the file in "Configs" with the line
  highlighted, "Go to certificate" highlights the certificate's row in
  "Certificates", "Firewall" appears for ports open to the internet, "Go
  to malware" opens the right tab. A container in a restart loop or not
  running gets "Logs" in a window and "Restart"/"Start" with a
  confirmation and a live log.

## v1.11.84 — 2026-10-02

- **Hosts on the hub:** the address is cut to the column width (in full
  in the tooltip), and the icon next to it copies `user@address:port`,
  over http too, where the browser Clipboard API is unavailable. "Banned"
  shows only icons and numbers: a shield and the number, a red pause when
  fail2ban is stopped, "—" when it is not installed.

## v1.11.83 — 2026-10-02

- **Bots: the "⚠️ Findings" button works**; it used to answer with a
  "cannot unmarshal object" error. The reply is a block: address, state,
  nkt version and the ten most important findings with severity icons
  (🔴 🟠 🟡), in the bot's language. "🔎 Overview" is the same block
  without the list.

## v1.11.82 — 2026-10-02

- **Telegram and Slack bot messages are readable**: an event is a block
  with a bold header (icon, host, what happened, time) and text; new
  findings come as a list of up to three, then "…and N more", without the
  event number.
- **Time is in the bot's time zone**: "13:05", "yesterday 22:40", "Sep 30
  08:15" instead of ISO in UTC. The zone is in the bot settings and
  defaults to the hub machine's (the setup window shows which).
- **`/alerts`** merges identical events from different hosts within two
  minutes ("crem1, cg221 — new findings") and offers the hosts' "⚠️
  Findings" buttons. Alerts the bot sends by itself still arrive one by
  one, at once.
- **`/hosts` and `/status` no longer paint everything green**: 🔴
  unreachable or an error (with its text), 🟠 critical or high findings,
  🟡 medium, 🟢 no serious findings, ⚪ no data; worst first.

## v1.11.81 — 2026-10-01

- The edge HTTPS check builds the address from the validated name, only
  through `url.URL`, with no path, login or foreign scheme (a CodeQL
  finding).

## v1.11.80 — 2026-10-01

- **Outside checks no longer reach into the VPS's own network**: an edge
  with this role refuses to connect to loopback, private, link-local
  (cloud metadata `169.254.169.254`) and CGNAT addresses. The address is
  checked after DNS, so a name pointing inside does not help either. Such
  a check reports "blocked: not a public address".
- The n8n node builds with `n8n-workflow` 2.41 (the stable branch); the
  vulnerable lodash, form-data, uuid and axios are gone from the build
  dependencies. Dependencies install with `npm ci --ignore-scripts`.
- Hub import errors quote names from the file and strip line breaks.

## v1.11.79 — 2026-10-01

- **The n8n node ships with every release**: the
  `n8n-nodes-nkt-<version>.tgz` archive (checksum in `SHA256SUMS`) is
  unpacked into your n8n custom nodes directory, with no build from the
  repository needed.
- **The site documentation** is updated for API tokens, nkt-edge roles,
  outgoing webhooks, n8n and bots: the home page, the hub overview, ports
  and access, security, limitations, troubleshooting, and CI/CD (a dry run
  from CI before deploying). A new page is an **end-to-end example**, "A
  hub behind NAT, n8n, a bot and CI", with new screenshots.

## v1.11.78 — 2026-10-01

- **Hub export carries outside access**: API tokens (role, scope,
  addresses, expiry, secret), outgoing webhooks and the Telegram and Slack
  bot settings. Secrets are re-encrypted with the receiving hub's key;
  hosts in a scope are matched by name. If no host with that name exists,
  the token or recipient is not imported (otherwise the scope would widen
  to "all hosts"), and the import says so. Name matches are "skip" or
  "replace", as in the other sections.

## v1.11.77 — 2026-10-01

- The accidentally committed `nkt-edge` binary (11 MB) is removed from the
  repository; a built `nkt-edge` in the root is now in `.gitignore`, like
  `nkt`.

## v1.11.76 — 2026-10-01

- **A Slack bot** in "Alerts" → "Slack": alerts with buttons in channels
  and the /nkt command: status, hosts, alerts, pipelines, deploy and
  ban/unban with a confirmation button, and dryrun. Slack sends commands
  and button presses by itself, so a way in is needed: the new nkt-edge
  "callbacks" role (which accepts only `POST /callbacks/slack/…`) or a hub
  reachable from the internet. The hub checks every request with the
  Slack signature. Channels have roles, actions can be limited to a list
  of people, and everything goes to the audit log.

## v1.11.75 — 2026-10-01

- **A Telegram bot** in "Alerts" → "Telegram bot". Hub alerts come with
  buttons: "Overview" and "Findings" for a host, "Log" and "Retry" for a
  deployment. Commands: /status, /hosts, /alerts, /pipelines, /deploy and
  /ban, /unban with a confirmation button, and /dryrun; the bot sends the
  job outcome by itself. The hub polls Telegram by itself, so it needs
  neither an edge nor an open port. Chats have a read or admin role, and
  actions can be limited to a list of people; everything goes to the
  audit log.
- A ban on the hub machine started by an API token no longer fails: such
  jobs act as a hub admin.

## v1.11.74 — 2026-10-01

- **Outside checks from nkt-edge**: a new "outside checks" edge role. A
  hub behind NAT cannot see itself from outside, so its port 80 or HTTPS
  checks could be wrong; now an edge on a VPS runs them as the internet
  does: DNS, ports 80/443, HTTPS and the certificate. The "Sites" wizard,
  the site check and the dry run use such an edge by themselves (marked
  "checked from outside via nkt-edge"), and the edge's "Check from
  outside" button works for any name and ports. This role accepts nothing
  from the internet.

## v1.11.73 — 2026-10-01

- **n8n nodes**: the `n8n-nodes-nkt` package in `integrations/n8n`. The
  **nkt** node covers hosts and their findings, vulnerabilities and
  services, service and container actions, pipeline deploys and dry runs
  with waiting for the result and the log, hub jobs, IP bans and unbans,
  alerts, and any token API call. **nkt Trigger** receives the hub's
  outgoing webhooks and checks the signature; **nkt Alert Trigger** polls
  alerts when n8n cannot receive webhooks. Credentials are an API token
  with signed requests (which also work through nkt-edge). Ready
  workflows: alerts to Telegram, a nightly dry run, and banning addresses
  from a list. Building and installing are on the "n8n" documentation
  page.

## v1.11.72 — 2026-10-01

- **Outgoing webhooks** in "Alerts" → "Outgoing webhooks": the hub sends
  events to n8n, a chat bot or your own system by itself. Events: a host
  is unreachable or back, new findings and their resolution, a failed
  job, a reboot, bans, and a deployment that succeeded or failed. Each
  recipient has a choice of events, hosts and groups, and a text
  language. A POST of JSON with an HMAC signature (as on nkt's incoming
  webhooks), retries on failure, a "Test" button and the last delivery
  outcome. Nothing on the hub needs to be exposed.

## v1.11.71 — 2026-10-01

- **nkt-edge roles and several edges.** An edge has roles: "webhooks" and
  "API". With the API role the edge passes signed API token requests to
  the hub, so n8n in the cloud or CI can reach a hub behind NAT. Bearer,
  cookies, password login and websockets do not pass through an edge, and
  the token secret never reaches the VPS; the token needs the "through
  nkt-edge" box.
- There can be several edges (different VPSes and names), and the
  nkt-edge card lists them all. Roles change with the "Reinstall" button.
  The previous edge becomes the first in the list with the "webhooks"
  role; hub export carries all edges with their roles.

## v1.11.70 — 2026-10-01

- **Hub API tokens** in "About" → "API tokens": access for n8n, CI and
  scripts without a password. A read role (hosts, findings,
  vulnerabilities, alerts, job logs) or an admin role (also deploys, dry
  runs, IP bans, service actions). Scope: hosts and groups, an address
  list and an expiry. A token is presented with an `Authorization: Bearer`
  header or a signed request, where the secret never crosses the network
  and each signature works once. Hub management, the terminal, files and
  websockets are closed to tokens. Details are on the "API and tokens"
  documentation page.
- For polling: `GET /api/hub/events?after=N` returns alerts newer than a
  number; `GET /api/hub/jobs/{id}` and `/log` return a hub job and its log.

## v1.11.69 — 2026-10-01

- **Documentation:** the site's "Features" page caught up with FEATURES:
  deployments from Git, sites, nkt-edge, clusters and hub export; compose
  stack deployment (examples, ports on 127.0.0.1, `.env` with history,
  removal as a job) and the dry run with selectable checks are added.

## v1.11.68 — 2026-10-01

- **Deployment and dry run logs:** problems (`✗`) are bold red, warnings
  (`?`, `!`) bold orange.
- **Pipeline examples:** the chosen example is remembered, so filling in
  the description again (after changing hosts) no longer loses its settings
  (`service: plausible`, `files`, `env_keys`, `wait_timeout`). For your own
  link the `site:` block has explicit placeholders `<service from compose>`
  and `<container port>` instead of `web`/`80`.
- **"About":** "Hub version" comes first, "Private mode" last.

## v1.11.67 — 2026-10-01

- **Dry run with check boxes:** the button opens a window listing the
  checks (Docker and compose, stack description, images, ports, resources,
  healthcheck, stack on the host, site: DNS, from outside, host,
  certificate and nginx, nkt version), all ticked by default. Unticked ones
  are skipped and the log says "not checked: …". The choice is remembered
  on the pipeline (shared by admins); a real deployment always runs its
  own checks.

## v1.11.66 — 2026-10-01

- **The dry run checks more. Problems:** a `${VAR}` without a value in
  `.env`, the docker daemon not running, too little space or memory, 80/443
  held by something other than the site proxy, the name already in another
  nginx config or nginx not reading `conf.d`, an AAAA record not pointing at
  the host, DNS not pointing at the host and port 80 closed from outside,
  nkt on the host older than the hub. **Warnings:** `latest` images,
  services without a healthcheck, the stack already on the host from
  elsewhere or deployed by other pipelines too. **Info:** memory and space,
  the certificate: a valid one (days left) or a new issuance.
- A real deployment also checks the docker daemon on all hosts before the
  first one.

## v1.11.65 — 2026-10-01

- **certbot installs itself:** if the host lacks it, site setup installs the
  `certbot` package by a background host job before issuing the
  certificate, like the proxy (not needed with Caddy). If the install
  fails, a warning: a valid certificate on the host works without it. The
  "Sites" wizard says "certbot will be installed" instead of "install it in
  Packages", and the dry run shows it too.

## v1.11.64 — 2026-10-01

- **ufw on hosts:** a rule could not be added ("'/etc/ufw/user.rules' is
  not writable"): the nkt service runs in a systemd sandbox where
  `/etc/ufw` was not open. Now the ufw rule is added outside the sandbox
  (like config writes), and new installs open `/etc/ufw` in the service.
  This covers both "Firewall" and sites.
- **Site setup no longer stops at the firewall:** if 80/443 cannot be
  opened, the log warns and the certificate and proxy steps follow.
- **The dry run shows the host firewall:** off, 80/443 open, will be
  opened, or the rule cannot be written (then open it by hand).

## v1.11.63 — 2026-09-30

- **"Examples": seven verified stacks:** httpbin, Uptime Kuma, umami +
  PostgreSQL, n8n + PostgreSQL, Gitea + PostgreSQL, WordPress + MariaDB and
  Plausible (PostgreSQL + ClickHouse). Each was deployed on docker and
  checked by the service's answer; README and `pipeline.yaml` are in
  `examples/`. They show `files` (n8n, Plausible), `env_keys` (umami, n8n,
  Plausible), `images` (umami pinned at 3.4.0), `ports` with SSH open
  (Gitea), and a site behind HTTPS (WordPress).
- **A failed image pull names the cause:** "Docker Hub limited pulls
  (429)", "no access to the image", "no such tag" instead of "pull: code
  1"; the dry run also names the Docker Hub limit.

## v1.11.62 — 2026-09-30

- **`compose.env_keys`: secrets from .env for someone else's compose
  files:** if a project's file writes a secret as a value (`APP_SECRET:
  replace-me…`), `.env` does not override it; now the hub replaces the
  listed variables' values in the file copy with references to the
  pipeline `.env`. A variable missing from `.env` stops the deployment
  before it starts, and the dry run names it.
- **"Examples" instead of the httpbin button:** a list of ready pipelines
  with descriptions: httpbin and **umami + PostgreSQL** (two services,
  secrets through `env_keys`, a data volume, a `.env` template);
  `examples/umami` in the repository. Verified: umami comes up in ~80
  seconds with both services healthy.
- The help buttons in the pipeline editor are labelled with their sections.

## v1.11.61 — 2026-09-30

- Docs (English): dry-run checks (ports, architecture), causes of a failed `up` and the "Enabled" switch.

## v1.11.60 — 2026-09-30

- **A stack that did not come up says why:** the host appends the container
  states and the last log lines of the failed services, and the error names
  the cause: a port in use ("Port 127.0.0.1:8080 on the host is already in
  use"), an image for another architecture, an exited container, a failed
  healthcheck, instead of "code 1".
- **The dry run checks host ports in use** (except those the stack itself
  holds) and **the image architecture** against the host's: what used to
  fail the deployment although the dry run passed.
- **The pipeline's "Enabled" switch** has a hint: it is only about
  automatic deployments; a disabled pipeline is marked "manual only".

## v1.11.59 — 2026-09-30

- **YAML errors in the pipeline description are explained:** the line,
  its text and what is wrong: "key site already has the value
  hb.example.com, yet a nested block follows", an unknown key with the
  allowed ones, a tab, "a number is expected" and so on, instead of "did not
  find expected key".
- **Site port:** `port: auto` (or no `port`) takes the port from the image
  if it declares one; a `site.port` equal to the host port from
  `compose.ports` ("127.0.0.1:8080:80" with `port: 8080`) is an error with
  the hint "80 is needed"; the publication log lines are clearer and come
  after the heading.
- **Wrong certificate:** if a site answers with a certificate for another
  name (the proxy answers as another site), the HTTPS check says so, the
  site gets the "error" status, and "Sites" shows "wrong certificate" with
  the certificate's names.

## v1.11.58 — 2026-09-30

- **A host with an old nkt version:** deployments and dry runs there no
  longer fail with "unknown field": the hub repeats the request without the
  new fields and logs which checks do not work on that host (site port,
  hand edits of `.env`); update nkt on the host.
- **Site port:** a clear error when `site.port` is not set; the dry run
  suggests the right port ("the image listens on 80; set port: 80"); a
  warning when `compose.ports` publishes a different container port of the
  service.
- **No model-analysis bulb** in the job windows of "Deployments"
  (deployment, dry run, deletion, site, stack on a host).

## v1.11.57 — 2026-09-30

- **Help next to the section title:** a "?" icon in place of the former
  "ⓘ" shows the hint on hover and opens the section's help when clicked
  (a window with "Detach"). The button at the bottom of the sidebar is
  gone.
- **The site port is checked before the certificate:** `site.port` (and the
  port in the "Sites" wizard) is compared with the ports the image declares
  (`EXPOSE`) before installing the proxy and issuing the certificate; a
  wrong port (e.g. image `kennethreitz/httpbin` listens on 80 but 8080 is
  given) gives a clear error instead of a 502. A dry run counts it as a
  problem; on a 502 the log suggests checking the port.

## v1.11.56 — 2026-09-30

- Housekeeping: the Python cache (`__pycache__`) no longer gets into the repository.

## v1.11.55 — 2026-09-30

- **In-app help:** the "Help" button at the bottom of the sidebar opens
  the documentation section for the current page, in a window inside nkt,
  with "Detach" into a separate browser window and "In a new tab". The
  pipeline editor, "Access" and the "Sites" wizard have buttons straight
  to their sections.
- **The help site address** is in "About" (and in the help window): the
  project site by default or your own copy, e.g. on a network without
  internet; editing with a diff and history.
- Fixed in-docs links to sections whose Russian titles contain "й".

## v1.11.54 — 2026-09-30

- **Compose stack ports on 127.0.0.1 only (important):** a publication
  without an address (`8080:80`) listened on all addresses, and docker
  opens it bypassing ufw and firewalld. The hub now gives such
  publications the `compose.bind` address, `127.0.0.1` by default, **for
  existing pipelines too, from their next deployment**. A stack meant to be
  public without a proxy (mail, a game server) needs `bind: 0.0.0.0` or an
  explicit address in `compose.ports`. `bind_force: true` replaces addresses
  that are already set. The deployment and dry run logs show what happened
  to each port.

## v1.11.53 — 2026-09-30

- **`compose.ports` in the pipeline:** a service's port publications
  instead of those in the compose file; an empty list removes them (e.g.
  the `80:80` in `postmanlabs/httpbin`, so port 80 stays with the site
  proxy). No fork of someone else's file needed.

## v1.11.52 — 2026-09-30

- **`compose.images` in the pipeline:** ready images for services instead
  of `build:`, for someone else's compose file without a fork (e.g.
  `postmanlabs/httpbin` → `httpbin: kennethreitz/httpbin`). The hub sets
  the service's `image:` and drops `build:`; `{{nkt.tag}}` is allowed in
  the image. A service missing from the file is an error before the
  deployment.

## v1.11.51 — 2026-09-29

- Deleting a file on a host checks the path up front (absolute, no
  ".."), not only when leaving the sandbox, the same as writing (CodeQL
  go/path-injection alert #271).

## v1.11.50 — 2026-09-29

- **Deleting a compose pipeline is a job that cleans the hosts:** the
  pipeline's site, then `compose down` and the stack directory on each
  host, then the hub record. Without the "volumes" tick the directory is
  not wiped but moved to `/srv/compose/.nkt-removed` (data kept); volumes,
  images and the certificate are behind ticks, off by default. If it fails:
  "deletion unfinished" and "Retry deletion" with the same ticks.
- **Deleting a site is a job:** the proxy config, the service's 127.0.0.1
  publication and, if ticked, the certificate; if it fails, "Retry
  deletion".
- `manifest`, `helm` and `script` pipelines are still deleted from the hub
  only.

## v1.11.49 — 2026-09-29

- **A failed deployment is not retried** by polling and the registry every
  interval: the hub remembers the commit or tag it failed on and waits for
  a new one; the pipeline shows "waits for a new commit". The "Deploy"
  button works as before.
- **Stack `.env` history** in "Access": every change is a version
  (encrypted), differences by variable name only, values behind a button
  for administrators with an audit log entry, restoring a version. Saving
  a new `.env` first shows which names appear and disappear.
- **Rollback with `.env`:** a tick in "History" restores the `.env`
  version that deployment used.
- **A hand-edited `.env` on a host** is visible: the deployment log and the
  dry run warn that the file was edited after the last deployment and will
  be overwritten.

## v1.11.48 — 2026-09-29

- **A site in the pipeline:** in `action: compose`, a `site:` block
  (names, service, port, proxy) makes the hub set the site up after
  deploying the stack, the way the "Sites" wizard does: DNS and ports from
  outside, proxy, publishing on 127.0.0.1, certificate, config, HTTPS.
  Later deployments only check HTTPS and set it up again if the block
  changed. A site needs exactly one host; if it fails, the deployment
  still succeeds, with the reason in the log and on the site. A dry run
  shows what would happen to the site. The "Sites" tab marks such a site
  with its pipeline. A `site: name` string still means only an HTTPS
  check.
- **An "Example: httpbin" button** in the new pipeline window: the link to
  the example in the nkt repository and the stack and pipeline names; with
  hosts picked, the description is ready at once.
- **git missing on the hub:** the "Pipelines" tab shows a banner and an
  "Install git" button (a background job on the hub machine); the
  deployment error points to it.

## v1.11.47 — 2026-09-29

- **fail2ban on the hub, ban and unban on all hosts:** a banned address
  shows up in "Banned addresses" right away (and disappears after an
  unban): the hub re-polls the affected hosts when the job ends instead of
  waiting for the next timed poll.

## v1.11.46 — 2026-09-29

- **Dry run of a compose stack deployment**: a button in the "Deploy"
  window and in the pipeline editor (unsaved text included). It does
  everything a deployment would do except the changes. On each host:
  docker/podman and compose, which stack files would appear or change,
  what happens to `.env`, `compose config` on a copy of the stack, whether
  the images are in the registry. The result is a job with a log, with no
  entry in the deployment history.
- **A host check as soon as the host is picked**, in "Compose from a link"
  and in the "Sites" wizard: whether docker with compose is ready; where it
  is not, an "Install Docker" (or "Install compose") button runs a
  background host job.
- A stack deployment first checks docker and compose on all hosts: if one
  lacks them, it does not start and no host is touched.

## v1.11.45 — 2026-09-29

- **fail2ban, ban on all hosts:** the `nkt-manual` jail did not load: its
  filter had no `<HOST>` group, and fail2ban rejected it on reload ("No
  failure-id group"). The filter is fixed and rewrites itself on the next
  ban or setup if a host still has the old one.
- Rolling back a new config that failed validation on hosts where the
  directory is closed by the systemd sandbox ("read-only file system"): the
  file is now removed the same way it is written, outside the sandbox.

## v1.11.44 — 2026-09-29

- The httpbin example: the image is pinned to a version,
  `mccutchen/go-httpbin:2.25.0` from Docker Hub instead of `latest` from
  ghcr.io.

## v1.11.43 — 2026-09-29

- **An example compose stack deployment, httpbin** (`examples/httpbin`):
  go-httpbin on a ready image, a pipeline and the steps up to a site with
  a certificate; it deploys straight from a link to the compose file in
  the nkt repository.
- **A service with `build:` and no `image:`**: the hub stops the
  deployment before touching any host and explains that a ready image is
  needed (before, the host failed on the build attempt with an unclear
  error).

## v1.11.42 — 2026-09-29

- **Deployments: a compose stack from the repository** (`action: compose`):
  the compose file and the files it needs go to hosts one after another,
  with `pull` and `up --wait` (the next host starts only once the
  previous one's containers are up and healthy); the first failure stops
  the deployment. Podman when there is no Docker. Ready images only.
- **Compose from a link:** in the new pipeline window, paste a link to a
  compose file on GitHub, GitLab or Codeberg/Gitea, pick hosts, and the
  description is ready.
- **The stack's `.env`** in the pipeline's "Access": encrypted on the hub,
  0600 on the host, kept out of the history and logs.
- **"Sites" tab:** a domain on a host with DNS and 80/443 checks from
  outside (from the hub), a proxy choice (nginx, HAProxy, Caddy; what is
  installed and running is shown; with none, nginx is installed), a
  target that is either a compose stack service (published on 127.0.0.1
  only) or `address:port`, a Let's Encrypt certificate, a proxy
  configuration with checks and history, and an HTTPS check from outside.
- A pipeline ref naming a tag: when no such branch exists but the tag
  does, the tag is used.

## v1.11.41 — 2026-09-29

- **AI answer:** the “Generated by …” line shows only the model name,
  without the provider and address; in the reader's language; a stored
  answer shows the model that produced it, not the current one.
- **“Show request” shows the whole request:** to whom (provider, model,
  address, wait time), the instruction (default or edited), the message
  as sent to the model, a “what was replaced” table (admins only) and a
  “copy the request” button. The request is stored with the answer; the
  same for the resource map architecture review and its history.
- Address check: in the model request the facts are labelled “What is
  known about the address”, and the address is not repeated.

## v1.11.40 — 2026-09-29

- **A local model with an “…/v1” address works.** An address like
  `http://server:8080/v1` (llama.cpp, LM Studio, OpenRouter) no longer
  turns into `…/v1/v1/…`, where “Get models” and “Test” answered 404
  “File Not Found”. A provider error now names the request address.
- “Test” with an empty model field said “the address must start with
  http://”; now it says the model is missing.
- **Alerts:** a third settings column, “hide”: the kind is recorded but
  not shown in the log and not counted as unread. The log gets filters by
  kind and host, a text search over the whole log and a “show hidden”
  checkbox; the filter is remembered in the browser.

## v1.11.39 — 2026-09-29

- **The AI model comes from the provider's list.** In “About → Model
  analysis” a “Get models” button next to the model field asks the
  provider (Anthropic, OpenAI, Ollama, vLLM, LM Studio) for its list using
  the address and key currently in the form. The list is searchable, shows
  the release date or model size, newest first; embedding, speech and
  image models are hidden (“show all” brings them back). A model missing
  from the list gets a mark; after picking one you are offered to “Test”
  it right away. Typing a name by hand still works. The list does not use
  the daily limit.

## v1.11.38 — 2026-09-29

- **Hub export carries everything.** The file (format version 4) now
  includes deployment pipelines with their revision history, secrets and
  the same webhook address (webhooks in GitHub/GitLab keep working),
  fail2ban templates with version history, nkt-edge settings and every
  edited AI instruction (“configuration help” and the new “address check”
  used to be lost). With a checkbox, web interface user accounts too
  (logins, roles, password hashes). Every secret is re-encrypted with the
  receiving hub's key. Version 1 to 3 files import as before.
- **Import goes through a plan.** The window shows what the file
  contains by section and what already exists on the hub; every match
  gets “skip” or “replace” (and “all” per section). Replacing adds a new
  version to the history (you can roll back). After the import comes a
  report: added, replaced, skipped, errors.
- Fixed: importing the same file again duplicated hosts; now a host with
  a taken name is skipped or replaced as chosen.

## v1.11.37 — 2026-09-29

- **fail2ban: exceptions (ignoreip) in plain sight.** A new card shows
  the common `[DEFAULT] ignoreip` list with the hub address pinned and
  the effective list of every jail, common or own. The common list is
  edited right there: a diff of every file, a `fail2ban-client -t` check,
  version history, an “add my address” button.
- An `ignoreip` edit in “Configs” is no longer lost: after writing or
  rolling back a fail2ban file the hub protection file is rebuilt (before,
  it overrode the edit until “Set up” was pressed).

## v1.11.36 — 2026-09-29

- **fail2ban gets its own host section, right below “Firewall”.** State
  and version, running jails (where they read from, rules, failures and
  bans), banned addresses with ban times: unban per row, with checkboxes
  or all at once, and a manual ban into the `nkt-manual` jail for a
  chosen time. A Ban/Unban/Found event log over 1, 3 or 7 days from
  `fail2ban.log` (with rotations) or journald, with search and filters.
- **Editing jails in a window with a form, a diff and history.** nkt
  writes its own `jail.d/nkt-<jail>.local` on top of `jail.conf`; before
  writing, `fail2ban-client -t` runs and a configuration with errors is
  rolled back. Jails are enabled and disabled the same way. fail2ban
  files in “Configs” are now checked with `fail2ban-client -t` too.
- **Jail templates:** sshd, nginx (auth, bots, limit_req), HAProxy,
  Postfix, Dovecot, recidive, marked by whether the program is on the
  host; applying goes through a diff of every file. Custom templates with
  a filter, a `fail2ban-regex` test against a host log and version
  history are stored on the hub.
- **Installation with a button**, as a package in the standard job
  window, followed by setup: the hub's address in `ignoreip`, the manual
  bans jail, sshd via journald where there is no log file (Debian 12+).
- **The hub never bans itself:** it learns its external address as the
  host sees it (`SSH_CONNECTION`) and passes it to the host, which keeps
  it in `[DEFAULT] ignoreip`. Banning the hub's address or your own is
  refused.
- **Findings:** SSH exposed without fail2ban, fail2ban not running, no
  sshd jail, a jail without logs, the hub not in `ignoreip`.
- **A “fail2ban” section on the hub:** where it is installed and how many
  are banned, which addresses are banned and where, banning and unbanning
  on all hosts as a job with a per-host log, custom templates. The host
  list gets a “Banned” column.
- **Alerts about new bans** and **address checks:** every external IP in
  an alert gets its own bulb, an AI review with its own instruction (bans
  and logs of all hosts, reverse DNS), and a “Ban on all hosts” button in
  the answer window, available even without AI configured.
- Fixed: an edited AI instruction for configuration help was not saved.

## v1.11.35 — 2026-09-28

- **nkt-edge:** the `EDGE_PROXY_ADDR` value is no longer written to the
  service log, neither on an error nor at startup — a string from the
  environment with line breaks could forge neighboring entries (gosec
  "Log injection via taint analysis" warnings).

## v1.11.34 — 2026-09-28

- **Deleting in "Containers & VMs" — by checkboxes and without repeats.**
  Docker and Podman containers, LXD instances and snapshots, libvirt
  machines, container images, machine images and disk files, LXD networks
  and images and Kubernetes objects can be selected and deleted at once.
  Potentially long deletions (containers, instances, snapshots, machines,
  container images, Kubernetes objects) run as a background job with a
  log: while an object is being deleted its row is locked — even after a
  page reload — and clicking again opens the same job. Afterwards the host
  is rescanned and the tables are rebuilt, related ones included (delete a
  machine with its disks and the disk list refreshes too). Quick deletions
  (LXD networks and images, machine images and templates, backups) happen
  right away, with the buttons locked meanwhile.
- **"Services":** no more chips above the table — it lists every installed
  service, running ones on top, stopped and failed ones below with a
  "start" button. Services that aren't installed are not shown: install
  them in "Packages".
- **"Configs":** the "File saved and configuration reloaded" message no
  longer stays when you switch to another file.

## v1.11.33 — 2026-09-28

- **Uploads to "Disks → Files" with a plan and a diff.** Before writing —
  a "What will change" window: new, changed (text files get a "on the host
  → uploaded" diff), identical (not uploaded) and protected files, each
  with a checkbox. An upload deletes nothing on the host.
- **A folder's protected files.** Server settings and data — `.env`,
  `wp-config.php`, `*.local.*`, the `uploads/`, `storage/`, `media/`,
  `data/` folders and others — are not overwritten by an upload by
  default; the list is edited with a diff and history, and overwriting
  takes an explicit checkbox.
- **Upload history and rollback.** Every upload is a record with its
  author, comment and contents; previous versions of replaced files are
  kept, and "Roll back" restores them as a job and removes what was added.
  Any file has a "Version history" with a diff and rollback.
- **History storage with limits.** 200 MB of large files per upload (text
  files always), 1 GB total, 30 days; beyond that the oldest is evicted.
  Manual cleanup: a version, a file's whole history, an upload, everything
  older than a chosen age. From 80% full — a finding in "Findings" and a
  hub alert.

## v1.11.32 — 2026-09-28

- **"Update all" no longer starts updates twice.** While updates run, the
  button doesn't count hosts whose installation is already running or
  queued ("updating: N" next to it). Unreachable hosts are skipped, and
  ones that failed last time are updated only with the "retry failed"
  checkbox in the confirmation window — which also shows who gets updated,
  who doesn't and why. While updates run, the host list refreshes every
  few seconds.
- **Editing files in "Disks" works as in "Configs".** An edit comment, a
  diff before writing and a "Version history" tab: who changed what, when
  and why, a diff of any version against the current file, rollback. If
  the file is a configuration of a known service (e.g. a compose file in
  `/srv`), it is written with the service's check and a rollback on error,
  and the history is shared with "Configs".

## v1.11.31 — 2026-09-27

- **nkt-edge: certbot issues the certificate during installation.**
  Instead of issuing "on the first request", the install job installs
  certbot and issues a Let's Encrypt certificate in standalone mode (no
  nginx needed). If port 80 is held by a service (e.g. nginx), certbot
  stops it for a few seconds during issuance and renewal. `certbot.timer`
  renews it, and edge picks up the new certificate by itself without a
  restart; it is also visible on the host's "Certificates" page.
- **Name ↔ IP check.** The webhook name is taken from the host address in
  the hub; the "check the name" button and the job itself compare the A
  record with the VPS addresses. On a mismatch the installation stops with
  an explanation instead of failing at certificate issuance.
- **A check at the end of installation:** the hub requests
  `https://name/healthz` the way GitHub will and logs the result.
- A stray nkt-edge instance (started by hand, left from an earlier
  install) holding the ports is now found before installing — with its
  PID and how to stop it. The nkt-edge service runs as the `nkt-edge`
  system user; "Remove from VPS" removes it and the certificate too.
- **"Services": a "Ports" column.** Every service shows the sockets it
  listens on — now you can see who holds 80 or 443.
- **"Other services"** no longer hide another program on a port from the
  nginx config: a socket counts as described only if nginx itself
  (haproxy, Caddy, containers) holds it. Before, e.g. nkt-edge on 443 with
  nginx installed showed only as port 8444.

## v1.11.30 — 2026-09-27

- **nkt-edge on a VPS that already runs a web server.** If 443 was taken
  by nginx or Caddy, edge crashed and restarted in a loop while the hub
  showed only "EOF". The install window now has a **proxy port**: edge
  takes webhooks over HTTP on `127.0.0.1:port`, and your server forwards
  `/hooks/` to it (ready nginx and Caddy snippets are in the install log).
  The rate limit and "GitHub addresses only" work behind the proxy using
  the address from `X-Real-IP`.
- **Edge installation checks the ports beforehand** and names the program
  holding 443 or 8444; a taken 80 is only a warning (the certificate is
  issued through 443). After starting, the job makes sure the service
  isn't restarting in a loop and shows its log otherwise.
- **A clear connection error:** instead of "EOF" — "edge accepts the
  connection and closes it right away", with a hint where to look.
- **"Remove from VPS"** on the nkt-edge card — a job that removes edge
  from the VPS completely (service, program, settings, certificates, the
  ufw rule for 8444) and forgets it in the hub.
- nkt-edge binds the webhook ports before the tunnel port: with a port
  taken, the service exits right away with an explanation.

## v1.11.29 — 2026-09-27

- **All documentation is on the site.** Everything from README, HUB.md and
  DEVELOPMENT.md has moved to the [site](https://piqab.github.io/nkt/en/)
  and is now in English too; the menu has "Installation", "Hub",
  "Deployments (CI/CD)", "Reference" and "Development" sections. The
  README is short now: what it is, a quick start and links; HUB.md and
  DEVELOPMENT.md point to the site pages.
- **New pages:** installing the hub (systemd, Docker Compose, Kubernetes,
  removal), "Ports and access" (what to expose, proxies, an SSH tunnel with
  the 8446 forward port), hub hosts, updates and the vulnerability
  database, the package cache, nkt-edge (layout, security model, install,
  checks, an "if it doesn't work" table), a reference of every `NKT_*` and
  `EDGE_*` variable with defaults, security, API, limitations,
  troubleshooting.
- **Deployments:** every pipeline field, GitHub, Gitea/Forgejo, GitLab and
  nkt webhook signatures, polling, registry, rollback and an "if nothing
  deploys" table. The "Deployments" page on the site opens again — the
  `{{nkt.tag}}` substitution used to break it.
- **CI/CD examples:** `examples/hello-app` gains GitLab CI
  (`.gitlab-ci.yml`), Gitea/Forgejo Actions (`.gitea/workflows/build.yml`),
  a signed webhook call script `scripts/nkt-hook.sh` (the GitHub workflow
  uses it too) and a no-webhook variant where the hub watches the image
  tags in the registry itself. Every file of the example is checked by
  tests, the signing script with a real request.
- New screenshots: deployments, the webhook via nkt-edge, Helm, port
  forwarding.

## v1.11.28 — 2026-09-27

- **Installing nkt-edge crashed the hub.** Uploading the program to the VPS
  called a missing progress handler, the hub crashed and after the restart
  resumed the same job — and crashed again. Fixed: the upload progress
  goes into the job log.
- **Jobs can no longer crash the service.** An internal runner error (a
  panic) now fails the job with an entry in the service log instead of
  stopping the hub or host.
- **A limit on resuming after restarts.** If the service restarted in the
  middle of a job more than three times, the job is interrupted instead of
  resuming in a loop.
- **The nkt-edge log:** values from the request are additionally stripped
  of newlines (a CodeQL warning).

## v1.11.27 — 2026-09-27

- **Helm:** the tab got a namespace selector — the same shared filter as in
  the other cluster object sections.
- **The nkt-edge tunnel without disabling TLS verification.** The hub
  trusts exactly its edge's tunnel certificate — it goes in as the only
  root of the check. When installing from the hub the certificate is
  fetched over SSH; for a manual setup it is pasted into the window
  (`sudo cat /var/lib/nkt-edge/tunnel/tunnel.crt`). There is no more
  trust on first connection. An edge installed by version 1.11.24
  recreates its certificate on restart — reinstall it from the hub or
  paste the new certificate into "Configure manually".
- **The nkt-edge log** is written as fields (`log/slog`): values from the
  request are escaped, a fake log line cannot be planted through the
  path.
- **Helm values and source files** are named after validated namespace and
  release names (DNS-1123) — the path cannot leave nkt's Helm directory.
- **Port forward:** an address without the trailing slash gets a 404 with
  a hint instead of a redirect (the UI links have the slash anyway).
- **The `examples/hello-app` example:** a manifest with a `securityContext`
  (non-root, no privilege escalation, all capabilities dropped, read-only
  root filesystem, seccomp) and a CPU limit — trivy is clean on the
  example.

## v1.11.26 — 2026-09-27

- **The `examples/hello-app` deployment example.** A small Python
  application with tests, a Dockerfile and a GitHub Actions workflow
  (tests, the image into GHCR, a signed hub webhook with the image tag) —
  and three ready deployment variants: a manifest into Kubernetes, a Helm
  release on the generic onechart chart, a Docker Compose host through a
  hub script. A step-by-step guide is in the example's README (ru and
  en); nkt's tests check the example files with the same checks the hub
  uses.
- **Hub scripts:** blocks (a compose file, a file) now get the script's
  variables (`param`, `set`) substituted — e.g. `${TAG}` in the image
  name. Other `${…}` stay as they are: that is compose's own syntax.

## v1.11.25 — 2026-09-27

- **Port forward to the browser — on a separate address.** The pod's
  application no longer opens sandboxed under the nkt address but on its
  own port (8446 by default, `NKT_FORWARD_ADDR`; behind a reverse proxy —
  `NKT_FORWARD_PUBLIC_URL`) — it gets its own origin: module scripts
  (Vite, React, Vue), `localStorage` and the app's cookies (under the
  forward path) work. The nkt session is not passed to the application
  and cannot be overwritten by it. The hub keeps such an address itself
  and hands the request to the host; port 8446 is published in the Docker
  Compose and Kubernetes manifests.
- **Cross-site request protection.** The nkt and hub API accept changing
  requests only from their own address (`Sec-Fetch-Site`, `Origin`) — a
  page on a neighbouring port of the same host cannot change anything on
  behalf of a logged-in administrator.
- **The forward window** lists the links already open for this pod or
  service: open, copy, close (the kubectl process stops and the link
  stops working).
- **Helm: release values.** When no values were set at install time the
  window says so instead of showing emptiness; "All values"
  (`helm get values --all`) and "Chart default values"
  (`helm show values`) buttons with "To the draft".

## v1.11.24 — 2026-09-27

- **nkt-edge — webhooks without exposing the hub.** A separate small
  program (about 8 MB) for a VPS: a Let's Encrypt certificate, it accepts
  only `POST /hooks/{id}` (body up to 1 MB, a rate limit, optionally
  GitHub addresses only) and hands the webhook to the hub over a tunnel
  the hub itself keeps to it. The hub may sit behind NAT with no open
  ports; only the webhook is reachable through the tunnel, and edge holds
  no database, no secrets and no access to hosts — the hub checks the
  signature.
- The tunnel is TLS 1.3 with a pinned edge certificate fingerprint and a
  token; the hub reconnects on its own.
- **Installing from the hub:** "Deployments" → nkt-edge → "Install on a
  host" — the job installs the program for the host's architecture and a
  least-privilege service, opens the ports in ufw, pins the fingerprint
  and connects the hub. The webhook address via edge appears in the
  pipeline's "Webhook" window. Manual installation is there too:
  `deploy/nkt-edge.service`, `deploy/edge.env.example`, the
  `nkt-edge-linux-*` binaries in the release, `make edge`.

## v1.11.23 — 2026-09-27

- **Hub: deployments from Git.** A new "Deployments" section: a pipeline
  takes a repository and deploys it as a manifest into clusters
  (`kubectl apply`), a Helm release (values from Git, the image tag into
  `tag_key`) or a hub script (`git pull`, `docker compose up`, migrations —
  parameters `TAG`, `COMMIT`, `REF`). Manifests get `{{nkt.tag}}`,
  `{{nkt.commit}}`, `{{nkt.ref}}` substituted. Images are built by your CI
  or by you — nkt deploys what is ready.
- **When:** the "Deploy" button (a branch or tag); a **webhook** from
  GitHub, Gitea/Forgejo, GitLab or a CI step — signed only, a repeated
  delivery is rejected, the webhook chooses nothing itself; repository
  **polling** (`poll`); **registry watching** (`registry`) — a new image
  tag is deployed on its own (GHCR, Docker Hub, GitLab, Harbor — the token
  is obtained through the standard challenge).
- The pipeline description is YAML with validation, diff-based editing
  and a revision history. Access to a private repository (token or key)
  and registry is kept encrypted on the hub and never shown; the webhook
  secret is shown to an administrator with an audit log entry.
- Every deployment is a hub job with a log; "History" shows what, the
  trigger and the outcome; "Roll back" deploys an earlier successful
  commit. Polling and registry switch on after the first deployment by
  the button.

## v1.11.22 — 2026-09-27

- **Hub: Kubernetes findings across all clusters.** The "Clusters" section
  has a card with Kubernetes findings from every control plane at once
  (polled in parallel), by cluster and severity, each with an AI bulb.
- **A Helm release into several clusters.** The "Helm to clusters" button:
  chart, version, release, namespace and values (with block mode) — and
  the clusters. A hub job installs the release on each control plane in
  turn, with each job's log; an error in one cluster does not stop the
  others, and the outcome lists the failed ones.
- **Picking clusters by group.** Manifests and Helm have a quick pick:
  every ready cluster or the clusters of one host group.

## v1.11.21 — 2026-09-27

- For developers: the job cancellation test sometimes hung in a full
  `go test ./...` run — the "job started" signal was lost when the runner
  got there before the test. It did not affect nkt itself.

## v1.11.20 — 2026-09-27

- **Kubernetes upgrades.** The host's Kubernetes card has "Upgrade": the
  current version and a choice of minor version (k3s — the update.k3s.io
  channels, kubeadm — at most the next minor, as kubeadm requires). The
  node is upgraded by a job: k3s gets the chosen branch's binary (checksum
  verified) and a service restart, keeping its install flags; kubeadm
  gets the pkgs.k8s.io branch, `kubeadm upgrade apply` on the control
  plane (`upgrade node` on the rest), kubelet and kubectl.
- **A hub cluster as a whole.** A cluster in the "Clusters" section has
  "Upgrade": a hub job walks the nodes, control planes first; on each —
  cordon and drain, the upgrade, waiting for Ready, uncordon (a single
  node skips draining). It stops at the first error, "continue" resumes
  from the same node. Downgrades are refused.

## v1.11.19 — 2026-09-27

- **Kubernetes in "Load".** On a control plane the usage collector takes
  `kubectl top pods` (needs metrics-server, k3s ships it), and pod CPU and
  memory build up a history like containers and machines. "Load" has a
  "Kubernetes" source: charts, top pods and the heatmap.
- **Pod restart alerts.** A new finding: a pod restarted within the last
  hour (high severity). Together with NotReady and CrashLoopBackOff it
  arrives as the hub's "problems" alert; after an hour without restarts
  the finding goes away and the hub records "resolved".

## v1.11.18 — 2026-09-27

- **Kubernetes: a pod's or service's application in the browser.** The pod
  and service menus have "Open in the browser": a port from the object's
  spec, `kubectl port-forward` on the host and the nkt proxy. The
  application opens in a new tab, through the hub too, with no Ingress or
  NodePort.
- The link is one-off (a random token), lives while it is used and closes
  after 30 minutes idle; open forwards are listed above the cluster
  objects with a "close" button. The page opens sandboxed with no access
  to the nkt session; every forward is recorded in the audit log.
  Applications that reference their files from the site root
  (`/static/…`) may open only partially.

## v1.11.17 — 2026-09-27

- **Vulnerabilities of Kubernetes pod images.** On a control plane the
  vulnerability check (both the full one and "images only") scans the
  images of every pod in the cluster. The image comes from the node's
  containerd (the k3s or kubeadm socket, namespace `k8s.io`), or straight
  from the registry when it is not there. The "source" column shows the
  image and the pods it runs in; an image already scanned as a Docker or
  Podman image is not checked twice.

## v1.11.16 — 2026-09-27

- **Kubernetes: access (RBAC).** A new tab: ServiceAccounts, Roles,
  RoleBindings, ClusterRoles, ClusterRoleBindings. A ServiceAccount shows
  "who can do what": every role bound to it in its namespace and
  cluster-wide; a binding to `cluster-admin` is highlighted. Roles show
  their rules briefly ("resources: verbs"), bindings the role and whom it
  is granted to.
- **NetworkPolicy** in the "Network" section: which pods it covers, types,
  the number of ingress and egress rules.
- **HPA** in Workloads: target, min/max, replicas "now → desired", metrics
  "now/target"; one stuck at its maximum is highlighted. The "Bounds"
  action changes min and max; YAML and block mode work as for other
  objects.

## v1.11.15 — 2026-09-27

- **Kubernetes: new cluster hygiene findings** (system namespaces `kube-*`
  are not checked):
  - containers without `limits.memory` — per Deployment or StatefulSet,
    not per pod;
  - images without a version or tagged `latest`;
  - a ServiceAccount (high severity) or a user bound to `cluster-admin`
    through a ClusterRoleBinding;
  - a namespace with pods but not a single NetworkPolicy.
- "New object" has a default-deny ingress NetworkPolicy template.

## v1.11.14 — 2026-09-27

- **Block mode in the Kubernetes YAML editors** — like the nginx, compose
  and libvirt configs. A "Text / Blocks" switch in the object YAML, "New
  object", Helm values and hub manifest windows. Blocks are the
  manifest's objects (between `---`), and inside them containers and init
  containers, volumes, Service ports, Ingress rules, ConfigMap keys; Helm
  values show their top-level keys.
- Clicking a block opens its text; "To the draft" replaces the block,
  "Delete" removes it. "+ container", "+ port", "+ rule", "+ key" insert
  a snippet with the right indentation, "+ Object" appends an object from
  a template after `---`. All of this changes the draft only: writing is
  the usual "Save", through the diff and `kubectl diff`.

## v1.11.13 — 2026-09-27

- **Hub: the first-column icon shows whether the host is reachable.**
  Green — the host answered the last poll, red — unreachable (the tooltip
  says when it last answered), grey — not polled yet. Before, the green
  check only meant "nkt is installed" and stayed green for an unreachable
  host. Nested machines too.
- **An unreachable host with no data no longer shows "no problems"** — it
  shows "no data". When data from an earlier successful poll exists, the
  finding counts are shown dimmed, marked "unreachable (data from …)".

## v1.11.12 — 2026-09-27

- **Hub: one YAML into several clusters.** The "Clusters" section has a
  "Manifests" card: a manifest (several objects separated by `---` are
  fine) and a choice of clusters. "Save" shows the text diff against the
  previous revision and `kubectl diff` for each cluster separately;
  writing runs `kubectl apply` on the first control plane of each, with
  the outcome per cluster (what was applied, where it failed).
- Manifests are kept on the hub with history: every application is a
  revision with the author, note and per-cluster outcome; a revision has
  a diff with the current one and "to the editor" to apply the earlier
  variant again (the rollback goes through the same diffs). In the
  clusters the object versions also enter the node's history
  (`k8s://…`), noting which hub manifest and who.

## v1.11.11 — 2026-09-27

- **Helm** — a new section in the cluster objects. Releases (with the
  shared namespace filter): chart, app version, status, revision.
  Actions: revision history and rollback to any of them, "values and
  upgrade" — the release's current values are edited in a window with a
  diff and go to `helm upgrade`, release uninstall. "Install chart" — a
  repository already added or a new one (or an `oci://` address), chart,
  version, release, namespace and values.
- Everything that changes the cluster runs as a background host job with
  the standard log window (`helm upgrade --install --wait`, `rollback`,
  `uninstall`), and the audit log records who did what. Viewing a
  release's values is audited too: they may contain passwords.
- No helm on the host — the "Install Helm" button puts the official
  archive into `/usr/local/bin` as a job. nkt keeps the Helm settings
  (repositories, cache) in its data directory; the values of a release
  installed from nkt are kept there too (0600) for the next upgrade.

## v1.11.10 — 2026-09-27

- **Kubernetes: findings.** On a control plane the scan takes a cluster
  summary, and "Findings" gets: a pod in CrashLoopBackOff/ImagePullBackOff,
  a pod Pending for more than 15 minutes, a NotReady node, a Deployment or
  StatefulSet missing replicas (all of them — high severity), a PVC
  Pending for more than 15 minutes, an expiring (under 30 days) or
  expired API server certificate, pods with privileged or hostNetwork,
  and NodePort and LoadBalancer ports open past the firewall: kube-proxy
  publishes them with nat rules, like Docker, and a ufw deny does not
  close them. Every finding has an AI bulb, like the others.
- **Kubernetes on the resource map:** Ingress → Service → pods → cluster
  node → the host machine the node runs on (by name or address). A
  service with no pods under its selector and an Ingress pointing at a
  missing service are highlighted.
- **CPU and memory** in the pod and node tables — from `kubectl top` when
  the cluster has metrics-server (k3s ships it by default).
- **An AI bulb on Warning events** in the "Events" section: what it means
  and what to do.

## v1.11.9 — 2026-09-27

- **Kubernetes: YAML with diff and history.** The row menu has "YAML":
  the object in an editor without service fields (managedFields,
  resourceVersion, status). Before saving the window shows two diffs — a
  text one and `kubectl diff` from the cluster (what will really change,
  defaults included); saving runs `kubectl apply`. Every edit is a
  version in the history (`k8s://namespace/kind/name`) with a diff
  against the current state and rollback, like configuration files and
  LXD.
- **"New object"** above the sections — a manifest from a template
  (Deployment + Service, Ingress, ConfigMap, CronJob or empty) with a
  name and namespace choice, the same cluster diff before saving; several
  objects separated by `---` are fine.
- Editing an object accepts a manifest of that object only (kind, name,
  namespace), and if the object changed while you were editing, the save
  is rejected. Secret YAML is not shown and never enters the history —
  values only via "reveal" with audit.

## v1.11.8 — 2026-09-27

- **Kubernetes: object actions.** Every cluster object row has an actions
  menu. For everyone — "Describe" (`kubectl describe` with events at the
  end). For an administrator: pods — logs (tail, follow, previous run,
  container choice) and a `kubectl exec` console, deletion; Deployments
  and StatefulSets — scaling, restart (`rollout restart`), rollout
  history and rollback to a revision; DaemonSets — restart and rollback;
  CronJobs — "run now", suspend and resume; nodes — cordon/uncordon and
  drain as a background job; namespaces — creation and deletion; the
  other kinds, Custom Resources included, can be deleted.
- Every action and every pod console login is recorded in the host audit
  log. The object's name and namespace are taken from the cluster
  listing — the request is only compared against them; deletion,
  rollback, 0 replicas and drain ask for confirmation.

## v1.11.7 — 2026-09-27

- **Kubernetes: cluster objects.** On a control plane the Kubernetes tab
  got sections: Workloads (Deployments, StatefulSets, DaemonSets, Jobs,
  CronJobs), pods, network (Services, Ingress), configuration (ConfigMaps,
  Secrets), storage (PVC, PV, StorageClass), nodes, namespaces and events.
  Every row has the key columns of its kind and a state color: replica
  readiness, pod phase, PVC status, node Ready, event type.
- **Custom Resources** — pick a kind from the cluster's CRDs; columns come
  from its own `additionalPrinterColumns`, state from the Ready condition.
- **Namespace filter** — in every section, shared by all and remembered;
  it does not apply to cluster-scoped kinds (nodes, PV, StorageClass).
  Plus a search by name and fields.
- **Secrets**: the list shows key names only; values come from the
  "reveal" button for an administrator and are recorded in the audit log.
  ConfigMaps — via the "data" button.
- Object actions, YAML with a diff, Helm and YAML from the hub are planned
  (TODO.md).

## v1.11.6 — 2026-09-27

- **The Android app plan is in TODO.md.** Today's assessment: the app
  builds and its models parse the current API, but it has none of the
  1.10–1.11 features. Five stages to parity with the web UI: the base
  (languages, jobs, APK in CI, emulator), containers and machines, host
  system, hub level, machine screen. Also deferred items from discussions:
  RDP via Guacamole, the machine console and screen size, hub scan
  vulnerabilities on the map.

## v1.11.5 — 2026-09-25

- **The AI answer window no longer opens empty.**
  - The program help bulb in "Configs" shows a saved answer right away.
    Before, an empty question field opened instead.
  - "Ask another question" opens the field with a list of earlier
    questions: a click shows that question's saved answer without a new
    model request.
  - A category bulb also lights up when answers to questions about it
    exist.
- **An answer without sections** — no "##" headings — is shown as plain
  text. **An empty answer** is shown as a message with "ask again" and
  "ask another question". The hub no longer returns `sections: null`,
  which crashed the window.

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
