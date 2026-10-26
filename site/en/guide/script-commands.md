---
title: Script commands
---

<!-- This page is built from nkt code (internal/script): do not edit it by hand — NKT_UPDATE_DOCS=1 go test ./internal/script -run TestScriptCommandsPage -->

# Script commands

Every command of the hub script language — the same ones “Check”, the “Reference” tab and AI know: the page is built from the command descriptions in nkt code and never falls behind it. How scripts are stored, checked and run — [Scripts](/en/guide/hub-scripts).

## In short

**Variables and questions at start**

| Command | What it does |
|---|---|
| [`set NAME value`](#cmd-set) | A variable: ${NAME} further down is replaced with the value. |
| [`param NAME "prompt"`](#cmd-param) | A parameter: its value is asked at run time, ${NAME} below is replaced with it. |

**The hub: groups, hosts, install, waiting**

| Command | What it does |
|---|---|
| [`group NAME`](#cmd-group) | Creates a host group on the hub; with a profile, machines created in it are built from it. |
| [`host NAME ADDR[:PORT] user USER`](#cmd-host) | Registers a host on the hub by address and SSH credentials — the same as “Add host”. |
| [`install HOST…`](#cmd-install) | Installs nkt on the listed hosts and waits until each one answers. |
| [`wait HOST online`](#cmd-wait) | Waits until the host is “online” (after an install or a reboot). |

**On a host (on HOST …)**

| Command | What it does |
|---|---|
| [`on HOST… packages install\|remove PACKAGE…`](#cmd-packages) | Installs or removes apt packages on the host in one transaction. |
| [`on HOST service NAME start\|stop\|restart\|reload\|enable\|disable`](#cmd-service) | Controls a systemd service: start, stop, restart, reload, autostart. |
| [`on HOST firewall allow\|deny PORT[/tcp\|udp]`](#cmd-firewall) | A ufw/firewalld rule: allow or deny a port, optionally only from an address. |
| [`on HOST docker install`](#cmd-docker-install) | Installs Docker from the official docker.com repository. |
| [`on HOST docker stack PATH up\|down\|start\|stop`](#cmd-docker-stack) | Brings a compose stack up or down, starts or stops it. |
| [`on HOST vm create NAME image …`](#cmd-vm-create) | Creates a virtual machine on the host from a cloud image — like the “New machine” dialog. |
| [`on HOST vm start\|shutdown\|destroy\|reboot\|delete NAME · …`](#cmd-vm-action) | Starts, shuts down, force-stops, reboots or deletes (with its disks and record) a machine on the host; a snapshot and reverting to it; vCPU, memory and disk (the disk only grows; vCPU and memory take effect after shutdown and start). |
| [`on HOST apply profile PROFILE`](#cmd-apply) | Applies a hub profile to the host in full: builds the plan and executes every item. |
| [`on HOST file put PATH`](#cmd-file-put) | Writes a file to the host from the block (through the config editor: with a check and history when it knows the path). |
| [`on HOST user add NAME`](#cmd-user-add) | Creates an account on the host with an SSH key and, optionally, passwordless sudo. |
| [`on HOST system hostname NAME`](#cmd-system) | System settings: machine name, time zone, default locale (generated if needed), NTP servers with an immediate sync. |
| [`on HOST cert issue DOMAIN…`](#cmd-cert) | A Let's Encrypt certificate via certbot --standalone: services and processes on 80/443 are stopped for the validation and started again. |
| [`on HOST git clone URL /DIR`](#cmd-git-clone) | Clones a repository onto the host as a file-browser job; private ones with a token asked at run time or the host's deploy key. |
| [`on HOST k8s create NAME`](#cmd-k8s-create) | Creates a Kubernetes cluster on the host's virtual machines — like “new cluster” in the host list: machines from a cloud image with nkt, control plane, joining nodes, waiting for Ready, port forward and kubeconfig (downloadable on the hub in the cluster card). |
| [`on HOST k8s destroy NAME`](#cmd-k8s-destroy) | Deletes the cluster: all its machines with disks and the record on the hub. |

**Pipelines, deployments, moves**

| Command | What it does |
|---|---|
| [`pipeline NAME`](#cmd-pipeline) | Saves a deployment pipeline — like “Save” in the pipeline window: creates it if missing, otherwise a new version of the description. |
| [`deploy PIPELINE`](#cmd-deploy) | Deploys a pipeline — like the “Deploy” button — and waits for the result; the deployment log goes into the script log. |
| [`migrate HOST CONTAINER to HOST`](#cmd-migrate) | Moves a stack to another host — like dragging on the hosts map: a plan (obstacles are an error before the stack stops), stop, data through the hub, start on the new host; a failed move rolls back by itself. |

**Clusters and Traffic Fly**

| Command | What it does |
|---|---|
| [`k8s CLUSTER ingress cilium`](#cmd-k8s-ingress) | Turns on the Cilium Ingress controller in host network mode: Envoy listens on the port on every node (no LoadBalancer), Cilium is the default controller; the Cilium version stays the same. |
| [`k8s CLUSTER apply`](#cmd-k8s-apply) | Applies a manifest (kubectl apply) on the first control plane — like “Manifests” in “Clusters”; in a dry run — kubectl diff. |
| [`k8s CLUSTER wait deployment NAME`](#cmd-k8s-wait) | Waits until all replicas of a deployment are ready. |
| [`fly route ID KIND`](#cmd-fly-route) | Creates or updates a Traffic Fly route and applies it — like a pipeline's fly: block: a new one is saved and applied, an existing one changes targets smoothly (new ones added and checked, old ones drained). |
| [`fly apply`](#cmd-fly-apply) | Applies the Traffic Fly map — like “Apply”; in a dry run — the apply dry run. |

**Plugins**

| Command | What it does |
|---|---|
| [`plugin NAME WORD args…`](#cmd-plugin) | A command of a connected plugin (“About” → “Plugins”): the plugin's command word and its arguments. |
| [`on HOST run "COMMAND"`](#cmd-run) | A command on a plugin host (MikroTik, Windows and other devices added through a plugin) in its type's language: a RouterOS script, PowerShell. |

## Commands

### `set NAME value | set NAME ask` {#cmd-set}

A variable: ${NAME} further down is replaced with the value.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | name — Latin letters, digits, underscore |
| `value` | ✓ | everything up to the end of the line |
| `ask` |  | “set NAME ask” — the value is asked at run time and never written to the text or the log (passwords, tokens) |

Example:

```
set IMAGE ubuntu-24.04
set DB_PASS ask
on web1 vm create app1 image ${IMAGE}
```

### `param NAME "prompt"` {#cmd-param}

A parameter: its value is asked at run time, ${NAME} below is replaced with it. That is how one script deploys different stands.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | name — Latin letters, digits, underscore |
| `prompt` |  | what to show in the question |

Example:

```
param ADDR "address of the new server"
param NAME "host name on the hub"
host ${NAME} ${ADDR} user root password ask
```

### `group NAME [profile PROFILE]` {#cmd-group}

Creates a host group on the hub; with a profile, machines created in it are built from it.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | group name |
| `profile` |  | a hub profile by name |

Example:

```
group web-farm profile web-base
```

### `host NAME ADDR[:PORT] user USER (password ask | password "…" | key hub) [group GROUP]` {#cmd-host}

Registers a host on the hub by address and SSH credentials — the same as “Add host”.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | host name on the hub; later lines refer to it |
| `ADDR` | ✓ | IP or DNS name, port after a colon (22 by default) |
| `user` | ✓ | SSH user (root or one with sudo) |
| `password ask` |  | the password is asked when the script runs; it never goes into the text |
| `password "…"` |  | the password right in the text — for test stands only: the script is stored in the clear |
| `key hub` |  | log in with the hub's key; its public part must be put on the host beforehand |
| `group` |  | the host's group (created above with “group” or already existing) |

Example:

```
host web1 192.0.2.10 user root password ask group web-farm
host web2 192.0.2.11:2222 user deploy key hub
```

### `install HOST…` {#cmd-install}

Installs nkt on the listed hosts and waits until each one answers.

| Argument | Required | What it is |
|---|---|---|
| `HOST` | ✓ | one or more hosts |

Example:

```
install web1 web2
```

### `wait HOST online [DURATION]` {#cmd-wait}

Waits until the host is “online” (after an install or a reboot).

```
wait HOST online [DURATION]
wait HOST port PORT [DURATION]
wait HOST http URL [CODE] [DURATION]
wait HOST service NAME active [DURATION]
wait url URL [CODE] [DURATION]
```

| Argument | Required | What it is |
|---|---|---|
| `HOST` | ✓ | host |
| `online` |  | the host answers the hub (after an install or a reboot) |
| `port` |  | the host's port accepts connections — checked from the hub, from outside |
| `http` |  | the URL answers with the code (200 by default); the name from the URL goes into Host, the certificate is not verified |
| `service` |  | the systemd service is active |
| `url` |  | url URL — an outside address: a request from the hub to the real name (DNS, gateway, certificate are verified), wait for a response with this code; no host needed |
| `DURATION` |  | how long to wait: 90s, 5m (5m by default) |

Example:

```
wait web1 online 5m
wait web1 port 443 30s
wait web1 http https://example.org/ 200 1m
wait web1 service nginx active
wait url https://git.example.com/api/healthz 200 10m
```

### `on HOST… packages install|remove PACKAGE…` {#cmd-packages}

Installs or removes apt packages on the host in one transaction.

| Argument | Required | What it is |
|---|---|---|
| `install\|remove` | ✓ | install — add, remove — delete |
| `PACKAGE` | ✓ | package names separated by spaces |

Example:

```
on web1 packages install nginx htop
```

### `on HOST service NAME start|stop|restart|reload|enable|disable` {#cmd-service}

Controls a systemd service: start, stop, restart, reload, autostart.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | unit name without .service |
| `action` | ✓ | start, stop, restart, reload, enable, disable |

Example:

```
on web1 service nginx restart
```

### `on HOST firewall allow|deny PORT[/tcp|udp] [from CIDR]` {#cmd-firewall}

A ufw/firewalld rule: allow or deny a port, optionally only from an address.

| Argument | Required | What it is |
|---|---|---|
| `allow\|deny` | ✓ | allow — permit, deny — block |
| `PORT` | ✓ | port and protocol: 443, 443/tcp, 53/udp |
| `from` |  | source: an IP or a 10.0.0.0/24 network |

Example:

```
on web1 firewall allow 443/tcp
on web1 firewall allow 5432 from 10.0.0.0/24
```

### `on HOST docker install` {#cmd-docker-install}

Installs Docker from the official docker.com repository.

Example:

```
on web1 docker install
```

### `on HOST docker stack PATH up|down|start|stop` {#cmd-docker-stack}

Brings a compose stack up or down, starts or stops it. Without a block, up starts the file already on the host; with a block (compose YAML up to “end”) it first writes it to the given path. start brings up what is on the host without going to the registry (a stopped container with a private image comes up without a key); stop stops it without removing anything. down, start and stop take no block. A stack deployed by a pipeline is started and stopped with deploy PIPELINE start|stop — “Check” reminds you.

```
on HOST docker stack PATH up|down|start|stop
[compose file line by line
end]
```

| Argument | Required | What it is |
|---|---|---|
| `PATH` | ✓ | full path to docker-compose.yml |
| `up\|down\|start\|stop` | ✓ | up — bring up (pulling images if needed), down — stop and remove, start — start what exists without the registry, stop — stop, keeping the containers |

Example:

```
on web1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
end
```

### `on HOST vm create NAME image IMAGE [cpu N] [mem MB] [disk GB] [user NAME] [network NETWORK] [profile PROFILE] [install]` {#cmd-vm-create}

Creates a virtual machine on the host from a cloud image — like the “New machine” dialog.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | machine name; it also becomes a host on the hub |
| `image` | ✓ | an image from the host's catalog (ubuntu-24.04, debian-12…) |
| `cpu / mem / disk` |  | cores, memory in MB, disk in GB |
| `user` |  | user inside the machine (deploy by default) |
| `network` |  | libvirt network (the first existing one by default) |
| `profile` |  | a profile to apply to the machine after nkt is installed |
| `install` |  | install nkt into the machine (needed for profile and for later “on NAME …”) |

Example:

```
on web1 vm create app1 image ubuntu-24.04 cpu 2 mem 2048 disk 20 profile app install
```

### `on HOST vm start|shutdown|destroy|reboot|delete NAME · vm snapshot|revert NAME SNAPSHOT · vm resize NAME [cpu N] [mem MB] [disk GB]` {#cmd-vm-action}

Starts, shuts down, force-stops, reboots or deletes (with its disks and record) a machine on the host; a snapshot and reverting to it; vCPU, memory and disk (the disk only grows; vCPU and memory take effect after shutdown and start). A token: only its own machines, within its limits.

| Argument | Required | What it is |
|---|---|---|
| `action` | ✓ | start, shutdown, destroy, reboot, delete, snapshot, revert, resize |
| `NAME` | ✓ | machine name |
| `SNAPSHOT` |  | snapshot name (snapshot, revert) |
| `cpu / mem / disk` |  | new vCPU, memory in MB, disk in GB (resize; at least one) |

Example:

```
on web1 vm snapshot app1 before-upgrade
```

### `on HOST apply profile PROFILE` {#cmd-apply}

Applies a hub profile to the host in full: builds the plan and executes every item.

| Argument | Required | What it is |
|---|---|---|
| `PROFILE` | ✓ | a hub profile by name |

Example:

```
on web2 apply profile web-base
```

### `on HOST file put PATH [mode 0644]` {#cmd-file-put}

Writes a file to the host from the block (through the config editor: with a check and history when it knows the path).

```
on HOST file put PATH [mode 0644]
  [contents line by line]
end
```

| Argument | Required | What it is |
|---|---|---|
| `PATH` | ✓ | full file path |
| `mode` |  | mode, octal (0644 by default) |

Example:

```
on web1 file put /etc/motd
This server is managed by nkt
end
```

### `on HOST user add NAME [sudo] [key "ssh-ed25519 …"]` {#cmd-user-add}

Creates an account on the host with an SSH key and, optionally, passwordless sudo.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | user name |
| `sudo` |  | grant passwordless sudo |
| `key` |  | an SSH public key in quotes |

Example:

```
on web1 user add deploy sudo key "ssh-ed25519 AAAAC3Nza… deploy@laptop"
```

### `on HOST system hostname NAME | timezone ZONE | locale LOCALE | ntp SERVER…` {#cmd-system}

System settings: machine name, time zone, default locale (generated if needed), NTP servers with an immediate sync.

| Argument | Required | What it is |
|---|---|---|
| `hostname` |  | machine name (RFC 1123) |
| `timezone` |  | time zone like Europe/Moscow |
| `locale` |  | locale like ru_RU.UTF-8 |
| `ntp` |  | one or more NTP servers |

Example:

```
on web1 system hostname web1
on web1 system timezone Europe/Moscow
on web1 system locale ru_RU.UTF-8
on web1 system ntp time.google.com pool.ntp.org
```

### `on HOST cert issue DOMAIN… | cert renew DOMAIN` {#cmd-cert}

A Let's Encrypt certificate via certbot --standalone: services and processes on 80/443 are stopped for the validation and started again.

| Argument | Required | What it is |
|---|---|---|
| `issue` |  | issue a new certificate for the listed names |
| `renew` |  | renew an existing certbot lineage |

Example:

```
on web1 cert issue example.org www.example.org
on web1 cert renew example.org
```

### `on HOST git clone URL /DIR [branch BRANCH] [token ask | key hub]` {#cmd-git-clone}

Clones a repository onto the host as a file-browser job; private ones with a token asked at run time or the host's deploy key.

| Argument | Required | What it is |
|---|---|---|
| `URL` | ✓ | https://… or git@host:path |
| `/DIR` | ✓ | where to clone — a full directory path |
| `branch` |  | branch (the default one otherwise) |
| `token ask` |  | the HTTPS token or password is asked at run time |
| `key hub` |  | the host's deploy key over SSH (its public part is in Disks → Files) |

Example:

```
on web1 git clone https://github.com/org/app.git /srv/app branch main token ask
```

### `on HOST k8s create NAME [flavor k3s|kubeadm] [nodes single|1+N|3+N|"HOST: cp N, w N, host cp|w, bridge BRIDGE, endpoint ADDR; …"] [image IMAGE] [cpu N] [mem MB] [disk GB] [network NETWORK|nat|bridge|wireguard] [bridge BRIDGE] [cni cilium|flannel] [no-kube-proxy] [version 1.34] [expose] [api PORT] [http PORT] [https PORT]` {#cmd-k8s-create}

Creates a Kubernetes cluster on the host's virtual machines — like “new cluster” in the host list: machines from a cloud image with nkt, control plane, joining nodes, waiting for Ready, port forward and kubeconfig (downloadable on the hub in the cluster card).

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | cluster name — also the group and the prefix of machine names (lowercase letters, digits, hyphen) |
| `flavor` |  | k3s (default) or kubeadm |
| `nodes` |  | single — one machine; 1+N — a control plane and N workers (default 1+1); 3+N — three control planes (k3s only); in quotes — placement across hosts, as in the “Clusters” section: “HOST: cp N, w N, host cp\|w, bridge BRIDGE, endpoint ADDR; HOST2: …” (cp/w N — machines with a role, host — the host itself as a node, endpoint — the host's address for WireGuard peers) |
| `image` |  | an image from the host catalog (default ubuntu-24.04) |
| `cpu / mem / disk` |  | size of every machine: cores, memory in MB, disk in GB (2 / 4096 / 30) |
| `network / bridge` |  | on one host — a libvirt network name; with a placement — the network mode between hosts: nat (one host), bridge (a host bridge: bridge BRIDGE for all or “bridge …” in a host's part) or wireguard (a tunnel the hub sets up itself) |
| `cni` |  | cilium — Cilium instead of flannel (installed after the control plane); flannel — the flavor default |
| `no-kube-proxy` |  | with cilium: replace kube-proxy with Cilium (eBPF) |
| `version` |  | the Kubernetes minor version: the pkgs.k8s.io branch for kubeadm, the v1.36 channel for k3s; default — the current stable |
| `expose` |  | forward host ports 6443, 80, 443 to the control plane |
| `api / http / https` |  | host ports with expose: api (6443), http (80), https (443); 0 — do not forward |

Example:

```
on hv1 k8s create lab nodes "hv1: cp 1, w 2; hv2: w 2; hv3: host w" network wireguard cni cilium expose api 16443
```

### `on HOST k8s destroy NAME` {#cmd-k8s-destroy}

Deletes the cluster: all its machines with disks and the record on the hub.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | the cluster name on this host |

Example:

```
on hv1 k8s destroy lab
```

### `pipeline NAME` {#cmd-pipeline}

Saves a deployment pipeline — like “Save” in the pipeline window: creates it if missing, otherwise a new version of the description. Values of the env block go to the pipeline's secrets, generate: is created by the hub (skipped for keys already set); a value different from the one already set is not replaced — an error.

```
pipeline NAME
  pipeline description line by line
end
```

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | pipeline name (Latin letters, digits, dot, dash, underscore) |

Example:

```
pipeline hello
repo: https://github.com/org/app.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.yml
  project: hello
  hosts: [web1]
  env:
    DB_PASSWORD: generate:password
end
```

### `deploy PIPELINE [start|stop] [DURATION]` {#cmd-deploy}

Deploys a pipeline — like the “Deploy” button — and waits for the result; the deployment log goes into the script log. In a dry run — the pipeline's dry run (with the description from “pipeline” above, if there is one). With start or stop — starts or stops what is already deployed on all its hosts, like the “Start” and “Stop” buttons.

| Argument | Required | What it is |
|---|---|---|
| `PIPELINE` | ✓ | a pipeline name on the hub or from “pipeline” above |
| `start\|stop` |  | start — start what is deployed (the same version, images from the host), stop — stop without removing (containers and data stay); like the pipeline buttons |
| `DURATION` |  | how long to wait for the result (30m by default) |

Example:

```
deploy hello 20m
deploy shop stop
deploy shop start
```

### `migrate HOST CONTAINER to HOST [switch-fly] [switch-pipeline] [clean-leftovers]` {#cmd-migrate}

Moves a stack to another host — like dragging on the hosts map: a plan (obstacles are an error before the stack stops), stop, data through the hub, start on the new host; a failed move rolls back by itself. The old stack stays stopped for a rollback (map: “Roll back”, “Remove old”).

| Argument | Required | What it is |
|---|---|---|
| `HOST` | ✓ | the host the stack runs on now |
| `CONTAINER` | ✓ | a container of the stack (the whole compose project moves) |
| `to HOST` | ✓ | the host to move to |
| `switch-fly` |  | switch Traffic Fly targets to the new host with the same role |
| `switch-pipeline` |  | switch the stack's pipeline (host in the description, deployment record, site) |
| `clean-leftovers` |  | remove leftovers of an earlier copy of this stack on the new host (moving there and back) |

Example:

```
migrate web1 forgejo-forgejo-1 to web2 switch-fly switch-pipeline
```

### `k8s CLUSTER ingress cilium [port PORT]` {#cmd-k8s-ingress}

Turns on the Cilium Ingress controller in host network mode: Envoy listens on the port on every node (no LoadBalancer), Cilium is the default controller; the Cilium version stays the same. Needs a cluster with Cilium replacing kube-proxy. A job on the control plane.

| Argument | Required | What it is |
|---|---|---|
| `CLUSTER` | ✓ | a cluster name on the hub or from “k8s create” above; actions go to the first control plane |
| `port` |  | port on the nodes (8080 by default, from 1024) |

Example:

```
k8s lab ingress cilium port 8080
```

### `k8s CLUSTER apply` {#cmd-k8s-apply}

Applies a manifest (kubectl apply) on the first control plane — like “Manifests” in “Clusters”; in a dry run — kubectl diff.

```
k8s CLUSTER apply
  manifest line by line
end
```

| Argument | Required | What it is |
|---|---|---|
| `CLUSTER` | ✓ | a cluster name on the hub or from “k8s create” above; actions go to the first control plane |

Example:

```
k8s lab apply
apiVersion: v1
kind: Namespace
metadata:
  name: demo
end
```

### `k8s CLUSTER wait deployment NAME [namespace NS] [DURATION]` {#cmd-k8s-wait}

Waits until all replicas of a deployment are ready.

| Argument | Required | What it is |
|---|---|---|
| `CLUSTER` | ✓ | a cluster name on the hub or from “k8s create” above; actions go to the first control plane |
| `NAME` | ✓ | deployment name |
| `namespace` |  | namespace (default by default) |
| `DURATION` |  | how long to wait: 90s, 5m (5m by default) |

Example:

```
k8s lab wait deployment hello 5m
```

### `fly route ID KIND [names NAME,…] [port PORT] [tls gateway] [dns plugin] [check tcp|http|none] [path PATH] [gateway GATEWAY] (targets cluster CLUSTER port PORT | target HOST container|vm|lxd NAME PORT … | target HOST host PORT …)` {#cmd-fly-route}

Creates or updates a Traffic Fly route and applies it — like a pipeline's fly: block: a new one is saved and applied, an existing one changes targets smoothly (new ones added and checked, old ones drained). In a dry run — the map is validated with the route.

| Argument | Required | What it is |
|---|---|---|
| `ID` | ✓ | route ID |
| `KIND` | ✓ | https, http (by name), tcp, udp, tcpudp (by port) |
| `names` |  | names separated by commas (https, http) |
| `port` |  | port on the gateway (required for tcp/udp; https — 443, http — 80) |
| `tls gateway` |  | certificate on the gateway (Let's Encrypt), targets get HTTP |
| `dns plugin` |  | the hub's DNS plugin keeps the route names' records (A at the gateway address, on “fly apply”); no plugin — nothing |
| `check` |  | target check: tcp (default), http (with path), none |
| `gateway` |  | gateway name; with one gateway it can be omitted |
| `targets cluster` |  | targets cluster CLUSTER port N — targets: all cluster nodes (machines as “virtual machine” on their hosts) |
| `target` |  | target HOST container\|vm\|lxd NAME PORT, target HOST host PORT — a target in a list; several are allowed |

Example:

```
fly route hello https names hello.example.com tls gateway check tcp targets cluster lab port 8080
fly route mc tcp port 25565 target web1 container mc-server-1 25565
```

### `fly apply` {#cmd-fly-apply}

Applies the Traffic Fly map — like “Apply”; in a dry run — the apply dry run.

Example:

```
fly apply
```

### `plugin NAME WORD args… | WORD args…` {#cmd-plugin}

A command of a connected plugin (“About” → “Plugins”): the plugin's command word and its arguments. It can be written without “plugin NAME” — as one word, unless another plugin took it. The plugin decides what the command does and which arguments it takes: “Check” asks it, in a dry run it only says what it would do. Not connected — a “Check” error.

| Argument | Required | What it is |
|---|---|---|
| `NAME` | ✓ | the plugin's name on the hub |
| `WORD` | ✓ | a command word from the plugin's help (dns, …) followed by its arguments |

Example:

```
dns zone add example.com
dns set app.example.com A 203.0.113.10 ttl 300
plugin dns dns rm old.example.com
```

### `on HOST run "COMMAND" | on HOST run … end` {#cmd-run}

A command on a plugin host (MikroTik, Windows and other devices added through a plugin) in its type's language: a RouterOS script, PowerShell. One line in quotes — or a block up to “end” (several lines). The plugin runs it: “Check” asks it, in a dry run it only says what it would run. A host with the nkt agent or a type without commands — a “Check” error.

| Argument | Required | What it is |
|---|---|---|
| `COMMAND` |  | the command in quotes; without it — the lines up to “end” |

Example:

```
on router1 run "/ip dns cache flush"
on router1 run
/ip firewall address-list add list=blocked address=198.51.100.7
/ip firewall address-list print where list=blocked
end
```

## Ready-made scripts

### A new host with nginx

Register a host, install nkt and packages, enable nginx and open ports.

```
group prod

host web1 192.0.2.10 user root password ask group prod
install web1

on web1 packages install nginx htop ncdu
on web1 service nginx enable
on web1 firewall allow 80/tcp
on web1 firewall allow 443/tcp
```

### An application in Docker

Install Docker and bring up a compose stack described right in the script.

```
host app1 192.0.2.20 user deploy password ask
install app1

on app1 docker install
on app1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
  db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: change-me
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata: {}
end
on app1 firewall allow 8080/tcp
```

### Machines on an existing host

Create two machines on a host already on the hub and set them up.

```
set IMAGE ubuntu-24.04
group lab profile base

# hv1 is already on the hub — no need to register it
on hv1 vm create lab-web image ${IMAGE} cpu 2 mem 2048 disk 20 profile base install
on hv1 vm create lab-db image ${IMAGE} cpu 2 mem 4096 disk 40 install

on lab-db packages install postgresql
on lab-db firewall allow 5432 from 192.168.100.0/24
on lab-web packages install nginx
```

### Forgejo behind Traffic Fly: deploy and move

A Forgejo pipeline with access through the gateway, deployment, an outside check, a move to another host switching the routes and the pipeline — the script of the “Moving a stack with Traffic Fly” guide.

```
# Forgejo behind Traffic Fly: deploy and move to another host
param FROM "host where Forgejo is deployed first"
param TO "host to move it to"
param DOMAIN "Forgejo name — A record at the Traffic Fly gateway"

# Web and git over SSH go through the gateway; the gateway issues the certificate
pipeline forgejo
repo: https://github.com/piqab/nkt.git
ref: main
action: compose
compose:
  file: examples/forgejo/deploy/docker-compose.yml
  project: forgejo
  hosts: [${FROM}]
  wait_timeout: 5m
  env_keys:
    forgejo-admin: [FORGEJO_ADMIN_USER, FORGEJO_ADMIN_PASSWORD, FORGEJO_ADMIN_EMAIL]
  env:
    POSTGRES_PASSWORD: generate:password
    FORGEJO_DOMAIN: ${DOMAIN}
    FORGEJO_ROOT_URL: https://${DOMAIN}/
    FORGEJO_ADMIN_USER: gitadmin
    FORGEJO_ADMIN_PASSWORD: generate:password
    FORGEJO_ADMIN_EMAIL: admin@example.com
  fly:
    - route: forgejo-web
      kind: https
      names: [${DOMAIN}]
      tls: gateway
      service: forgejo
      target_port: 3000
      check: http
      check_path: /api/healthz
    - route: forgejo-ssh
      kind: tcp
      port: 2222
      service: forgejo
      target_port: 2222
end
deploy forgejo 20m

# Check from outside — via DNS and the gateway, as a user would
wait url https://${DOMAIN}/api/healthz 200 10m

# Move: Traffic Fly routes and the pipeline switch to the new host
migrate ${FROM} forgejo-forgejo-1 to ${TO} switch-fly switch-pipeline
wait url https://${DOMAIN}/api/healthz 200 5m
```

### A Kubernetes cluster: from zero to the first pod

kubeadm on two hosts over WireGuard, Cilium without kube-proxy, Cilium Ingress, a whoami test pod and outside access through Traffic Fly — the script of the “Cluster: from zero to the first pod” guide.

```
# A Kubernetes cluster: from zero to the first pod
param HOST1 "host with the control plane and the first worker"
param HOST2 "host with the second worker"
param DOMAIN "test site name — A record at the Traffic Fly gateway"

# kubeadm: 1 control plane + 2 workers on two hosts, WireGuard, Cilium without kube-proxy
on ${HOST1} k8s create lab flavor kubeadm nodes "${HOST1}: cp 1, w 1; ${HOST2}: w 1" network wireguard cni cilium no-kube-proxy image debian-13 expose http 0 https 0

# Cilium Ingress: port 8080 on every node
k8s lab ingress cilium port 8080

# Test pod, service and Ingress
k8s lab apply
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello
spec:
  replicas: 2
  selector:
    matchLabels: {app: hello}
  template:
    metadata:
      labels: {app: hello}
    spec:
      containers:
        - name: whoami
          image: traefik/whoami:v1.12.0
          ports: [{containerPort: 80}]
---
apiVersion: v1
kind: Service
metadata:
  name: hello
spec:
  selector: {app: hello}
  ports: [{port: 80, targetPort: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello
spec:
  ingressClassName: cilium
  rules:
    - host: ${DOMAIN}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: hello, port: {number: 80}}
end
k8s lab wait deployment hello 5m

# Outside access: the Traffic Fly gateway to all cluster nodes
fly route hello https names ${DOMAIN} tls gateway check tcp targets cluster lab port 8080
wait url https://${DOMAIN}/ 200 10m
```

### Maintaining existing hosts

Apply a profile, write a file, reload a service, remove a package — without registering hosts.

```
# The hosts are already on the hub: actions only
on web1 apply profile web-base
on web2 apply profile web-base

on web1 file put /etc/motd
This server is managed by nkt.
end
on web1 service nginx reload
on web2 packages remove telnet
```
