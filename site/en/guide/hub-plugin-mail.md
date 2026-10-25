---
title: 'Plugin: mail (docker-mailserver)'
---

# Plugin: mail (docker-mailserver)

Mailboxes, aliases and DKIM of the
[docker-mailserver](https://github.com/docker-mailserver/docker-mailserver)
mail server from the hub, and the domain's DNS records (MX, SPF, DKIM,
DMARC) to the connected DNS plugin
([Technitium](/en/guide/hub-plugin-dns),
[Cloudflare](/en/guide/hub-plugin-cloudflare)) with one button. How
plugins work in general — [Plugins](/en/guide/hub-plugins).

## The mail server

The pipeline example Pipelines → Examples → **Mail: docker-mailserver**
(Postfix, Dovecot, Rspamd, fail2ban; the first mailbox
`postmaster@DOMAIN` — its password is in the pipeline's `.env`). Before
deploying:

- **Certificate** for the server name (`MAIL_HOSTNAME`) — the host's
  Certificates; the container mounts `/etc/letsencrypt` read-only.
- **Ports** 25, 465, 587, 993 open in the firewall; **outbound 25** is
  blocked by many hosting providers — they open it on request.
- **PTR** of the public address — to the server name (set by the hosting
  provider).

Details — the [example's README](https://github.com/piqab/nkt/tree/main/examples/docker-mailserver).

## Installing the plugin

docker-mailserver has no API of its own: the plugin runs `setup` in the
container via `docker exec`. So it's installed **on the mail host**, and
the service runs in the `docker` group — that's root rights on the host;
the hub warns before installing.

About → Plugins → **Install on a host** → Mail (docker-mailserver) → the
mail host → the server name and public IPv4 (`MAIL_PUBLIC_IP`: the A
record and SPF; empty — no A). Service `nkt-plugin-mail`,
127.0.0.1:9200.

## What it does

- **Domains** — mailbox counts, whether there is a DKIM key; links to
  mailboxes and DNS records.
- **Mailboxes** — add, change password, delete. The password is entered
  only in a (masked) form field: it reaches the container as an
  environment variable — not in command arguments, not in the action log,
  not in job parameters.
- **Aliases** — add, delete.
- **DKIM key** — Rspamd, 2048 bits; after creating it, restart the
  `mailserver` container so Rspamd starts signing.
- **DNS records** of a domain → **Set through the DNS plugin**: a window
  with the records, Check (what changes) and Apply. The hub remembers
  what it set (owner `plugin:mail:domain:DOMAIN`): other values at the
  same names aren't touched, a removed record is deleted. Needs an admin
  or a token with the “plugin mail” action.
- **Scripts:** `mail dkim DOMAIN`, `mail alias add|rm ALIAS TO`,
  `mail rm ADDRESS`, `mail records DOMAIN` — no passwords.
- **MCP:** `mail_domains`, `mail_mailboxes`, `mail_aliases`, `mail_dns`,
  `mail_mailbox_add`, `mail_mailbox_password`, `mail_mailbox_delete`,
  `mail_alias_add`, `mail_alias_delete`, `mail_dkim_generate`.
