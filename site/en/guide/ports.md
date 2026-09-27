---
title: Ports and access
---

# Ports and access

The short rule: **nkt is never exposed to the internet.** The host and
hub UI listens on loopback; the hub reaches the hosts over SSH on its
own; everything else is internal ports that need not be opened to the
internet.

## Summary

| Port | Listens on | Who connects | Expose? |
|---|---|---|---|
| `8077/tcp` | host and hub, `127.0.0.1` | your browser (tunnel or proxy); the hub — over SSH | no, only via an SSH tunnel or a TLS proxy |
| `8443/tcp` | hub in Docker (published to `8077`) | your browser | only behind a TLS proxy |
| `8446/tcp` | host and hub, the host part of `NKT_ADDR` | your browser — Kubernetes port forwards | same as the UI |
| `22/tcp` | managed hosts | the hub | from the hub's address |
| `8078/tcp` | managed hosts (fallback channel) | the hub | from the hub's address |
| `3142/tcp` | managed hosts, `127.0.0.1` | apt and containerd on the host (reverse SSH forward from the hub) | no |
| `443/tcp`, `80/tcp` | the VPS with [nkt-edge](/en/guide/edge) | GitHub, GitLab, CI; 80 — certbot (Let's Encrypt) | yes — that's their purpose |
| `8444/tcp` | the VPS with nkt-edge | the hub (tunnel) | from the hub's address; may be open to all — login by token |
| `51820/udp` | cluster hosts on WireGuard | neighboring cluster hosts | between the cluster hosts |
| `6443`, `80`, `443` | a host with a cluster (DNAT) | cluster clients | optional, "expose" |

## 8077: UI and API

Both nkt and the hub listen on `127.0.0.1:8077` by default (`NKT_ADDR`),
and the session cookie is HTTPS-only (`NKT_COOKIE_SECURE=true`). Ways to
reach it:

- **SSH tunnel** — `ssh -L 8077:127.0.0.1:8077 -L 8446:127.0.0.1:8446 user@host`
  and `http://127.0.0.1:8077`; `NKT_COOKIE_SECURE=false` in the env file.
  The second `-L` is only needed for Kubernetes port forwards.
- **nkt's own HTTPS** — `NKT_TLS_ENABLED=true` (self-signed or your
  certificate) and `NKT_ADDR=0.0.0.0:8077` if you open it on a LAN.
- **A reverse proxy** with TLS — nginx or Caddy in front of
  `127.0.0.1:8077`.

On a managed host the nkt API listens on loopback only; the hub reaches
it through an SSH tunnel. The port is the hub's `NKT_HUB_HOST_API_PORT`
(8077 by default) or the host's own "API port" field if 8077 is taken
there.

::: danger
Access to nkt equals root on the host; access to the hub equals root on
every host. Don't expose a TLS proxy to the internet without separate
authentication (VPN, SSO, an address allow-list).
:::

An nginx example (UI and API, including WebSocket):

```nginx
server {
    listen 443 ssl;
    server_name nkt.example.com;
    ssl_certificate     /etc/letsencrypt/live/nkt.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/nkt.example.com/privkey.pem;

    # allow 203.0.113.0/24; deny all;   # restrict who may connect

    location / {
        proxy_pass http://127.0.0.1:8077;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 1h;
        client_max_body_size 0;      # image and file uploads
    }
}
```

nkt accepts state-changing requests only from its own origin
(`Sec-Fetch-Site`/`Origin` headers vs `Host`), so the proxy must pass the
original `Host`. Other allowed origins — `NKT_CORS_ORIGINS`.

## 8446: Kubernetes port forwards

"Open in browser" on a pod or service runs `kubectl port-forward` and
opens the application on a **separate port** — it has its own origin,
and the pod's application doesn't see nkt's cookies. The address is
`NKT_FORWARD_ADDR`, by default the same host as in `NKT_ADDR` and port
8446; TLS is the same as the UI's.

- Through an **SSH tunnel**, forward 8446 too (see above).
- Behind a **proxy**, set up a separate name or port for forwards and
  tell nkt how the browser sees them:
  `NKT_FORWARD_PUBLIC_URL=https://fwd.example.com`.
- `NKT_FORWARD_ADDR=off` turns the separate port off: forwards go
  through the UI's path in a sandbox (`Content-Security-Policy:
  sandbox`). Simple pages work, applications relying on JavaScript and
  cookies — not always.

In Docker Compose and the Kubernetes manifest port 8446 is already
published.

## Hub → hosts: 22 and 8078

The hub connects to the hosts itself — the hub needs no incoming
connections and no public address.

- **SSH (22)** — installation, updates, the tunnel to the host API. On
  the host it's enough to allow SSH from the hub's address.
- **Fallback channel (8078)** — for when SSH is unavailable (sshd down,
  stale credentials): the hub dials this port at the same address as
  SSH; TLS with a pinned fingerprint and a per-host token. The port is
  `NKT_HUB_TUNNEL_PORT`. If closed, the host's "Channel" column is gray. A
  firewall that closes all incoming ports closes this one too.

## Package cache: 3142

The hub keeps a reverse SSH forward to the host: on the host
`127.0.0.1:3142` (`NKT_HUB_APTCACHE_PORT`) leads to the hub's caching
proxy. The port lives only on the host's loopback, nothing has to be
opened. See [Package cache](/en/guide/hub-cache).

## Deployment webhooks

A pipeline webhook is `POST /api/hub/hooks/{id}` at the hub's address. If
the hub is behind NAT or must not be exposed:

- **repository or registry polling** — the hub asks by itself, nothing
  to open;
- **[nkt-edge](/en/guide/edge)** on a VPS — accepts only webhooks on 443
  and passes them to the hub over a tunnel the hub keeps itself (8444 on
  the VPS).

If the hub is already behind a proxy and you want to take webhooks
directly, expose **only that path**:

```nginx
location /api/hub/hooks/ {
    proxy_pass http://127.0.0.1:8077;
    proxy_set_header Host $host;
    client_max_body_size 1m;
}
location / { deny all; }    # the rest — only from a VPN or an allow-list
```

The hub rejects a webhook without a valid signature (401), and a replay
of the same delivery too.

## Clusters

- **WireGuard** between cluster hosts — UDP 51820 between them.
- **Expose** — DNAT from host ports (`6443`, `80`, `443` by default, any
  can be changed or cleared) to the control plane. Without it the
  cluster is reachable only from the host's network.
