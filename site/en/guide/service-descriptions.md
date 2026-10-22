---
title: Your own services
---

# Your own services: YAML descriptions

nkt's built-in parsers understand nginx, HAProxy, Caddy, Docker, Podman,
LXD, libvirt and firewalls. Any other program — Traefik, RabbitMQ, Tomcat,
your own service — can be **described**: where its configuration is, how
to read it and what to take from it. A described service shows up where
the built-in ones do:

- **resource map** — the addresses it accepts connections on and where it
  forwards;
- **findings** — your rules (“the dashboard is open without a password”)
  plus nkt's general checks: a declared port nobody listens on, a port
  open to the outside without a firewall rule;
- **“Configs”** — its files, with edit history and diffs;
- **sources** in the host overview — what was read, the version, warnings.

Descriptions change nothing on the host: they only read.

## Built-in descriptions

Ready descriptions are already inside nkt, nothing to install. Each can be
overridden with your own file with the same `name` or turned off
(`disabled: true`).

| Service | What it reads | Findings |
|---|---|---|
| **Traefik** | `/etc/traefik/traefik.yml` or `.toml` — entryPoints (tcp/udp, TLS); `/etc/traefik/dynamic/*.yml` and, if the API is open on 127.0.0.1:8080, `/api/rawdata` — services of every provider (Docker too) with backend addresses; version — `/api/version` | dashboard and API without protection (`api.insecure`); backend certificates not verified (`insecureSkipVerify`) |
| **RabbitMQ** | `rabbitmq-diagnostics listeners` — every listener (AMQP, TLS, management, MQTT, STOMP); version — `server_version`; `rabbitmq.conf` | guest can log in over the network; management over HTTP on all addresses |
| **Redis** | `redis.conf` (Debian and RHEL) — bind, port, tls-port | all addresses without a password; protected-mode off without a password |
| **PostgreSQL** | `postgresql.conf` and `pg_hba.conf` of every cluster (`/etc/postgresql/*/*`, `/var/lib/pgsql`) | network login without a password (trust) — critical; login from every address; cleartext password; all addresses without SSL |
| **Mosquitto** | `mosquitto.conf` and `conf.d` — listeners with their settings (TLS, websockets) | login without credentials (`allow_anonymous true`) — global and per listener |
| **Envoy** | `/etc/envoy/envoy.yaml` — listeners, admin, clusters with addresses | admin listens beyond localhost |

A found service with a loaded systemd unit (from the description's `unit`
or from the `detect` checks: `redis-server` on Debian, `redis` on RHEL)
appears in the **“Services”** section — state, autostart and buttons to
start, stop, restart and reload the configuration.

## Where descriptions live

| From | Where | Precedence |
|---|---|---|
| host | `/etc/netknownsthat/services.d/*.yaml` | highest |
| hub profile | nkt data directory, `services.d/profile/` (delivery from the hub comes with the “Services” section in profiles) | middle |
| built-in | inside nkt | lowest |

A description with the same `name` **overrides** the lower one entirely;
`disabled: true` turns the name off (for example, a built-in description
you don't need):

```yaml
name: rabbitmq
disabled: true
```

A broken description (a YAML error, a wrong path) doesn't break the scan:
the **last good version** of that file is used, and the host sources show
a warning with the error.

## Example

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
  - id: api
    http: http://127.0.0.1:8080/api/rawdata
    interval: 60s
    timeout: 5s
    optional: true
version:
  from: api
  value: "$.version"
extract:
  endpoints:
    - from: static
      each: $.entryPoints.*
      fields:
        address: "@.address | host"
        port: "@.address | port"
        tls: "@.http.tls | exists"
  upstreams:
    - from: dynamic
      each: $.http.services.*
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
    suggestion: "api.insecure: false and access through an entryPoint with auth"
```

## Detection: `detect`

The service “is on the host” if **any** check from `any` and **all** from
`all` match. Each check has one field:

| Check | Matches when |
|---|---|
| `file: /path` | the file exists (glob allowed) |
| `unit: name` | the systemd unit is loaded |
| `binary: name` | the program is in PATH |
| `image: text` | there is a Docker or Podman container from such an image |
| `process: name` | a process with that name listens on a port |
| `port: 15672` | someone listens on this TCP port |

Without `detect` the service is looked up by its file sources (and
without them — by `unit`). Until the service is found, its sources are
not read.

## Sources: `sources`

A source has an `id` and exactly one of:

- **`file`** — a path or glob (`/etc/app/conf.d/*.yml`): each file is its
  own document. A file is re-read only when its modification time or size
  changes. Files go to “Configs” (`editable: false` — view only).
- **`http`** — the program's API address; `interval` — how long the
  answer lives (every scan by default), `timeout` — up to a minute. No
  login and password in the address: `basic_auth: {user: …,
  password_file: /path}` or `bearer_file` — the secret is read from a file
  on the host and is not stored in the description (or on the hub).
- **`command`** — a **read-only** command as an argument list:
  `[rabbitmqctl, list_queues, --formatter, json]`. No shell: `sh`, `bash`,
  `sudo`, `env`, interpreters and anything that runs other commands are
  forbidden. It runs as the same user as the nkt service, with a timeout
  and an output limit.

`optional: true` — a missing file or answer is not a warning.

Closed paths are never read: `/etc/shadow`, sudoers, private keys (`.ssh`,
`ssh_host_*`, `*.key`, `privkey`), `/root`, `/proc`, nkt's settings and
data.

### Formats

A file's `format` defaults by extension (`.yaml`, `.json`, `.toml`,
`.ini`/`.cnf`, `.env`, `.properties`, `.xml`, `.conf` → `lines`); for HTTP
and commands — `json`.

| Format | For | The tree |
|---|---|---|
| `yaml`, `json`, `toml` | Traefik, Envoy, Prometheus, API answers | as in the file |
| `ini` | my.cnf, PHP, Gitea, supervisord | `[section]` → object; a key without a value is `true` |
| `env` | `.env`, `/etc/default/*` | key → value |
| `properties` | Kafka, ZooKeeper, Spring | flat keys: `$['server.port']` |
| `lines` | redis.conf, postgresql.conf, mosquitto.conf, sshd_config | “key value”; a repeated key is a list |
| `xml` | Tomcat, Jenkins, ActiveMQ, Hadoop | attribute — `@name`, text — `#text`, repeated tag — a list |
| `regex` | everything else | a list of matches with named groups |
| `text` | a rule over the whole text | a string |

Options (`options`): `pattern` — for `regex`; `blocks: [listener]` — for
`lines`: such a key opens a block (mosquitto: settings after
`listener 8883` belong to it); `lower_keys: true` — lower-case keys
(sshd_config).

## Paths

Paths are **JSONPath per RFC 9535**: `$` — the document, `@` — the current
item.

| Path | What |
|---|---|
| `$.entryPoints.*` | all members of an object |
| `$.servers[0]`, `$.servers[-1]`, `$.servers[0:2]` | index, from the end, slice |
| `$..port` | `port` at any depth |
| `$.users[?@.admin == true]` | a filter |
| `$[?@.port > 1024 && !@.internal]` | conditions, `&&`, `\|\|`, `!` |
| `$[?match(@.name, 'web-.*')]` | functions `match`, `search`, `length`, `count`, `value` |

A deviation from the RFC for configs, where everything is a string: a
number compares with a numeric string as a number (`"6379" == 6379`), a
boolean — with the words `true/false/yes/no/on/off/1/0`.

`each` is the path to the items (the whole document by default). If the
path gave one list, its elements are iterated. `when` is a condition on
the item, the same expression as in a `[?…]` filter.

## Fields and transforms

A field value is a path (`@.address`), the special `@key` (the item's key:
name or index), `@file` (the document's file), `@source`, or plain text
(`tcp`). After `|` come transforms:

| | |
|---|---|
| `port`, `host`, `scheme` | from `0.0.0.0:8080`, `:8080`, `[::]:443`, a URL (port by scheme) |
| `lower`, `upper`, `trim`, `prefix(x)`, `suffix(x)`, `replace(a,b)` | strings |
| `split(,)`, `join(,)`, `first`, `last`, `count`, `keys` | lists |
| `default(x)`, `exists`, `bool`, `not`, `int` | values |
| `contains(x)`, `matches(re)` | checks |

**Addresses** (`extract.endpoints`): `port` — required, `address`,
`protocol` (`tcp` by default), `tls`, `mode`, `names`, `label`, `kind`,
`upstreams`. **Pools** (`extract.upstreams`): `name`, `mode`, `algorithm`,
`health` and `servers` — `each` and the fields `url` or `host`, `port`,
`weight`, `backup`, `down`. **Version** (`version`) — a value from a
source.

## Rules

```yaml
rules:
  - id: no-password
    from: config
    when: "!@.requirepass"
    severity: high          # critical | high | medium | low | info
    title: {ru: "Redis без пароля", en: "Redis without a password"}
    detail: "File {{@file}}"
    suggestion: "requirepass in redis.conf"
    object: "@.port"        # what the finding is about; @key or the file by default
```

A finding per item (`each`) for which `when` is true. Texts take
`{{expression}}`. Texts come in two languages, the finding is shown in the
reader's language. The finding rule is `svc.<name>.<id>`.

## One file, two descriptions

If several descriptions (or a built-in parser) take a file, in “Configs”
it belongs to one: the built-in parser, then the description with the
higher `priority` (50 by default), then the higher place. The others get
the finding “File is described by several services”.

## Debugging

```sh
sudo nkt services                      # all descriptions: from where, found or not, what they gave
sudo nkt services test traefik.yaml    # the parse log of one description
```

`nkt services test` shows every step: which `detect` checks matched,
which files were read (or taken from the cache), how many nodes each path
gave and how many were left after the condition, which addresses, pools
and findings came out and why an item was skipped.
