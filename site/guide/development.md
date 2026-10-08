---
title: Разработка
---

# Разработка

Сборка из исходников, проверочный стенд, тесты, устройство кода, сайт и
переводы.

## Что нужно

Docker 29.6 (сборка релиза и стенд), Go 1.26 (минимум 1.25 — требует
`modernc.org/sqlite`), Node 24 / npm 11 (достаточно Node 20). На целевом
хосте — ничего, кроме бинарника.

## Команды сборки

`make` без аргументов печатает список целей.

| Команда | Что делает |
|---|---|
| `make build` | **Основная цель.** Продакшен-бинарник `dist/nkt` (статический ELF, версия из `VERSION` и `git describe`). С Docker — в контейнерах, без — через `native-build`. Другая архитектура: `make build GOARCH=arm64` |
| `make web` | Только веб-интерфейс в `internal/webui/dist`, откуда его вшивает `go:embed`. `make build` вызывает сам |
| `make native-build` | Сборка без Docker: при нехватке Go или Node спрашивает и ставит их в `$HOME/.local`, без sudo |
| `make build-dev` | Бинарник для текущей ОС — **только для режима fixtures** |
| `make edge` | `nkt-edge` (вебхуки выкладок для VPS) в `dist/` |
| `make test` | `go test ./...` и проверка типов фронтенда |
| `make check` | То же плюс `go vet` и `gofmt` — гонять перед коммитом |
| `make stand` / `make stand-down` | Проверочный стенд (ниже) |
| `make hub` / `make hub-down` | Хаб в Docker из исходников |
| `make install` | Поставить на **этот** Linux-хост; свой `nkt.env` не перезаписывает |
| `make hub-install` | Поставить хаб на этот хост службой |
| `make bump-version` | Увеличить патч-версию в `VERSION` |
| `make clean` | Удалить `dist/` и локальные бинарники |

Кэш Go и npm живёт в именованных docker-томах, повторная сборка не тянет
зависимости заново. Сбросить: `docker volume rm nkt-gomod nkt-gocache nkt-npm`.

Под Windows `make` обычно нет, а `native-build` рассчитана на Linux —
нужен Docker:

```powershell
docker run --rm -v "${PWD}:/src" -w /src/web node:22-alpine sh -c "npm ci && npm run build"
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux golang:1.26-alpine `
  go build -trimpath -ldflags "-s -w" -o dist/nkt ./cmd/nkt
```

## Релизы

Под каждый тег `vX.Y.Z` `.github/workflows/release.yml` собирает и
публикует бинарники `nkt` и `nkt-edge` для amd64/arm64/arm, образ хаба
`ghcr.io/piqab/nkt-hub`, манифесты `docker-compose.hub.release.yml` и
`k8s-hub.yaml` с точной версией образа и `SHA256SUMS`. Тег
`vX.Y.Z-beta` (`git tag v$(cat VERSION)-beta`) собирает бету: релиз
pre-release, версия `X.Y.Z-beta`, образ `:X.Y.Z-beta` и `:beta`; хаб
предлагает её только с галочкой «использовать бета-версии».

**Нумерация — semver.** Версия в `VERSION` меняется каждым коммитом:
первая новая функция после последнего тега поднимает minor и сбрасывает
patch (`1.11.184` → `1.12.0`), всё остальное — исправления, справка, CI и
следующие функции до нового тега — patch (`1.12.0` → `1.12.1`); major —
только для несовместимых изменений. Patch меньше 1000: из версии
считается `versionCode` Android-приложения.

Описание релиза берётся не из коммитов, а из `WHATSNEW.md` и
`WHATSNEW.en.md`: раздел `## vX.Y.Z` пишется тем же коммитом, что и бамп
`VERSION`, а `scripts/release-notes.sh` при выпуске собирает разделы,
накопившиеся с прошлого тега, в тело GitHub Release (английская часть —
после маркера `<!-- en -->`). Хаб показывает его в «О системе» на языке
интерфейса — поэтому текст пишется для пользователя. Забытый раздел
релиз не ломает: workflow откатывается на список коммитов.

Новая возможность — ещё и строка в `FEATURES.ru.md` и `FEATURES.md`, и
правка сайта на обоих языках.

## Проверочный стенд

Настоящие nginx и haproxy с бэкендами, рядом — nkt в режиме `local`,
читающий те же файлы и работающий с настоящим docker.

```bash
make stand              # docker compose -f stand/docker-compose.yml up -d --build
docker logs nkt         # пароль администратора
make stand-down         # остановить и удалить тома
```

| Адрес | Что это |
|---|---|
| <http://127.0.0.1:8077> | nkt |
| <http://127.0.0.1:8081> | nginx, проксирует на два бэкенда |
| <http://127.0.0.1:8082> | haproxy, балансирует те же бэкенды |
| <http://127.0.0.1:8404> | панель статистики haproxy (намеренно без пароля) |
| `127.0.0.1:6380` | redis, намеренно опубликован на всех интерфейсах |

Стенд проверяет разбор живых конфигов, Docker Engine API, управление
контейнерами и цикл правки конфигурации с настоящим `nginx -t`, включая
отказ с автооткатом. Не покрывает systemd и firewall хоста (их нет в
контейнере) и «объявлено против слушается» для nginx и haproxy — они в
своих сетевых пространствах имён; приложение это распознаёт и молчит.

`make stand-down` удаляет тома обязательно: учётная запись живёт в томе,
без его удаления пересозданный стенд не напечатает новый пароль.

## Фронтенд с горячей перезагрузкой

```bash
./nkt                        # API на :8077
cd web && npm run dev        # интерфейс на :5173, проксирует /api
```

## Тесты

```bash
make test        # go test ./... и npm run typecheck в web/
make check       # плюс go vet и gofmt
```

::: warning Остановите локальный nkt на 8077
Тесты хаба поднимают свои процессы nkt; запущенный рядом стенд или
демо на `127.0.0.1:8077` мешает им (ошибки `401` и `429`). Перед
`go test ./...` остановите его.
:::

- Тесты работают против `fixtures/host` — снапшота с заложенными
  проблемами; `TestScanFindsPlantedProblems` проверяет, что анализатор
  находит каждую с ожидаемой серьёзностью.
- Терминальный интерфейс тестируется headless: `tcell` отдаёт
  симулированный экран, тест нажимает клавиши и проверяет кадры.
- Хаб тестируется против **настоящего** `sshd` и настоящего процесса nkt —
  установка по SSH, проксирование API, WebSocket-сессии терминала через
  туннель. Моков в этом слое намеренно нет.
- Пример `examples/hello-app` проверяется `TestHelloAppExample` и
  `TestHelloAppHookScript` (`internal/hub/examples_test.go`): описания
  конвейеров, манифест, values, сценарий, файлы CI и настоящий вызов
  `scripts/nkt-hook.sh` с проверкой подписи.

Живые тесты, которые ходят в сеть, по умолчанию пропускаются:

| Переменная | Что включает |
|---|---|
| `NKT_TEST_LIVE_VULN=1` | Скачивание trivy и его базы, настоящий скан (`internal/vuln`) |
| `NKT_TEST_LIVE_REGISTRY=1` | Запросы к реестру образов (`internal/aptcache`) |
| `NKT_TEST_LIVE_GO_INSTALL=1` | Установка Go при сборке из исходников (`internal/hub`) |
| `NKT_TEST_LIVE_RELEASE_DOWNLOAD=1` | Скачивание бинарника релиза с GitHub (`internal/hub`) |
| `NKT_TEST_LIVE_RELEASE_VERSION` | Какую версию релиза качать в этом тесте |
| `NKT_TEST_LIVE_VERSION_CHECK=1` | Проверка последней версии через API GitHub (`internal/hub`) |

## Проверки безопасности

В CI (`.github/workflows/security.yml`) — govulncheck, gitleaks, CodeQL,
gosec, Trivy. Локально перед коммитом полезно прогнать то же:

```bash
govulncheck ./...
gosec -conf .gosec.json ./...
trivy fs --scanners vuln,misconfig,secret .
```

## Устройство

```
cmd/nkt                 подкоманды: serve, tui, scan, hub
cmd/nkt-edge            входной сервис вебхуков для VPS
internal/config         все настройки из NKT_* переменных
internal/collect        ЕДИНСТВЕННОЕ место, где расходятся fixtures и реальный хост
  ├── local.go          настоящая ФС, exec, unix-сокеты docker/podman (только Linux)
  └── fixtures.go       снапшот на диске, заготовленные ответы команд
internal/parse          nginx, haproxy, caddy, docker, podman, lxd, libvirt,
                        iptables, ufw, firewalld, ss, systemd
internal/model          вендор-нейтральное описание найденного
internal/analyze        правила поиска проблем
internal/topology       граф ресурсов
internal/inventory      оркестрация скана, снапшоты, цели мониторинга
internal/monitor        пробы, метрики, логи доступа, планировщик
internal/tlscheck       живой TLS: что сокет отдаёт на самом деле
internal/control        сервисы, правка конфигов с версиями, firewall,
                        сертификаты, podman/lxd/libvirt
internal/k8s            Kubernetes: объекты, действия, Helm, пробросы
internal/jobs           фоновые задания с журналом и продолжением
internal/profile        профили: желаемое состояние, план, применение
internal/script         язык сценариев хаба
internal/deploy         выкладки: описание конвейера, git, вебхуки, registry
internal/edge           протокол туннеля хаб ↔ nkt-edge
internal/aptcache       кэш пакетов, файлов и образов на хабе
internal/store          SQLite (modernc, чистый Go — статическая сборка)
internal/secretbox      шифрование секретов (AES-256-GCM) для хаба
internal/hub            хаб: установка по SSH, проксирование, кластеры, выкладки
internal/msgs           каталог серверных сообщений ru/en
internal/api            HTTP API на chi
internal/webui          вшитый веб-интерфейс
internal/tui            терминальный интерфейс на tview
web/                    React + TypeScript + antd, графики на голом SVG
```

Ключевое решение — интерфейс `collect.Collector`. Остальное приложение
работает с POSIX-путями хоста и не знает, читает оно настоящий сервер или
снапшот, поэтому парсеры и правила ведут себя одинаково везде.

Веб-интерфейс и TUI — равноправные потребители одних пакетов: оба
вызывают `inventory`, `analyze`, `topology` и `control` напрямую.
Исправление в правилах одинаково видно и там, и там.

Каждый рантайм (docker, podman, lxd, libvirt) — отдельный модуль: своя
модель, свой парсер, своя страница. Общей абстракции «workload» между
ними намеренно нет.

**Новый парсер**: файл в `internal/parse`, возвращающий
`model.Endpoint` / `model.Upstream` / `model.SourceStatus`, и вызов в
`internal/inventory/scan.go`. Карта, анализ, мониторинг и оба интерфейса
подхватят его сами.

Внешние зависимости: `nginx-go-crossplane`, `haproxytech/config-parser`,
`modernc.org/sqlite`, `go-chi/chi`, `rivo/tview`, `golang.org/x/crypto`,
`pkg/sftp`, `coder/websocket`, `creack/pty`, `hashicorp/yamux`,
`gopkg.in/yaml.v3`. Docker и Podman опрашиваются собственным минимальным
клиентом Engine API; LXD и libvirt — через их CLI (`lxc`, `virsh`).

## Сообщения и переводы

Интерфейс двуязычный: язык — по браузеру (русский → русский, любой
другой → английский), переключатель запоминает выбор. Фронтенд берёт
строки из `web/src/i18n/{ru,en}.json`.

Серверный текст, который доходит до интерфейса (ошибки API, журналы
заданий), пишется ключом каталога `internal/msgs` с переводами в `ru.go`
и `en.go`:

- ошибка — `msgs.Errorf("pkg.key", args...)` вместо `fmt.Errorf`; в
  каталоге вместо `%w` пишется `%v`, `errors.Is/As` всё равно работают;
- текст по месту — `msgs.Tc(ctx, "pkg.key", args...)`; у задания язык —
  автора (`jc.Log("pkg.key", …)`, `jc.Lang()`);
- на границе API — `writeErr(w, r, status, err)`: каталожная ошибка — на
  языке запроса, чужая (из `os`, `exec`) — как есть.

Ключ — `<пакет>.<смысл>` (`files.pathOutsideAllowed`). Вывод внешних
программ (nginx -t, apt, certbot, git) не переводится.

Что ещё держит переводы:

- текст, сохранённый в базу (причина ошибки хоста), — ключом: пишется
  через `store.HostError(err)`, на выдаче — `h.LocalizedError(lang)`;
  тексты снимка (находки, заметки, предупреждения) — через
  `model.LocalizeSnapshot`;
- `cmd/nkt/i18n_sweep_test.go` поднимает API хоста на фикстурах и API хаба
  и обходит все GET-маршруты на английском: кириллица в ответе —
  непереведённая строка, тест падает;
- журнал службы (slog) — только по-английски, ошибки каталога в нём тоже
  переводятся на английский (`englishErrors`);
- командная строка — ключи `cli.*` (`internal/msgs/{ru,en}_cli.go`), язык по
  `LANG`;
- Android-приложение — `t("рус", "eng")`, проверка — `I18nCoverageTest`.

## Сайт и скриншоты

Сайт (VitePress) — в `site/`, отдельный `package.json`:

```bash
cd site && npm ci && npm run dev      # http://localhost:5173/nkt/
npm run build                         # site/.vitepress/dist; ловит мёртвые ссылки
```

Публикуется на GitHub Pages workflow'ом `.github/workflows/pages.yml` при
push в `main`. Русский — корень, английский — `/en/`; первый заход
перебрасывает по языку браузера.

Скриншоты (`site/public/screens/{ru,en}`) снимает `scripts/screenshots.py`
через Chrome DevTools Protocol с двух стендов: nkt в режиме fixtures
(`NKT_MODE=fixtures` на `127.0.0.1:8077`, `NKT_DATA_DIR` — нейтральный
каталог вроде `/tmp/nkt-demo/host`, он виден на экране образов) и
демо-хаба, чьи хосты через локальный sshd ведут на тот же fixtures-nkt.
Демо-хаб заполняется `go run scripts/demo/seed.go`; sshd поднимается по
образцу `startTestSSHD` из `internal/hub`. Запуск — в шапке скрипта.

Демо-история доступности и нагрузки засеивается один раз на базу и
только при `NKT_SCHEDULER_ENABLED=true`: если графики опустели, стенд
запускается на свежей базе. Для экрана `job` нужно хоть одно задание —
например, `POST /api/lxd/instances?job=1`.
