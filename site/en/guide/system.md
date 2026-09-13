---
title: Packages, disks, files, system
---

# Packages, disks and files, hardware, system

## Packages

- apt package search ranked by match, installing several in one
  transaction; the package description on hover.
- Installed packages as a grid with search and removal.
- Available updates and `apt-get upgrade` in a live terminal window —
  deliberately without `-y`, with a confirmation.
- snap and flatpak packages: list, update, remove.

## Disks and files

- Filesystems with usage (pseudo-filesystems hidden), swap, physical disks
  and partitions.
- **What takes the space**: subdirectory sizes on click, level by level.
- **Files** — a browser over the allowed roots (`/home`, `/srv`, `/opt`,
  `/var/www`, `/tmp`; configurable with `NKT_FILES_ROOTS`): folders,
  rename, delete, download; upload of files and whole folders via a dialog
  or drag and drop with overall progress; unpacking zip/tar in place; `git
  clone` as a job, private repositories included (a token over HTTPS or
  the host's deploy key over SSH); a file editor with line numbers and
  protection against overwriting someone else's change.

## Hardware

Machine, CPU, memory, batteries, temperatures (lm-sensors), PCI and USB
devices.

## System settings

- Hostname, time zone (with search), NTP, the state of automatic security
  updates.
- **Locales**: every locale the system knows, with search; generate the
  selected ones, set the default.
- **Time sync**: which service is installed (systemd-timesyncd, chrony,
  ntpsec), what it syncs with, stratum and offset; NTP server selection;
  “sync now”; installing the service if there is none.
- NetworkManager: connections, devices, Wi-Fi networks, connecting with a
  password.

## Terminal

A full shell on the host in the browser (`NKT_TERMINAL_ENABLED=true`, off
by default — this is direct shell access). tmux sessions survive a closed
tab; detach into a separate window; a toolbar: copy, clear, font size,
search. tmux and dbus helpers install with a button if missing.
