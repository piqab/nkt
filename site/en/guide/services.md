---
title: Services
---

# Services

![Services](/screens/en/services.png)

systemd units with state, autostart, description and the **ports** each
one listens on (by the process's unit — nginx shows its worker processes'
sockets too). So you can see who holds 80 or 443 even when the port is
described in a config.

The table has every installed service from nkt's catalogue: running ones
on top, stopped and failed ones below, with a "start" button and the
journal. Services that aren't installed are not shown — install them in
the "Packages" section.

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
- **Port check**: TCP, HTTP/HTTPS, TLS handshake, an arbitrary `curl` —
  with the response body, headers, a rendering of the page and a download
  of the response. Without “do not verify the certificate”, trust is
  checked for a bare address too: if it fails (by IP, almost always on the
  name), the certificate is still shown with the reason, but no data is
  exchanged; for the service's answer tick the box. The same is available from “Network interfaces” for
  ports open on `0.0.0.0`.
