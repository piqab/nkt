# hello-app — a deployment example for the nkt hub

A small Python web application (standard library only) and everything
needed to deploy it from Git through the hub's **"Deployments"** section:

- `app.py`, `test_app.py` — the application (`/` — a page with the version,
  `/healthz` — a check) and tests;
- `Dockerfile` — the image;
- `.github/workflows/build.yml` — GitHub Actions: tests, the image build
  into GHCR, a call to the hub webhook with the image tag;
- `.gitlab-ci.yml` — the same for GitLab CI (the image goes to GitLab's
  built-in registry), `.gitea/workflows/build.yml` — for Gitea and Forgejo
  Actions;
- `scripts/nkt-hook.sh` — a signed call to the hub webhook from any CI, or
  by hand after a local build;
- `deploy/` — four deployment variants:

| Variant | Files | Where |
|---|---|---|
| 1. Manifest | `pipeline-k8s.yaml`, `k8s.yaml` | a Kubernetes cluster (`kubectl apply`) |
| 2. Helm | `pipeline-helm.yaml`, `values.yaml` | a cluster, a Helm release on the onechart chart |
| 3. Docker host | `pipeline-host.yaml`, `deploy.nkt` | a host without a cluster, `docker compose` |
| 4. No webhook | `pipeline-registry.yaml`, `k8s.yaml` | a cluster; the hub watches the image tags in the registry itself |

CI builds, the hub deploys the ready image: access keys to hosts and
clusters stay on the hub, CI only gets to "poke" the webhook.

## 1. Repository

1. Copy `examples/hello-app` into a new GitHub repository (its contents go
   to the root, `.github` included).
2. Replace `OWNER` with your login or organization (lowercase) in
   `deploy/*.yaml` and `deploy/deploy.nkt`; in `k8s.yaml` and `values.yaml`
   replace `hello.example.com` with your own name.
3. Push to `main` for the first time. The workflow runs the tests and
   builds `ghcr.io/OWNER/hello-app:<commit>`; the webhook is not called yet —
   there are no secrets.
4. GitHub → profile → Packages → `hello-app` → Package settings → make the
   package public (or give the cluster/host access to a private GHCR).

## 2. Pipeline in the hub

1. "Deployments" → "New pipeline", name `hello-app`.
2. Paste one of `deploy/pipeline-*.yaml`, adjust `repo` and:
   - variants 1 and 2 — `clusters` (cluster names from "Clusters") or
     `group`;
   - variant 3 — the host name `web1` in `deploy/deploy.nkt`.
3. A private repository — "Access": a GitHub token with read access
   (Contents: read) or a deploy key.
4. "Deploy" → put the tag of the built image (the short commit from the
   workflow log) into the tag field. The job shows every step; the
   outcome is in "History".

## 3. Webhook: deploy on push

1. In the hub: pipeline → "Webhook" — the address (via nkt-edge if the hub
   is behind NAT) and "Show secret".
2. In GitHub: Settings → Secrets and variables → Actions:
   `NKT_HOOK_URL` — the address, `NKT_HOOK_SECRET` — the secret.
3. Change something in `app.py`, push to `main`: the workflow builds the
   image and calls the webhook with its tag, the hub deploys.
4. Releasing a version: `git tag v1.0.0 && git push origin v1.0.0` — the
   `hello-app:v1.0.0` image and a deployment with that tag.

GitLab CI and Gitea Actions work the same way: the `NKT_HOOK_URL` and
`NKT_HOOK_SECRET` variables in the project's CI settings (details are in
the header of `.gitlab-ci.yml` and `.gitea/workflows/build.yml`). All the
variants are walked through on the site, "CI/CD examples" page.

The webhook is signed (`X-NKT-Signature`, HMAC-SHA256 of "timestamp.body"):
without the secret or with a stale timestamp the hub rejects it, and so it
does a repeat of the same signature.

## 4. Rollback

Pipeline → "History" → "Roll back" on an earlier successful deployment: the
hub deploys its commit and tag again (the image is already in the
registry).

## How the variants work

**1. Manifest** — `deploy/k8s.yaml`: Namespace, Deployment (2 replicas, a
`/healthz` check, limits), Service, Ingress. The hub replaces `{{nkt.tag}}`
in the image name with the tag from the webhook. Every application also
lands in the hub's manifest library ("Clusters" → "Manifests").

**2. Helm** — the [onechart](https://github.com/gimlet-io/onechart) chart, a
generic chart for a single application; values — `deploy/values.yaml`, the
hub puts the image tag into `image.tag` (`tag_key`). Check the values keys
with the "Chart default values" button in the release window.

**3. Docker host** — `deploy/deploy.nkt`: a hub script brings up compose on
the `web1` host with the `hello-app:${TAG}` image (TAG — the tag from the
webhook) and waits for `/healthz`. No cluster needed, a hub host with
Docker is enough.

**4. No webhook** — `deploy/pipeline-registry.yaml`: every 5 minutes the
hub looks at the image tags in the registry and deploys a new version tag.
CI is only needed to build and push the image on a repository tag; no hub
secrets in CI, and the hub is not exposed.

## Locally

```bash
python -m unittest -v test_app
APP_VERSION=local python app.py      # http://127.0.0.1:8000
docker build -t hello-app --build-arg VERSION=local . && docker run -p 8000:8000 hello-app
```

Without CI — build, push and deploy from your workstation:

```bash
TAG=$(git rev-parse --short=12 HEAD)
docker build --build-arg VERSION=$TAG -t ghcr.io/OWNER/hello-app:$TAG . && docker push ghcr.io/OWNER/hello-app:$TAG
NKT_HOOK_URL=… NKT_HOOK_SECRET=… sh scripts/nkt-hook.sh "$TAG" "$(git rev-parse HEAD)" main
```

(or "Deploy" in the hub with that tag).
