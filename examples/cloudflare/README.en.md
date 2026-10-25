# Cloudflare DNS plugin

> **WARNING:** this example uses a third-party service (Cloudflare) — its availability and terms are not guaranteed.

An nkt hub plugin for zones that live at Cloudflare: everything the hub's
DNS plugin can do — without your own name server.

- **Records from the hub:** a table, add, delete, “set” exactly these
  values; the whole zone as text `NAME TTL TYPE VALUE [proxied]` in the
  edit window with a diff and history. `proxied` — the Cloudflare orange
  cloud (A, AAAA, CNAME only). The apex NS is assigned by Cloudflare — it
  is a comment in the text and isn't edited.
- **Delegation:** which NS Cloudflare assigned and what public resolvers
  see.
- **Scripts:** `cf set|add|rm|check …`; **MCP:** `cloudflare_*`.
- **For the hub** (`provides: dns`): Traffic Fly route names, site domains
  (`site.dns`), host autonames. The proxy is off for them. Cloudflare has
  Failover only in paid Load Balancing: with several gateways all
  addresses are set, round-robin.

Zones are created in the Cloudflare dashboard — the plugin doesn't create
or delete them.

## Token

dash.cloudflare.com → **My Profile → API Tokens → Create Token** → the
**Edit zone DNS** template → Zone Resources: **Include → Specific zone** —
only the zones you need. No account-wide token is needed.

## Install

**No Docker** (recommended): About → Plugins → **Install on a host** →
Cloudflare DNS → the hub machine or any host with internet → the token.
The hub installs the `nkt-plugin-cloudflare` service (port 9190 on
127.0.0.1) and connects it. A second account — again with another name
(`cloudflare2`).

**As a container:** a pipeline from [`pipeline.yaml`](pipeline.yaml), then
About → Plugins → Connect → From a pipeline.

## Check

`python3 -I deploy/test_plugin.py` — the plugin against a fake Cloudflare
API (no network, no token); `go test ./examples` runs it too.
