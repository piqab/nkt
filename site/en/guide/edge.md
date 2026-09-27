---
title: nkt-edge
---

# nkt-edge: webhooks without exposing the hub

A deployment webhook needs an address GitHub, GitLab or your CI can
reach. You usually don't want to expose the hub like that: it sits at
home or in an office behind NAT, and access to it is access to every
host. **nkt-edge** solves this: a small separate program (about 8 MB) on
a cheap VPS with a public address accepts **only webhooks** from the
internet and passes them to the hub over a tunnel the hub keeps to it.

![nkt-edge](/screens/en/deploy-edge.png)

## Layout

```
GitHub / GitLab / CI ──HTTPS 443 (Let's Encrypt)──▶ VPS: nkt-edge
                                                      ▲
                                    tunnel 8444: TLS 1.3 + token,
                                    opened by the hub
                                                      │
                                        hub behind NAT, no open ports
```

- The hub connects to the edge and keeps the connection; its address may
  change, it needs no public IP. On a drop the hub reconnects.
- The edge accepts `POST /hooks/{id}` and passes the request to the hub
  through the tunnel. Through the tunnel the hub serves **this one
  route**: the hub's UI and API are not reachable via the edge.
- Repository or registry polling is an alternative without an edge at
  all: the hub asks by itself, nothing to expose, but a deployment starts
  with the polling delay.

## Security model

- **The edge holds nothing valuable**: no database, no pipeline secrets,
  no host access. The hub verifies the webhook signature. A compromised
  VPS can only send requests the hub rejects without a valid signature.
- **Filters on the edge**: only `POST` to `/hooks/{id}`, a body up to
  1 MB, a per-address rate limit (`EDGE_RATE`, 60 per minute), optionally
  GitHub addresses only (`EDGE_GITHUB_ONLY`, the list from
  `api.github.com/meta`). `Cookie` and `Authorization` aren't passed to
  the hub; the sender's address goes in `X-Forwarded-For` for the hub's
  log.
- **The tunnel** — TLS 1.3 with the edge's self-signed certificate (name
  `nkt-edge`, created once and kept in `/var/lib/nkt-edge/tunnel/`). The
  hub uses **exactly that certificate** as the only verification root —
  the edge can't be impersonated on the way. Then a handshake with a
  token (at least 32 characters, compared in constant time), and a stream
  multiplexer on top.
- **The service on the VPS** — a dynamic user (`DynamicUser=yes`), the
  only capability is binding 80 and 443, `ProtectSystem=strict`, a system
  call filter.
- **The edge log** — one line per request: address, method, path,
  status. Request values have no line breaks — neighboring entries can't
  be forged.

## Requirements

- A VPS with a public address, Linux amd64/arm64/arm.
- A domain (e.g. `hooks.example.com`) with an A/AAAA record pointing to
  the VPS.
- Open on the VPS: **80** (Let's Encrypt HTTP-01 issuance), **443**
  (webhooks), **8444** (the tunnel from the hub; can be restricted to the
  hub's address if it is static).

## Install from the hub

1. Add the VPS to the hub as a regular host (installing nkt on it is not
   required — only SSH is).
2. "Deployments" → the **nkt-edge** card → **"Install on a host"**: host,
   domain, e-mail for Let's Encrypt (optional), "GitHub addresses only".
3. A hub job (log — like other jobs):
   1. SSH connection, architecture;
   2. the `nkt-edge` program — built from the hub's source or downloaded
      from a release with a checksum check;
   3. `/usr/local/bin/nkt-edge`, `/etc/nkt-edge/edge.env` (0600, with a
      generated token) and the `nkt-edge.service` unit;
   4. the service starts; if ufw is active, 80, 443 and 8444 are opened;
   5. the hub fetches the tunnel certificate over SSH
      (`sudo cat /var/lib/nkt-edge/tunnel/tunnel.crt`), remembers it and
      connects.
4. The card shows "connected", the webhook address and the tunnel
   certificate fingerprint. Every pipeline's "Webhook" window gets a
   **"Via edge"** line with the address `https://hooks.example.com/hooks/…`
   — next to "Directly to the hub".

The Let's Encrypt certificate is issued on the first HTTPS request to the
domain.

## Manual install

If you'd rather not add the VPS to the hub:

```bash
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/nkt-edge-linux-amd64
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
sudo install -m 0755 nkt-edge-linux-amd64 /usr/local/bin/nkt-edge

sudo install -d -m 0700 /etc/nkt-edge
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/edge.env.example \
  | sudo install -m 0600 /dev/stdin /etc/nkt-edge/edge.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/nkt-edge.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/nkt-edge.service
openssl rand -base64 33          # token → EDGE_TOKEN
sudo $EDITOR /etc/nkt-edge/edge.env   # EDGE_DOMAIN, EDGE_TOKEN
sudo systemctl daemon-reload
sudo systemctl enable --now nkt-edge
sudo cat /var/lib/nkt-edge/tunnel/tunnel.crt
```

Then in the hub, "Deployments" → nkt-edge → **"Configure manually"**: the
tunnel address `vps.example.com:8444`, the webhook domain, the same
`EDGE_TOKEN` and the tunnel certificate (the whole output of the last
command, with `BEGIN` and `END`).

From source: `make edge` puts the binaries into `dist/`.

## Edge settings

| Variable | Default | Meaning |
|---|---|---|
| `EDGE_DOMAIN` | — | Name for the Let's Encrypt certificate; required unless `EDGE_SELF_SIGNED` |
| `EDGE_EMAIL` | empty | E-mail for Let's Encrypt |
| `EDGE_TOKEN` | — | Shared secret with the hub, at least 32 characters |
| `EDGE_DATA_DIR` | `/var/lib/nkt-edge` | Let's Encrypt and tunnel certificates |
| `EDGE_HTTPS_ADDR` | `:443` | Webhooks |
| `EDGE_HTTP_ADDR` | `:80` | Certificate issuance only; empty — don't listen |
| `EDGE_TUNNEL_ADDR` | `:8444` | Where the hub connects |
| `EDGE_RATE` | `60` | Requests per minute from one address |
| `EDGE_GITHUB_ONLY` | `false` | Accept webhooks from GitHub addresses only |
| `EDGE_SELF_SIGNED` | `false` | No Let's Encrypt, the tunnel certificate instead — for testing and internal networks |
| `EDGE_PROXY_ADDR` | empty | Behind a reverse proxy: webhooks over HTTP on this loopback address (`127.0.0.1:8445`), 80 and 443 are not taken |

## If 80 and 443 on the VPS are taken

The VPS already runs nginx, Caddy or another web server — then edge can't
take 443. It goes **behind that server**: it takes webhooks over HTTP on
loopback only, and TLS and the certificate stay with your server.

1. "Install on a host" → the **"proxy port"** field, e.g. `8445`.
   `edge.env` gets `EDGE_PROXY_ADDR=127.0.0.1:8445`; ufw opens only 8444.
2. On the server, forward `/hooks/` of the webhook domain to that port
   (a ready snippet is in the install log):

   ```nginx
   location /hooks/ {
       proxy_pass http://127.0.0.1:8445;
       proxy_set_header X-Real-IP $remote_addr;
       client_max_body_size 1m;
   }
   ```

   ```
   hooks.example.com {
       handle /hooks/* {
           reverse_proxy 127.0.0.1:8445
       }
   }
   ```

Edge takes the sender's address from `X-Real-IP` (or the last
`X-Forwarded-For` address) — only when the request comes from loopback,
so the rate limit and "GitHub addresses only" work behind a proxy too.

Installing from the hub checks the ports beforehand: a taken 443 without a
proxy port is an error naming the program holding it; after starting, the
job makes sure the service isn't restarting in a loop.

## Updating

- **From the hub** — "Install on a host" again on the same host: the
  program and unit are replaced, the token stays the same, the hub
  reconnects.
- **By hand** — a new binary over `/usr/local/bin/nkt-edge` and
  `sudo systemctl restart nkt-edge`. The tunnel certificate doesn't
  change, nothing to do in the hub.

## Removal

- **"Remove from VPS"** on the card (if the hub installed edge) — a hub
  job: stops and disables the service, deletes `/usr/local/bin/nkt-edge`,
  the unit, `/etc/nkt-edge`, the data with certificates
  (`/var/lib/nkt-edge`, with `DynamicUser` — `/var/lib/private/nkt-edge`)
  and the ufw rule for 8444, then the hub forgets edge. The ufw rules for
  80 and 443 stay — another server on the VPS may use them.
- **"Forget"** only disconnects the hub from edge and erases its settings;
  the service on the VPS stays.
- **By hand** (edge installed manually):

  ```bash
  sudo systemctl disable --now nkt-edge
  sudo rm -f /usr/local/bin/nkt-edge /etc/systemd/system/nkt-edge.service
  sudo rm -rf /etc/nkt-edge /var/lib/nkt-edge /var/lib/private/nkt-edge
  sudo systemctl daemon-reload
  sudo ufw delete allow 8444/tcp
  ```

  and "Forget" in the hub.

## Log and checks

```bash
sudo journalctl -u nkt-edge -f
curl -s https://hooks.example.com/healthz     # {"ok":true,"hub":true,"version":"…"}
```

`"hub": true` — the hub is connected to the tunnel. On the hub every
delivery is recorded in the audit log (`pipeline.hook.*`) with the
sender's address and an `(edge)` mark.

## If it doesn't work

| Symptom | Cause and fix |
|---|---|
| The card says "disconnected", error `x509: …` | The hub doesn't trust the certificate: a wrong PEM pasted or the edge created a new one (an old-format certificate without the `nkt-edge` name is replaced automatically). Copy `tunnel.crt` into "Configure manually" again or reinstall from the hub |
| "edge accepts the connection and closes it right away" (before — just `EOF`) | The service on the VPS is crashing and restarting: `journalctl -u nkt-edge`. If it says `listen tcp :443: bind: address already in use`, 443 is taken by another server — put edge behind it (section above) |
| "edge rejected the hub: ERR token" | The token in the hub and `EDGE_TOKEN` on the VPS differ |
| "disconnected", timeout | Port 8444 on the VPS is closed (ufw, the provider's security group) or the tunnel address is wrong |
| The webhook answers 503 `hub not connected` | The hub isn't holding the tunnel right now — see the card status and the hub log |
| 429 | `EDGE_RATE` exceeded from one address |
| 403 | `EDGE_GITHUB_ONLY` is on and the request isn't from GitHub addresses (e.g. from GitLab or curl) |
| 404 | Not a `POST` or the path isn't `/hooks/<id>` — check the address in the "Webhook" window |
| 401 from the hub | Wrong signature or secret, a replayed delivery, a stale timestamp (nkt signature) — see [Deployments](/en/guide/hub-deploy#if-nothing-deploys) |
| Browser or curl: certificate error on 443 | Let's Encrypt didn't issue a certificate: the A record isn't on the VPS, port 80 is closed, an issuance limit. Details — in `journalctl -u nkt-edge` |
| The service doesn't start | `EDGE_TOKEN` shorter than 32 characters, no `EDGE_DOMAIN`, port 443 or 8444 taken — the message is in the service log |
| 403 behind a proxy with "GitHub only" on | The proxy doesn't pass `X-Real-IP` — add `proxy_set_header X-Real-IP $remote_addr;` |
