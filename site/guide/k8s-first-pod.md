# Кластер Kubernetes: от нуля до первого пода

Пошагово: кластер **kubeadm** из трёх машин на **двух хостах** (сеть
**WireGuard** между ними), сеть подов **Cilium**, и тестовый под, который
открывается снаружи по `https://hello.example.com` через **Ingress** и шлюз
[Traffic Fly](/guide/traffic-fly). Справочно о кластерах —
[Кластеры Kubernetes](/guide/hub-clusters). Всё то же выходит и на **k3s**;
там отличается только ingress-контроллер — шаг 5, 6 и порт целей.

Как устроен вход снаружи: ingress-контроллер слушает свой порт на каждом
узле, маршрут Traffic Fly **привязан к кластеру** и ведёт с шлюза на
`узел:порт` всех его машин, сертификат `hello.example.com` выпускает шлюз.
Пробрасывать 80/443 хостов не нужно; упавший узел шлюз выводит из маршрута
сам, а новый узел добавляет в цели автомат.

| Вариант | Ingress-контроллер | `ingressClassName` | Порт на каждом узле |
|---|---|---|---|
| **kubeadm** | Ingress Cilium — включается шагом 5 | `cilium` | **8080** |
| **k3s** | Traefik — приезжает с самим k3s, включать нечего | `traefik` | **80** |

```
клиент ─https─▶ шлюз Traffic Fly ─WireGuard─▶ хост 1 ─▶ cp-1:порт, w-1:порт
  (сертификат,                              ─▶ хост 2 ─▶ w-2:порт
   проверка целей)       Ingress (Cilium · Traefik) ─▶ под whoami (любой узел)
```

::: warning Экспериментальная возможность
Кластер создаётся настоящими машинами, отката нет. Обкатывайте на тестовых
хостах; перед созданием — «Сухой прогон», после — журнал задания.
:::

## 0. Что нужно заранее

- **Хаб** с включёнными «Кластерами Kubernetes»: «О системе» →
  «Экспериментальные функции» → «Изменить» → «Кластеры Kubernetes».
- **Два хоста с виртуализацией** (KVM и libvirtd) в хабе, nkt на обоих —
  1.13.62 или новее. Места под машины (размеры по умолчанию — 2 CPU,
  4096 МБ, 30 ГБ на машину):

  | Хост | Машины | Нужно свободным, не меньше |
  |---|---|---|
  | хост 1 | control plane + worker | 4 ядра, 9 ГБ памяти, 60 ГБ диска |
  | хост 2 | worker | 2 ядра, 5 ГБ памяти, 30 ГБ диска |

- **UDP 51820 между хостами** — для WireGuard кластера. Хосты в разных сетях
  — откройте его во внешних файрволах; если хаб ходит на хост по адресу,
  которого второй хост не видит, впишите другой в «Адреса хостов для соседей»
  (появляется в форме при сети WireGuard).
- **Интернет у хостов** — для образа машин, пакетов Kubernetes и образов
  контейнеров. Без него — кэш пакетов хаба («О системе» → «Использовать
  кэш пакетов хаба» и галочка у хоста): через хаб пойдут и файлы, и registry
  ([Кэш хаба](/guide/hub-cache)).
- **Шлюз Traffic Fly** — машина в интернете с nkt-edge в роли fly: «Traffic Fly» →
  «Шлюзы» → «Добавить шлюз» ([nkt-edge](/guide/edge)).
- **Домен:** A-запись `hello.example.com` — на адрес шлюза.

## 1. Новый кластер

«Кластеры» → **«Новый кластер»** — «Новый кластер Kubernetes на нескольких
хостах».

| Поле | Значение | Зачем |
|---|---|---|
| Имя кластера | `lab` | имя машин и карточки |
| Вариант | **kubeadm** | стандартный Kubernetes; на **k3s** всё то же, кроме шагов 5–6 |
| Версия Kubernetes | актуальная stable | одна на все узлы; старые ветки (v1.31) на Debian 13 не ставятся |
| Сеть между хостами | **WireGuard между хостами** | хосты в разных сетях; в одном L2-сегменте — «мост»; если машине нужен адрес прямо в сети хостов и моста на ней нет — «macvtap» с устройством в каждой строке размещения (там адрес закрепляет администратор сети, а проброс портов с хоста не работает) |
| Размещение, строка 1 | хост 1 · виртуальные машины · **control plane** · 1 | первый узел с API |
| Размещение, строка 2 | хост 1 · виртуальные машины · **worker** · 1 | |
| Размещение, строка 3 | хост 2 · виртуальные машины · **worker** · 1 | «добавить строку» |
| Образ | облачный образ из каталога хоста | например, Debian 13; «будет скачан», если его ещё нет |
| **Cilium** | включено | сеть подов вместо flannel |
| **заменить kube-proxy на Cilium** | включено | **обязательно**: без этого Ingress Cilium не работает |
| Пробросить наружу | только **API** (6443), HTTP и HTTPS — `0` | API — для `kubectl` со своей машины; 80/443 не нужны, вход — через шлюз |

Размеры машин — по умолчанию (2 CPU, 4096 МБ, 30 ГБ); меньше 2 CPU у
control plane kubeadm не поставится.

![Форма нового кластера](/screens/ru/k8s-first-pod-form.png)

Удобно **«сохранить набор»**: следующий такой кластер заполнится выбором
набора.

## 2. Сухой прогон

**«Сухой прогон»** проверяет каждый хост и пишет чек-лист в журнал поверх
формы (настройки остаются): ✓ — в порядке, ! — предупреждение, ✗ —
создание не пройдёт.

| ✗ в чек-листе | Что сделать |
|---|---|
| нет KVM / libvirtd не работает | включить виртуализацию в BIOS или у провайдера (нужна KVM, не контейнер), `systemctl enable --now libvirtd` |
| мало памяти или диска | уменьшить машины или освободить место |
| нет связи между хостами (ping, адрес для WireGuard) | открыть UDP 51820, вписать адрес в «Адреса хостов для соседей» |
| образ или пакет недоступен | интернет у хоста или кэш пакетов хаба |
| имя машины занято | другое имя кластера |

Галочка **«заодно подготовить»** сразу скачивает образ и пакеты в кэш и
поднимает сеть libvirt — создание потом идёт быстрее.

## 3. Создание

**«Создать кластер»** — задание хаба, журнал открывается сразу. Шаги:

1. **Проверки перед созданием** — то же, что сухой прогон.
2. **Туннель WireGuard между хостами** — ключи, `wg-quick@nktwg<N>` и своя
   сеть машин на каждом хосте; проверка рукопожатия каждой пары хостов.
3. **Машины** — по одной на строку размещения: облачный образ, cloud-init,
   nkt внутри.
4. **Control plane** — containerd, пакеты Kubernetes, `kubeadm init`,
   Cilium (`cilium install` с заменой kube-proxy, ожидание готовности).
5. **Присоединение узлов** — `kubeadm join` обоих worker.
6. **Ожидание Ready, проброс, kubeconfig** — все узлы `Ready`, проброс API,
   kubeconfig — в карточку кластера.

Время зависит в основном от скачивания: образ машин, пакеты Kubernetes,
образы Cilium; с «заодно подготовить» и кэшем хаба повторные кластеры
создаются заметно быстрее. Упало — исправьте
причину из журнала и **«продолжить»** у кластера: созданные машины и
установленные роли не делаются заново.

## 4. Проверить кластер

У готового кластера — кнопка **«kubectl»**: терминал на первом control plane
с готовым `kubectl` (автодополнение, алиас `k`).

```sh
kubectl get nodes -o wide        # три узла, все Ready
cilium status --wait             # Cilium: OK, kube-proxy заменён
kubectl get pods -A              # системные поды Running
```

**«kubeconfig»** в карточке — файл администратора для `kubectl` на своей
машине; адрес API в нём — хост первого control plane и порт из «Пробросить
наружу».

Терминал из карточки уже с `KUBECONFIG` — важно для `cilium`: без него CLI
уходит на `http://localhost:8080` и отвечает ошибками `status`, которые
ничего не проверяют.

## 5. Ingress: две ветки по варианту кластера

Что есть в кластере — видно сразу:

```sh
kubectl get ingressclass
```

### kubeadm: включить Ingress в Cilium

Своего ingress-контроллера у kubeadm нет — дальше считается Ingress Cilium;
свой контроллер (Traefik, NGINX) — та же схема, только свой порт. Ingress
Cilium в режиме **host network** слушает порт **8080** прямо на каждом узле,
LoadBalancer не нужен.

```sh
# Версия работающего Cilium: без --version «cilium upgrade» обновил бы
# заодно и сам Cilium — до версии, встроенной в CLI.
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

Проверить:

```sh
kubectl get ingressclass                  # cilium (default)
ss -ltn | grep ':8080 '                   # Envoy слушает 8080 на узле
```

Настройка хранится в значениях Helm самого Cilium — переживает перезагрузки
узлов и добавление новых. Порт можно сменить
(`--set ingressController.hostNetwork.sharedListenerPort=…`), только тогда
и в маршруте ниже — тот же.

### k3s: Traefik уже стоит

k3s приносит Traefik сам, nkt при установке его не отключает — включать
нечего: `kubectl get ingressclass` даёт `traefik` с пометкой `(default)`, под
`traefik` сидит в kube-system, а рядом `svclb-traefik-*` на **каждом** узле —
они держат порты **80** и **443** этого узла.

```sh
kubectl -n kube-system get pods -o wide | grep -E 'traefik|svclb'
```

Порт целей маршрута — **80** узла. Слушателя в `ss -ltn` на хосте при этом
нет: порт держит не сокет в сетевом пространстве хоста, а правило
kube-proxy (с заменой на Cilium — eBPF), так что проверять надо адресом
узла, а не списком слушателей:

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://$(hostname -I | awk '{print $1}'):80/
# 404            ← до шага 6 это норма: Traefik жив, ни одно правило не подходит
```

По `127.0.0.1:80` ответ может не прийти вовсе — так не проверяют.

## 6. Тестовый под

`traefik/whoami` отвечает текстом: имя пода и заголовки запроса — сразу видно,
какой под ответил и с какого адреса пришёл клиент.

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
  ingressClassName: cilium      # на k3s — traefik
  rules:
    - host: hello.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: hello, port: {number: 80}}
EOF

kubectl get pods -l app=hello -o wide     # два пода Running, на разных worker
kubectl get ingress hello
```

`ingressClassName` — контроллер своего варианта: `cilium` на kubeadm,
`traefik` на k3s. Не угадать — и Ingress не возьмёт никто, снаружи будет
молчание. `kubectl get ingress` адрес может не показать: у host network
контроллера адреса в статусе нет, проверять ответом ниже.

Проверка изнутри — через Ingress на адресе этого узла (**8080** на kubeadm,
**80** на k3s):

```sh
curl -s -H 'Host: hello.example.com' http://$(hostname -I | awk '{print $1}'):8080/
# Hostname: hello-…   ← под, который ответил
```

`kubectl exec` в этот под ничего не покажет: у `traefik/whoami` нет ни шелла,
ни `env` — exec вернёт «executable file not found in $PATH», и это не ошибка
сети.

Тот же YAML можно применить из раздела «Кластеры» → «Манифесты» — сразу в
несколько кластеров, с `kubectl diff` перед записью.

## 7. Маршрут Traffic Fly

«Traffic Fly» → **«Новый маршрут»**:

| Поле | Значение |
|---|---|
| Вид | **HTTPS по имени (443)** |
| Имена | `hello.example.com` |
| Сертификат на шлюзе (Let's Encrypt) | включено — цели отдаётся обычный HTTP |
| Проверка целей | **TCP-подключение** |
| **Кластер** | `lab` |
| **Порт на узле** | **8080** (kubeadm) / **80** (k3s) |

Цели у привязанного маршрута руками не перечисляют: их строит автомат по
узлам кластера — узел прибыл, цель появилась, узел ушёл, цель ушла. В
редакторе под полем это сказано подсказкой, а в списке целей у каждой стоит
метка «кластер lab». «Порт на узле» можно оставить пустым, если в карточке
кластера задан **«Ingress для клиентов»** (узел и порт HTTP
ingress-контроллера) — автомат возьмёт порт оттуда.

Сохранить → **«Применить»**: хаб поднимет туннели Traffic Fly на обоих хостах
(если их ещё нет), шлюз выпустит сертификат, а цели соберёт по узлам. Через
минуту у маршрута три цели — все **живые**.

Перекладывать их потом не руками: это делает задание создания и удаления
кластера, наблюдение (раз в минуту, не чаще раза в 10 минут на кластер) и
кнопки — **«Цели Traffic Fly»** в карточке кластера и **«Сверить цели»** в
самом маршруте.

![Маршрут, привязанный к кластеру](/screens/ru/k8s-first-pod-route.png)

## 8. Проверить снаружи

С любой машины в интернете:

```sh
curl -s https://hello.example.com/
# Hostname: hello-5d…            ← под
# X-Forwarded-For: 203.0.113.55  ← ваш настоящий адрес
# X-Forwarded-Proto: https
```

Несколько запросов подряд — отвечают разные поды.

**Отказ узла:** выключите машину `lab-w-2` (или хост 2). Шлюз за несколько
секунд пометит её цель «не жива» и выведет из маршрута, Kubernetes
перезапустит под на оставшемся worker — `curl` продолжает отвечать.
Включите машину — цель вернётся сама.

Удалите машину из хаба — цель уйдёт из пула вместе с ней, и имя сайта
останется на живых узлах; руками править цели для этого не нужно.

## 9. Убрать

```sh
kubectl delete ingress,service,deployment hello
```

Маршрут — «Удалить» в «Traffic Fly» и «Применить». Весь кластер —
**«удалить кластер»** в карточке: машины с дисками, туннель WireGuard и сеть
машин уходят с хостов, хосты-узлы (если были) остаются. Задание удаления
снимает с карты и привязанные к кластеру маршруты — вести трафик больше некуда.

## По API и из n8n

Шаги 6–7 — и без интерфейса: манифест в кластеры — API-токеном
(`POST /api/hub/k8s/manifests/apply?job=1`) или в n8n **Cluster → Apply
Manifest**; применить Traffic Fly — `POST /api/hub/fly/apply` или
**Traffic Fly → Apply**; весь путь целиком — сохранённым сценарием
(**Script → Run**); сверить цели привязанного маршрута —
`POST /api/hub/fly/cluster {"cluster":"lab"}`. Создание кластера и kubeconfig
по токену закрыты.
Подробно — [API хаба](/guide/hub-api#что-доступно-токену), [n8n](/guide/n8n).

## Что дальше

- **Helm в кластеры** — релиз в выбранные кластеры заданием хаба.
- **Манифесты** — один YAML в несколько кластеров с `kubectl diff`.
- **Обновить** — версия Kubernetes по узлам: cordon, drain, обновление,
  uncordon.
- **+ worker** — ещё узел в готовый кластер (та же ветка Kubernetes);
  привязанный маршрут возьмёт его в цели сам, как только узел станет `Ready`.

## То же сценарием

Всё от создания кластера до ответа `https://hello.example.com` — одним
сценарием хаба «Кластер Kubernetes: от нуля до первого пода» (он же —
[`examples/scripts/k8s-first-pod.nkt`](https://github.com/piqab/nkt/blob/main/examples/scripts/k8s-first-pod.nkt), нужен хаб 1.13.68+).

Как запустить: **«Профили» → вкладка «Сценарии» → «Новый сценарий»**, в
окне редактора — **«Готовые сценарии ▾»** → «Кластер Kubernetes: от нуля до первого пода»; впишите имя →
«Сохранить» → «Проверить» → «Сухой прогон» → «Выполнить» (параметры
спросит при запуске, ход — в журнале задания). Готовые сценарии есть и на
вкладке «Справка» страницы сценариев (кнопка «В редактор»).

При запуске спрашивает два хоста и имя сайта. «Сухой прогон» проверит хосты
(как «Сухой прогон» формы кластера), остальное — «сделал бы»: кластера до
создания ещё нет.

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

- **`on … k8s create`** — шаги 1–3: форма кластера одной строкой (kubeadm,
  размещение по двум хостам, WireGuard, Cilium без kube-proxy, наружу —
  только API).
- **`k8s lab ingress cilium port 8080`** — шаг 5: та же команда `cilium
  upgrade` с версией работающего Cilium, заданием на control plane. На k3s
  шаг пропускают (Traefik приезжает с самим k3s), а в манифесте —
  `ingressClassName: traefik`.
- **`k8s lab apply … end`** и **`k8s lab wait deployment hello`** — шаг 6.
- **`fly route … targets cluster lab port 8080`** — шаг 7: маршрут,
  привязанный к кластеру; цели он строит по его узлам и применяет сразу. На
  k3s в этом шаге `port 80`.
- **`wait url`** — шаг 8: ответ снаружи, с хаба по настоящему имени.

## Если не работает

| Симптом | Причина и что делать |
|---|---|
| узел `NotReady` | Cilium на узле не поднялся: `cilium status`, `kubectl -n kube-system get pods -o wide`; узел без связи с control plane — туннель WireGuard (рукопожатие в журнале создания) |
| поды `Pending` | мало ресурсов: `kubectl describe pod`; на control plane kubeadm поды не ставит (taint) — нужен хотя бы один worker |
| `ImagePullBackOff` | у узлов нет доступа к registry: интернет хоста или кэш пакетов хаба (зеркало registry хаба) |
| `cilium status` отвечает сплошными `dial tcp [::1]:8080: connect: connection refused` | CLI запускают без kubeconfig — он идёт на `http://localhost:8080` и эти «ошибки» ничего не проверяют: работайте из терминала карточки кластера (там `KUBECONFIG` уже задан) или экспортируйте сами |
| `kubectl get ingressclass` пусто | **kubeadm**: Cilium без замены kube-proxy или upgrade не применился — шаг 5, `cilium status`. **k3s**: Traefik отключён при установке — проверьте `kubectl -n kube-system get pods -o wide \| grep traefik` |
| Ingress есть, снаружи молчание | `ingressClassName` не от того контроллера: Traefik не берёт `cilium`, Ingress Cilium не берёт `traefik` — шаг 6 |
| `curl … :8080` с узла — 404 | имя в `Host:` не совпало с `host:` в Ingress — или порт не от варианта кластера: **8080** у kubeadm, **80** у k3s |
| на узле k3s `ss -ltn` не показывает `:80` | это не симптом: порт держит `svclb-traefik-*` (правило kube-proxy или eBPF у Cilium), слушателя в сетевом пространстве хоста нет — проверять `curl` на адрес узла |
| цель маршрута «не жива» | на узле не слушает порт ingress (8080 / 80 — шаги 5 и 6), туннель Traffic Fly хоста не поднят — подсказка под шлюзом в «Traffic Fly» |
| `kubectl exec` или `logs` пода на **одном** узле: `proxy error from 127.0.0.1:6443 while dialing <узел>:10250, code 502`, узел при этом `Ready` | **k3s**: эти запросы идут не напрямую на kubelet, а через туннель, который агент сам открыл на `server:6443`, — проверка «открыта ли 10250 с другой машины» ничего не доказывает; 502 — состояние агента этого узла, `systemctl restart k3s-agent` на worker (на control plane — `k3s`). **kubeadm**: путь apiserver → kubelet прямой, смотреть `ss -ltn \| grep ':10250 '` на узле и маршрут до него |
| сертификат не выпускается | A-запись `hello.example.com` не на адрес шлюза или закрыт его порт 80 |
| `kubeadm` не ставится на Debian 13 | выбрана старая ветка (v1.31): `sqv` отвергает её подпись — выберите актуальную |
| шаг «установка роли» падает на `FileAvailable--etc-kubernetes-manifests-kube-apiserver.yaml` или на `error execution phase check-etcd` | узел недоделан после прошлой попытки (вход оборвался на фазе `etcd-join`, а манифесты control plane уже лежат): повтор сносит состояние сам, каскадом `kubeadm reset`. Если повтор тоже упал — в журнале видно, на какой попытке reset отказывает; руками — `kubeadm reset -f --skip-phases=remove-etcd-member` на этом узле, потом повтор задания |
| узел на **macvtap** не отвечает с того хоста, где он заведён | это не поломка: хост со своими macvtap-машинами не говорит (ограничение Linux, а не настройка) — проверять доступ надо с другой машины или с хаба, а узлы кластера видят его по настоящему адресу в сети хостов |
| у машины на **macvtap** адрес так и не появился, в журнале — «гостевой агент не отвечает» | в этом режиме адрес сообщает только `qemu-guest-agent`: ни DHCP-аренды libvirt, ни строки в ARP-таблице моста у машины нет, а канал агента — virtio-порт, а не сеть, поэтому хост слышит его даже у своих macvtap-машин. Агент ставится пакетом при первом запуске и там же запускается; если он стоит и молчит — `systemctl enable --now qemu-guest-agent` внутри машины (или её перезапуск); адрес можно и вписать самому |
| узел на **macvtap** был `Ready` и выпал после перезапуска машины, у соседей `no route to host` к его адресу | адрес машине выдаёт DHCP сети хостов, и закрепить его хаб нечем — сети libvirt с её бронями здесь нет: бронь по MAC или статика на узле, это делает администратор сети (в журнале создания для этого отдельная строка); маршруты Traffic Fly, проброс и kubeconfig держат именно этот адрес |
