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

- **Kubernetes findings across clusters** — a summary from every control
  plane.
- **Helm to clusters** — one release into the chosen clusters (or the
  clusters of a host group) by a hub job, in turn, with a per-cluster
  outcome.
- **Cluster upgrade** — "Upgrade" on a cluster: a version, then node by
  node (control planes first) cordon, drain, the upgrade, waiting for
  Ready, uncordon; on an error the job stops and continues from the same
  node.
- **Manifests: one YAML into several clusters** — a manifest and a choice
  of clusters; before writing — the text diff against the previous
  revision and `kubectl diff` for each cluster, writing runs `kubectl
  apply` on the first control plane of each, with the outcome per
  cluster. Every application is kept on the hub as a revision; an earlier
  one can be opened in the editor and applied again.
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
hub:<file>`, `endpoint ADDRESS` ([Scripts](/en/guide/hub-scripts)).

## Installation details

- **kubeadm** installs packages from `pkgs.k8s.io` of the chosen branch;
  the branch is recorded in the cluster, and workers added later get the
  same one. Old branches like v1.31 don't work on Debian 13: `sqv`
  rejects their signature as outdated.
- **containerd** on a machine from a cloud image is installed as a
  distribution package; an existing one on a "bare-metal" host (e.g.
  Docker's `containerd.io`) isn't reinstalled — CRI and `SystemdCgroup`
  are enabled on it, the previous config stays in `config.toml.nkt-bak`,
  Docker containers are not stopped.
- **Cilium** — after the control plane starts, the `cilium` CLI (latest
  stable, checksum-verified) is installed and `cilium install` runs;
  "replace kube-proxy" — `--disable-kube-proxy` for k3s,
  `--skip-phases=addon/kube-proxy` for kubeadm.
- **Expose** — DNAT from host ports (`6443`, `80`, `443`, any can be
  changed or cleared) to the control plane: its own `NKT-PF` chains, the
  `nkt-portforward.service` unit restores them on boot. The kubeconfig
  then points to the host and the chosen API port.
- **WireGuard**: interface `nktwg<N>` with address `10.200.<N>.<i>/24`,
  machine network `10.<100+N>.<i>.0/24`; `PostUp` opens the UDP port,
  allows FORWARD through the tunnel and removes libvirt masquerading
  between machine subnets — nodes see each other by their real
  addresses. Before creating machines the hub checks every pair of hosts
  over the tunnel; without a handshake the log says "UDP 51820 is closed
  or the address is wrong".
- **Bridge**: a machine's address is found through `qemu-guest-agent`,
  which cloud-init installs on first boot.
- The creation job is a hub job: machines are created the same way as
  "New machine", then the role install, the token, joining the other
  nodes, waiting for `Ready`, the port forward and the kubeconfig. Retry
  API: `POST /api/jobs/{id}/retry`, `POST /api/hub/clusters/{id}/retry`.
