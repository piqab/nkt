---
title: Limitations
---

# Limitations

What nkt deliberately doesn't do or can't do yet.

## Platform

- **Linux only** (amd64, arm64, 32-bit ARM). On Windows and macOS only the
  `fixtures` snapshot mode works.
- OS package upgrades, new host preparation by the hub, vulnerabilities
  inside guests — Debian/Ubuntu only (`apt-get`, `dpkg`).

## One host — one database

Each nkt manages one host: its own map, configs and database. Several
hosts — only through the hub, which proxies to each of them, not through
a shared data model.

## Network and firewall

- `ufw` and `firewalld` rules are written; editing iptables directly is
  deliberately not supported. iptables/ip6tables, ufw and firewalld are
  read — pure `nftables` (without the iptables-nft layer) isn't parsed.
- The `declared-not-listening` rule needs `ss`. It stays silent if the
  socket table confirms none of the declared ports (a different network
  namespace is being read). Published container ports aren't checked by
  this rule: with `userland-proxy: false` Docker forwards them via DNAT,
  there is no listener on the host.

## Web servers and runtimes

- nginx, haproxy and Caddy are supported. Traefik and Envoy would need a
  new parser in `internal/parse`.
- Classic LXC (`lxc-ls`/`lxc-info`) isn't supported — LXD only. Podman
  Quadlet isn't parsed — only the list of running containers is visible.
- The SPICE screen — only a plain (not TLS-only) port; a machine's serial
  console doesn't pass the window size, the guest assumes 80×24.
- A libvirt guest password is set through qemu-guest-agent, and the user
  must already exist; for LXD a missing user is created.

## Certificates

- The "what does the socket actually serve" check is one TLS connection
  per certificate, to its first known address. Several certificates by
  SNI on one port — the first one is checked. Not done in `fixtures`.
- A self-signed certificate doesn't remove the browser warning. TLS
  directives are added to the config by hand through the editor (with the
  same validation and automatic rollback).
- Let's Encrypt issuance is `--standalone`: no wildcards, nginx/haproxy
  stop during issuance and renewal.

## Vulnerabilities

- Vulnerabilities inside guests — Debian/Ubuntu packages only. LXD VM
  packages are read through lxd-agent, libvirt machine packages through
  qemu-guest-agent; a guest without the agent is skipped with a warning.
- A machine node on the resource map shows the host's own latest scan;
  a scan made by the hub is not reflected there.

## fixtures mode

Config validation in `fixtures` always "passes" — the failure path is
tested on the test stand or a real host. Probes and metrics are
simulated.

## Hub

- The hub is the single point of access to all hosts; treat it like a
  machine with production access.
- One replica: the registry is SQLite. No horizontal scaling and no
  failover pair — there is export/import.
- Kubernetes clusters from the hub are experimental: no rollback, 3
  control planes — k3s only, start with a dry run.
- Scripts are experimental: no rollback, and the language deliberately
  has no arbitrary shell commands.
- The registry mirror caches public images only.

## Deployments

- No image building — that's CI's job or yours.
- Pipelines, their history and nkt-edge settings are not part of the hub
  export.
- Polling and registry watching start after the first deployment with
  the button.
