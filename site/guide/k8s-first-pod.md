# Кластер Kubernetes: от нуля до первого пода

Пошагово: кластер **kubeadm** из трёх машин на **двух хостах** (сеть
**WireGuard** между ними), сеть подов **Cilium**, и тестовый под, который
открывается снаружи по `https://hello.example.com` через **Ingress** и шлюз
[Traffic Fly](/guide/traffic-fly). Справочно о кластерах —
[Кластеры Kubernetes](/guide/hub-clusters).

Как устроен вход снаружи: Ingress-контроллер Cilium слушает порт **8080** на
каждом узле, маршрут Traffic Fly ведёт с VPS-шлюза на `узел:8080` всех трёх
машин, сертификат `hello.example.com` выпускает шлюз. Пробрасывать 80/443
хостов не нужно, упавший узел шлюз выводит из маршрута сам.

```
клиент ─https─▶ шлюз Traffic Fly ─WireGuard─▶ хост 1 ─▶ cp-1:8080, w-1:8080
  (сертификат,                              ─▶ хост 2 ─▶ w-2:8080
   проверка целей)                    Ingress Cilium ─▶ под whoami (любой узел)
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
- **Шлюз Traffic Fly** — VPS с nkt-edge в роли fly: «Traffic Fly» →
  «Шлюзы» → «Добавить шлюз» ([nkt-edge](/guide/edge)).
- **Домен:** A-запись `hello.example.com` — на адрес шлюза.

## 1. Новый кластер

«Кластеры» → **«Новый кластер»** — «Новый кластер Kubernetes на нескольких
хостах».

| Поле | Значение | Зачем |
|---|---|---|
| Имя кластера | `lab` | имя машин и карточки |
| Вариант | **kubeadm** | стандартный Kubernetes |
| Версия Kubernetes | актуальная stable | одна на все узлы; старые ветки (v1.31) на Debian 13 не ставятся |
| Сеть между хостами | **WireGuard между хостами** | хосты в разных сетях; в одном L2-сегменте — «мост» |
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

## 5. Включить Ingress в Cilium

В том же терминале — Ingress-контроллер Cilium в режиме **host network**: он
слушает порт **8080** прямо на каждом узле, LoadBalancer не нужен.

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
  ingressClassName: cilium
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

Проверка изнутри — через Ingress на этом узле:

```sh
curl -s -H 'Host: hello.example.com' http://$(hostname -I | awk '{print $1}'):8080/
# Hostname: hello-…   ← под, который ответил
```

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
| Цели | «Добавить цель» ×3: хост 1 · **виртуальная машина** · `lab-cp-1`, порт **8080**; хост 1 · `lab-w-1`, 8080; хост 2 · `lab-w-2`, 8080 |

Имена машин — как в карточке кластера (там же видно, на каком они хосте).
Сохранить → **«Применить»**: хаб поднимет туннели Traffic Fly на обоих хостах
(если их ещё нет), шлюз выпустит сертификат. Через минуту у маршрута все три
цели — **живые**.

![Маршрут на три узла кластера](/screens/ru/k8s-first-pod-route.png)

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

## 9. Убрать

```sh
kubectl delete ingress,service,deployment hello
```

Маршрут — «Удалить» в «Traffic Fly» и «Применить». Весь кластер —
**«удалить кластер»** в карточке: машины с дисками, туннель WireGuard и сеть
машин уходят с хостов, хосты-узлы (если были) остаются.

## Что дальше

- **Helm в кластеры** — релиз в выбранные кластеры заданием хаба.
- **Манифесты** — один YAML в несколько кластеров с `kubectl diff`.
- **Обновить** — версия Kubernetes по узлам: cordon, drain, обновление,
  uncordon.
- **+ worker** — ещё узел в готовый кластер (та же ветка Kubernetes); в
  маршрут Traffic Fly — ещё одна цель на его 8080.

## То же сценарием

Всё от создания кластера до ответа `https://hello.example.com` — одним
сценарием хаба («Сценарии» → вкладка «Справка» → «Готовые сценарии» →
«Кластер Kubernetes: от нуля до первого пода» → «В редактор», он же —
[`examples/scripts/k8s-first-pod.nkt`](https://github.com/piqab/nkt/blob/main/examples/scripts/k8s-first-pod.nkt)).
При запуске спрашивает два хоста и имя сайта. «Сухой прогон» проверит хосты
(как «Сухой прогон» формы кластера), остальное — «сделал бы»: кластера до
создания ещё нет.

```
# Кластер Kubernetes: от нуля до первого пода
param HOST1 "хост с control plane и первым worker"
param HOST2 "хост со вторым worker"
param DOMAIN "имя тестового сайта — A-запись на адрес шлюза Traffic Fly"

# kubeadm: 1 control plane + 2 worker на двух хостах, WireGuard, Cilium без kube-proxy
on ${HOST1} k8s create lab flavor kubeadm nodes "${HOST1}: cp 1, w 1; ${HOST2}: w 1" network wireguard cni cilium no-kube-proxy image debian-13 expose http 0 https 0

# Ingress Cilium: порт 8080 на каждом узле
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
  upgrade` с версией работающего Cilium, заданием на control plane.
- **`k8s lab apply … end`** и **`k8s lab wait deployment hello`** — шаг 6.
- **`fly route … targets cluster lab port 8080`** — шаг 7: маршрут на все
  узлы кластера; применяется сразу.
- **`wait url`** — шаг 8: ответ снаружи, с хаба по настоящему имени.

## Если не работает

| Симптом | Причина и что делать |
|---|---|
| узел `NotReady` | Cilium на узле не поднялся: `cilium status`, `kubectl -n kube-system get pods -o wide`; узел без связи с control plane — туннель WireGuard (рукопожатие в журнале создания) |
| поды `Pending` | мало ресурсов: `kubectl describe pod`; на control plane kubeadm поды не ставит (taint) — нужен хотя бы один worker |
| `ImagePullBackOff` | у узлов нет доступа к registry: интернет хоста или кэш пакетов хаба (зеркало registry хаба) |
| `kubectl get ingressclass` пусто | Cilium без замены kube-proxy или upgrade не применился — шаг 5, `cilium status` |
| `curl … :8080` с узла — 404 | имя в `Host:` не совпало с `host:` в Ingress |
| цель маршрута «не жива» | на узле не слушает 8080 (шаг 5), туннель Traffic Fly хоста не поднят — подсказка под шлюзом в «Traffic Fly» |
| сертификат не выпускается | A-запись `hello.example.com` не на адрес шлюза или закрыт его порт 80 |
| `kubeadm` не ставится на Debian 13 | выбрана старая ветка (v1.31): `sqv` отвергает её подпись — выберите актуальную |
