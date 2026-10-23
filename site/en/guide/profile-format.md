---
title: Profile format
---

<!-- This page is built from nkt code (internal/profile): do not edit it by hand — NKT_UPDATE_DOCS=1 go test ./internal/profile -run TestProfileFormatPage -->

# Profile format

A profile is a YAML description of a host's desired state: which packages must be installed, which services must run, what belongs in config files, which ports are open, who has access, and which compose stacks are up. It is not a programming language — no execution order, no conditions, no variables: just a flat list of what must be true.

A profile is additive and never turns off what it does not mention: a package missing from the list is not removed, an unmentioned service is not stopped. So “not specified” and “must be off” are different things — for services that is the difference between leaving the key out and writing “active: false”.

The flow is: edit the description and save it, press “plan” — the host compares the description with reality and lists the differences one by one; untick anything you do not want applied and press “apply” — each item runs as a background job with its own log. A saved profile is then checked on its own, and drifted state shows up among the host's findings.

How profiles are stored, applied and assigned to groups — [Profiles](/en/guide/profiles).

## In short

| Section | What it sets |
|---|---|
| [Header: version and name](#sec-head) | version is the format version (1 today); name is the profile's name, shown in the list and in the job log. |
| [packages — packages](#sec-packages) | Packages that must be installed; installed with apt. |
| [services — systemd units](#sec-services) | Each unit has two independent flags: enabled (starts at boot) and active (running right now). |
| [files — config files](#sec-files) | The full content, not a hash: a hash shows a difference but gives nothing to fix it with. |
| [firewall — open ports](#sec-firewall) | Lists the ports that must be reachable. |
| [users — accounts](#sec-users) | The account is created if missing; sudo: true grants passwordless sudo; keys are added to authorized_keys. |
| [system — hostname and timezone](#sec-system) | The hostname shows up in certificates, logs and the shell prompt, so changing it is flagged as risky. |
| [compose — services from docker compose](#sec-compose) | A stack is described in full: name is the stack name (also the directory name and the docker project name), content is the compose file itself, up says whether the stack must be running (yes by default). |

## Sections

### Header: version and name {#sec-head}

version is the format version (1 today); name is the profile's name, shown in the list and in the job log.

Example:

```yaml
version: 1
name: web-server
```

### packages — packages {#sec-packages}

Packages that must be installed; installed with apt. Extra ones are never removed — a profile only adds.

Example:

```yaml
packages:
  - nginx
  - btop
```

### services — systemd units {#sec-services}

Each unit has two independent flags: enabled (starts at boot) and active (running right now). Specify only what matters to you: a missing key means “none of my business”, false means “must be off”.

Example:

```yaml
services:
  nginx:
    enabled: true
    active: true
  apache2:
    active: false
```

### files — config files {#sec-files}

The full content, not a hash: a hash shows a difference but gives nothing to fix it with. Writing goes through the same path as a manual edit: the content is validated by the service itself (nginx -t, docker compose config, …), recorded in the version history, and rolled back if validation fails. mode is octal; without it a new file is created 0644 and an existing file keeps its permissions.

Example:

```yaml
files:
  - path: /etc/motd
    mode: "0644"
    content: |
      Managed by nkt
```

### firewall — open ports {#sec-firewall}

Lists the ports that must be reachable. The profile does not decide whether the host uses ufw or firewalld — the host does. proto defaults to tcp; from narrows the source to an address or subnet. A rule for port 22 is flagged as risky: it can cut off your own access.

Example:

```yaml
firewall:
  allow:
    - port: 443
    - port: 5432
      proto: tcp
      from: 10.0.0.0/24
```

### users — accounts {#sec-users}

The account is created if missing; sudo: true grants passwordless sudo; keys are added to authorized_keys. Other keys are never removed — a person may have put them there, not a forgotten profile. Granting sudo and touching keys are flagged as risky.

Example:

```yaml
users:
  - name: deploy
    sudo: true
    keys:
      - "ssh-ed25519 AAAA... deploy@laptop"
```

### system — hostname and timezone {#sec-system}

The hostname shows up in certificates, logs and the shell prompt, so changing it is flagged as risky. timezone is a tzdata zone name.

Example:

```yaml
system:
  hostname: web-1
  timezone: Europe/Moscow
```

### compose — services from docker compose {#sec-compose}

A stack is described in full: name is the stack name (also the directory name and the docker project name), content is the compose file itself, up says whether the stack must be running (yes by default). Without an explicit path the file goes to /srv/compose/&lt;name&gt;/docker-compose.yml — its own directory, so applying a profile never overwrites what you maintain by hand. The plan shows two separate items — write the file and bring the stack up — so it is visible when running containers get recreated. up: false runs docker compose down; volumes are left alone. If the host has no docker, the plan offers to install it as its first item — the official way, from docker.com's repository: the distro package lags behind and may ship no compose plugin at all. It is a separate item and can be unticked.

Example:

```yaml
compose:
  - name: shop
    up: true
    content: |
      services:
        web:
          image: nginx:1.27
          ports: ["8080:80"]
```

## What a profile does not do

- It never removes packages, services, files or accounts that the description does not mention.
- It does not watch a file after applying: the next check simply reports the difference.
- It expresses no ordering or dependencies — plan items run top to bottom, as shown.
- It never deletes data: docker compose down keeps volumes, and removing a disk or a directory stays a separate, manual step.

## Ready-made profiles

### A base server

Console tools, the time zone, a deploy account with a key, and SSH open.

```yaml
version: 1
name: base
packages:
  - htop
  - curl
  - ncdu
system:
  timezone: Europe/Moscow
users:
  - name: deploy
    keys:
      - "ssh-ed25519 AAAA... deploy@laptop"
firewall:
  allow:
    - port: 22
```

### An nginx web server

nginx installed, enabled and running, ports 80 and 443 open, the login greeting from the profile.

```yaml
version: 1
name: web-server
packages:
  - nginx
services:
  nginx:
    enabled: true
    active: true
firewall:
  allow:
    - port: 80
    - port: 443
files:
  - path: /etc/motd
    mode: "0644"
    content: |
      web server, managed by nkt
```

### A Docker host with a stack

A compose stack with a web server is up; if the host has no Docker, the plan offers to install it as the first item.

```yaml
version: 1
name: docker-host
compose:
  - name: web
    up: true
    content: |
      services:
        web:
          image: nginx:1.27
          ports: ["8080:80"]
          restart: unless-stopped
```
