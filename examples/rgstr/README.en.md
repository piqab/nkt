# rgstr — your own image registry and a hub plugin

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Check them yourself before deploying to production.

[rgstr](https://github.com/piqab/rgstr) is a lightweight OCI/Docker image
registry in Go (MIT): `docker push/pull`, projects with quotas, robots,
webhooks, retention and immutability policies, garbage collection, an audit
log, the `/ui` web interface; since 1.8 — LDAP/OIDC sign-in and backups, since
1.9 — a cluster on PostgreSQL and S3, since 1.10 — cache projects of other
registries and Trivy vulnerability scanning. A single instance keeps data on
disk, no database. In Kubernetes
it is run by the [rgstroperator](https://github.com/piqab/rgstroperator) —
see the “rgstr and rgstroperator in nkt” guide article.

## Deploying with a pipeline

Examples → “rgstr — image registry”, a host, the name in `site:`. The stack
is [`deploy/docker-compose.yml`](deploy/docker-compose.yml) (rgstr 1.10.1,
the `rgstr-data` volume), a site with HTTPS and `max_body: "0"` (image layers
stream with no size limit).

- Login is on; the hub creates the token secret (`generate:hex32`).
- The first administrator is `RGSTR_ADMIN_USER` with a **temporary**
  password from `.env` (Secrets → .env → history → Show). Sign in at
  `https://<domain>/ui` and change it — until then it works for neither
  `docker login` nor the plugin.
- Only an administrator can delete (`RGSTR_ADMINS`).
- **Vulnerability scanning** is on (`RGSTR_SCAN_ENABLED`, `true` by
  default): Trivy is in the image, its database (~70 MB) is downloaded from
  the internet (`mirror.gcr.io`, `ghcr.io` as a fallback) at the first scan.
  A scan takes a few hundred MB of memory; on a small host or without
  internet set `"false"`.
- **A Docker Hub cache** — `RGSTR_PROXIES: dockerhub=https://registry-1.docker.io`
  in the pipeline `env`; `docker pull <domain>/dockerhub/library/nginx`. A
  Docker Hub login and access token (`RGSTR_PROXY_DOCKERHUB_USERNAME`,
  `…_PASSWORD`) raise the pull limits; the token goes in Secrets → .env.
- **Backups to S3** — `RGSTR_BACKUP_TARGET: s3://bucket/prefix` and
  `RGSTR_BACKUP_S3_*` (endpoint, region, keys); the default schedule is
  `0 3 * * *` UTC, 7 kept.

## The plugin

Installed on the rgstr host (or on the hub machine if rgstr is reachable from
there): Plugins → Catalog → rgstr → Install on a host.

| Setting | What |
|---|---|
| `RGSTR_URL` | where the plugin reaches rgstr; on the rgstr host — `http://127.0.0.1:5000` |
| `RGSTR_USER`, `RGSTR_PASSWORD` | an rgstr administrator (not a temporary password); the password is a secret |
| `RGSTR_PUBLIC` | the registry address for docker and pipelines — `registry.example.com` |
| `RGSTR_CA_FILE` | your own CA (PEM) if rgstr HTTPS is signed by it |

**What it does:**

- **Summary** — version, login (and LDAP/OIDC), repositories, pulls, disk,
  garbage collection, scanning, backups and findings: login off, the registry
  over HTTP not on 127.0.0.1, temporary passwords, low disk, garbage
  collection off, failed or not run for long, no retention policies, open
  `/metrics`, a robot with push to all repositories, a `user` allowed to
  delete, scanning off or failing, critical vulnerabilities, backups off,
  failed or late, a cache reaching its upstream over HTTP.
- **Repositories and tags** — the worst vulnerability severity, delete a tag
  (not immutable ones), collect garbage.
- **Vulnerabilities** of an image — severity, CVE, package, installed and
  fixed versions; “Scan” queues the image.
- **Projects** — create (public, a quota in GB, a **cache** of another
  registry, blocking pulls of vulnerable images), change the pull blocking,
  delete an empty one.
- **Backups** — target, schedule, last success and error, archives in
  storage, “Back up now”.
- **Robots** — create (a project or `*`, pull or push, a lifetime), issue a
  new secret, delete. The secret goes **once** to the browser window, or into
  a pipeline `.env`, or straight as **the pipeline's registry key**
  (`env: @registry`: the address is `RGSTR_PUBLIC`, the login is the robot's
  name): the pipeline then pulls its images from rgstr by itself.
- **Policies** — retention and immutability: edit as JSON text with a diff and
  history in the hub, apply the retention policy now.
- **Users, webhooks (test), audit log.**

**Script commands:**

```
rgstr gc
rgstr retention run
rgstr tags shop/api
rgstr tag-delete shop/api 1.0
rgstr project shop quota 20
rgstr project dockerhub proxy https://registry-1.docker.io block critical
rgstr scan shop/api 1.0
rgstr backup run
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

**MCP:** `rgstr_summary`, `rgstr_repos`, `rgstr_tags`, `rgstr_projects`,
`rgstr_robots`, `rgstr_policies`, `rgstr_tag_delete`, `rgstr_gc_run`,
`rgstr_retention_run`, `rgstr_project_create`, `rgstr_robot_create`,
`rgstr_vulns`, `rgstr_scan_run`, `rgstr_backups`, `rgstr_backup_run`,
`rgstr_project_block`… — an
agent gets a robot secret only into a pipeline.

## Security

- The plugin talks to rgstr as an administrator (Basic) — the password lives
  only in `/etc/nkt-plugins/rgstr.env` (0600), never in logs or answers.
- Every name (repository, tag, project, robot) is checked before the request;
  a cache upstream is only `http(s)://host[/path]`, with no login in it. The
  plugin takes no upstream login: it is set in the rgstr `/ui` or in
  `RGSTR_PROXY_*`.
- rgstr shows a robot secret once; the plugin does not keep it.
- Every change supports `dry_run` (garbage collection and the retention
  policy in a dry run ask rgstr what would happen without deleting anything).
