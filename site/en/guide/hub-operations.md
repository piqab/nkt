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
to a group, a script — all of these are hub jobs with a live log; a job
interrupted by a hub restart resumes.

## Export and import

“Export” in the host list saves everything the hub knows and that takes
long to set up again: hosts with SSH and admin secrets, groups, machines
with parents, profiles with history, machine templates, scripts,
settings. The file is encrypted with a password — “import” on a new hub
asks for the same one. `nkt hub import` does the same from the command
line.

## About

![About](/screens/en/hub-about.png)

- The hub version, a check of GitHub releases, **update to the latest** —
  the hub downloads the binary, verifies the checksum and restarts;
  **rollback** to the previous version if something went wrong. The new
  version's notes are shown before installing.
- Hosts whose version differs from the hub's are brought to the hub's
  version when opened; “update all” in the host list — for the ones
  behind.
- The trivy **vulnerability database** is shared by all hosts: updated on a
  schedule and by a button, hosts take it from the hub instead of
  downloading their own.
