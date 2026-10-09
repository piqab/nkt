---
title: Примеры CI/CD
---

# Примеры CI/CD

Все примеры — на одном приложении,
[`examples/hello-app`](https://github.com/piqab/nkt/tree/main/examples/hello-app):
веб-приложение на Python (только стандартная библиотека), тесты,
Dockerfile, CI для трёх систем и четыре варианта выкладки. Файлы примера
проверяются тестами nkt — описания конвейеров, манифест, values,
сценарий и скрипт подписи вебхука проходят те же проверки, что в хабе.

Разделение ролей везде одинаковое: **CI тестирует и собирает образ,
хаб выкладывает**. Ключи от кластеров и хостов остаются на хабе; CI
знает только адрес и секрет вебхука одного конвейера — а в варианте
«без вебхука» и их нет.

```
push / тег ──▶ CI: тесты → образ → registry ──▶ вебхук хаба (подпись)
                                                  │
                         хаб: git checkout → manifest | helm | script
                                                  │
                             кластеры Kubernetes / хосты с Docker
```

## Шаг 1. Репозиторий

1. Скопируйте `examples/hello-app` в новый репозиторий (содержимое — в
   корень, вместе со скрытыми `.github`, `.gitea`, `.gitlab-ci.yml` —
   лишнее можно удалить).
2. Замените `OWNER` на свой логин или организацию **в нижнем регистре**
   в `deploy/*.yaml` и `deploy/deploy.nkt`; имя `hello.example.com` в
   `k8s.yaml` и `values.yaml` — на своё.
3. Проверьте локально:
   ```bash
   python -m unittest -v test_app
   APP_VERSION=local python app.py            # http://127.0.0.1:8000
   docker build -t hello-app --build-arg VERSION=local . && docker run -p 8000:8000 hello-app
   ```

## Шаг 2. Куда выкладывать — конвейер в хабе

«Выкладки» → «Новый конвейер», вставьте один из файлов `deploy/` и
поправьте `repo`, `clusters` или имя хоста.

### Вариант 1. Манифест в Kubernetes

`deploy/pipeline-k8s.yaml` + `deploy/k8s.yaml` (Namespace, Deployment на 2
реплики с проверкой `/healthz`, лимитами и ограниченным
`securityContext`, Service, Ingress):

```yaml
repo: https://github.com/OWNER/hello-app.git
ref: main
tags: "v*"
action: manifest
manifests:
  - deploy/k8s.yaml
clusters: [prod]
```

В `k8s.yaml` образ записан как <code v-pre>ghcr.io/OWNER/hello-app:{{nkt.tag}}</code> —
хаб подставит тег из вебхука.

### Вариант 2. Helm-релиз

`deploy/pipeline-helm.yaml` + `deploy/values.yaml`. Чарт —
[onechart](https://github.com/gimlet-io/onechart), универсальный чарт для
одного приложения; тег образа хаб кладёт в `image.tag`:

```yaml
action: helm
clusters: [prod]
helm:
  repo_name: onechart
  repo_url: https://chart.onechart.dev
  chart: onechart
  release: hello-app
  namespace: hello
  values: deploy/values.yaml
  tag_key: image.tag
```

Ключи values сверяйте кнопкой «Значения чарта по умолчанию» в окне
релиза Helm. На control plane должен стоять Helm.

### Вариант 3. Хост с Docker, без кластера

`deploy/pipeline-host.yaml` + `deploy/deploy.nkt` — сценарий хаба:

```
param TAG "тег образа"

on web1 docker stack /srv/hello-app/docker-compose.yml up
services:
  web:
    image: ghcr.io/OWNER/hello-app:${TAG}
    restart: unless-stopped
    ports:
      - "8080:8000"
    environment:
      APP_VERSION: ${TAG}
end

wait web1 http http://127.0.0.1:8080/healthz 200 60s
```

`web1` — имя хоста в хабе. Хаб кладёт compose-файл с новым тегом,
поднимает стек и ждёт ответа `/healthz`; если не дождался — выкладка
помечается ошибкой.

### Вариант 4. Без вебхука — хаб следит за registry

`deploy/pipeline-registry.yaml`:

```yaml
repo: https://github.com/OWNER/hello-app.git
ref: main
action: manifest
manifests:
  - deploy/k8s.yaml
clusters: [prod]
registry: ghcr.io/owner/hello-app
registry_tags: '^v\d+\.\d+\.\d+$'
registry_poll: 5m
```

Хаб раз в 5 минут смотрит теги образа и выкладывает новую версию.
Секретов хаба в CI нет, хаб наружу не открывается. Закрытый registry —
ключ registry (адрес, логин, токен) в «Секреты» → «Реестры» конвейера.

### Первая выкладка

Нажмите **«Выложить»** и укажите тег уже собранного образа (например,
короткий коммит из журнала CI). Опрос и registry начинают работать
только после этой первой выкладки.

## Шаг 3. Кто собирает — CI

Во всех трёх системах одинаково: push в `main` → образ с тегом
«короткий коммит» (12 знаков); тег `v1.2.3` → образ `:v1.2.3`; затем —
вебхук с этим тегом. Если адрес вебхука не задан, CI только собирает
образ.

Вызов вебхука во всех примерах — через
[`scripts/nkt-hook.sh`](https://github.com/piqab/nkt/blob/main/examples/hello-app/scripts/nkt-hook.sh):
подпись nkt (HMAC-SHA256 от «отметка времени.тело»), ветка, коммит, тег.
Нужны `sh`, `openssl` и `curl`.

### GitHub Actions

Файл — `.github/workflows/build.yml`. Образ — в GHCR, вход встроенным
`GITHUB_TOKEN` (права `packages: write` уже заданы в файле).

1. Settings → Secrets and variables → Actions → **Secrets**:
   `NKT_HOOK_URL` — адрес из окна «Вебхук» конвейера, `NKT_HOOK_SECRET` —
   секрет оттуда же.
2. Первый образ — после первого push; сделайте пакет публичным (профиль →
   Packages → `hello-app` → Package settings) или дайте кластеру доступ к
   закрытому GHCR.

```yaml
      - name: Выложить через хаб nkt
        if: ${{ env.NKT_HOOK_URL != '' }}
        env:
          NKT_HOOK_SECRET: ${{ secrets.NKT_HOOK_SECRET }}
        run: sh scripts/nkt-hook.sh "${{ steps.tag.outputs.tag }}" "$GITHUB_SHA" main
```

### GitLab CI

Файл — `.gitlab-ci.yml`: стадии `test`, `build` (Docker-in-Docker,
образ — во встроенный registry GitLab `$CI_REGISTRY_IMAGE`), `deploy`.

1. Settings → CI/CD → **Variables**: `NKT_HOOK_URL` и `NKT_HOOK_SECRET`,
   с галочками Masked и Protected (ветка `main` и теги `v*` должны быть
   защищёнными, иначе protected-переменные в них не видны).
2. Раннеру нужен Docker-in-Docker (`privileged = true`); на gitlab.com
   общие раннеры его уже умеют.
3. Кластеру — доступ к registry проекта: публичный проект или Deploy
   token с `read_registry` в `imagePullSecret`.

```yaml
deploy:
  stage: deploy
  image: alpine:3.20
  needs: [build]
  rules:
    - if: $NKT_HOOK_URL
  script:
    - apk add --no-cache curl openssl
    - sh scripts/nkt-hook.sh "$TAG" "$CI_COMMIT_SHA" main
```

Тег образа переходит из `build` в `deploy` через `artifacts:reports:dotenv`.

Вместо шага CI можно подключить **обычный вебхук GitLab** (Settings →
Webhooks, Secret token — секрет конвейера, события Push и Tag push): хаб
проверит `X-Gitlab-Token`. Тогда тег образа — короткий коммит, и образ к
моменту выкладки уже должен быть собран — подходит для вариантов, где
образ не нужен или собирается раньше.

### Gitea и Forgejo Actions

Файл — `.gitea/workflows/build.yml` (синтаксис как у GitHub Actions,
раннер — `act_runner` с Docker). Образ — во встроенный registry Gitea.

1. Settings → Actions → **Variables**: `REGISTRY` — адрес Gitea без
   `https://` (`gitea.example.com`).
2. **Secrets**: `REGISTRY_TOKEN` — токен Gitea с правом `write:package`,
   `NKT_HOOK_URL`, `NKT_HOOK_SECRET`.

Обычный вебхук Gitea/Forgejo тоже подходит: Settings → Webhooks → Gitea,
Secret — секрет конвейера; хаб проверит `X-Gitea-Signature`.

### Без CI: собрать у себя

```bash
TAG=$(git rev-parse --short=12 HEAD)
docker build --build-arg VERSION=$TAG -t ghcr.io/OWNER/hello-app:$TAG .
docker push ghcr.io/OWNER/hello-app:$TAG
NKT_HOOK_URL=… NKT_HOOK_SECRET=… sh scripts/nkt-hook.sh "$TAG" "$(git rev-parse HEAD)" main
```

Или без вебхука — «Выложить» в хабе с этим тегом.

### Без своего образа

Если образ собирать не нужно (манифест с публичным образом, конфиг,
статический сайт через сценарий `git clone`) — подключите обычный
вебхук GitHub, GitLab или Gitea прямо к конвейеру или включите
`poll: 5m`: хаб выложит новый коммит ветки сам.

## Шаг 4. Выпуск, проверка, откат

- **Выпуск версии**: `git tag v1.0.0 && git push origin v1.0.0` — CI
  соберёт `hello-app:v1.0.0` и вызовет вебхук (варианты 1–3), либо хаб сам
  найдёт тег в registry (вариант 4).
- **Проверка**: конвейер → «История» — кто запустил, коммит, тег, итог и
  журнал задания. Отклонённые вебхуки — в журнале действий хаба
  (`pipeline.hook.rejected` с причиной).
- **Откат**: «История» → «Откатить» у прежней удачной выкладки — хаб
  выложит её коммит и тег заново, без пересборки.

## Сухой прогон из CI перед выкладкой

CI может сначала спросить хаб, пройдёт ли выкладка, и вызывать вебхук,
только если да. Нужен [API-токен](/guide/hub-api) с ролью «администратор»
(сухой прогон — действие), лучше с пределами по хостам конвейера. Ключ и
секрет — в секретах CI (`NKT_KEY`, `NKT_SECRET`), адрес хаба или edge с
ролью API — `NKT_URL`.

```yaml
# .github/workflows/release.yml — шаг перед вызовом вебхука
- name: Сухой прогон на хабе
  env:
    NKT_URL: ${{ secrets.NKT_URL }}
    NKT_KEY: ${{ secrets.NKT_KEY }}
    NKT_SECRET: ${{ secrets.NKT_SECRET }}
  run: |
    call() {  # метод, путь, тело — подписанный запрос к хабу
      ts=$(date +%s); nonce=$(openssl rand -hex 16)
      hash=$(printf %s "$3" | sha256sum | cut -d' ' -f1)
      sig=$(printf 'NKT-API-1\n%s\n%s\n%s\n%s\n%s' "$ts" "$nonce" "$1" "$2" "$hash" \
        | openssl dgst -sha256 -hmac "$NKT_SECRET" | sed 's/.* //')
      curl -sf -X "$1" -H 'Content-Type: application/json' \
        -H "X-NKT-API-Key: $NKT_KEY" -H "X-NKT-API-Timestamp: $ts" \
        -H "X-NKT-API-Nonce: $nonce" -H "X-NKT-API-Signature: $sig" \
        ${3:+-d "$3"} "$NKT_URL$2"
    }
    job=$(call POST /api/hub/pipelines/dryrun '{"pipeline_id":1}' | jq -r .job_id)
    for i in $(seq 1 120); do
      status=$(call GET /api/hub/jobs/$job "" | jq -r .status)
      case "$status" in queued|running) sleep 5 ;; *) break ;; esac
    done
    call GET "/api/hub/jobs/$job/log?after=0" "" | jq -r '.lines[].text'
    test "$status" = succeeded
```

Сухой прогон не прошёл — шаг падает, вебхук не вызывается, журнал
проверок — в выводе CI.

## Хаб за NAT

Вебхуку нужен адрес, до которого достучится CI. Если хаб не открыт
наружу — [nkt-edge](/guide/edge) на VPS с ролью «вебхуки» или вариант 4
(registry) и `poll`, которым ничего открывать не нужно. Сухому прогону из
CI — edge с ролью «API» (`NKT_URL` — его имя, у токена — галочка «через
nkt-edge»).

Если что-то не выкладывается — таблица в конце страницы
[Выкладки](/guide/hub-deploy#если-не-выкладывается).
