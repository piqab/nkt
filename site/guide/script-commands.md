---
title: Команды сценариев
---

<!-- Страница собирается из кода nkt (internal/script): не правьте её вручную — NKT_UPDATE_DOCS=1 go test ./internal/script -run TestScriptCommandsPage -->

# Команды сценариев

Все команды языка сценариев хаба — те же, что знают «Проверить», вкладка «Справка» и ИИ: страница собирается из описания команд в коде nkt и не отстаёт от него. Как сценарии хранятся, проверяются и запускаются — [Сценарии](/guide/hub-scripts).

## Коротко

**Переменные и вопросы при запуске**

| Команда | Что делает |
|---|---|
| [`set ИМЯ значение`](#cmd-set) | Переменная: дальше по тексту ${ИМЯ} заменяется значением. |
| [`param ИМЯ "подсказка"`](#cmd-param) | Параметр: значение спрашивают при запуске, дальше ${ИМЯ} заменяется им. |

**Хаб: группы, хосты, установка, ожидание**

| Команда | Что делает |
|---|---|
| [`group ИМЯ`](#cmd-group) | Создаёт группу хостов на хабе; с профилем — машины, создаваемые в ней, делаются по нему. |
| [`host ИМЯ АДРЕС[:ПОРТ] user ПОЛЬЗОВАТЕЛЬ`](#cmd-host) | Заводит хост на хабе по адресу и учётным данным SSH — то же, что «Добавить хост». |
| [`install ХОСТ…`](#cmd-install) | Ставит nkt на перечисленные хосты и ждёт, пока каждый ответит. |
| [`wait ХОСТ online`](#cmd-wait) | Ждёт, пока хост станет «в сети» (после установки или перезагрузки). |

**На хосте (on ХОСТ …)**

| Команда | Что делает |
|---|---|
| [`on ХОСТ… packages install\|remove ПАКЕТ…`](#cmd-packages) | Ставит или удаляет пакеты apt на хосте одной транзакцией. |
| [`on ХОСТ service ИМЯ start\|stop\|restart\|reload\|enable\|disable`](#cmd-service) | Управляет службой systemd: запуск, остановка, перезапуск, перечитывание, автозапуск. |
| [`on ХОСТ firewall allow\|deny ПОРТ[/tcp\|udp]`](#cmd-firewall) | Правило ufw/firewalld: разрешить или запретить порт, при желании — только с адреса. |
| [`on ХОСТ docker install`](#cmd-docker-install) | Ставит Docker с официального репозитория docker.com. |
| [`on ХОСТ docker stack ПУТЬ up\|down\|start\|stop`](#cmd-docker-stack) | Поднимает, гасит, запускает или останавливает compose-стек. |
| [`on ХОСТ vm create ИМЯ image …`](#cmd-vm-create) | Создаёт виртуальную машину на хосте из облачного образа — как окно «Новая машина». |
| [`on ХОСТ vm start\|shutdown\|destroy ИМЯ`](#cmd-vm-action) | Запускает, выключает или принудительно гасит машину на хосте. |
| [`on ХОСТ apply profile ПРОФИЛЬ`](#cmd-apply) | Применяет профиль хаба к хосту целиком: строит план и выполняет все пункты. |
| [`on ХОСТ file put ПУТЬ`](#cmd-file-put) | Записывает файл на хост из блока (через редактор конфигураций: с проверкой и историей, если путь ему знаком). |
| [`on ХОСТ user add ИМЯ`](#cmd-user-add) | Заводит учётную запись на хосте с SSH-ключом и, при желании, sudo без пароля. |
| [`on ХОСТ system hostname ИМЯ`](#cmd-system) | Системные настройки: имя машины, часовой пояс, основная локаль (генерируется, если нужно), серверы NTP с немедленной сверкой. |
| [`on ХОСТ cert issue ДОМЕН…`](#cmd-cert) | Сертификат Let's Encrypt через certbot --standalone: службы и процессы на 80/443 останавливаются на время проверки и запускаются снова. |
| [`on ХОСТ git clone URL /КАТАЛОГ`](#cmd-git-clone) | Клонирует репозиторий на хост заданием проводника; приватные — токеном, спрошенным при запуске, или deploy-ключом хоста. |
| [`on ХОСТ k8s create ИМЯ`](#cmd-k8s-create) | Создаёт кластер Kubernetes на виртуалках хоста — как «новый кластер» в списке хостов: машины из облачного образа с nkt, control plane, присоединение узлов, ожидание Ready, проброс и kubeconfig (скачивается на хабе в карточке кластера). |
| [`on ХОСТ k8s destroy ИМЯ`](#cmd-k8s-destroy) | Удаляет кластер: все его машины с дисками и запись на хабе. |

**Конвейеры, выкладки, переносы**

| Команда | Что делает |
|---|---|
| [`pipeline ИМЯ`](#cmd-pipeline) | Сохраняет конвейер выкладки — как «Сохранить» в окне конвейера: нет такого — создаёт, есть — новая версия описания. |
| [`deploy КОНВЕЙЕР`](#cmd-deploy) | Выкладывает конвейер — как кнопка «Выложить» — и ждёт итога; журнал выкладки идёт в журнал сценария. |
| [`migrate ХОСТ КОНТЕЙНЕР to ХОСТ`](#cmd-migrate) | Переносит стек на другой хост — как перетаскивание на карте хостов: план (препятствия — ошибка до остановки стека), остановка, данные через хаб, запуск на новом; упавший перенос откатывается сам. |

**Кластеры и Traffic Fly**

| Команда | Что делает |
|---|---|
| [`k8s КЛАСТЕР ingress cilium`](#cmd-k8s-ingress) | Включает Ingress-контроллер Cilium в режиме host network: Envoy слушает порт на каждом узле (без LoadBalancer), Cilium — контроллер по умолчанию; версия Cilium не меняется. |
| [`k8s КЛАСТЕР apply`](#cmd-k8s-apply) | Применяет манифест (kubectl apply) на первом control plane — как «Манифесты» раздела «Кластеры»; в сухом прогоне — kubectl diff. |
| [`k8s КЛАСТЕР wait deployment ИМЯ`](#cmd-k8s-wait) | Ждёт, пока все реплики deployment готовы. |
| [`fly route ИД ВИД`](#cmd-fly-route) | Создаёт или обновляет маршрут Traffic Fly и применяет его — как блок fly: конвейера: новый записывается и применяется, у существующего цели меняются плавно (новые добавляются и проверяются, прежние выводятся). |
| [`fly apply`](#cmd-fly-apply) | Применяет карту Traffic Fly — как «Применить»; в сухом прогоне — сухой прогон применения. |

## Команды

### `set ИМЯ значение | set ИМЯ ask` {#cmd-set}

Переменная: дальше по тексту ${ИМЯ} заменяется значением.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя — латиница, цифры, подчёркивание |
| `значение` | ✓ | всё до конца строки |
| `ask` |  | «set ИМЯ ask» — значение спросят при запуске и не запишут ни в текст, ни в журнал (пароли, токены) |

Пример:

```
set IMAGE ubuntu-24.04
set DB_PASS ask
on web1 vm create app1 image ${IMAGE}
```

### `param ИМЯ "подсказка"` {#cmd-param}

Параметр: значение спрашивают при запуске, дальше ${ИМЯ} заменяется им. Так один сценарий разворачивает разные стенды.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя — латиница, цифры, подчёркивание |
| `подсказка` |  | что показать в вопросе |

Пример:

```
param ADDR "адрес нового сервера"
param NAME "имя хоста в хабе"
host ${NAME} ${ADDR} user root password ask
```

### `group ИМЯ [profile ПРОФИЛЬ]` {#cmd-group}

Создаёт группу хостов на хабе; с профилем — машины, создаваемые в ней, делаются по нему.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя группы |
| `profile` |  | профиль хаба по имени |

Пример:

```
group web-farm profile web-base
```

### `host ИМЯ АДРЕС[:ПОРТ] user ПОЛЬЗОВАТЕЛЬ (password ask | password "…" | key hub) [group ГРУППА]` {#cmd-host}

Заводит хост на хабе по адресу и учётным данным SSH — то же, что «Добавить хост».

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя хоста в хабе; дальше на него ссылаются по нему |
| `АДРЕС` | ✓ | IP или DNS-имя, порт через двоеточие (по умолчанию 22) |
| `user` | ✓ | пользователь SSH (root или с sudo) |
| `password ask` |  | пароль спросят при запуске сценария; в текст он не попадает |
| `password "…"` |  | пароль прямо в тексте — только для стендов: сценарий хранится открытым |
| `key hub` |  | вход ключом хаба; его публичную часть на хост кладут заранее |
| `group` |  | группа хоста (должна быть выше строкой «group» или уже существовать) |

Пример:

```
host web1 192.0.2.10 user root password ask group web-farm
host web2 192.0.2.11:2222 user deploy key hub
```

### `install ХОСТ…` {#cmd-install}

Ставит nkt на перечисленные хосты и ждёт, пока каждый ответит.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ХОСТ` | ✓ | один или несколько хостов |

Пример:

```
install web1 web2
```

### `wait ХОСТ online [ДЛИТЕЛЬНОСТЬ]` {#cmd-wait}

Ждёт, пока хост станет «в сети» (после установки или перезагрузки).

```
wait ХОСТ online [ДЛИТЕЛЬНОСТЬ]
wait ХОСТ port ПОРТ [ДЛИТЕЛЬНОСТЬ]
wait ХОСТ http URL [КОД] [ДЛИТЕЛЬНОСТЬ]
wait ХОСТ service ИМЯ active [ДЛИТЕЛЬНОСТЬ]
wait url URL [КОД] [ДЛИТЕЛЬНОСТЬ]
```

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ХОСТ` | ✓ | хост |
| `online` |  | хост отвечает хабу (после установки или перезагрузки) |
| `port` |  | порт хоста принимает соединения — проверяется с хаба, снаружи |
| `http` |  | адрес отвечает кодом (по умолчанию 200); имя из адреса идёт в Host, сертификат не проверяется |
| `service` |  | служба systemd в состоянии active |
| `url` |  | url URL — адрес снаружи: запрос с хаба по настоящему имени (DNS, шлюз, сертификат проверяются), ждать ответа с этим кодом; хост не нужен |
| `ДЛИТЕЛЬНОСТЬ` |  | сколько ждать: 90s, 5m (по умолчанию 5m) |

Пример:

```
wait web1 online 5m
wait web1 port 443 30s
wait web1 http https://example.org/ 200 1m
wait web1 service nginx active
wait url https://git.example.com/api/healthz 200 10m
```

### `on ХОСТ… packages install|remove ПАКЕТ…` {#cmd-packages}

Ставит или удаляет пакеты apt на хосте одной транзакцией.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `install\|remove` | ✓ | install — поставить, remove — удалить |
| `ПАКЕТ` | ✓ | имена пакетов через пробел |

Пример:

```
on web1 packages install nginx htop
```

### `on ХОСТ service ИМЯ start|stop|restart|reload|enable|disable` {#cmd-service}

Управляет службой systemd: запуск, остановка, перезапуск, перечитывание, автозапуск.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя юнита без .service |
| `действие` | ✓ | start, stop, restart, reload, enable, disable |

Пример:

```
on web1 service nginx restart
```

### `on ХОСТ firewall allow|deny ПОРТ[/tcp|udp] [from CIDR]` {#cmd-firewall}

Правило ufw/firewalld: разрешить или запретить порт, при желании — только с адреса.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `allow\|deny` | ✓ | allow — разрешить, deny — запретить |
| `ПОРТ` | ✓ | порт и протокол: 443, 443/tcp, 53/udp |
| `from` |  | источник: IP или сеть 10.0.0.0/24 |

Пример:

```
on web1 firewall allow 443/tcp
on web1 firewall allow 5432 from 10.0.0.0/24
```

### `on ХОСТ docker install` {#cmd-docker-install}

Ставит Docker с официального репозитория docker.com.

Пример:

```
on web1 docker install
```

### `on ХОСТ docker stack ПУТЬ up|down|start|stop` {#cmd-docker-stack}

Поднимает, гасит, запускает или останавливает compose-стек. Без блока up поднимает файл, который уже лежит на хосте; с блоком (YAML compose до «end») — сначала записывает его по указанному пути. start поднимает то, что есть на хосте, не обращаясь в registry (остановленный контейнер с закрытым образом поднимется без ключа); stop останавливает, ничего не удаляя. down, start и stop блока не берут. Стек, выложенный конвейером, запускают и останавливают командой deploy КОНВЕЙЕР start|stop — «Проверить» об этом напомнит.

```
on ХОСТ docker stack ПУТЬ up|down|start|stop
[compose-файл построчно
end]
```

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ПУТЬ` | ✓ | полный путь к docker-compose.yml |
| `up\|down\|start\|stop` | ✓ | up — поднять (скачав образы, если нужно), down — остановить и убрать, start — запустить имеющееся без registry, stop — остановить, оставив контейнеры |

Пример:

```
on web1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
end
```

### `on ХОСТ vm create ИМЯ image ОБРАЗ [cpu N] [mem МБ] [disk ГБ] [user ИМЯ] [network СЕТЬ] [profile ПРОФИЛЬ] [install]` {#cmd-vm-create}

Создаёт виртуальную машину на хосте из облачного образа — как окно «Новая машина».

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя машины; она же появится хостом в хабе |
| `image` | ✓ | образ из каталога хоста (ubuntu-24.04, debian-12…) |
| `cpu / mem / disk` |  | ядра, память в МБ, диск в ГБ |
| `user` |  | пользователь внутри машины (по умолчанию deploy) |
| `network` |  | сеть libvirt (по умолчанию первая существующая) |
| `profile` |  | профиль, который применить к машине после установки nkt |
| `install` |  | поставить в машину nkt (нужно для profile и для «on ИМЯ …» дальше) |

Пример:

```
on web1 vm create app1 image ubuntu-24.04 cpu 2 mem 2048 disk 20 profile app install
```

### `on ХОСТ vm start|shutdown|destroy ИМЯ` {#cmd-vm-action}

Запускает, выключает или принудительно гасит машину на хосте.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `действие` | ✓ | start, shutdown, destroy |
| `ИМЯ` | ✓ | имя машины |

Пример:

```
on web1 vm start app1
```

### `on ХОСТ apply profile ПРОФИЛЬ` {#cmd-apply}

Применяет профиль хаба к хосту целиком: строит план и выполняет все пункты.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ПРОФИЛЬ` | ✓ | профиль хаба по имени |

Пример:

```
on web2 apply profile web-base
```

### `on ХОСТ file put ПУТЬ [mode 0644]` {#cmd-file-put}

Записывает файл на хост из блока (через редактор конфигураций: с проверкой и историей, если путь ему знаком).

```
on ХОСТ file put ПУТЬ [mode 0644]
  [содержимое построчно]
end
```

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ПУТЬ` | ✓ | полный путь к файлу |
| `mode` |  | права, восьмеричные (по умолчанию 0644) |

Пример:

```
on web1 file put /etc/motd
Этот сервер под управлением nkt
end
```

### `on ХОСТ user add ИМЯ [sudo] [key "ssh-ed25519 …"]` {#cmd-user-add}

Заводит учётную запись на хосте с SSH-ключом и, при желании, sudo без пароля.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя пользователя |
| `sudo` |  | выдать sudo без пароля |
| `key` |  | публичный ключ SSH в кавычках |

Пример:

```
on web1 user add deploy sudo key "ssh-ed25519 AAAAC3Nza… deploy@laptop"
```

### `on ХОСТ system hostname ИМЯ | timezone ЗОНА | locale ЛОКАЛЬ | ntp СЕРВЕР…` {#cmd-system}

Системные настройки: имя машины, часовой пояс, основная локаль (генерируется, если нужно), серверы NTP с немедленной сверкой.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `hostname` |  | имя машины (RFC 1123) |
| `timezone` |  | часовой пояс вида Europe/Moscow |
| `locale` |  | локаль вида ru_RU.UTF-8 |
| `ntp` |  | один или несколько серверов NTP |

Пример:

```
on web1 system hostname web1
on web1 system timezone Europe/Moscow
on web1 system locale ru_RU.UTF-8
on web1 system ntp time.google.com pool.ntp.org
```

### `on ХОСТ cert issue ДОМЕН… | cert renew ДОМЕН` {#cmd-cert}

Сертификат Let's Encrypt через certbot --standalone: службы и процессы на 80/443 останавливаются на время проверки и запускаются снова.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `issue` |  | выпустить новый сертификат на перечисленные имена |
| `renew` |  | продлить существующую линейку certbot |

Пример:

```
on web1 cert issue example.org www.example.org
on web1 cert renew example.org
```

### `on ХОСТ git clone URL /КАТАЛОГ [branch ВЕТКА] [token ask | key hub]` {#cmd-git-clone}

Клонирует репозиторий на хост заданием проводника; приватные — токеном, спрошенным при запуске, или deploy-ключом хоста.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `URL` | ✓ | https://… или git@host:path |
| `/КАТАЛОГ` | ✓ | куда клонировать — полный путь к каталогу |
| `branch` |  | ветка (по умолчанию — основная) |
| `token ask` |  | токен или пароль HTTPS спросят при запуске |
| `key hub` |  | deploy-ключ хоста по SSH (его публичная часть — в «Диски → Файлы») |

Пример:

```
on web1 git clone https://github.com/org/app.git /srv/app branch main token ask
```

### `on ХОСТ k8s create ИМЯ [flavor k3s|kubeadm] [nodes single|1+N|3+N|"ХОСТ: cp N, w N, host cp|w, bridge МОСТ, endpoint АДРЕС; …"] [image ОБРАЗ] [cpu N] [mem МБ] [disk ГБ] [network СЕТЬ|nat|bridge|wireguard] [bridge МОСТ] [cni cilium|flannel] [no-kube-proxy] [version 1.34] [expose] [api ПОРТ] [http ПОРТ] [https ПОРТ]` {#cmd-k8s-create}

Создаёт кластер Kubernetes на виртуалках хоста — как «новый кластер» в списке хостов: машины из облачного образа с nkt, control plane, присоединение узлов, ожидание Ready, проброс и kubeconfig (скачивается на хабе в карточке кластера).

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя кластера — оно же группа и префикс имён машин (строчные буквы, цифры, дефис) |
| `flavor` |  | k3s (по умолчанию) или kubeadm |
| `nodes` |  | single — одна машина; 1+N — control plane и N worker (по умолчанию 1+1); 3+N — три control plane (только k3s); в кавычках — размещение по хостам, как в разделе «Кластеры»: «ХОСТ: cp N, w N, host cp\|w, bridge МОСТ, endpoint АДРЕС; ХОСТ2: …» (cp/w N — машины с ролью, host — сам хост как узел, endpoint — адрес хоста для соседей по туннелю WireGuard) |
| `image` |  | образ из каталога хоста (по умолчанию ubuntu-24.04) |
| `cpu / mem / disk` |  | размеры каждой машины: ядра, память в МБ, диск в ГБ (2 / 4096 / 30) |
| `network / bridge` |  | на одном хосте — имя сети libvirt; при размещении по хостам — режим сети между хостами: nat (один хост), bridge (мост хоста: bridge МОСТ для всех или «bridge …» в строке хоста) или wireguard (туннель, хаб настроит сам) |
| `cni` |  | cilium — Cilium вместо flannel (ставится после control plane); flannel — как у варианта по умолчанию |
| `no-kube-proxy` |  | с cilium: заменить kube-proxy на Cilium (eBPF) |
| `version` |  | минорная версия Kubernetes: у kubeadm — ветка pkgs.k8s.io, у k3s — канал v1.36; по умолчанию — актуальная stable |
| `expose` |  | пробросить порты 6443, 80, 443 хоста в control plane |
| `api / http / https` |  | порты хоста при expose: api (6443), http (80), https (443); 0 — не пробрасывать |

Пример:

```
on hv1 k8s create lab nodes "hv1: cp 1, w 2; hv2: w 2; hv3: host w" network wireguard cni cilium expose api 16443
```

### `on ХОСТ k8s destroy ИМЯ` {#cmd-k8s-destroy}

Удаляет кластер: все его машины с дисками и запись на хабе.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя кластера на этом хосте |

Пример:

```
on hv1 k8s destroy lab
```

### `pipeline ИМЯ` {#cmd-pipeline}

Сохраняет конвейер выкладки — как «Сохранить» в окне конвейера: нет такого — создаёт, есть — новая версия описания. Значения блока env уходят в секреты конвейера, generate: создаёт хаб (у уже заданных — пропуск); значение, отличное от уже заданного, не заменяется — ошибка.

```
pipeline ИМЯ
  описание конвейера построчно
end
```

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИМЯ` | ✓ | имя конвейера (латиница, цифры, точка, дефис, подчёркивание) |

Пример:

```
pipeline hello
repo: https://github.com/org/app.git
ref: main
action: compose
compose:
  file: deploy/docker-compose.yml
  project: hello
  hosts: [web1]
  env:
    DB_PASSWORD: generate:password
end
```

### `deploy КОНВЕЙЕР [start|stop] [ДЛИТЕЛЬНОСТЬ]` {#cmd-deploy}

Выкладывает конвейер — как кнопка «Выложить» — и ждёт итога; журнал выкладки идёт в журнал сценария. В сухом прогоне — сухой прогон конвейера (с описанием из «pipeline» выше, если оно есть). С start или stop — запускает или останавливает уже выложенное на всех его хостах, как кнопки «Запустить» и «Остановить».

| Аргумент | Обязательный | Что это |
|---|---|---|
| `КОНВЕЙЕР` | ✓ | имя конвейера в хабе или из «pipeline» выше |
| `start\|stop` |  | start — запустить выложенное (та же версия, образы с хоста), stop — остановить без удаления (контейнеры и данные остаются); как кнопки конвейера |
| `ДЛИТЕЛЬНОСТЬ` |  | сколько ждать итога (по умолчанию 30m) |

Пример:

```
deploy hello 20m
deploy shop stop
deploy shop start
```

### `migrate ХОСТ КОНТЕЙНЕР to ХОСТ [switch-fly] [switch-pipeline] [clean-leftovers]` {#cmd-migrate}

Переносит стек на другой хост — как перетаскивание на карте хостов: план (препятствия — ошибка до остановки стека), остановка, данные через хаб, запуск на новом; упавший перенос откатывается сам. Старый стек остаётся остановленным для отката (карта: «Откатить», «Убрать старый»).

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ХОСТ` | ✓ | хост, где стек работает сейчас |
| `КОНТЕЙНЕР` | ✓ | контейнер стека (переносится весь compose-проект) |
| `to ХОСТ` | ✓ | хост, куда перенести |
| `switch-fly` |  | переключить цели Traffic Fly на новый хост с той же ролью |
| `switch-pipeline` |  | переключить конвейер стека (хост в описании, учёт выкладки, сайт) |
| `clean-leftovers` |  | убрать на новом хосте остатки прежней копии этого стека (перенос туда и обратно) |

Пример:

```
migrate web1 forgejo-forgejo-1 to web2 switch-fly switch-pipeline
```

### `k8s КЛАСТЕР ingress cilium [port ПОРТ]` {#cmd-k8s-ingress}

Включает Ingress-контроллер Cilium в режиме host network: Envoy слушает порт на каждом узле (без LoadBalancer), Cilium — контроллер по умолчанию; версия Cilium не меняется. Нужен кластер с Cilium и заменой kube-proxy. Заданием на control plane.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `КЛАСТЕР` | ✓ | имя кластера в хабе или из «k8s create» выше; действия идут на первый control plane |
| `port` |  | порт на узлах (по умолчанию 8080, от 1024) |

Пример:

```
k8s lab ingress cilium port 8080
```

### `k8s КЛАСТЕР apply` {#cmd-k8s-apply}

Применяет манифест (kubectl apply) на первом control plane — как «Манифесты» раздела «Кластеры»; в сухом прогоне — kubectl diff.

```
k8s КЛАСТЕР apply
  манифест построчно
end
```

| Аргумент | Обязательный | Что это |
|---|---|---|
| `КЛАСТЕР` | ✓ | имя кластера в хабе или из «k8s create» выше; действия идут на первый control plane |

Пример:

```
k8s lab apply
apiVersion: v1
kind: Namespace
metadata:
  name: demo
end
```

### `k8s КЛАСТЕР wait deployment ИМЯ [namespace NS] [ДЛИТЕЛЬНОСТЬ]` {#cmd-k8s-wait}

Ждёт, пока все реплики deployment готовы.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `КЛАСТЕР` | ✓ | имя кластера в хабе или из «k8s create» выше; действия идут на первый control plane |
| `ИМЯ` | ✓ | имя deployment |
| `namespace` |  | namespace (по умолчанию default) |
| `ДЛИТЕЛЬНОСТЬ` |  | сколько ждать: 90s, 5m (по умолчанию 5m) |

Пример:

```
k8s lab wait deployment hello 5m
```

### `fly route ИД ВИД [names ИМЯ,…] [port ПОРТ] [tls gateway] [check tcp|http|none] [path ПУТЬ] [gateway ШЛЮЗ] (targets cluster КЛАСТЕР port ПОРТ | target ХОСТ container|vm|lxd ИМЯ ПОРТ … | target ХОСТ host ПОРТ …)` {#cmd-fly-route}

Создаёт или обновляет маршрут Traffic Fly и применяет его — как блок fly: конвейера: новый записывается и применяется, у существующего цели меняются плавно (новые добавляются и проверяются, прежние выводятся). В сухом прогоне — проверка карты с маршрутом.

| Аргумент | Обязательный | Что это |
|---|---|---|
| `ИД` | ✓ | идентификатор маршрута |
| `ВИД` | ✓ | https, http (по имени), tcp, udp, tcpudp (по порту) |
| `names` |  | имена через запятую (https, http) |
| `port` |  | порт на шлюзе (у tcp/udp обязателен; у https — 443, у http — 80) |
| `tls gateway` |  | сертификат на шлюзе (Let's Encrypt), целям — HTTP |
| `check` |  | проверка целей: tcp (по умолчанию), http (с path), none |
| `gateway` |  | имя шлюза; один шлюз — можно не писать |
| `targets cluster` |  | targets cluster КЛАСТЕР port N — цели: все узлы кластера (машины — как «виртуальная машина» на своих хостах) |
| `target` |  | target ХОСТ container\|vm\|lxd ИМЯ ПОРТ, target ХОСТ host ПОРТ — цель списком; можно несколько |

Пример:

```
fly route hello https names hello.example.com tls gateway check tcp targets cluster lab port 8080
fly route mc tcp port 25565 target web1 container mc-server-1 25565
```

### `fly apply` {#cmd-fly-apply}

Применяет карту Traffic Fly — как «Применить»; в сухом прогоне — сухой прогон применения.

Пример:

```
fly apply
```

## Готовые сценарии

### Новый хост с nginx

Завести хост, поставить nkt, пакеты, включить nginx и открыть порты.

```
group prod

host web1 192.0.2.10 user root password ask group prod
install web1

on web1 packages install nginx htop ncdu
on web1 service nginx enable
on web1 firewall allow 80/tcp
on web1 firewall allow 443/tcp
```

### Приложение в Docker

Поставить Docker и поднять compose-стек, описанный прямо в сценарии.

```
host app1 192.0.2.20 user deploy password ask
install app1

on app1 docker install
on app1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
  db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: change-me
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata: {}
end
on app1 firewall allow 8080/tcp
```

### Машины на существующем хосте

Создать две машины на хосте, который уже есть в хабе, и настроить их.

```
set IMAGE ubuntu-24.04
group lab profile base

# hv1 уже есть в хабе — заводить не нужно
on hv1 vm create lab-web image ${IMAGE} cpu 2 mem 2048 disk 20 profile base install
on hv1 vm create lab-db image ${IMAGE} cpu 2 mem 4096 disk 40 install

on lab-db packages install postgresql
on lab-db firewall allow 5432 from 192.168.100.0/24
on lab-web packages install nginx
```

### Forgejo за Traffic Fly: выкладка и перенос

Конвейер Forgejo с входом через шлюз, выкладка, проверка снаружи, перенос на другой хост с переключением маршрутов и конвейера — сценарием справки «Перенос стека с Traffic Fly».

```
# Forgejo за Traffic Fly: выкладка и перенос на другой хост
param FROM "хост, где Forgejo будет выложен сначала"
param TO "хост, куда его перенести"
param DOMAIN "имя Forgejo — A-запись на адрес шлюза Traffic Fly"

# Веб и git по SSH — через шлюз; сертификат выпускает шлюз
pipeline forgejo
repo: https://github.com/piqab/nkt.git
ref: main
action: compose
compose:
  file: examples/forgejo/deploy/docker-compose.yml
  project: forgejo
  hosts: [${FROM}]
  wait_timeout: 5m
  env_keys:
    forgejo-admin: [FORGEJO_ADMIN_USER, FORGEJO_ADMIN_PASSWORD, FORGEJO_ADMIN_EMAIL]
  env:
    POSTGRES_PASSWORD: generate:password
    FORGEJO_DOMAIN: ${DOMAIN}
    FORGEJO_ROOT_URL: https://${DOMAIN}/
    FORGEJO_ADMIN_USER: gitadmin
    FORGEJO_ADMIN_PASSWORD: generate:password
    FORGEJO_ADMIN_EMAIL: admin@example.com
  fly:
    - route: forgejo-web
      kind: https
      names: [${DOMAIN}]
      tls: gateway
      service: forgejo
      target_port: 3000
      check: http
      check_path: /api/healthz
    - route: forgejo-ssh
      kind: tcp
      port: 2222
      service: forgejo
      target_port: 2222
end
deploy forgejo 20m

# Проверка снаружи — через DNS и шлюз, как зайдёт пользователь
wait url https://${DOMAIN}/api/healthz 200 10m

# Перенос: маршруты Traffic Fly и конвейер переключаются на новый хост
migrate ${FROM} forgejo-forgejo-1 to ${TO} switch-fly switch-pipeline
wait url https://${DOMAIN}/api/healthz 200 5m
```

### Кластер Kubernetes: от нуля до первого пода

kubeadm на двух хостах через WireGuard, Cilium без kube-proxy, Ingress Cilium, тестовый под whoami и вход снаружи через Traffic Fly — сценарием справки «Кластер: от нуля до первого пода».

```
# Кластер Kubernetes: от нуля до первого пода
param HOST1 "хост с control plane и первым worker"
param HOST2 "хост со вторым worker"
param DOMAIN "имя тестового сайта — A-запись на адрес шлюза Traffic Fly"

# kubeadm: 1 control plane + 2 worker на двух хостах, WireGuard, Cilium без kube-proxy
on ${HOST1} k8s create lab flavor kubeadm nodes "${HOST1}: cp 1, w 1; ${HOST2}: w 1" network wireguard cni cilium no-kube-proxy image debian-13 expose http 0 https 0

# Ingress Cilium: порт 8080 на каждом узле
k8s lab ingress cilium port 8080

# Тестовый под, сервис и Ingress
k8s lab apply
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello
spec:
  replicas: 2
  selector:
    matchLabels: {app: hello}
  template:
    metadata:
      labels: {app: hello}
    spec:
      containers:
        - name: whoami
          image: traefik/whoami:v1.12.0
          ports: [{containerPort: 80}]
---
apiVersion: v1
kind: Service
metadata:
  name: hello
spec:
  selector: {app: hello}
  ports: [{port: 80, targetPort: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello
spec:
  ingressClassName: cilium
  rules:
    - host: ${DOMAIN}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: hello, port: {number: 80}}
end
k8s lab wait deployment hello 5m

# Вход снаружи: шлюз Traffic Fly на все узлы кластера
fly route hello https names ${DOMAIN} tls gateway check tcp targets cluster lab port 8080
wait url https://${DOMAIN}/ 200 10m
```

### Обслуживание существующих хостов

Применить профиль, записать файл, перечитать службу, убрать пакет — без заведения хостов.

```
# Хосты уже есть в хабе: только действия
on web1 apply profile web-base
on web2 apply profile web-base

on web1 file put /etc/motd
This server is managed by nkt.
end
on web1 service nginx reload
on web2 packages remove telnet
```
