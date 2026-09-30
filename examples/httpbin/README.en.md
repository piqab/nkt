# httpbin — a compose stack deployed through the nkt hub

[httpbin](https://httpbin.org) is a service for testing HTTP clients
(`/get`, `/headers`, `/status/418`, `/delay/2`…). The example shows
`action: compose` and the "Sites" tab on a ready image.

- `deploy/docker-compose.yml` — the stack: one `httpbin` service, port 8080
  not published;
- `deploy/pipeline.yaml` — the pipeline.

## Why not the compose file from postmanlabs/httpbin

The original's compose file,
[postmanlabs/httpbin](https://github.com/postmanlabs/httpbin/blob/master/docker-compose.yml),
builds the image from source (`build: '.'`) and takes port 80. nkt
deployments take ready images only, so the hub stops the deployment before
touching any host:

```
service httpbin is built from source (build:) — specify a ready image (image:)
```

and port 80 belongs to the proxy. This example uses
[go-httpbin](https://github.com/mccutchen/go-httpbin), an API-compatible Go
clone with a ready image `mccutchen/go-httpbin:2.25.0` from Docker Hub for
amd64 and arm64. The version is pinned; a new one is deployed by editing the
tag in `docker-compose.yml` (on ghcr.io go-httpbin's versioned tags end at
v2.16.1, hence Docker Hub).

## 1. Pipeline

"Deployments" → "New pipeline" → **"Examples" → "httpbin"** fills in
the compose file link and the stack and pipeline names; pick a host (the hub
shows right away whether it has docker with compose and offers to install
it if not) and the description is ready. Without the button, use "Compose
from a link":

```
https://github.com/piqab/nkt/blob/main/examples/httpbin/deploy/docker-compose.yml
```

a host, the stack name `httpbin` (otherwise the stack is named after the
repository, `nkt`), "Fill in the description". Or paste
`deploy/pipeline.yaml` and adjust `hosts`.

**"Dry run"** checks without changing anything: docker and compose on the
host, `compose config`, whether the image is in the registry. Then "Save"
and "Deploy". The job log shows the files written to `/srv/compose/httpbin`,
`pull`, and `up -d --wait`, which waits until the container runs (the image
has no healthcheck of its own: it is built on distroless, with no shell or
curl inside).

## 2. Site

The simplest way is in the pipeline itself: uncomment the `site:` block
and put your name there (its A record must point at the host):

```yaml
  site:
    domains: [httpbin.example.com]
    service: httpbin
    port: 8080
```

After deploying the stack, the hub sets the site up by itself: checks DNS
and ports 80/443 from outside, installs a proxy if there is none (nginx),
publishes the service on `127.0.0.1`, issues a certificate, writes the proxy
config and checks HTTPS. The site shows up in the "Sites" tab marked with the
pipeline. Later deployments only check the site over HTTPS; they set it up
again if something in `site:` changed. If the site fails (DNS does not point
at the host yet), the deployment still succeeds, and the reason is in the
log and on the site; a dry run shows DNS and ports in advance.

By hand: "Deployments" → "Sites" → "New site" (after deploying the stack):
host, name, "Check", proxy, target: stack `httpbin`, service `httpbin`, port
`8080`, "Set up".

Check:

```sh
curl https://httpbin.example.com/get
curl -i https://httpbin.example.com/status/418
```

## Without a proxy

To try the stack on the host without a site, uncomment
`ports: ["127.0.0.1:8080:8080"]` in `docker-compose.yml` and run on the host:
`curl http://127.0.0.1:8080/get`.
