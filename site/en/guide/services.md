---
title: Services
---

# Services

![Services](/screens/en/services.png)

systemd units with state, autostart, description and the **ports** each
one listens on (by the process's unit — nginx shows its worker processes'
sockets too). So you can see who holds 80 or 443 even when the port is
described in a config.

- **Actions**: one power button by state (running — "stop", stopped —
  "start"), restart, reload, one autostart button by state, the unit's
  journal.
- **Config check before an action**: `nginx -t`, `haproxy -c`, `caddy
  validate` — a broken config will not take the service down with a
  restart.
- **Other services** — sockets that are in no parsed config (processes
  started by hand or from a container), with whether the port is open to
  the outside; terminate with SIGTERM/SIGKILL. A port described in the
  nginx or haproxy config doesn't show up here — it is in its service's
  "Ports" column.
- **Installing a missing service** as a package with a live apt log.
- **Port check**: TCP, HTTP/HTTPS, TLS handshake, an arbitrary `curl` —
  with the response body, headers, a rendering of the page and a download
  of the response. The same is available from “Network interfaces” for
  ports open on `0.0.0.0`.
