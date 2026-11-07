---
title: Kubernetes clusters
---

# Kubernetes clusters

::: warning Experimental feature
Clusters across several hosts, WireGuard between hosts and Cilium have
been exercised on test stands. Do a “dry run” before a real creation and
read the job log after it.

The section, the "Clusters" card in the host list and "New cluster" show
when "Kubernetes clusters" is on in "About" → "Experimental features"
(existing clusters work without it).
:::

The hub assembles a cluster from machines on hosts with virtualization
(and, if needed, from the hosts themselves): it creates the machines from
a cloud image, installs nkt into them, and nkt on each node runs the k3s
or kubeadm installation steps. Nodes are ordinary hub machines under
their host; the kubeconfig is in the cluster card.

Step by step from creation to a test pod reachable from outside through an
Ingress and Traffic Fly: [Cluster: from zero to the first
pod](/en/guide/k8s-first-pod).

## A cluster on one host

“New cluster” next to a host with virtualization: the flavor (**k3s** or
**kubeadm**), the **Kubernetes version** (the current stable by default
and the three previous branches), the topology — one machine / 1 control
plane + N workers / 3 control planes + N workers (for kubeadm — with a
**shared control plane address**, below), machine sizes, image,
network, **port publishing** from the host (API 6443 and application
ports), **Cilium** by a checkbox (with kube-proxy replacement) and
**Longhorn** volumes with a chart version (below). Inside
a node the API port is always 6443; the port in “publish outside” belongs
to the host.

## The “Clusters” section: several hosts

The “New cluster on several hosts” dialog is a placement table: host →
role → **machines** (how many, sizes, image, bridge) or **the host
itself** as a node. The image comes from the host catalogue (“will be
downloaded” if it is not there yet) or from the hub library (below); the
form itself reports a wrong number of control planes, a missing shared
control plane address for kubeadm, and a missing bridge.

### The shared control plane address (kubeadm)

With **kubeadm** nodes join the cluster not by the address of the first
control plane but by a shared one — `--control-plane-endpoint`. The hub
takes it as the `endpoint` field of the cluster API (or the `endpoint`
parameter of the `k8s create` step) — `HOST` or `HOST:PORT`, port 6443 by
default. What it does:

- it goes into `kubeadm init --control-plane-endpoint`, and kubeadm puts
  it into the API certificate of **every** control plane by itself —
  adding it to the TLS SANs separately is not needed;
- nodes join through it (`kubeadm join`) and the kubeconfig is issued
  through it — the address in the cluster card becomes the address of
  the cluster, not of one node;
- with Cilium replacing kube-proxy it is also what the agent gets as
  `k8sServiceHost`/`k8sServicePort` — the endpoint's port, not a hard
  6443;
- **with three control planes it is required**: without a shared address
  the cluster API hangs on the first node, and joining the other two
  changes nothing.

The hub takes the control plane certificate key before every join
(`kubeadm init phase upload-certs`): it lives two hours, so it is not
stockpiled once at cluster creation. If kubeadm prints no key or prints
something that is not one, the job fails with a clear line instead of
sending a node an empty `--certificate-key`.

**k3s** has no shared entry point: nodes join the address of a specific
server, so the `endpoint` field is not accepted for k3s. The cluster form
offers three control planes for both flavors now, but kubeadm without a
shared address does not start.

### The listener for the shared address

The hub does not build a load balancer of its own — **Traffic Fly** is
that: a route `api-<cluster name>` on a gateway whose targets are the
cluster's control planes. The listener is a step of the creation job: it
goes down **before** the first control plane (at the end of `init`
kubeadm waits for the API exactly at the shared address — the `apicheck`
phase) and is removed by the cluster deletion job.

The listener is needed where the shared address points:

- the endpoint address equals the gateway address, or its A records
  contain it — the hub puts the listener on that gateway;
- there are several gateways and the address points at exactly one —
  that one is chosen;
- the address points at no gateway (your own VIP, an external haproxy,
  the name is not in DNS yet) — the step writes to the log that the
  address is served outside the hub, and the job continues: someone
  else's port on the gateway would not help the cluster.

The form fields — **“Shared control plane address”**, **“Where to
listen”** and **“Who else”** — appear for kubeadm only.

![The shared control plane address in the new cluster dialog](/screens/en/hub-cluster-ha.png)

“Where to listen” follows the address by default; a named
gateway is an instruction to put the listener there even if the name does
not point anywhere yet (the log then shows what the address does
resolve to). Over the API these are the `gateway` (the gateway's edge id)
and `allow_extra` fields; in a script — the `gateway GATEWAY` (name or
address) and `allow ADDRESS[,ADDRESS…]` parameters.

The route port is the endpoint's port, the targets are the control planes
on their 6443, and the balancing is by source address (API connections
are long-lived, moving them between nodes would achieve nothing). The
allow list is mandatory: a gateway address is an entry to kube-apiserver
and through it to the etcd of the whole cluster. The hub puts the
outward addresses of the placement hosts in it plus what a person
added; an empty list at Traffic Fly means “everyone”, so an empty list is
a refusal, not an open port. A host behind NAT, whose address is not
visible from outside, is logged with “!” — its nodes would be locked
out, and that should be seen before creating.

**Dry run** shows all of it as a checklist: the gateway and its address,
the port and targets of the route, who is in the allow list, whether the
port on the gateway is free of someone else's route, and “!” where the
address looks somewhere else than a gateway or there is no gateway at
all.

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
- **Client ingress** — a cluster node and the HTTP port of the ingress
  controller where Traffic Fly sends token routes to their namespaces
  (`fly own … cluster … namespace …`, MCP `fly_own_add`); without it no
  route to a client namespace can be made. The window shows a diff, empty
  turns it off.
- **Traffic Fly routes following the nodes** — a route bound to the cluster
  takes its targets from the cluster's nodes: a node arrives, a target
  appears; a node leaves, the target leaves. The target port is the client
  ingress, or the one a human named (NodePort passes through). The rewrite
  runs from the creation and deletion jobs, from the minute watch, and from
  a button: **“Traffic Fly targets”** on the cluster card (it shows when
  the cluster has bound routes, their number is in the tooltip),
  **“Check targets”** inside the route. The shared address listener is
  not rewritten by the automation — it carries control planes only.
  Details — [Traffic Fly](/en/guide/traffic-fly).

  ![The cluster card with the “Traffic Fly targets” button](/screens/en/hub-cluster-fly.png)
- **Client resources** — operator resources (CRDs) clients may create in
  their namespaces: one per line, `resource.group`
  (`registries.registry.rgstr.io`). The hub applies the ClusterRole
  `nkt-client-crds` with the `aggregate-to-edit` and `aggregate-to-admin`
  labels, so the `edit` role of a client namespace gets full rights on
  them. Kubernetes, RBAC, admission, networking, Cilium and Longhorn groups are not
  given; what the operator creates for a client resource passes the same
  admission policies. The window shows a diff; the list goes into the
  export. Example — [rgstr and rgstroperator](/en/guide/hub-plugin-rgstr).
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
  the host, a cluster machine gets the same checkbox by itself (the hub
  keeps the reverse-forward session to the machine, so the machine's
  virtual network is not opened outward), and the same port serves files
  by URL (the k3s installer and binary with airgap images, Cilium CLI,
  the `pkgs.k8s.io` key, machine images, the helm archive and the
  Longhorn chart tarball — an add-on installs on such nodes too) and a
  **registry mirror** (docker.io, quay.io, registry.k8s.io, ghcr.io,
  gcr.io) — containerd on the nodes is configured for it
  automatically. Everything stays on the hub and leaves for the internet
  once; apt on a machine goes through the hub for `https` too — with only
  the http line, `https` mirrors stayed outside the cache.
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
  stable, checksum-verified) is installed and `cilium install` runs; an
  already installed Cilium is not reinstalled — the check looks at
  `ds/cilium` in `kube-system`, and `cilium install` over a release name
  still in use is refused by helm ("cannot reuse a name that is still in
  use"), so without that check a retried failed job tripped over the
  working Cilium; "replace kube-proxy" — `--disable-kube-proxy` for k3s,
  `--skip-phases=addon/kube-proxy` for kubeadm.
- **Longhorn** (the "Longhorn" checkbox and a chart version; in a script —
  `addon longhorn[=1.12.1]`, in the API and MCP —
  `"addons":[{"name":"longhorn","version":"1.12.1"}]`) — block volumes
  replicated across nodes. Installed as the last step, once every node is
  `Ready`:
  - `open-iscsi` and `nfs-common` (ReadWriteMany volumes) are installed by
    **cloud-init on the machine's first boot**, while its outbound TCP still
    works: by the node-prepare step the node already runs Cilium instead of
    kube-proxy, and in the host's NAT network outbound TCP can go silent
    after that while ping and DNS stay fine. On a ready node the packages
    are checked (`dpkg -s`) and fetched only if missing (host nodes,
    machines of older clusters), with short timeouts and one clear log line
    instead of hanging until the job times out; the same step loads the
    `iscsi_tcp` module and starts `iscsid`; if `multipathd` runs, the log
    warns (nkt does not touch its `multipath.conf`);
  - on the first control plane — the chart into `longhorn-system` (helm
    is installed if missing), then waiting for `longhorn-manager`. Both
    helm and the chart tarball come **through the hub cache** when the
    host has the package port open: a node without internet installs the
    add-on over the same channel as k3s and Cilium, and a downloaded
    chart stays on the node at `/var/lib/nkt/addons/` and is reused when
    the add-on is updated. If the chart did not arrive, the install takes
    `longhorn/longhorn` from `https://charts.longhorn.io`, and with the
    cache open the repository commands run through the hub proxy;
  - volume replicas — one per node, at most three (one on a single-node
    cluster);
  - Longhorn becomes the **default** StorageClass: the previous one
    (`local-path` on k3s) loses the flag, and k3s stops managing it from its
    manifest (`local-storage.yaml.skip`) — otherwise a restart would bring
    the flag back.

  **Version:** empty — the nkt default (now 1.12.1); the list is the latest
  patches of the last four branches from the repository `index.yaml`
  (cached for an hour, `GET /api/hub/k8s-addon-versions?addon=longhorn`);
  versions that do not fit the cluster's Kubernetes (the chart's
  `kubeVersion`) cannot be chosen. The dry run checks the version against
  the repository and warns when node disks are under 20 GB — replicas live
  in `/var/lib/longhorn` on the same disk. Workers added later are
  prepared by the same step. The `longhorn.io` group is never given to
  clients ("Client resources") — they use Longhorn only through PVCs.
- **Add-ons of a ready cluster** — the "Add-ons" button: which Longhorn
  version is installed (by `helm` on the control plane), install or upgrade
  as a job (`POST /api/hub/clusters/{id}/addons {"name":"longhorn",
  "version":"1.12.1"}`, MCP `cluster_addons`, `cluster_addon_install`).
  Upgrades go forward only and no further than the next minor branch at a
  time (1.11 → 1.12), as Longhorn requires; the previous values are kept
  (`--reuse-values`).
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
- **NAT and WireGuard** (a node not on a bridge): once the node's machine
  exists, the hub pins its address — a DHCP reservation by MAC in the
  libvirt network (`virsh net-update <net> add-last|modify ip-dhcp-host
  --live --config`) using the address the host sees on it right now. A
  node's address lives in Traffic Fly routes, in port forwards and in the
  kubeconfig, and a lease without a reservation expires: the machine
  reboots, the address changes, and the route target stops resolving. If
  the reservation fails (the network does not answer, the machine has no
  address yet) the log carries a warning and the node creation is not
  cancelled.
- The creation job is a hub job: machines are created the same way as
  "New machine", then the role install, the token, joining the other
  nodes, waiting for `Ready`, the port forward and the kubeconfig. Retry
  API: `POST /api/jobs/{id}/retry`, `POST /api/hub/clusters/{id}/retry`.
