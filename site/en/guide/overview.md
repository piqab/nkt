---
title: Overview and findings
---

# Overview, findings, resource map

The first menu sections answer the main question: **what is really going
on on the host and what is broken.** The nginx, haproxy, caddy, docker
compose, firewall configs and certificates are parsed and checked against
the live state — `ss` output, iptables counters, running containers, a real
TLS connection to the socket.

## Overview

![Host overview](/screens/en/overview.png)

- Summary: OS, kernel, uptime, load, memory, disks, network.
- Counters: findings by severity, declared and public listeners,
  containers, availability over 24 h, package updates. Each leads to its
  section.
- **What changed since your last visit** — new and gone findings,
  services, containers, certificates: open it in the morning and see what
  happened overnight.
- **Helper packages** (htop, ncdu, jq, tmux…) with an “installed” mark and
  one-click install.
- “Rescan” — a fresh scan right now, without waiting for the schedule.

## Findings

![Findings](/screens/en/findings.png)

Every finding has a severity, a plain-language explanation, the file and
line, a concrete fix and links to documentation. Search and filters by
severity, service and text.

What is checked:

- **Network and firewall** — port conflicts, “declared but not
  listening”, “listening but not declared”, no default deny, a public port
  blocked by the firewall, a Docker container publishing a port around
  ufw, stale rules, Redis/PostgreSQL/MongoDB on all interfaces.
- **TLS** — weak protocols, no HSTS, certificate not set, expired,
  expiring, not yet valid, unreadable, not covering the name, not renewed
  automatically, an orphaned certbot lineage, self-signed, a weak key,
  **the service has not reloaded the certificate** (the socket serves
  something else than the disk), a plaintext HTTP proxy.
- **Pools and containers** — an undefined or unused upstream, a backend
  not listening, all backends disabled, a single backend, no health check,
  a container in a restart loop, not running, not declared, without a
  restart policy, the haproxy stats page without a password.
- **Profile drift** — when a [profile](/en/guide/profiles) is applied to
  the host, deviations from it show up here too.

The full list of rules with their codes is in the
[README](https://github.com/piqab/nkt/blob/main/README.md).

## Vulnerabilities

Scans of installed OS packages and Docker/Podman container images for CVEs
via trivy. trivy installs itself on the first scan, the vulnerability
database is downloaded automatically (or taken from the hub, which keeps
one for all hosts). The list: severity, package, fixed version, links; the
scan progress is live.

## Resource map

![Resource map](/screens/en/topology.png)

A graph where traffic reads left to right:

```
external network → service → listener → pool → backend → container → docker network
```

Edges come from the configs (`proxy_pass`, `upstream`, `use_backend`,
published ports), node state from live listeners, containers and
findings. The column layout stays stable between scans; zoom and drag
with the mouse, node details on hover.
