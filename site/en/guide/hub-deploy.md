---
title: Deployments
---

# Deployments: from Git to clusters and hosts

The hub's **"Deployments"** section deploys your applications from a Git
repository: as a manifest into Kubernetes clusters, a Helm release or a hub
script. Images are built and tested by your CI (GitHub Actions, GitLab CI)
or by you — nkt takes the second half: "ready → deploy".

## Pipeline

A pipeline is described in YAML (edited in a window with a diff, the
description has a revision history):

```yaml
repo: https://github.com/org/app.git
ref: main                 # a push to this branch is a deployment
# tags: "v*"              # or repository tags matching a pattern

action: manifest          # manifest | helm | script
manifests: [deploy/k8s.yaml]
clusters: [prod]          # hub clusters
# group: prod             # or the clusters of a host group

# poll: 5m                # repository polling
# registry: ghcr.io/org/app
# registry_tags: '^v?\d+\.\d+\.\d+$'
```

- **manifest** — manifest files from the repository, `kubectl apply` on
  each cluster's control plane; every application also lands in the hub's
  manifest library.
- **helm** — a chart from a chart repository, values from a file in Git;
  `tag_key: image.tag` substitutes the image tag.
- **script** — a hub script from the repository (parameters `TAG`,
  `COMMIT`, `REF`): `git pull` and `docker compose up`, a service restart,
  migrations.

Manifests and values get `{{nkt.tag}}`, `{{nkt.commit}}` and
`{{nkt.ref}}` substituted.

## When to deploy

- **The "Deploy" button** — the branch head or a given tag.
- **Webhook** — a push to the branch or a tag matching the pattern. A
  signature is required: GitHub and Gitea — HMAC-SHA256, GitLab — a secret
  token, from a CI step — an nkt signature with a timestamp (an example is
  in the "Webhook" window). A repeat of the same delivery is rejected; the
  webhook chooses nothing itself — what and where to deploy is in the
  description.
- **Polling** (`poll`) — the hub notices a new commit in the branch itself.
- **Registry** (`registry`) — the hub watches the image tags and deploys
  the newest one matching the pattern.

Polling and registry start working after the first deployment by the
button — a new pipeline deploys nothing on its own.

## Access and secrets

A token or deploy key for a private repository and login:token for a
private registry are kept encrypted on the hub; they cannot be shown, only
replaced or removed. The webhook secret is shown to an administrator with
an audit log entry and is rotated with one button.

## History and rollback

Every deployment is a hub job with a log: what (commit, tag), what
triggered it (button, webhook, polling, registry), how it ended.
"Roll back" deploys the commit and tag of an earlier successful deployment
again.

## Hub not reachable from the internet

A webhook needs an address GitHub can reach. If the hub sits at home or in
an office behind NAT:

- enable repository or registry polling — nothing needs to be opened;
- or put **nkt-edge** on a VPS — a small separate program with a Let's
  Encrypt certificate that accepts only webhooks and hands them to the hub
  over a tunnel the hub itself keeps to it (see below).
