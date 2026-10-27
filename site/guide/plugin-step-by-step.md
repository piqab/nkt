---
title: 'Пишем плагин по шагам'
---

# Пишем плагин по шагам

Статья собирает рабочий плагин хаба с нуля: **`rgstr-lite`**, учебный
плагин к реестру образов [rgstr](https://github.com/piqab/rgstr). Его
можно скачать, прочитать и загрузить в свой хаб:
[rgstr-lite.tar.gz](https://piqab.github.io/nkt/samples/rgstr-lite.tar.gz).
Исходник —
[`examples/_samples/rgstr-lite`](https://github.com/piqab/nkt/tree/main/examples/_samples/rgstr-lite).
Шаги ниже совпадают с комментариями «шаг 1…6» в коде.

Полная версия того же плагина — `rgstr` из каталога, о ней статья
[rgstr и rgstroperator в nkt](/guide/hub-plugin-rgstr). Справочник по
протоколу и правилам —
[Как написать плагин](/guide/plugin-authoring) и
[Плагины → Протокол](/guide/hub-plugins#протокол-для-своих-плагинов).

## Что получится

- раздел **«Сводка»** — версия rgstr, вход, число репозиториев и
  находки (вход выключен, реестр по HTTP);
- раздел **«Репозитории»** с кнопками «Теги», «Собрать мусор» и «Робот
  для конвейера»;
- скрытый раздел **«Теги»** с удалением тега — с подтверждением и сухим
  прогоном;
- **робот**: секрет один раз окну или сразу в конвейер — в `.env` или
  ключом registry (`@registry`);
- команды сценария `rgstr-lite gc` и `rgstr-lite robot …`;
- инструменты MCP `rgstr_lite_*` для агентов.

Плагин — один файл Python на стандартной библиотеке, около 330 строк.
Писать можно на любом языке: хабу нужен только HTTP-сервер.

## Папка плагина

```
rgstr-lite/
  README.md, README.en.md
  deploy/
    nkt-plugin.json     пакет: имя, версия, порт, настройки — для установки на хост
    nkt-plugin.py       сам плагин
    test_plugin.py      тест на поддельном rgstr (хаб его не хранит)
```

`nkt-plugin.json` читает хаб, когда **ставит** плагин на хост:

```json
{
  "name": "rgstr-lite", "version": "1.0.0", "min_nkt": "1.17.15", "protocol": 1,
  "title": {"ru": "rgstr (учебный)", "en": "rgstr (tutorial)"},
  "run": "python3", "file": "nkt-plugin.py", "port": 9281,
  "settings": [
    {"env": "RGSTR_URL", "title": {"ru": "Адрес rgstr", "en": "rgstr address"}, "default": "http://127.0.0.1:5000"},
    {"env": "RGSTR_USER", "title": {"ru": "Администратор rgstr", "en": "rgstr administrator"}, "default": "admin"},
    {"env": "RGSTR_PASSWORD", "title": {"ru": "Пароль администратора", "en": "Administrator password"}, "secret": true},
    {"env": "RGSTR_PUBLIC", "title": {"ru": "Адрес реестра для docker", "en": "Registry address for docker"}}
  ]
}
```

- `settings` — поля формы «Установить на хост». Они становятся
  окружением службы (`/etc/nkt-plugins/rgstr-lite.env`, `0600`).
- `secret: true` — поле скрытое, в истории и журналах значения нет.
- `version` должна совпадать с `VERSION` в коде: это проверяет lint.
- Ещё хаб сам передаёт `NKT_PLUGIN_SECRET`, `NKT_PLUGIN_PORT` и
  `NKT_PLUGIN_LISTEN`.

## Шаг 1. Сервер, подпись, манифест

Хаб ходит к плагину двумя запросами: `GET /nkt/manifest` и
`POST /nkt/call`. Каждый подписан: заголовок `X-NKT-Signature` — это
HMAC-SHA256 строки `отметка.путь.тело` общим секретом. Отметка — в
`X-NKT-Timestamp`, она не старше 5 минут.

```python
def signed(self, body):
    ts = self.headers.get("X-NKT-Timestamp", "")
    try:
        fresh = abs(time.time() - int(ts)) < 300
    except ValueError:
        return False
    want = hmac.new(SECRET.encode(), f"{ts}.{urllib.parse.urlsplit(self.path).path}.".encode() + body,
                    hashlib.sha256).hexdigest()
    return fresh and hmac.compare_digest(want, self.headers.get("X-NKT-Signature", ""))
```

- Без подписи — ответ `401`, плагин ничего не делает.
- Тело больше 1 МБ — ответ `413`.
- Без секрета длиной от 16 знаков плагин не запускается.

**Манифест** описывает, что плагин умеет. Хаб строит по нему разделы,
кнопки, инструменты MCP и команды сценариев:

```python
"ops": [
  {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True, "description": "…"},
  {"name": "tag_delete", "title": c.t("delete"), "mcp": True, "destructive": True,
   "input": {"type": "object", "properties": {"repo": s, "tag": s}, "required": ["repo", "tag"]}},
  {"name": "robot_create", "title": c.t("robot"), "mcp": True, "reveals": True, …},
],
"pages": [{"id": "summary", "title": c.t("summary"), "op": "summary"},
          {"id": "repos", "title": c.t("repos"), "op": "repos"},
          {"id": "tags", "title": c.t("tags"), "op": "tags", "hidden": True}],
"commands": [{"word": "rgstr-lite", "usage": c.t("usage"), "example": "rgstr-lite gc"}],
```

| Поле операции | Что значит для хаба |
|---|---|
| `read_only` | читать может любой; токен чтения тоже |
| `mcp` | операция видна агенту как инструмент `rgstr_lite_<op>` (дефис в имени — подчёркивание) |
| `destructive` | в MCP помечена как удаляющая |
| `reveals` | ответ может нести секрет — токену только с `to_pipeline`/`env` |
| `input` | JSON Schema аргументов: по ней строится инструмент MCP |

Тексты — на двух языках. Язык приходит заголовком `X-NKT-Lang`, поэтому
манифест и ответы плагин отдаёт на языке пользователя (`T["ru"]`,
`T["en"]`).

## Шаг 2. Раздел-сводка с находками

Раздел — это операция, которая возвращает **вид** (`view`): таблицу,
текст или `sections` (несколько видов подряд).

```python
def summary_view(c):
    health = api(c, "GET", "/healthz", auth=False)
    me = api(c, "GET", "/ui/api/me")
    …
    if not me.get("auth_enabled"):
        found.append(c.t("f_auth_off"))
    return {"type": "sections", "items": [table(c.t("summary"), …), table(c.t("findings"), …)]}
```

Откуда брать данные — из API программы, к которой плагин подключён. У
rgstr это `/ui/api/*`:

- вход — Basic или Bearer;
- у **каждого** POST должно быть JSON-тело, хотя бы `{}`;
- ошибка — `{"error": "…"}`.

Всё это видно в исходниках rgstr; функция `api()` учитывает каждое
правило. Ответ `401` превращается в понятный текст «вход не принят —
проверьте RGSTR_USER и RGSTR_PASSWORD».

**Находки** — то, что администратор должен исправить. Пишите их
утверждением о проблеме («Вход выключен: любой может загружать и
удалять образы»), а не названием проверки.

## Шаг 3. Таблица и скрытый раздел

```python
rows = [{"cells": {"repo": r["name"], "n_tags": len(r.get("tags") or [])},
         "actions": [{"title": c.t("open"), "open": "tags", "args": {"repo": r["name"]}}]}
        for r in repositories if REPO_RE.match(r["name"])]
```

- `actions` у строки — кнопки.
- `"open": "tags"` открывает раздел `tags` с аргументами. Раздел помечен
  `"hidden": true`, поэтому в меню его нет — попасть туда можно только из
  строки.
- `actions` у таблицы — кнопки над ней. `{"op": "gc_run"}` вызывает
  операцию; с `fields` сначала открывается форма (так устроен «Робот для
  конвейера»).

**Всё, что пришло снаружи, проверяется до использования** — даже имя
репозитория из ответа самого rgstr. Регулярные выражения `REPO_RE`,
`TAG_RE`, `NAME_RE` стоят в начале файла. Значение, которое не прошло
проверку, не попадает ни в путь, ни в тело запроса.

## Шаг 4. Изменения: подтверждение и сухой прогон

```python
{"title": c.t("delete"), "op": "tag_delete", "args": {"repo": repo, "tag": tag}, "danger": True,
 "confirm": c.t("confirm", repo=repo, tag=tag)}
```

- `confirm` — хаб спросит «Удалить тег shop/api:1.0?»;
- `danger` — красная кнопка.

Каждая операция, которая что-то меняет, понимает `dry_run`. Хаб
передаёт его в запросе: в сухом прогоне сценария, у агента с
`dry_run: true`, в API.

```python
def tag_delete(c, args, dry):
    repo, tag = check(c, REPO_RE, args.get("repo")), check(c, TAG_RE, args.get("tag"))
    what = c.t("deleted", repo=repo, tag=tag)
    if dry:
        return c.t("would", what=what)        # «(сухой прогон) shop/api:1.0 удалён»
    api(c, "POST", "/ui/api/tags/delete", {"repo": repo, "tags": [tag]})
    return what
```

Сухой прогон проверяет ввод так же, как настоящий вызов, — и ничего не
меняет.

## Шаг 5. Секрет: окну или в конвейер

Робот rgstr отдаёт секрет один раз. Плагин не хранит его и не пишет в
журнал. У секрета два пути:

**Человеку в окне** — ответ с `reveal`. Хаб покажет секрет один раз, с
кнопкой «Скопировать»:

```python
return {"ok": True, "text": c.t("robot_done", name=name),
        "reveal": {"title": c.t("secret", name=name), "value": secret,
                   "note": c.t("note", host=PUBLIC, name=name)}}
```

**В конвейер** — ответ с `store`. Хаб сам запишет значение и уберёт его
из ответа:

```python
store = {"pipeline": pipe, "env": env, "value": secret}
if env == "@registry":                       # не в .env, а ключом registry конвейера
    store.update({"registry": PUBLIC, "user": name})
return {"ok": True, "text": c.t("robot_done", name=name), "store": store}
```

- `env` — имя переменной: значение уходит в `.env` конвейера с историей
  версий.
- `env: "@registry"` — значение уходит в «Секреты» → «Реестры» конвейера
  как ключ registry `registry` с логином `user`. Ключ с тем же адресом
  заменяется, его CA сохраняется. Конвейер скачивает образы этим ключом —
  `docker login` на хостах не нужен.

Правила, которые хаб проверяет за плагин:

- `pipeline` и `env` в ответе должны совпадать с тем, что было **в
  запросе** (`to_pipeline`, `env`). Плагин не может выбрать конвейер сам.
- Право писать в этот конвейер хаб проверяет **до** вызова плагина.
- Токену (API, MCP) операция с `reveals` доступна только с `to_pipeline`
  и `env`: агент значение не увидит.
- Из сценария секрет идёт только в конвейер, поэтому `_command` выбрасывает
  `reveal`.

## Шаг 6. Команда сценария

Хаб передаёт плагину слова команды (`argv`) двумя служебными операциями:

- `_check` — «Проверить» сценарий: ответ — ошибка текстом или `ok`;
- `_command` — выполнение (с `dry_run` в сухом прогоне); `text` уходит в
  журнал задания.

```python
def command(c, argv, dry, only_check):
    a = argv[1:]                              # argv[0] — само слово «rgstr-lite»
    if a == ["gc"]:
        return {"ok": True} if only_check else {"ok": True, "text": gc_run(c, dry)}
    if len(a) == 8 and a[0] == "robot" and a[2] == "project" and a[4] == "to-pipeline" and a[6] == "env":
        …
    raise Fail(c.t("usage"))
```

В сценарии:

```
rgstr-lite gc
rgstr-lite robot ci project shop to-pipeline shop env @registry
deploy shop
```

Если слово не занято другим плагином, оно работает само по себе; иначе —
`plugin rgstr-lite gc`.

## Шаг 7. Тест на подделке

`deploy/test_plugin.py` поднимает **поддельный rgstr** (ответы — как у
настоящего, по его исходникам) и сам плагин отдельным процессом. Затем
он вызывает плагин подписанными запросами так же, как хаб:

```sh
python3 -I examples/_samples/rgstr-lite/deploy/test_plugin.py
```

Тест проверяет:

- манифест и разделы;
- отказ на плохой ввод (`../x`, `a b`, `x;rm`);
- что сухой прогон не шлёт rgstr ни одного POST;
- `reveal` и `store` (в том числе `@registry`), отказ на плохое имя
  переменной;
- команды `_check` и `_command` — и что `_command` не отдаёт `reveal`;
- `401` без подписи и отказ на неизвестную операцию.

## Шаг 8. Проверка и архив

```sh
go run ./cmd/nkt-catalog lint -dir examples/_samples/rgstr-lite
tar czf rgstr-lite.tar.gz -C examples/_samples rgstr-lite
```

`lint` делает ту же проверку, что хаб при загрузке:

- пакет и версия = `VERSION` в коде;
- тексты на ru и en;
- секретные настройки;
- запрещённый код (`shell=True`, `os.system`, `eval`, команда строкой).

## Шаг 9. Загрузить и поставить

1. «О системе» → «Плагины» → «Каталог» → **«Загрузить свой плагин»** →
   архив. Хаб покажет код целиком. Прочитайте его, отметьте **«Код
   просмотрен»** → «Сохранить». На хабе плагин не запускается.
2. «rgstr (учебный)» (метка «свой») → **«Установить на хост»** → хост, с
   которого виден rgstr, → настройки → задание. Появится служба
   `nkt-plugin-rgstr-lite` на порту 9281.
3. Разделы «Сводка» и «Репозитории» плагина. «Для агентов» — инструменты
   `rgstr_lite_summary`, `rgstr_lite_repos`, `rgstr_lite_tag_delete`,
   `rgstr_lite_gc_run`, `rgstr_lite_robot_create`.

Вызов из API:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"tag_delete","args":{"repo":"shop/api","tag":"1.0"},"dry_run":true}' \
  https://hub.example.com/api/hub/plugins/rgstr-lite/call
```

**Новая версия:** поднимите `VERSION` в коде и `version` в
`nkt-plugin.json`, затем загрузите архив снова. Хаб покажет дифф с
прежней версией. Потом нажмите «Обновить на хосте».

## Дальше

- [Как написать плагин](/guide/plugin-authoring) — все правила:
  - формы и документы с историей (`edit`);
  - записи DNS от плагина;
  - `provides: dns` и `provides: hosts`;
  - `_connect` с токеном хаба;
  - чек-лист безопасности.
- Полный `rgstr` (`examples/rgstr/deploy/nkt-plugin.py`) — то же на
  шагах 1–6, плюс:
  - проекты;
  - политики с правкой текстом;
  - вебхуки и журнал;
  - десять находок.
