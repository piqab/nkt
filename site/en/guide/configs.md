---
title: Configs
---

# Configs

![Config editor](/screens/en/configs-editor.png)

Every discovered config — nginx, haproxy, caddy, docker compose, systemd,
cron, sshd, netplan, libvirt — by category; a finding in “Findings” jumps
straight to the line.

## Editor

- Line numbers, a change note, a “reload the service after saving”
  switch.
- **Before writing, the config is checked by the service itself**
  (`nginx -t`, `haproxy -c`, `docker compose config -q`,
  `virt-xml-validate`…). If the check fails, the file is automatically
  restored to its previous state.
- **Apply after writing**: `reload`/`restart` of the service, `docker
  compose up`, `virsh define` — with a check as well; netplan is checked
  with `netplan generate` and applied by hand, so you do not lose the
  network.
- **sshd protection**: before writing, nkt checks whether you would still
  get into the host if sshd failed to come up; after writing — that sshd
  accepts connections, otherwise rollback.
- A new file — in the category's directory; writing to directories outside
  the systemd sandbox offers “Allow writes” (a `ReadWritePaths` drop-in
  and a service restart).

## Version history

Every save is a version with an author and a note. Any one can be viewed,
compared with the current one (unified diff) and rolled back to; the
rollback is checked by the service as well.

## Block mode

nginx and haproxy have **blocks**: a tree of `server`, `location`,
`frontend`, `backend`. A block is added, edited and removed on its own —
with no risk of breaking its neighbours. The same mode is used for
compose files and machine XML.
