---
title: 'Plugin: Technitium DNS'
---

# Technitium DNS plugin

Your domains on your own name servers, managed from the hub: zones
`example.com`, `example1.com`… are created and edited in the Plugins
section, with script commands and by agents over MCP. The servers are the
[Technitium DNS](https://github.com/piqab/nkt/tree/main/examples/technitium)
stack from the pipeline examples: ns1 (primary) and ns2 (secondary) on
different hosts. The plugin is the `plugin` service of the same stack;
how plugins work in general — [Plugins](/en/guide/hub-plugins).

Without the plugin the hub works as before: Traffic Fly, sites and
deployments don't touch DNS.

## Deploy and connect

1. Deployments → New pipeline → Examples → **Technitium DNS Server** on
   the ns1 host; the second pipeline — `pipeline.secondary.yaml` on ns2.
   Details — the [example's README](https://github.com/piqab/nkt/tree/main/examples/technitium).
   The plugin is already in the description: the `plugin` service,
   `nkt-plugin.py` in `files:` and `NKT_PLUGIN_SECRET: generate:hex32`.
2. About → Plugins → Connect → From a pipeline → the Technitium pipeline →
   Check → Connect.
3. In the hub menu — Plugins → **Technitium DNS**: zones, records,
   delegation, DNSSEC.

## Several domains

Each domain is its own zone. **Add zone** (or `dns zone add …` in a
script):

- the zone is created as primary on ns1 and becomes a member of the
  **catalog zone** (`catalog.nkt.invalid`,
  [RFC 9432](https://www.rfc-editor.org/rfc/rfc9432)): ns2 gets it by
  itself — no need to set it up on the second server;
- the zone's NS are ns1 and ns2;
- an optional template: **site** (`@` and `www` at the address) or
  **mail** (MX, SPF, DMARC, `mail` at the address).

Then at the domain registrar:

1. **NS** — `ns1.example.com` and `ns2.example.com` (your servers' names);
2. if the name servers are inside the same domain — **glue records**
   (their addresses);
3. the zone's **Delegation** shows what to set and what public resolvers
   (Cloudflare, Google) already see: “delegated”, “different NS”, “the
   zone isn't visible”.

Create all the domain's records (A, MX, TXT…) first, then change the NS at
the registrar: that way the site and mail don't drop out during the move.

## Records

- **Records** — a table: name, TTL, type, value; “Add record” and “Delete”
  on a row.
- **As text** — the whole zone as lines `NAME TTL TYPE VALUE` (`@` is the
  zone itself) in the edit window with a diff before writing and a version
  history on the hub. The plugin compares the text with the zone and
  changes only the difference; SOA, DNSSEC and app records (Failover)
  aren't shown in the text and aren't touched.
- Types: A, AAAA, CNAME, NS, MX, TXT, SRV, CAA, PTR, ANAME.

```
@        3600   A     203.0.113.10
@        3600   MX    10 mail
@        3600   TXT   "v=spf1 mx -all"
mail     300    A     203.0.113.20
www      3600   CNAME @
_sip._tcp 3600  SRV   10 5 5060 sip.example.com.
```

## DNSSEC

A zone's DNSSEC — sign it (ECDSA P-256, keys and their rollover are
Technitium's) and the **DS** records for the registrar. To remove the
signing, remove the DS at the registrar first.

## Scripts

```
dns zone add example.com template site ip 203.0.113.10
dns set app.example.com A 203.0.113.10,203.0.113.11 ttl 300
dns add example.com TXT "google-site-verification=…"
dns rm old.example.com
dns check example.com
dns sign example.com
dns zone rm test.example.org
```

- `set` — exactly these values of the type for the name (adds missing
  ones, removes other values of that type); `add` — one value next to the
  existing ones; `rm` — all of the name's records, one type or one value.
- Check parses the commands with the plugin; a dry run shows what would
  change (`+` / `-`) without changing anything.

## MCP

Agent tools: `dns_zones`, `dns_records`, `dns_zone_text`,
`dns_delegation`, `dns_dnssec` (read) and `dns_zone_add`,
`dns_zone_delete`, `dns_zone_apply`, `dns_template_apply`,
`dns_record_set`, `dns_record_add`, `dns_record_delete`,
`dns_dnssec_sign`, `dns_dnssec_unsign` — for an administrator or a token
by permission with the “plugin dns” action. An example request: “create
zone example1.com with a site at 203.0.113.10 and check the delegation”.

<!-- hub-dns -->

## Upgrading an existing installation

The catalog zone appeared in this version of the example:

- **primary**: deploy its pipeline again — `dns-init` creates the catalog
  zone and adds the zones from `ZONES` to it;
- **secondary**: deploy its pipeline again — `dns-init` sets up the
  secondary catalog zone. Secondary zones created by hand earlier stay as
  they are; to let the catalog manage them, delete them in the ns2 console
  — the catalog creates them again within seconds.
