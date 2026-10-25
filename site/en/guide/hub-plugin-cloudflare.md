---
title: 'Plugin: Cloudflare DNS'
---

# Cloudflare DNS plugin

For zones that live at Cloudflare: everything the hub's DNS plugin can do —
without your own name server. How plugins work in general —
[Plugins](/en/guide/hub-plugins); your own name server —
[Technitium DNS](/en/guide/hub-plugin-dns).

## Install

1. **A token:** dash.cloudflare.com → My Profile → API Tokens → **Create
   Token** → the **Edit zone DNS** template → Zone Resources: Include →
   Specific zone — only the zones you need.
2. About → Plugins → **Install on a host** → Cloudflare DNS → the hub
   machine or any host with internet access → the token; “Only these
   zones” — optional.
3. The hub installs the `nkt-plugin-cloudflare` service (127.0.0.1:9190)
   and connects it: Plugins → Cloudflare DNS.

A second Cloudflare account — again with another name (`cloudflare2`). As a
container — the “Cloudflare DNS plugin” pipeline example and Connect → From
a pipeline.

## What it does

- **Records:** a table (name, TTL, type, value, proxy), add, delete; the
  whole zone **as text** `NAME TTL TYPE VALUE [proxied]` in the window with
  a diff and history — only the difference is changed. TTL — seconds or
  `auto`. `proxied` — the orange cloud (A, AAAA, CNAME only). The apex NS is
  assigned by Cloudflare: it is a comment in the text and isn't edited.
- **Delegation:** which NS Cloudflare assigned and what public resolvers
  see.
- **Scripts:**
  ```
  cf set app.example.com A 203.0.113.10 ttl 300
  cf add www.example.com CNAME example.com proxied
  cf rm old.example.com
  cf check example.com
  ```
- **MCP:** `cloudflare_zones`, `cloudflare_records`, `cloudflare_zone_text`,
  `cloudflare_delegation` (read), `cloudflare_zone_apply`,
  `cloudflare_record_set`, `cloudflare_record_add`,
  `cloudflare_record_delete`.
- **For the hub** (`provides: dns`): Traffic Fly route names, site domains
  (`site.dns`), host autonames — without the Cloudflare proxy (the gateway
  or the site holds TLS). Cloudflare has Failover only in paid Load
  Balancing: with several gateways all addresses are set, round-robin.

Zones are created in the Cloudflare dashboard — the plugin doesn't create
or delete them.
