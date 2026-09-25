---
title: Containers and VMs
---

# Containers and virtual machines

One section with tabs: Docker, Podman, LXD and libvirt/QEMU virtual
machines.

## Docker

![Docker containers](/screens/en/containers.png)

- **Containers**: status, image, ports, networks; start, stop, restart,
  remove, logs; create a container; scan compose stacks.
- **Images**: size, date, who uses it; remove, save to a tar on the host,
  prune orphaned layers.
- **Stacks**: the host's compose files — `up`, `down`, `restart`; compose
  is edited in the [config editor](/en/guide/configs) with a `docker
  compose config` check; a new stack from a template.
- No Docker on the host — install it with a button from the official
  docker.com repository (or the get.docker.com script) with a live log.

## Podman and LXD

Podman — containers through its own socket, the same lifecycle as Docker.
LXD — containers and virtual machines with one tool: `launch`, start,
stop, delete.

LXD instance rows show memory and limits (`limits.memory`,
`limits.cpu`), disk, forwarded ports (`proxy` devices) with "probe
port", autostart as one button (`boot.autostart`), **logs** (the journal
inside the instance or LXD's own log) and the console.

Instance **snapshots** open in their own window from the row: take one
(optionally with memory), restore (`lxc restore`), delete. A snapshot
lives on the same storage pool; moving to another host needs a backup.

The instance **configuration** (`lxc config show`/`edit`) is edited in a
window: text, quick `limits.cpu` and `limits.memory` fields, a diff
before writing and version history with rollback. Next to it — the
effective configuration with profiles (`--expanded`), read-only.

**Port forwarding** (a `proxy` device) is added with "+" in the ports
column and removed with the bin next to a port: a form builds the
device, and it is written through the same configuration window — with
a diff and a version in history. Below the instance list are LXD
**networks** (create a bridge, delete a managed network), **images on
the host** (delete, download in advance from `images:` or `ubuntu:` with
live output) and **storage pools** (read-only).

## Libvirt (KVM virtual machines)

![Virtual machines](/screens/en/vms.png)

- libvirt domains: start, shut down, force off, reboot, autostart, delete
  with or without disks; machine addresses.
- **Machine screen** — a "screen" icon on a running machine: VNC right in
  the browser (noVNC), with Ctrl+Alt+Del and a "view only" mode, through
  the hub too. A machine with SPICE graphics only opens over SPICE
  (spice-html5), and the window offers "Add VNC" — an XML edit with a
  diff. A running LXD virtual machine gets the same SPICE screen. Client
  licenses are in `THIRD_PARTY_NOTICES.en.md`.
- **Console** — an icon in the row of a running container (Docker,
  Podman: `exec`, bash or sh, a user can be set), LXD instance (`lxc
  exec`) and machine (serial console `virsh console`, exit with Ctrl+]).
  The web terminal must be enabled.
- **Backup** — an icon in the row of a machine and a container (Docker,
  Podman, LXD via `lxc export`/`lxc import`; a compose container — the
  whole stack): archives on the host in
  `/var/lib/netknownsthat/backups`, creation as a background job,
  download, restore as a copy or over the original. A running machine is
  not stopped — disks move to a snapshot while copying.
- **LXD and Podman** install from their tabs when missing: Podman — apt,
  LXD — snap (snapd is installed first) and `lxd init --auto`. The image
  for a new LXD instance is picked from a list: `images:`, `ubuntu:` or
  those already on the host; container or VM.
- Each row has **one power button** by state (running — "stop",
  stopped — "start", paused — "resume"); restart and pause only on a
  running one.
- **Starting and restarting a container** runs in nkt's command window:
  the `docker start` output, the state a few seconds later and, if the
  container is not running, the tail of its logs with the reason.
  **Logs** — an icon in the row: live `docker logs`, tail 200/1000/5000
  lines, "follow", "timestamps". Same for Podman.
- **Deleting a machine** is one button; "delete the machine's disks too"
  is a checkbox in the confirmation window, off by default.
- **Machine configuration (XML)** — the pencil in the row: "text",
  "history" and
  **"blocks"** tabs (domain elements and devices one by one: disk,
  network interface, graphics — edit, delete, `+ disk`/`+ interface`/
  `+ graphics` with a template); before
  writing, a window shows the **diff** "on disk → draft", and `virsh
  define` runs only after "Write". The pencil opens
  `/etc/libvirt/qemu/<name>.xml` in the config editor, with version
  history, rollback and `virsh define` on save. For a running machine the
  changes take effect after it restarts.
- **New machine** from a cloud image: name, cores, memory, disk, network,
  user and SSH key via cloud-init. Missing tools (`virt-install`,
  `qemu-img`…) install themselves. Machine templates — to create identical
  ones again.
- **Images**: a catalogue of Ubuntu, Debian and other cloud images —
  downloaded with a checksum check; your own images — upload a file, move
  it into the disk directory.
- **libvirt networks**: list, a NAT network with DHCP and autostart (the
  subnet is checked against the host's networks and interfaces), a bridge
  onto a host interface.
- The domain XML is edited in the config editor with a `virt-xml-validate`
  check and applied via `virsh define`.

::: tip Machines from the hub
On the hub a machine is created on a host straight from the host list,
nkt is installed into it automatically and the group profile is applied
right away — see [Hub](/en/guide/hub).
:::

## Kubernetes

On a cluster node (a machine created by the hub via “new cluster”) — the
**Kubernetes** tab: flavor and role, cluster nodes with roles and `Ready`,
pods by namespace, kubeconfig and removing the node from the cluster.

![Kubernetes](/screens/en/kubernetes.png)

