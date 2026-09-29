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
clone with a ready image `ghcr.io/mccutchen/go-httpbin` for amd64 and arm64.
On ghcr.io the versioned tags end at v2.16.1 and newer versions are only
`latest` (a deployment pulls every time); to pin a version, use the same
author's Docker Hub image, `mccutchen/go-httpbin:2.25.0`.

## 1. Pipeline

"Deployments" → "New pipeline" → "Compose from a link":

```
https://github.com/piqab/nkt/blob/main/examples/httpbin/deploy/docker-compose.yml
```

pick a host, set the stack name to `httpbin` (otherwise the stack is
named after the repository, `nkt`), and "Fill in the description" (or paste
`deploy/pipeline.yaml` and adjust `hosts`). "Save", then "Deploy". The job
log shows the files written to `/srv/compose/httpbin`, `pull`, and
`up -d --wait`, which waits until the container runs (the image has no
healthcheck of its own: it is built on distroless, with no shell or curl
inside).

## 2. Site

"Deployments" → "Sites" → "New site":

1. the same host, the name `httpbin.example.com` (its A record already
   points at the host) → "Check";
2. the proxy that the host has (with none, nginx is installed);
3. the target: stack `httpbin`, service `httpbin`, port `8080`; nkt
   publishes it on `127.0.0.1` through a `compose.nkt.yml` file next to the
   stack;
4. "Set up": certificate, proxy configuration, HTTPS check.

Check:

```sh
curl https://httpbin.example.com/get
curl -i https://httpbin.example.com/status/418
```

Uncomment `site:` in the pipeline and the hub will check the site over
HTTPS after every deployment.

## Without a proxy

To try the stack on the host without a site, uncomment
`ports: ["127.0.0.1:8080:8080"]` in `docker-compose.yml` and run on the host:
`curl http://127.0.0.1:8080/get`.
