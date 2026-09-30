# n8n + PostgreSQL — три сервиса, файл рядом, всё из .env

[n8n](https://n8n.io) — автоматизация. Compose-файл из
[n8n-io/n8n-hosting](https://github.com/n8n-io/n8n-hosting/tree/main/docker-compose/withPostgres)
(вариант `withPostgres`): PostgreSQL, n8n и task runner.

| В файле | nkt |
|---|---|
| все настройки — `${…}` из `.env` | `.env` конвейера («Доступ» → «.env стека»), в том числе `N8N_VERSION` — версия закреплена там |
| `./init-data.sh` в каталоге базы | `files:` — едет рядом с compose-файлом; создаёт пользователя базы для n8n |
| `ports: 5678:5678` | `bind`: `127.0.0.1:5678`, сайт смотрит туда |
| нет `WEBHOOK_URL`, `N8N_PROXY_HOPS` | `env_keys` добавляет их в `environment` n8n со значениями из `.env` — нужны за HTTPS-прокси |

Шаблон `.env` — в конце `pipeline.yaml`. У n8n нет healthcheck: `up
--wait` ждёт запуска, веб-интерфейс отвечает через несколько секунд.

Проверено: выкладка за ~20 секунд (образы скачаны), `init-data.sh`
выполнен (`CREATE ROLE`), `/healthz` отвечает `ok`.
