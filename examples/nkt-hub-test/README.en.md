# Test nkt hub

A second hub in Docker next to the production one — to try new versions,
imports and settings without risking production. It is deployed by the
production hub's pipeline onto its own machine (`localhost`).

## 1. Deploy

"Deployments" → "New pipeline" → **"Examples" → "Test nkt hub"**, host
**localhost** — the description is ready: the image of the same version as
the production hub, ports `127.0.0.1:18443` and `18446`, the admin
password created by the hub. Or paste [`pipeline.yaml`](pipeline.yaml).
"Dry run", then "Deploy".

Log in:

- on the machine itself — `http://127.0.0.1:18443`;
- from your computer — `ssh -L 18443:127.0.0.1:18443 <hub machine>`, then
  `http://127.0.0.1:18443`;
- with HTTPS — uncomment `site:` (a name and an A record pointing to the
  hub machine).

User `admin`, password — "Secrets" → ".env" → "Show"
(`NKT_BOOTSTRAP_ADMIN_PASSWORD`; applied on the first start, while the
database is empty).

## 2. Don't manage the same hosts

Two hubs over the same hosts get in each other's way: polled deployments
run from both, Traffic Fly "Apply" in one overwrites the other's gateways,
nkt updates, and a narrow sudo key change cuts the other hub off.

- **Don't import the production hub's full export into the test one.** If
  you need data, skip hosts, pipelines, Traffic Fly routes and nkt-edge in
  the import plan.
- Set up test hosts: a VM, an LXD container, a spare VPS.
- A host both hubs talk to carries an "another hub" mark in "Hosts" —
  remove it from one of them.

## 3. Keep in mind

- The test hub's `localhost` is its container: it cannot deploy or move
  "onto the hub machine" (there is no Docker in the container).
- Update by editing the tag in `images:` and deploying from the
  production hub. The "Update" button in the test hub's "About" does not
  update the container.
- A hub in a container does not deploy or move the stack it runs in —
  it refuses with an explanation.
- Remove it by deleting the pipeline; with "and volumes" the test hub's
  database and encryption key go too.

## If it does not deploy

**"No access to the image"** — the `ghcr.io/piqab/nkt-hub` image of this
version is not available without a login: not released yet, or the
package is private on GitHub (new GitHub packages are private by
default). The repository owner opens it once: GitHub → Packages →
`nkt-hub` → Package settings → Change visibility → Public. Until then — a
token with `read:packages` in the pipeline's "Secrets" → "Registries"
(`ghcr.io`).
