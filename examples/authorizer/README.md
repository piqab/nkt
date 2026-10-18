# Authorizer + PostgreSQL: вход и пользователи для своих приложений

> **WARNING:** пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются. Проверьте их сами, прежде чем выкладывать в рабочую среду.

[Authorizer](https://github.com/authorizerdev/authorizer) — сервер
авторизации (OAuth2/OIDC, вход по паролю, магическим ссылкам, соцсетям,
MFA) с панелью администратора. Пример — версия 2.4.2 и PostgreSQL 17.

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — стек: `authorizer` и `db`;
- [`pipeline.yaml`](pipeline.yaml) — конвейер.

## Почему свой compose, а не из репозитория authorizer

| В их `docker-compose.yaml` | Здесь |
|---|---|
| `build: .` — образ собирается из исходников | готовый образ `lakhansamani/authorizer:2.4.2` с Docker Hub (версия закреплена, новая — правкой тега) |
| `env_file: .env` | v2 **не читает переменные окружения**: все настройки — флагами запуска (`--client-id`, `--database-url`…); флаги в `command:` подставляются из `.env` конвейера |
| публикует 8080 и 9091 (gRPC без TLS) | 8080 публикует сайт на `127.0.0.1`, наружу — только через прокси; gRPC 9091 и метрики 8081 не публикуются |
| базы нет | PostgreSQL с проверкой готовности, данные — в томе `db-data` |

Секреты во флагах видны на хосте в `docker inspect` и `ps` — так устроен
сам authorizer v2 (настройки только флагами); на хост с чужими
пользователями такой стек не кладите.

## 1. Конвейер

«Выкладки» → «Новый конвейер» → **«Примеры» → «Authorizer + PostgreSQL»**,
выберите хост — описание готово (или вставьте `pipeline.yaml`, поправив
`hosts` и `domains`). Сохраните.

## 2. .env стека

«Доступ» конвейера → **«.env стека»**:

```
AUTH_DOMAIN=auth.example.com
ALLOWED_ORIGINS=https://auth.example.com,https://app.example.com
CLIENT_ID=<openssl rand -hex 16>
CLIENT_SECRET=<openssl rand -hex 32>
ADMIN_SECRET=<openssl rand -hex 32>
JWT_SECRET=<openssl rand -hex 32>
ENCRYPTION_KEY=<openssl rand -hex 32>
POSTGRES_PASSWORD=<пароль базы>
```

- `AUTH_DOMAIN` — то же имя, что в `site:` (из него `--url`, адрес в
  токенах и письмах).
- `ALLOWED_ORIGINS` — сайты, с которых приложения ходят в authorizer
  (через запятую, без пробелов); сам `AUTH_DOMAIN` — тоже.
- `ENCRYPTION_KEY` — отдельно от `JWT_SECRET`: им шифруются ключи TOTP, и
  смена `JWT_SECRET` без него заперла бы всех с двухфакторным входом.

## 3. Выкладка и вход

«Сухой прогон» → «Выложить». Первый старт — около 15 секунд (миграции).

- Панель администратора — `https://auth.example.com/dashboard/`, пароль —
  `ADMIN_SECRET`.
- Страница входа для пользователей — `https://auth.example.com/app/`.
- Приложение подключается с `CLIENT_ID` и адресом `https://auth.example.com`
  (SDK authorizer, OIDC: `https://auth.example.com/.well-known/openid-configuration`).
- Регистрация по умолчанию открыта; если пользователей заводит только
  администратор — раскомментируйте `--enable-signup=false` в compose-файле.

## Через Traffic Fly вместо прокси сайтов

Если хост не виден из интернета, вход — через VPS-шлюз
[Traffic Fly](https://piqab.github.io/nkt/guide/traffic-fly):

1. шлюз уже установлен («Выкладки» → «Traffic Fly» → «Добавить шлюз»);
2. A-запись `auth.example.com` — на адрес шлюза, а не хоста;
3. в `pipeline.yaml` раскомментируйте блок `fly:` и уберите `site:`
   целиком (оба сразу не нужны);
4. «Выложить» — в журнале выкладки «маршрут создан», на карте Traffic Fly —
   маршрут `authorizer`.

Сертификат выпускает шлюз; authorizer получает HTTP с
`X-Forwarded-Proto: https`, поэтому cookie с `secure` работают. gRPC 9091
через шлюз не выводите: он без TLS.

Проверено: выкладка на docker 29 за ~13 секунд, оба сервиса healthy; панель
и страница входа отвечают, API принимает `ADMIN_SECRET`, регистрация пишет в
PostgreSQL и переживает перезапуск контейнера.
