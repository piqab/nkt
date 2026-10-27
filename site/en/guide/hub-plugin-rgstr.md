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

Data lives on disk, with no database.
[rgstroperator](https://github.com/piqab/rgstroperator) installs it into
Kubernetes as a single `Registry` resource.

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

**First sign-in.** `RGSTR_ADMIN_PASSWORD` is a **temporary** password.

1. Open `https://registry.example.com/ui` and sign in as `admin`.
2. Change the password.

Until you do, `docker login` and the API refuse that password: rgstr
rejects a temporary password over Basic. The plugin cannot sign in either.

### The plugin

"About" → "Plugins" → "Catalog" → **rgstr** → "Install on host". Pick the
rgstr host, or any host that can reach rgstr.

| Setting | What |
|---|---|
| `RGSTR_URL` | where the plugin reaches rgstr; on the rgstr host — `http://127.0.0.1:5000` |
| `RGSTR_USER`, `RGSTR_PASSWORD` | an rgstr administrator whose password is **already changed**; the password is a secret (the service environment file, `0600`) |
| `RGSTR_PUBLIC` | the address for docker and pipelines — `registry.example.com` |
| `RGSTR_CA_FILE` | the PEM of your own CA if rgstr HTTPS is signed by it |

The `nkt-plugin-rgstr` service listens on 127.0.0.1:9280, without Docker.

### What the hub shows

- **Summary** — version, login, repositories, pulls, disk, garbage
  collection, policies, and **findings**:
  - login is off;
  - rgstr is reachable over HTTP and not on loopback;
  - a user still has a temporary password;
  - the `user` role has delete rights;
  - a robot can push to every repository (`*` + push);
  - disk space is low;
  - garbage collection is off, stale or failed;
  - there is no retention policy;
  - `/metrics` is open without login.
- **Repositories → Tags** — digest, size, platforms, push time and the
  immutable flag. Deleting a tag asks for confirmation and works as a dry
  run.
- **Projects** — create (public, quota in GB) and delete an empty one.
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
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

- "Check" asks the plugin whether the words are right.
- A dry run only reports what would be done.

Nightly cleanup is a saved script:

```
rgstr retention run
rgstr gc
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
```

**MCP tools:**

- reads, for any token: `rgstr_summary`, `rgstr_repos`, `rgstr_tags`,
  `rgstr_projects`, `rgstr_robots`, `rgstr_users`, `rgstr_policies`;
- changes, for a token with "plugin rgstr": `rgstr_tag_delete`,
  `rgstr_gc_run`, `rgstr_retention_run`, `rgstr_project_create`,
  `rgstr_project_delete`, `rgstr_robot_create`, `rgstr_robot_secret`,
  `rgstr_robot_delete`.

The full list with arguments is under "Plugins" → rgstr → **"For agents"**.

## 2. A registry in a cluster (administrator)

### The operator

An administrator installs the operator: it brings a ClusterRole, a CRD and
its own namespace. A token cannot do this.

**Helm into clusters** — from the "Clusters" section or the API, as a job:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' -d '{
  "clusters": [3],
  "release": {"repo_url": "oci://ghcr.io/piqab/charts", "chart": "rgstroperator", "version": "0.2.1",
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
```

What goes wrong otherwise:

- **No `securityContext`.** The pod fails `restricted` and the Deployment
  stays without replicas. The reason is in the ReplicaSet events.
- **`serviceType: NodePort` or a foreign `ingress.host`.** The hub refuses
  the resource before it is applied: it checks a `Registry` the same way
  as a Service and an Ingress.

`backup` and `metrics.serviceMonitor` do not belong in a client
namespace: they are administrator resources.

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
- It keeps the rgstr administrator password only in the plugin service
  environment. Robot secrets never reach logs or agents.
- rgstr's `/metrics` has no login. The plugin reports this as a finding
  when the address is reachable from outside. Close it at the proxy, or
  keep it inside the cluster.
