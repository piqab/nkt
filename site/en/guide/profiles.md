---
title: Profiles
---

# Profiles

![Profiles](/screens/en/profiles.png)

A profile is the desired state of a host in YAML: which packages must be
installed, which services must run, what must be in files, which ports are
open, who has access, which compose stacks are up. It is not a programming
language: no order, no conditions — a flat list of what should be.

Next to it, the “Services” tab holds the hub's [service descriptions](/en/guide/service-descriptions):
a YAML library with history and delivery to hosts.

```yaml
version: 1
name: web-base
packages:
  - nginx
  - certbot
services:
  nginx:
    enabled: true
    active: true
firewall:
  - allow: 80/tcp
  - allow: 443/tcp
files:
  /etc/nginx/conf.d/gzip.conf: |
    gzip on;
```

- **Plan**: before applying, nkt compares the profile with the host and
  shows what will change; dangerous items (stop a service, remove a
  package) are marked.
- **Apply** — as a job with a log; every item uses the same code as the UI
  buttons.
- **Drift** is checked on a schedule (`NKT_DRIFT_INTERVAL`) and shown in
  “Findings”.
- Version history (a table: No., time, author, note, size, a “current”
  label; each has “Content”, “Difference” with the current or the
  previous one, and “Back to the editor”), export and import, a format
  reference in both languages right in the UI (the “?” icon on the
  profile and in the editing window; the “?” by the section title is this
  page).

::: tip On the hub
In the hub's “Profiles” section a profile gets a colour and is assigned to a
group when the group is created: machines created in the group are built
from it and tinted with its colour. See [Hub](/en/guide/hub).
:::
