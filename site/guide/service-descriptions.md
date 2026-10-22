---
title: Свои сервисы
---

# Свои сервисы: описания на YAML

Встроенные разборщики nkt понимают nginx, HAProxy, Caddy, Docker, Podman,
LXD, libvirt, Kubernetes и файрволы, а готовые описания — Traefik,
RabbitMQ, Redis, PostgreSQL, Mosquitto и Envoy. Любую другую программу —
Tomcat, Kafka, свой сервис — можно **описать**: где у неё конфигурация,
как её прочитать и что из неё взять. Описанный сервис появляется там же,
где встроенные:

- **карта ресурсов** — адреса, на которых он принимает соединения, и
  куда пересылает;
- **находки** — ваши правила («панель открыта без пароля») и общие
  проверки nkt: объявленный порт никто не слушает, порт открыт наружу без
  правила файрвола;
- **«Конфигурации»** — его файлы, с историей правок и диффом;
- **«Сервисы»** — его юнит systemd с кнопками управления;
- **источники** в обзоре хоста — что прочитано, версия, предупреждения.

Описания ничего не меняют на хосте: только читают.

## Свой сервис за 5 минут

Допустим, на хосте работает программа `myapp`, и её адреса записаны в
`/etc/myapp/config.yml`:

```yaml
listeners:
  - name: public
    address: 0.0.0.0:8080
  - name: admin
    address: 127.0.0.1:9090
    auth: false
```

**1. Опишите её** — файл `/etc/netknownsthat/services.d/myapp.yaml`:

```yaml
name: myapp
title: My app
unit: myapp                       # юнит systemd — в раздел «Сервисы»
sources:
  - id: config
    file: /etc/myapp/config.yml   # формат — по расширению (yaml)
extract:
  endpoints:
    - from: config
      each: "$.listeners[*]"      # каждый элемент списка — адрес
      fields:
        address: "@.address | host"
        port: "@.address | port"
        label: "@.name | prefix('myapp ')"
rules:
  - id: admin-no-auth
    from: config
    each: "$.listeners[*]"
    when: "@.name == 'admin' && @.auth == false"
    severity: medium
    title:
      ru: "Админка myapp без авторизации ({{@.address}})"
      en: "myapp admin without auth ({{@.address}})"
    object: "@.address"
```

**2. Проверьте**: `sudo nkt services test /etc/netknownsthat/services.d/myapp.yaml`
покажет каждый шаг — что найдено, какой путь сколько элементов дал, какие
адреса и находки получились.

**3. Пересканируйте хост** (кнопка «Пересканировать» или подождите
очередного скана, по умолчанию раз в 5 минут): адреса `myapp public` и
`myapp admin` — на карте, находка — в «Проблемах», файл
`/etc/myapp/config.yml` — в «Конфигурациях».

То же можно сделать с хаба: «Профили» → «Сервисы» → «Новое описание»,
путь подобрать в «Конструкторе путей» на образце с хоста, проверить
«Журналом разбора» и «Доставить на хосты» (см. [ниже](#на-хабе)).

::: tip Кавычки в YAML
Выражение, которое начинается с `@` или содержит `: `, берите в кавычки:
`"@.address | port"`. Без кавычек YAML не разберёт строку, и nkt скажет об
ошибке при проверке.
:::

## Где лежат описания

| Откуда | Где | Старшинство |
|---|---|---|
| хост | `/etc/netknownsthat/services.d/*.yaml` | старшее |
| профиль хаба | каталог данных nkt, `services.d/profile/` — пишет хаб («Доставить на хосты») | среднее |
| встроенные | внутри nkt | младшее |

Описание с тем же `name` **перекрывает** младшее целиком; `disabled: true`
выключает имя:

```yaml
name: rabbitmq
disabled: true
```

Сломанное описание (ошибка YAML, неверный путь) не роняет скан: если это
описание хоста или профиля и раньше оно было исправным, работает его
**последняя исправная версия**, а в источниках хоста — предупреждение с
ошибкой. Новое описание с ошибкой просто не работает (и видно в
`nkt services` как ошибка).

Изменения подхватываются при следующем скане хоста.

## Обнаружение: `detect`

Сервис «есть на хосте», если срабатывает **любая** проверка из `any` и
**все** из `all`. В каждой проверке — одно поле:

| Проверка | Срабатывает, если |
|---|---|
| `file: /путь` | файл есть (можно glob) |
| `unit: имя` | юнит systemd загружен |
| `binary: имя` | программа есть в PATH |
| `image: строка` | есть контейнер Docker или Podman из образа, имя которого содержит строку |
| `process: имя` | процесс с таким именем слушает порт |
| `port: 15672` | кто-то слушает этот TCP-порт |

Без `detect` сервис ищется по файлам-источникам, а без них — по `unit`;
описанию только с HTTP и командами `detect` обязателен. Пока сервис не
найден, его источники не читаются.

`unit` описания — юнит для раздела «Сервисы». Показывается первый
загруженный из `unit` и проверок `unit` в `detect` — так одно описание
работает с `redis-server` в Debian и `redis` в RHEL.

## Источники: `sources`

У источника `id` и ровно одно из:

- **`file`** — путь или glob (`/etc/app/conf.d/*.yml`): каждый файл — свой
  документ. Файл перечитывается, только когда изменились время правки или
  размер; больше 8 МБ — не читается. Файлы попадают в «Конфигурации»
  (`editable: false` — только просмотр).
- **`http`** — адрес API программы **на этом хосте**: `localhost`,
  127.0.0.0/8 или `::1` (API в контейнере — через опубликованный на
  127.0.0.1 порт); адрес проверяется и при соединении, перенаправления не
  выполняются. `interval` — сколько живёт ответ (по
  умолчанию — каждый скан), `timeout` — по умолчанию 5 с, не больше
  минуты. Логин и пароль в адресе и заголовки `Authorization`/`Cookie`
  нельзя: `basic_auth: {user: …, password_file: /путь}` или
  `bearer_file: /путь` — секрет читается из файла на хосте и в описании
  (а значит, и на хабе) не хранится; файл секрета не может быть закрытым
  путём.
- **`command`** — команда списком аргументов:
  `[consul, members, -format=json]`. Без оболочки: `sh`, `bash`, `sudo`,
  `env`, интерпретаторы и всё, что запускает другие команды, запрещены.
  Команда идёт **от пользователя nobody** (`setpriv`, без групп и новых
  привилегий): даже ошибочная команда ничего не изменит на хосте. Если
  программе нужен root (или она сама переключается на своего
  пользователя, как `rabbitmq-diagnostics`), — `as_root: true`: хаб
  помечает такие описания («команды от root») и пишет их в журнал
  действий. `timeout` и предел вывода 8 МБ; `interval` — как у HTTP.

`optional: true` — нет файла или ответа — не предупреждение.

Закрытые пути описания не читают вовсе: `/etc/shadow`, sudoers, закрытые
ключи (`.ssh`, `ssh_host_*`, `*.key`, `privkey`), `/root`, `/proc`,
настройки и данные nkt. Проверяется настоящий путь: символьная ссылка на
закрытый файл не читается так же, как сам файл.

### Форматы

`format` у файла по умолчанию — по расширению (`.yaml`/`.yml`, `.json`,
`.toml`, `.ini`/`.cnf`, `.env`, `.properties`, `.xml`, `.conf` → `lines`);
у HTTP и команды — `json`. Файл без расширения — `format` обязателен.

| Формат | Для чего | Как выглядит дерево |
|---|---|---|
| `yaml`, `json`, `toml` | Traefik, Envoy, Prometheus, ответы API | как в файле (порядок ключей сохраняется) |
| `ini` | my.cnf, PHP, Gitea, supervisord | `[секция]` → объект; ключ без значения — `true` |
| `env` | `.env`, `/etc/default/*`, `/etc/os-release` | ключ → значение |
| `properties` | Kafka, ZooKeeper, Spring | плоские ключи: `$['server.port']` |
| `lines` | redis.conf, postgresql.conf, mosquitto.conf, sshd_config | «ключ значение» или «ключ = значение»; повтор ключа — список |
| `xml` | Tomcat, Jenkins, ActiveMQ, Hadoop | атрибут — `@имя`, текст — `#text`, повтор тега — список |
| `regex` | всё остальное | список совпадений: именованные группы и `#match` |
| `text` | правило по тексту целиком | строка |

Настройки (`options`): `pattern` — для `regex`; `blocks: [listener]` — для
`lines`: такой ключ открывает блок, значение — в `@value`, а настройки
после него относятся к блоку (mosquitto: `listener 8883` и его
`certfile`); `lower_keys: true` — ключи в нижнем регистре (sshd_config).

## Пути

Пути — **JSONPath по RFC 9535**: `$` — документ, `@` — текущий элемент.

| Путь | Что |
|---|---|
| `$.entryPoints.*` | все члены объекта |
| `$.servers[0]`, `$.servers[-1]`, `$.servers[0:2]` | номер, с конца, срез |
| `$..port` | `port` на любой глубине |
| `$['server.port']` | имя с точкой или пробелом |
| `$.users[?@.admin == true]` | фильтр |
| `$[?@.port > 1024 && !@.internal]` | условия, `&&`, `\|\|`, `!`; `?@.x` — «поле есть» |
| `$[?match(@.name, 'web-.*')]` | функции `match` (целиком), `search` (часть), `length`, `count`, `value` |

Отступление от RFC ради конфигов, где всё — строки: число сравнивается со
строкой-числом как число (`"6379" == 6379`), логическое — со словами
`true/false/yes/no/on/off/1/0`.

`each` — путь к элементам (по умолчанию — весь документ). Если путь дал
один список, перебираются его элементы. `each` может кончаться
преобразованиями: `"$.listeners | split(,)"` перебирает части строки
(пример Kafka ниже). `when` — условие на элемент, то же выражение, что в
фильтре `[?…]`, без `?`.

## Поля и преобразования

Значение поля — путь (`@.address` — от элемента, `$.global.port` — от
документа), особые `@key` (ключ элемента: имя или номер), `@file` (файл
документа), `@source`, или просто текст (`tcp`, `'текст | с чертой'`).
После `|` — преобразования, слева направо:

| | |
|---|---|
| `port`, `host`, `scheme` | из `0.0.0.0:8080`, `:8080`, `[::]:443`, `:53/udp`, URL (порт по схеме) |
| `lower`, `upper`, `trim`, `prefix(x)`, `suffix(x)`, `replace(a,b)` | строки |
| `split(,)`, `join(,)`, `first`, `last`, `nth(i)`, `count`, `keys` | списки |
| `default(x)`, `exists`, `bool`, `not`, `int` | значения |
| `contains(x)`, `matches(re)` | проверки |

**Адреса** (`extract.endpoints`): `port` — обязательно (элемент без порта
пропускается), `address` (пусто — все адреса), `protocol` (`tcp` по
умолчанию), `tls`, `mode`, `names`, `label`, `kind`, `upstreams`.

**Пулы** (`extract.upstreams`): `name` — обязательно, `mode`,
`algorithm`, `health` и `servers` — свой `each` (от элемента пула) и поля
`url` или `host`, `port`, `weight`, `backup`, `down`.

**Версия** — `version: {from: источник, value: выражение}`.

## Правила

```yaml
rules:
  - id: no-password
    from: config
    each: "$"                # по умолчанию — документ целиком
    when: "!@.requirepass"
    severity: high           # critical | high | medium | low | info
    title: {ru: "Redis без пароля", en: "Redis without a password"}
    detail: "Файл {{@file}}" # строка или {ru, en}
    suggestion: "requirepass в redis.conf"
    object: "@.port"         # о чём находка; по умолчанию @key или файл
```

Находка — на каждый элемент `each`, для которого `when` истинно. В
текстах — `{{выражение}}`. Тексты на двух языках, находка показывается на
языке читателя. Правило находки — `svc.<имя>.<id>`.

## Полный пример

```yaml
name: traefik
title: Traefik
unit: traefik
priority: 50
detect:
  any:
    - file: /etc/traefik/traefik.yml
    - image: traefik
sources:
  - id: static
    file: /etc/traefik/traefik.yml
  - id: dynamic
    file: /etc/traefik/dynamic/*.yml
    optional: true
  - id: version
    http: http://127.0.0.1:8080/api/version
    interval: 1h
    timeout: 3s
    optional: true
version:
  from: version
  value: "$.Version"
extract:
  endpoints:
    - from: static
      each: "$.entryPoints.*"
      fields:
        address: "@.address | host"
        port: "@.address | port"
        tls: "@.http.tls | exists"
  upstreams:
    - from: dynamic
      each: "$.http.services.*"
      fields:
        name: "@key"
      servers:
        each: "@.loadBalancer.servers[*]"
        fields:
          url: "@.url"
rules:
  - id: insecure-api
    from: static
    when: "@.api.insecure == true"
    severity: high
    title:
      ru: "Панель Traefik открыта без защиты"
      en: "Traefik dashboard is open without protection"
    suggestion: "api.insecure: false и доступ через entryPoint с авторизацией"
```

## Ещё примеры

Каждый пример ниже проверен тестом nkt на таких же файлах: описание даёт
ровно то, что написано под ним.

### Tomcat — XML

`/etc/tomcat10/server.xml`:

```xml
<Server port="8005" shutdown="SHUTDOWN">
  <Service name="Catalina">
    <Connector port="8080" protocol="HTTP/1.1" connectionTimeout="20000" redirectPort="8443"/>
    <Connector port="8443" protocol="org.apache.coyote.http11.Http11NioProtocol" SSLEnabled="true"/>
    <Connector port="8009" protocol="AJP/1.3" address="0.0.0.0" secretRequired="false"/>
    <Engine name="Catalina" defaultHost="localhost"/>
  </Service>
</Server>
```

```yaml
name: tomcat
title: Tomcat
unit: tomcat10
sources:
  - id: server
    file: /etc/tomcat*/server.xml
extract:
  endpoints:
    - from: server
      each: "$.Server.Service.Connector"
      fields:
        address: "@['@address']"
        port: "@['@port']"
        tls: "@['@SSLEnabled'] | bool"
        label: "@['@protocol'] | prefix('Tomcat ')"
rules:
  - id: ajp-no-secret
    from: server
    each: "$.Server.Service.Connector"
    when: "search(@['@protocol'], 'AJP') && @['@secretRequired'] == false"
    severity: high
    title:
      ru: "Tomcat: AJP без секрета на порту {{@['@port']}}"
      en: "Tomcat: AJP without a secret on port {{@['@port']}}"
    suggestion:
      ru: "secretRequired=\"true\" и secret, или уберите коннектор AJP."
      en: "secretRequired=\"true\" and a secret, or remove the AJP connector."
    object: "@['@port']"
```

Атрибут — ключ `@имя`, поэтому путь к нему — `@['@port']`; три
`<Connector>` — список, `each` перебирает его. Получится: адреса 8080,
8443 (TLS) и 0.0.0.0:8009, находка «AJP без секрета на порту 8009»
(Ghostcat).

### Kafka — properties и несколько адресов в одной строке

`/opt/kafka/config/server.properties`:

```properties
broker.id=0
listeners=PLAINTEXT://:9092,SSL://:9093
advertised.listeners=PLAINTEXT://kafka1.example.com:9092
log.dirs=/var/lib/kafka
```

```yaml
name: kafka
title: Kafka
unit: kafka
sources:
  - id: server
    file: /opt/kafka/config/server.properties
extract:
  endpoints:
    - from: server
      each: "$.listeners | split(,)"
      fields:
        address: "@ | host"
        port: "@ | port"
        tls: "@ | scheme | contains(ssl)"
        label: "@ | scheme | upper | prefix('Kafka ')"
rules:
  - id: plaintext-all
    from: server
    each: "$.listeners | split(,)"
    when: "match(@, 'PLAINTEXT://(0\\.0\\.0\\.0)?:[0-9]+')"
    severity: medium
    title:
      ru: "Kafka: PLAINTEXT на всех адресах ({{@}})"
      en: "Kafka: PLAINTEXT on all addresses ({{@}})"
    object: "@"
```

`each` с преобразованием: `$.listeners | split(,)` перебирает части
строки, `@` — сама часть (`PLAINTEXT://:9092`). Получится: адреса 9092
и 9093 (TLS), находка «PLAINTEXT на всех адресах».

### MySQL — ini

`/etc/mysql/mysql.conf.d/mysqld.cnf`:

```ini
[mysqld]
user = mysql
port = 3306
bind-address = 0.0.0.0
skip-name-resolve
```

```yaml
name: mysql
title: MySQL
unit: mysql
sources:
  - id: cnf
    file: /etc/mysql/*.conf.d/*.cnf
extract:
  endpoints:
    - from: cnf
      each: "$.mysqld"
      fields:
        address: "@['bind-address'] | default(127.0.0.1)"
        port: "@.port | default(3306)"
rules:
  - id: listen-all
    from: cnf
    each: "$.mysqld"
    when: "@['bind-address'] == '0.0.0.0' || @['bind-address'] == '*'"
    severity: medium
    title:
      ru: "MySQL слушает все адреса"
      en: "MySQL listens on all addresses"
    suggestion:
      ru: "bind-address = 127.0.0.1 или адрес внутренней сети."
      en: "bind-address = 127.0.0.1 or an internal network address."
  - id: skip-grant-tables
    from: cnf
    each: "$.mysqld"
    when: "@['skip-grant-tables']"
    severity: critical
    title:
      ru: "MySQL без проверки паролей (skip-grant-tables)"
      en: "MySQL without password checks (skip-grant-tables)"
```

Секция `[mysqld]` — объект; ключ без значения (`skip-name-resolve`,
`skip-grant-tables`) — `true`, поэтому `when: "@['skip-grant-tables']"`
— «есть такой ключ». Каждый файл с `[mysqld]` даёт свой адрес.
Получится: адрес 0.0.0.0:3306, находка «MySQL слушает все адреса».

### RabbitMQ — HTTP API с паролем из файла

```yaml
name: rabbitmq-api
title: RabbitMQ API
detect:
  any:
    - port: 15672
sources:
  - id: overview
    http: http://127.0.0.1:15672/api/overview
    basic_auth:
      user: nkt-monitor
      password_file: /etc/rabbitmq/nkt-monitor.pass
    interval: 1m
    timeout: 5s
version:
  from: overview
  value: "$.rabbitmq_version"
rules:
  - id: many-queues
    from: overview
    when: "@.object_totals.queues > 1000"
    severity: low
    title:
      ru: "RabbitMQ: очередей {{@.object_totals.queues}}"
      en: "RabbitMQ: {{@.object_totals.queues}} queues"
```

Пароль пользователя `nkt-monitor` лежит в
`/etc/rabbitmq/nkt-monitor.pass` на хосте и в описании не появляется.
Ответ `/api/overview` живёт минуту (`interval`), версия берётся из него.
Получится: версия 3.13.7 в источниках, находка «очередей 1520».

### Consul — команда

```yaml
name: consul
title: Consul
unit: consul
detect:
  any:
    - binary: consul
    - unit: consul
sources:
  - id: members
    command: [consul, members, -format=json]
    interval: 2m
    timeout: 10s
  - id: version
    command: [consul, version, -format=json]
    interval: 1h
version:
  from: version
  value: "$.Version"
rules:
  - id: member-down
    from: members
    each: "$[?@.Status != 'alive']"
    when: "@.Status != 'alive'"
    severity: medium
    title:
      ru: "Consul: узел {{@.Name}} — {{@.Status}}"
      en: "Consul: node {{@.Name}} is {{@.Status}}"
    object: "@.Name"
```

Команда — списком аргументов, без оболочки; её вывод — JSON. Идёт от
nobody: `consul members` спрашивает локального агента по сети, root ей не
нужен, поэтому `as_root` не задан. Получится:
версия 1.18.1, находка «узел node-2 — failed» на каждый узел не в
состоянии `alive`.

### memcached — regex для конфига без формата

`/etc/memcached.conf` — флаги командной строки:

```
# memcached default config
-d
logfile /var/log/memcached.log
-m 64
-p 11211
-u memcache
```

```yaml
name: memcached
title: memcached
unit: memcached
sources:
  - id: conf
    file: /etc/memcached.conf
    format: regex
    options:
      pattern: '^-(?P<flag>[a-zA-Z])\s+(?P<value>\S+)'
extract:
  endpoints:
    - from: conf
      each: "$[0]"
      fields:
        address: "$[?@.flag == 'l'].value | split(,) | first"
        port: "$[?@.flag == 'p'].value | default(11211)"
        label: memcached
rules:
  - id: listen-all
    from: conf
    each: "$[0]"
    when: "!$[?@.flag == 'l']"
    severity: high
    title:
      ru: "memcached слушает все адреса (нет -l)"
      en: "memcached listens on all addresses (no -l)"
    suggestion:
      ru: "-l 127.0.0.1 в /etc/memcached.conf; снаружи memcached используют для DDoS-усиления."
      en: "-l 127.0.0.1 in /etc/memcached.conf; exposed memcached is used for DDoS amplification."
    object: "$[?@.flag == 'p'].value | default(11211)"
```

`regex` превращает каждую строку `-флаг значение` в объект `{flag,
value}`; поля берут значения от корня (`$[?@.flag == 'p']`). Нет `-l` —
memcached слушает все адреса. Получится: адрес 11211, находка «слушает
все адреса (нет -l)».

## Один файл — два описания

Если файл берут несколько описаний (или встроенный разборщик), в
«Конфигурациях» он принадлежит одному — с большим `priority` (у описаний
по умолчанию 50, у встроенных разборщиков 100); при равенстве —
встроенному разборщику, затем старшему месту. Остальным — находка «Файл
описан несколькими сервисами».

## Встроенные описания

Готовые описания — уже внутри nkt, ничего ставить не нужно. Каждое можно
перекрыть своим с тем же `name` или выключить (`disabled: true`).

| Сервис | Что читает | Находки |
|---|---|---|
| **Traefik** | `/etc/traefik/traefik.yml` или `.toml` — entryPoints (tcp/udp, TLS); `/etc/traefik/dynamic/*.yml` и, если API открыт на 127.0.0.1:8080, `/api/rawdata` — сервисы всех провайдеров (Docker тоже) с адресами бэкендов; версия — `/api/version` | панель и API без защиты (`api.insecure`); не проверяются сертификаты бэкендов (`insecureSkipVerify`) |
| **RabbitMQ** | `rabbitmq-diagnostics listeners` (`as_root`) — все слушатели (AMQP, TLS, панель, MQTT, STOMP); версия — `server_version`; `rabbitmq.conf` | guest может входить по сети; панель по HTTP на всех адресах |
| **Redis** | `redis.conf` (Debian и RHEL) — bind, port, tls-port | все адреса без пароля; protected-mode выключен без пароля |
| **PostgreSQL** | `postgresql.conf` и `pg_hba.conf` всех кластеров (`/etc/postgresql/*/*`, `/var/lib/pgsql`) | вход по сети без пароля (trust) — критично; вход со всех адресов; пароль открытым текстом; все адреса без SSL |
| **Mosquitto** | `mosquitto.conf` и `conf.d` — слушатели с их настройками (TLS, websockets) | вход без логина (`allow_anonymous true`) — общий и у слушателя |
| **Envoy** | `/etc/envoy/envoy.yaml` — слушатели, admin, кластеры с адресами | admin слушает не только localhost |

Сервисы в контейнерах встроенные описания не разбирают (конфигурация —
внутри контейнера); исключение — Traefik, у которого читается API.

## Встроенные разборщики

nginx, HAProxy, Caddy, Docker, Podman, LXD, libvirt, Kubernetes, файрвол
(iptables, ufw, firewalld) и fail2ban разбирает код nkt, но и у них есть
описания — `engine:` с тем же именем. Через них разборщик можно
**выключить**, **поменять его пути** и **приоритет его файлов**, не трогая
переменные окружения службы:

```yaml
# /etc/netknownsthat/services.d/nginx.yaml — nginx собран в /opt
name: nginx
engine: nginx
options:
  main_config: /opt/nginx/conf/nginx.conf
```

```yaml
# Docker здесь не нужен — не опрашивать
name: docker
disabled: true
```

| Разборщик | Настройки (`options`) | По умолчанию |
|---|---|---|
| `nginx`, `haproxy`, `caddy` | `main_config` — главный файл | `NKT_NGINX_MAIN_CONFIG` и т. п. |
| `docker` | `compose_files` — файлы compose через запятую | `NKT_COMPOSE_FILES` |
| `libvirt` | `uri` | `NKT_LIBVIRT_URI` |
| `fail2ban` | `root` — каталог | `NKT_FAIL2BAN_ROOT` |
| `podman`, `lxd`, `kubernetes`, `firewall` | — | |

Не заданная настройка берётся из окружения, как раньше. В таком описании
только `priority`, `disabled`, `options` и `detect` — и тот только из
`file`, `unit` и `binary`: не сработал — разборщик не запускается.
Выключенный разборщик виден в источниках хоста с указанием, каким
описанием он выключен.

## На хабе

![Описания сервисов на хабе](/screens/ru/hub-svcdesc.png)

На хабе описания хранятся библиотекой с историей правок — вкладка
**«Профили» → «Сервисы»**:

- **Описания хаба** — свои: «Новое описание» (шаблон), «Импорт .yaml»,
  правка в окне с диффом перед записью, «История» (любую прошлую
  редакцию можно вернуть в редактор), «Скачать», удаление. Имя берётся из
  `name` в самом описании; при сохранении оно проверяется тем же
  разбором, что на хосте, — сломанное не сохраняется.
- **Встроенные** — для просмотра; «Перекрыть» создаёт описание хаба с тем
  же текстом: правьте и доставляйте. Перекрытое встроенное помечено.

В окне правки:

- **«Проверить»** — разбор описания без сохранения: ошибка с указанием
  поля или сводка «источников, адресов, пулов, правил».
- **«Журнал разбора»** — черновик разбирается на выбранном хосте, как
  `nkt services test`: какие проверки `detect` сработали, что прочитано,
  сколько узлов дал каждый путь, что получилось — адреса, пулы, находки,
  версия. Ничего не сохраняется ни на хабе, ни на хосте.
- **«Конструктор путей»** — образец источника (файл, HTTP или команда) с
  выбранного хоста показывается деревом; щелчок по узлу даёт путь,
  «each: все такие» обобщает его до всех соседей, «each: этот узел» берёт
  его как есть, «в поле (от each)» — путь от элемента; `each`, `when` и
  поле считаются над образцом сразу. Готовые пути копируются в описание.
  Для образца действуют те же запреты, что для описаний.

![Конструктор путей](/screens/ru/hub-svcdesc-builder.png)

**«Доставить на хосты»** — задание хаба: на каждый выбранный хост
уходят все описания хаба целиком, в уровень «профиль», лишние оттуда
удаляются; хост сразу пересканирует себя. Описание на самом хосте
(`/etc/netknownsthat/services.d`) по-прежнему старше. Описания хаба
входят в экспорт и импорт хаба (с историей).

На хосте описания из `/etc/netknownsthat/services.d` видны в
**«Конфигурациях»** категорией «описания сервисов»: правка с историей и
откатом, при записи описание проверяется разбором — сломанное не
записывается.

## Отладка

```sh
sudo nkt services                      # все описания: откуда, состояние, что дали
sudo nkt services test traefik.yaml    # журнал разбора одного описания
```

`nkt services` перечисляет все описания — встроенные, профиля и хоста — с
состоянием: найден, не найден, ошибка, прошлая исправная версия,
перекрыто, выключено, встроенный разборщик (с его настройками) — и
сколько адресов, пулов и находок каждое дало.

`nkt services test` показывает каждый шаг: какие проверки `detect`
сработали, какие файлы прочитаны (или взяты из кэша), сколько узлов дал
каждый путь и сколько осталось после условия, какие адреса, пулы и
находки получились, какие элементы пропущены без порта.
