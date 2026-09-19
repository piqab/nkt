---
title: Alerts, jobs, updates
---

# Alerts, jobs, export, updates

## Alerts

![Alerts](/screens/en/hub-alerts.png)

The hub notices transitions itself: a host is **down** / **back**,
**serious findings** appeared / **were fixed**, **a job failed**. An alert
log with settings for what to record and what to notify about; short
episodes (“down” → “back” a couple of minutes later) collapse into one
line “was down for N min”.

The **“Notify on problems”** switch turns on browser notifications — they
arrive in any hub section while the tab is open. The unread counter is on
the menu item.

## Hub jobs

Installing nkt on a host, updating, creating a machine, applying a profile
to a group, a script, a cluster — all of these are hub jobs with a live
log; a job interrupted by a hub restart resumes. A failed job has **“try
again”**: a new job with the same parameters continues from the saved
state (created machines, the tunnel, installed roles are skipped), the
old log stays. Job logs, titles and errors, like alerts, are shown **in
the reader's language** — a job started from a Russian UI reads in
English for an English user; raw tool output stays as is.

## Export and import

“Export” in the host list saves everything the hub knows and that takes
long to set up again: hosts with SSH and admin secrets, groups, machines
with parents, Kubernetes clusters with their nodes and kubeconfig,
profiles and scripts with history, machine templates, settings. Cluster
images (qcow2) are not in the file — the import lists the ones to copy by
hand. The file is encrypted with a password — “import” on a new hub
asks for the same one. `nkt hub import` does the same from the command
line.

## About

![About](/screens/en/hub-about.png)

- The hub version, a check of GitHub releases, **update to the latest** —
  the hub downloads the binary, verifies the checksum and restarts;
  **rollback** to the previous version if something went wrong. The new
  version's notes are shown before installing — in the UI language.
- Hosts whose version differs from the hub's are brought to the hub's
  version when opened; “update all” in the host list — for the ones
  behind.
- The trivy **vulnerability database** is shared by all hosts: updated on a
  schedule and by a button, hosts take it from the hub instead of
  downloading their own.
- **Kubernetes clusters** — on one host or across several, with
  WireGuard between hosts, Cilium, your own images and saved form
  presets: a separate page, [Kubernetes clusters](/en/guide/hub-clusters).
- **Package cache** — hosts download `.deb` files through the hub over a
  reverse SSH forward, every package leaves for the internet once; without
  the hub apt goes direct. A checkbox in the host form, the limit and
  clearing — in “About”. The same port serves files by URL (installers,
  binaries, machine images) and a registry mirror for container images —
  a host without internet gets everything from the hub.
- **ClamAV database** — a copy of the signature database on the hub; on a
  host page, in the “Malware” tab, “database from hub” uploads it over SSH.
## Privacy mode

The **“hide sensitive data”** checkbox in “About” — for
screen sharing and screenshots: addresses and names of hosts and
machines, users, keys and tokens, domains, cluster API addresses, IP/MAC
are blurred on every page and in modal windows; in logs, alerts, findings
and audit — addresses, e-mail, domains and the host names from the list;
terminal, logs and topology — as a whole. Nothing shows on hover — turn
the mode off to read. The mode mark is the orange “nkt” badge in the
header; the state is remembered in the browser.
