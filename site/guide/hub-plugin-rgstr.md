---
title: 'rgstr и rgstroperator в nkt'
---

# rgstr и rgstroperator в nkt

[rgstr](https://github.com/piqab/rgstr) — лёгкий реестр образов OCI/Docker
на Go (MIT): `docker push/pull`, проекты с квотами, роботы, вебхуки,
политики хранения и неизменяемости, сборка мусора, журнал и веб-интерфейс
`/ui`. С версии 1.10 у него есть:

- **проекты-кеши** Docker Hub, GHCR и любых реестров (pull-through): лимиты
  Docker Hub не мешают, образы доступны, когда upstream лежит;
- **сканирование уязвимостей** встроенным Trivy и запрет pull уязвимых
  образов по проекту;
- **бэкапы** по расписанию в S3 или каталог и `rgstr restore`;
- **вход через LDAP / Active Directory и OIDC** с ролями по группам;
- **кластер** — несколько процессов на PostgreSQL и S3.

Один экземпляр хранит всё на диске, без базы.
[rgstroperator](https://github.com/piqab/rgstroperator) ставит rgstr в
Kubernetes одним ресурсом `Registry`.

Статья написана для rgstr 1.10.1 и rgstroperator 0.5.1.

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
    # необязательно:
    # RGSTR_SCAN_ENABLED: "false"
    # RGSTR_PROXIES: dockerhub=https://registry-1.docker.io
    # RGSTR_BACKUP_TARGET: s3://backups/rgstr
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

Образ — `ghcr.io/piqab/rgstr:1.10.1`, данные — в томе `rgstr-data`.

**Первый вход.** Пароль `RGSTR_ADMIN_PASSWORD` — **временный**. Откройте
`https://registry.example.com/ui`, войдите как `admin` и смените его. До
смены временным паролем не работают ни `docker login`, ни API: rgstr
отвергает такой Basic-вход. Значит, не работает и плагин.

### Скан, кеш, бэкапы

Всё задаётся в `env` конвейера: compose примера передаёт эти переменные
rgstr, пустые — значения rgstr по умолчанию.

**Скан уязвимостей** включён по умолчанию (`RGSTR_SCAN_ENABLED: "true"`).

- Trivy уже в образе. Его база (около 70 МБ) качается из интернета
  (`mirror.gcr.io`, запасной — `ghcr.io`) при первом скане и лежит в томе.
- Образы сканируются при push, повторно — раз в сутки (база узнаёт новые
  уязвимости).
- Скан берёт несколько сотен МБ памяти. На маленьком хосте или без доступа
  к ним задайте `"false"`; иначе каждый скан кончится ошибкой, и
  плагин покажет её находкой.
- Запрет pull уязвимых образов — в настройках проекта: «Запрещать pull с
  критическими уязвимостями» и т. п. Клиент получит `403 denied` с
  причиной.

**Кеш Docker Hub:**

```yaml
    RGSTR_PROXIES: dockerhub=https://registry-1.docker.io,ghcr=https://ghcr.io
    RGSTR_PROXY_DOCKERHUB_USERNAME: me              # необязательно: выше лимиты pull
    RGSTR_PROXY_DOCKERHUB_PASSWORD: dckr_pat_…      # токен доступа Docker Hub
```

```sh
docker pull registry.example.com/dockerhub/library/nginx:1.27   # = docker.io/library/nginx:1.27
```

- Теги перепроверяются в upstream раз в 5 минут запросом HEAD, слои
  скачиваются один раз.
- Push в проект-кеш отклоняется; старые образы чистят политики хранения.
- Токен Docker Hub после выкладки лежит в «Секреты» → «.env» конвейера.
  Кеш можно завести и в плагине или `/ui` (проект с адресом upstream);
  логин upstream тогда задаётся в `/ui` rgstr.

**Бэкапы в S3:**

```yaml
    RGSTR_BACKUP_TARGET: s3://backups/rgstr
    RGSTR_BACKUP_S3_ENDPOINT: https://storage.yandexcloud.net
    RGSTR_BACKUP_S3_REGION: ru-central1
    RGSTR_BACKUP_S3_ACCESS_KEY: …
    RGSTR_BACKUP_S3_SECRET_KEY: …
    # RGSTR_BACKUP_SCHEDULE: "0 3 * * *"   # UTC, по умолчанию
    # RGSTR_BACKUP_KEEP: "7"
```

- Бэкап делается на работающем реестре и целостен: сборка мусора на это
  время ждёт.
- Архив — `rgstr-backup-<время>.tar`; лишние сверх `KEEP` удаляются.
- Восстановление — `rgstr restore` (см. README rgstr): остановите стек,
  запустите `docker compose run --rm rgstr restore -force latest`,
  поднимите стек.

**LDAP, Active Directory, OIDC** — переменные `RGSTR_LDAP_*` и
`RGSTR_OIDC_*`
([README rgstr](https://github.com/piqab/rgstr/blob/main/README.ru.md#ldap-active-directory-и-единый-вход)).
Compose примера их не передаёт: для входа через каталог возьмите свой
compose с этими переменными. Пользователи SSO выполняют `docker login` с
**CLI-секретом** из `/ui` — браузерного входа у docker нет.

### Плагин

«О системе» → «Плагины» → «Каталог» → **rgstr** → «Установить на хост».
Хост — тот, где rgstr (или любой, откуда он виден).

| Настройка | Что |
|---|---|
| `RGSTR_URL` | откуда плагин видит rgstr; на хосте rgstr — `http://127.0.0.1:5000` |
| `RGSTR_USER`, `RGSTR_PASSWORD` | администратор rgstr с **уже сменённым** паролем; пароль — секрет (файл окружения службы `0600`) |
| `RGSTR_PUBLIC` | адрес для docker и конвейеров — `registry.example.com` |
| `RGSTR_CA_FILE` | PEM своего УЦ, если HTTPS rgstr подписан им |

Плагин 1.1 работает и со старыми rgstr: разделов, которых у реестра нет
(скан — с 1.10, бэкапы — с 1.8), он просто не показывает.

Служба `nkt-plugin-rgstr` слушает 127.0.0.1:9280 и работает без Docker.

### Что видно в хабе

- **Сводка** — версия, вход (и LDAP/OIDC), репозитории, скачивания, диск,
  сборка мусора, скан, бэкапы и **находки**:
  - вход выключен;
  - rgstr по HTTP не на loopback;
  - у пользователя временный пароль;
  - роль `user` с правом удалять;
  - робот с записью во все репозитории (`*` + push);
  - мало места на диске;
  - сборка мусора выключена, давно не шла или упала;
  - нет политик хранения;
  - `/metrics` открыт без входа;
  - скан выключен или кончается ошибкой (например, нет доступа к базе
    Trivy);
  - критические уязвимости в образах (первые пять тегов);
  - встроенные бэкапы не настроены, последний упал или опоздал (больше
    суток при ежедневном расписании);
  - кеш ходит в upstream по HTTP.
- **Репозитории → Теги** — digest, размер, платформы, время загрузки,
  «неизменяемый», **уязвимости** по уровням. Удалить тег можно с
  подтверждением; доступно сухим прогоном.
- **Уязвимости** тега — уровень, CVE, пакет, установленная версия и версия
  с исправлением, сначала критические. «Сканировать» ставит образ в
  очередь rgstr.
- **Проекты** — создать (публичный, квота в ГБ, **кеш** другого реестра,
  запрет pull уязвимых образов), сменить запрет pull, удалить пустой.
  Адрес upstream плагин принимает только вида `http(s)://хост[/путь]`,
  без логина в адресе.
- **Бэкапы** — куда, расписание, последний удачный и размер, ошибка,
  архивы в хранилище; «Сделать бэкап сейчас» (бэкап идёт в фоне).
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
rgstr project dockerhub proxy https://registry-1.docker.io block critical
rgstr scan shop/web 1.0
rgstr backup run
rgstr robot ci-shop project shop push to-pipeline shop env @registry
```

У `project` слова `quota`, `proxy` и `block` (`critical`, `high`,
`medium`, `low`) — в любом порядке.

«Проверить» сценарий спрашивает плагин, правильно ли написаны слова. Сухой
прогон только пишет, что было бы сделано.

Чистка по ночам — сохранённый сценарий:

```
rgstr retention run
rgstr gc
```

Бэкап перед обновлением реестра:

```
rgstr backup run
deploy rgstr
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
# уязвимости образа
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"vulns","args":{"repo":"shop/web","tag":"1.0"}}' https://hub.example.com/api/hub/plugins/rgstr/call
# запретить pull образов проекта с критическими уязвимостями
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"project_block","args":{"name":"shop","severity":"critical"}}' https://hub.example.com/api/hub/plugins/rgstr/call
```

**Инструменты MCP:**

- чтение (любой токен): `rgstr_summary`, `rgstr_repos`, `rgstr_tags`,
  `rgstr_vulns`, `rgstr_projects`, `rgstr_robots`, `rgstr_users`,
  `rgstr_policies`, `rgstr_backups`;
- изменения (токен с «плагин rgstr»): `rgstr_tag_delete`, `rgstr_gc_run`,
  `rgstr_retention_run`, `rgstr_scan_run`, `rgstr_backup_run`,
  `rgstr_project_create` (с `proxy_url` и `block_severity`),
  `rgstr_project_block`, `rgstr_project_delete`, `rgstr_robot_create`,
  `rgstr_robot_secret`, `rgstr_robot_delete`.

Агент может, например, найти теги с критическими уязвимостями
(`rgstr_repos` → `rgstr_vulns`) и включить запрет pull в проекте.

Полный список с аргументами — «Плагины» → rgstr → **«Для агентов»**.

## 2. Реестр в кластере (администратор)

### Оператор

Оператор ставит администратор: у него ClusterRole, CRD и свой namespace.
Токену это недоступно.

**Helm в кластеры** (раздел «Кластеры» или API, заданием):

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_ADMIN" -H 'Content-Type: application/json' -d '{
  "clusters": [3],
  "release": {"repo_url": "oci://ghcr.io/piqab/charts", "chart": "rgstroperator", "version": "0.5.1",
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
  scan:
    enabled: true            # Trivy; новые образы — при push и попадании в кеш
  resources:
    limits: {memory: 1Gi}    # скану нужно несколько сотен МБ
  proxies:
    - name: dockerhub
      url: https://registry-1.docker.io
      public: true           # pull через кеш без входа
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
- База Trivy лежит на томе данных (при данных в S3 — в `emptyDir`).
- Логин Docker Hub для кеша — `credentialsSecretRef: {name: dockerhub}`
  (ключи `username`, `password`).
- `kubectl get registry` показывает фазу, адрес и версию rgstr; справка по
  полям — `kubectl explain registry.spec`.

### Бэкапы

`spec.backup` — три метода:

| Метод | Что нужно | Где копии |
|---|---|---|
| `S3` | бакет и Secret с ключами | в S3, вне кластера; делает сам rgstr |
| `Longhorn` | Longhorn | backup target Longhorn (`task: backup`) или локальный снимок |
| `VolumeSnapshot` | CSI-драйвер со снимками и external-snapshotter | обычно там же, где том |

**Longhorn в кластере nkt** ставится галочкой при создании кластера или
кнопкой «Дополнения» у готового ([Кластеры](/guide/hub-clusters)). Он же
становится StorageClass по умолчанию, так что том реестра сразу на
Longhorn:

```yaml
spec:
  storage:
    size: 50Gi                     # storageClassName не нужен: longhorn — по умолчанию
  backup:
    enabled: true
    method: Longhorn
    schedule: "0 3 * * *"          # cron, UTC
    keep: 7
    longhorn:
      task: snapshot               # backup — в backup target, его настраивают в Longhorn
```

Через S3 — без требований к кластеру:

```yaml
spec:
  backup:
    enabled: true
    method: S3
    schedule: "0 3 * * *"
    keep: 7
    s3:
      bucket: backups
      prefix: rgstr/prod
      endpoint: https://storage.yandexcloud.net
      region: ru-central1
      secretRef: {name: rgstr-s3}  # AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
```

Состояние — `status.lastBackupTime` и условие `BackupReady` (`Overdue`,
если копия опоздала больше чем на час). Бэкап S3 виден и в плагине
(раздел «Бэкапы»). Восстановление — Job с `rgstr restore` или PVC из
снимка в `storage.existingClaim`
([README оператора](https://github.com/piqab/rgstroperator/blob/main/README.ru.md#восстановление)).

### Кластер (HA)

Несколько подов на PostgreSQL и S3: любой принимает push и pull,
обновление и drain узлов не прерывают работу.

```yaml
spec:
  replicas: 3
  auth: {enabled: true}
  database:
    urlSecretRef: {name: rgstr-db, key: url}   # postgres://…?sslmode=require
  storage:
    s3:
      bucket: registry
      endpoint: https://storage.yandexcloud.net
      region: ru-central1
      secretRef: {name: rgstr-s3}
```

- Оператор не создаёт PVC, обновляет без простоя (`maxUnavailable: 0`),
  заводит `PodDisruptionBudget` и разносит поды по узлам.
- Отказоустойчивость PostgreSQL и S3 — их забота. Встроенные бэкапы
  (`spec.backup`) в этом режиме не применяются: копии — средствами базы и
  бакета.
- Перенос одиночного реестра — `rgstr migrate -from /data` Job-ом.

### Вход через каталог

```yaml
spec:
  auth:
    enabled: true
    ldap:
      url: ldaps://dc1.corp.example:636
      bindDN: CN=svc-rgstr,OU=Service,DC=corp,DC=example
      bindPasswordSecretRef: {name: rgstr-ldap, key: password}
      baseDN: DC=corp,DC=example
      userFilter: (&(objectClass=user)(sAMAccountName=%s))
      adminGroups: [Registry-Admins]
    # oidc:
    #   issuer: https://sso.example.com/realms/main
    #   clientID: rgstr
    #   clientSecretRef: {name: rgstr-oidc, key: client-secret}
```

Плагин показывает в сводке, что включены LDAP и OIDC. Сам он по-прежнему
входит локальным администратором: локальные пользователи главнее
каталога и работают, даже когда каталог недоступен.

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
  scan:
    enabled: true                     # по желанию
  resources:
    limits: {memory: 1Gi}             # скан Trivy; в пределах квоты токена
  proxies:                            # по желанию: свой кеш Docker Hub
    - name: dockerhub
      url: https://registry-1.docker.io
```

Без `securityContext` под не пройдёт `restricted` и останется без реплик,
причина будет в событиях ReplicaSet. С `serviceType: NodePort` или чужим
`ingress.host` хаб откажет ещё до применения: он проверяет `Registry` так
же, как Service и Ingress.

**Что ещё учесть в namespace клиента:**

- **Исходящие** открыты в интернет, но не в частные сети. Кеш Docker Hub,
  публичный S3 и база Trivy доступны; upstream, MinIO или PostgreSQL в
  локальной сети — нет. PostgreSQL в своём namespace доступен.
- **Бэкапы** — `method: S3` (их делает сам rgstr) или `VolumeSnapshot`
  (снимки в namespace клиента). `method: Longhorn` хаб не пустит: оператор
  создал бы `RecurringJob` в `longhorn-system` — вне namespace клиента и в
  backup target администратора.
- **Память.** Без `resources.limits` подставляются значения LimitRange
  токена; для скана их может не хватить, и под перезапустится по OOM.
- **Кластер** (`replicas` больше 1) работает, если у клиента есть
  PostgreSQL и S3.
- `metrics.serviceMonitor` — ресурс администратора: Prometheus кластера
  клиенту не принадлежит.

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
- Не сканирует образы сам: скан делает Trivy внутри rgstr, плагин только
  показывает отчёты и ставит образы в очередь.
- Не держит пароль администратора rgstr нигде, кроме окружения службы
  плагина. Секреты роботов не попадают ни в журналы, ни к агенту. Логин
  upstream кеша плагин не принимает вовсе — он задаётся в rgstr.
- `/metrics` у rgstr — без входа. Плагин находит это, если адрес виден
  снаружи. Закройте его на прокси или оставьте только внутри кластера.
