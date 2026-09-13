---
layout: home
title: nkt — a control panel for Linux hosts
hero:
  name: nkt
  text: A control panel for Linux hosts
  tagline: One static binary shows what is really going on on a host — configs, services, containers, firewall, certificates — and lets you fix it from the browser. The hub gathers many hosts into one window.
  image:
    src: /screens/en/overview.png
    alt: Host overview in nkt
  actions:
    - theme: brand
      text: Install
      link: /en/guide/getting-started
    - theme: alt
      text: Guide
      link: /en/guide/hub
    - theme: alt
      text: GitHub
      link: https://github.com/piqab/nkt
features:
  - icon: 🔎
    title: Finds problems, not just lists things
    details: Parses nginx, haproxy, caddy, docker compose, firewall and certificates, checks them against the live host state and explains every finding — file, line, what to do.
  - icon: 🧩
    title: Everything on one screen
    details: Services, Docker/Podman/LXD containers, virtual machines, packages, disks, files, interfaces, hardware, system settings — with actions, not only lists.
  - icon: 📝
    title: Configs with history and checks
    details: An editor that validates with the service itself before writing, versions, diff, rollback, a block editor for nginx/haproxy, sshd protection against locking yourself out.
  - icon: 🔐
    title: Certificates in full
    details: Every certificate from the configs and /etc/letsencrypt, comparison with the TLS socket, auto-renewal, Let's Encrypt issuance, PEM for haproxy, self-signed ones.
  - icon: 🛡️
    title: Firewall and vulnerabilities
    details: ufw and firewalld with port 22 protection, iptables as is; OS package and container image CVE scans via trivy.
  - icon: 📈
    title: Monitoring
    details: Availability of every listener on a schedule, a downtime heatmap, load graphs, live logs, a terminal and btop in the browser.
  - icon: 🖧
    title: Hub — many hosts
    details: Hosts over SSH, groups, alerts, jobs, desired-state profiles, virtual machines from the hub, deployment scripts, export and import of everything.
  - icon: 🌍
    title: Two languages, three themes
    details: UI and server messages in English and Russian, light, dark and system themes, a terminal UI and a JSON API to everything.
  - icon: 🔒
    title: Safe by default
    details: One dependency-free binary, a systemd sandbox, argon2id, roles, a read-only mode, an audit log of every action, listens on localhost only.
---
