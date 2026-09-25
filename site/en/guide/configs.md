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

## Editing

The file on the page is read-only. "Edit" opens a window: the editor, a
note for the edit, "apply" (reload the service), "show changes" and a
"History" tab — versions, a diff of any of them against the current one,
rollback. "Save" first shows the diff "on disk → draft"; only "Write"
writes, with the configuration check and a rollback on failure.

## Block mode

nginx, haproxy, caddy, compose files and machine XML have **blocks**: a
tree of `server`/`location`/`upstream`, `frontend`/`backend`/`listen`,
`site`, for compose — `service`, `network`, `volume`, `secret`, `config`,
for a machine — settings and devices (`disk`, `interface`, `graphics`).
A block is added (`+` with a template), edited and deleted on its own —
without the risk of breaking its neighbours; a diff "on disk → after the
edit" is shown before writing, and the write is validated and rolled
back on failure.