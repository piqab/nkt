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
      link: /en/guide/intro
    - theme: alt
      text: GitHub
      link: https://github.com/piqab/nkt
features:
  - icon: 🔎
    title: Finds problems, not just lists things
    details: Parses nginx, haproxy, caddy, docker compose, firewall and certificates, checks them against the live host state and explains every finding — file, line, what to do — with a button to where it gets fixed.
  - icon: 🧩
    title: Everything on one screen
    details: Services, Docker/Podman/LXD containers, libvirt machines with a VNC/SPICE screen in the browser, backups and snapshots, packages, disks, files, interfaces, hardware, system settings — with actions, not only lists; long operations run as background jobs.
  - icon: 📝
    title: Configs with history and checks
    details: An editor with a diff before writing and validation by the service itself — if it rejects the file, the file goes back as it was; versions, diff, rollback, a block editor for nginx/haproxy, sshd protection against locking yourself out.
  - icon: 🔐
    title: Certificates in full
    details: Every certificate from the configs and /etc/letsencrypt, comparison with the TLS socket, auto-renewal, Let's Encrypt issuance, PEM for haproxy, self-signed ones.
  - icon: 🛡️
    title: Firewall, vulnerabilities, malware
    details: ufw and firewalld with port 22 protection; CVEs in the host's OS packages, inside LXD and libvirt guests and in images via trivy, network-reachable ones first; miners and signs of a break-in with no third-party tools, ClamAV with quarantine.
  - icon: 📈
    title: Monitoring
    details: Availability of listeners, container ports and machines on a schedule, a downtime heatmap, container and machine load, live logs, a terminal and btop in the browser.
  - icon: 🖧
    title: Hub — many hosts
    details: Hosts over SSH, groups, alerts, jobs, desired-state profiles, virtual machines and Kubernetes clusters from the hub, deployment scripts, export and import of everything.
  - icon: 🚀
    title: Deployments from Git
    details: Applications from a repository — as a manifest, a Helm release, a compose stack or a script — on a button, a CI webhook, polling or a new image tag; a dry run with selectable checks, sites with HTTPS, history and rollback, nkt-edge as a way in without exposing the hub.
  - icon: 🤖
    title: Automation and bots
    details: API tokens with roles and host scopes, n8n nodes, outgoing alert webhooks, Telegram and Slack bots with buttons and commands — all of it working with a hub behind NAT.
  - icon: 🌍
    title: Two languages, three themes
    details: UI and server messages in English and Russian, light, dark and system themes, a terminal UI and a JSON API to everything.
  - icon: 🔒
    title: Safe by default
    details: One dependency-free binary, a systemd sandbox, argon2id, roles, a read-only mode, an audit log of every action, listens on localhost only.
---
