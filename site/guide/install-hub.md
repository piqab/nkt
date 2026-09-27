---
title: Установка хаба
---

# Установка хаба

`nkt hub` — тот же бинарник в другом режиме. Хаб ставится на одну машину
и оттуда по SSH устанавливает и обслуживает nkt на остальных хостах;
каждый хост открывается в интерфейсе хаба той же панелью, что у
одиночного nkt. Что хаб умеет — на странице [Хаб](/guide/hub).

По умолчанию хаб — обычная systemd-служба с тем же принципом наименьших
привилегий, что у nkt. Docker и Kubernetes — самодостаточные
альтернативы.

## Что нужно

- **Машина для хаба** — Linux. Хаб сам сканирует и правит эту же машину
  (строка «localhost» в списке хостов), поэтому юниту нужен тот же профиль
  прав, что у nkt: root с ограниченным набором capabilities. Go ставить
  не обязательно: если хаб собирает бинарники из исходников, а системного
  `go` нет, он сам скачает Go с go.dev в свой каталог данных.
- **SSH с хаба до каждого хоста**: адрес, порт, пользователь `root` или с
  `sudo` без пароля, пароль или ключ. Установка кладёт файлы в
  `/usr/local/bin`, `/etc/systemd/system`, `/etc/netknownsthat`.
- **Хосты** — Linux amd64, arm64 или 32-битный ARM (Raspberry Pi —
  armv6l/armv7l). Заранее ставить на них ничего не нужно. Архитектура
  самого хаба не важна: бинарник для хоста собирается или скачивается
  под его архитектуру.
- **Белый адрес хабу не нужен.** Хаб сам ходит к хостам; входящие
  соединения ему нужны только от вашего браузера (и от CI, если вебхуки
  идут напрямую — см. [nkt-edge](/guide/edge)).

## Вариант 1. systemd из готового бинарника

Бинарник — как для [хоста](/guide/getting-started#_1-бинарник), юнит и
env-файл свои:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/hub.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/hub.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat-hub.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat-hub.service
sudo $EDITOR /etc/netknownsthat/hub.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat-hub
sudo journalctl -u netknownsthat-hub -n 30     # пароль администратора
```

Исходников на такой машине нет, поэтому бинарник nkt для нового хоста
хаб скачивает с GitHub Releases той же версии, что у него самого, и
сверяет `SHA256SUMS`. Нужен исходящий доступ к `github.com`.

## Вариант 2. systemd из исходников

Хаб кросс-компилирует nkt под каждый хост из клона репозитория — так он
работает без доступа к GitHub и с любой версией, даже не выпущенной:

```bash
git clone https://github.com/piqab/nkt.git /opt/netknownsthat
cd /opt/netknownsthat
make build                    # или make native-build без Docker
sudo make hub-install
sudo systemctl enable --now netknownsthat-hub
```

`hub-install` кладёт бинарник в `/usr/local/bin/nkt`, юнит
`netknownsthat-hub.service` и заготовку `/etc/netknownsthat/hub.env`, где
`NKT_HUB_SOURCE_ROOT` уже указывает на `/opt/netknownsthat`. Клон лежит вне
каталога данных, обновляйте его вместе с хабом:
`git pull && make build && sudo make hub-install`. Сборка кэшируется —
второй хост той же архитектуры ставится без пересборки.

Проверить, что поднялось:

```bash
curl -s http://127.0.0.1:8077/api/health
journalctl -u netknownsthat-hub -f
```

## Вариант 3. Docker Compose

Готовый образ `ghcr.io/piqab/nkt-hub` публикуется под каждый релиз,
`docker login` не нужен:

```bash
curl -fsSLO https://raw.githubusercontent.com/piqab/nkt/main/deploy/docker-compose.hub.release.yml
docker compose -f docker-compose.hub.release.yml up -d
docker compose -f docker-compose.hub.release.yml logs hub    # пароль администратора
curl -s http://127.0.0.1:8443/api/health
```

- Порт интерфейса — `8443` на хосте (внутри контейнера `8077`, хаб
  слушает `0.0.0.0`, иначе публикация порта не сработает); `8446` —
  [пробросы портов](/guide/ports#_8446-пробросы-портов-kubernetes).
  Перед ним нужен свой обратный прокси с TLS, если это не только
  локальная проверка.
- Данные — в томе `nkt-hub-data` (`/var/lib/netknownsthat` в контейнере):
  база, ключ шифрования, кэш.
- Хаб в контейнере работает **не от root** (пользователь `nkt`,
  uid 1000). Том от старой версии, созданный root, нужно один раз отдать
  этому пользователю — хаб при старте выйдет с понятной ошибкой и
  командой:
  ```bash
  docker run --rm -v nkt-hub-data:/data alpine chown -R 1000:1000 /data
  ```
  (имя тома — в `docker volume ls`).
- К каждому релизу приложен `docker-compose.hub.release.yml` с **точной
  версией** образа вместо `:latest` (сумма — в `SHA256SUMS`): мутабельный
  тег можно переставить, а том хранит мастер-ключ и секреты хостов.
- Сборка из исходников тем же compose: `make hub` (файл
  `deploy/docker-compose.hub.yml`).
- Строка «localhost» в контейнере показывает сам контейнер, а не машину,
  на которой он работает.

## Вариант 4. Kubernetes

Манифест — [`deploy/k8s/hub.yaml`](https://github.com/piqab/nkt/blob/main/deploy/k8s/hub.yaml):
Namespace, PersistentVolumeClaim, Deployment и Service (порты 8077 и
8446).

```bash
kubectl apply -f https://raw.githubusercontent.com/piqab/nkt/main/deploy/k8s/hub.yaml
kubectl logs -n netknownsthat deploy/nkt-hub       # пароль администратора
kubectl port-forward -n netknownsthat svc/nkt-hub 8443:8077
```

- **Строго одна реплика**: реестр хостов — SQLite на томе ReadWriteOnce,
  ключ шифрования генерируется при первом старте; второй под с тем же
  томом не согласуется с первым. Стратегия — `Recreate`.
- `fsGroup: 1000` отдаёт том пользователю хаба сам.
- Доступ снаружи — свой Ingress с TLS перед Service (в манифест не
  включён: у каждого кластера свой контроллер).
- Резервный канал к хостам и кэш пакетов работают без настройки сети:
  хаб сам дозванивается из пода обычным исходящим трафиком.
- К релизу приложен `k8s-hub.yaml` с точной версией образа.

## Первый вход

При первом старте хаб создаёт администратора и печатает пароль в журнал:

```
=== Hub admin account created ===
  username: admin
  password: <string>
```

Больше он нигде не показывается — сохраните. Задать заранее —
`NKT_BOOTSTRAP_ADMIN_USER` и `NKT_BOOTSTRAP_ADMIN_PASSWORD` в `hub.env`.
Как открыть интерфейс снаружи — те же три способа, что у
[хоста](/guide/getting-started#_4-открыть-в-браузере): SSH-туннель,
`NKT_TLS_ENABLED=true` или обратный прокси.

## Строка «localhost»

Первой в списке хостов всегда стоит **localhost** — машина, на которой
работает хаб. Она сканируется тем же кодом, что у одиночного nkt, без SSH
и без отдельной установки, и открывается сразу после входа. Поэтому
`netknownsthat-hub.service` запускается с тем же профилем прав, что
`netknownsthat.service` (root, `CapabilityBoundingSet`, запись в
`/etc/nginx`, `/etc/haproxy`, `/etc/caddy`, `/etc/letsencrypt`); без этих
прав (старый юнит) она почти ничего не увидит. У неё нет установки, удаления и SSH-полей —
единственное действие «открыть».

## Обновление хаба

- **systemd** — «О системе» → «Обновить до vX.Y.Z»: хаб качает бинарник и
  юнит того же тега, сверяет суммы и перезапускается (подробно — в
  [Обновлениях](/guide/hub-updates)). Или вручную, как при установке.
- **Docker** — `docker compose pull && docker compose up -d`.
- **Kubernetes** — новый тег образа в Deployment.

Хосты после обновления хаба приводятся к его версии: при открытии
отставшего хоста или кнопкой «обновить всё».

## Остановить и удалить

```bash
# systemd
sudo systemctl disable --now netknownsthat-hub

# Docker
docker compose -f docker-compose.hub.release.yml down      # том остаётся
docker compose -f docker-compose.hub.release.yml down -v   # стереть данные хаба
```

Остановка хаба не трогает nkt на хостах: он продолжит работать как
обычная служба; убрать его — `systemctl disable --now netknownsthat` на
самом хосте.

`down -v` удаляет файлы, но их остатки можно восстановить с диска или
снапшота. Для безвозвратного удаления ключа шифрования и секретов всех
хостов есть отдельная команда:

```bash
sudo nkt hub delete
```

Она спросит, сохранить ли экспорт перед удалением, и **обязательно**
предложит зашифровать его паролем (AES-256-GCM, ключ из пароля через
PBKDF2 — тот же формат, что у экспорта из интерфейса). Затем —
подтверждение словом «удалить», после чего команда:

1. останавливает и отключает `netknownsthat-hub` (если юнита нет —
   пропускает);
2. **затирает** (перезаписывает случайными данными, потом удаляет) ключ
   шифрования, базу со всеми секретами, историю конфигов и TLS-ключ; кэш
   бинарников и Go просто удаляет — в них нет секретов.

Бинарник и юнит остаются: `sudo systemctl start netknownsthat-hub` сразу
поднимает новый чистый хаб. Без терминала (`-yes` или не TTY) нужно явно
указать `-export <файл>` или `-no-export`; пароль экспорта — только из
`NKT_HUB_EXPORT_PASSWORD` (флагом его передавать нельзя — виден в `ps`).

Восстановить на новом хабе:

```bash
sudo nkt hub import -file nkt-hub-export.json
```

Зашифрованный файл распознаётся сам, пароль спрашивается (или берётся из
`NKT_HUB_EXPORT_PASSWORD`); расшифровка идёт только в памяти.

::: warning Оговорка про затирание
На SSD (выравнивание износа) и на copy-on-write файловых системах
(btrfs, ZFS) старые блоки могут пережить перезапись. Затирание защищает
от обычного восстановления удалённых файлов, но не от лаборатории с
физическим доступом к носителю.
:::
