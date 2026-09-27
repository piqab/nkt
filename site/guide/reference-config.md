---
title: Настройка
---

# Настройка: все переменные

Каждая настройка — переменная окружения. У службы хоста они лежат в
`/etc/netknownsthat/nkt.env`, у хаба — в `/etc/netknownsthat/hub.env`, в
Docker — в `environment` compose-файла или `.env` рядом с ним. Заготовки с
пояснениями — [`deploy/nkt.env.example`](https://github.com/piqab/nkt/blob/main/deploy/nkt.env.example)
и [`deploy/hub.env.example`](https://github.com/piqab/nkt/blob/main/deploy/hub.env.example).

- **Длительности** — `30s`, `5m`, `6h`, `720h`; голое число — секунды.
- **Списки** — через запятую.
- **Логические** — `true` / `false`.
- Пустое значение равно отсутствию переменной — берётся умолчание.

::: warning Хост под хабом
`nkt.env` управляемого хоста хаб перезаписывает при каждой установке и
обновлении. Настройки такого хоста (порт API, терминал, резервный канал)
меняются в форме хоста на хабе, а не правкой файла.
:::

## Режим и данные

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_MODE` | `local` на Linux, иначе `fixtures` | `local` — читать настоящий хост, `fixtures` — снапшот, `hub` — режим хаба |
| `NKT_DATA_DIR` | `/var/lib/netknownsthat`; у хаба `/var/lib/netknownsthat-hub`; в `fixtures` — `./data` | База, история конфигов, TLS, кэши |
| `NKT_FIXTURES_ROOT` | `./fixtures/host` | Каталог снапшота для `fixtures` |
| `NKT_DEMO_BACKFILL` | `true` | В `fixtures` засеять 14 дней синтетической истории |

## Веб и доступ

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_ADDR` | `127.0.0.1:8077` | Адрес интерфейса и API |
| `NKT_COOKIE_SECURE` | `true` | Cookie сессии только по HTTPS; `false` — для SSH-туннеля по HTTP |
| `NKT_SESSION_TTL` | `12h` | Срок жизни сессии |
| `NKT_CORS_ORIGINS` | `http://localhost:5173` | Дополнительные разрешённые origin (для запросов, меняющих состояние, и CORS) |
| `NKT_TLS_ENABLED` | `false` | Отдавать HTTPS самому |
| `NKT_TLS_HOSTS` | `127.0.0.1,::1,<имя хоста>` | Имена и адреса в самоподписанном сертификате |
| `NKT_TLS_CERT`, `NKT_TLS_KEY` | пусто | Свой сертификат и ключ (оба сразу) вместо самоподписанного |
| `NKT_FORWARD_ADDR` | хост из `NKT_ADDR`, порт `8446` | Отдельный адрес для пробросов портов Kubernetes в браузер; `off` — выключить (пробросы по пути, в песочнице) |
| `NKT_FORWARD_PUBLIC_URL` | пусто | Как браузер видит адрес пробросов за прокси: `https://fwd.example.com` |

## Учётные записи

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_BOOTSTRAP_ADMIN_USER` | `admin` | Логин первого администратора |
| `NKT_BOOTSTRAP_ADMIN_PASSWORD` | пусто — сгенерировать | Пароль первого администратора (действует только при первом запуске) |
| `NKT_SYSTEM_LOGIN` | `true` в `local`, иначе `false` | Вход системной учётной записью хоста (нужен `unix_chkpwd`, группа `sudo`/`wheel`/`admin` или root). В хабе недоступен |

## Управление

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_ALLOW_MUTATIONS` | `true` | `false` — всё приложение только для чтения |
| `NKT_COMMAND_TIMEOUT` | `30s` | Предел для команд хоста (`systemctl`, `nginx -t`…) |
| `NKT_TERMINAL_ENABLED` | `false`; у хаба `true` | Веб-терминал (root-shell в браузере). У хаба — для строки «localhost», то есть машины самого хаба |
| `NKT_TERMINAL_IDLE_TIMEOUT` | `30m` | Закрыть терминал без ввода и вывода |
| `NKT_TERMINAL_USER` | пусто — от имени службы | От чьего имени открывается терминал хоста; хаб ставит сюда своего SSH-пользователя |

## Что читать на хосте

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_NGINX_ROOT`, `NKT_NGINX_MAIN_CONFIG` | `/etc/nginx`, `/etc/nginx/nginx.conf` | Конфигурация nginx |
| `NKT_HAPROXY_ROOT`, `NKT_HAPROXY_MAIN_CONFIG` | `/etc/haproxy`, `/etc/haproxy/haproxy.cfg` | Конфигурация haproxy |
| `NKT_CADDY_ROOT`, `NKT_CADDY_MAIN_CONFIG` | `/etc/caddy`, `/etc/caddy/Caddyfile` | Конфигурация Caddy |
| `NKT_FAIL2BAN_ROOT` | `/etc/fail2ban` | Конфигурация fail2ban |
| `NKT_SYSTEMD_UNIT_ROOT` | `/etc/systemd/system` | Юниты в «Конфигурациях» |
| `NKT_NETPLAN_ROOT` | `/etc/netplan` | Netplan |
| `NKT_SSH_ROOT` | `/etc/ssh` | sshd |
| `NKT_SYSCTL_ROOT` | `/etc/sysctl.d` | sysctl |
| `NKT_CRON_ROOT` | `/etc/cron.d` | cron |
| `NKT_COMPOSE_FILES` | `/srv/docker/docker-compose.yml,/opt/stacks/docker-compose.yml` | Compose-файлы, которые показывать и править |
| `NKT_FILES_ROOTS` | `/home,/srv,/opt,/var/www` | Корни проводника «Диски → Файлы» (показываются только существующие; `/tmp` — всегда) |
| `NKT_NGINX_ACCESS_LOGS` | `/var/log/nginx/access.log` | Логи доступа nginx для нагрузки |
| `NKT_HAPROXY_ACCESS_LOGS` | `/var/log/haproxy.log` | Логи доступа haproxy |
| `NKT_DOCKER_SOCKET` | `/var/run/docker.sock` | Сокет Docker |
| `NKT_PODMAN_SOCKET` | `/run/podman/podman.sock` | Сокет Podman |
| `NKT_LIBVIRT_URI` | `qemu:///system` | Подключение к libvirt |

## Наблюдение

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_SCHEDULER_ENABLED` | `true` | Фоновые пробы, метрики, сканы; без него нет истории |
| `NKT_PROBE_INTERVAL` | `1m` | Как часто проверять доступность |
| `NKT_PROBE_TIMEOUT` | `5s` | Предел одной пробы |
| `NKT_METRICS_INTERVAL` | `1m` | Сбор нагрузки (счётчики, контейнеры, машины) |
| `NKT_LOG_SCAN_INTERVAL` | `5m` | Разбор логов доступа |
| `NKT_INVENTORY_INTERVAL` | `5m` | Полный скан хоста |
| `NKT_DRIFT_INTERVAL` | `1h` | Сверка хоста с профилями; `0` — не сверять |
| `NKT_RETENTION` | `720h` (30 дней) | Сколько хранить историю проб и метрик |

## Сертификаты

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_AUTO_RENEW_CERTS` | `false` | Продлевать сертификаты certbot по расписанию (на время продления сайт останавливается) |
| `NKT_AUTO_RENEW_INTERVAL` | `6h` | Как часто проверять |
| `NKT_AUTO_RENEW_WITHIN` | `720h` (30 дней) | За сколько до истечения продлевать |
| `NKT_CERTBOT_TIMEOUT` | `3m` | Предел `certbot` (сетевой вызов к Let's Encrypt) |
| `NKT_CERTBOT_EMAIL` | пусто | Почта для Let's Encrypt при выпуске |

## Хаб

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `NKT_HUB_MASTER_KEY` | пусто — сгенерировать в `NKT_DATA_DIR/hub.key` | Ключ шифрования секретов (AES-256, base64: `openssl rand -base64 32`) |
| `NKT_HUB_SOURCE_ROOT` | рабочий каталог | Клон исходников для кросс-компиляции nkt под хосты; нет исходников — хаб качает релиз |
| `NKT_HUB_GO_BIN` | `go` | Каким Go собирать; не работает — хаб ставит свой в `NKT_DATA_DIR/go-toolchain` |
| `NKT_HUB_RELEASE_REPO` | `piqab/nkt` | Откуда качать релизы (для форков) |
| `NKT_HUB_GITHUB_API` | `https://api.github.com` | API GitHub (для GitHub Enterprise или стенда) |
| `NKT_HUB_HOST_API_PORT` | `8077` | Порт API nkt на хостах (loopback) |
| `NKT_HUB_TUNNEL_PORT` | `8078` | Порт резервного канала на хостах |
| `NKT_HUB_FINDINGS_POLL_INTERVAL` | `60s` | Опрос хостов: доступность и счётчики находок |
| `NKT_HUB_UPDATE_CHECK_INTERVAL` | `6h` | Проверка новых релизов (только бейдж) |
| `NKT_HUB_VULNDB_REFRESH_INTERVAL` | `12h` | Проверка свежести общей базы trivy |
| `NKT_HUB_CLAMDB_REFRESH_INTERVAL` | `24h` | Обновление копии базы ClamAV; `0` — не обновлять |
| `NKT_HUB_APTCACHE_MAX_GB` | `20` | Лимит кэша пакетов; `0` — без лимита (значение из интерфейса важнее) |
| `NKT_HUB_APTCACHE_PORT` | `3142` | Порт кэша на хостах (loopback) |
| `NKT_HUB_EXPORT_PASSWORD` | пусто | Пароль файла для `nkt hub delete -export` и `nkt hub import` без терминала |

Хаб читает и общие переменные выше (`NKT_ADDR`, TLS, учётные записи,
терминал для «localhost», пробросы).

## Служебные

Задаются самим nkt или хабом — вручную их не ставят:

| Переменная | Кто ставит |
|---|---|
| `NKT_HUB_TUNNEL_LISTEN_ADDR`, `NKT_HUB_TUNNEL_TOKEN` | хаб в `nkt.env` хоста с резервным каналом |
| `NKT_GIT_SECRET`, `NKT_CONSOLE_USER` | nkt для дочерних процессов (git, консоли контейнеров) |
| `NKT_FAKE_BIN`, `NKT_FAKE_VM_BIN`, `NKT_TEST_*` | тесты и стенды, см. [Разработку](/guide/development#тесты) |

## nkt-edge

Переменные `EDGE_*` — на странице [nkt-edge](/guide/edge#настроики-edge).
