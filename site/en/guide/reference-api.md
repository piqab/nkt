---
title: API
---

# API

Everything visible in the UI is available as a JSON API with the same
authentication and permissions. Paths are under `/api`, errors —
`{"error": "…"}` in the request language (`Accept-Language`).

## Login

```bash
curl -s -c jar -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}' http://127.0.0.1:8077/api/auth/login
curl -s -b jar http://127.0.0.1:8077/api/overview
curl -s -b jar http://127.0.0.1:8077/api/findings
```

The session is the `nkt_session` cookie; WebSockets authenticate with the
same cookie. Reading is available to any role, changes — to `admin` with
`NKT_ALLOW_MUTATIONS=true`. State-changing requests from a browser are
accepted only from the application's own origin (see
[Security](/en/guide/reference-security#requests-from-other-sites)); from
`curl` and scripts — as is.

For automation the hub has **API tokens** (Bearer or a signed request),
with a role and a host and group scope: see [API and
tokens](/en/guide/hub-api).

`GET /api/health` — no login: whether the process is alive, the version.

## Through the hub

The same host paths are available on the hub with a prefix:

- `/api/hosts/{id}/…` — proxied to host `{id}` over SSH (or the fallback
  channel);
- `/api/hosts/local/…` — the "localhost" row, the hub's own machine.

`/api/auth/*` never gets the prefix — login is always to the hub itself.
For example, `GET /api/hosts/3/services` — the services of host 3.

## Host

| Group | Paths | What |
|---|---|---|
| Login | `/auth/*` | Login, logout, `me`, changing your own password |
| Summary | `/overview`, `/inventory*`, `/findings`, `/topology`, `/snapshots`, `/changes` | Dashboard, host snapshot, findings, map, scan history |
| Services | `/services*` | State, start/stop/restart/reload, validation, install |
| Containers | `/containers*`, `/podman/*`, `/images*` | Docker, Podman, images |
| Kubernetes | `/k8s/*` | Nodes, objects, actions, YAML with a diff, Helm, port forwards, upgrades |
| LXD | `/lxd/*` | Instances, snapshots, configuration, networks, images, pools, logs, screen |
| libvirt | `/vms/*`, `/vm/*` | Machines, the creation wizard, images, networks, VNC/SPICE screen |
| Guests | `/guests/{kind}/{name}/*` | Guest login and password |
| Backups | `/backups*` | List, create, restore, download |
| Jobs | `/jobs*` | List (`?q=&status=&kind=&limit=&offset=&order=asc`, the response carries `total` and `kinds`), log, log stream, cancel, retry (`/jobs/{id}/retry`) |
| Configs | `/configs/*` | Files, blocks, validated write, versions, diff, rollback |
| Profiles | `/profiles*` | Desired state, plan, apply |
| Firewall | `/firewall/*` | ufw and firewalld rules, install |
| Certificates | `/certificates*` | Certificates, issuance, renewal, self-signed |
| Monitoring | `/monitor/*` | Availability targets, usage, heatmaps, outages |
| Security | `/vulnerabilities*`, `/malware`, `/clamav/*` | Vulnerabilities, malware, ClamAV |
| System | `/system/*`, `/updates*`, `/disks*`, `/files*`, `/hardware`, `/network*`, `/interfaces` | Packages, updates, disks, files, hardware, network |
| Accounts | `/users*`, `/os-users*`, `/audit` | nkt and OS users, audit log |
| WebSocket | `/terminal/ws`, `/console/ws`, `/jobs/{id}/ws`, `/vms/{name}/vnc/ws`, `*/spice/ws`, `*/logs/ws` | Terminal, consoles, job log, screen, logs |

Long operations (installs, backups, machine creation, upgrades) return a
job ID; the log is at `GET /api/jobs/{id}` and the stream at
`/api/jobs/{id}/ws`.

## Hub

Under `/api/hub/`:

| Group | Paths | What |
|---|---|---|
| Hosts | `/hub/hosts`, `/hub/hosts/{id}`, `…/install`, `…/install/cancel`, `…/start`, `…/stop`, `…/pubkey`, `…/forget-hostkey`, `…/sudo/remove`, `…/group`, `…/apt-proxy` | The host registry, installation, service control |
| Groups | `/hub/groups*` | Groups, renaming, group profile |
| Machines | `/hub/vm/provision`, `/hub/hosts/{id}/vm*` | Creating, finding and importing machines, access checks |
| Scripts | `/hub/scripts*` | Scripts, check, run, versions, help |
| Clusters | `/hub/clusters*`, `/hub/cluster-images*`, `/hub/cluster-presets*`, `/hub/k8s-versions` | Creation, dry run, workers, resume, upgrade, kubeconfig, images, presets |
| Manifests and Helm | `/hub/k8s/manifests*`, `/hub/k8s/helm/install`, `/hub/k8s/findings` | One YAML or release into several clusters, findings across clusters |
| Deployments | `/hub/pipelines*` | Pipelines, access, webhook secret, deploy, rollback, history |
| API tokens | `/hub/tokens*`, `/hub/jobs/{id}*` | Tokens (from the browser only), a hub job and its log |
| Webhook | `POST /hub/hooks/{id}` | No session, access by signature |
| nkt-edge | `/hub/edge`, `/hub/edge/install`, `/hub/edge/uninstall` | State, configuration, installation, removal from the VPS |
| Alerts | `/hub/events*` | Log, mark seen, settings |
| About | `/hub/version*`, `/hub/update`, `/hub/rollback`, `/hub/vulndb*`, `/hub/clamdb*`, `/hub/aptcache*` | Version, update, rollback, databases, cache |
| AI | `/hub/ai*` | Settings, test, analysis, answers, instructions |
| Export | `/hub/export`, `/hub/import` | Registry export and import |

Example — deploy pipeline 1 with a tag:

```bash
curl -s -b jar -H 'Content-Type: application/json' \
  -d '{"tag":"v1.2.3"}' http://127.0.0.1:8077/api/hub/pipelines/1/deploy
```

## Command line

| Command | What it does |
|---|---|
| `nkt serve` (or just `nkt`) | the web UI and API |
| `nkt scan` | a one-off scan to JSON; exit code 2 on critical findings |
| `nkt tui` | the terminal UI |
| `nkt users`, `nkt passwd` | accounts without the web UI |
| `nkt hub` | the hub; `nkt hub import` — restore the registry, `nkt hub delete` — wipe all hub data |
| `nkt version` | version |
