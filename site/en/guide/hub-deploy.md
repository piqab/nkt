---
title: Deployments
---

# Deployments: from Git to clusters and hosts

The hub's **"Deployments"** section deploys your applications from a Git
repository: as a manifest to Kubernetes clusters, as a Helm release or
with a hub script. Building images and running tests is up to your CI
(GitHub Actions, GitLab CI, Gitea) or you — nkt takes the second half:
"ready → deploy".

![Deployments](/screens/en/deployments.png)

Why this way: keys to hosts and clusters stay on the hub. CI only gets
the right to "poke" one pipeline's webhook, and what goes where is
decided by the description on the hub — only the branch, commit and tag
are taken from the request.

## Pipeline

"New pipeline" → a name and a YAML description. Editing happens in a
window with a diff, the description has a revision history with notes;
it is validated before it is saved. There are no secrets in the
description — they are in "Access".

```yaml
repo: https://github.com/org/app.git
ref: main                 # a push to this branch — a deployment
tags: "v*"                # and/or repository tags matching a pattern

action: manifest          # manifest | helm | script
manifests: [deploy/k8s.yaml]
clusters: [prod]          # hub clusters
# group: prod             # or the clusters of a host group

# poll: 5m                # poll the repository
# registry: ghcr.io/org/app
# registry_tags: '^v?\d+\.\d+\.\d+$'
# registry_poll: 5m
```

### All fields

| Field | For | Value |
|---|---|---|
| `repo` | always | `https://…`, `ssh://…` or `git@host:path`. No login or password in the URL — a token or key goes into "Access" |
| `ref` | branch | A push to it deploys its head. `ref` or `tags` is required |
| `tags` | tags | A pattern (glob, `v*`): a repository tag matching it deploys that tag |
| `action` | always | `manifest`, `helm` or `script` |
| `manifests` | manifest | A list of files in the repository |
| `clusters` | manifest, helm | Cluster names from the "Clusters" section |
| `group` | manifest, helm | A host group: every cluster whose first control plane is in that group. Can be combined with `clusters` |
| `helm.repo_name`, `helm.repo_url` | helm | The chart repository (`helm repo add`) or `oci://…` |
| `helm.chart`, `helm.version` | helm | Chart and version (empty — latest) |
| `helm.release`, `helm.namespace` | helm | Release name and namespace |
| `helm.values` | helm | A values file in the repository |
| `helm.tag_key` | helm | The values key to put the image tag into: `image.tag` |
| `script` | script | A hub script in the repository (`deploy/deploy.nkt`) |
| `poll` | trigger | Branch polling interval, at least `1m` |
| `registry` | trigger | An image whose tags to watch: `ghcr.io/org/app`, `org/app` (Docker Hub), `registry.example.com:5000/app` |
| `registry_tags` | trigger | A regular expression for suitable tags; default `^v?\d+(\.\d+){1,2}$` |
| `registry_poll` | trigger | Registry check interval, default `5m` |

Every value is checked separately: branches, tags and paths against
strict patterns, `git` is called with `--`, files are read only inside
the checkout (escaping it, including via a symlink, is forbidden).

### Substitutions

Manifests and values get:

| Substitution | Value |
|---|---|
| <code v-pre>{{nkt.tag}}</code> | the tag from the request, button or registry; without a tag — the short commit (12 characters) |
| <code v-pre>{{nkt.commit}}</code> | the full commit |
| <code v-pre>{{nkt.ref}}</code> | the repository branch or tag |

A script gets the same values as the `TAG`, `COMMIT`, `REF` parameters
(`param TAG "image tag"` at the start of the script).

## Actions

- **manifest** — manifest files from the repository are joined, the
  substitutions applied, and `kubectl apply` runs on the control plane of
  each cluster (through the node's API, over the same channel as other
  requests to the host). Every application lands in the hub's manifest
  library ("Clusters" → "Manifests") with the note
  `pipeline <name> <commit>`.
- **helm** — `helm upgrade --install` in each cluster: values from the
  file, the tag into `tag_key`. Helm is needed on the control plane — if
  it's missing, install it with the "Install Helm" button on the node's
  Kubernetes tab.
- **script** — a hub script from the repository: `docker stack … up`,
  `git clone`, a service restart, `wait … http` — anything the
  [script language](/en/guide/hub-scripts) can do. No cluster needed.

Only clusters in the "ready" state are used. If the deployment fails in
some clusters, the job ends with "failed in N of M", and the log shows
where and why.

## When to deploy

- **The "Deploy" button** — the head of the branch from the description;
  you can specify another branch or repository tag and an image tag.
- **Webhook** — a push to the `ref` branch or a tag matching `tags`; a
  request from CI with an image tag (see below).
- **Polling** (`poll`) — the hub notices a new commit in the branch.
- **Registry** (`registry`) — the hub notices a new image tag newer than
  the previous one (compared by numbers: `v1.10.0` is newer than
  `v1.9.3`) and deploys the `ref` branch with that tag.

Polling and registry start working **after the first deployment with
the button** — a freshly saved pipeline deploys nothing by itself. A
disabled pipeline ("Enabled" unchecked) reacts to nothing but the button.
While a pipeline's deployment is running, polling doesn't start another.

## Webhook

A pipeline's **"Webhook"** window: the address (direct to the hub —
`https://hub/api/hub/hooks/<id>`, and via [nkt-edge](/en/guide/edge) if
configured), "Show secret" (recorded in the audit log), "New secret" and
ready instructions for GitHub, GitLab and a CI step.

A signature is required; supported:

| Sender | Headers | Check |
|---|---|---|
| GitHub | `X-Hub-Signature-256: sha256=…`, `X-GitHub-Delivery` | HMAC-SHA256 of the body |
| Gitea, Forgejo | `X-Gitea-Signature` / `X-Forgejo-Signature` | HMAC-SHA256 of the body |
| GitLab | `X-Gitlab-Token` | the secret as is |
| CI step (nkt) | `X-NKT-Timestamp`, `X-NKT-Signature` | HMAC-SHA256 of `<timestamp>.<body>`, the timestamp no older than 5 minutes |

- **A replayed delivery** is rejected: the hub remembers delivery IDs
  (for the nkt signature — the signature itself).
- **Responses** are terse: an unknown address and a wrong signature both
  give `401`, so the real one can't be guessed. The body is up to 1 MB.
- A GitHub `ping` answers `pong`.
- Only `ref` (`refs/heads/…` or `refs/tags/…`), the commit (`after`,
  `checkout_sha` or `commit`) and `tag` are taken from the body. A
  deleted branch or tag, another branch, a tag not matching the pattern —
  `200` with `ignored` and the reason.

**The nkt signature from a CI step** — when the image is already built
and the branch should be deployed with its tag:

```bash
BODY=$(printf '{"ref":"refs/heads/main","commit":"%s","tag":"%s"}' "$SHA" "$TAG")
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$NKT_HOOK_SECRET" -hex | sed 's/^.* //')
curl -fsS -X POST "$NKT_HOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-NKT-Timestamp: $TS" -H "X-NKT-Signature: $SIG" \
  -d "$BODY"
```

Without `ref` (just `{"tag":"v1.2.3"}`) the hub deploys the branch from
the description with that tag. The response is `202` —
`{"status":"deploying","deployment_id":…}`.

Ready CI pipelines — on the [CI/CD examples](/en/guide/cicd-examples)
page.

## Access and secrets

A pipeline's "Access":

- **Repository** — a token (for `https://`, e.g. a GitHub fine-grained
  token with Contents: read) or a private deploy key (for `ssh`/`git@`).
- **Registry** — `login:token` for a private registry (for tag watching).

They are stored on the hub encrypted and never get into the description,
logs or command lines (git gets them through the environment); they
can't be shown — only replaced or removed. The hub needs the `git`
program (the hub image has it).

## History and rollback

Every deployment is a hub job with a log: what (commit, branch, tag), who
started it (button, webhook, polling, registry, rollback), when and how
it ended. **"Roll back"** on an earlier successful deployment deploys its
commit and tag again — the image is already in the registry, no rebuild.

Deleting a pipeline deletes its history; what was deployed to clusters
and hosts stays.

## Example

A ready example — [`examples/hello-app`](https://github.com/piqab/nkt/tree/main/examples/hello-app):
a Python application, a Dockerfile, CI for GitHub Actions, GitLab CI and
Gitea Actions and three deployment variants — manifest, Helm, a host with
Docker Compose. Step by step — [CI/CD examples](/en/guide/cicd-examples).

## Hub not reachable from the internet

- Turn on repository **polling** or **registry** watching — nothing to
  expose.
- Or install **[nkt-edge](/en/guide/edge)** on a VPS — a small program
  with a Let's Encrypt certificate that accepts only webhooks and passes
  them to the hub over a tunnel the hub keeps to it.

## If nothing deploys

| Symptom | Cause and fix |
|---|---|
| Webhook `401` | Wrong secret or signature; a replayed delivery ("Redeliver" in GitHub sends the same ID); for the nkt signature — CI and hub clocks differ by more than 5 minutes. The reason is in the hub's audit log (`pipeline.hook.rejected`) |
| `200 ignored: another branch` | The push wasn't to `ref`; the tag doesn't match `tags` |
| `no cluster matches` | Names in `clusters` don't match the "Clusters" section, the group has no clusters or the cluster isn't "ready" |
| `the repository has no branch or tag …` | A typo in `ref`, the tag isn't pushed yet (`git push origin v1.0.0`) |
| `git …: Authentication failed` / `Permission denied (publickey)` | A private repository without "Access", the token can't read, the key isn't added as a deploy key |
| `the repository has no file …` | Paths in `manifests`, `helm.values` or `script` are from the repository root |
| `Helm is not installed on the host` | Install Helm on the control plane: the node's Kubernetes tab → Helm → "Install Helm" |
| Pods in `ImagePullBackOff` | The image is private: make the package public or add an `imagePullSecret` to the cluster; the image tag doesn't match what CI built |
| Polling or registry stay silent | No first deployment with the button yet; the pipeline is disabled; `registry_tags` filters the tags out (by default — only versions like `1.2.3`) |
| `a deployment of this pipeline is already running` | Wait for the current one — its log is in "History" |
