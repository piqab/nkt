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

## The creation steps window

The main button of the form is **“Next: steps”**: before anything starts,
a checklist of all the job's actions opens — the host checks, the tunnel
between hosts (wireguard), **each machine as its own step**, the shared
address listener, the first control plane (init + CNI), **each node join
as its own step**, waiting for `Ready` with the port forward and the
kubeconfig, the addons (Longhorn). Everything is ticked by default; an
unticked step is not run, and the log carries a “skipped” line.

The same window opens on retry — the **“Continue”** button on the card of
a failed cluster, or “Try again” in the creation job log. In it **the
completed steps are unticked**: what is already done (machines, the
tunnel, the listener, installed roles, addons) the job remembers, and the
operator edits the continuation — adding or removing steps. The resume
state is not lost: the new job takes the addresses and machine ids from
it.

- **Dependency conflicts** are highlighted red on the spot, and
  “Continue” stays disabled until they are gone: a ticked step must have
  its hard dependencies ticked or already completed (a node join without
  the first control plane, addons without `Ready`, a machine without the
  tunnel in wireguard mode). The hub validates the choice on the receiving
  side too — the window is not the only entry point (API, script): an
  empty choice, an unknown step and a conflict are all rejected.
- **Forced repeat**: re-ticking a *completed* destructive step (the first
  control plane, a node join) runs it with a node reset — `kubeadm reset`
  before init/join (for k3s — the k3s uninstall script), the role is
  installed from scratch, and the “already initialized/joined” guards do
  not fire. The window marks such a step “with a node reset”: this is the
  operator's decision — for example when the node reported success last
  time but never became an etcd member.
- The “listener is deployed” mark is independent of “cp-1 is installed”:
  init could have failed after the listener, and repeating cp1 does not
  require re-deploying the route (and repeating the listener alone, with
  no cp1 and no joins, expects nothing from the nodes).
- Jobs created before the steps window existed (and “+ worker”) keep
  working the old way: the resume state decides what to skip.

## The “Clusters” section: several hosts

The “New cluster on several hosts” dialog is a placement table: host →
role → **machines** (how many, sizes, image, bridge or device) or **the
host itself** as a node. The image comes from the host catalogue (“will be
downloaded” if it is not there yet) or from the hub library (below); the
form itself reports a wrong number of control planes, a missing shared
control plane address for kubeadm, a missing bridge, and a machine row
with no device in macvtap mode.

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
- the gateway runs on a host whose machines of this cluster are plugged
  into macvtap — the hub does not put the listener through it and says
  why in the log (a host cannot talk to its own macvtap guests): the
  shared address has to lead to a node, not to an address the placement
  host cannot reach.

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
shape of a target depends on the network mode: a host node and a machine
in NAT, bridge or WireGuard become “the host itself” and “the machine”
(the gateway resolves the machine's address on its placement host), while
**a machine in macvtap also becomes “the host itself”** — the host cannot
be reached by network, but its own record in the gateway's tunnel is
visible and alive. The allow list is mandatory: a gateway address is an
entry to kube-apiserver and through it to the etcd of the whole cluster.
The hub puts the outward addresses of the placement hosts in it plus what
a person added; an empty list at Traffic Fly means “everyone”, so an empty
list is a refusal, not an open port. A host behind NAT, whose address is
not visible from outside, is logged with “!” — its nodes would be locked
out, and that should be seen before creating.

A macvtap machine needs its own line in that list: it arrives at the
gateway from the address its own network gave it (usually the site's
router), not from the parent host's address. The two may coincide, but
nothing guarantees it, so the hub warns about this in the checklist and in
the job log, and a person adds the address under “who else” — the hub does
not guess it and never puts foreign addresses into the list. A gateway
checks a target's liveness from itself, so “the target is alive” does not
mean “the node is allowed in”: dead targets and a route with no connection
at all are explained line by line in the log.

**Dry run** shows all of it as a checklist: the gateway and its address,
the port and targets of the route, the target shape for the network mode,
who is in the allow list, whether the port on the gateway is free of
someone else's route, and “!” where the address looks somewhere else than
a gateway or there is no gateway at all.

**Network between hosts** — how nodes on different hosts see each other:

| Mode | When | Needs |
|---|---|---|
| **NAT** | all nodes on one host | nothing: the host's libvirt network |
| **Bridge** | hosts in one L2 segment | a bridge on every host with machines; machine addresses come from the hosts' network |
| **WireGuard** | hosts in different networks | UDP 51820 between the hosts: the hub generates keys, installs `wireguard-tools`, brings up `wg-quick@nktwg<N>` and a separate machine network on every host; peer subnets go through the tunnel |
| **macvtap** | the machine needs an address in the hosts' own network and there is no bridge on it | a device on every host with machines (picked in the placement row from the ones that are up); no libvirt network, no bridge; the machine's address comes from that network's DHCP |

**macvtap** plugs the machine straight into a host device (libvirt
`type='direct'`, `bridge` mode), past the host's bridges and networks: it
stands in the same network as the hosts, and peers see it by a real
address. The list offers the host's devices that are up, with their
addresses (service ones — tunnels, bridges, libvirt networks and `lo` —
are not offered); the host agent answers for whether a device fits, and a
**dry run** marks it right in the host's row: missing, not up, a loopback
or already a macvlan/macvtap. In a real run the check is the same one and
runs in the same place — when the machine is created — so picking
something from the list does not promise the creation will pass.

![macvtap mode: a device in every placement row](/screens/en/hub-cluster-macvtap.png)

Two consequences of the macvlan limitation have to be accepted before
creating: **a host cannot talk to its own macvtap machines** (this is a
Linux limitation, not a setting), and **the hub has nothing to pin the
machine's address with** — there is no libvirt network with its DHCP and
reservation tables here.

- **Port forwarding from the host** into such machines does not work and
  is not offered: DNAT would run from a peer that is not in the
  receiver's network. Outward the cluster is led by the shared control
  plane address — a Traffic Fly gateway serves it, not the placement host;
  if the gateway stands on a host whose machines are in macvtap, the hub
  skips the listener with an explicit line in the log and asks for
  another gateway. Such a cluster still reaches its nodes through the
  gateway: the listener builds its targets as “the host itself”, from the
  machines' records, not as “the machine”.
- **The host cannot see these machines either** — no SSH to them, no
  address read from them, no port check. That is why the hub takes the
  node's address from `qemu-guest-agent`, which is installed into the
  machine together with nkt, and uses the same address for kubeadm's
  `--node-ip`, `--advertise-address` and
  `--apiserver-advertise-address`: a machine outside a libvirt network has
  neither a DNS name the peers can resolve nor an address everyone sees
  the same way — without this, kubelet advertises an address the peers do
  not have, and the control plane join fails in the etcd check.
- **The agent starts on the very first boot.** cloud-init installs the
  guest agent package in its final stage and does not start the service —
  the unit only comes up on the next boot, so a machine that already had
  an address stayed address-less for the hub. Now the machine gets, along
  with the package, a first-boot script that enables the agent: the
  address is known right away and the machine does not need rebooting. If
  the agent still keeps silent, the hub reports "the guest agent does not
  answer" with virsh's own words, not "the network has no DHCP lease" — in
  macvtap there is no lease to look for. Silence usually means a machine
  created by an older host agent (before 1.18.1): start the service inside
  the machine (`systemctl enable --now qemu-guest-agent`) or reboot it.
- **The network administrator pins the address**: a DHCP reservation by
  the machine's MAC or a static address on the node. In the creation log
  this is its own line — the next DHCP lease would move the address and
  the node would drop out of the cluster: Traffic Fly routes, the port
  forward and the kubeconfig all hold exactly that address.

WireGuard works when the hosts can reach each other by address (LAN,
VPN, public IPs — then open UDP 51820 in the external firewall). If the
hub reaches a host by an address the peers cannot see, set a **“host
address for peers”** on it. Hosts whose networks cannot see each other at
all are not connected by the tunnel yet — a relay through the hub is
planned ([TODO](https://github.com/piqab/nkt/blob/main/TODO.md)). Node
traffic never goes through the hub — the hub only hands out keys and
configs.

The **dry run** checks every host — KVM and libvirtd, memory and cores,
ports, required packages, the image, the bridge, the WireGuard address and
the macvtap device, ping between hosts, link availability — and writes a
checklist (✓ / ! /
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
- **Try again / Continue** — a failed creation job opens the [steps
  window](#the-creation-steps-window): the completed steps are unticked,
  the continuation can be edited, and repeating a completed destructive
  step resets the node.
- Before a role is installed the node is updated to the hub's nkt
  version; on retry the leftovers of a failed `kubeadm init/join` are
  removed automatically — the node's state is judged by the control
  plane manifests (below).
- Deleting a cluster removes the machines, the tunnel and the machine
  network; a host node stays in the host list.

Scripts do the same — `k8s create` with `nodes "hv1: cp 1, w 2; hv2: w 2;
hv3: host w"`, `network nat|bridge|macvtap|wireguard`, `nic DEVICE` (or
`nic DEVICE` in a host's row — a device of its own per host), `version
1.34`, `image hub:<file>`, `endpoint ADDRESS` ([Scripts](/en/guide/hub-scripts)).

## Installation details

- **kubeadm** installs packages from `pkgs.k8s.io` of the chosen branch;
  the branch is recorded in the cluster, and workers added later get the
  same one. Old branches like v1.31 don't work on Debian 13: `sqv`
  rejects their signature as outdated.
- **Retrying "install role"** decides whether the node is ready by its
  manifests, not by `kubelet.conf`: `kubeadm join --control-plane` writes
  `kubelet.conf` and the apiserver/controller-manager/scheduler manifests
  already in the `control-plane-prepare` phase, and only becomes an etcd
  member — writing `etcd.yaml` — in the `etcd-join` phase, which is exactly
  where the join used to fail when a peer's etcd address was unreachable.
  Manifests without `etcd.yaml` mean a half-finished node: it is reset and
  rejoined, not declared ready (with the kubelet running such a node looked
  joined, and a green step left a broken control plane). The reset is a
  cascade: first the plain `kubeadm reset -f`, then the same without
  removing the etcd member — that first phase is what fails when etcd is
  unreachable, so reset aborted on it, the manifests stayed on disk and the
  next attempt refused with
  `FileAvailable--etc-kubernetes-manifests-*.yaml` — then node cleanup
  alone. The reset output stays in the job log: if the state cannot be
  removed, the step fails with a reason instead of pretending it cleaned.
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
  then points to the host and the chosen API port. Forwarding into nodes
  plugged into macvtap does not work — the host cannot talk to them — so
  the form does not offer it while any machine stands in a device.
- **WireGuard**: interface `nktwg<N>` with address `10.200.<N>.<i>/24`,
  machine network `10.<100+N>.<i>.0/24`; `PostUp` opens the UDP port,
  allows FORWARD through the tunnel and removes libvirt masquerading
  between machine subnets — nodes see each other by their real
  addresses. Before creating machines the hub checks every pair of hosts
  over the tunnel; without a handshake the log says "UDP 51820 is closed
  or the address is wrong".
- **Bridge and macvtap**: a machine's address is found through
  `qemu-guest-agent`, which cloud-init installs on first boot — outside a
  libvirt network the host does not see its machine's address (on macvtap
  it cannot see it at all), and without the agent the hub would not know
  where to reach the node. The package does not start the service itself,
  so the machine also gets a script in that same first boot which enables
  the agent: the address is known before the hub goes to join the node. A
  silent agent is reported as a silent agent, not as a missing DHCP lease.
- **macvtap**: there is nothing to pin the address with — the network
  here is the hosts' own, with its DHCP, not a libvirt network with
  reservation tables. The hub writes a line to the job log that the
  network administrator has to pin the address (a reservation by MAC or a
  static address on the node) and uses that address itself for kubeadm's
  `--node-ip` and advertise addresses.
- **NAT** (a node in a libvirt network): once the node's machine
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
  nodes, waiting for `Ready`, the port forward and the kubeconfig. Steps
  window API: `POST /api/hub/clusters/steps-preview` — the checklist for
  the creation form (the body is the cluster spec, no step completed),
  `GET /api/hub/clusters/{id}/steps` — the checklist of the last job
  (`job_id`, `can_retry`); retry: `POST /api/jobs/{id}/retry`, `POST
  /api/hub/clusters/{id}/retry` with an optional
  `{"steps":["cp1","join:k8s-cp-2",…]}` — without `steps` the retry works
  as before, from the resume state; cluster creation accepts `steps` in
  the same body as the spec.
