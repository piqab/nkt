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
- [`deploy/init.py`](deploy/init.py) — initial setup through the API (keys, zones, transfer, Failover);
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

`dns-init` creates the secondary zones and Failover. Changes from the
primary arrive by NOTIFY in about 10 seconds. Check:

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
