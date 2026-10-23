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
  allow:
    - port: 80
    - port: 443
files:
  - path: /etc/nginx/conf.d/gzip.conf
    content: |
      gzip on;
```

Every section of the format with examples is on the [Profile format](/en/guide/profile-format) page.

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

## The editing window

![The profile editing window: a ready-made profile, “Check” and the result](/screens/en/profile-editor.png)

Editing happens in a window, as with scripts:

- **“Check”** parses the profile with the same code as saving: a typo in a
  field name, a wrong package name or file path is an error with a line;
  on success — what the profile sets (`packages: 1, services: 1, …`).
- **“Save”** is available when the current text passed “Check”; any edit
  needs a new check, and next to the disabled button it says what is
  missing. A diff comes before writing.
- **“Ready-made profiles ▾”** — a base server, an nginx web server, a
  Docker host with a stack — go in whole (with a confirmation if the
  window has edits).
- **“?”** in the window title and on the profile opens [Profile format](/en/guide/profile-format);
  the profile card has “Text” and “Reference” tabs: the same site page,
  without access to the site — the help built into nkt (“Site / From
  nkt”), whose ready-made profiles go into the editor too.

::: tip On the hub
In the hub's “Profiles” section a profile gets a colour and is assigned to a
group when the group is created: machines created in the group are built
from it and tinted with its colour. See [Hub](/en/guide/hub).
:::
