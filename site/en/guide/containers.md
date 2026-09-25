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

## Virtual machines

![Virtual machines](/screens/en/vms.png)

- libvirt domains: start, shut down, force off, reboot, autostart, delete
  with or without disks; machine addresses.
- **Backup** — an icon in the row of a machine and a container (Docker,
  Podman; a compose container — the whole stack): archives on the host in
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

