---
title: What nkt is
---

# What nkt is

**NetKnownsThat (nkt)** looks at a Linux host and answers the question
that usually takes half a dozen commands by hand: **what is actually
listening on the network here, does it match the configuration, and
what is broken.**

It parses nginx, haproxy, Caddy, docker/compose, podman, LXD, libvirt,
iptables, ufw and firewalld and checks what it read against what really
happens on the machine: `ss` output, packet counters, live containers, a
real TLS connection to the service's socket. Mismatches become a list of
findings, links between configs become a resource map, probe history
becomes an availability schedule. And all of it can be fixed right
there: a config editor with validation and automatic rollback, services
and containers, firewall rules, certificates.

One static binary (~25 MB), four modes:

```
nkt          web panel and background data collection
nkt tui      terminal UI for working over SSH
nkt scan     one-off check, exit code 2 on critical findings
nkt hub      control center for many hosts
```

No Python, no Node, no separate files on the host: the web UI is
embedded in the binary.

## Quick start

**One host** — binary, service, tunnel:

```bash
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSL -o nkt https://github.com/piqab/nkt/releases/download/$V/nkt-linux-amd64 && sudo install -m 0755 nkt /usr/local/bin/nkt
sudo nkt scan
```

Then [install the service](/en/guide/getting-started#_3-service).

**Many hosts** — the hub in Docker:

```bash
curl -fsSLO https://raw.githubusercontent.com/piqab/nkt/main/deploy/docker-compose.hub.release.yml
docker compose -f docker-compose.hub.release.yml up -d
docker compose -f docker-compose.hub.release.yml logs hub   # admin password
```

The hub opens at `http://127.0.0.1:8443`; details —
[installing the hub](/en/guide/install-hub).

**Look without installing** — a snapshot of a real server with planted
problems; works on Linux, macOS and Windows:

```bash
git clone https://github.com/piqab/nkt.git && cd nkt
make build-dev
NKT_MODE=fixtures ./nkt        # http://127.0.0.1:8077, password in the console
```

`fixtures` mode reads nothing from your machine and touches nothing: the
panel immediately shows 25 findings — Redis open to the world, a port
conflict, a container in a restart loop, expired TLS. Probes and metrics
are simulated there (a banner in the UI, `"simulated": true` in the API),
and 14 days of history are seeded on first start; turn that off with
`NKT_DEMO_BACKFILL=false`. On Windows and macOS this mode is the default.

## Where next

- [Installing on a host](/en/guide/getting-started) and
  [installing the hub](/en/guide/install-hub).
- [Ports and access](/en/guide/ports) — what to expose and what not.
- [Section guide](/en/guide/overview) — what every page has.
- [Deployments](/en/guide/hub-deploy) — applications from Git to clusters
  and hosts, [CI/CD examples](/en/guide/cicd-examples).
- [Automation](/en/guide/case-automation) — a hub behind NAT, n8n, bots
  and outgoing webhooks in one end-to-end example.
- [Reference](/en/guide/reference-config) — every variable, security,
  API, limitations, troubleshooting.
