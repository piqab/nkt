---
title: Командная строка и настройка
---

# Командная строка, TUI, API, настройка

## Команды

| Команда | Что делает |
|---|---|
| `nkt serve` (или просто `nkt`) | веб-интерфейс и API |
| `nkt scan` | разовый скан хоста в JSON — для скриптов и проверки перед установкой |
| `nkt tui` | терминальный интерфейс |
| `nkt users`, `nkt passwd` | учётные записи и пароли без веб-интерфейса |
| `nkt hub` | [хаб](/guide/hub); `nkt hub import` — восстановление реестра, `nkt hub delete` — полное удаление данных хаба |
| `nkt version` | версия |

## Терминальный интерфейс

`nkt tui` показывает те же данные в терминале: обзор, проблемы, карта
ресурсов деревом, сервисы, контейнеры, сертификаты, конфигурации,
доступность и нагрузка. Управление сервисами и контейнерами, продление
сертификатов, откат конфигов. На русском и английском.

## API

Всё, что видно в интерфейсе, доступно как JSON API с той же
авторизацией: `POST /api/auth/login` → cookie сессии, дальше
`GET /api/overview`, `/api/findings`, `/api/services`… Через хаб — те же
пути с приставкой `/api/hosts/{id}/`.

## Настройка

Каждая настройка — переменная окружения `NKT_*` в
`/etc/netknownsthat/nkt.env`; полный список с пояснениями — в
[deploy/nkt.env.example](https://github.com/piqab/nkt/blob/main/deploy/nkt.env.example).
Главное:

| Переменная | Значение |
|---|---|
| `NKT_ADDR` | адрес прослушивания, по умолчанию `127.0.0.1:8077` |
| `NKT_DATA_DIR` | база и история конфигов, по умолчанию `/var/lib/netknownsthat` |
| `NKT_ALLOW_MUTATIONS` | `false` — только чтение |
| `NKT_COOKIE_SECURE` | `false` для обычного HTTP (SSH-туннель) |
| `NKT_TLS_ENABLED`, `NKT_TLS_HOSTS`, `NKT_TLS_CERT`, `NKT_TLS_KEY` | собственный HTTPS |
| `NKT_TERMINAL_ENABLED` | веб-терминал, по умолчанию выключен |
| `NKT_COMPOSE_FILES` | compose-файлы через запятую |
| `NKT_FILES_ROOTS` | корни проводника файлов |
| `NKT_PROBE_INTERVAL` | как часто проверять доступность |
| `NKT_AUTO_RENEW_CERTS` | автопродление сертификатов certbot |
| `NKT_SCHEDULER_ENABLED` | фоновые проверки и сбор метрик |

Хаб настраивается так же — через `hub.env`, переменные `NKT_HUB_*`
описаны в [HUB.md](https://github.com/piqab/nkt/blob/main/HUB.md#6-настройка-переменные-окружения).
