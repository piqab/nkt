# rgstr — your own image registry and a hub plugin

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Check them yourself before deploying to production.

[rgstr](https://github.com/piqab/rgstr) is a lightweight OCI/Docker image
registry in Go (MIT): `docker push/pull`, projects with quotas, robots,
webhooks, retention and immutability policies, garbage collection, an audit
log, the `/ui` web interface. Data lives on disk, no database. In Kubernetes
it is run by the [rgstroperator](https://github.com/piqab/rgstroperator) —
see the “rgstr and rgstroperator in nkt” guide article.

## Deploying with a pipeline

Examples → “rgstr — image registry”, a host, the name in `site:`. The stack
is [`deploy/docker-compose.yml`](deploy/docker-compose.yml) (rgstr 1.7.0,
the `rgstr-data` volume), a site with HTTPS and `max_body: "0"` (image layers
stream with no size limit).

- Login is on; the hub creates the token secret (`generate:hex32`).
- The first administrator is `RGSTR_ADMIN_USER` with a **temporary**
  password from `.env` (Secrets → .env → history → Show). Sign in at
  `https://<domain>/ui` and change it — until then it works for neither
  `docker login` nor the plugin.
- Only an administrator can delete (`RGSTR_ADMINS`).

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

- **Summary** — version, login, repositories, pulls, disk, garbage collection
  and findings: login off, the registry over HTTP not on 127.0.0.1,
  temporary passwords, low disk, garbage collection off, failed or not run
  for long, no retention policies, open `/metrics`, a robot with push to all
  repositories, a `user` allowed to delete.
- **Repositories and tags** — delete a tag (not immutable ones), collect
  garbage.
- **Projects** — create (public, a quota in GB), delete an empty one.
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
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

**MCP:** `rgstr_summary`, `rgstr_repos`, `rgstr_tags`, `rgstr_projects`,
`rgstr_robots`, `rgstr_policies`, `rgstr_tag_delete`, `rgstr_gc_run`,
`rgstr_retention_run`, `rgstr_project_create`, `rgstr_robot_create`… — an
agent gets a robot secret only into a pipeline.

## Security

- The plugin talks to rgstr as an administrator (Basic) — the password lives
  only in `/etc/nkt-plugins/rgstr.env` (0600), never in logs or answers.
- Every name (repository, tag, project, robot) is checked before the request.
- rgstr shows a robot secret once; the plugin does not keep it.
- Every change supports `dry_run` (garbage collection and the retention
  policy in a dry run ask rgstr what would happen without deleting anything).
