# Раннер Forgejo Actions с Docker-in-Docker

Свой раннер для сборки образов контейнеров, выложенный конвейером nkt:

- `docker` — отдельный dockerd (`docker:29.9-dind`, **privileged**):
  задания собирают образы в нём, а не в Docker хоста;
- `runner-register` — разовая регистрация в Forgejo токеном из `.env`
  (повторно не регистрирует: `.runner` лежит в томе);
- `runner` — `forgejo-runner daemon`, задания ходят в DinD по TLS;
- `docker-gc` — раз в сутки чистит кэш сборок и образы старше 7 дней.

nkt образы не собирает — это работа CI; этот пример поднимает CI рядом с
хабом.

## 1. Хост

Отдельный хост или машина для сборок: privileged-контейнер — это root на
хосте, а задания выполняют код из репозиториев. Не ставьте раннер рядом с
рабочими стеками. Токен «по правам» такой стек не выложит (privileged
запрещён) — выкладывайте от администратора.

## 2. Токен регистрации

Forgejo → «Администрирование» → «Actions» → «Раннеры» → **«Создать новый
раннер»** (или то же в настройках организации или репозитория — тогда
раннер только для них). Скопируйте токен.

## 3. Выложить

«Выкладки» → «Новый конвейер» → **«Примеры» → «Раннер Forgejo Actions»**
или вставьте [`pipeline.yaml`](pipeline.yaml), хост — свой (`build1`).
В `.env` стека: `FORGEJO_URL`, `RUNNER_TOKEN`, имя и метки. При сохранении
значения уходят в секреты конвейера, в описании их не остаётся. «Сухой
прогон», затем «Выложить».

Проверка: Forgejo → «Раннеры» — раннер в состоянии «Ожидание» (online).
Перерегистрация (новый токен, другой Forgejo) — удалите том
`forgejo-runner_runner-data` и выложите снова.

Метки (`RUNNER_LABELS`):

- `ubuntu-latest` — образ с node, git и docker CLI, как `runs-on:
  ubuntu-latest` в GitHub Actions (`actions/checkout` работает);
- `docker` — только docker CLI.

## 4. Сборка образа и выкладка

[`workflows/build-app.yml`](workflows/build-app.yml) — в репозиторий
приложения как `.forgejo/workflows/build.yml`. Push в `main` — образ
`<registry>/<владелец>/<репозиторий>:<короткий коммит>`, тег `v1.2.3` —
`…:v1.2.3`, в registry самого Forgejo.

Переменная репозитория `REGISTRY` — адрес Forgejo без `https://`; секрет
`REGISTRY_TOKEN` — токен Forgejo с правом `write:package`.

Дальше — один из двух способов.

### Способ 1. Вебхук — сразу после сборки

Скопируйте [`scripts/nkt-hook.sh`](../hello-app/scripts/nkt-hook.sh) в
репозиторий приложения, в секреты — `NKT_HOOK_URL` и `NKT_HOOK_SECRET` из
окна «Вебхук» конвейера приложения. Последний шаг workflow вызовет его с
тегом образа — хаб выложит именно его. Хаб должен быть доступен из
раннера.

### Способ 2. Хаб сам следит за registry

Без секретов хаба в CI и без входящих к хабу. В конвейере приложения:

```yaml
action: compose
compose:
  file: deploy/docker-compose.yml
  project: app
  hosts: [web1]
  images:
    app: git.example.com/owner/app:{{nkt.tag}}
registry: git.example.com/owner/app
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 5m
```

Закрытый registry — ключ (адрес, логин, токен с `read:package`) в
«Секреты» → «Реестры» конвейера. Опрос начинается после первой выкладки
вручную.

## 5. Пример: образ хаба nkt

1. Forgejo → «Новая миграция» → зеркало `https://github.com/piqab/nkt`.
2. [`workflows/build-nkt-hub.yml`](workflows/build-nkt-hub.yml) — в него
   как `.forgejo/workflows/hub.yml`, `REGISTRY` и `REGISTRY_TOKEN` — как
   выше.
3. Тег `v1.13.101` → образ `git.example.com/owner/nkt-hub:v1.13.101`.
4. Тестовый хаб ([`nkt-hub-test`](../nkt-hub-test)) со своим образом и
   опросом registry:

```yaml
repo: https://github.com/piqab/nkt.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.hub.release.yml
  project: nkt-hub-test
  hosts: [localhost]
  images:
    hub: git.example.com/owner/nkt-hub:{{nkt.tag}}
  ports:
    hub: ["127.0.0.1:18443:8077", "127.0.0.1:18446:8446"]
  env_keys:
    hub: [NKT_BOOTSTRAP_ADMIN_PASSWORD]
  env:
    NKT_BOOTSTRAP_ADMIN_PASSWORD: generate:password
registry: git.example.com/owner/nkt-hub
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 10m
```

Рабочий хаб так обновлять не стоит — для него есть «Обновить» в
настройках; это пример сборки своего образа целиком на своих машинах.
