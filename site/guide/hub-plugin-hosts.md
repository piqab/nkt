---
title: 'Хосты плагинов: MikroTik и другие устройства'
---

# Хосты плагинов: MikroTik и другие устройства

Не всё, чем нужно управлять, — Linux с агентом nkt. Роутер MikroTik,
Windows-сервер, облачная ВМ: у них нет агента, зато есть API или SSH.
**Хост плагина** — такое устройство в списке хостов nkt: читает его и
управляет им [плагин](/guide/hub-plugins), а хаб даёт всё остальное —
группы, пределы токенов, оповещения, «Мониторинг», сценарии, MCP, экспорт.

Плагины: **MikroTik (RouterOS 7)** — [README](https://github.com/piqab/nkt/tree/main/examples/mikrotik);
**connector** — устройства по профилям без своего кода (macOS, FreeBSD,
OpenWrt, Linux без агента, Keenetic, принтеры и коммутаторы по SNMP) —
[ниже](#connector-устройства-по-профилям); **Windows** — через встроенный
OpenSSH и PowerShell, без WinRM: находки (обновления, Defender, брандмауэр,
RDP без NLA, SMBv1, BitLocker), службы, установка обновлений, команды
PowerShell — [README](https://github.com/piqab/nkt/tree/main/examples/windows);
**ВМ Yandex Cloud** — [плагин Yandex Cloud](/guide/hub-plugin-yandex-cloud).

## Добавить устройство

1. Поставьте плагин: «О системе» → «Плагины» → «Установить на хост» →
   MikroTik — на машину хаба или любой хост, откуда видны устройства.
2. «Хосты» → **«Добавить устройство»** → тип → поля (адрес, пользователь,
   пароль…) → **«Проверить»** → «Добавить». Имя можно не писать — возьмётся
   с устройства.

![Добавить устройство](/screens/ru/hub-device-add.png)

- **Учётки хранит хаб**, зашифрованными мастер-ключом, как SSH-ключи
  хостов, и передаёт плагину в каждом вызове. Пароль никому не
  показывается — в карточке только «задан»; не попадает ни в журнал
  действий, ни в параметры заданий.
- **Свой сертификат устройства** (у роутеров обычно так): отпечаток
  запоминается при «Проверить» и сверяется при каждом подключении — как
  ключ SSH хоста.

## Что видно и что можно

Строка в списке хостов — как у всех: состояние, находки, адрес, «виден».
«Открыть» — панель устройства с разделами, которые есть у его типа:

| Раздел | Что |
|---|---|
| **Состав** | модель, версия, интерфейсы и адреса, порты служб и откуда открыты, соседи по сети, таблицы типа (у MikroTik — маршруты, firewall, DHCP) |
| **Метрики** | сейчас; хаб опрашивает устройство раз в минуту — история в «Мониторинге» вместе с остальными хостами |
| **Находки** | что не так (обновления, открытые наружу службы…) с подсказкой, как исправить; критичные — в оповещения, как у хостов с агентом |
| **Конфигурация** | хаб читает её раз в час и хранит 30 версий: изменилась на устройстве — новая версия и дифф, кто бы ни менял |
| **Действия** | перезагрузка, обновление и прочее, что объявил тип; с подтверждением и сухим прогоном |
| **Команды** | команда на языке устройства (RouterOS, PowerShell) — с подтверждением, в журнал действий |
| **Учётки** | сменить логин и пароль (пусто — прежний), с «Проверить» и диффом |

![Устройство: состав](/screens/ru/hub-device-overview.png)

![Устройство: дифф конфигурации](/screens/ru/hub-device-config.png)

![Windows: находки](/screens/ru/hub-device-windows.png)

Смотреть может любой, кто видит хост (и токен чтения в пределах его
хостов); действия, команды и учётки — администратор или токен «по
правам» с действием «плагин ИМЯ». Убрать устройство из nkt — на самом
устройстве ничего не меняет.

## Windows: вход, брандмауэр, виртуальные машины

**Пользователь.** Локальное имя — то, что после «\\» в `whoami`. Для
учётной записи Microsoft (`name@outlook.com`) в поле пишется тоже локальное
имя, не почта (обычно как папка в `C:\Users`), а пароль — от учётной
записи, не PIN Windows Hello. Если в Windows включён вход только через
Windows Hello, вход паролем по SSH может не пройти — тогда ключ:

```powershell
Add-Content C:\ProgramData\ssh\administrators_authorized_keys "ssh-ed25519 AAAA… nkt"
icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r /grant "*S-1-5-32-544:F" /grant "*S-1-5-18:F"
```

Без `icacls` sshd файл молча не читает; SID вместо названий групп — на
русской Windows группа называется «Администраторы». Закрытый ключ — в поле
«Закрытый ключ SSH», пароль пустой. (В RDP, наоборот, вход учётной записью
Microsoft — почтой.)

**Брандмауэр.** Сеть ВМ или новая сеть Windows обычно «Общедоступная», а
правило SSH может быть включено только для частных сетей:

```powershell
Set-NetFirewallRule -Name OpenSSH-Server-In-TCP -Enabled True -Profile Any
```

Проверять доступность — портом (`nc -zv -w 5 АДРЕС 22`), не `ping`: на
ping Windows по умолчанию не отвечает.

**Windows в виртуальной машине.** Плагин ставится на хост, **с которого
виден адрес Windows**, — и это не всегда хост, где работает ВМ. Тип сети
ВМ: `virsh domiflist ВМ` (колонка Type).

| Сеть ВМ | Кто видит Windows | Куда ставить плагин |
|---|---|---|
| macvtap (`direct`) | вся сеть, **кроме самого хоста KVM** | хаб или любой другой хост этой сети, **не** хост KVM |
| мост Linux (`bridge`, `br0`) | все, и хост KVM | хост KVM или любой хост сети |
| NAT `default` или изолированная (`network`) | **только хост KVM** | только хост KVM |
| VirtualBox NAT с пробросом порта | хост ВМ | хост ВМ, адрес `127.0.0.1:ПОРТ` |

macvtap — ограничение Linux, а не настройка: хост не обменивается
пакетами со своими ВМ на macvtap, и брандмауэр тут ни при чём.
Беспроводной адаптер в мост обычно не включить — точка доступа не
пропускает чужие MAC.

## Сценарии и MCP

Команда сценария `on ХОСТ run "КОМАНДА"` — или блок до `end`:

```
on router-office run "/ip dns cache flush"
on router-office run
/ip firewall address-list add list=blocked address=198.51.100.7
/ip firewall address-list print where list=blocked
end
```

«Проверить» отдаёт команду плагину на разбор, сухой прогон — плагин
отвечает, что выполнил бы. Хосту плагина — только `run` (агента nkt у него
нет), хосту с агентом — `run` нельзя.

MCP: `list_hosts` показывает и хосты плагинов (`kind: plugin`), а
`device_snapshot`, `device_findings`, `device_config` (чтение),
`device_action` и `device_exec` (администратор) — их разделы.

## Connector: устройства по профилям

Плагин **connector** — типы устройств без своего кода: каждый тип —
профиль JSON. Встроенные: Linux без агента, macOS, FreeBSD, OpenWrt (SSH),
Keenetic (HTTP), принтер и коммутатор (SNMP v2c) — [README](https://github.com/piqab/nkt/tree/main/examples/connector).
Свои — раздел плагина «Профили» (правка с диффом и историей в хабе).

![Профили connector](/screens/ru/hub-connector-profiles.png)

![Правка профиля](/screens/ru/hub-connector-edit.png)

Профиль:

```json
{"id": "my-nas", "title": {"ru": "Мой NAS", "en": "My NAS"}, "transport": "ssh",
 "hint": "что включить на устройстве",
 "info": {"name": {"cmd": "hostname", "parse": [{"trim": 1}]},
          "version": {"cmd": "cat /etc/os-release", "parse": [{"kv": "="}, {"key": "VERSION_ID"}]}},
 "snapshot": {"packages": {"cmd": "opkg list-installed", "parse": [{"each": "^(?P<name>\\S+) - (?P<version>\\S+)"}]}},
 "metrics": {"cpu_pct": {"cmd": "top -bn1", "parse": [{"regex": "(\\d+)% idle"}, {"rev": 100}]}},
 "findings": [{"id": "disk", "severity": "warning", "title": "Диск заполнен на {value}%",
               "value": {"cmd": "df -P / | awk 'NR==2{print $5+0}'", "parse": [{"num": 1}]}, "if": {"gt": 90}}],
 "config": {"cmd": "uci export"},
 "actions": [{"op": "reboot", "title": "Перезагрузить", "cmd": "reboot", "danger": true, "disconnects": true}]}
```

- **Связь** (`transport`): `ssh` — источник `{"cmd": "…"}` (команда на
  устройстве; считать можно и в ней — awk, sed); `http` — `{"get": "/путь"}`
  (`auth`: `basic`, `bearer`, `keenetic`); `snmp` — `{"oid": "…"}` и
  `{"table": {"столбец": "OID"}}` (обход, строки по индексу). Ещё
  `{"const": значение}` и `{"conn": "поле учёток"}`.
- **Разбор** (`parse`, по порядку): `json` (путь `a.b.0`, `a[].b`), `kv`
  (`ключ=значение` построчно), `key`, `regex` (первое совпадение; именованные
  группы — объект), `each` (регулярное выражение на каждую строку →
  строки-объекты), `lines` (пропустить N строк), `cols` (столбцы по
  пробелам), `num` (число × множитель), `rev` (X − число), `pick`
  (переименовать поля строк), `items` (объект → строки), `filter`, `pct`,
  `join`, `count`, `first`, `trim`.
- **Куда:** `info` (name, os, version, model, serial, arch, firmware,
  uptime_s), `snapshot` (mem_total, cpu, interfaces, services, packages,
  ports, users, neighbors, disks — строки с полями общей модели), `extra`
  (свои таблицы), `metrics` (cpu_pct, mem_used, mem_total, disk_used,
  disk_size, temp_c, uptime_s, net — строки name/rx/tx), `findings`
  (`if`: `gt`, `lt`, `ge`, `le`, `eq`, `ne`, `contains`, `empty`,
  `nonempty`; для строк — `field`), `config`, `actions` (`cmd` или `post`,
  поля формы подставляются в кавычках), `exec` (у ssh — команды `sh`).

Неудачный разбор поля — поле пустое, а не ошибка: один профиль подходит к
разным версиям устройства.

## Свой тип устройств (для авторов плагинов)

Плагин объявляет в манифесте `provides: ["hosts"]` и `host_types`:

```json
{"provides": ["hosts"],
 "host_types": [{
   "id": "routeros", "title": "MikroTik RouterOS", "hint": "что включить на устройстве",
   "fields": [{"name": "address", "title": "Адрес", "required": true},
              {"name": "password", "title": "Пароль", "type": "password", "required": true},
              {"name": "fingerprint", "title": "Отпечаток"}],
   "sections": ["overview", "metrics", "findings", "config", "actions", "exec"],
   "actions": [{"title": "Перезагрузить", "op": "reboot", "confirm": "Точно?", "danger": true}],
   "exec": "routeros"}]}
```

Поле `address` обязательно; `type: password` — секрет (хаб его не
показывает); поле `fingerprint` хаб заполняет тем, что вернула проверка.
Хаб вызывает служебные операции с `args: {type, conn, …}` (`conn` — поля
формы):

| Операция | Ответ |
|---|---|
| `_host_check` | `result`: `name`, `os`, `version`, `model`, `serial`, `firmware`, `uptime_s`, `fingerprint` |
| `_host_snapshot` | `result`: `info`, `cpu`, `mem_total`, `disks`, `interfaces`, `services`, `packages`, `ports`, `users`, `neighbors`, `extra` (виды-таблицы) |
| `_host_metrics` | `result`: `cpu_pct`, `mem_used`, `mem_total`, `disk_used`, `disk_size`, `temp_c`, `uptime_s`, `net` (`{интерфейс: {rx, tx}}` — байты с начала работы) |
| `_host_findings` | `result.findings`: `id`, `severity` (`critical` / `warning` / `info`), `title`, `detail`, `fix` |
| `_host_action` | `action`, `args`, `dry_run` → `text` |
| `_host_config` | `result.text` (без меняющихся строк — времени выгрузки) |
| `_host_exec` | `command`, `check` (только разобрать), `dry_run` → `text` |

Образец — `examples/mikrotik/deploy/nkt-plugin.py` и его тест на
поддельном устройстве.

## Экспорт и импорт

Хосты плагинов переносятся экспортом хаба вместе с группой, плагином и
типом; учётки перешифровываются ключом хаба-получателя. Версии
конфигурации не переносятся — новый хаб соберёт их опросом.
