"""rgstr-lite — учебный плагин хаба nkt к реестру образов rgstr.

Его по шагам собирает статья справки «Пишем плагин по шагам»; полный
плагин — examples/rgstr (каталог). Здесь только то, на чём видно всё
устройство плагина:

  шаг 1 — сервер с подписью HMAC и манифест;
  шаг 2 — раздел-сводка с находками;
  шаг 3 — таблица репозиториев и скрытый раздел тегов (кнопка «open»);
  шаг 4 — изменение с подтверждением и сухим прогоном (удалить тег, собрать мусор);
  шаг 5 — секрет: один раз окну (reveal) или в конвейер (store);
  шаг 6 — команда сценария (_check, _command).

Настройки (окружение службы): RGSTR_URL, RGSTR_USER, RGSTR_PASSWORD
(секрет), RGSTR_PUBLIC. Только стандартная библиотека Python.
"""
import base64
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

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9281") or 9281)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
URL = (os.environ.get("RGSTR_URL", "").strip() or "http://127.0.0.1:5000").rstrip("/")
USER = os.environ.get("RGSTR_USER", "").strip()
PASSWORD = os.environ.get("RGSTR_PASSWORD", "")
PUBLIC = os.environ.get("RGSTR_PUBLIC", "").strip().lower().rstrip("/") or urllib.parse.urlsplit(URL).netloc

# Все тексты — на двух языках; язык приходит заголовком X-NKT-Lang.
T = {
    "ru": {
        "title": "rgstr (учебный)", "desc": "Учебный плагин к реестру rgstr: сводка, репозитории и теги, сборка мусора, робот для конвейера.",
        "summary": "Сводка", "repos": "Репозитории", "tags": "Теги", "findings": "Находки", "param": "Параметр", "value": "Значение",
        "version": "Версия", "auth": "Вход", "on": "включён", "off": "выключен", "n_repos": "Репозиториев",
        "repo": "Репозиторий", "n_tags": "Тегов", "tag": "Тег", "size": "Размер, МБ", "open": "Теги", "delete": "Удалить",
        "confirm": "Удалить тег {repo}:{tag}?", "gc": "Собрать мусор", "robot": "Робот для конвейера", "f_name": "Имя", "f_project": "Проект",
        "f_auth_off": "Вход выключен: любой может загружать и удалять образы", "f_http": "Реестр по HTTP: {url}", "f_none": "Находок нет",
        "deleted": "{repo}:{tag} удалён", "gc_done": "освобождено {mb} МБ", "robot_done": "робот {name} создан",
        "secret": "Секрет робота {name}", "note": "docker login {host} -u '{name}' — показан один раз.",
        "would": "(сухой прогон) {what}", "checked": "проверено",
        "bad": "недопустимое значение: {v}", "need_pipeline": "из сценария и агенту секрет — только в конвейер: to_pipeline и env",
        "api": "rgstr {path}: {err}", "login": "rgstr: вход не принят — проверьте RGSTR_USER и RGSTR_PASSWORD",
        "usage": "rgstr-lite gc | rgstr-lite robot ИМЯ project ПРОЕКТ to-pipeline КОНВЕЙЕР env ИМЯ|@registry",
    },
    "en": {
        "title": "rgstr (tutorial)", "desc": "A tutorial plugin for the rgstr registry: summary, repositories and tags, garbage collection, a robot for a pipeline.",
        "summary": "Summary", "repos": "Repositories", "tags": "Tags", "findings": "Findings", "param": "Parameter", "value": "Value",
        "version": "Version", "auth": "Login", "on": "on", "off": "off", "n_repos": "Repositories",
        "repo": "Repository", "n_tags": "Tags", "tag": "Tag", "size": "Size, MB", "open": "Tags", "delete": "Delete",
        "confirm": "Delete the tag {repo}:{tag}?", "gc": "Collect garbage", "robot": "A robot for a pipeline", "f_name": "Name", "f_project": "Project",
        "f_auth_off": "Login is off: anyone can push and delete images", "f_http": "The registry over HTTP: {url}", "f_none": "No findings",
        "deleted": "{repo}:{tag} deleted", "gc_done": "freed {mb} MB", "robot_done": "robot {name} created",
        "secret": "Secret of the robot {name}", "note": "docker login {host} -u '{name}' — shown once.",
        "would": "(dry run) {what}", "checked": "checked",
        "bad": "invalid value: {v}", "need_pipeline": "from a script and to an agent the secret goes only into a pipeline: to_pipeline and env",
        "api": "rgstr {path}: {err}", "login": "rgstr: login rejected — check RGSTR_USER and RGSTR_PASSWORD",
        "usage": "rgstr-lite gc | rgstr-lite robot NAME project PROJECT to-pipeline PIPELINE env NAME|@registry",
    },
}


class Fail(Exception):
    """Ошибка для человека: уходит хабу как {ok: false, error}."""


class Ctx:
    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        return T[self.lang][key].format(**kw)


def log(msg):
    # Журнал службы: кто и что сделал — без секретов и тел запросов.
    print(f"nkt-plugin-rgstr-lite: {msg}", flush=True)


# --- проверка ввода: всё, что пришло снаружи, — до использования -----------------

REPO_RE = re.compile(r"^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$")
TAG_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$")
NAME_RE = re.compile(r"^[a-z0-9]+(?:[._-][a-z0-9]+)*$")
PIPE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
ENV_RE = re.compile(r"^[A-Z_][A-Z0-9_]{0,63}$")


def check(c, rx, v):
    v = str(v or "").strip()
    if not rx.match(v) or len(v) > 255:
        raise Fail(c.t("bad", v=v[:60] or "—"))
    return v


# --- API rgstr: Basic, JSON у каждого POST -----------------------------------------

def api(c, method, path, body=None, auth=True):
    headers = {"Accept": "application/json"}
    data = None
    if method == "POST":
        data, headers["Content-Type"] = json.dumps(body or {}).encode(), "application/json"
    if auth and USER:
        headers["Authorization"] = "Basic " + base64.b64encode(f"{USER}:{PASSWORD}".encode()).decode()
    try:
        with urllib.request.urlopen(urllib.request.Request(URL + path, data=data, method=method, headers=headers), timeout=120) as r:
            raw = r.read(16 << 20)
    except urllib.error.HTTPError as e:
        if e.code == 401:
            raise Fail(c.t("login"))
        raise Fail(c.t("api", path=path, err=f"HTTP {e.code}"))
    except urllib.error.URLError as e:
        raise Fail(c.t("api", path=path, err=str(e.reason)))
    return json.loads(raw) if raw.strip() else {}


# --- шаг 2: сводка с находками -------------------------------------------------------

def table(title, cols, rows, actions=None):
    return {"type": "table", "title": title, "columns": [{"key": k, "title": t} for k, t in cols], "rows": rows, "actions": actions or []}


def summary_view(c):
    health = api(c, "GET", "/healthz", auth=False)
    me = api(c, "GET", "/ui/api/me")
    repos = api(c, "GET", "/ui/api/repos").get("repositories") or []
    found = []
    if not me.get("auth_enabled"):
        found.append(c.t("f_auth_off"))
    if URL.startswith("http://") and urllib.parse.urlsplit(URL).hostname not in ("127.0.0.1", "localhost", "::1"):
        found.append(c.t("f_http", url=URL))
    params = [(c.t("version"), health.get("version", "")), (c.t("auth"), c.t("on") if me.get("auth_enabled") else c.t("off")), (c.t("n_repos"), len(repos))]
    return {"type": "sections", "items": [
        table(c.t("summary"), [("param", c.t("param")), ("value", c.t("value"))], [{"cells": {"param": k, "value": v}} for k, v in params]),
        table(c.t("findings"), [("value", c.t("findings"))], [{"cells": {"value": f}} for f in found or [c.t("f_none")]]),
    ]}


# --- шаг 3: таблица и скрытый раздел -------------------------------------------------

def repos_view(c):
    rows = [{"cells": {"repo": r["name"], "n_tags": len(r.get("tags") or [])}, "actions": [{"title": c.t("open"), "open": "tags", "args": {"repo": r["name"]}}]}
            for r in api(c, "GET", "/ui/api/repos").get("repositories") or [] if REPO_RE.match(str(r.get("name", "")))]
    robot = {"title": c.t("robot"), "op": "robot_create", "fields": [{"name": "name", "title": c.t("f_name"), "required": True},
                                                                      {"name": "project", "title": c.t("f_project"), "required": True}]}
    return table(c.t("repos"), [("repo", c.t("repo")), ("n_tags", c.t("n_tags"))], rows, [{"title": c.t("gc"), "op": "gc_run"}, robot])


def tags_view(c, repo):
    repo = check(c, REPO_RE, repo)
    rows = []
    for r in api(c, "GET", "/ui/api/repos").get("repositories") or []:
        if r.get("name") == repo:
            for t in r.get("tags") or []:
                rows.append({"cells": {"tag": t["name"], "size": round((t.get("size") or 0) / (1 << 20), 1)},
                             "actions": [{"title": c.t("delete"), "op": "tag_delete", "args": {"repo": repo, "tag": t["name"]}, "danger": True,
                                          "confirm": c.t("confirm", repo=repo, tag=t["name"])}]})
    return table(f"{c.t('tags')}: {repo}", [("tag", c.t("tag")), ("size", c.t("size"))], rows)


# --- шаг 4: изменения — с сухим прогоном --------------------------------------------

def tag_delete(c, args, dry):
    repo, tag = check(c, REPO_RE, args.get("repo")), check(c, TAG_RE, args.get("tag"))
    what = c.t("deleted", repo=repo, tag=tag)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", "/ui/api/tags/delete", {"repo": repo, "tags": [tag]})
    return what


def gc_run(c, dry):
    if dry:
        return c.t("would", what=c.t("gc"))
    run = api(c, "POST", "/ui/api/gc/run", {})
    return c.t("gc_done", mb=round((run.get("bytes_freed") or 0) / (1 << 20), 1))


# --- шаг 5: секрет — окну или в конвейер ---------------------------------------------

def robot_create(c, args, dry, agent):
    short, project = check(c, NAME_RE, args.get("name")), str(args.get("project") or "").strip()
    if project != "*":
        project = check(c, NAME_RE, project)
    pipe, env = str(args.get("to_pipeline") or "").strip(), str(args.get("env") or "").strip()
    if pipe or env or agent:
        if not PIPE_RE.match(pipe) or not (ENV_RE.match(env) or env == "@registry"):
            raise Fail(c.t("need_pipeline"))
    name = "robot$" + short
    if dry:
        return {"ok": True, "text": c.t("would", what=c.t("robot_done", name=name))}
    res = api(c, "POST", "/ui/api/robots/create", {"name": short, "permissions": [{"project": project, "actions": ["pull"]}]})
    secret = str(res.get("secret") or "")
    if pipe:
        store = {"pipeline": pipe, "env": env, "value": secret}
        if env == "@registry":
            store.update({"registry": PUBLIC, "user": name})
        return {"ok": True, "text": c.t("robot_done", name=name), "store": store}
    return {"ok": True, "text": c.t("robot_done", name=name),
            "reveal": {"title": c.t("secret", name=name), "value": secret, "note": c.t("note", host=PUBLIC, name=name)}}


# --- шаг 6: команда сценария --------------------------------------------------------

def command(c, argv, dry, only_check):
    a = argv[1:]
    if a == ["gc"]:
        return {"ok": True} if only_check else {"ok": True, "text": gc_run(c, dry)}
    if len(a) == 8 and a[0] == "robot" and a[2] == "project" and a[4] == "to-pipeline" and a[6] == "env":
        args = {"name": a[1], "project": a[3], "to_pipeline": a[5], "env": a[7]}
        if only_check:
            check(c, NAME_RE, a[1])
            return {"ok": True}
        return robot_create(c, args, dry, True)
    raise Fail(c.t("usage"))


# --- шаг 1: манифест и вызовы ---------------------------------------------------------

def manifest(c):
    s = {"type": "string"}
    return {
        "protocol": 1, "name": "rgstr-lite", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True, "description": "rgstr version, login state, repository count and findings."},
            {"name": "repos", "title": c.t("repos"), "read_only": True, "mcp": True, "description": "Repositories with tag counts."},
            {"name": "tags", "title": c.t("tags"), "read_only": True, "description": "Tags of a repository.",
             "input": {"type": "object", "properties": {"repo": s}, "required": ["repo"]}},
            {"name": "tag_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a tag.",
             "input": {"type": "object", "properties": {"repo": s, "tag": s}, "required": ["repo", "tag"]}},
            {"name": "gc_run", "title": c.t("gc"), "mcp": True, "description": "Run garbage collection."},
            {"name": "robot_create", "title": c.t("robot"), "mcp": True, "reveals": True,
             "description": "Create a pull robot for a project; for an agent the secret goes only into a pipeline (to_pipeline, env: a .env name or @registry).",
             "input": {"type": "object", "properties": {"name": s, "project": s, "to_pipeline": s, "env": s}, "required": ["name", "project", "to_pipeline", "env"]}},
        ],
        "pages": [{"id": "summary", "title": c.t("summary"), "op": "summary"}, {"id": "repos", "title": c.t("repos"), "op": "repos"},
                  {"id": "tags", "title": c.t("tags"), "op": "tags", "hidden": True}],
        "commands": [{"word": "rgstr-lite", "usage": c.t("usage"), "example": "rgstr-lite gc\nrgstr-lite robot ci project shop to-pipeline shop env @registry"}],
    }


def handle(c, req):
    op, args = req.get("op", ""), req.get("args") or {}
    dry = bool(req.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    views = {"summary": lambda: summary_view(c), "repos": lambda: repos_view(c), "tags": lambda: tags_view(c, args.get("repo"))}
    if op in views:
        return {"ok": True, "view": views[op]()}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("checked")}
    if op == "_command":
        out = command(c, [str(a) for a in args.get("argv") or []], dry, False)
        out.pop("reveal", None)
        return out
    if op == "robot_create":
        return robot_create(c, args, dry, False)
    changes = {"tag_delete": lambda: tag_delete(c, args, dry), "gc_run": lambda: gc_run(c, dry)}
    if op not in changes:
        return {"ok": False, "error": f"no op {op}"}
    text = changes[op]()
    if not dry:
        log(f"{req.get('user', '')}: {op}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-rgstr-lite/" + VERSION

    def log_message(self, fmt, *a):
        pass

    def reply(self, obj, code=200):
        raw = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def signed(self, body):
        # Подпись хаба: HMAC-SHA256 «отметка.путь.тело», отметка — не старше 5 минут.
        ts = self.headers.get("X-NKT-Timestamp", "")
        try:
            fresh = abs(time.time() - int(ts)) < 300
        except ValueError:
            return False
        want = hmac.new(SECRET.encode(), f"{ts}.{urllib.parse.urlsplit(self.path).path}.".encode() + body, hashlib.sha256).hexdigest()
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
        try:
            self.reply(handle(Ctx(self.headers.get("X-NKT-Lang", "ru")), json.loads(body or b"{}")))
        except Fail as e:
            self.reply({"ok": False, "error": str(e)})
        except Exception as e:  # неожиданное — текстом хабу, в журнал — только тип
            log(f"ошибка: {type(e).__name__}")
            self.reply({"ok": False, "error": f"plugin error: {type(e).__name__}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, {URL}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
