# hello-app — пример выкладки через хаб nkt

Маленькое веб-приложение на Python (только стандартная библиотека) и всё,
что нужно, чтобы выкладывать его из Git через раздел хаба **«Выкладки»**:

- `app.py`, `test_app.py` — приложение (`/` — страница с версией,
  `/healthz` — проверка) и тесты;
- `Dockerfile` — образ;
- `.github/workflows/build.yml` — GitHub Actions: тесты, сборка образа в
  GHCR, вызов вебхука хаба с тегом образа;
- `.gitlab-ci.yml` — то же для GitLab CI (образ во встроенном registry
  GitLab), `.gitea/workflows/build.yml` — для Gitea и Forgejo Actions;
- `scripts/nkt-hook.sh` — подписанный вызов вебхука хаба из любого CI или
  вручную после локальной сборки;
- `deploy/` — четыре варианта выкладки:

| Вариант | Файлы | Куда |
|---|---|---|
| 1. Манифест | `pipeline-k8s.yaml`, `k8s.yaml` | кластер Kubernetes (`kubectl apply`) |
| 2. Helm | `pipeline-helm.yaml`, `values.yaml` | кластер, Helm-релиз на чарте onechart |
| 3. Хост с Docker | `pipeline-host.yaml`, `deploy.nkt` | хост без кластера, `docker compose` |
| 4. Без вебхука | `pipeline-registry.yaml`, `k8s.yaml` | кластер; хаб сам следит за тегами образа в registry |

Сборку делает CI, хаб выкладывает готовый образ: ключи доступа к хостам и
кластерам остаются на хабе, CI получает только право «дёрнуть» вебхук.

## 1. Репозиторий

1. Скопируйте каталог `examples/hello-app` в новый репозиторий GitHub
   (содержимое — в корень, вместе с `.github`).
2. Замените `OWNER` на ваш логин или организацию (в нижнем регистре) в
   `deploy/*.yaml` и `deploy/deploy.nkt`; в `k8s.yaml` и `values.yaml` —
   имя `hello.example.com` на своё.
3. Сделайте первый push в `main`. Workflow прогонит тесты и соберёт
   `ghcr.io/OWNER/hello-app:<коммит>`; вебхук пока не вызывается — секретов
   ещё нет.
4. GitHub → профиль → Packages → `hello-app` → Package settings → сделайте
   пакет публичным (или добавьте кластеру/хосту доступ к закрытому GHCR).

## 2. Конвейер в хабе

1. «Выкладки» → «Новый конвейер», имя `hello-app`.
2. Вставьте один из `deploy/pipeline-*.yaml`, поправьте `repo` и:
   - варианты 1 и 2 — `clusters` (имена кластеров из раздела «Кластеры»)
     или `group`;
   - вариант 3 — имя хоста `web1` в `deploy/deploy.nkt`.
3. Репозиторий закрытый — «Доступ»: токен GitHub с правом чтения
   (Contents: read) или ключ развёртывания.
4. «Выложить» → в поле тега — тег собранного образа (короткий коммит из
   журнала workflow). Задание покажет каждый шаг; итог — в «Истории».

## 3. Вебхук: выкладка по push

1. В хабе: конвейер → «Вебхук» — адрес (через nkt-edge, если хаб за NAT)
   и «Показать секрет».
2. В GitHub: Settings → Secrets and variables → Actions:
   `NKT_HOOK_URL` — адрес, `NKT_HOOK_SECRET` — секрет.
3. Поменяйте что-нибудь в `app.py`, push в `main`: workflow соберёт
   образ и вызовет вебхук с его тегом, хаб выложит.
4. Выпуск версии: `git tag v1.0.0 && git push origin v1.0.0` — образ
   `hello-app:v1.0.0` и выкладка с этим тегом.

GitLab CI и Gitea Actions — так же: переменные `NKT_HOOK_URL` и
`NKT_HOOK_SECRET` в настройках CI проекта (подробности — в шапке
`.gitlab-ci.yml` и `.gitea/workflows/build.yml`). Разбор всех вариантов —
на сайте, страница «Примеры CI/CD».

Вебхук подписан (`X-NKT-Signature`, HMAC-SHA256 от «отметка времени.тело»):
без секрета или с устаревшей отметкой хаб его отвергнет, повтор той же
подписи — тоже.

## 4. Откат

Конвейер → «История» → «Откатить» у прежней удачной выкладки: хаб выложит
её коммит и тег заново (образ уже есть в registry).

## Как устроены варианты

**1. Манифест** — `deploy/k8s.yaml`: Namespace, Deployment (2 реплики,
проверка `/healthz`, limits), Service, Ingress. `{{nkt.tag}}` в имени образа
хаб заменяет тегом из вебхука. Каждое применение попадает и в библиотеку
манифестов хаба («Кластеры» → «Манифесты»).

**2. Helm** — чарт [onechart](https://github.com/gimlet-io/onechart),
универсальный чарт для одного приложения; values — `deploy/values.yaml`,
тег образа хаб подставляет в `image.tag` (`tag_key`). Ключи values
сверяйте по кнопке «Значения чарта по умолчанию» в окне релиза.

**3. Хост с Docker** — `deploy/deploy.nkt`: сценарий хаба поднимает compose
на хосте `web1` с образом `hello-app:${TAG}` (TAG — тег из вебхука) и ждёт
ответа `/healthz`. Кластер не нужен, достаточно хоста с Docker в хабе.

**4. Без вебхука** — `deploy/pipeline-registry.yaml`: хаб раз в 5 минут
смотрит теги образа в registry и выкладывает новый тег-версию. CI нужен
только чтобы собрать и запушить образ по тегу репозитория; секретов хаба
в CI нет, хаб наружу не открывается.

## Локально

```bash
python -m unittest -v test_app
APP_VERSION=local python app.py      # http://127.0.0.1:8000
docker build -t hello-app --build-arg VERSION=local . && docker run -p 8000:8000 hello-app
```

Без CI — собрать, запушить и выложить с рабочей машины:

```bash
TAG=$(git rev-parse --short=12 HEAD)
docker build --build-arg VERSION=$TAG -t ghcr.io/OWNER/hello-app:$TAG . && docker push ghcr.io/OWNER/hello-app:$TAG
NKT_HOOK_URL=… NKT_HOOK_SECRET=… sh scripts/nkt-hook.sh "$TAG" "$(git rev-parse HEAD)" main
```

(или «Выложить» в хабе с этим тегом).
