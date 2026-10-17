---
title: Traffic Fly
---

# Traffic Fly: inbound traffic through a VPS

The service lives at home or in an office behind NAT, but users need an
internet address. **Traffic Fly** receives traffic on a cheap VPS with a
public address and passes it through a WireGuard tunnel to your hosts —
into a container, a virtual machine, an LXD instance or a service on the
host itself. Targets see the client under **its real IP**, **TCP and
UDP** both work, and a single port can carry a **pool of targets with
load balancing** and health checks.

The hub configures it all — in **Deployments → Traffic Fly** — but the
gateway and hosts work **without the hub**: tunnels, rules and the
router come back on their own after a reboot.

![Traffic Fly: map and routes](/screens/en/traffic-fly.png)

## Layout

```
clients ──80/443 by name, TCP/UDP ports──▶ VPS: nkt-edge (fly role)
                                              │  WireGuard 51820/udp
                                              ▼
                               host: tunnel nkt-flyN → DNAT → container,
                               VM, LXD, host service, address on its network
```

- **Gateway** — a VPS running [nkt-edge](/en/guide/edge) in the `fly`
  role. A separate `nkt-fly` service (root) runs there: the `nkt-fly`
  WireGuard interface, nftables rules and the name router. The edge
  itself stays unprivileged and only relays the hub's commands to it.
- **Host** — each host has a tunnel to each gateway (`nkt-flyN`,
  `wg-quick@nkt-flyN`). Private keys are created on their own machine and
  never leave it; the hub sees only public keys.
- Target replies go back into the same tunnel by connection mark — the
  host's default route is left alone.

## Routes

| Kind | What | Client IP |
|---|---|---|
| **HTTPS by name** | port 443, chosen by SNI; TLS is not decrypted — the certificate lives on the target | PROXY protocol v2 |
| **HTTP by name** | port 80, chosen by the `Host` header | PROXY protocol v2 |
| **TCP port**, **UDP port**, **TCP+UDP** | the whole port goes to a pool | real, no target changes |

- **80 and 443 are split by name**: `app.example.com`, `*.example.com`;
  an exact name beats a wildcard. One name, one route.
- **Other ports**: one gateway port, one pool. A second pool on the same
  port needs another port or a second gateway (a VPS with another
  address).
- A route by name passes the client IP via **PROXY protocol v2**: the
  target must accept it (nginx `listen 443 proxy_protocol`, Caddy,
  HAProxy, Traefik). Without the checkbox the target sees the gateway's
  tunnel address.
- A route by port goes **through the kernel** (DNAT without source
  rewrite): the target sees the real client address by itself.

## Pools and balancing

A route has one or more **targets**: a host and what runs on it
(container, VM, LXD instance, the host itself or an address on the
host's network) and a port. The hub looks up the container or VM address
when applying.

![Route: names, balancing, targets](/screens/en/traffic-fly-edit.png)

| Balancing | Where |
|---|---|
| **least connections** | routes by name |
| **round robin** | everywhere |
| **by client IP (sticky)** | everywhere; a client sticks to one target |

- **Weight** — the target's share of traffic relative to the others.
- **Drain** — the target gets no new connections, current ones finish;
  this is how a target goes into maintenance.
- **Check**: TCP connect, HTTP request (path and status code) or none; a
  separate check port can be set. A target is down after 3 failures in a
  row and alive again after 2 successes; checks run every 5 s. A down
  target leaves the pool at once, for routes by port too. A UDP route
  needs a TCP check port or no check.

## Map and summary

At the top of the tab is the **map**: gateways → routes → hosts →
targets. Color shows state (green — all alive, yellow — some, red —
none), link thickness — active connections; the map refreshes every
5 s. Click a route for details: targets with check, latency and
connections, traffic and client count. **Admins** see client addresses
while there are at most 30 of them; beyond that, only the count. Viewers
see the map and the client count, not addresses.

## Editing, history and applying

- Routes are changed by admins; saving goes through a **diff** window
  with a note, and each save is a new **version**. Two simultaneous edits
  don't overwrite each other: the second is refused.
- **History** — the difference between versions and restoring any of
  them.
- **Dry run** — a job builds the gateway and host configurations, checks
  keys and target addresses, but writes nothing.
- **Apply** — a job in steps: gateway keys → host keys and target
  addresses → hosts (tunnels, DNAT) → gateways (tunnels, rules, router).
  Hosts go before gateways so traffic flows only once tunnels are ready.
  A host or gateway with nothing left to do removes its tunnels and
  rules.
- Hosts get `wireguard-tools` and `nftables` installed if missing; the
  gateway gets them with the edge install.

## Gateway

**Add gateway** opens the [nkt-edge](/en/guide/edge#install-from-the-hub)
install with the `fly` role: no name or certificate is needed, the
gateway's 80/443 belong to the routes. That is why `fly` can't be
combined with webhooks, API or callbacks — those need a separate edge;
outside checks (`probe`) can be combined. If `ufw` is enabled on the VPS,
the gateway opens 51820/udp and the ports of routes by name itself.

## Events

Once a minute the hub compares the gateway summaries and writes to
[alerts](/en/guide/hub-operations#alerts): **target down / back up**, **no live
targets in pool**, **tunnel down / up**. Telegram, Slack, outgoing
webhooks and the n8n node get them like any other event.

## If it doesn't work

| Symptom | What to check |
|---|---|
| Gateway “offline” | is the edge connected to the hub? `systemctl status nkt-edge nkt-fly` on the VPS |
| Tunnel doesn't come up | the reason is shown under the gateway in Gateways: “not configured yet” — Apply has not reached the host; “gateway rejects” — the host sends, the gateway does not accept: run `sudo tcpdump -ni any udp port 51820` on the gateway — nothing: 51820/udp is closed at the VPS provider or outbound UDP on the host; packets visible: the gateway firewall, or the interface has no key (`sudo wg show nkt-fly public-key` → `(none)`; gateways on 1.13.22+ restore it themselves); “keys do not match” — Apply again; “tunnel is not up on the host” — `systemctl status wg-quick@nkt-flyN` on the host |
| “Host did not answer within 20 s” | does the host page open in the hub; `systemctl status netknownsthat` on the host |
| “nkt cannot do Traffic Fly” | update nkt on the host (1.13.12 or newer) |
| Target “down” | target and check ports; the container listens on its own address, not only 127.0.0.1 |
| Target sees 10.77.x.1 | route by name without PROXY protocol — enable it and configure the target |
| Name doesn't open | the name's DNS points to the gateway address; nothing else on 443 |
