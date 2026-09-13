---
title: Services
---

# Services

![Services](/screens/en/services.png)

systemd units with state, autostart, description and the ports each one
listens on.

- **Actions**: start, stop, restart, reload, enable or disable autostart,
  the unit's journal.
- **Config check before an action**: `nginx -t`, `haproxy -c`, `caddy
  validate` — a broken config will not take the service down with a
  restart.
- **Other services** — processes started by hand or from a container, with
  their sockets and whether the port is open to the outside; terminate
  with SIGTERM/SIGKILL.
- **Installing a missing service** as a package with a live apt log.
- **Port check**: TCP, HTTP/HTTPS, TLS handshake, an arbitrary `curl` —
  with the response body, headers, a rendering of the page and a download
  of the response. The same is available from “Network interfaces” for
  ports open on `0.0.0.0`.
