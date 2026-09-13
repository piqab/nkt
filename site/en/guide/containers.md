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
