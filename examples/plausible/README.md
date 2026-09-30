# Plausible — три сервиса, конфиги рядом, ~2 ГБ памяти

[Plausible CE](https://github.com/plausible/community-edition) —
веб-аналитика: приложение, PostgreSQL и ClickHouse. Compose-файл проекта
(ветка `v3.2.1` — версия закреплена самим репозиторием).

| В файле | nkt |
|---|---|
| `./clickhouse/*.xml` в ClickHouse | `files: [clickhouse/]` — каталог целиком рядом с compose-файлом |
| `POSTGRES_PASSWORD=postgres` значением | `env_keys` — пароль из `.env`; `DATABASE_URL` в `.env` — с тем же паролем |
| `BASE_URL`, `SECRET_KEY_BASE` — `${…}` | `.env` конвейера |
| порт приложения не публикуется | сайт публикует `8000` (EXPOSE образа) на 127.0.0.1 сам |
| healthcheck у баз | `up --wait` ждёт; приложение стартует после миграций |

Нужно ~2 ГБ свободной памяти (ClickHouse). Регистрация —
`DISABLE_REGISTRATION=invite_only`: первый пользователь заводится при
первом входе, остальные — по приглашению.

Проверено: выкладка за ~55 секунд, `/api/health` —
`postgres: ok, clickhouse: ok`.
