"""Плагин хаба nkt для rgstr — лёгкого реестра образов OCI/Docker
(https://github.com/piqab/rgstr).

Написан по examples/AGENTS.md из заготовки examples/_template. Ходит в API
веб-интерфейса rgstr (/ui/api/…) учётной записью администратора (Basic —
настройки RGSTR_USER, RGSTR_PASSWORD) и в /healthz. Что умеет: сводка с
находками (вход выключен, временный пароль, нет TLS, мало места, сборка
мусора выключена или с ошибкой, нет политик хранения, открытые метрики,
робот с правом записи во все репозитории), репозитории и теги (удалить
тег), проекты (создать с квотой, удалить пустой), роботы (создать,
перевыпустить секрет, удалить; секрет — один раз окну, в .env конвейера
или сразу ключом registry конвейера), пользователи, политики хранения и
неизменяемости (правка документом с диффом в хабе, запуск), сборка мусора,
вебхуки (проверка), журнал; команды сценариев «rgstr …», инструменты MCP.
Без зависимостей: только стандартная библиотека Python.
"""
import base64
import datetime
import hashlib
import hmac
import ipaddress
import json
import os
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9280") or 9280)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
URL = (os.environ.get("RGSTR_URL", "").strip() or "http://127.0.0.1:5000").rstrip("/")
USER = os.environ.get("RGSTR_USER", "").strip()
PASSWORD = os.environ.get("RGSTR_PASSWORD", "")
CA_FILE = os.environ.get("RGSTR_CA_FILE", "").strip()
# Адрес реестра для docker и конвейеров (registry.example.com); пусто —
# хост и порт из RGSTR_URL.
PUBLIC = os.environ.get("RGSTR_PUBLIC", "").strip().lower().rstrip("/")
GC_STALE_DAYS = 2

T = {
    "ru": {
        "title": "rgstr", "desc": "Реестр образов rgstr из хаба: репозитории и теги, проекты, роботы, политики хранения, сборка мусора, находки.",
        "summary": "Сводка", "repos": "Репозитории", "tags": "Теги", "projects": "Проекты", "robots": "Роботы", "users": "Пользователи",
        "policies": "Политики", "webhooks": "Вебхуки", "audit": "Журнал", "findings": "Находки",
        "param": "Параметр", "value": "Значение", "version": "Версия", "auth": "Вход", "on": "включён", "off": "выключен",
        "n_repos": "Репозиториев", "n_pulls": "Скачиваний", "disk": "Диск", "disk_val": "свободно {free} из {total}",
        "gc": "Сборка мусора", "gc_val": "каждые {every}; последняя — {last}, освобождено {freed}", "gc_off": "выключена",
        "repo": "Репозиторий", "n_tags": "Тегов", "size": "Размер", "pulls": "Скачиваний",
        "tag": "Тег", "digest": "Digest", "platforms": "Платформы", "pushed": "Загружен", "immutable": "Неизменяемый",
        "open_tags": "Теги", "delete": "Удалить", "tag_delete_confirm": "Удалить тег {repo}:{tag}? Образ останется, если на него ссылается другой тег; место освободит сборка мусора.",
        "project": "Проект", "public": "Открытый", "quota": "Квота", "used": "Занято", "members": "Участников",
        "project_create": "Создать проект", "f_name": "Имя", "f_desc": "Описание", "f_public": "Открытый (чтение без входа)", "f_quota": "Квота, ГБ",
        "f_quota_hint": "пусто — без квоты", "project_delete_confirm": "Удалить проект {name}? В нём не должно остаться репозиториев.",
        "robot": "Робот", "perms": "Права", "expires": "Истекает", "state": "Состояние", "disabled": "выключен", "expired": "истёк",
        "robot_create": "Создать робота", "f_project": "Проект", "f_project_hint": "* — все репозитории", "f_push": "Запись (push)",
        "f_expires": "Срок, дней", "f_expires_hint": "пусто — бессрочно", "robot_secret": "Новый секрет",
        "robot_secret_confirm": "Выпустить роботу {name} новый секрет? Прежний перестанет работать сразу.",
        "robot_delete_confirm": "Удалить робота {name}?", "secret_title": "Секрет робота {name}",
        "secret_note": "docker login {host} -u '{name}' — секрет показан один раз.",
        "role": "Роль", "source": "Источник", "must_change": "Сменить пароль",
        "rules_retention": "Политика хранения", "rules_immutable": "Неизменяемые теги", "repos_glob": "Репозитории", "tags_glob": "Теги",
        "keep_last": "Оставить последних", "keep_days": "Оставить дней", "as_text": "Править текстом", "retention_run": "Применить политику хранения",
        "retention_confirm": "Удалить теги, которые не оставляет политика хранения?",
        "gc_run": "Собрать мусор", "gc_confirm": "Запустить сборку мусора сейчас?",
        "hook": "Вебхук", "url": "Адрес", "events": "События", "hook_test": "Проверить",
        "time": "Время", "user": "Кто", "action": "Действие", "resource": "Объект",
        "yes": "да", "no": "нет", "never": "никогда",
        "f_auth_off": "Вход выключен (RGSTR_AUTH_ENABLED=false): любой может загружать, скачивать и удалять образы",
        "f_no_tls": "Реестр отвечает по HTTP ({url}): пароли и образы идут открыто — поставьте HTTPS (TLS rgstr или прокси)",
        "f_must_change": "У пользователя {name} временный пароль — сменить при первом входе",
        "f_disk_low": "Мало места: свободно {free} из {total}",
        "f_gc_off": "Сборка мусора по расписанию выключена — удалённые образы занимают место",
        "f_gc_err": "Последняя сборка мусора — с ошибкой: {err}",
        "f_gc_stale": "Сборка мусора не шла {days} дн.",
        "f_no_retention": "Нет политик хранения — старые теги копятся",
        "f_metrics": "Метрики /metrics открыты без входа",
        "f_robot_all": "Робот {name}: запись во все репозитории (*)",
        "f_delete_all": "Пользователь {name} с ролью user может удалять — задайте RGSTR_ADMINS",
        "f_none": "Находок нет",
        "deleted_tag": "{repo}:{tag} удалён", "gc_done": "сборка мусора: освобождено {freed}, блобов {blobs}, манифестов {mans}",
        "gc_would": "(сухой прогон) сборка мусора освободила бы {freed}, блобов {blobs}",
        "ret_done": "политика хранения: удалено тегов {n}", "ret_would": "(сухой прогон) политика хранения удалила бы тегов {n}",
        "project_created": "проект {name} создан", "project_deleted": "проект {name} удалён",
        "robot_created": "робот {name} создан", "robot_deleted": "робот {name} удалён", "robot_rotated": "роботу {name} выпущен новый секрет",
        "policies_saved": "политики сохранены: хранение — {r}, неизменяемые — {i}", "hook_tested": "вебхук {id}: ответ {status}",
        "would": "(сухой прогон) {what}", "check_ok": "проверено",
        "api_failed": "rgstr {method} {path}: {err}", "auth_failed": "rgstr: вход не принят — проверьте RGSTR_USER и RGSTR_PASSWORD (нужен администратор)",
        "must_change_err": "rgstr: у RGSTR_USER временный пароль — смените его в веб-интерфейсе rgstr (/ui)",
        "not_admin": "rgstr: RGSTR_USER — не администратор реестра", "no_user": "не задан RGSTR_USER — «Настройки» на странице плагина",
        "bad_repo": "репозиторий {v}: строчные латиница, цифры, . _ - и /", "bad_tag": "тег {v}: латиница, цифры, . _ - (до 128)",
        "bad_name": "имя {v}: строчные латиница, цифры, . _ - (до 64)", "bad_number": "{v}: нужно целое число",
        "bad_env": "переменная .env {v}: A-Z, 0-9, _ или @registry", "agent_needs_pipeline": "секрет робота агенту и сценарию — только в конвейер: to_pipeline и env (имя .env или @registry)",
        "bad_doc": "документ политик: {err}", "bad_id": "номер вебхука {v}",
        "usage": "rgstr repos | rgstr tags РЕПО | rgstr tag-delete РЕПО ТЕГ | rgstr gc | rgstr retention run | rgstr project ИМЯ [quota ГБ] | rgstr robot ИМЯ project ПРОЕКТ [push] to-pipeline КОНВЕЙЕР env ИМЯ|@registry",
    },
    "en": {
        "title": "rgstr", "desc": "The rgstr image registry from the hub: repositories and tags, projects, robots, retention policies, garbage collection, findings.",
        "summary": "Summary", "repos": "Repositories", "tags": "Tags", "projects": "Projects", "robots": "Robots", "users": "Users",
        "policies": "Policies", "webhooks": "Webhooks", "audit": "Audit log", "findings": "Findings",
        "param": "Parameter", "value": "Value", "version": "Version", "auth": "Login", "on": "on", "off": "off",
        "n_repos": "Repositories", "n_pulls": "Pulls", "disk": "Disk", "disk_val": "{free} free of {total}",
        "gc": "Garbage collection", "gc_val": "every {every}; last — {last}, freed {freed}", "gc_off": "off",
        "repo": "Repository", "n_tags": "Tags", "size": "Size", "pulls": "Pulls",
        "tag": "Tag", "digest": "Digest", "platforms": "Platforms", "pushed": "Pushed", "immutable": "Immutable",
        "open_tags": "Tags", "delete": "Delete", "tag_delete_confirm": "Delete the tag {repo}:{tag}? The image stays if another tag points to it; garbage collection frees the space.",
        "project": "Project", "public": "Public", "quota": "Quota", "used": "Used", "members": "Members",
        "project_create": "Create a project", "f_name": "Name", "f_desc": "Description", "f_public": "Public (pull without login)", "f_quota": "Quota, GB",
        "f_quota_hint": "empty — no quota", "project_delete_confirm": "Delete the project {name}? It must have no repositories left.",
        "robot": "Robot", "perms": "Permissions", "expires": "Expires", "state": "State", "disabled": "disabled", "expired": "expired",
        "robot_create": "Create a robot", "f_project": "Project", "f_project_hint": "* — all repositories", "f_push": "Write (push)",
        "f_expires": "Lifetime, days", "f_expires_hint": "empty — never expires", "robot_secret": "New secret",
        "robot_secret_confirm": "Issue a new secret to the robot {name}? The old one stops working at once.",
        "robot_delete_confirm": "Delete the robot {name}?", "secret_title": "Secret of the robot {name}",
        "secret_note": "docker login {host} -u '{name}' — the secret is shown once.",
        "role": "Role", "source": "Source", "must_change": "Must change password",
        "rules_retention": "Retention policy", "rules_immutable": "Immutable tags", "repos_glob": "Repositories", "tags_glob": "Tags",
        "keep_last": "Keep last", "keep_days": "Keep days", "as_text": "Edit as text", "retention_run": "Apply the retention policy",
        "retention_confirm": "Delete the tags the retention policy does not keep?",
        "gc_run": "Collect garbage", "gc_confirm": "Run garbage collection now?",
        "hook": "Webhook", "url": "Address", "events": "Events", "hook_test": "Test",
        "time": "Time", "user": "Who", "action": "Action", "resource": "Object",
        "yes": "yes", "no": "no", "never": "never",
        "f_auth_off": "Login is off (RGSTR_AUTH_ENABLED=false): anyone can push, pull and delete images",
        "f_no_tls": "The registry answers over HTTP ({url}): passwords and images travel in clear — set up HTTPS (rgstr TLS or a proxy)",
        "f_must_change": "User {name} has a temporary password — to be changed at first login",
        "f_disk_low": "Low disk space: {free} free of {total}",
        "f_gc_off": "Scheduled garbage collection is off — deleted images keep taking space",
        "f_gc_err": "The last garbage collection failed: {err}",
        "f_gc_stale": "Garbage collection has not run for {days} days",
        "f_no_retention": "No retention policies — old tags pile up",
        "f_metrics": "Metrics /metrics are open without login",
        "f_robot_all": "Robot {name}: write access to all repositories (*)",
        "f_delete_all": "User {name} with the user role can delete — set RGSTR_ADMINS",
        "f_none": "No findings",
        "deleted_tag": "{repo}:{tag} deleted", "gc_done": "garbage collection: freed {freed}, blobs {blobs}, manifests {mans}",
        "gc_would": "(dry run) garbage collection would free {freed}, blobs {blobs}",
        "ret_done": "retention policy: deleted tags {n}", "ret_would": "(dry run) the retention policy would delete tags {n}",
        "project_created": "project {name} created", "project_deleted": "project {name} deleted",
        "robot_created": "robot {name} created", "robot_deleted": "robot {name} deleted", "robot_rotated": "a new secret issued to the robot {name}",
        "policies_saved": "policies saved: retention — {r}, immutable — {i}", "hook_tested": "webhook {id}: answer {status}",
        "would": "(dry run) {what}", "check_ok": "checked",
        "api_failed": "rgstr {method} {path}: {err}", "auth_failed": "rgstr: login rejected — check RGSTR_USER and RGSTR_PASSWORD (an administrator is needed)",
        "must_change_err": "rgstr: RGSTR_USER has a temporary password — change it in the rgstr web UI (/ui)",
        "not_admin": "rgstr: RGSTR_USER is not a registry administrator", "no_user": "RGSTR_USER is not set — Settings on the plugin page",
        "bad_repo": "repository {v}: lowercase latin letters, digits, . _ - and /", "bad_tag": "tag {v}: latin letters, digits, . _ - (up to 128)",
        "bad_name": "name {v}: lowercase latin letters, digits, . _ - (up to 64)", "bad_number": "{v}: an integer is needed",
        "bad_env": "the .env variable {v}: A-Z, 0-9, _ or @registry", "agent_needs_pipeline": "a robot secret for an agent or a script goes only into a pipeline: to_pipeline and env (a .env name or @registry)",
        "bad_doc": "the policy document: {err}", "bad_id": "webhook number {v}",
        "usage": "rgstr repos | rgstr tags REPO | rgstr tag-delete REPO TAG | rgstr gc | rgstr retention run | rgstr project NAME [quota GB] | rgstr robot NAME project PROJECT [push] to-pipeline PIPELINE env NAME|@registry",
    },
}


class Fail(Exception):
    """Ошибка для человека: уходит хабу как {ok: false, error}."""


class Ctx:
    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        return T[self.lang][key].format(**kw)

    def fail(self, key, **kw):
        return Fail(self.t(key, **kw))


def log(msg):
    print(f"nkt-plugin-rgstr: {msg}", flush=True)


# --- проверка ввода ---------------------------------------------------------------

COMP = r"[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*"
REPO_RE = re.compile(rf"^{COMP}(?:/{COMP})*$")
TAG_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$")
NAME_RE = re.compile(r"^[a-z0-9]+(?:[._-][a-z0-9]+)*$")
ENV_RE = re.compile(r"^[A-Z_][A-Z0-9_]{0,63}$")
PIPE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
HOOK_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
STORE_REGISTRY = "@registry"


def check(c, rx, value, key, limit=255):
    v = str(value or "").strip()
    if not rx.match(v) or len(v) > limit:
        raise c.fail(key, v=v[:80] or "—")
    return v


def check_repo(c, v):
    return check(c, REPO_RE, v, "bad_repo", 255)


def check_tag(c, v):
    return check(c, TAG_RE, v, "bad_tag", 128)


def check_name(c, v):
    return check(c, NAME_RE, str(v or "").strip().removeprefix("robot$"), "bad_name", 64)


def to_int(c, v, default=0):
    if v in (None, ""):
        return default
    try:
        n = int(str(v).strip())
    except ValueError:
        raise c.fail("bad_number", v=str(v)[:40])
    if n < 0 or n > 10 ** 9:
        raise c.fail("bad_number", v=str(v)[:40])
    return n


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


# --- API rgstr --------------------------------------------------------------------

def tls_context():
    if not URL.startswith("https://"):
        return None
    ctx = ssl.create_default_context()
    if CA_FILE:
        ctx.load_verify_locations(cafile=CA_FILE)
    return ctx


def request(c, method, path, body=None, params=None, auth=True, raw=False):
    url = URL + path + ("?" + urllib.parse.urlencode(params) if params else "")
    headers = {"Accept": "application/json"}
    data = None
    if body is not None or method == "POST":
        # rgstr ждёт JSON у каждого POST — хотя бы {}.
        data = json.dumps(body if body is not None else {}).encode()
        headers["Content-Type"] = "application/json"
    if auth and USER:
        headers["Authorization"] = "Basic " + base64.b64encode(f"{USER}:{PASSWORD}".encode()).decode()
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=120, context=tls_context()) as r:
            out = r.read(16 << 20)
            status = r.status
    except urllib.error.HTTPError as e:
        detail = ""
        try:
            doc = json.loads(e.read(4096) or b"{}")
            detail = str(doc.get("error") or "")[:200]
            if doc.get("must_change"):
                raise c.fail("must_change_err")
        except (ValueError, AttributeError):
            pass
        if raw:
            return e.code, b""
        if e.code == 401:
            raise c.fail("auth_failed")
        if e.code == 403 and "administrator" in detail:
            raise c.fail("not_admin")
        raise c.fail("api_failed", method=method, path=path, err=f"HTTP {e.code}" + (f": {detail}" if detail else ""))
    except urllib.error.URLError as e:
        raise c.fail("api_failed", method=method, path=path, err=str(e.reason))
    if raw:
        return status, out
    return json.loads(out) if out.strip() else None


def api(c, method, path, body=None, params=None):
    return request(c, method, path, body, params)


def need_admin(c):
    """Учётка плагина — администратор (при включённом входе)."""
    me = request(c, "GET", "/ui/api/me") or {}
    if me.get("auth_enabled") and not USER:
        raise c.fail("no_user")
    if me.get("auth_enabled") and not me.get("admin"):
        raise c.fail("not_admin" if me.get("user") else "auth_failed")
    return me


def human(n):
    n = float(n or 0)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if n < 1024 or unit == "TB":
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024
    return f"{n:.1f} TB"


def parse_time(s):
    try:
        t = datetime.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
        return t if t.tzinfo else t.replace(tzinfo=datetime.timezone.utc)
    except ValueError:
        return None


def short_time(s):
    t = parse_time(s)
    return t.strftime("%Y-%m-%d %H:%M") if t and t.year > 1970 else ""


def registry_host():
    """Адрес реестра для docker login и ключей конвейеров."""
    if PUBLIC:
        return PUBLIC.removeprefix("https://").removeprefix("http://")
    return urllib.parse.urlsplit(URL).netloc


def loopback_url():
    host = urllib.parse.urlsplit(URL).hostname or ""
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


# --- виды -------------------------------------------------------------------------

def table(title, cols, rows, actions=None, note=""):
    return {"type": "table", "title": title, "note": note, "columns": [{"key": k, "title": t} for k, t in cols], "rows": rows, "actions": actions or []}


def findings(c, me):
    out = []
    if not me.get("auth_enabled"):
        out.append(c.t("f_auth_off"))
    if URL.startswith("http://") and not loopback_url():
        out.append(c.t("f_no_tls", url=URL))
    if me.get("auth_enabled"):
        us = (api(c, "GET", "/ui/api/users") or {}).get("users") or []
        for u in us:
            if u.get("must_change"):
                out.append(c.t("f_must_change", name=u.get("name", "")))
            if u.get("role") == "user" and "delete" in (u.get("actions") or []):
                out.append(c.t("f_delete_all", name=u.get("name", "")))
        for r in (api(c, "GET", "/ui/api/robots") or {}).get("robots") or []:
            if r.get("disabled") or r.get("expired"):
                continue
            for p in r.get("permissions") or []:
                if p.get("project") == "*" and "push" in (p.get("actions") or []):
                    out.append(c.t("f_robot_all", name=r.get("name", "")))
    disk = api(c, "GET", "/ui/api/disk") or {}
    if disk.get("low"):
        out.append(c.t("f_disk_low", free=human(disk.get("free")), total=human(disk.get("total"))))
    gc = api(c, "GET", "/ui/api/gc") or {}
    hist = gc.get("history") or []
    if gc.get("interval") in ("0s", "0"):
        out.append(c.t("f_gc_off"))
    if hist and hist[0].get("error"):
        out.append(c.t("f_gc_err", err=str(hist[0]["error"])[:200]))
    elif hist:
        t = parse_time(hist[0].get("start"))
        days = (datetime.datetime.now(datetime.timezone.utc) - t).days if t else 0
        if days >= GC_STALE_DAYS:
            out.append(c.t("f_gc_stale", days=days))
    pol = api(c, "GET", "/ui/api/policies") or {}
    if not [r for r in pol.get("retention") or [] if not r.get("disabled")]:
        out.append(c.t("f_no_retention"))
    status, _ = request(c, "GET", "/metrics", auth=False, raw=True)
    if status == 200:
        out.append(c.t("f_metrics"))
    return out, disk, gc


def summary_view(c):
    health = request(c, "GET", "/healthz", auth=False) or {}
    me = need_admin(c)
    repos = api(c, "GET", "/ui/api/repos") or {}
    found, disk, gc = findings(c, me)
    hist = gc.get("history") or []
    last = short_time(hist[0].get("start")) if hist else c.t("never")
    gc_val = c.t("gc_off") if gc.get("interval") in ("0s", "0") else c.t("gc_val", every=gc.get("interval", ""), last=last,
                                                                           freed=human(hist[0].get("bytes_freed")) if hist else "—")
    params = [(c.t("version"), health.get("version", "")), (c.t("auth"), c.t("on") if me.get("auth_enabled") else c.t("off")),
              (c.t("n_repos"), len(repos.get("repositories") or [])), (c.t("n_pulls"), repos.get("total_pulls", 0)),
              (c.t("disk"), c.t("disk_val", free=human(disk.get("free")), total=human(disk.get("total"))) if disk.get("supported", True) else "—"),
              (c.t("gc"), gc_val)]
    return {"type": "sections", "items": [
        table(c.t("summary"), [("param", c.t("param")), ("value", c.t("value"))], [{"cells": {"param": k, "value": v}} for k, v in params]),
        table(c.t("findings"), [("value", c.t("findings"))], [{"cells": {"value": f}} for f in found] or [{"cells": {"value": c.t("f_none")}}]),
    ]}


def repos_view(c):
    rows = []
    for r in sorted((api(c, "GET", "/ui/api/repos") or {}).get("repositories") or [], key=lambda r: str(r.get("name"))):
        name = str(r.get("name", ""))
        if not REPO_RE.match(name):
            continue
        tags = r.get("tags") or []
        rows.append({"cells": {"repo": name, "n_tags": len(tags), "size": human(sum(int(t.get("size") or 0) for t in tags)), "pulls": r.get("pulls", 0)},
                     "actions": [{"title": c.t("open_tags"), "open": "tags", "args": {"repo": name}}]})
    return table(c.t("repos"), [("repo", c.t("repo")), ("n_tags", c.t("n_tags")), ("size", c.t("size")), ("pulls", c.t("pulls"))], rows,
                 [{"title": c.t("gc_run"), "op": "gc_run", "confirm": c.t("gc_confirm")}])


def tags_view(c, repo):
    repo = check_repo(c, repo)
    rows = []
    for r in (api(c, "GET", "/ui/api/repos") or {}).get("repositories") or []:
        if r.get("name") != repo:
            continue
        for t in sorted(r.get("tags") or [], key=lambda t: str(t.get("pushed_at") or ""), reverse=True):
            tag = str(t.get("name", ""))
            plats = ", ".join(f"{p.get('os')}/{p.get('architecture')}" for p in t.get("platforms") or [])
            actions = [] if t.get("immutable") else [{"title": c.t("delete"), "op": "tag_delete", "args": {"repo": repo, "tag": tag}, "danger": True,
                                                       "confirm": c.t("tag_delete_confirm", repo=repo, tag=tag)}]
            rows.append({"cells": {"tag": tag, "digest": str(t.get("digest") or "")[:19], "size": human(t.get("size")), "platforms": plats,
                                   "pushed": short_time(t.get("pushed_at")), "immutable": c.t("yes") if t.get("immutable") else ""}, "actions": actions})
    return table(f"{c.t('tags')}: {repo}", [("tag", c.t("tag")), ("digest", c.t("digest")), ("size", c.t("size")), ("platforms", c.t("platforms")),
                                            ("pushed", c.t("pushed")), ("immutable", c.t("immutable"))], rows)


def projects_view(c):
    rows = []
    for p in (api(c, "GET", "/ui/api/projects") or {}).get("projects") or []:
        name = str(p.get("name", ""))
        rows.append({"cells": {"project": name, "public": c.t("yes") if p.get("public") else "", "quota": human(p["quota_bytes"]) if p.get("quota_bytes") else "—",
                               "used": human(p.get("used_bytes")), "members": p.get("members", 0), "n_repos": p.get("repos", 0)},
                     "actions": [{"title": c.t("delete"), "op": "project_delete", "args": {"name": name}, "danger": True,
                                  "confirm": c.t("project_delete_confirm", name=name)}]})
    create = {"title": c.t("project_create"), "op": "project_create", "fields": [
        {"name": "name", "title": c.t("f_name"), "required": True, "placeholder": "team-a"},
        {"name": "description", "title": c.t("f_desc")},
        {"name": "public", "title": c.t("f_public"), "type": "bool"},
        {"name": "quota_gb", "title": c.t("f_quota"), "type": "number", "hint": c.t("f_quota_hint")}]}
    return table(c.t("projects"), [("project", c.t("project")), ("public", c.t("public")), ("quota", c.t("quota")), ("used", c.t("used")),
                                   ("members", c.t("members")), ("n_repos", c.t("repos"))], rows, [create])


def robots_view(c):
    rows = []
    for r in (api(c, "GET", "/ui/api/robots") or {}).get("robots") or []:
        name = str(r.get("name", ""))
        perms = "; ".join(f"{p.get('project')}: {','.join(p.get('actions') or [])}" for p in r.get("permissions") or [])
        state = c.t("disabled") if r.get("disabled") else c.t("expired") if r.get("expired") else ""
        rows.append({"cells": {"robot": name, "perms": perms, "expires": short_time(r.get("expires_at")) or c.t("never"), "state": state},
                     "actions": [{"title": c.t("robot_secret"), "op": "robot_secret", "args": {"name": name}, "confirm": c.t("robot_secret_confirm", name=name)},
                                 {"title": c.t("delete"), "op": "robot_delete", "args": {"name": name}, "danger": True, "confirm": c.t("robot_delete_confirm", name=name)}]})
    create = {"title": c.t("robot_create"), "op": "robot_create", "fields": [
        {"name": "name", "title": c.t("f_name"), "required": True, "placeholder": "ci-shop"},
        {"name": "project", "title": c.t("f_project"), "required": True, "hint": c.t("f_project_hint"), "placeholder": "team-a"},
        {"name": "push", "title": c.t("f_push"), "type": "bool"},
        {"name": "expires_days", "title": c.t("f_expires"), "type": "number", "hint": c.t("f_expires_hint")}]}
    return table(c.t("robots"), [("robot", c.t("robot")), ("perms", c.t("perms")), ("expires", c.t("expires")), ("state", c.t("state"))], rows, [create])


def users_view(c):
    us = (api(c, "GET", "/ui/api/users") or {}).get("users") or []
    rows = [{"cells": {"user": u.get("name", ""), "role": u.get("role", ""), "source": u.get("source", ""), "must_change": c.t("yes") if u.get("must_change") else ""}}
            for u in sorted(us, key=lambda u: (u.get("role") != "admin", str(u.get("name"))))]
    return table(c.t("users"), [("user", c.t("user")), ("role", c.t("role")), ("source", c.t("source")), ("must_change", c.t("must_change"))], rows)


def policies_view(c):
    pol = api(c, "GET", "/ui/api/policies") or {}
    ret = [{"cells": {"repos": r.get("repos", ""), "tags": r.get("tags", ""), "keep_last": r.get("keep_last", 0), "keep_days": r.get("keep_days", 0),
                      "state": c.t("disabled") if r.get("disabled") else ""}} for r in pol.get("retention") or []]
    imm = [{"cells": {"repos": r.get("repos", ""), "tags": r.get("tags", ""), "state": c.t("disabled") if r.get("disabled") else ""}} for r in pol.get("immutable") or []]
    edit = {"title": c.t("as_text"), "edit": {"get": "policies_get", "put": "policies_put", "doc": "policies"}}
    run = {"title": c.t("retention_run"), "op": "retention_run", "confirm": c.t("retention_confirm")}
    return {"type": "sections", "items": [
        table(c.t("rules_retention"), [("repos", c.t("repos_glob")), ("tags", c.t("tags_glob")), ("keep_last", c.t("keep_last")), ("keep_days", c.t("keep_days")),
                                       ("state", c.t("state"))], ret, [edit, run]),
        table(c.t("rules_immutable"), [("repos", c.t("repos_glob")), ("tags", c.t("tags_glob")), ("state", c.t("state"))], imm),
    ]}


def webhooks_view(c):
    rows = []
    for h in (api(c, "GET", "/ui/api/webhooks") or {}).get("webhooks") or []:
        hid = str(h.get("id", ""))
        rows.append({"cells": {"hook": h.get("name", ""), "url": h.get("url", ""), "project": h.get("project") or "*", "events": ", ".join(h.get("events") or []),
                               "state": c.t("disabled") if h.get("disabled") else ""},
                     "actions": [{"title": c.t("hook_test"), "op": "webhook_test", "args": {"id": hid}}]})
    return table(c.t("webhooks"), [("hook", c.t("hook")), ("url", c.t("url")), ("project", c.t("project")), ("events", c.t("events")), ("state", c.t("state"))], rows)


def audit_view(c):
    ents = (api(c, "GET", "/ui/api/audit", params={"limit": 200}) or {}).get("entries") or []
    rows = [{"cells": {"time": short_time(e.get("time")), "user": e.get("user") or "anonymous", "action": e.get("action", ""),
                       "resource": e.get("resource", ""), "value": e.get("error") or e.get("detail") or ""}} for e in ents]
    return table(c.t("audit"), [("time", c.t("time")), ("user", c.t("user")), ("action", c.t("action")), ("resource", c.t("resource")), ("value", c.t("value"))], rows)


# --- изменения ---------------------------------------------------------------------

def tag_delete(c, args, dry):
    repo, tag = check_repo(c, args.get("repo")), check_tag(c, args.get("tag"))
    what = c.t("deleted_tag", repo=repo, tag=tag)
    if dry:
        return c.t("would", what=what)
    res = api(c, "POST", "/ui/api/tags/delete", {"repo": repo, "tags": [tag], "free_space": truthy(args.get("free_space"))}) or {}
    for r in res.get("results") or []:
        if r.get("error"):
            raise c.fail("api_failed", method="POST", path="/ui/api/tags/delete", err=str(r["error"])[:200])
    return what


def gc_run(c, dry):
    need_admin(c)
    run = api(c, "POST", "/ui/api/gc/run", {"dry_run": dry}) or {}
    if run.get("error"):
        raise c.fail("f_gc_err", err=str(run["error"])[:200])
    key = "gc_would" if dry else "gc_done"
    return c.t(key, freed=human(run.get("bytes_freed")), blobs=run.get("blobs_removed", 0), mans=run.get("manifests_removed", 0))


def retention_run(c, dry):
    need_admin(c)
    run = api(c, "POST", "/ui/api/policies/retention/run", {"dry_run": dry}) or {}
    if run.get("error"):
        raise c.fail("api_failed", method="POST", path="/ui/api/policies/retention/run", err=str(run["error"])[:200])
    return c.t("ret_would" if dry else "ret_done", n=run.get("tags_count", 0))


def project_create(c, args, dry):
    name = check_name(c, args.get("name"))
    quota = to_int(c, args.get("quota_gb"))
    what = c.t("project_created", name=name)
    if dry:
        return c.t("would", what=what)
    body = {"name": name, "description": str(args.get("description") or "")[:300], "public": truthy(args.get("public"))}
    if quota:
        body["quota_bytes"] = quota << 30
    api(c, "POST", "/ui/api/projects/create", body)
    return what


def project_delete(c, args, dry):
    name = check_name(c, args.get("name"))
    what = c.t("project_deleted", name=name)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", "/ui/api/projects/delete", {"name": name})
    return what


def store_target(c, args, agent):
    """Куда отдать секрет: окну (reveal) или в конвейер (store) — .env или
    ключ registry; агенту и сценарию — только в конвейер."""
    pipe, env = str(args.get("to_pipeline") or "").strip(), str(args.get("env") or "").strip()
    if pipe or env or agent:
        if not (pipe and env):
            raise c.fail("agent_needs_pipeline")
        if not PIPE_RE.match(pipe) or not (ENV_RE.match(env) or env == STORE_REGISTRY):
            raise c.fail("bad_env", v=env)
    return pipe, env


def secret_answer(c, name, secret, pipe, env, text):
    if pipe:
        store = {"pipeline": pipe, "env": env, "value": secret}
        if env == STORE_REGISTRY:
            store.update({"registry": registry_host(), "user": name})
        return {"ok": True, "text": text, "store": store}
    return {"ok": True, "text": text, "reveal": {"title": c.t("secret_title", name=name), "value": secret,
                                                 "note": c.t("secret_note", host=registry_host(), name=name)}}


def robot_create(c, args, dry, agent):
    short = check_name(c, args.get("name"))
    project = str(args.get("project") or "").strip()
    if project != "*":
        project = check_name(c, project)
    actions = ["pull", "push"] if truthy(args.get("push")) else ["pull"]
    days = to_int(c, args.get("expires_days"))
    pipe, env = store_target(c, args, agent)
    name = "robot$" + short
    if dry:
        return {"ok": True, "text": c.t("would", what=c.t("robot_created", name=name) + (f" → {pipe} ({env})" if pipe else ""))}
    body = {"name": short, "description": "nkt", "permissions": [{"project": project, "actions": actions}], "expires_days": days or None}
    res = api(c, "POST", "/ui/api/robots/create", body) or {}
    name, secret = str(res.get("name") or name), str(res.get("secret") or "")
    return secret_answer(c, name, secret, pipe, env, c.t("robot_created", name=name))


def robot_secret(c, args, dry, agent):
    name = "robot$" + check_name(c, args.get("name"))
    pipe, env = store_target(c, args, agent)
    if dry:
        return {"ok": True, "text": c.t("would", what=c.t("robot_rotated", name=name))}
    res = api(c, "POST", "/ui/api/robots/secret", {"name": name}) or {}
    return secret_answer(c, name, str(res.get("secret") or ""), pipe, env, c.t("robot_rotated", name=name))


def robot_delete(c, args, dry):
    name = "robot$" + check_name(c, args.get("name"))
    what = c.t("robot_deleted", name=name)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", "/ui/api/robots/delete", {"name": name})
    return what


def policies_get(c):
    pol = api(c, "GET", "/ui/api/policies") or {}
    doc = {"retention": pol.get("retention") or [], "immutable": pol.get("immutable") or []}
    return {"ok": True, "result": {"text": json.dumps(doc, ensure_ascii=False, indent=2) + "\n"}}


def policies_put(c, args, dry):
    try:
        doc = json.loads(str(args.get("text") or ""))
    except ValueError as e:
        raise c.fail("bad_doc", err=str(e)[:120])
    if not isinstance(doc, dict) or not isinstance(doc.get("retention", []), list) or not isinstance(doc.get("immutable", []), list):
        raise c.fail("bad_doc", err="{retention: [...], immutable: [...]}")
    clean = {"retention": [], "immutable": []}
    for r in doc.get("retention") or []:
        if not isinstance(r, dict):
            raise c.fail("bad_doc", err="retention")
        clean["retention"].append({"repos": str(r.get("repos") or "")[:200], "tags": str(r.get("tags") or "")[:200],
                                   "keep_last": to_int(c, r.get("keep_last")), "keep_days": to_int(c, r.get("keep_days")), "disabled": truthy(r.get("disabled"))})
    for r in doc.get("immutable") or []:
        if not isinstance(r, dict):
            raise c.fail("bad_doc", err="immutable")
        clean["immutable"].append({"repos": str(r.get("repos") or "")[:200], "tags": str(r.get("tags") or "")[:200], "disabled": truthy(r.get("disabled"))})
    what = c.t("policies_saved", r=len(clean["retention"]), i=len(clean["immutable"]))
    if dry:
        return c.t("would", what=what)
    api(c, "POST", "/ui/api/policies/retention", {"rules": clean["retention"]})
    api(c, "POST", "/ui/api/policies/immutable", {"rules": clean["immutable"]})
    return what


def webhook_test(c, args, dry):
    hid = check(c, HOOK_RE, args.get("id"), "bad_id", 64)
    if dry:
        return c.t("would", what=c.t("hook_test") + " " + hid)
    d = api(c, "POST", "/ui/api/webhooks/test", {"id": hid}) or {}
    return c.t("hook_tested", id=hid, status=d.get("status") or d.get("error") or "—")


# --- команда сценария -------------------------------------------------------------

def command(c, argv, dry, check_only):
    a = argv[1:]
    if a == ["repos"]:
        return {"ok": True} if check_only else {"ok": True, "text": "\n".join(r.get("name", "") for r in (api(c, "GET", "/ui/api/repos") or {}).get("repositories") or [])}
    if len(a) == 2 and a[0] == "tags":
        repo = check_repo(c, a[1])
        if check_only:
            return {"ok": True}
        for r in (api(c, "GET", "/ui/api/repos") or {}).get("repositories") or []:
            if r.get("name") == repo:
                return {"ok": True, "text": "\n".join(t.get("name", "") for t in r.get("tags") or [])}
        return {"ok": True, "text": ""}
    if len(a) == 3 and a[0] == "tag-delete":
        args = {"repo": check_repo(c, a[1]), "tag": check_tag(c, a[2])}
        return {"ok": True} if check_only else {"ok": True, "text": tag_delete(c, args, dry)}
    if a == ["gc"]:
        return {"ok": True} if check_only else {"ok": True, "text": gc_run(c, dry)}
    if a == ["retention", "run"]:
        return {"ok": True} if check_only else {"ok": True, "text": retention_run(c, dry)}
    if len(a) in (2, 4) and a[0] == "project":
        args = {"name": check_name(c, a[1])}
        if len(a) == 4:
            if a[2] != "quota":
                raise Fail(c.t("usage"))
            args["quota_gb"] = to_int(c, a[3])
        return {"ok": True} if check_only else {"ok": True, "text": project_create(c, args, dry)}
    if len(a) >= 8 and a[0] == "robot" and a[2] == "project":
        rest = a[4:]
        push = bool(rest) and rest[0] == "push"
        if push:
            rest = rest[1:]
        if len(rest) != 4 or rest[0] != "to-pipeline" or rest[2] != "env":
            raise Fail(c.t("usage"))
        args = {"name": a[1], "project": a[3], "push": push, "to_pipeline": rest[1], "env": rest[3]}
        check_name(c, a[1])
        if a[3] != "*":
            check_name(c, a[3])
        store_target(c, args, True)
        return {"ok": True} if check_only else robot_create(c, args, dry, True)
    raise Fail(c.t("usage"))


# --- манифест и вызовы -----------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}
B = {"type": "boolean"}
N = {"type": "integer"}
STORE = {"to_pipeline": {"type": "string", "description": "pipeline name"},
         "env": {"type": "string", "description": "a .env variable name, or @registry to save as the pipeline's registry key"}}


def manifest(c):
    return {
        "protocol": 1, "name": "rgstr", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True,
             "description": "rgstr version, repository and pull counts, disk, garbage collection and findings: login off, no TLS, temporary passwords, low disk, GC off or failed, no retention policy, open metrics, robots with push to all repositories."},
            {"name": "repos", "title": c.t("repos"), "read_only": True, "mcp": True, "description": "Repositories: tag count, size, pulls."},
            {"name": "tags", "title": c.t("tags"), "read_only": True, "mcp": True, "description": "Tags of a repository: digest, size, platforms, push time, immutable.",
             "input": schema({"repo": S}, ["repo"])},
            {"name": "projects", "title": c.t("projects"), "read_only": True, "mcp": True, "description": "Projects: public, quota, used space, members, repositories."},
            {"name": "robots", "title": c.t("robots"), "read_only": True, "mcp": True, "description": "Robot accounts: permissions per project, expiry, state (secrets are never listed)."},
            {"name": "users", "title": c.t("users"), "read_only": True, "mcp": True, "description": "Users: role, source, must change password."},
            {"name": "policies", "title": c.t("policies"), "read_only": True, "mcp": True, "description": "Retention and immutability rules."},
            {"name": "webhooks", "title": c.t("webhooks"), "read_only": True, "description": "Registry webhooks."},
            {"name": "audit", "title": c.t("audit"), "read_only": True, "description": "The registry audit log (last 200 entries)."},
            {"name": "policies_get", "title": c.t("as_text"), "read_only": True, "description": "Policies as a JSON document."},
            {"name": "policies_put", "title": c.t("as_text"), "description": "Replace the policies from a JSON document {retention: [...], immutable: [...]}.",
             "input": schema({"text": S}, ["text"])},
            {"name": "tag_delete", "title": c.t("delete"), "mcp": True, "destructive": True,
             "description": "Delete a tag (free_space=true also runs garbage collection for an admin).", "input": schema({"repo": S, "tag": S, "free_space": B}, ["repo", "tag"])},
            {"name": "gc_run", "title": c.t("gc_run"), "mcp": True, "description": "Run garbage collection now; with dry_run it reports what would be freed."},
            {"name": "retention_run", "title": c.t("retention_run"), "mcp": True, "destructive": True,
             "description": "Apply the retention policy now (deletes tags it does not keep); with dry_run it counts them."},
            {"name": "project_create", "title": c.t("project_create"), "mcp": True, "description": "Create a project (repositories named project/...), optionally public and with a quota in GB.",
             "input": schema({"name": S, "description": S, "public": B, "quota_gb": N}, ["name"])},
            {"name": "project_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete an empty project.", "input": schema({"name": S}, ["name"])},
            {"name": "robot_create", "title": c.t("robot_create"), "mcp": True, "reveals": True,
             "description": "Create a robot account for a project (* — all repositories), pull or pull+push, optional lifetime. For an agent the secret goes only into a pipeline: to_pipeline and env (a .env name, or @registry — the pipeline's registry key for this registry); the value is never returned.",
             "input": schema({"name": S, "project": S, "push": B, "expires_days": N, **STORE}, ["name", "project", "to_pipeline", "env"])},
            {"name": "robot_secret", "title": c.t("robot_secret"), "mcp": True, "reveals": True,
             "description": "Issue a new robot secret (the old one stops working). For an agent only into a pipeline: to_pipeline and env.",
             "input": schema({"name": S, **STORE}, ["name", "to_pipeline", "env"])},
            {"name": "robot_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a robot account.", "input": schema({"name": S}, ["name"])},
            {"name": "webhook_test", "title": c.t("hook_test"), "description": "Send a test ping to a webhook.", "input": schema({"id": S}, ["id"])},
        ],
        "pages": [
            {"id": "summary", "title": c.t("summary"), "op": "summary"},
            {"id": "repos", "title": c.t("repos"), "op": "repos"},
            {"id": "projects", "title": c.t("projects"), "op": "projects"},
            {"id": "robots", "title": c.t("robots"), "op": "robots"},
            {"id": "policies", "title": c.t("policies"), "op": "policies"},
            {"id": "users", "title": c.t("users"), "op": "users"},
            {"id": "webhooks", "title": c.t("webhooks"), "op": "webhooks"},
            {"id": "audit", "title": c.t("audit"), "op": "audit"},
            {"id": "tags", "title": c.t("tags"), "op": "tags", "hidden": True},
        ],
        "commands": [{"word": "rgstr", "usage": c.t("usage"),
                      "example": "rgstr gc\nrgstr project shop quota 20\nrgstr robot ci-shop project shop push to-pipeline shop env @registry"}],
    }


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    views = {"summary": lambda: summary_view(c), "repos": lambda: repos_view(c), "tags": lambda: tags_view(c, args.get("repo")),
             "projects": lambda: projects_view(c), "robots": lambda: robots_view(c), "users": lambda: users_view(c),
             "policies": lambda: policies_view(c), "webhooks": lambda: webhooks_view(c), "audit": lambda: audit_view(c)}
    if op in views:
        return {"ok": True, "view": views[op]()}
    if op == "policies_get":
        return policies_get(c)
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        out = command(c, [str(a) for a in args.get("argv") or []], dry, False)
        out.pop("reveal", None)  # из сценария — только в конвейер
        return out
    if op in ("robot_create", "robot_secret"):
        out = (robot_create if op == "robot_create" else robot_secret)(c, args, dry, False)
        if not dry:
            log(f"{req.get('user', '')}: {op} {args.get('name', '')}")
        return out
    changes = {"tag_delete": lambda: tag_delete(c, args, dry), "gc_run": lambda: gc_run(c, dry), "retention_run": lambda: retention_run(c, dry),
               "project_create": lambda: project_create(c, args, dry), "project_delete": lambda: project_delete(c, args, dry),
               "robot_delete": lambda: robot_delete(c, args, dry), "policies_put": lambda: policies_put(c, args, dry),
               "webhook_test": lambda: webhook_test(c, args, dry)}
    if op not in changes:
        return {"ok": False, "error": f"no op {op}"}
    text = changes[op]()
    if not dry:
        log(f"{req.get('user', '')}: {op} {args.get('repo') or args.get('name') or ''}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-rgstr/" + VERSION

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
        except Exception as e:  # ошибка плагина — текстом хабу, а не обрывом
            log(f"ошибка: {type(e).__name__}")
            self.reply({"ok": False, "error": f"plugin error: {type(e).__name__}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, {URL}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
