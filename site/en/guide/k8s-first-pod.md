# A Kubernetes cluster: from zero to the first pod

Step by step: a **kubeadm** cluster of three machines on **two hosts**
(**WireGuard** network between them), **Cilium** as the pod network, and a
test pod reachable from outside at `https://hello.example.com` through an
**Ingress** and a [Traffic Fly](/en/guide/traffic-fly) gateway. For reference
on clusters see [Kubernetes clusters](/en/guide/hub-clusters). The same works
on **k3s**; only the ingress controller differs there — steps 5, 6 and the
target port.

How outside access works: the ingress controller listens on its port on every
node, the Traffic Fly route is **bound to the cluster** and leads from the
gateway to `node:port` of all its machines, and the gateway issues the
`hello.example.com` certificate. No host 80/443 forwarding is needed; the
gateway takes a failed node out of the route by itself, and the automation
adds a new node to the pool.

| Flavor | Ingress controller | `ingressClassName` | Port on every node |
|---|---|---|---|
| **kubeadm** | Cilium Ingress — turned on in step 5 | `cilium` | **8080** |
| **k3s** | Traefik — comes with k3s itself, nothing to turn on | `traefik` | **80** |

```
client ─https─▶ Traffic Fly gateway ─WireGuard─▶ host 1 ─▶ cp-1:port, w-1:port
  (certificate,                                ─▶ host 2 ─▶ w-2:port
   target checks)      Ingress (Cilium · Traefik) ─▶ whoami pod (any node)
```

::: warning Experimental feature
The cluster is built from real machines and has no rollback. Try it on test
hosts first; "Dry run" before creating, the job log after.
:::

## 0. What you need first

- **A hub** with "Kubernetes clusters" turned on: "About" → "Experimental
  features" → "Edit" → "Kubernetes clusters".
- **Two virtualization hosts** (KVM and libvirtd) in the hub, nkt 1.13.62 or
  newer on both. Room for the machines (default size — 2 CPU, 4096 MB,
  30 GB per machine):

  | Host | Machines | Free at least |
  |---|---|---|
  | host 1 | control plane + worker | 4 cores, 9 GB memory, 60 GB disk |
  | host 2 | worker | 2 cores, 5 GB memory, 30 GB disk |

- **UDP 51820 between the hosts** — for the cluster's WireGuard. Hosts in
  different networks: open it in the outside firewalls; if the hub reaches a
  host at an address the other host cannot see, enter another one in "Host
  addresses for peers" (shown in the form with the WireGuard network).
- **Internet on the hosts** — for the machine image, Kubernetes packages and
  container images. Without it — the hub package cache ("About" → "Use the
  hub package cache" and the checkbox at the host): files and the registry
  also go through the hub ([Hub cache](/en/guide/hub-cache)).
- **A Traffic Fly gateway** — a machine on the internet with nkt-edge in the
  fly role: "Traffic Fly" → "Gateways" → "Add gateway"
  ([nkt-edge](/en/guide/edge)).
- **A domain:** the `hello.example.com` A record points at the gateway.

## 1. New cluster

"Clusters" → **"New cluster"** — "New Kubernetes cluster across several
hosts".

| Field | Value | Why |
|---|---|---|
| Cluster name | `lab` | machine and card names |
| Flavor | **kubeadm** | standard Kubernetes; on **k3s** everything is the same except steps 5–6 |
| Kubernetes version | current stable | one for all nodes; old branches (v1.31) do not install on Debian 13 |
| Network between hosts | **WireGuard between hosts** | hosts in different networks; in one L2 segment — "bridge"; if the machine needs an address in the hosts' own network and there is no bridge on it — "macvtap" with a device in every placement row (there the network administrator pins the address, and port forwarding from the host does not work) |
| Placement, row 1 | host 1 · virtual machines · **control plane** · 1 | the first node with the API |
| Placement, row 2 | host 1 · virtual machines · **worker** · 1 | |
| Placement, row 3 | host 2 · virtual machines · **worker** · 1 | "add row" |
| Image | a cloud image from the host's catalog | for example Debian 13; "will be downloaded" if it is not there yet |
| **Cilium** | on | the pod network instead of flannel |
| **replace kube-proxy with Cilium** | on | **required**: Cilium Ingress does not work without it |
| Expose outside | **API** (6443) only, HTTP and HTTPS — `0` | the API — for `kubectl` from your machine; 80/443 are not needed, access goes through the gateway |

Machine sizes — the defaults (2 CPU, 4096 MB, 30 GB); kubeadm does not install
a control plane with less than 2 CPUs.

![New cluster form](/screens/en/k8s-first-pod-form.png)

**"save preset"** helps: the next such cluster fills in by picking the preset.

## 2. Dry run

**"Dry run"** checks every host and writes a checklist into the log over the
form (the settings stay): ✓ — fine, ! — a warning, ✗ — creation will fail.

| ✗ in the checklist | What to do |
|---|---|
| no KVM / libvirtd not running | turn on virtualization in the BIOS or at the provider (you need KVM, not a container), `systemctl enable --now libvirtd` |
| not enough memory or disk | smaller machines or free some space |
| no connection between hosts (ping, WireGuard address) | open UDP 51820, enter an address in "Host addresses for peers" |
| image or package unavailable | internet on the host or the hub package cache |
| machine name taken | another cluster name |

The **"also prepare"** checkbox downloads the image and packages into caches
and brings up the libvirt network right away — creation goes faster after.

## 3. Creation

**"Create cluster"** — a hub job, the log opens at once. Steps:

1. **Checks before creation** — the same as the dry run.
2. **WireGuard tunnel between hosts** — keys, `wg-quick@nktwg<N>` and a
   machine network per host; a handshake check for every pair of hosts.
3. **Machines** — one per placement row: cloud image, cloud-init, nkt inside.
4. **Control plane** — containerd, Kubernetes packages, `kubeadm init`,
   Cilium (`cilium install` replacing kube-proxy, waiting until ready).
5. **Joining nodes** — `kubeadm join` for both workers.
6. **Waiting for Ready, port forward, kubeconfig** — all nodes `Ready`, the
   API forwarded, the kubeconfig into the cluster card.

The time mostly depends on downloads: the machine image, Kubernetes packages,
Cilium images; with "also prepare" and the hub cache further clusters are
created noticeably faster. Failed? Fix the cause from the log and press
**"continue"** at the cluster: created machines and installed roles are not
redone.

## 4. Check the cluster

A ready cluster has a **"kubectl"** button: a terminal on the first control
plane with `kubectl` set up (completion, the `k` alias).

```sh
kubectl get nodes -o wide        # three nodes, all Ready
cilium status --wait             # Cilium: OK, kube-proxy replaced
kubectl get pods -A              # system pods Running
```

**"kubeconfig"** in the card is the administrator file for `kubectl` on your
machine; its API address is the first control plane's host and the port from
"Expose outside".

The terminal from the card already sets `KUBECONFIG` — that matters for
`cilium`: without it the CLI falls back to `http://localhost:8080` and answers
with `status` errors that check nothing.

## 5. Ingress: two branches by flavor

What the cluster has is visible right away:

```sh
kubectl get ingressclass
```

### kubeadm: turn on Ingress in Cilium

kubeadm ships no ingress controller — this branch assumes Cilium Ingress; your
own controller (Traefik, NGINX) follows the same scheme, only with its own
port. Cilium Ingress in **host network** mode listens on port **8080** right
on every node, no LoadBalancer needed.

```sh
# The running Cilium version: without --version "cilium upgrade" would also
# upgrade Cilium itself — to the version built into the CLI.
V=$(kubectl -n kube-system get ds cilium \
  -o jsonpath='{.spec.template.spec.containers[0].image}' | sed -E 's/.*:(v[0-9.]+).*/\1/')
echo "$V"

cilium upgrade --version "$V" --reuse-values \
  --set ingressController.enabled=true \
  --set ingressController.default=true \
  --set ingressController.loadbalancerMode=shared \
  --set ingressController.hostNetwork.enabled=true

kubectl -n kube-system rollout restart deployment/cilium-operator
kubectl -n kube-system rollout restart ds/cilium
kubectl -n kube-system rollout status ds/cilium
```

Check:

```sh
kubectl get ingressclass                  # cilium (default)
ss -ltn | grep ':8080 '                   # Envoy listens on 8080 on the node
```

The setting lives in Cilium's own Helm values — it survives node reboots and
new nodes. The port can be changed
(`--set ingressController.hostNetwork.sharedListenerPort=…`), then use the
same one in the route below.

### k3s: Traefik is already there

k3s brings Traefik itself and nkt does not disable it during installation, so
there is nothing to turn on: `kubectl get ingressclass` gives `traefik` marked
`(default)`, the `traefik` pod sits in kube-system, and next to it
`svclb-traefik-*` on **every** node — those hold ports **80** and **443** of
that node.

```sh
kubectl -n kube-system get pods -o wide | grep -E 'traefik|svclb'
```

The route's target port is **80** of the node. `ss -ltn` on the host shows no
listener for it: the port is held not by a socket in the host's network
namespace but by a kube-proxy rule (with Cilium replacing kube-proxy — by
eBPF), so check it through the node's address, not through the listener list:

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://$(hostname -I | awk '{print $1}'):80/
# 404            ← before step 6 this is normal: Traefik is alive, no rule matches
```

`127.0.0.1:80` may not answer at all — that is not how you check it.

## 6. Test pod

`traefik/whoami` answers with text: the pod name and the request headers — you
see right away which pod answered and the client's address.

```sh
kubectl apply -f - <<'EOF'
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
  ingressClassName: cilium      # on k3s — traefik
  rules:
    - host: hello.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: hello, port: {number: 80}}
EOF

kubectl get pods -l app=hello -o wide     # two pods Running, on different workers
kubectl get ingress hello
```

`ingressClassName` — the controller of your flavor: `cilium` on kubeadm,
`traefik` on k3s. Guess wrong and no controller takes the Ingress at all; from
outside it will be silence. `kubectl get ingress` may show no address: a host
network controller has no address in the status, so check by the answer below.

Check from inside — through the Ingress on this node's address (**8080** on
kubeadm, **80** on k3s):

```sh
curl -s -H 'Host: hello.example.com' http://$(hostname -I | awk '{print $1}'):8080/
# Hostname: hello-…   ← the pod that answered
```

`kubectl exec` into this pod shows nothing: `traefik/whoami` has neither a
shell nor `env`, and exec returns "executable file not found in $PATH" — that
is not a network error.

The same YAML can be applied from "Clusters" → "Manifests" — to several
clusters at once, with `kubectl diff` before writing.

## 7. Traffic Fly route

"Traffic Fly" → **"New route"**:

| Field | Value |
|---|---|
| Kind | **HTTPS by name (443)** |
| Names | `hello.example.com` |
| Certificate on the gateway (Let's Encrypt) | on — the target gets plain HTTP |
| Target check | **TCP connect** |
| **Cluster** | `lab` |
| **Port on the node** | **8080** (kubeadm) / **80** (k3s) |

A bound route's targets are not listed by hand: the automation builds them
from the cluster's nodes — a node arrives, a target appears; a node leaves, a
target goes. The editor says so under the field, and each target in the list
is tagged "cluster lab". "Port on the node" can be left empty if the cluster
card has **"Client ingress"** set (a node and the HTTP port of its ingress
controller) — the automation takes the port from there.

Save → **"Apply"**: the hub brings up the Traffic Fly tunnels on both hosts (if
they are not there yet), the gateway issues the certificate, and the targets
are pooled from the nodes. Within a minute the route has three targets, all
**alive**.

Re-pooling them later is also not manual work: the cluster creation and
deletion jobs do it, the watcher does (once a minute, at most once per 10
minutes per cluster), and so do the buttons — **"Traffic Fly targets"** in the
cluster card and **"Check targets"** in the route itself.

![A route bound to a cluster](/screens/en/k8s-first-pod-route.png)

## 8. Check from outside

From any machine on the internet:

```sh
curl -s https://hello.example.com/
# Hostname: hello-5d…            ← the pod
# X-Forwarded-For: 203.0.113.55  ← your real address
# X-Forwarded-Proto: https
```

Several requests in a row are answered by different pods.

**A node failure:** shut down the `lab-w-2` machine (or host 2). Within
seconds the gateway marks its target "not alive" and takes it out of the
route, Kubernetes restarts the pod on the remaining worker — `curl` keeps
answering. Start the machine again — the target comes back by itself.

Remove the machine from the hub and its target leaves the pool with it, and
the site name stays on the live nodes — no manual target editing for that
either.

## 9. Clean up

```sh
kubectl delete ingress,service,deployment hello
```

The route — "Delete" in "Traffic Fly" and "Apply". The whole cluster —
**"delete cluster"** in the card: the machines with their disks, the WireGuard
tunnel and the machine network leave the hosts; host nodes (if any) stay. The
deletion job also takes the cluster's bound routes off the map — there is
nowhere left to lead the traffic.

## Through the API and n8n

Steps 6–7 also work without the UI: a manifest into clusters with an API
token (`POST /api/hub/k8s/manifests/apply?job=1`) or the n8n **Cluster →
Apply Manifest**; applying Traffic Fly — `POST /api/hub/fly/apply` or
**Traffic Fly → Apply**; the whole path — as a saved script
(**Script → Run**); re-pool a bound route's targets —
`POST /api/hub/fly/cluster {"cluster":"lab"}`. Creating a cluster and the
kubeconfig are closed to tokens. Details — [Hub API](/en/guide/hub-api#what-a-token-can-call),
[n8n](/en/guide/n8n).

## What next

- **Helm to clusters** — a release into chosen clusters as a hub job.
- **Manifests** — one YAML to several clusters with `kubectl diff`.
- **Upgrade** — the Kubernetes version node by node: cordon, drain, upgrade,
  uncordon.
- **+ worker** — one more node in a ready cluster (the same Kubernetes
  branch); a bound route takes it into its targets by itself once the node is
  `Ready`.

## The same as a script

Everything from creating the cluster to an answer from
`https://hello.example.com` — as one hub script "A Kubernetes cluster: from
zero to the first pod" (also
[`examples/scripts/k8s-first-pod.nkt`](https://github.com/piqab/nkt/blob/main/examples/scripts/k8s-first-pod.nkt), needs hub 1.13.68+).

How to run it: **"Profiles" → the "Scripts" tab → "New script"**, in the
editor window — **"Ready-made scripts ▾"** → "A Kubernetes cluster: from zero to the first pod"; enter a name →
"Save" → "Check" → "Dry run" → "Run" (the parameters are asked at start, the
progress is in the job log). The ready-made scripts are also on the scripts
page's "Reference" tab ("Into editor" button).

At start it asks for two hosts and the site name. "Dry run" checks the hosts
(like the cluster form's "Dry run"), the rest is "would": the cluster does not
exist before it is created.

```
# Кластер Kubernetes: от нуля до первого пода
param HOST1 "хост с control plane и первым worker" host
param HOST2 "хост со вторым worker" host
param DOMAIN "имя тестового сайта — A-запись на адрес шлюза Traffic Fly"

# kubeadm: 1 control plane + 2 worker на двух хостах, WireGuard, Cilium без kube-proxy
on ${HOST1} k8s create lab flavor kubeadm nodes "${HOST1}: cp 1, w 1; ${HOST2}: w 1" network wireguard cni cilium no-kube-proxy image debian-13 expose http 0 https 0

# Ingress Cilium: порт 8080 на каждом узле (на k3s шаг не нужен — Traefik уже стоит)
k8s lab ingress cilium port 8080

# Тестовый под, сервис и Ingress
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

# Вход снаружи: шлюз Traffic Fly на все узлы кластера
fly route hello https names ${DOMAIN} tls gateway check tcp targets cluster lab port 8080
wait url https://${DOMAIN}/ 200 10m
```

The script's comments are in Russian; the commands are the same in any
language.

- **`on … k8s create`** — steps 1–3: the cluster form as one line (kubeadm,
  placement on two hosts, WireGuard, Cilium without kube-proxy, only the API
  exposed).
- **`k8s lab ingress cilium port 8080`** — step 5: the same `cilium upgrade`
  with the running Cilium version, as a job on the control plane. On k3s the
  step is skipped (Traefik comes with k3s itself) and the manifest says
  `ingressClassName: traefik`.
- **`k8s lab apply … end`** and **`k8s lab wait deployment hello`** — step 6.
- **`fly route … targets cluster lab port 8080`** — step 7: a route bound to
  the cluster; it builds its targets from the cluster's nodes and is applied
  right away. On k3s this step says `port 80`.
- **`wait url`** — step 8: an answer from outside, from the hub to the real
  name.

## If it doesn't work

| Symptom | Cause and what to do |
|---|---|
| node `NotReady` | Cilium did not come up on the node: `cilium status`, `kubectl -n kube-system get pods -o wide`; the node cannot reach the control plane — the WireGuard tunnel (handshake in the creation log) |
| pods `Pending` | not enough resources: `kubectl describe pod`; kubeadm does not schedule pods on the control plane (taint) — you need at least one worker |
| `ImagePullBackOff` | the nodes cannot reach a registry: internet on the host or the hub package cache (the hub's registry mirror) |
| `cilium status` answers with `dial tcp [::1]:8080: connect: connection refused` everywhere | the CLI runs without a kubeconfig — it falls back to `http://localhost:8080` and those "errors" check nothing: work from the cluster card's terminal (it sets `KUBECONFIG`) or export it yourself |
| `kubectl get ingressclass` is empty | **kubeadm**: Cilium without kube-proxy replacement, or the upgrade did not apply — step 5, `cilium status`. **k3s**: Traefik was disabled at install — check `kubectl -n kube-system get pods -o wide \| grep traefik` |
| the Ingress exists, outside is silent | `ingressClassName` is not the controller's own: Traefik does not take `cilium`, Cilium Ingress does not take `traefik` — step 6 |
| `curl … :8080` from the node gives 404 | the `Host:` name does not match `host:` in the Ingress — or the port is from the other flavor: **8080** on kubeadm, **80** on k3s |
| on a k3s node `ss -ltn` shows no `:80` | not a symptom: the port is held by `svclb-traefik-*` (a kube-proxy rule, or eBPF with Cilium), there is no listener in the host's network namespace — check with `curl` on the node's address |
| a route target is "not alive" | nothing listens on the ingress port on the node (8080 / 80 — steps 5 and 6), the host's Traffic Fly tunnel is down — the hint under the gateway in "Traffic Fly" |
| `curl` to the shared control plane address gives `SSL_ERROR_SYSCALL` / "connection reset", while the node's own `:6443` works | the listener is there, the node is not allowed in: the gateway accepts only the addresses on its allow list, and the address the node actually arrives from may be missing (for a machine on **macvtap** that is its own site's address, not the parent host's — see "The listener for the shared address"). The creation log carries the target shape, the list, and "no connection through this route at all" — that is exactly this case; add the address under "who else" and apply again |
| the shared address works, but a node in it is always "drained" | a person drained that target on the Traffic Fly map — no connections go through it, and the hub does not undo a drain; bring the target back in the same place |
| `kubectl exec` or `logs` of a pod on **one** node: `proxy error from 127.0.0.1:6443 while dialing <node>:10250, code 502`, the node being `Ready` | **k3s**: these requests do not go to the kubelet directly, they go through the tunnel the agent opened to `server:6443` itself — "is 10250 open from another machine" proves nothing here; a 502 is that node's agent state, `systemctl restart k3s-agent` on a worker (`k3s` on a control plane). **kubeadm**: the apiserver → kubelet path is direct, look at `ss -ltn \| grep ':10250 '` on the node and the route to it |
| no certificate | the `hello.example.com` A record does not point at the gateway, or its port 80 is closed |
| `kubeadm` does not install on Debian 13 | an old branch (v1.31) was chosen: `sqv` rejects its signature — pick a current one |
| the "install role" step fails with `FileAvailable--etc-kubernetes-manifests-kube-apiserver.yaml` or with `error execution phase check-etcd` | the node is half-finished from the previous attempt (the join broke in the `etcd-join` phase, while the control plane manifests were already written): a retry removes the state itself, through the `kubeadm reset` cascade. If the retry fails too, the job log shows which attempt of the reset refused; by hand — `kubeadm reset -f --skip-phases=remove-etcd-member` on that node, then rerun the job |
| the retry went past the failed step (to the addons instead of re-joining the node) | retrying a creation job opens the [steps window](/en/guide/hub-clusters#the-creation-steps-window): the steps that did not complete stay ticked, the completed ones are unticked — check the selection. If the node reported success last time but never became an etcd member, tick its join again — it runs with a node reset |
| a node on **macvtap** does not answer from the host it was created on | that is not a failure: a host cannot talk to its own macvtap machines (a Linux limitation, not a setting) — check access from another machine or from the hub; the cluster nodes see it by its real address in the hosts' network |
| a machine on **macvtap** never gets an address and the log says "the guest agent does not answer" | in this mode only `qemu-guest-agent` reports the address: the machine has no libvirt DHCP lease and no row in a bridge's ARP table, and the agent's channel is a virtio port, not the network — so the host does hear it even from its own macvtap machines. The agent arrives as a package on the first boot and is started there too; if it is installed and stays silent, run `systemctl enable --now qemu-guest-agent` inside the machine (or reboot it); the address can also be entered by hand |
| a node on **macvtap** was `Ready` and dropped after the machine rebooted, peers report `no route to host` to its address | that network's DHCP gives the machine its address, and the hub has nothing to pin it with — there is no libvirt network with reservations here: a reservation by MAC or a static address on the node is the network administrator's job (the creation log carries its own line about it); Traffic Fly routes, the port forward and the kubeconfig all hold exactly that address |
