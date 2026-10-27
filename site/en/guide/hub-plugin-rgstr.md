---
title: 'rgstr and rgstroperator in nkt'
---

# rgstr and rgstroperator in nkt

[rgstr](https://github.com/piqab/rgstr) is a lightweight OCI/Docker image
registry in Go (MIT):

- `docker push/pull`;
- projects with quotas, robots, webhooks;
- retention and immutability policies;
- garbage collection, an audit log and the `/ui` web interface.

Since 1.10 it also has:

- **cache projects** of Docker Hub, GHCR and any registry (pull-through):
  Docker Hub limits stop mattering, and images stay available when the
  upstream is down;
- **vulnerability scanning** with built-in Trivy, and blocking pulls of
  vulnerable images per project;
- **backups** on a schedule to S3 or a directory, and `rgstr restore`;
- **LDAP / Active Directory and OIDC sign-in** with roles from groups;
- **a cluster** — several processes on PostgreSQL and S3.

A single instance keeps everything on disk, with no database.
[rgstroperator](https://github.com/piqab/rgstroperator) installs rgstr into
Kubernetes as a single `Registry` resource.

The article is written for rgstr 1.10.1 and rgstroperator 0.5.1.

Both are third-party open source: nkt does not modify them, it deploys and
runs them. The article covers three paths:

1. **A registry on a host** — a pipeline from "Examples" plus the hub's
   `rgstr` plugin.
2. **A registry in a cluster** — the operator from an administrator, a
   `Registry` resource.
3. **A client's registry in their own namespace** — the same `Registry`
   from a token, published through Traffic Fly.

The general plugin model is in [Plugins](/en/guide/hub-plugins). How such a
plugin is written: [Writing a plugin step by step](/en/guide/plugin-step-by-step),
which walks through the tutorial `rgstr-lite`.

## 1. A registry on a host

### Deploy

"Pipelines" → "Examples" → **"rgstr — image registry"** → host and domain.
The example is
[`examples/rgstr`](https://github.com/piqab/nkt/tree/main/examples/rgstr):

```yaml
action: compose
compose:
  file: examples/rgstr/deploy/docker-compose.yml
  project: rgstr
  hosts: [web1]
  env:
    RGSTR_DOMAIN: registry.example.com
    RGSTR_AUTH_SECRET: generate:hex32
    RGSTR_ADMIN_USER: admin
    RGSTR_ADMIN_PASSWORD: generate:password
    # optional:
    # RGSTR_SCAN_ENABLED: "false"
    # RGSTR_PROXIES: dockerhub=https://registry-1.docker.io
    # RGSTR_BACKUP_TARGET: s3://backups/rgstr
  site:
    domains: [registry.example.com]
    service: rgstr
    port: 5000
    max_body: "0"
```

- With `generate:`, the hub creates the token secret and the first
  administrator's password. Both are in the pipeline's "Secrets" → ".env".
- `max_body: "0"` streams image layers with no size limit and no proxy
  buffering.
- HTTPS comes from the host site: docker refuses a registry without TLS
  unless it is on loopback.

The image is `ghcr.io/piqab/rgstr:1.10.1`; data lives in the `rgstr-data`
volume.

**First sign-in.** `RGSTR_ADMIN_PASSWORD` is a **temporary** password.

1. Open `https://registry.example.com/ui` and sign in as `admin`.
2. Change the password.

Until you do, `docker login` and the API refuse that password: rgstr
rejects a temporary password over Basic. The plugin cannot sign in either.

### Scanning, cache, backups

All of it is set in the pipeline `env`: the example compose passes these
variables to rgstr, and empty ones mean rgstr's defaults.

**Vulnerability scanning** is on by default (`RGSTR_SCAN_ENABLED: "true"`).

- Trivy is already in the image. Its database (about 70 MB) is downloaded
  from the internet (`mirror.gcr.io`, `ghcr.io` as a fallback) at the first
  scan and kept in the volume.
- Images are scanned on push and again once a day (the database learns of
  new vulnerabilities).
- A scan takes a few hundred MB of memory. On a small host or without
  access to them, set `"false"`; otherwise every scan ends in an error,
  which the plugin reports as a finding.
- Blocking pulls of vulnerable images is a project setting: “Block pulls
  with critical vulnerabilities” and so on. The client gets `403 denied`
  with the reason.

**A Docker Hub cache:**

```yaml
    RGSTR_PROXIES: dockerhub=https://registry-1.docker.io,ghcr=https://ghcr.io
    RGSTR_PROXY_DOCKERHUB_USERNAME: me              # optional: higher pull limits
    RGSTR_PROXY_DOCKERHUB_PASSWORD: dckr_pat_…      # a Docker Hub access token
```

```sh
docker pull registry.example.com/dockerhub/library/nginx:1.27   # = docker.io/library/nginx:1.27
```

- Tags are rechecked upstream every 5 minutes with a HEAD request; layers
  are downloaded once.
- Pushes to a cache project are refused; retention policies clean up old
  images.
- After the deploy the Docker Hub token is in the pipeline's "Secrets" →
  ".env". A cache can also be created in the plugin or in `/ui` (a project
  with an upstream address); its upstream login is then set in the rgstr
  `/ui`.

**Backups to S3:**

```yaml
    RGSTR_BACKUP_TARGET: s3://backups/rgstr
    RGSTR_BACKUP_S3_ENDPOINT: https://storage.yandexcloud.net
    RGSTR_BACKUP_S3_REGION: ru-central1
    RGSTR_BACKUP_S3_ACCESS_KEY: …
    RGSTR_BACKUP_S3_SECRET_KEY: …
    # RGSTR_BACKUP_SCHEDULE: "0 3 * * *"   # UTC, the default
    # RGSTR_BACKUP_KEEP: "7"
```

- A backup is taken while the registry runs and is consistent: garbage
  collection waits meanwhile.
- The archive is `rgstr-backup-<time>.tar`; ones beyond `KEEP` are deleted.
- To restore (see the rgstr README): stop the stack, run
  `docker compose run --rm rgstr restore -force latest`, start the stack.

**LDAP, Active Directory, OIDC** — the `RGSTR_LDAP_*` and `RGSTR_OIDC_*`
variables
([the rgstr README](https://github.com/piqab/rgstr#ldap-active-directory-and-single-sign-on)).
The example compose does not pass them: for directory sign-in use your own
compose with these variables. SSO users run `docker login` with a **CLI
secret** from `/ui` — docker has no browser sign-in.

### The plugin

"About" → "Plugins" → "Catalog" → **rgstr** → "Install on host". Pick the
rgstr host, or any host that can reach rgstr.

| Setting | What |
|---|---|
| `RGSTR_URL` | where the plugin reaches rgstr; on the rgstr host — `http://127.0.0.1:5000` |
| `RGSTR_USER`, `RGSTR_PASSWORD` | an rgstr administrator whose password is **already changed**; the password is a secret (the service environment file, `0600`) |
| `RGSTR_PUBLIC` | the address for docker and pipelines — `registry.example.com` |
| `RGSTR_CA_FILE` | the PEM of your own CA if rgstr HTTPS is signed by it |

Plugin 1.1 works with older rgstr too: it simply does not show what the
registry lacks (scanning arrived in 1.10, backups in 1.8).

The `nkt-plugin-rgstr` service listens on 127.0.0.1:9280, without Docker.

### What the hub shows

- **Summary** — version, login (and LDAP/OIDC), repositories, pulls, disk,
  garbage collection, scanning, backups, and **findings**:
  - login is off;
  - rgstr is reachable over HTTP and not on loopback;
  - a user still has a temporary password;
  - the `user` role has delete rights;
  - a robot can push to every repository (`*` + push);
  - disk space is low;
  - garbage collection is off, stale or failed;
  - there is no retention policy;
  - `/metrics` is open without login;
  - scanning is off or ends in errors (for example, the Trivy database is
    unreachable);
  - images have critical vulnerabilities (the first five tags);
  - built-in backups are not configured, the last one failed or is late
    (over a day on a daily schedule);
  - a cache reaches its upstream over HTTP.
- **Repositories → Tags** — digest, size, platforms, push time, the
  immutable flag and **vulnerabilities** by severity. Deleting a tag asks
  for confirmation and works as a dry run.
- **Vulnerabilities** of a tag — severity, CVE, package, installed and
  fixed versions, the critical ones first. "Scan" queues the image in rgstr.
- **Projects** — create (public, quota in GB, a **cache** of another
  registry, blocking pulls of vulnerable images), change the pull blocking,
  delete an empty one. The plugin accepts an upstream address only as
  `http(s)://host[/path]`, with no login in it.
- **Backups** — target, schedule, last success and size, error, archives
  in storage; "Back up now" (the backup runs in the background).
- **Robots** — create (a project or `*`, pull or pull+push, lifetime),
  issue a new secret, delete. Where the secret goes is described below.
- **Policies** — retention and immutability as JSON text, edited in a
  modal with a diff and version history. "Apply retention" runs now or as a
  dry run.
- **Users**; **webhooks** ("Test" sends a ping); the **audit log**, last
  200 entries.

### Robots as pipeline registry keys

A pipeline that pulls images from rgstr needs a key in "Secrets" →
"Registries". The plugin puts it there itself, and nobody sees the secret.

**In the window:** "Robots" → "Create" → set "Straight into a pipeline"
and "Variable name" = `@registry`. The hub writes the registry key into
the pipeline's registries: address `registry.example.com`, login
`robot$ci-shop`, and the secret.

- A key with the same address is replaced, and its CA is kept.
- The write has a version history.
- The action log does not include the value.

A plain name such as `REGISTRY_TOKEN` instead of `@registry` writes the
value into the pipeline `.env`.

**Script:**

```
rgstr project shop quota 20
rgstr robot ci-shop project shop to-pipeline shop env @registry
deploy shop
```

**API.** Use a token with the "plugin rgstr" and "pipeline.create"
actions, within its limits:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"robot_create","args":{"name":"ci-shop","project":"shop","to_pipeline":"shop","env":"@registry"}}' \
  https://hub.example.com/api/hub/plugins/rgstr/call
```

**MCP:** call `rgstr_robot_create` with `to_pipeline` and `env` — either
`@registry` or a variable name.

- An agent never receives the value.
- Without these arguments the operation is not available to it.

The hub checks permissions **before** calling the plugin. If the secret
has nowhere to go, the robot is not created.

### Scripts, API, MCP

Commands use the word `rgstr`. Help is in the plugin section and in
[script commands](/en/guide/script-commands#cmd-plugin):

```
rgstr repos
rgstr tags shop/web
rgstr tag-delete shop/web 1.0.0
rgstr gc
rgstr retention run
rgstr project shop quota 20
rgstr project dockerhub proxy https://registry-1.docker.io block critical
rgstr scan shop/web 1.0
rgstr backup run
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

`project` takes the words `quota`, `proxy` and `block` (`critical`, `high`,
`medium`, `low`) in any order.

- "Check" asks the plugin whether the words are right.
- A dry run only reports what would be done.

Nightly cleanup is a saved script:

```
rgstr retention run
rgstr gc
```

A backup before updating the registry:

```
rgstr backup run
deploy rgstr
```

Run it on a schedule or through the API:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"dry_first":true}' https://hub.example.com/api/hub/scripts/7/run
```

Any plugin operation is available through `POST /api/hub/plugins/rgstr/call`:

```sh
# what is stored
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"repos"}' https://hub.example.com/api/hub/plugins/rgstr/call
# what garbage collection would free — nothing deleted
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"gc_run","dry_run":true}' https://hub.example.com/api/hub/plugins/rgstr/call
# the vulnerabilities of an image
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"vulns","args":{"repo":"shop/web","tag":"1.0"}}' https://hub.example.com/api/hub/plugins/rgstr/call
# block pulls of a project's images with critical vulnerabilities
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"project_block","args":{"name":"shop","severity":"critical"}}' https://hub.example.com/api/hub/plugins/rgstr/call
```

**MCP tools:**

- reads, for any token: `rgstr_summary`, `rgstr_repos`, `rgstr_tags`,
  `rgstr_vulns`, `rgstr_projects`, `rgstr_robots`, `rgstr_users`,
  `rgstr_policies`, `rgstr_backups`;
- changes, for a token with "plugin rgstr": `rgstr_tag_delete`,
  `rgstr_gc_run`, `rgstr_retention_run`, `rgstr_scan_run`,
  `rgstr_backup_run`, `rgstr_project_create` (with `proxy_url` and
  `block_severity`), `rgstr_project_block`, `rgstr_project_delete`,
  `rgstr_robot_create`, `rgstr_robot_secret`, `rgstr_robot_delete`.

An agent can, for example, find tags with critical vulnerabilities
(`rgstr_repos` → `rgstr_vulns`) and turn on pull blocking in the project.

The full list with arguments is under "Plugins" → rgstr → **"For agents"**.

## 2. A registry in a cluster (administrator)

### The operator

An administrator installs the operator: it brings a ClusterRole, a CRD and
its own namespace. A token cannot do this.

**Helm into clusters** — from the "Clusters" section or the API, as a job:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' -d '{
  "clusters": [3],
  "release": {"repo_url": "oci://ghcr.io/piqab/charts", "chart": "rgstroperator", "version": "0.5.1",
              "release": "rgstroperator", "namespace": "rgstroperator-system"}
}' https://hub.example.com/api/hub/k8s/helm/install
```

**Or as a manifest:**

- In the UI: "Clusters" → "Manifests", with the content of
  `https://github.com/piqab/rgstroperator/releases/latest/download/operator.yaml`.
- Through the API: `POST /api/hub/k8s/manifests/apply?job=1`
  (`{"name","note","content","clusters"}`).

### Registry

```
k8s lab apply
apiVersion: registry.rgstr.io/v1alpha1
kind: Registry
metadata:
  name: registry
  namespace: registry
spec:
  storage:
    size: 50Gi
  auth:
    enabled: true
    admins: [admin]
  ingress:
    enabled: true
    host: registry.example.com
    className: cilium
  scan:
    enabled: true            # Trivy; new images on push and on entering the cache
  resources:
    limits: {memory: 1Gi}    # a scan needs a few hundred MB
  proxies:
    - name: dockerhub
      url: https://registry-1.docker.io
      public: true           # pull through the cache without login
end
k8s lab wait deployment registry namespace registry 5m
```

- The Deployment, Service and Ingress are named after the `Registry`.
- Without `auth.secretRef` the operator creates a Secret `<name>-auth`
  with a random `RGSTR_AUTH_SECRET` and a **temporary**
  `RGSTR_ADMIN_PASSWORD`. Do the first sign-in in `/ui` as in part 1.
- `className` is the cluster's ingress class; it is `cilium` on nkt
  clusters with Cilium.
- TLS comes from `ingress.tls` (cert-manager or your own Secret) or from
  the Traffic Fly gateway certificate.
- The Trivy database lives on the data volume (in an `emptyDir` when data
  is in S3).
- The Docker Hub login for the cache is
  `credentialsSecretRef: {name: dockerhub}` (keys `username`, `password`).
- `kubectl get registry` shows the phase, address and rgstr version; field
  help is `kubectl explain registry.spec`.

### Backups

`spec.backup` has three methods:

| Method | Needs | Where copies go |
|---|---|---|
| `S3` | a bucket and a Secret with keys | S3, outside the cluster; rgstr does it itself |
| `Longhorn` | Longhorn | the Longhorn backup target (`task: backup`) or a local snapshot |
| `VolumeSnapshot` | a CSI driver with snapshots and external-snapshotter | usually next to the volume |

**Longhorn on an nkt cluster** is a checkbox when the cluster is created,
or the "Add-ons" button on a ready one ([Clusters](/en/guide/hub-clusters)).
It also becomes the default StorageClass, so the registry volume lands on
Longhorn right away:

```yaml
spec:
  storage:
    size: 50Gi                     # no storageClassName: longhorn is the default
  backup:
    enabled: true
    method: Longhorn
    schedule: "0 3 * * *"          # cron, UTC
    keep: 7
    longhorn:
      task: snapshot               # backup — to the backup target configured in Longhorn
```

Through S3, with no cluster requirements:

```yaml
spec:
  backup:
    enabled: true
    method: S3
    schedule: "0 3 * * *"
    keep: 7
    s3:
      bucket: backups
      prefix: rgstr/prod
      endpoint: https://storage.yandexcloud.net
      region: ru-central1
      secretRef: {name: rgstr-s3}  # AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
```

The state is in `status.lastBackupTime` and the `BackupReady` condition
(`Overdue` when a copy is more than an hour late). An S3 backup also shows
in the plugin ("Backups"). To restore: a Job with `rgstr restore`, or a PVC
from a snapshot in `storage.existingClaim`
([the operator README](https://github.com/piqab/rgstroperator#restore)).

### Cluster (HA)

Several pods on PostgreSQL and S3: any of them takes push and pull, and
upgrades and node drains do not interrupt work.

```yaml
spec:
  replicas: 3
  auth: {enabled: true}
  database:
    urlSecretRef: {name: rgstr-db, key: url}   # postgres://…?sslmode=require
  storage:
    s3:
      bucket: registry
      endpoint: https://storage.yandexcloud.net
      region: ru-central1
      secretRef: {name: rgstr-s3}
```

- The operator creates no PVC, upgrades without downtime
  (`maxUnavailable: 0`), adds a `PodDisruptionBudget` and spreads pods over
  nodes.
- PostgreSQL and S3 availability is their own concern. Built-in backups
  (`spec.backup`) do not apply in this mode: copies come from the database
  and the bucket.
- Moving a single registry over is `rgstr migrate -from /data` in a Job.

### Directory sign-in

```yaml
spec:
  auth:
    enabled: true
    ldap:
      url: ldaps://dc1.corp.example:636
      bindDN: CN=svc-rgstr,OU=Service,DC=corp,DC=example
      bindPasswordSecretRef: {name: rgstr-ldap, key: password}
      baseDN: DC=corp,DC=example
      userFilter: (&(objectClass=user)(sAMAccountName=%s))
      adminGroups: [Registry-Admins]
    # oidc:
    #   issuer: https://sso.example.com/realms/main
    #   clientID: rgstr
    #   clientSecretRef: {name: rgstr-oidc, key: client-secret}
```

The plugin summary shows that LDAP and OIDC are on. The plugin itself still
signs in as a local administrator: local users take precedence over the
directory and work even when it is down.

Install the `rgstr` plugin as in part 1. Set `RGSTR_URL` to wherever the
plugin reaches the registry: the Ingress or Traffic Fly.

## 3. A client's registry in their own namespace

A client with a token and an [own namespace](/en/guide/hub-api) in a
shared cluster can run their own rgstr. Three things are needed.

### Administrator: "Client resources"

The `edit` role in a client namespace does not include third-party CRDs.

1. Open "Clusters" and find the cluster.
2. Click **"Client resources"**.
3. Add the line `registries.registry.rgstr.io`.
4. Review the diff and click "Save".

The hub applies the ClusterRole `nkt-client-crds` with the
`aggregate-to-edit` and `aggregate-to-admin` labels. Kubernetes adds these
rights to `edit` in every namespace, so the client manages only their own
`Registry` objects. An empty list removes the rights.

- The hub refuses Kubernetes groups (`*.k8s.io`), RBAC, admission,
  networking, policy and `cilium.io`. Other resources that act beyond a
  namespace are better not given either; the administrator decides.
- The list is kept per cluster on the hub and goes into the export.

The API is `GET`/`PUT /api/hub/clusters/{id}/client-crds`:

```sh
curl -sS -X PUT -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' \
  -d '{"resources":["registries.registry.rgstr.io"]}' https://hub.example.com/api/hub/clusters/3/client-crds
```

The administrator also sets **"Client ingress"**. Without it, a Traffic
Fly route to a namespace cannot be created; see
[Clusters](/en/guide/hub-clusters).

### Client: a Registry under namespace protection

A client namespace has:

- Pod Security `restricted`;
- a quota and a LimitRange from the token's ceilings;
- incoming traffic only from the same namespace;
- external exposure only through an Ingress with a name in the token's
  domain;
- Services of type `ClusterIP` only.

Admission policies also check what the operator creates, so the
`Registry` must say everything explicitly:

```yaml
apiVersion: registry.rgstr.io/v1alpha1
kind: Registry
metadata:
  name: registry
  namespace: shop
spec:
  serviceType: ClusterIP              # NodePort and LoadBalancer are not allowed
  storage:
    size: 10Gi
  auth:
    enabled: true
  ingress:
    enabled: true
    host: registry.shop.client.example.com   # only within the token's domain
    className: cilium                 # the cluster's ingress class
  pod:
    securityContext:                  # the rgstr image runs as root by default; restricted demands otherwise
      runAsNonRoot: true
      runAsUser: 65532
      runAsGroup: 65532
      fsGroup: 65532                  # the data volume becomes writable for that user
      seccompProfile:
        type: RuntimeDefault
    containerSecurityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop: [ALL]
  scan:
    enabled: true                     # optional
  resources:
    limits: {memory: 1Gi}             # the Trivy scan; within the token quota
  proxies:                            # optional: your own Docker Hub cache
    - name: dockerhub
      url: https://registry-1.docker.io
```

What goes wrong otherwise:

- **No `securityContext`.** The pod fails `restricted` and the Deployment
  stays without replicas. The reason is in the ReplicaSet events.
- **`serviceType: NodePort` or a foreign `ingress.host`.** The hub refuses
  the resource before it is applied: it checks a `Registry` the same way
  as a Service and an Ingress.

**What else to keep in mind in a client namespace:**

- **Outgoing traffic** is open to the internet but not to private networks.
  The Docker Hub cache, a public S3 and the Trivy database are reachable;
  an upstream, MinIO or PostgreSQL on the local network are not. A
  PostgreSQL in the same namespace is reachable.
- **Backups** — `method: S3` (rgstr does them itself) or `VolumeSnapshot`
  (snapshots in the client namespace). The hub refuses `method: Longhorn`:
  the operator would create a `RecurringJob` in `longhorn-system`, outside
  the client namespace and in the administrator's backup target.
- **Memory.** Without `resources.limits` the token's LimitRange values
  apply; a scan may not fit in them, and the pod restarts on OOM.
- **A cluster** (`replicas` above 1) works if the client has PostgreSQL and
  S3.
- `metrics.serviceMonitor` is an administrator resource: the cluster's
  Prometheus does not belong to the client.

**How to apply it:**

- with your own kubeconfig: `kubectl --kubeconfig shop.kubeconfig apply -f
  registry.yaml`;
- with a script from a token (the `k8s.apply` action):

  ```
  k8s shared apply
  …the same YAML…
  end
  k8s shared wait deployment registry namespace shop 5m
  fly own registry.shop.client.example.com cluster shared namespace shop
  ```

**The administrator password:**

```sh
kubectl --kubeconfig shop.kubeconfig -n shop get secret registry-auth \
  -o jsonpath='{.data.RGSTR_ADMIN_PASSWORD}' | base64 -d
```

It is temporary: change it in `https://registry.shop.client.example.com/ui`.

### Publishing through Traffic Fly

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"registry.shop.client.example.com","cluster":"shared","namespace":"shop"}' \
  https://hub.example.com/api/hub/own/fly
```

- The script command `fly own … cluster … namespace …` and the MCP tool
  `fly_own_add` do the same.
- The certificate is on the gateway; traffic to the cluster ingress is
  HTTP.
- By default the operator derives `auth.realm` from `ingress.host`
  (`https://<host>/v2/auth`). So `docker login
  registry.shop.client.example.com` works with no extra setup.

An administrator who wants to see such a registry in the hub installs the
`rgstr` plugin for it. The client does not need the plugin: they have `/ui`
and the rgstr API.

## What nkt does not do

- It does not modify rgstr or rgstroperator: everything above uses their
  documented settings. New releases arrive through the image tag and the
  chart version.
- It does not scan images itself: Trivy inside rgstr does, and the plugin
  only shows the reports and queues images.
- It keeps the rgstr administrator password only in the plugin service
  environment. Robot secrets never reach logs or agents. The plugin takes
  no cache upstream login at all — it is set in rgstr.
- rgstr's `/metrics` has no login. The plugin reports this as a finding
  when the address is reachable from outside. Close it at the proxy, or
  keep it inside the cluster.
