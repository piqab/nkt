"""Заготовка плагина хаба nkt (протокол плагинов nkt v1) — копируйте её.

Плагин к условному REST API «элементов» (GET/POST/DELETE /api/items) с
токеном: раздел со списком, форма «Добавить», удаление с подтверждением,
сводка с находками, команда сценария «example …», инструменты MCP.
Замените API на настоящее, имена — на свои; правила — examples/AGENTS.md.

Настройки — окружение службы (хаб пишет их в /etc/nkt-plugins/<имя>.env,
секреты — только там): EXAMPLE_URL, EXAMPLE_TOKEN. Плагин запускается и
без них — их задают после установки («Настройки» на странице плагина).
Без зависимостей: только стандартная библиотека Python.
"""
import hashlib
import hmac
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Версия — та же, что «version» в nkt-plugin.json (проверяет lint); любое
# изменение файлов плагина — новая версия, иначе каталог не соберётся.
VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9299") or 9299)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
API = (os.environ.get("EXAMPLE_URL", "").strip() or "http://127.0.0.1:8080").rstrip("/")
TOKEN = os.environ.get("EXAMPLE_TOKEN", "").strip()

# Все тексты — на двух языках: хаб присылает X-NKT-Lang (ru, en).
T = {
    "ru": {
        "title": "Заготовка плагина", "desc": "Пример плагина к REST API: список, добавление, удаление, сводка, команда сценария, MCP.",
        "items": "Элементы", "summary": "Сводка", "name": "Имя", "size": "Размер", "state": "Состояние",
        "add": "Добавить", "delete": "Удалить", "delete_confirm": "Удалить элемент {name}?",
        "name_hint": "латиница, цифры, - и _ (до 64)", "added": "добавлен {name}", "deleted": "удалён {name}",
        "would": "(сухой прогон) {what}", "check_ok": "проверено", "nothing": "нечего делать",
        "no_token": "не задан EXAMPLE_TOKEN — «Настройки» на странице плагина",
        "api_failed": "API {method} {path}: {err}", "bad_name": "имя {name}: латиница, цифры, - и _ (до 64)",
        "usage": "example list | example add ИМЯ | example rm ИМЯ",
        "f_broken": "Элемент {name} сломан", "f_broken_fix": "удалите и добавьте заново",
        "x_total": "Всего", "x_broken": "Сломано",
    },
    "en": {
        "title": "Plugin template", "desc": "An example plugin for a REST API: list, add, delete, summary, a script command, MCP.",
        "items": "Items", "summary": "Summary", "name": "Name", "size": "Size", "state": "State",
        "add": "Add", "delete": "Delete", "delete_confirm": "Delete item {name}?",
        "name_hint": "latin letters, digits, - and _ (up to 64)", "added": "added {name}", "deleted": "deleted {name}",
        "would": "(dry run) {what}", "check_ok": "checked", "nothing": "nothing to do",
        "no_token": "EXAMPLE_TOKEN is not set — Settings on the plugin page",
        "api_failed": "API {method} {path}: {err}", "bad_name": "name {name}: latin letters, digits, - and _ (up to 64)",
        "usage": "example list | example add NAME | example rm NAME",
        "f_broken": "Item {name} is broken", "f_broken_fix": "delete it and add it again",
        "x_total": "Total", "x_broken": "Broken",
    },
}


class Fail(Exception):
    """Ошибка для человека: уходит хабу как {ok: false, error}."""


class Ctx:
    """Язык запроса: c.t("ключ", …) — текст, c.fail(…) — ошибка."""

    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        return T[self.lang][key].format(**kw)

    def fail(self, key, **kw):
        return Fail(self.t(key, **kw))


def log(msg):
    """Журнал службы (journalctl). Секретов здесь не бывает: ни токена, ни
    значений из форм с type password."""
    print(f"nkt-plugin-example: {msg}", flush=True)


# --- API -----------------------------------------------------------------------

# Всё, что приходит от хаба и уходит в путь, команду или запрос, —
# через регулярное выражение до использования.
NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")


def check_name(c, name):
    name = str(name or "").strip()
    if not NAME_RE.match(name):
        raise c.fail("bad_name", name=name or "—")
    return name


def api(c, method, path, body=None):
    if not TOKEN:
        raise c.fail("no_token")
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        raise c.fail("api_failed", method=method, path=path, err=f"HTTP {e.code}")
    except urllib.error.URLError as e:
        raise c.fail("api_failed", method=method, path=path, err=str(e.reason))
    return json.loads(raw or b"null")


def items(c):
    res = api(c, "GET", "/api/items")
    return [x for x in res if isinstance(x, dict) and x.get("name")] if isinstance(res, list) else []


def item_add(c, name, dry):
    name = check_name(c, name)
    what = c.t("added", name=name)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", "/api/items", {"name": name})
    return what


def item_delete(c, name, dry):
    name = check_name(c, name)
    what = c.t("deleted", name=name)
    if dry:
        return c.t("would", what=what)
    api(c, "DELETE", "/api/items/" + urllib.parse.quote(name, safe=""))
    return what


# --- виды разделов ---------------------------------------------------------------

def items_view(c):
    rows = [{"cells": {"name": x["name"], "size": x.get("size", ""), "state": x.get("state", "")},
             "actions": [{"title": c.t("delete"), "op": "item_delete", "args": {"name": x["name"]}, "danger": True,
                          "confirm": c.t("delete_confirm", name=x["name"])}]} for x in items(c)]
    return {"type": "table", "title": c.t("items"), "rows": rows,
            "columns": [{"key": "name", "title": c.t("name")}, {"key": "size", "title": c.t("size")}, {"key": "state", "title": c.t("state")}],
            "actions": [{"title": c.t("add"), "op": "item_add",
                         "fields": [{"name": "name", "title": c.t("name"), "required": True, "hint": c.t("name_hint"), "placeholder": "alpha"}]}]}


def summary_view(c):
    lst = items(c)
    broken = [x for x in lst if x.get("state") == "broken"]
    rows = [{"cells": {"name": c.t("x_total"), "size": len(lst)}}, {"cells": {"name": c.t("x_broken"), "size": len(broken)}}]
    rows += [{"cells": {"name": c.t("f_broken", name=x["name"]), "size": c.t("f_broken_fix")}} for x in broken]
    return {"type": "table", "title": c.t("summary"), "rows": rows,
            "columns": [{"key": "name", "title": c.t("name")}, {"key": "size", "title": c.t("size")}]}


# --- команда сценария ------------------------------------------------------------

def command(c, argv, dry, check):
    """example list | example add ИМЯ | example rm ИМЯ; check — только
    разобрать («Проверить» в редакторе сценариев), ничего не вызывая."""
    if len(argv) == 2 and argv[1] == "list":
        return {"ok": True} if check else {"ok": True, "text": "\n".join(x["name"] for x in items(c))}
    if len(argv) == 3 and argv[1] in ("add", "rm"):
        check_name(c, argv[2])
        if check:
            return {"ok": True}
        return {"ok": True, "text": item_add(c, argv[2], dry) if argv[1] == "add" else item_delete(c, argv[2], dry)}
    raise Fail(c.t("usage"))


# --- манифест и вызовы -----------------------------------------------------------

def manifest(c):
    name = {"name": {"type": "string", "description": "item name: latin letters, digits, - and _"}}
    return {
        "protocol": 1, "name": "example", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "items", "title": c.t("items"), "read_only": True, "mcp": True, "description": "Items: name, size, state."},
            {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True, "description": "Totals and broken items."},
            {"name": "item_add", "title": c.t("add"), "mcp": True, "description": "Add an item by name.",
             "input": {"type": "object", "properties": name, "required": ["name"]}},
            {"name": "item_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete an item by name.",
             "input": {"type": "object", "properties": name, "required": ["name"]}},
        ],
        "pages": [{"id": "items", "title": c.t("items"), "op": "items"}, {"id": "summary", "title": c.t("summary"), "op": "summary"}],
        "commands": [{"word": "example", "usage": c.t("usage"), "example": "example add alpha\nexample rm alpha"}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if op == "items":
        return {"ok": True, "view": items_view(c)}
    if op == "summary":
        return {"ok": True, "view": summary_view(c)}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        return command(c, [str(a) for a in args.get("argv") or []], dry, False)
    if op == "item_add":
        text = item_add(c, args.get("name"), dry)
    elif op == "item_delete":
        text = item_delete(c, args.get("name"), dry)
    else:
        return {"ok": False, "error": f"no op {op}"}
    if not dry:
        log(f"{req.get('user', '')}: {op} {args.get('name')}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-example/" + VERSION

    def log_message(self, fmt, *a):
        pass  # строки доступа — не в журнал (в адресе могут быть аргументы)

    def reply(self, obj, code=200):
        raw = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def signed(self, body):
        """HMAC-SHA256 «отметка.путь.тело» секретом; отметка — не старше 300 с."""
        ts = self.headers.get("X-NKT-Timestamp", "")
        try:
            fresh = abs(time.time() - int(ts)) < 300
        except ValueError:
            return False
        path = urllib.parse.urlsplit(self.path).path
        want = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        return fresh and hmac.compare_digest(want, self.headers.get("X-NKT-Signature", ""))

    def do_GET(self):
        if not self.signed(b""):
            return self.reply({"error": "bad signature"}, 401)
        if urllib.parse.urlsplit(self.path).path != "/nkt/manifest":
            return self.reply({"error": "not found"}, 404)
        self.reply(manifest(Ctx(self.headers.get("X-NKT-Lang", "ru"))))

    def do_POST(self):
        size = int(self.headers.get("Content-Length") or 0)
        if size > 1 << 20:
            return self.reply({"error": "too large"}, 413)
        body = self.rfile.read(size)
        if not self.signed(body):
            return self.reply({"error": "bad signature"}, 401)
        if urllib.parse.urlsplit(self.path).path != "/nkt/call":
            return self.reply({"error": "not found"}, 404)
        c = Ctx(self.headers.get("X-NKT-Lang", "ru"))
        try:
            self.reply(handle(c, json.loads(body or b"{}")))
        except Fail as e:
            self.reply({"ok": False, "error": str(e)})
        except Exception as e:  # ошибка плагина — текстом хабу, а не обрывом; без подробностей запроса
            log(f"ошибка: {type(e).__name__}")
            self.reply({"ok": False, "error": f"plugin error: {type(e).__name__}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, {API}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
