---
title: Kubernetes clusters
---

# Kubernetes clusters

::: warning Experimental feature
Clusters across several hosts, WireGuard between hosts and Cilium have
been exercised on test stands. Do a “dry run” before a real creation and
read the job log after it.
:::

The hub assembles a cluster from machines on hosts with virtualization
(and, if needed, from the hosts themselves): it creates the machines from
a cloud image, installs nkt into them, and nkt on each node runs the k3s
or kubeadm installation steps. Nodes are ordinary hub machines under
their host; the kubeconfig is in the cluster card.

## A cluster on one host

“New cluster” next to a host with virtualization: the flavor (**k3s** or
**kubeadm**), the **Kubernetes version** (the current stable by default
and the three previous branches), the topology — one machine / 1 control
plane + N workers / 3 control planes + N workers, machine sizes, image,
network, **port publishing** from the host (API 6443 and application
ports) and **Cilium** by a checkbox (with kube-proxy replacement). Inside
a node the API port is always 6443; the port in “publish outside” belongs
to the host.

## The “Clusters” section: several hosts

The “New cluster on several hosts” dialog is a placement table: host →
role → **machines** (how many, sizes, image, bridge) or **the host
itself** as a node. The image comes from the host catalogue (“will be
downloaded” if it is not there yet) or from the hub library (below); the
form itself reports a wrong number of control planes, three control
planes with kubeadm and a missing bridge.

**Network between hosts** — how nodes on different hosts see each other:

| Mode | When | Needs |
|---|---|---|
| **NAT** | all nodes on one host | nothing: the host's libvirt network |
| **Bridge** | hosts in one L2 segment | a bridge on every host with machines; machine addresses come from the hosts' network |
| **WireGuard** | hosts in different networks | UDP 51820 between the hosts: the hub generates keys, installs `wireguard-tools`, brings up `wg-quick@nktwg<N>` and a separate machine network on every host; peer subnets go through the tunnel |

WireGuard works when the hosts can reach each other by address (LAN,
VPN, public IPs — then open UDP 51820 in the external firewall). If the
hub reaches a host by an address the peers cannot see, set a **“host
address for peers”** on it. Hosts whose networks cannot see each other at
all are not connected by the tunnel yet — a relay through the hub is
planned ([TODO](https://github.com/piqab/nkt/blob/main/TODO.md)). Node
traffic never goes through the hub — the hub only hands out keys and
configs.

The **dry run** checks every host — KVM and libvirtd, memory and cores,
ports, required packages, the image, the bridge or the WireGuard address,
ping between hosts, link availability — and writes a checklist (✓ / ! /
✗) to a log over the form: the settings stay in place, ready to adjust
and create. With “prepare as well” the hub downloads images and packages
into the cache and brings up the libvirt network right away.

## What else the section does

- **Cluster images** — your own qcow2/img/raw is uploaded to the hub once
  (up to 10 GB) and chosen in the placement as “from hub: …”; on creation
  the hub uploads it to every host that lacks it.
- **Saved presets** — “save preset” remembers the whole form under a
  name, picking a preset fills it again; presets are part of
  export/import.
- **Hub cache for nodes without internet** — with “apt via hub” on for
  the host, the same port also serves files by URL (the k3s installer and
  binary with airgap images, Cilium CLI, the `pkgs.k8s.io` key, machine
  images) and a **registry mirror** (docker.io, quay.io, registry.k8s.io,
  ghcr.io, gcr.io) — containerd on the nodes is configured for it
  automatically. Everything stays on the hub and leaves for the internet
  once.
- **kubectl** — a button on a ready cluster opens a terminal on the first
  control plane with `kubectl` ready (`KUBECONFIG`, completion, the `k`
  alias); no external API access needed.
- **Try again** — a failed creation job resumes where it stopped: created
  machines, the tunnel and installed roles are not redone.
- Before a role is installed the node is updated to the hub's nkt
  version; on retry the leftovers of a failed `kubeadm init/join` are
  removed automatically.
- Deleting a cluster removes the machines, the tunnel and the machine
  network; a host node stays in the host list.

Scripts do the same — `k8s create` with `nodes "hv1: cp 1, w 2; hv2: w 2;
hv3: host w"`, `network nat|bridge|wireguard`, `version 1.34`, `image
hub:<file>`, `endpoint ADDRESS` ([Scripts](/en/guide/hub-scripts)). The
details are in section 11 of
[HUB.md](https://github.com/piqab/nkt/blob/main/HUB.md).
