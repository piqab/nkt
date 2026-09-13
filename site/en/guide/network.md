---
title: Network and firewall
---

# Network interfaces and firewall

## Network interfaces

![Network interfaces](/screens/en/interfaces.png)

- Interfaces with addresses, state, counters and errors; the interface kind
  (bridge, veth, WireGuard, tunnel, libvirt) and the containers attached to
  it.
- Listening sockets with processes; ports open on `0.0.0.0` get a one-click
  port check (TCP, HTTP, TLS, `curl`).

## Firewall

![Firewall](/screens/en/firewall.png)

- **ufw**: state, numbered rules, adding and removing
  (port/protocol/source/comment), enabling **with port 22 protection** —
  you cannot enable the firewall without allowing SSH.
- **firewalld**: zones, services, ports; add and remove temporarily or
  permanently, `reload`.
- Nothing installed — ufw or firewalld is installed as a package with a
  live log.
- iptables is shown as is — no editing, on purpose.
