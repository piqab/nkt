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
    title: "Finds problems, not just shows things"
    details: "Parses nginx, haproxy, caddy, docker compose, the firewall and certificates, checks them against the live host and explains every finding — file, line, what to do — with a button to where it gets fixed."
    link: "/en/guide/overview#findings"
    linkText: "Learn more"
  - icon: 🗺️
    title: "Resource map"
    details: "A graph “internet → service → listener → pool → container or machine”: the whole traffic path is highlighted, one service's ports fold together, a node's window links to the right section."
    link: "/en/guide/overview#resource-map"
    linkText: "Learn more"
  - icon: 📝
    title: "Configs with history and validation"
    details: "An editor with a diff before saving and validation by the service itself — if rejected, the file goes back as it was; versions, rollback, a block editor for nginx/haproxy, sshd lock-out protection."
    link: "/en/guide/configs"
    linkText: "Learn more"
  - icon: 🔐
    title: "Certificates end to end"
    details: "All certificates from configs and /etc/letsencrypt, checks against the TLS socket, auto-renewal, Let's Encrypt issuance, PEM for haproxy, self-signed."
    link: "/en/guide/certificates"
    linkText: "Learn more"
  - icon: 🧱
    title: "Firewall and fail2ban"
    details: "ufw and firewalld with port 22 protection and a port's rules highlighted; fail2ban jails, bans, exceptions, templates and a config check before applying."
    link: "/en/guide/network#firewall"
    linkText: "Learn more"
  - icon: 🦠
    title: "Vulnerabilities and malware"
    details: "CVEs in OS packages, inside LXD and libvirt guests and in images via trivy — network-reachable first; miners and signs of a break-in without third-party tools; ClamAV as jobs, with quarantine."
    link: "/en/guide/overview#vulnerabilities"
    linkText: "Learn more"
  - icon: 🐳
    title: "Containers"
    details: "Docker, Podman and LXD: compose stacks, “Inspect” with variables and their source, image archives with browser upload, engine install with a button, backups."
    link: "/en/guide/containers"
    linkText: "Learn more"
  - icon: 🖥️
    title: "Virtual machines"
    details: "libvirt machines: creation from images and templates, the VNC and SPICE screen right in the browser, snapshots, cloning, backup and restore."
    link: "/en/guide/containers#libvirt-kvm-virtual-machines"
    linkText: "Learn more"
  - icon: ☸️
    title: "Kubernetes on the host"
    details: "Cluster objects by section, pod logs and console, Helm releases, port forwarding to the browser, node upgrade as a job."
    link: "/en/guide/containers#kubernetes"
    linkText: "Learn more"
  - icon: 📈
    title: "Availability and load"
    details: "Probes of listeners, container ports, machines and Kubernetes services, a downtime heatmap; load of the host, containers, pods and cluster nodes from hourly summaries."
    link: "/en/guide/monitoring"
    linkText: "Learn more"
  - icon: ⌨️
    title: "Terminal, logs, jobs"
    details: "A terminal and btop in the browser, live service and file logs, background jobs with a log, progress and cancel."
    link: "/en/guide/system#terminal"
    linkText: "Learn more"
  - icon: ⚙️
    title: "System"
    details: "Packages and updates, disks and a file browser, OS users, time, network, hardware; a reboot with a preview of what will not come back by itself."
    link: "/en/guide/system"
    linkText: "Learn more"
  - icon: 🖧
    title: "The hub — many hosts"
    details: "Hosts over SSH in one window: groups, installing and updating nkt, narrow sudo with signed operations, a fallback channel, export and import."
    link: "/en/guide/hub"
    linkText: "Learn more"
  - icon: 🔔
    title: "Alerts"
    details: "Host unreachable, new findings, bans, forecasts — with a button straight to the problem on the host; Telegram, Slack, outgoing webhooks."
    link: "/en/guide/hub-operations"
    linkText: "Learn more"
  - icon: 📊
    title: "Hub monitoring"
    details: "Availability and load history of all hosts, containers and Kubernetes nodes, forecasts of disks filling up and memory leaks, hints on what to do."
    link: "/en/guide/hub-monitoring"
    linkText: "Learn more"
  - icon: 🧩
    title: "Profiles and scripts"
    details: "A host's desired state in YAML with a plan and apply, drift as findings; deployment scripts with a dry run and a diagram."
    link: "/en/guide/profiles"
    linkText: "Learn more"
  - icon: ☁️
    title: "Kubernetes clusters from the hub"
    details: "Clusters on machines of one or several hosts, WireGuard, Cilium; manifests and Helm into several clusters at once, node-by-node upgrades."
    link: "/en/guide/hub-clusters"
    linkText: "Learn more"
  - icon: 🚀
    title: "Deployments from Git"
    details: "Compose stacks, manifests, Helm and scripts on a button, CI webhook, polling or a new image tag; a dry run, sites with HTTPS, changing the host and stack name, rollback."
    link: "/en/guide/hub-deploy"
    linkText: "Learn more"
  - icon: 🤖
    title: "Automation and AI"
    details: "API tokens with roles and host scopes, n8n nodes, bots; model analysis of findings, events and the architecture — with your own instructions."
    link: "/en/guide/hub-api"
    linkText: "Learn more"
  - icon: 🔒
    title: "Secure by default"
    details: "One dependency-free binary, a systemd sandbox, argon2id, roles, read-only mode, an audit log of every action, privacy mode, listens on localhost only."
    link: "/en/guide/reference-security"
    linkText: "Learn more"
  - icon: 🌍
    title: "Two languages, three themes"
    details: "The interface and server messages in Russian and English, light, dark and system themes, a terminal UI and a JSON API to everything."
    link: "/en/guide/cli"
    linkText: "Learn more"
---
