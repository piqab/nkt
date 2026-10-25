# Technitium DNS Server: your own name server, DNS-01 for certificates, failover

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Check them yourself before deploying to production.

[Technitium DNS Server](https://github.com/TechnitiumSoftware/DnsServer) is
an authoritative and recursive DNS server with a web console. In this
example it is **the name server of your zones**: two servers (`ns1` and
`ns2`) on different hosts, zones transferred from the primary to the
secondary. On top of it:

- **DNS-01 validation** for Let's Encrypt certificates — cert-manager in
  Kubernetes (and certbot on hosts) puts a TXT record via RFC 2136 with a
  TSIG key: wildcard certificates and certificates for services without an
  open port 80;
- **failover records** — a name returns only live addresses (the Failover
  app checks them over TCP, HTTP or ping);
- **DoH** `https://dns.example.com/dns-query` behind the site proxy,
  **DoT** optionally (Android "Private DNS").

Files:

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — the primary (ns1): `dns` and the one-shot `dns-init`;
- [`deploy/docker-compose.secondary.yml`](deploy/docker-compose.secondary.yml) — the secondary (ns2);
- [`deploy/init.py`](deploy/init.py) — initial setup through the API (keys, zones, the catalog zone, transfer, Failover);
- [`deploy/nkt-plugin.py`](deploy/nkt-plugin.py) — the nkt hub plugin: zones and records from the hub, scripts, MCP (§8);
- [`pipeline.yaml`](pipeline.yaml), [`pipeline.secondary.yaml`](pipeline.secondary.yaml) — the pipelines;
- [`deploy/cert-manager-issuer.yaml`](deploy/cert-manager-issuer.yaml) — a `ClusterIssuer` and an example certificate;
- [`deploy/certbot-dot-hook.sh`](deploy/certbot-dot-hook.sh) — the DoT certificate after every renewal.

## Why our own compose rather than theirs

| Their `docker-compose.yml` | Here |
|---|---|
| `image: …:latest` | the version is pinned (`15.6.2`); a new one is a tag edit |
| console `5380` on all addresses | `127.0.0.1` only: `ssh -L 5380:127.0.0.1:5380 host` |
| `53` on all addresses | on the host's interface address — `ports:` in the pipeline (see below) |
| default recursion | private networks only — not an open resolver |
| health check `/api/dnsClient/healthCheck` | needs a token in 15.x and always fails — the console's response is checked |
| everything set up in the console | TSIG keys, zones, transfer, DoH behind the proxy, Failover — by the one-shot `dns-init` through the API on every deployment |

## 1. Addresses and names

You need two hosts with public addresses (here `203.0.113.10` is ns1,
`198.51.100.20` is ns2) and a domain (`example.com`).

At the domain registrar:

1. **Glue records** (often "host names" / "child nameservers"):
   `ns1.example.com → 203.0.113.10`, `ns2.example.com → 198.51.100.20`.
2. **An A record** `dns.example.com → 203.0.113.10` — the DoH site.
3. Then one of two:
   - **DNS-01 validation only** (the main DNS stays at the registrar): NS
     records of the subzone `acme.example.com` → `ns1.example.com` and
     `ns2.example.com`;
   - **the whole domain on your servers** (needed for failover records in
     `example.com`): the domain's NS at the registrar — `ns1.example.com`
     and `ns2.example.com`. Create all the zone's records (A, MX, TXT…) in
     the console first, then change the NS.

## 2. The primary (ns1)

"Deployments" → "New pipeline" → **"Examples" → "Technitium DNS Server"**,
pick the host — the description is ready (the host's address is filled
into `ports:`), or paste [`pipeline.yaml`](pipeline.yaml). Adjust:

- `ports:` — the host's **interface** address (`ip -4 addr`; in a cloud
  behind NAT — the internal address). Not `0.0.0.0`: on Ubuntu
  `127.0.0.53:53` is held by systemd-resolved and publishing on all
  addresses fails with "address already in use". The other way is to turn
  the stub off: `DNSStubListener=no` in `/etc/systemd/resolved.conf` and
  `systemctl restart systemd-resolved`;
- in `env:` — `DNS_DOMAIN`, `ZONES`, `ACME_ZONE`, `SECONDARY_IP`,
  `SECONDARY_NS`. The admin password and both TSIG keys are created by the
  hub (`generate:`);
- `site:` — `dns.example.com` (DoH).

Save and deploy. `dns-init` writes what it did into the deployment log:
the `cert-manager` and `zone-transfer` keys, zones, transfer only to
`SECONDARY_IP` and only with the key, the secondary's NS in the zones, in
`ACME_ZONE` — updates of TXT only and only with the `cert-manager` key,
the Failover app. Repeating it on every deployment breaks nothing; other
TSIG keys created in the console stay.

Console: `ssh -L 5380:127.0.0.1:5380 ns1` → `http://127.0.0.1:5380`, user
`admin`, password — `DNS_ADMIN_PASSWORD` ("Secrets" → ".env" → "Show").
Technitium takes the password from `.env` only on the first start: if you
change it in the console, change it in `.env` too, or `dns-init` cannot
log in.

## 3. The secondary (ns2)

A second pipeline on a **different** host — [`pipeline.secondary.yaml`](pipeline.secondary.yaml):

- `ports:` — this host's interface address;
- `PRIMARY_IP` — the primary's address;
- `TSIG_TRANSFER` — **the same** as the primary's (copy it from its
  secrets); `ZONES` and `ACME_ZONE` — the same.

`dns-init` sets up the secondary **catalog zone** (`catalog.nkt.invalid`)
and Failover: the primary's zones arrive with the catalog by themselves —
both those from `ZONES` and new ones added later in the console or from
the hub. Changes from the primary arrive by NOTIFY in about 10 seconds.
Check:

```bash
dig +short @203.0.113.10 acme.example.com SOA
dig +short @198.51.100.20 acme.example.com SOA   # the same serial
```

## 4. Certificates through cert-manager (DNS-01)

In the main zone (at the registrar or in Technitium), a CNAME into the
validation subzone for every name:

```
_acme-challenge.app.example.com.  CNAME  app.acme.example.com.
```

In the cluster — a Secret with the key and the `ClusterIssuer` from
[`deploy/cert-manager-issuer.yaml`](deploy/cert-manager-issuer.yaml):

```bash
kubectl -n cert-manager create secret generic technitium-tsig \
  --from-literal=secret='<TSIG_CERT_MANAGER from the pipeline secrets>'
kubectl apply -f cert-manager-issuer.yaml
```

`rfc2136.nameserver` is the primary's address; pods must reach it on
53/tcp and udp. `cnameStrategy: Follow` — cert-manager writes the TXT
where the CNAME points. Start with the Let's Encrypt staging address (in
a comment in the file).

The same key works for certbot on a host (the `dns-rfc2136` plugin):

```ini
# /etc/letsencrypt/rfc2136.ini (chmod 600)
dns_rfc2136_server = 203.0.113.10
dns_rfc2136_name = cert-manager
dns_rfc2136_secret = <TSIG_CERT_MANAGER>
dns_rfc2136_algorithm = HMAC-SHA256
```

The `cert-manager` key can change **TXT only** and **only in
`ACME_ZONE`** — even leaked, it cannot rewrite A records.

## 5. Failover records

You need a zone on your servers (`ZONES`) and a short TTL. In the
primary's console: the zone → **Add Record** → type **APP** → app
**Failover**, class **Failover.Address**, data:

```json
{
  "primary": ["203.0.113.21"],
  "secondary": ["203.0.113.22"],
  "serverDown": [],
  "healthCheck": "tcp443",
  "allowTxtStatus": true
}
```

TTL 30–60 s. The name returns `primary` while its check passes, otherwise
`secondary`. With `allowTxtStatus` a TXT query for the same name shows
the check status. The checks (`tcp443`, `https` with a URL, `ping`,
interval, retries) — "Apps" → Failover → **Config**.

- **The app and its checks — on both servers.** The record reaches the
  secondary by transfer, but the server's own app answers it: without
  Failover the secondary answers **SERVFAIL** (`dns-init` installs the app
  on both). Repeat your own checks changed in the primary's Config on the
  secondary.
- Switching is not instant: clients keep the previous answer until the
  TTL runs out. Traffic Fly switches instantly and transparently (through
  its gateway); they also work together — a failover record picks between
  gateways.

## 6. DoH and DoT

**DoH** — the `dns.example.com` site from the pipeline: a proxy with a
Let's Encrypt certificate in front of the container's DNS-over-HTTP. The
address for browsers ("Secure DNS"), Windows 11, iOS and macOS:
`https://dns.example.com/dns-query`.

**DoT and DoQ** (Android "Private DNS" is DoT only, name
`dns.example.com`): Technitium holds TLS itself and needs the certificate
in PFX format.

1. On the primary's host — the certbot hook (adjust `DOMAIN`):
   ```bash
   sudo install -m 0755 certbot-dot-hook.sh /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   sudo sed -i 's/^DOMAIN=.*/DOMAIN=dns.example.com/' /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   sudo RENEWED_LINEAGE=/etc/letsencrypt/live/dns.example.com /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   ```
   It puts `dns.pfx` and `pfx.pass` into `/srv/compose/technitium/tls/` —
   now and after every renewal.
2. In `docker-compose.yml` uncomment the `./tls:/etc/dns/tls:ro` volume (a
   directory, not a file — otherwise the swap on renewal is not visible),
   in the pipeline — ports `853/tcp` and `853/udp`. Deploy.
3. In the console: **Settings → Optional Protocols** — DNS-over-TLS (and
   DNS-over-QUIC), certificate `/etc/dns/tls/dns.pfx`, password — from
   `pfx.pass`.

Technitium re-reads the new certificate after a renewal by itself — in
under a minute, without a restart.

**Your own names over DoH/DoT answer everyone, other names do not.**
Recursion (a resolver for any name) is open to private networks only:
behind the proxy Technitium sees the client's real address (`X-Real-IP`),
and a phone on a mobile network gets REFUSED. To use the server as your
own resolver, list your networks:

```yaml
env:
  RECURSION: UseSpecifiedNetworkACL
  RECURSION_ACL: 203.0.113.0/24, 2001:db8::/32
```

`RECURSION: Allow` turns port 53 into an **open resolver** — it gets found
within hours and used for DDoS amplification. Don't.

## 7. Backups and updates

- All state is in the `technitium_config` volume (`/etc/dns`): zones, TSIG
  keys, settings, apps. The secondary is restored by transfer, the primary
  only from a copy:
  ```bash
  docker run --rm -v technitium_config:/data -v "$PWD":/out alpine tar czf /out/technitium-config.tgz -C /data .
  ```
  Or in the console: **Settings → Backup Settings**.
- A new version — a tag edit in both compose files and a deployment: the
  secondary first, then the primary.

## 8. The nkt hub plugin

The `plugin` service is a hub plugin: zones and records right in the hub,
`dns …` commands in scripts, MCP tools for agents. It listens only on
`127.0.0.1:9180` of the host, the hub reaches it over SSH, every call is
signed with `NKT_PLUGIN_SECRET` (the hub creates it, `generate:hex32`).

To connect: About → Plugins → Connect → From a pipeline → this pipeline →
Check → Connect. Then — Plugins → **Technitium DNS**: add a zone
(`example1.com` — ns2 gets it through the catalog), a site or mail
template, records as a table or the whole zone as text with a diff, the
registrar delegation check, DNSSEC and DS. Details — the
“[Plugin: Technitium DNS](https://piqab.github.io/nkt/en/guide/hub-plugin-dns)” guide.

Don't need the plugin — remove the `plugin` service from the compose file
or just don't connect it in the hub.

## 9. Moving from a Technitium that already runs

Technitium is installed by hand (natively — `install.sh`, the `dns`
service, or with your own compose) and everything has to move into the nkt
stack **on the same host**: zones, records, TSIG keys, apps, DNS settings.
One server at a time: ns1 first, then ns2 — while one moves, the other
answers for all zones. The server itself is down for a minute or two.

**Preparation (the old server keeps running):**

1. **Docker on the host** — in nkt: host → Docker → Install.
2. **Version:** the old console, at the bottom of the page. The example's
   image (`15.6.2`) must be **no older** — Technitium carries settings
   forward only. Yours is newer — change the image tag in the compose file.
3. **Backup:** the old console → **Settings → Backup Settings** →
   download the zip.
4. **Put it on the host:** nkt → host → Disks → Files →
   `/srv/compose/technitium/import/backup.zip`.
5. **The host's own resolver:** `cat /etc/resolv.conf`. If it says
   `nameserver 127.0.0.1` (the Technitium installer may have set it), the
   host can't resolve names during the move — and the deployment can't
   pull images. Put ns2's address (or 1.1.1.1) there for the time being.
   After the move the new Technitium listens on 53 at the interface
   address (`ports:`), so `resolv.conf` needs the host's address, not
   127.0.0.1.
6. **Images in advance** (optional, for a shorter outage): the host's
   terminal — `docker pull technitium/dns-server:15.6.2` and
   `docker pull python:3.13-alpine`.
7. **A pipeline** from the example (“Technitium DNS Server”, this host),
   **not deployed yet**:
   - `ports:` — the host's interface address;
   - uncomment `IMPORT_BACKUP: /import/backup.zip`;
   - `ZONES:` — **empty** (or new zones only): the moved zones come from
     the backup, while zones from `ZONES` join the catalog zone — the
     catalog on ns2 would then take them too, where you already have your
     own secondaries;
   - `SECONDARY_IP`, `SECONDARY_NS` — as they are now.

   TSIG keys from the backup stay as they were (ns2 transfers zones with
   them); the `TSIG_*` values in `.env` don't replace them (to replace —
   `TSIG_OVERWRITE: "true"`). The old server's accounts aren't moved:
   sign in as `admin` with the password the hub created. Need them too —
   `IMPORT_AUTH: "true"` and the old server's password in
   `DNS_ADMIN_PASSWORD`.

**The move — with a script** (Profiles → Scripts, `HOST` — the host, the
pipeline — `technitium`):

```
param HOST "host with the old Technitium"
on ${HOST} service dns stop
on ${HOST} service dns disable
deploy technitium
wait ${HOST} port 53 2m
```

The installer's service is `dns` (some wrappers name it `technitium`).
`dns-init` sees an empty server and the backup — restores it and goes on
with the usual setup; the deployment log says “backup … restored: zones —
N”.

**Check:** `dig @<host address> example.com SOA` — the same serial as
before; the console — `ssh -L 5380:127.0.0.1:5380 host`.

**Rollback** — while the old files are intact (`/opt/technitium/dns`,
`/etc/dns`):

```
param HOST "host"
deploy technitium stop
on ${HOST} service dns enable
on ${HOST} service dns start
```

**ns2** — the same with `pipeline.secondary.yaml` on its host: its own
backup from its console, `PRIMARY_IP` — ns1's address. Its secondary zones
come from the backup and keep transferring from ns1 as before; new zones —
through the catalog.

**The plugin** — after ns1 has moved: About → Plugins → From a pipeline.

After a week, if all is well, remove the old installation
(`/opt/technitium/dns`, `/etc/systemd/system/dns.service`). Don't run their
`uninstall.sh`: it removes .NET too and rewrites `/etc/resolv.conf`.

## Troubleshooting

- **Deployment: "address already in use" on 53** — systemd-resolved, see §2.
- **`dns-init`: "Failover не установлен … timed out"** — the server could
  not reach the app catalog (often outgoing DNS over UDP is blocked).
  Turn on forwarding over DoH: `FORWARDERS: https://cloudflare-dns.com/dns-query (1.1.1.1)`,
  `FORWARDER_PROTOCOL: Https`; or install Failover in the console ("Apps" →
  "App Store").
- **The secondary lags behind** — different SOA serials: check
  `SECONDARY_IP` on the primary, `PRIMARY_IP` and the same
  `TSIG_TRANSFER`, 53/tcp between the hosts.
- **cert-manager: "REFUSED"** — wrong key, or the name is outside
  `ACME_ZONE` (no `_acme-challenge` CNAME into the subzone).
