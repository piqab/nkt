---
title: Плагины
---

# Плагины

Плагин — отдельная программа, которая добавляет хабу свои разделы,
команды сценариев и инструменты MCP. Обычно это контейнер, выложенный
обычным конвейером: обновляется и удаляется тоже конвейером, а хаб к нему
только подключается. Без подключённых плагинов хаб работает как прежде.

Первый плагин — [Technitium DNS](/guide/hub-plugin-dns): зоны и записи
своих доменов на своих серверах имён, делегирование, DNSSEC.

## Как это устроено

- Плагин слушает HTTP на **127.0.0.1** своего хоста (в compose —
  `127.0.0.1:9180:9180`): снаружи его не видно.
- Хаб ходит к нему **через SSH-соединение с хостом** (плагин на машине
  хаба — напрямую). Хост, подключённый только туннелем (без SSH), для
  плагина не подходит.
- Каждый вызов **подписан** общим секретом: `NKT_PLUGIN_SECRET` из `.env`
  конвейера плагина. Хаб хранит его зашифрованным, в экспорт он уходит
  вместе с остальными секретами хаба.
- Плагин описывает себя **манифестом**: разделы, операции, команды
  сценариев, инструменты MCP, что он делает для хаба и какие права у хаба
  просит.

## Подключить

1. Выложите плагин конвейером (пример — в его справке). В `.env`
   конвейера — `NKT_PLUGIN_SECRET: generate:hex32` и, если порт не 9180,
   `NKT_PLUGIN_PORT`.
2. «О системе» → **«Плагины»** → «Подключить» → «С конвейера», выберите
   конвейер. Хост, порт и секрет хаб возьмёт из него сам.
3. **«Проверить»** — хаб покажет, что плагин добавит: разделы, команды
   сценариев, инструменты MCP, что он делает для хаба и какие права
   просит. Совпадения (слово сценария или инструмент уже есть) — там же.
4. **«Подключить»**. В меню хаба появится раздел «Плагины».

«Вручную» — хост, порт и секрет без конвейера (плагин, установленный не
через nkt). «Отключить» убирает разделы, команды и инструменты из хаба и
отзывает токен плагина; сам плагин и его данные остаются на хосте.

## Права

| Кто | Что |
|---|---|
| Любой вошедший, токен чтения | разделы плагина и операции «только чтение» |
| Администратор | всё, что умеет плагин |
| Токен по правам | операции, которые меняют, — с действием «плагин ИМЯ» в окне токена |
| Сам плагин | вызовы хаба — токеном по правам только с теми действиями, которые он просил (`hub_actions`) и которые вы подтвердили при подключении |

Каждое действие плагина (операция, правка документа, команда сценария) —
в журнале действий хаба: `plugin.op`, `plugin.doc`, `plugin.command`,
подключение — `plugin.connect`.

## Разделы

Раздел — то, что плагин показывает: таблицы, текст, кнопки. Кнопки
вызывают операции плагина (с формой и подтверждением), открывают другой
раздел («открыть зону») или **правят документ** — текст (например, файл
зоны) в том же окне, что и всё остальное в nkt: дифф перед записью и
история версий в хабе. Если документ изменился, пока вы его правили,
запись не пройдёт — хаб покажет новый текст.

## Сценарии

Команда плагина пишется так:

```
plugin ИМЯ СЛОВО аргументы…
```

или одним словом, если его не занял другой плагин (`dns set …`).
«Проверить» спрашивает плагин, правильно ли написано; в сухом прогоне
плагин только отвечает, что сделал бы. Справка по словам — в разделе
плагина и в [командах сценариев](/guide/script-commands#cmd-plugin).

## MCP

Операции, которые плагин отметил для агентов, — инструменты
`<имя>_<операция>` (`dns_zones`, `dns_record_set`). Токен чтения видит
только операции чтения, остальные — с действием «плагин ИМЯ».
Отключили плагин — инструментов нет в `tools/list`.

## Протокол (для своих плагинов)

Версия 1. Плагин — HTTP-сервер на любом языке.

**Подпись.** Каждый запрос хаба несёт заголовки:

- `X-NKT-Timestamp` — время в секундах Unix;
- `X-NKT-Signature` — hex HMAC-SHA256 строки
  `<отметка>.<путь>.<тело>` секретом (`<путь>` — `/nkt/manifest` или
  `/nkt/call`, тело GET — пустое);
- `X-NKT-Lang` — `ru` или `en`: тексты плагин отдаёт на этом языке.

Отметка старше 5 минут или неверная подпись — ответ `401`.

**`GET /nkt/manifest`:**

```json
{
  "protocol": 1,
  "name": "dns",
  "title": "Technitium DNS",
  "version": "1.0.0",
  "description": "…",
  "ops": [
    {"name": "zones", "title": "Зоны", "read_only": true, "mcp": true,
     "description": "List zones", "input": {"type": "object", "properties": {}}},
    {"name": "record_set", "mcp": true, "destructive": false,
     "input": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}
  ],
  "pages": [{"id": "zones", "title": "Зоны", "op": "zones"}],
  "commands": [{"word": "dns", "usage": "dns set ИМЯ ТИП ЗНАЧЕНИЕ [ttl СЕК]"}],
  "provides": ["dns"],
  "hub_actions": []
}
```

- `name` — латиница, цифры, дефис (2–32); операции и разделы — латиница,
  цифры, `_`. Раздел ссылается на операцию «только чтение».
- `commands` — слова сценариев; слова языка (`on`, `fly`, `deploy`…)
  занять нельзя.
- `provides: ["dns"]` — плагин ведёт DNS-записи для хаба (ниже).
- `hub_actions` — действия токена по правам (`pipeline.deploy`,
  `fly.manage`…), которые плагин просит у хаба.

**`POST /nkt/call`** — `{"op": "…", "args": {…}, "user": "admin",
"dry_run": false}` → `{"ok": true, "text": "…", "result": …, "view": …}`
или `{"ok": false, "error": "текст для человека"}`.

**Вид** (`view`) раздела:

```json
{"type": "table", "title": "Зоны", "note": "…",
 "columns": [{"key": "zone", "title": "Зона"}],
 "rows": [{"cells": {"zone": "example.com"},
           "actions": [{"title": "Записи", "open": "records", "args": {"zone": "example.com"}},
                       {"title": "Файл зоны", "args": {"zone": "example.com"},
                        "edit": {"get": "zone_text", "put": "zone_import", "doc": "example.com"}},
                       {"title": "Удалить", "op": "zone_delete", "args": {"zone": "example.com"},
                        "confirm": "Удалить зону example.com?", "danger": true}]}],
 "actions": [{"title": "Добавить зону", "op": "zone_add",
              "fields": [{"name": "zone", "title": "Зона", "required": true}]}]}
```

`type` — `table`, `text` (`text`) или `sections` (`items` — виды подряд).
Поля формы: `text`, `textarea`, `number`, `select` (`options`), `bool`.
Документ: операция `get` отдаёт `result.text`, `put` получает
`args.text`; историю версий ведёт хаб.

**Служебные операции** (их нет в `ops`, хаб вызывает сам):

| Операция | Аргументы | Когда |
|---|---|---|
| `_connect` | `name`, `hub_url`, `token` | подключение (токен — если просили `hub_actions`); не нужна — ответьте ошибкой со словом `_connect` |
| `_check` | `argv` | «Проверить» сценарий с командой плагина: ошибка — текстом |
| `_command` | `argv`, `dry_run` у запроса | выполнение команды сценария; `text` — строки в журнал задания |
| `_dns_zones` | — | `provides: dns`: `{"result": {"zones": ["example.com"]}}` |
| `_dns_sync` | `owner`, `records`, `previous` | `provides: dns`: привести записи владельца (`fly:<маршрут>`, `site:<домен>`) к `records`, убрав из `previous` то, чего в `records` нет |

`argv` — слова команды сценария начиная со слова команды:
`dns set app.example.com A 203.0.113.10` → `["dns", "set", "app.example.com", "A", "203.0.113.10"]`.

## Свой плагин: самое короткое

Python без зависимостей, один раздел и одна команда:

```python
import hashlib, hmac, json, os, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SECRET = os.environ["NKT_PLUGIN_SECRET"].encode()

def signed(h, path, body):
    ts = h.headers.get("X-NKT-Timestamp", "0")
    want = hmac.new(SECRET, f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
    return abs(time.time() - int(ts)) < 300 and hmac.compare_digest(want, h.headers.get("X-NKT-Signature", ""))

MANIFEST = {"protocol": 1, "name": "hello", "title": "Hello", "version": "0.1",
            "ops": [{"name": "info", "read_only": True, "mcp": True, "description": "Say hello"}],
            "pages": [{"id": "info", "title": "Hello", "op": "info"}],
            "commands": [{"word": "hello", "usage": "hello ИМЯ"}]}

class H(BaseHTTPRequestHandler):
    def reply(self, obj, code=200):
        raw = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def do_GET(self):
        if not signed(self, self.path, b""): return self.reply({"error": "signature"}, 401)
        self.reply(MANIFEST)
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if not signed(self, self.path, body): return self.reply({"error": "signature"}, 401)
        req = json.loads(body)
        if req["op"] == "info":
            return self.reply({"ok": True, "view": {"type": "text", "text": "Hello from the plugin"}})
        if req["op"] in ("_check", "_command"):
            argv = req["args"]["argv"]
            if len(argv) != 2: return self.reply({"ok": False, "error": "hello ИМЯ"})
            return self.reply({"ok": True, "text": f"hello, {argv[1]}" + (" (dry run)" if req.get("dry_run") else "")})
        self.reply({"ok": False, "error": f"no op {req['op']}"})

ThreadingHTTPServer(("0.0.0.0", 9180), H).serve_forever()
```

В compose — `ports: ["127.0.0.1:9180:9180"]` и секрет из `.env`.
Полный пример — плагин Technitium DNS в
[examples/technitium](https://github.com/piqab/nkt/tree/main/examples/technitium).
