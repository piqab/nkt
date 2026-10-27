---
title: Security
---

# Security

::: danger The main point
Access to nkt equals root on the host; access to the hub equals root on
all its hosts. The UI is not exposed to the internet without a separate
authentication layer (VPN, SSO, an address allow-list). What to open and
what not — [Ports and access](/en/guide/ports).
:::

## Login and sessions

- Passwords — argon2id (64 MiB, t=3, p=2), irreversible; minimum length
  10 characters. A "current" password can't be recovered — only a new
  one issued (`nkt passwd`).
- Sessions — random revocable tokens in SQLite; cookies `HttpOnly`,
  `SameSite=Lax`, `Secure` by default. A password change ends all
  sessions of that account.
- After five failed attempts, login from an address is blocked with a
  growing delay.
- Roles: `viewer` — read-only, `admin` — changes. The global switch
  `NKT_ALLOW_MUTATIONS=false` applies to the TUI too. You can't demote,
  disable or delete your own account.

## Requests from other sites

State-changing requests are accepted only from the application's own
origin: `Sec-Fetch-Site` must be `same-origin` (or `none`), and when it's
absent, `Origin` must match `Host`. Extra origins — `NKT_CORS_ORIGINS`.
Exceptions — deployment webhooks (`/api/hub/hooks/`, access by signature)
and port forwards (`/k8s/pf/`, its own token in the path).

## Kubernetes port forwards

"Open in browser" on a pod or service serves the application from a
**separate port** (8446, `NKT_FORWARD_ADDR`) — a different origin, so the
application's JavaScript can't see nkt's cookies and API. The link is
one-time, with a token; a forward is closed with a button. nkt's cookies
aren't passed to the application, and the application's cookies are
rewritten to the forward's path. If the separate port is off (`off`),
the application is served by the UI's path in a sandbox
(`Content-Security-Policy: sandbox`).

## What can be changed

- Config editing is limited to a list of directories (nginx, haproxy,
  Caddy, systemd, netplan, sshd, sysctl, cron roots, the listed compose
  files, `/etc/libvirt/qemu`). Paths with `..` are rejected.
- Before writing, the service itself validates the config (`nginx -t`,
  `haproxy -c`, `caddy validate`, `sshd -t`…); on failure the file is
  restored. Optimistic locking by SHA-256: a file that changed on disk
  after it was opened isn't silently overwritten.
- Service and firewall actions accept values only from fixed lists — a
  name or action can't be injected into a command. Request values reach
  command lines or paths only after a strict pattern check.
- Deleting a ufw rule is matched against the exact text the operator
  saw: numbers shift after every change, and deleting the "wrong" rule
  can cut off SSH.
- Every changing action goes into the audit log with the user, result
  and command output.

## The systemd sandbox

The units in `deploy/` narrow privileges: `ProtectSystem=strict` with an
explicit list of writable directories, a reduced `CapabilityBoundingSet`,
`SystemCallFilter=@system-service`, `NoNewPrivileges`.

**The deliberate exception** — the web terminal, OS package upgrades,
installs from the UI and self-update over the fallback channel. They run
through `systemd-run` in a separate unrestricted unit, not as a child of
nkt: otherwise a root shell or `apt-get`'s privilege drop would hit the
restrictions that protect the daemon itself. The daemon's restrictions
limit the blast radius of compromising **the daemon**, not the actions
of an authenticated administrator who explicitly asked for root. It only
works when nkt runs as a unit and `systemd-run` is available.

`systemd-run` needs a live D-Bus. Where there is none (minimal images,
Debian 11 without dbus), the fallback is `CAP_SYS_ADMIN` in the unit and
`nsenter --mount` into PID 1's namespace; `RestrictNamespaces` is
narrowed to exactly `mnt`. The "Terminal" page says if neither path is
available and offers to install dbus.

Only the server builds the commands for these sessions; names from a
request (container, package, pod) are validated and matched against the
inventory. When nkt does not run as a unit and starts the program
directly, its name comes from a closed list (shells, `apt-get`, `snap`,
`docker`, `podman`, `lxc`, `virsh`, `kubectl`, `k3s`, `tmux`, `btop` and
so on); anything else is not started.

The web terminal is off by default (`NKT_TERMINAL_ENABLED`); a host under
a hub has its own checkbox in its form.

## Hub

- **Host secrets** (SSH passwords and keys, host administrator accounts,
  fallback channel tokens, pipeline access, the model API key) are stored
  encrypted with AES-256-GCM under the master key (`NKT_HUB_MASTER_KEY`
  or `hub.key` in the data directory). They can't be shown from the UI —
  only replaced.
- **The host SSH key** is remembered on the first connection and required
  on every following one; a substituted key is an error, not a login.
- **The fallback channel** — TLS with the host certificate's pinned
  fingerprint and a separate per-host token; five wrong attempts — the
  address is blocked.
- **Management operations** go through an SSH tunnel to the host's own
  API, which applies its own rules (`NKT_ALLOW_MUTATIONS`, role); the hub
  doesn't bypass them.
- **The export** carries the master key, so it is encrypted with a
  password in the browser (PBKDF2 + AES-256-GCM); without a password —
  only through a separate confirmation. `nkt hub delete` shreds the key
  and the database.
- **Kubernetes secrets**: lists show only key names; values — with the
  "show" button for an administrator, every reveal goes into the audit
  log.
- Secrets aren't passed in job parameters or command lines — only
  through a child process's environment or stdin.

## Deployments and webhooks

- A webhook is accepted only with a valid signature: HMAC-SHA256 (GitHub,
  Gitea, Forgejo), a secret token (GitLab) or the nkt signature with a
  timestamp no older than 5 minutes. A replayed delivery is rejected. An
  unknown address and a wrong signature give the same `401`.
- A webhook chooses nothing itself: what goes where is set on the hub;
  only the branch, commit and tag are taken from the request, each
  checked against a pattern.
- Repository and registry access is stored encrypted and passed to git
  through the environment, not through the URL or arguments.

## nkt-edge

- The VPS has no database, no pipeline secrets, no host access — a
  compromised edge can only send requests the hub rejects without a
  signature.
- The hub initiates the tunnel; TLS 1.3, the hub trusts **exactly** its
  edge's certificate (the only verification root), then a token.
- Through the tunnel the hub serves only this edge's roles: "webhooks"
  is `POST /hooks/{id}`; "API" is **signed** token requests from tokens
  with the "through nkt-edge" box (Bearer, cookies, password login and
  websockets do not pass: the token secret never reaches the VPS, and a
  signature works once); "callbacks" is `POST /callbacks/slack/…` with a
  Slack signature; "outside checks" is only streams the hub opens. The UI
  is never reachable via an edge.
- The service on the VPS — a system user `nkt-edge`, the only capability
  is binding 443; certbot issues the certificate.

Details — [nkt-edge](/en/guide/edge#security-model).

## API tokens

- A token opens not the whole API but a list of automation calls; hub
  management (accounts, tokens, export, updates, edge, bots), the
  terminal, console, files, backups, configs and websockets are closed to
  it with any role; so are the host's Kubernetes sections: a token reaches
  clusters only through its own calls (manifests into its namespaces, own
  namespaces and clusters — below, “Client machines and clusters”).
- The read role reads and the admin role also acts; a host and group
  scope narrows both lists and actions (a deploy only of a pipeline whose
  hosts are all in scope; a dry run only of a saved pipeline).
- The secret is stored encrypted with the master key (HMAC signatures are
  checked with it) and is shown once; a signature lives 5 minutes, the
  nonce is single-use, and the body is signed.
- The token's address list is checked against the real connection
  address; the hub trusts proxy headers only from loopback (your own
  reverse proxy, the edge tunnel). Five invalid tokens from an address
  mean a pause.
- Creating, editing, a new secret, revoking and every call go to the
  audit log as `token:<name>`.

## Client machines and clusters

A token “by permission” can hand machines, namespaces and clusters to
outside clients. What holds them:

- **The machine's network.** A token machine (KVM and LXD) lives in the
  token's own NAT network `nkt-t<number>` on the host; the token does not
  choose the network or bridge. The nftables table `nkt_isolation` is set
  before the machine first starts and re-checked every 10 minutes: to the
  host — only DNS, DHCP and ping; no private networks (the host's LAN,
  libvirt, LXD and other token networks, CGNAT, the cloud metadata
  address) and no IPv6; inbound — only replies and what the host
  forwards itself (pool ports, Traffic Fly); outbound port 25 is closed
  unless mail is explicitly allowed for the token. Another token's network
  cannot reach a machine through a host port — checked on live traffic in
  the tests (`NKT_NETNS=1`).
- **Other machines** are invisible to the token in lists, monitoring and
  scripts; machine actions apply only to its own (the token's usage).
  Images come only from the catalog: a file on the host's disk (`host:…`)
  is not allowed, other machines' disks live there too. Own user-data
  cannot set the account, keys or login.
- **Resources** — the token's limits: machine count, vCPU, memory, disk
  per machine and in total, pool ports, snapshots, Fly routes, namespaces,
  clusters; the client sees them itself (`/api/hub/tokens/self/usage`, MCP
  `my_usage`).
- **LXD** — a container shares the kernel with the host: weaker isolation
  than a KVM machine. A token container is unprivileged and without
  nesting regardless of the LXD profile; for outside clients machines
  (KVM or an LXD `vm`) are safer.
- **Own namespace in a shared cluster** — only in a cluster with Cilium
  (it enforces network policies) and Kubernetes 1.30+. The client works
  with its own kubeconfig directly, so the rules live in the cluster:
  - Pod Security `restricted` — no privileges, no node network, ports or
    paths, no extra capabilities;
  - admission policies (ValidatingAdmissionPolicy) on client namespaces:
    Service — only ClusterIP and without `externalIPs` (hijacking other
    addresses, CVE-2020-8554), no `NodePort` and `LoadBalancer` (ports on
    every node and addresses from the administrator's pool); Ingress and
    HTTPRoute — only names in the token's domain, no domain — none;
    Gateway — not allowed; the client's accounts cannot change network
    policies;
  - network policies: ingress — from its own namespace, kube-system and
    Cilium ingress; egress — cluster DNS, its own namespace and the
    internet without private networks, the metadata address and (unless
    allowed) port 25;
  - quota and LimitRange from the token's limits; the client's account has
    the `edit` role only in its namespace; the kubeconfig is issued once,
    through the API (not MCP), and a reissue disables the old one;
  - operator resources (CRDs) — only those the administrator ticked in
    “Client resources” (aggregated into `edit`; Kubernetes, RBAC,
    admission, networking, Cilium and Longhorn groups — never). What the operator
    creates for a client resource passes the same admission policies; the
    hub checks an rgstroperator `Registry` up front (Service — ClusterIP,
    ingress — within the token's domain).
  The hub checks the same rules itself for the token's manifests, helm and
  scripts — an early, clear refusal.
- **Own cluster** — k3s with Cilium on the token's machines in its
  network: the kernel and nodes are not shared with other clients. The API
  goes through a host pool port, the kubeconfig (`cluster-admin` of the
  own cluster) is issued once.

**Bandwidth and disk.** Token machines have network (Mbit/s), IOPS and
disk MB/s limits (the token's own or the hub's defaults); snapshots reserve
the machine's disk in the token's disk limit.

**Limits.** Bandwidth and IO in Kubernetes are not limited (they cannot be
imposed without an admission mechanism, and the client works directly).
Everything a machine does on the internet comes from the host's address —
port 25 is closed, the rest is up to the host owner and the agreement with
the client.

## Outgoing webhooks and bots

- Outgoing webhooks are signed with HMAC using the recipient's secret (as
  nkt's incoming webhooks are); redirects are not followed, and no more
  than 300 bytes of a response body are read.
- The Telegram bot reaches Telegram by itself (long polling), so no way
  into the hub is needed; Slack sends commands by itself, and every
  request is checked with the Slack signature and its time (no older than
  5 minutes).
- Bots answer only the chats and channels on the list; actions need the
  admin role, an allowed person and a confirmation button (which lives 10
  minutes and works only for whoever ran the command).
- Bot tokens and signing secrets are stored encrypted with the master key
  and are never shown in error logs.

## Model analysis (AI)

Off by default. Passwords, tokens, keys, password hashes and credentials
in URLs are always stripped from requests to the model; by default
names, addresses and domains are replaced with aliases. "Show request"
shows exactly what was sent. The model executes nothing — commands are
only shown.

## CI checks

`.github/workflows/security.yml` on every push, pull request and weekly:
govulncheck and gitleaks (blocking), CodeQL for Go and TypeScript, gosec
and Trivy (dependencies, `deploy/` manifests, Dockerfile) — in Security →
Code scanning. Dependabot opens grouped weekly PRs with dependency
updates.
