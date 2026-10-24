# Forgejo Actions runner with Docker-in-Docker

Your own runner for building container images, deployed by an nkt
pipeline:

- `docker` — a separate dockerd (`docker:29.9-dind`, **privileged**):
  jobs build images in it, not in the host's Docker;
- `runner-register` — one-time registration in Forgejo with a token from
  `.env` (it doesn't register again: `.runner` is kept in a volume);
- `runner` — `forgejo-runner daemon`, jobs reach DinD over TLS;
- `docker-gc` — daily cleanup of the build cache and images older than 7
  days.

nkt doesn't build images — that's CI's job; this example runs CI next to
the hub.

## 1. Host

Use a separate host or VM for builds: a privileged container is root on
the host, and jobs run code from repositories. Don't put the runner next
to production stacks. A custom-role token can't deploy this stack
(privileged is forbidden) — deploy as an administrator.

## 2. Registration token

Forgejo → Site administration → Actions → Runners → **Create new runner**
(or the same in organization or repository settings — then the runner
serves only them). Copy the token.

## 3. Deploy

Deployments → New pipeline → **Examples → Forgejo Actions runner**, or
paste [`pipeline.yaml`](pipeline.yaml), with your host (`build1`). In the
stack's `.env`: `FORGEJO_URL`, `RUNNER_TOKEN`, the name and labels. On
save the values move to the pipeline's secrets and leave the
description. Dry run, then Deploy.

Check: Forgejo → Runners — the runner is Idle (online). To re-register
(a new token, another Forgejo) delete the `forgejo-runner_runner-data`
volume and deploy again.

Labels (`RUNNER_LABELS`):

- `ubuntu-latest` — an image with node, git and the docker CLI, like
  `runs-on: ubuntu-latest` in GitHub Actions (`actions/checkout` works);
- `docker` — the docker CLI only.

## 4. Building an image and deploying it

[`workflows/build-app.yml`](workflows/build-app.yml) goes into the app
repository as `.forgejo/workflows/build.yml`. A push to `main` builds
`<registry>/<owner>/<repo>:<short commit>`, a `v1.2.3` tag builds
`…:v1.2.3`, into Forgejo's own registry.

Repository variable `REGISTRY` — the Forgejo address without `https://`;
secret `REGISTRY_TOKEN` — a Forgejo token with `write:package`.

Then one of two methods.

### Method 1. Webhook — right after the build

Copy [`scripts/nkt-hook.sh`](../hello-app/scripts/nkt-hook.sh) into the
app repository and add `NKT_HOOK_URL` and `NKT_HOOK_SECRET` from the app
pipeline's Webhook window to the secrets. The workflow's last step calls
it with the image tag, and the hub deploys exactly that tag. The hub must
be reachable from the runner.

### Method 2. The hub watches the registry

No hub secrets in CI and no inbound connections to the hub. In the app
pipeline:

```yaml
action: compose
compose:
  file: deploy/docker-compose.yml
  project: app
  hosts: [web1]
  images:
    app: git.example.com/owner/app:{{nkt.tag}}
registry: git.example.com/owner/app
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 5m
```

For a private registry add a key (address, login, a token with
`read:package`) in the pipeline's Secrets → Registries. Polling starts
after the first manual deployment.

## 5. Example: the nkt hub image

1. Forgejo → New migration → a mirror of `https://github.com/piqab/nkt`.
2. [`workflows/build-nkt-hub.yml`](workflows/build-nkt-hub.yml) goes into
   it as `.forgejo/workflows/hub.yml`, with `REGISTRY` and
   `REGISTRY_TOKEN` as above.
3. Tag `v1.13.101` → image `git.example.com/owner/nkt-hub:v1.13.101`.
4. A test hub ([`nkt-hub-test`](../nkt-hub-test)) with your own image
   and registry polling:

```yaml
repo: https://github.com/piqab/nkt.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.hub.release.yml
  project: nkt-hub-test
  hosts: [localhost]
  images:
    hub: git.example.com/owner/nkt-hub:{{nkt.tag}}
  ports:
    hub: ["127.0.0.1:18443:8077", "127.0.0.1:18446:8446"]
  env_keys:
    hub: [NKT_BOOTSTRAP_ADMIN_PASSWORD]
  env:
    NKT_BOOTSTRAP_ADMIN_PASSWORD: generate:password
registry: git.example.com/owner/nkt-hub
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 10m
```

Don't update your production hub this way — it has Update in Settings;
this is an example of building your own image entirely on your own
machines.
