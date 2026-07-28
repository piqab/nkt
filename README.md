# NetKnownsThat

Приложение для одного Linux-хоста: разбирает конфигурации **nginx**, **haproxy**,
**docker/compose**, **iptables** и **ufw**, сопоставляет их с тем, что реально
происходит на машине, строит **карту сетевых ресурсов**, ведёт **расписание
доступности и использования** и даёт **дашборд для управления** сервисами,
конфигами и правилами firewall.

Один статический бинарник со вшитым интерфейсом. На хосте не нужны ни Python,
ни Node, ни отдельные статические файлы.

---

## Что именно оно делает

### 1. Проверяет конфигурации

Конфиги не просто читаются, а сопоставляются друг с другом и с реальным
состоянием хоста: вывод `ss -tulpnH`, счётчики `iptables-save -c`, список
контейнеров из Docker Engine API.

Реализованные правила:

| Правило | Серьёзность | Что находит |
|---|---|---|
| `port-conflict` | high | Два сервиса объявляют один и тот же порт на пересекающихся адресах |
| `declared-not-listening` | high | Порт описан в конфиге, но никто его не слушает (конфиг не применён) |
| `listening-not-declared` | medium/info | Процесс слушает порт, не описанный ни в одном конфиге |
| `no-default-deny` | high | Политика INPUT — ACCEPT и ufw выключен |
| `public-port-blocked` | medium | Сервис слушает 0.0.0.0, но firewall его не пропускает |
| `docker-bypasses-firewall` | critical/high | Контейнер публикует порт на 0.0.0.0 — DNAT обходит INPUT и правила ufw |
| `stale-firewall-rule` | low | Правило разрешает порт, который никто не слушает |
| `sensitive-port-public` | critical/high | Redis, PostgreSQL, MongoDB и т. п. слушают все интерфейсы |
| `weak-tls` | medium | В `ssl_protocols` остались TLSv1 / TLSv1.1 |
| `missing-hsts` | low | TLS-сервер не отдаёт Strict-Transport-Security |
| `tls-cert-missing` | high | `listen ... ssl` без `ssl_certificate` |
| `public-plaintext-proxy` | medium | Публичный HTTP-слушатель проксирует трафик без шифрования |
| `upstream-undefined` | high | Маршрут ссылается на несуществующий пул |
| `upstream-orphan` | low | Пул объявлен, но нигде не используется |
| `upstream-member-down` | high | Локальный backend пула не слушает свой порт |
| `all-backends-disabled` | critical | Все серверы пула помечены down/backup |
| `single-backend` | info | В пуле один сервер — нет резерва |
| `backend-no-healthcheck` | medium | В пуле больше одного сервера и нет проверки здоровья |
| `container-restarting` | high | Контейнер в цикле перезапуска |
| `container-not-running` | medium | Описан в compose, но не запущен |
| `container-undeclared` | low | Запущен, но не описан ни в одном compose-файле |
| `container-no-restart-policy` | low | Не задана политика перезапуска |
| `admin-interface-open` | high/medium | Панель статистики haproxy доступна без пароля |

Каждая находка содержит объяснение, ссылку на файл и строку, и конкретное
действие для исправления.

### 2. Строит карту сетевых ресурсов

Граф, где трафик читается слева направо:

```
внешняя сеть → сервис → слушатель → пул → backend-адрес → контейнер → сеть docker
```

Связи берутся из конфигураций (`proxy_pass`, `upstream`, `use_backend`,
`default_backend`, публикация портов), а состояние узлов — из реальных
слушателей, состояния контейнеров и найденных проблем. Раскладка по колонкам,
а не силовая: она стабильна между сканами и читается как схема потока запросов.

### 3. Ведёт расписание доступности и использования

* **Доступность** — каждый объявленный слушатель и каждый backend пула
  проверяется по расписанию (TCP-коннект или HTTP-запрос с правильным
  заголовком `Host`). Из истории строится тепловая карта «час недели ×
  недоступность», графики доступности и задержки и список простоев.
* **Использование** — приросты счётчиков iptables, `docker stats` и разбор
  access-логов nginx и haproxy. Записи логов раскладываются по времени самой
  записи, поэтому график показывает, когда нагрузка была на самом деле.

### 4. Даёт управлять

* systemd: `start` / `stop` / `restart` / `reload`, проверка конфигурации.
* docker: `start` / `stop` / `restart` контейнеров через Engine API.
* Конфиги: редактор с версионированием. Перед записью содержимое проверяется
  самим сервисом (`nginx -t`, `haproxy -c -f`, `docker compose config -q`);
  **если проверка не прошла, файл автоматически возвращается в прежнее
  состояние**. Любую версию можно посмотреть, сравнить (unified diff) и откатить.
* firewall: добавление и удаление правил через `ufw`. Прямая правка iptables из
  веб-интерфейса намеренно не поддерживается.

Все изменения пишутся в журнал с указанием пользователя, результата и вывода
команды.

---

## Быстрый старт (Windows, без Linux-хоста)

Режим `fixtures` читает снапшот хоста из каталога `fixtures/host` — полноценный
пример production-сервера с намеренно заложенными проблемами. Работает на любой ОС.

```powershell
# 1. Собрать интерфейс (нужен один раз; результат вшивается в бинарник)
cd web
npm install
npm run build
cd ..

# 2. Собрать и запустить
go build -o nkt.exe ./cmd/nkt
$env:NKT_MODE = "fixtures"
.\nkt.exe
```

Открыть <http://127.0.0.1:8077>. Пароль администратора печатается в консоль при
первом запуске.

Разработка интерфейса с горячей перезагрузкой:

```powershell
.\nkt.exe                       # бэкенд на :8077
cd web; npm run dev             # фронтенд на :5173, проксирует /api на :8077
```

Разовая проверка без дашборда — печатает отчёт и завершается с кодом 2, если
есть критичные находки (годится для cron или CI):

```powershell
.\nkt.exe -scan
```

### Что означает «синтетические данные»

В режиме `fixtures` описанных сокетов на машине не существует, а счётчики в
снапшоте заморожены. Поэтому пробы и метрики **моделируются**, а история за
14 дней засевается один раз при первом запуске. Интерфейс сообщает об этом
баннером, а API отдаёт `"simulated": true`. Отключается через
`NKT_DEMO_BACKFILL=false`. В режиме `local` всё измеряется по-настоящему.

---

## Проверочный стенд на настоящих сервисах

```bash
docker compose -f stand/docker-compose.yml up --build
```

Поднимает настоящие nginx и haproxy с настоящими бэкендами и рядом —
NetKnownsThat в режиме `local`, который читает те же файлы конфигурации и
разговаривает с настоящим демоном docker. Открыть <http://127.0.0.1:8077>,
пароль — в логах контейнера `nkt`.

Стенд не покрывает systemd и firewall хоста: внутри контейнера их нет. Эти пути
проверяются только на настоящей Linux-машине.

---

## Установка на боевой Linux-хост

```bash
# Кросс-компиляция с Windows или сборка на месте
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --tags --always)" -o nkt ./cmd/nkt

install -m 0755 nkt /usr/local/bin/nkt
install -d -m 0750 /etc/netknownsthat
install -m 0640 deploy/nkt.env.example /etc/netknownsthat/nkt.env
$EDITOR /etc/netknownsthat/nkt.env

install -m 0644 deploy/netknownsthat.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now netknownsthat
journalctl -u netknownsthat -n 30      # здесь будет пароль администратора
```

По умолчанию приложение слушает `127.0.0.1:8077`. Наружу его следует отдавать
только через reverse proxy с TLS и выставить `NKT_COOKIE_SECURE=true`.

---

## Архитектура

```
cmd/nkt                 точка входа, graceful shutdown
internal/config         все настройки из NKT_* переменных
internal/collect        ЕДИНСТВЕННОЕ место, где расходятся fixtures и реальный хост
  ├── local.go          настоящая ФС, exec, unix-сокет docker
  └── fixtures.go       снапшот на диске, заготовленные ответы команд
internal/parse          nginx (crossplane), haproxy (config-parser), docker,
                        iptables, ufw, ss, systemd
internal/model          вендор-нейтральное описание найденного
internal/analyze        правила поиска проблем
internal/topology       построение графа ресурсов
internal/inventory      оркестрация скана, снапшоты, цели мониторинга
internal/monitor        пробы, метрики, access-логи, планировщик
internal/control        сервисы, правка конфигов с версиями, firewall
internal/store          SQLite (modernc, чистый Go — статическая сборка)
internal/api            HTTP API на chi
internal/webui          вшитый фронтенд
web/                    React + TypeScript, графики на голом SVG
```

Ключевое решение — интерфейс `collect.Collector`. Всё остальное приложение
работает с POSIX-путями хоста и не знает, читает оно настоящий сервер или
снапшот, поэтому парсеры и правила одинаково работают и на Windows, и на
боевой машине.

Внешние зависимости: `nginx-go-crossplane` (официальный парсер nginx),
`haproxytech/config-parser` (парсер из HAProxy Dataplane API), `modernc.org/sqlite`,
`go-chi/chi`, `golang.org/x/crypto` (argon2id), `gopkg.in/yaml.v3`.
Docker опрашивается собственным минимальным клиентом Engine API — вместо
трёхсот пакетов официального SDK.

---

## API

Всё под `/api`, аутентификация — по cookie-сессии.

| Метод | Путь | Роль | Назначение |
|---|---|---|---|
| POST | `/auth/login`, `/auth/logout`, `/auth/password` | — / любая | Вход, выход, смена своего пароля |
| GET | `/overview` | viewer | Сводка для дашборда одним запросом |
| GET | `/inventory`, `/findings`, `/topology` | viewer | Полный снапшот, проблемы, граф |
| GET | `/services`, `/containers`, `/firewall` | viewer | Состояние сервисов, контейнеров, правил |
| GET | `/configs`, `/configs/file`, `/configs/versions*` | viewer | Файлы, содержимое, история, diff |
| GET | `/monitor/targets`, `/monitor/heatmap`, `/monitor/outages`, `/monitor/usage*` | viewer | Доступность и нагрузка |
| GET | `/audit`, `/monitor/jobs`, `/snapshots` | viewer | Журнал, фоновые задачи, история сканов |
| POST | `/inventory/refresh` | admin | Пересканировать хост |
| POST | `/services/{name}/{action}` | admin | start / stop / restart / reload |
| POST | `/containers/{name}/{action}` | admin | Управление контейнером |
| PUT | `/configs/file` | admin | Правка с проверкой и автооткатом |
| POST | `/configs/versions/{id}/rollback` | admin | Откат к версии |
| POST/DELETE | `/firewall/rules` | admin | Добавить / удалить правило ufw |
| GET/POST/PATCH/DELETE | `/users*` | admin | Управление учётными записями |

---

## Безопасность

* Пароли — argon2id (64 МиБ, t=3, p=2). Сессии — случайные токены в SQLite,
  отзываемые; cookie `HttpOnly`, `SameSite=Lax`.
* Роли: `viewer` — только чтение, `admin` — изменения. Плюс глобальный
  выключатель `NKT_ALLOW_MUTATIONS=false`, переводящий всё приложение в режим
  чтения.
* Защита от перебора: после пяти неудачных попыток вход по учётной записи
  блокируется с нарастающей задержкой.
* Правка конфигов ограничена белым списком каталогов (корни nginx и haproxy,
  перечисленные compose-файлы). Пути с `..` отвергаются.
* Действия над сервисами и firewall принимают только значения из фиксированных
  списков — имя сервиса или действие невозможно подставить в команду.
* Удаление правила ufw сверяется с текстом, который видел оператор: номера
  правил сдвигаются после каждого изменения, и удаление «не того» правила
  способно отрезать SSH.
* Оптимистичная блокировка при сохранении конфига по SHA-256: файл, изменившийся
  на диске после открытия в редакторе, не будет перезаписан молча.
* Каждое изменяющее действие пишется в `audit_log`.

**Доступ к этому приложению равносилен root на хосте.** Слушайте только
localhost или отдавайте через reverse proxy с TLS и отдельной аутентификацией.

---

## Ограничения

* Один хост. Нет ни агентов, ни сбора с нескольких машин.
* Пишутся только правила `ufw`; прямая правка iptables не поддерживается
  намеренно.
* Из firewall-бэкендов поддержаны iptables/ip6tables и ufw. Чистый `nftables`
  (без слоя iptables-nft) не разбирается.
* Правило `declared-not-listening` требует доступного `ss`; без него сравнивать
  конфиг не с чем, и правило молчит.
* Проверка конфигурации в режиме `fixtures` всегда «успешна» — путь отказа
  валидации проверяется на стенде или на настоящем хосте.
* Из веб-серверов и балансировщиков поддержаны nginx и haproxy. Caddy, Traefik,
  Envoy не разбираются — под них нужен новый файл в `internal/parse`.

---

## Разработка

```powershell
go test ./...                  # парсеры и полный скан против снапшота
go vet ./...
gofmt -l .
cd web; npm run typecheck
```

Тесты работают против `fixtures/host` — снапшота, в который намеренно заложены
конфликт портов, открытый наружу Redis, контейнер в цикле перезапуска, панель
статистики без пароля, устаревший TLS и неиспользуемое правило firewall.
Тест `TestScanFindsPlantedProblems` проверяет, что анализатор находит каждую из
этих проблем с ожидаемой серьёзностью.

Добавление нового парсера: новый файл в `internal/parse`, возвращающий
`model.Endpoint` / `model.Upstream` / `model.SourceStatus`, и вызов в
`internal/inventory/scan.go`. Всё остальное — карта, анализ, мониторинг,
интерфейс — подхватит его автоматически.
