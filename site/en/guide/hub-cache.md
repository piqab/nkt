---
title: Package cache
---

# Package cache and hosts without internet

Hosts download packages through the hub, and each `.deb` goes to the
internet once for all hosts. The hub needs no public address, and the
hosts get no new open ports.

## How it works

- For every host with **"packages through the hub cache"** (a checkbox in
  the host form, on by default) the hub keeps a separate SSH session with
  a **reverse port forward**: on the host `127.0.0.1:3142`
  (`NKT_HUB_APTCACHE_PORT`) leads to the hub's caching proxy. The session
  reconnects by itself; an icon in the "Channel" column shows whether the
  forward is up.
- The host has `/etc/apt/apt.conf.d/99nkt-hub-proxy` and
  `/usr/local/bin/nkt-apt-proxy` — the standard
  `Acquire::http::Proxy-Auto-Detect`: the script answers with the proxy
  address if the port is open and `DIRECT` if the hub isn't connected —
  apt then works as usual. The files are put there when nkt is installed
  from the hub, and on an already installed host — right when the
  checkbox is turned on (root or passwordless sudo is needed).
- Only immutable content is cached — `pool/…` and `by-hash/…`; indexes
  (`dists/…`) pass through; `https` repositories are tunneled as is — the
  proxy doesn't look inside TLS.
- The "localhost" row doesn't use the cache.

"About" → **"Package cache"**: size, file count, hits and misses,
traffic, how many hosts are on the forward, a limit in gigabytes
(`NKT_HUB_APTCACHE_MAX_GB`, 20 by default; `0` — no limit; the value set
in the UI wins) and clearing. When full, the least recently requested
content is evicted.

## Files by URL

The same port does more than apt. Everything a host needs from the
internet for clusters and machines goes through the hub when the port is
open and settles in its cache: a host without internet access gets it
from the hub, a host with access doesn't download per node what the hub
has already seen. The host checks that the port answers as the nkt cache
(`/nkt/ping`): someone else's proxy on the same port is not taken for
it. Port closed — everything goes direct.

`GET http://127.0.0.1:3142/nkt/artifact?url=…` — the hub downloads the
URL itself and serves it from the cache. That's how the k3s installer
and binary with **airgap images** (k3s is installed with
`INSTALL_K3S_SKIP_DOWNLOAD`), the Cilium CLI, the `pkgs.k8s.io`
repository key, the Flannel manifest and cloud machine images are
fetched. URLs with a version in the path and `.deb` files are kept
indefinitely, the rest (`get.k3s.io`, `…/latest/…`, the "current" Ubuntu
image) is re-downloaded when older than 6 hours; if the source is
unavailable, the copy is served.

## Registry mirror

`/v2/…` at the same address — a pull-through cache of container images.
containerd on a cluster node is configured to fetch `docker.io`,
`quay.io`, `registry.k8s.io`, `ghcr.io` and `gcr.io` through the mirror
(k3s — `/etc/rancher/k3s/registries.yaml`, kubeadm —
`/etc/containerd/certs.d/<registry>/hosts.toml`).

- Layers and manifests by digest are kept indefinitely, "tag → digest" —
  5 minutes; if the source is unavailable, the previous value is used.
- The hub reaches the sources anonymously (Bearer per the
  `WWW-Authenticate` challenge); **private images don't go through the
  mirror**.
- `.deb` files from `https` repositories (`pkgs.k8s.io`) are still
  tunneled without caching.

A cluster dry run checks URLs through the hub cache when there is one,
and with "prepare as well" the hub puts everything the nodes will fetch
into the cache in advance.
