# What's new in NetKnownsThat

Short notes per version in English — what the user sees, not the list of
commits (that is [CHANGELOG.md](CHANGELOG.md)). The Russian original is
[WHATSNEW.md](WHATSNEW.md); both files get a section with every bump of
`VERSION`, headed `## vX.Y.Z` exactly like the release tag. On a release
`.github/workflows/release.yml` appends the English sections to the
release body after a `<!-- en -->` marker, and the hub shows the reader
their language in "About" when a newer version appears. Newest first.

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
