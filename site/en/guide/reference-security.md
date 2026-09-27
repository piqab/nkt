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
- One route is available through the tunnel — `POST /hooks/{id}`. The
  hub's UI and API are not reachable via the edge.
- The service on the VPS — a system user `nkt-edge`, the only capability
  is binding 443; certbot issues the certificate.

Details — [nkt-edge](/en/guide/edge#security-model).

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
