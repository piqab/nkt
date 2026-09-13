---
title: Certificates
---

# Certificates

![Certificates](/screens/en/certificates.png)

Every certificate from the nginx, haproxy, caddy configs and
`/etc/letsencrypt`: expiry, names, issuer, algorithm, key, self-signed or
not. haproxy `crt` directories are expanded by SNI, derived copies (the
combined PEM for haproxy) are found by fingerprint and inherit the
original's status.

## Not just dates

- **Comparison with the real socket**: nkt opens a real TLS connection to
  the service address and compares fingerprints — the file on disk may be
  fresh while the service never did a `reload`.
- **Auto-renewal state**: whether certbot knows the certificate, whether
  `certbot.timer` or cron is active. An expired certificate is almost
  always broken automation, not forgetfulness.

## Actions

- **Renew** a certbot lineage via `--standalone`: services and processes
  holding 80/443 (including ones started by hand) are stopped and started
  again regardless of the outcome; before the call nkt checks who holds
  port 80.
- **Issue** a new Let's Encrypt certificate; certbot is installed if
  missing.
- **Combine a PEM for haproxy** from a certbot lineage with a haproxy
  reload.
- **Self-signed**: RSA 2048/3072/4096, several names, wildcard, unicode
  domains.
- nginx, haproxy and caddy config snippets for the issued certificate —
  with copy to clipboard.

Everything long runs as a job with a live log — the window can be closed,
nothing stops on the host. Scheduled auto-renewal —
`NKT_AUTO_RENEW_CERTS=true`.
