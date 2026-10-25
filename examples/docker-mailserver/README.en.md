# Mail: docker-mailserver

> **WARNING:** this example uses a third-party repository and images
> ([docker-mailserver](https://github.com/docker-mailserver/docker-mailserver)) —
> their security and stability are not guaranteed.

Mail on your own domain in one pipeline: Postfix (SMTP), Dovecot (IMAP),
Rspamd (spam and DKIM signing), fail2ban. No webmail and no database —
mailboxes live in docker-mailserver's files.

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — a one-off
  `mail-init` (the first mailbox `postmaster@MAIL_DOMAIN` from `.env`,
  if there are no mailboxes yet) and `mailserver` (a pinned image).
- [`pipeline.yaml`](pipeline.yaml) — the pipeline: host `mail1`, ports
  25, 465, 587, 993 — public on all host addresses.
- [`deploy/nkt-plugin.py`](deploy/nkt-plugin.py) — the hub plugin:
  mailboxes, aliases, DKIM and DNS records from About → Plugins.

## Before deploying

1. **Name and address.** `MAIL_HOSTNAME` (e.g. `mail.example.com`) — an
   A record to the host's public address. `MAIL_DOMAIN` — the mailbox
   domain.
2. **Certificate** for `MAIL_HOSTNAME` — the host's Certificates
   (certbot, `/etc/letsencrypt/live/MAIL_HOSTNAME`). The container
   mounts it read-only and picks up a renewed one by itself.
3. **Ports.** 25 (incoming mail), 465 and 587 (client submission), 993
   (IMAP) — open them in the host firewall. Many hosting providers block
   **outbound 25** by default — mail won't leave without it; they open
   it on request.
4. **PTR** (reverse record) of the public address — to `MAIL_HOSTNAME`.
   The hosting provider sets it (panel or request), not the domain's DNS.
   Without PTR, large mail services put your mail in spam or reject it.
5. **Memory.** About 1 GB without the antivirus; `ENABLE_CLAMAV: "1"` in
   `.env` — about 1 GB more.

## Deploying

Pipelines → Examples → **Mail: docker-mailserver** → host, names,
domain. The hub generates the `postmaster` password itself
(`generate:password`) — it's in the pipeline's `.env`. `mail-init` adds
the mailbox only on the first deploy: change the password later with the
plugin.

## Hub plugin

docker-mailserver has no API of its own — the plugin runs `setup` in the
container (`docker exec`), so it's installed **on the mail host** and
needs Docker (a service in the `docker` group — that's root rights on
the host; the hub warns before installing).

About → Plugins → **Install on a host** → Mail (docker-mailserver) → the
mail host → `MAIL_HOSTNAME`, `MAIL_PUBLIC_IP` (the public IPv4: the
server name's A record and SPF; empty — no A). Service
`nkt-plugin-mail`, port 9200 on 127.0.0.1.

- **Domains** — with mailbox counts and whether there is a DKIM key. A
  domain appears with its first mailbox.
- **Mailboxes** — add, change password, delete. The password goes only
  through the form field: it reaches the container as an environment
  variable, not on the command line and not in logs. It's stored as a
  hash.
- **Aliases** — add and delete.
- **DKIM key** — Create a DKIM key (Rspamd, 2048 bits). After creating
  it, restart docker-mailserver (Hosts → the `mailserver` container →
  Restart) so Rspamd starts signing.
- **DNS records** of a domain: MX, SPF, DKIM, DMARC and A (if
  `MAIL_PUBLIC_IP` is set). The **Set through the DNS plugin** button —
  if a DNS plugin (Technitium, Cloudflare) is connected to the hub:
  Check shows what changes, Apply sets it. The hub remembers what it
  set: other values at the same names aren't touched, and a removed
  record is deleted on the next Apply.
- **Scripts:** `mail dkim DOMAIN`, `mail alias add|rm ALIAS TO`,
  `mail rm ADDRESS`, `mail records DOMAIN` — no passwords. **MCP:**
  `mail_*`.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against a fake `docker`
(no Docker needed); `go test ./examples` runs it too. It checks that the
password never appears in command arguments.
