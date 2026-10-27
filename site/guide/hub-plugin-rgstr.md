---
title: 'rgstr и rgstroperator в nkt'
---

# rgstr и rgstroperator в nkt

[rgstr](https://github.com/piqab/rgstr) — лёгкий реестр образов OCI/Docker
на Go (MIT): `docker push/pull`, проекты с квотами, роботы, вебхуки,
политики хранения и неизменяемости, сборка мусора, журнал и веб-интерфейс
`/ui`. Данные лежат на диске, базы нет.
[rgstroperator](https://github.com/piqab/rgstroperator) ставит его в
Kubernetes одним ресурсом `Registry`.

Обе программы — сторонний открытый код: nkt их не меняет, а только
выкладывает и ведёт. В статье — три пути:

1. **Реестр на хосте** — конвейер из «Примеров» плюс плагин хаба `rgstr`.
2. **Реестр в кластере** — оператор от администратора, ресурс `Registry`.
3. **Реестр клиента в своём namespace** — тот же `Registry` от токена,
   наружу через Traffic Fly.

Общий принцип плагинов — [Плагины](/guide/hub-plugins). Как такой плагин
пишется — [Пишем плагин по шагам](/guide/plugin-step-by-step): статья
разбирает учебный `rgstr-lite`.

## 1. Реестр на хосте

### Выкладка

«Конвейеры» → «Примеры» → **«rgstr — реестр образов»** → хост и домен.
Пример — [`examples/rgstr`](https://github.com/piqab/nkt/tree/main/examples/rgstr):

```yaml
action: compose
compose:
  file: examples/rgstr/deploy/docker-compose.yml
  project: rgstr
  hosts: [web1]
  env:
    RGSTR_DOMAIN: registry.example.com
    RGSTR_AUTH_SECRET: generate:hex32
    RGSTR_ADMIN_USER: admin
    RGSTR_ADMIN_PASSWORD: generate:password
  site:
    domains: [registry.example.com]
    service: rgstr
    port: 5000
    max_body: "0"
```

- `generate:` — секрет токенов и пароль первого администратора создаёт
  хаб. Оба лежат в «Секреты» → «.env» конвейера.
- `max_body: "0"` — слои образов идут потоком, без предела размера и без
  буферизации у прокси.
- HTTPS даёт сайт хоста: docker без TLS к реестру не ходит, если реестр не
  на loopback.

**Первый вход.** Пароль `RGSTR_ADMIN_PASSWORD` — **временный**. Откройте
`https://registry.example.com/ui`, войдите как `admin` и смените его. До
смены временным паролем не работают ни `docker login`, ни API: rgstr
отвергает такой Basic-вход. Значит, не работает и плагин.

### Плагин

«О системе» → «Плагины» → «Каталог» → **rgstr** → «Установить на хост».
Хост — тот, где rgstr (или любой, откуда он виден).

| Настройка | Что |
|---|---|
| `RGSTR_URL` | откуда плагин видит rgstr; на хосте rgstr — `http://127.0.0.1:5000` |
| `RGSTR_USER`, `RGSTR_PASSWORD` | администратор rgstr с **уже сменённым** паролем; пароль — секрет (файл окружения службы `0600`) |
| `RGSTR_PUBLIC` | адрес для docker и конвейеров — `registry.example.com` |
| `RGSTR_CA_FILE` | PEM своего УЦ, если HTTPS rgstr подписан им |

Служба `nkt-plugin-rgstr` слушает 127.0.0.1:9280 и работает без Docker.

### Что видно в хабе

- **Сводка** — версия, вход, репозитории, скачивания, диск, сборка
  мусора, политики и **находки**:
  - вход выключен;
  - rgstr по HTTP не на loopback;
  - у пользователя временный пароль;
  - роль `user` с правом удалять;
  - робот с записью во все репозитории (`*` + push);
  - мало места на диске;
  - сборка мусора выключена, давно не шла или упала;
  - нет политик хранения;
  - `/metrics` открыт без входа.
- **Репозитории → Теги** — digest, размер, платформы, время загрузки,
  «неизменяемый». Удалить тег можно с подтверждением; доступно сухим
  прогоном.
- **Проекты** — создать (публичный, квота в ГБ) и удалить пустой.
- **Роботы** — создать (проект или `*`, pull или pull+push, срок),
  выдать новый секрет, удалить. Куда идёт секрет — ниже.
- **Политики** — хранение и неизменяемость: правка JSON-текстом в окне с
  диффом и историей версий. «Применить хранение» — сразу или сухим
  прогоном.
- **Пользователи, вебхуки** («Проверить» шлёт ping), **журнал**
  (последние 200 записей).

### Роботы — ключи registry конвейеров

Конвейеру, который берёт образы из rgstr, нужен ключ в «Секреты» →
«Реестры». Плагин кладёт его туда сам — секрет никто не видит.

**В окне:** «Роботы» → «Создать» → поля «Сразу в конвейер» и «Имя
переменной» = `@registry`. Хаб запишет ключ registry
`registry.example.com`, логин `robot$ci-shop` и секрет в реестры
конвейера:

- ключ с тем же адресом заменяется, его CA сохраняется;
- запись — с историей версий;
- в журнале действий — без значения.

Обычное имя вместо `@registry` (`REGISTRY_TOKEN`) — запись в `.env`
конвейера.

**Сценарий:**

```
rgstr project shop quota 20
rgstr robot ci-shop project shop to-pipeline shop env @registry
deploy shop
```

**API** (токен с действиями «плагин rgstr» и «pipeline.create» в своих
пределах):

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"robot_create","args":{"name":"ci-shop","project":"shop","to_pipeline":"shop","env":"@registry"}}' \
  https://hub.example.com/api/hub/plugins/rgstr/call
```

**MCP:** `rgstr_robot_create` с `to_pipeline` и `env` (`@registry` или
имя переменной). Агент значение не получает никогда: без этих аргументов
операция ему недоступна.

Права хаб проверяет **до** вызова плагина. Робот, секрет которого некуда
положить, не создаётся.

### Сценарии, API, MCP

Команды (слово `rgstr`; справка — в разделе плагина и в
[командах сценариев](/guide/script-commands#cmd-plugin)):

```
rgstr repos
rgstr tags shop/web
rgstr tag-delete shop/web 1.0.0
rgstr gc
rgstr retention run
rgstr project shop quota 20
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

«Проверить» сценарий спрашивает плагин, правильно ли написаны слова. Сухой
прогон только пишет, что было бы сделано.

Чистка по ночам — сохранённый сценарий:

```
rgstr retention run
rgstr gc
```

Запуск — по расписанию или через API:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"dry_first":true}' https://hub.example.com/api/hub/scripts/7/run
```

Любая операция плагина — через `POST /api/hub/plugins/rgstr/call`:

```sh
# что занято
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"repos"}' https://hub.example.com/api/hub/plugins/rgstr/call
# сколько освободит сборка мусора — без удаления
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"gc_run","dry_run":true}' https://hub.example.com/api/hub/plugins/rgstr/call
```

**Инструменты MCP:**

- чтение (любой токен): `rgstr_summary`, `rgstr_repos`, `rgstr_tags`,
  `rgstr_projects`, `rgstr_robots`, `rgstr_users`, `rgstr_policies`;
- изменения (токен с «плагин rgstr»): `rgstr_tag_delete`, `rgstr_gc_run`,
  `rgstr_retention_run`, `rgstr_project_create`, `rgstr_project_delete`,
  `rgstr_robot_create`, `rgstr_robot_secret`, `rgstr_robot_delete`.

Полный список с аргументами — «Плагины» → rgstr → **«Для агентов»**.

## 2. Реестр в кластере (администратор)

### Оператор

Оператор ставит администратор: у него ClusterRole, CRD и свой namespace.
Токену это недоступно.

**Helm в кластеры** (раздел «Кластеры» или API, заданием):

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' -d '{
  "clusters": [3],
  "release": {"repo_url": "oci://ghcr.io/piqab/charts", "chart": "rgstroperator", "version": "0.2.1",
              "release": "rgstroperator", "namespace": "rgstroperator-system"}
}' https://hub.example.com/api/hub/k8s/helm/install
```

Или **манифестом** — «Манифесты» раздела «Кластеры» с содержимым
`https://github.com/piqab/rgstroperator/releases/latest/download/operator.yaml`.
Через API — `POST /api/hub/k8s/manifests/apply?job=1`
(`{"name","note","content","clusters"}`).

### Registry

```
k8s lab apply
apiVersion: registry.rgstr.io/v1alpha1
kind: Registry
metadata:
  name: registry
  namespace: registry
spec:
  storage:
    size: 50Gi
  auth:
    enabled: true
    admins: [admin]
  ingress:
    enabled: true
    host: registry.example.com
    className: cilium
end
k8s lab wait deployment registry namespace registry 5m
```

- Deployment, Service и Ingress называются как `Registry`.
- Без `auth.secretRef` оператор создаёт Secret `<имя>-auth` со случайным
  `RGSTR_AUTH_SECRET` и **временным** `RGSTR_ADMIN_PASSWORD`. Первый вход
  в `/ui` — как в части 1.
- `className` — ingress-класс кластера (у кластеров nkt с Cilium —
  `cilium`).
- TLS — `ingress.tls` (cert-manager или свой Secret) или сертификат на
  шлюзе Traffic Fly.

Плагин `rgstr` ставится так же, как в части 1: `RGSTR_URL` — адрес, откуда
плагин видит реестр (Ingress или Traffic Fly).

## 3. Реестр клиента в своём namespace

Клиент с токеном и [своим namespace](/guide/hub-api) в общем кластере
может держать свой rgstr. Для этого — три условия.

### Администратор: «Ресурсы для клиентов»

Роль `edit` в namespace клиента не включает чужие CRD. «Кластеры» → у
кластера **«Ресурсы для клиентов»** → строка `registries.registry.rgstr.io`
→ дифф → «Сохранить».

Хаб применит на кластере ClusterRole `nkt-client-crds` с метками
`aggregate-to-edit` и `aggregate-to-admin`. Kubernetes добавит эти права к
`edit` в каждом namespace: клиент управляет только своими `Registry`. Пустой
список — права убраны.

Группы Kubernetes (`*.k8s.io`), прав, допуска, сети, политик и
`cilium.io` хаб не отдаёт. Отдавать ресурсы, которые действуют за пределами
namespace, не стоит и среди остальных: решает администратор. Список по
кластерам хранится на хабе и едет в экспорт.

API: `GET`/`PUT /api/hub/clusters/{id}/client-crds`:

```sh
curl -sS -X PUT -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' \
  -d '{"resources":["registries.registry.rgstr.io"]}' https://hub.example.com/api/hub/clusters/3/client-crds
```

Ещё администратору нужно задать **«Ingress для клиентов»** — без него
маршрут Traffic Fly на namespace не завести
([Кластеры](/guide/hub-clusters)).

### Клиент: Registry под защитой namespace

В namespace клиента действуют:

- Pod Security `restricted`;
- квота и LimitRange по потолкам токена;
- входящие — только из этого namespace;
- наружу — только Ingress с именем в домене токена;
- Service — только `ClusterIP`.

Политики допуска проверяют и то, что создаёт оператор, поэтому в
`Registry` нужно указать всё явно:

```yaml
apiVersion: registry.rgstr.io/v1alpha1
kind: Registry
metadata:
  name: registry
  namespace: shop
spec:
  serviceType: ClusterIP              # NodePort и LoadBalancer — нельзя
  storage:
    size: 10Gi
  auth:
    enabled: true
  ingress:
    enabled: true
    host: registry.shop.client.example.com   # только в домене токена
    className: cilium                 # ingress-класс кластера
  pod:
    securityContext:                  # образ rgstr по умолчанию — root; restricted требует иначе
      runAsNonRoot: true
      runAsUser: 65532
      runAsGroup: 65532
      fsGroup: 65532                  # том данных — на запись этому пользователю
      seccompProfile:
        type: RuntimeDefault
    containerSecurityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop: [ALL]
```

Без `securityContext` под не пройдёт `restricted` и останется без реплик,
причина будет в событиях ReplicaSet. С `serviceType: NodePort` или чужим
`ingress.host` хаб откажет ещё до применения: он проверяет `Registry` так
же, как Service и Ingress. `backup` и `metrics.serviceMonitor` в
namespace клиента не нужны — это ресурсы администратора.

**Как применить:**

- своим kubeconfig: `kubectl --kubeconfig shop.kubeconfig apply -f
  registry.yaml`;
- сценарием от токена (действие `k8s.apply`):

  ```
  k8s shared apply
  …тот же YAML…
  end
  k8s shared wait deployment registry namespace shop 5m
  fly own registry.shop.client.example.com cluster shared namespace shop
  ```

**Пароль администратора:**

```sh
kubectl --kubeconfig shop.kubeconfig -n shop get secret registry-auth \
  -o jsonpath='{.data.RGSTR_ADMIN_PASSWORD}' | base64 -d
```

Пароль временный — смените его в `https://registry.shop.client.example.com/ui`.

### Наружу через Traffic Fly

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"registry.shop.client.example.com","cluster":"shared","namespace":"shop"}' \
  https://hub.example.com/api/hub/own/fly
```

- То же делают сценарий `fly own … cluster … namespace …` и MCP
  `fly_own_add`.
- Сертификат — на шлюзе, до ingress кластера идёт HTTP.
- `auth.realm` оператор по умолчанию строит из `ingress.host`
  (`https://<host>/v2/auth`), поэтому `docker login
  registry.shop.client.example.com` работает без доп. настроек.

Плагин `rgstr` к такому реестру ставит администратор, если хочет видеть
его в хабе. Клиенту плагин не нужен: у него есть `/ui` и API rgstr.

## Что не делает nkt

- Не меняет rgstr и rgstroperator: всё, что выше, — их документированные
  настройки. Новые версии подхватываются тегом образа и версией чарта.
- Не держит пароль администратора rgstr нигде, кроме окружения службы
  плагина. Секреты роботов не попадают ни в журналы, ни к агенту.
- `/metrics` у rgstr — без входа. Плагин находит это, если адрес виден
  снаружи. Закройте его на прокси или оставьте только внутри кластера.
