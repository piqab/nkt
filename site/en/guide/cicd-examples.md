---
title: CI/CD examples
---

# CI/CD examples

All examples use one application,
[`examples/hello-app`](https://github.com/piqab/nkt/tree/main/examples/hello-app):
a Python web application (standard library only), tests, a Dockerfile,
CI for three systems and four deployment variants. The example's files
are checked by nkt's tests — pipeline descriptions, the manifest, values,
the script and the webhook signing script go through the same checks as
in the hub.

The split of roles is the same everywhere: **CI tests and builds the
image, the hub deploys**. Keys to clusters and hosts stay on the hub; CI
only knows one pipeline's webhook address and secret — and in the
"no webhook" variant not even those.

```
push / tag ──▶ CI: tests → image → registry ──▶ hub webhook (signed)
                                                  │
                         hub: git checkout → manifest | helm | script
                                                  │
                            Kubernetes clusters / hosts with Docker
```

## Step 1. Repository

1. Copy `examples/hello-app` into a new repository (its contents go to
   the root, together with the hidden `.github`, `.gitea`,
   `.gitlab-ci.yml` — remove what you don't need).
2. Replace `OWNER` with your login or organization **in lowercase** in
   `deploy/*.yaml` and `deploy/deploy.nkt`; the name `hello.example.com`
   in `k8s.yaml` and `values.yaml` — with your own.
3. Check locally:
   ```bash
   python -m unittest -v test_app
   APP_VERSION=local python app.py            # http://127.0.0.1:8000
   docker build -t hello-app --build-arg VERSION=local . && docker run -p 8000:8000 hello-app
   ```

## Step 2. Where to deploy — a pipeline in the hub

"Deployments" → "New pipeline", paste one of the files from `deploy/` and
adjust `repo`, `clusters` or the host name.

### Variant 1. A manifest to Kubernetes

`deploy/pipeline-k8s.yaml` + `deploy/k8s.yaml` (a Namespace, a Deployment
with 2 replicas, a `/healthz` check, limits and a restricted
`securityContext`, a Service, an Ingress):

```yaml
repo: https://github.com/OWNER/hello-app.git
ref: main
tags: "v*"
action: manifest
manifests:
  - deploy/k8s.yaml
clusters: [prod]
```

In `k8s.yaml` the image is <code v-pre>ghcr.io/OWNER/hello-app:{{nkt.tag}}</code> — the
hub substitutes the tag from the webhook.

### Variant 2. A Helm release

`deploy/pipeline-helm.yaml` + `deploy/values.yaml`. The chart is
[onechart](https://github.com/gimlet-io/onechart), a generic chart for a
single application; the hub puts the image tag into `image.tag`:

```yaml
action: helm
clusters: [prod]
helm:
  repo_name: onechart
  repo_url: https://chart.onechart.dev
  chart: onechart
  release: hello-app
  namespace: hello
  values: deploy/values.yaml
  tag_key: image.tag
```

Check the values keys with the "Chart default values" button in the Helm
release window. Helm must be installed on the control plane.

### Variant 3. A Docker host, no cluster

`deploy/pipeline-host.yaml` + `deploy/deploy.nkt` — a hub script:

```
param TAG "тег образа"

on web1 docker stack /srv/hello-app/docker-compose.yml up
services:
  web:
    image: ghcr.io/OWNER/hello-app:${TAG}
    restart: unless-stopped
    ports:
      - "8080:8000"
    environment:
      APP_VERSION: ${TAG}
end

wait web1 http http://127.0.0.1:8080/healthz 200 60s
```

`web1` is the host name in the hub. The hub writes the compose file with
the new tag, brings the stack up and waits for `/healthz`; if it doesn't
answer in time, the deployment is marked failed.

### Variant 4. No webhook — the hub watches the registry

`deploy/pipeline-registry.yaml`:

```yaml
repo: https://github.com/OWNER/hello-app.git
ref: main
action: manifest
manifests:
  - deploy/k8s.yaml
clusters: [prod]
registry: ghcr.io/owner/hello-app
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 5m
```

Every 5 minutes the hub looks at the image tags and deploys a new
version. No hub secrets in CI, the hub isn't exposed. A private registry
— `login:token` in the pipeline's "Access".

### The first deployment

Press **"Deploy"** and enter the tag of an image that's already built
(e.g. the short commit from the CI log). Polling and registry watching
start only after this first deployment.

## Step 3. Who builds — CI

The same in all three systems: a push to `main` → an image tagged with
the short commit (12 characters); a `v1.2.3` tag → an image `:v1.2.3`;
then — a webhook with that tag. If the webhook address isn't set, CI
only builds the image.

In all examples the webhook is called through
[`scripts/nkt-hook.sh`](https://github.com/piqab/nkt/blob/main/examples/hello-app/scripts/nkt-hook.sh):
the nkt signature (HMAC-SHA256 of "timestamp.body"), branch, commit, tag.
It needs `sh`, `openssl` and `curl`.

### GitHub Actions

The file is `.github/workflows/build.yml`. The image goes to GHCR, login
with the built-in `GITHUB_TOKEN` (the `packages: write` permission is
already set in the file).

1. Settings → Secrets and variables → Actions → **Secrets**:
   `NKT_HOOK_URL` — the address from the pipeline's "Webhook" window,
   `NKT_HOOK_SECRET` — the secret from there.
2. The first image appears after the first push; make the package public
   (profile → Packages → `hello-app` → Package settings) or give the
   cluster access to private GHCR.

```yaml
      - name: Выложить через хаб nkt
        if: ${{ env.NKT_HOOK_URL != '' }}
        env:
          NKT_HOOK_SECRET: ${{ secrets.NKT_HOOK_SECRET }}
        run: sh scripts/nkt-hook.sh "${{ steps.tag.outputs.tag }}" "$GITHUB_SHA" main
```

### GitLab CI

The file is `.gitlab-ci.yml`: stages `test`, `build` (Docker-in-Docker,
the image goes to GitLab's built-in registry `$CI_REGISTRY_IMAGE`),
`deploy`.

1. Settings → CI/CD → **Variables**: `NKT_HOOK_URL` and
   `NKT_HOOK_SECRET`, with Masked and Protected checked (the `main`
   branch and `v*` tags must be protected, otherwise protected variables
   aren't visible in them).
2. The runner needs Docker-in-Docker (`privileged = true`); shared
   runners on gitlab.com already support it.
3. The cluster needs access to the project registry: a public project or
   a Deploy token with `read_registry` in an `imagePullSecret`.

```yaml
deploy:
  stage: deploy
  image: alpine:3.20
  needs: [build]
  rules:
    - if: $NKT_HOOK_URL
  script:
    - apk add --no-cache curl openssl
    - sh scripts/nkt-hook.sh "$TAG" "$CI_COMMIT_SHA" main
```

The image tag passes from `build` to `deploy` through
`artifacts:reports:dotenv`.

Instead of a CI step you can connect a **regular GitLab webhook**
(Settings → Webhooks, Secret token — the pipeline's secret, Push and Tag
push events): the hub checks `X-Gitlab-Token`. The image tag is then the
short commit, and the image must already be built by the time of the
deployment — fits variants where no image is needed or it's built
earlier.

### Gitea and Forgejo Actions

The file is `.gitea/workflows/build.yml` (the same syntax as GitHub
Actions, the runner is `act_runner` with Docker). The image goes to
Gitea's built-in registry.

1. Settings → Actions → **Variables**: `REGISTRY` — the Gitea address
   without `https://` (`gitea.example.com`).
2. **Secrets**: `REGISTRY_TOKEN` — a Gitea token with `write:package`,
   `NKT_HOOK_URL`, `NKT_HOOK_SECRET`.

A regular Gitea/Forgejo webhook works too: Settings → Webhooks → Gitea,
Secret — the pipeline's secret; the hub checks `X-Gitea-Signature`.

### No CI: build on your machine

```bash
TAG=$(git rev-parse --short=12 HEAD)
docker build --build-arg VERSION=$TAG -t ghcr.io/OWNER/hello-app:$TAG .
docker push ghcr.io/OWNER/hello-app:$TAG
NKT_HOOK_URL=… NKT_HOOK_SECRET=… sh scripts/nkt-hook.sh "$TAG" "$(git rev-parse HEAD)" main
```

Or without a webhook — "Deploy" in the hub with that tag.

### No image of your own

If no image needs building (a manifest with a public image, a config, a
static site through a `git clone` script) — connect a regular GitHub,
GitLab or Gitea webhook straight to the pipeline or turn on `poll: 5m`:
the hub deploys a new branch commit by itself.

## Step 4. Release, check, roll back

- **Releasing a version**: `git tag v1.0.0 && git push origin v1.0.0` —
  CI builds `hello-app:v1.0.0` and calls the webhook (variants 1–3), or
  the hub finds the tag in the registry itself (variant 4).
- **Checking**: pipeline → "History" — who started it, commit, tag,
  result and the job log. Rejected webhooks are in the hub's audit log
  (`pipeline.hook.rejected` with the reason).
- **Rolling back**: "History" → "Roll back" on an earlier successful
  deployment — the hub deploys its commit and tag again, no rebuild.

## Hub behind NAT

A webhook needs an address CI can reach. If the hub isn't exposed —
[nkt-edge](/en/guide/edge) on a VPS, or variant 4 (registry) and `poll`,
which need nothing exposed.

If something doesn't deploy — the table at the end of the
[Deployments](/en/guide/hub-deploy#if-nothing-deploys) page.
