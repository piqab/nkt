"""Плагин хаба nkt для Forgejo (и Gitea — тот же API /api/v1).

Написан по examples/AGENTS.md из заготовки examples/_template. Ходит в
REST API Forgejo токеном администратора (FORGEJO_URL, FORGEJO_TOKEN —
настройки службы). Что умеет: сводка с находками (упавшие последние
запуски Actions, зеркала без синхронизации больше 7 дней, много
администраторов), репозитории (создать, архивировать, синхронизировать
зеркало, удалить), запуски Actions и вебхуки репозитория (добавить —
например, вебхук выкладки конвейера nkt; удалить), пользователи, токен
регистрации раннера Actions (один раз окну или сразу в .env конвейера),
команды сценариев «forgejo …», инструменты MCP.
Без зависимостей: только стандартная библиотека Python.
"""
import datetime
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
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9270") or 9270)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
API = (os.environ.get("FORGEJO_URL", "").strip() or "http://127.0.0.1:3000").rstrip("/") + "/api/v1"
TOKEN = os.environ.get("FORGEJO_TOKEN", "").strip()
# Зеркало без синхронизации дольше — находка; запусков на репозиторий в
# сводке смотрим не больше, репозиториев для находок — не больше.
MIRROR_STALE_DAYS = 7
SUMMARY_REPOS = 30
ADMINS_MANY = 3

T = {
    "ru": {
        "title": "Forgejo", "desc": "Forgejo (и Gitea) из хаба: репозитории, Actions, вебхуки, пользователи, токен раннера, находки.",
        "summary": "Сводка", "repos": "Репозитории", "users": "Пользователи", "runs": "Запуски Actions", "hooks": "Вебхуки",
        "repo": "Репозиторий", "owner": "Владелец", "visibility": "Видимость", "private": "закрытый", "public": "открытый",
        "size": "Размер, МБ", "kind": "Вид", "mirror": "зеркало", "archived": "в архиве", "regular": "обычный", "updated": "Изменён",
        "param": "Параметр", "value": "Значение", "version": "Версия", "n_repos": "Репозиториев", "n_users": "Пользователей",
        "n_admins": "Администраторов", "findings": "Находки",
        "f_failed": "{repo}: последний запуск «{name}» — {status}", "f_mirror": "{repo}: зеркало не синхронизировалось {days} дн.",
        "f_admins": "Администраторов: {n} — проверьте, все ли нужны", "f_none": "Находок нет",
        "create": "Создать репозиторий", "f_name": "Имя", "f_org": "Организация", "f_org_hint": "пусто — от пользователя токена",
        "f_private": "Закрытый", "f_desc": "Описание",
        "archive": "В архив", "archive_confirm": "Перевести {repo} в архив? Запись в него станет невозможна.",
        "sync": "Синхронизировать зеркало", "delete": "Удалить", "delete_confirm": "Удалить репозиторий {repo} со всей историей? Это необратимо.",
        "open_runs": "Запуски", "open_hooks": "Вебхуки",
        "run": "Запуск", "branch": "Ветка", "event": "Событие", "status": "Состояние", "started": "Начат",
        "hook_url": "Адрес", "events": "События", "active": "Включён", "yes": "да", "no": "нет",
        "hook_add": "Добавить вебхук", "f_url": "Адрес вебхука", "f_url_hint": "например, вебхук выкладки конвейера nkt («Выкладки» → конвейер → «Вебхук»)",
        "f_secret": "Секрет вебхука", "f_secret_hint": "необязательно; хранится только в Forgejo", "hook_delete_confirm": "Удалить вебхук {url}?",
        "login": "Логин", "email": "Почта", "admin": "Админ", "last_login": "Последний вход", "state": "Состояние", "blocked": "заблокирован",
        "runner_token": "Токен регистрации раннера", "runner_note": "Раннер: forgejo-runner register --token … Токен показан один раз.",
        "runner_repo_hint": "пусто — для всего экземпляра (нужен администратор)",
        "created": "создан {repo}", "archived_ok": "{repo} — в архиве", "synced": "{repo}: синхронизация зеркала запущена", "deleted": "удалён {repo}",
        "hook_added": "{repo}: вебхук на {url}", "hook_deleted": "{repo}: вебхук {id} удалён", "token_stored": "токен раннера записан в .env конвейера {pipeline} ({env})",
        "would": "(сухой прогон) {what}", "check_ok": "проверено", "never": "никогда",
        "no_token": "не задан FORGEJO_TOKEN — «Настройки» на странице плагина",
        "api_failed": "Forgejo {method} {path}: {err}", "auth_failed": "Forgejo: токен не принят (401) — нужен токен администратора с правами admin, repository, user, organization",
        "bad_repo": "репозиторий {repo}: нужно владелец/имя (латиница, цифры, . _ -)", "bad_name": "имя {name}: латиница, цифры, . _ - (до 100)",
        "bad_url": "адрес вебхука {url}: http:// или https:// без пробелов и логина", "bad_id": "номер вебхука {id}",
        "bad_env": "переменная .env {env}: A-Z, 0-9, _", "agent_needs_pipeline": "токен раннера агенту или сценарию — только в .env конвейера: to_pipeline и env",
        "usage": "forgejo repos | forgejo mirror-sync ВЛАДЕЛЕЦ/РЕПО | forgejo archive ВЛАДЕЛЕЦ/РЕПО | forgejo hook add ВЛАДЕЛЕЦ/РЕПО АДРЕС | forgejo runner-token [ВЛАДЕЛЕЦ/РЕПО] to-pipeline КОНВЕЙЕР env ИМЯ",
    },
    "en": {
        "title": "Forgejo", "desc": "Forgejo (and Gitea) from the hub: repositories, Actions, webhooks, users, a runner token, findings.",
        "summary": "Summary", "repos": "Repositories", "users": "Users", "runs": "Actions runs", "hooks": "Webhooks",
        "repo": "Repository", "owner": "Owner", "visibility": "Visibility", "private": "private", "public": "public",
        "size": "Size, MB", "kind": "Kind", "mirror": "mirror", "archived": "archived", "regular": "regular", "updated": "Updated",
        "param": "Parameter", "value": "Value", "version": "Version", "n_repos": "Repositories", "n_users": "Users",
        "n_admins": "Administrators", "findings": "Findings",
        "f_failed": "{repo}: the last run “{name}” — {status}", "f_mirror": "{repo}: the mirror has not synced for {days} days",
        "f_admins": "Administrators: {n} — check that all are needed", "f_none": "No findings",
        "create": "Create a repository", "f_name": "Name", "f_org": "Organization", "f_org_hint": "empty — the token's user",
        "f_private": "Private", "f_desc": "Description",
        "archive": "Archive", "archive_confirm": "Archive {repo}? It becomes read-only.",
        "sync": "Sync the mirror", "delete": "Delete", "delete_confirm": "Delete the repository {repo} with all its history? This can't be undone.",
        "open_runs": "Runs", "open_hooks": "Webhooks",
        "run": "Run", "branch": "Branch", "event": "Event", "status": "Status", "started": "Started",
        "hook_url": "Address", "events": "Events", "active": "Active", "yes": "yes", "no": "no",
        "hook_add": "Add a webhook", "f_url": "Webhook address", "f_url_hint": "for example, an nkt pipeline deploy webhook (Deployments → pipeline → Webhook)",
        "f_secret": "Webhook secret", "f_secret_hint": "optional; kept only in Forgejo", "hook_delete_confirm": "Delete the webhook {url}?",
        "login": "Login", "email": "Email", "admin": "Admin", "last_login": "Last login", "state": "State", "blocked": "blocked",
        "runner_token": "Runner registration token", "runner_note": "A runner: forgejo-runner register --token … The token is shown once.",
        "runner_repo_hint": "empty — for the whole instance (needs an administrator)",
        "created": "created {repo}", "archived_ok": "{repo} is archived", "synced": "{repo}: mirror sync started", "deleted": "deleted {repo}",
        "hook_added": "{repo}: a webhook to {url}", "hook_deleted": "{repo}: webhook {id} deleted", "token_stored": "the runner token is in the {pipeline} pipeline .env ({env})",
        "would": "(dry run) {what}", "check_ok": "checked", "never": "never",
        "no_token": "FORGEJO_TOKEN is not set — Settings on the plugin page",
        "api_failed": "Forgejo {method} {path}: {err}", "auth_failed": "Forgejo: the token was rejected (401) — an administrator token with admin, repository, user, organization rights is needed",
        "bad_repo": "repository {repo}: owner/name (latin letters, digits, . _ -)", "bad_name": "name {name}: latin letters, digits, . _ - (up to 100)",
        "bad_url": "webhook address {url}: http:// or https:// with no spaces or login", "bad_id": "webhook number {id}",
        "bad_env": "the .env variable {env}: A-Z, 0-9, _", "agent_needs_pipeline": "a runner token for an agent or a script goes only into a pipeline .env: to_pipeline and env",
        "usage": "forgejo repos | forgejo mirror-sync OWNER/REPO | forgejo archive OWNER/REPO | forgejo hook add OWNER/REPO URL | forgejo runner-token [OWNER/REPO] to-pipeline PIPELINE env NAME",
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
    print(f"nkt-plugin-forgejo: {msg}", flush=True)


# --- проверка ввода ---------------------------------------------------------------

REPO_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,39}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$")
NAME_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$")
ORG_RE = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,39}$")
URL_RE = re.compile(r"^https?://[A-Za-z0-9.-]{1,253}(:\d{1,5})?(/[A-Za-z0-9._~/%?&=+:-]*)?$")
ENV_RE = re.compile(r"^[A-Z_][A-Z0-9_]{0,63}$")
PIPE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")


def check_repo(c, repo):
    repo = str(repo or "").strip()
    if not REPO_RE.match(repo) or ".." in repo:
        raise c.fail("bad_repo", repo=repo or "—")
    return repo


def check_name(c, name):
    name = str(name or "").strip()
    if not NAME_RE.match(name) or ".." in name:
        raise c.fail("bad_name", name=name or "—")
    return name


def check_url(c, url):
    url = str(url or "").strip()
    if not URL_RE.match(url) or len(url) > 500:
        raise c.fail("bad_url", url=url or "—")
    return url


def rpath(repo):
    """«владелец/имя» → часть пути API (каждая часть — в кавычках)."""
    owner, name = repo.split("/", 1)
    return urllib.parse.quote(owner, safe="") + "/" + urllib.parse.quote(name, safe="")


# --- API Forgejo ----------------------------------------------------------------------

def api(c, method, path, body=None, params=None):
    if not TOKEN:
        raise c.fail("no_token")
    url = API + path + ("?" + urllib.parse.urlencode(params) if params else "")
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Authorization": "token " + TOKEN, "Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read(8 << 20)
    except urllib.error.HTTPError as e:
        if e.code == 401:
            raise c.fail("auth_failed")
        detail = ""
        try:
            detail = str(json.loads(e.read(4096)).get("message") or "")[:200]
        except (ValueError, AttributeError):
            pass
        raise c.fail("api_failed", method=method, path=path, err=f"HTTP {e.code}" + (f": {detail}" if detail else ""))
    except urllib.error.URLError as e:
        raise c.fail("api_failed", method=method, path=path, err=str(e.reason))
    return json.loads(raw) if raw.strip() else None


def paged(c, path, key=None, limit=200):
    """Все страницы списка (по 50), не больше limit записей."""
    out = []
    for page in range(1, 100):
        res = api(c, "GET", path, params={"limit": 50, "page": page})
        items = (res or {}).get(key, []) if key else (res or [])
        if not isinstance(items, list):
            break
        out += [x for x in items if isinstance(x, dict)]
        if len(items) < 50 or len(out) >= limit:
            break
    return out[:limit]


def repos(c):
    return paged(c, "/repos/search", "data", 1000)


def users(c):
    return paged(c, "/admin/users", None, 1000)


def last_run(c, repo):
    res = api(c, "GET", f"/repos/{rpath(repo)}/actions/tasks", params={"limit": 1, "page": 1})
    runs = (res or {}).get("workflow_runs") or []
    return runs[0] if runs else None


def parse_time(s):
    try:
        t = datetime.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
        return t if t.tzinfo else t.replace(tzinfo=datetime.timezone.utc)
    except ValueError:
        return None


def short_time(s):
    t = parse_time(s)
    return t.strftime("%Y-%m-%d %H:%M") if t and t.year > 1970 else ""


# --- изменения ---------------------------------------------------------------------

def repo_create(c, args, dry):
    name = check_name(c, args.get("name"))
    org = str(args.get("org") or "").strip()
    if org and not ORG_RE.match(org):
        raise c.fail("bad_repo", repo=f"{org}/{name}")
    body = {"name": name, "private": truthy(args.get("private")), "description": str(args.get("description") or "")[:500]}
    what = c.t("created", repo=f"{org or '~'}/{name}")
    if dry:
        return c.t("would", what=what)
    res = api(c, "POST", f"/orgs/{urllib.parse.quote(org, safe='')}/repos" if org else "/user/repos", body)
    return c.t("created", repo=(res or {}).get("full_name") or name)


def repo_archive(c, repo, dry):
    repo = check_repo(c, repo)
    what = c.t("archived_ok", repo=repo)
    if dry:
        return c.t("would", what=what)
    api(c, "PATCH", f"/repos/{rpath(repo)}", {"archived": True})
    return what


def mirror_sync(c, repo, dry):
    repo = check_repo(c, repo)
    what = c.t("synced", repo=repo)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", f"/repos/{rpath(repo)}/mirror-sync")
    return what


def repo_delete(c, repo, dry):
    repo = check_repo(c, repo)
    what = c.t("deleted", repo=repo)
    if dry:
        return c.t("would", what=what)
    api(c, "DELETE", f"/repos/{rpath(repo)}")
    return what


def hook_add(c, args, dry):
    repo = check_repo(c, args.get("repo"))
    url = check_url(c, args.get("url"))
    cfg = {"url": url, "content_type": "json"}
    if args.get("secret"):
        cfg["secret"] = str(args["secret"])[:200]
    what = c.t("hook_added", repo=repo, url=url)
    if dry:
        return c.t("would", what=what)
    api(c, "POST", f"/repos/{rpath(repo)}/hooks", {"type": "forgejo", "config": cfg, "events": ["push"], "active": True})
    return what


def hook_delete(c, args, dry):
    repo = check_repo(c, args.get("repo"))
    hid = str(args.get("id") or "").strip()
    if not hid.isdigit() or len(hid) > 12:
        raise c.fail("bad_id", id=hid or "—")
    what = c.t("hook_deleted", repo=repo, id=hid)
    if dry:
        return c.t("would", what=what)
    api(c, "DELETE", f"/repos/{rpath(repo)}/hooks/{hid}")
    return what


def runner_token(c, args, dry, agent):
    """Токен регистрации раннера: окну — один раз (reveal), агенту и
    сценарию — только в .env конвейера (store: пишет хаб)."""
    repo = str(args.get("repo") or "").strip()
    path = f"/repos/{rpath(check_repo(c, repo))}/actions/runners/registration-token" if repo else "/admin/runners/registration-token"
    pipe, env = str(args.get("to_pipeline") or "").strip(), str(args.get("env") or "").strip()
    if pipe or env or agent:
        if not PIPE_RE.match(pipe) or not ENV_RE.match(env):
            raise c.fail("agent_needs_pipeline") if not (pipe and env) else c.fail("bad_env", env=env)
    if dry:
        return {"ok": True, "text": c.t("would", what=c.t("runner_token") + (f" → {pipe} ({env})" if pipe else ""))}
    token = str((api(c, "GET", path) or {}).get("token") or "")
    if pipe:
        return {"ok": True, "text": c.t("token_stored", pipeline=pipe, env=env), "store": {"pipeline": pipe, "env": env, "value": token}}
    return {"ok": True, "text": c.t("runner_token"), "reveal": {"title": c.t("runner_token"), "value": token, "note": c.t("runner_note")}}


# --- виды -------------------------------------------------------------------------

def table(title, cols, rows, actions=None, note=""):
    return {"type": "table", "title": title, "note": note, "columns": [{"key": k, "title": t} for k, t in cols], "rows": rows, "actions": actions or []}


def findings(c, lst, admins):
    out = []
    now = datetime.datetime.now(datetime.timezone.utc)
    for r in sorted(lst, key=lambda r: str(r.get("updated_at") or ""), reverse=True)[:SUMMARY_REPOS]:
        repo = r.get("full_name", "")
        if r.get("mirror"):
            t = parse_time(r.get("mirror_updated"))
            if t and (now - t).days > MIRROR_STALE_DAYS:
                out.append(c.t("f_mirror", repo=repo, days=(now - t).days))
        elif not r.get("archived") and not r.get("empty") and REPO_RE.match(repo):
            try:
                run = last_run(c, repo)
            except Fail:
                run = None  # Actions выключены у репозитория — не находка
            if run and run.get("status") in ("failure", "error", "cancelled"):
                out.append(c.t("f_failed", repo=repo, name=run.get("name") or run.get("display_title") or "?", status=run.get("status")))
    if admins > ADMINS_MANY:
        out.append(c.t("f_admins", n=admins))
    return out


def summary_view(c):
    ver = str((api(c, "GET", "/version") or {}).get("version") or "")
    lst = repos(c)
    try:
        us = users(c)
    except Fail:
        us = []  # токен не администратора — без пользователей
    admins = sum(1 for u in us if u.get("is_admin"))
    params = [(c.t("version"), ver), (c.t("n_repos"), len(lst)), (c.t("n_users"), len(us) or "—"), (c.t("n_admins"), admins if us else "—")]
    found = findings(c, lst, admins)
    return {"type": "sections", "items": [
        table(c.t("summary"), [("param", c.t("param")), ("value", c.t("value"))], [{"cells": {"param": k, "value": v}} for k, v in params]),
        table(c.t("findings"), [("value", c.t("findings"))], [{"cells": {"value": f}} for f in found] or [{"cells": {"value": c.t("f_none")}}]),
    ]}


def repos_view(c):
    rows = []
    for r in sorted(repos(c), key=lambda r: str(r.get("full_name") or "").lower()):
        repo = r.get("full_name", "")
        if not REPO_RE.match(repo):
            continue
        args = {"repo": repo}
        kind = c.t("mirror") if r.get("mirror") else c.t("archived") if r.get("archived") else c.t("regular")
        actions = [{"title": c.t("open_runs"), "open": "runs", "args": args}, {"title": c.t("open_hooks"), "open": "hooks", "args": args}]
        if r.get("mirror"):
            actions.append({"title": c.t("sync"), "op": "mirror_sync", "args": args})
        if not r.get("archived"):
            actions.append({"title": c.t("archive"), "op": "repo_archive", "args": args, "confirm": c.t("archive_confirm", repo=repo)})
        actions.append({"title": c.t("delete"), "op": "repo_delete", "args": args, "danger": True, "confirm": c.t("delete_confirm", repo=repo)})
        rows.append({"cells": {"repo": repo, "visibility": c.t("private") if r.get("private") else c.t("public"), "kind": kind,
                               "size": round((r.get("size") or 0) / 1024, 1), "updated": short_time(r.get("updated_at"))}, "actions": actions})
    create = {"title": c.t("create"), "op": "repo_create", "fields": [
        {"name": "name", "title": c.t("f_name"), "required": True, "placeholder": "my-app"},
        {"name": "org", "title": c.t("f_org"), "hint": c.t("f_org_hint")},
        {"name": "private", "title": c.t("f_private"), "type": "bool", "default": True},
        {"name": "description", "title": c.t("f_desc")}]}
    token = {"title": c.t("runner_token"), "op": "runner_token", "fields": [{"name": "repo", "title": c.t("repo"), "hint": c.t("runner_repo_hint")}]}
    return table(c.t("repos"), [("repo", c.t("repo")), ("visibility", c.t("visibility")), ("kind", c.t("kind")), ("size", c.t("size")), ("updated", c.t("updated"))],
                 rows, [create, token])


def runs_view(c, repo):
    repo = check_repo(c, repo)
    res = api(c, "GET", f"/repos/{rpath(repo)}/actions/tasks", params={"limit": 30, "page": 1})
    rows = [{"cells": {"run": f"#{x.get('run_number', '')} {x.get('name') or x.get('display_title') or ''}", "branch": x.get("head_branch", ""),
                       "event": x.get("event", ""), "status": x.get("status", ""), "started": short_time(x.get("created_at"))}}
            for x in (res or {}).get("workflow_runs") or [] if isinstance(x, dict)]
    return table(f"{c.t('runs')}: {repo}", [("run", c.t("run")), ("branch", c.t("branch")), ("event", c.t("event")), ("status", c.t("status")), ("started", c.t("started"))], rows)


def hooks_view(c, repo):
    repo = check_repo(c, repo)
    rows = []
    for h in api(c, "GET", f"/repos/{rpath(repo)}/hooks") or []:
        url = str((h.get("config") or {}).get("url") or "")
        rows.append({"cells": {"url": url, "events": ", ".join(h.get("events") or []), "active": c.t("yes") if h.get("active") else c.t("no")},
                     "actions": [{"title": c.t("delete"), "op": "hook_delete", "args": {"repo": repo, "id": str(h.get("id", ""))}, "danger": True,
                                  "confirm": c.t("hook_delete_confirm", url=url)}]})
    add = {"title": c.t("hook_add"), "op": "hook_add", "args": {"repo": repo}, "fields": [
        {"name": "url", "title": c.t("f_url"), "required": True, "hint": c.t("f_url_hint"), "placeholder": "https://hub.example.com/hooks/…"},
        {"name": "secret", "title": c.t("f_secret"), "type": "password", "hint": c.t("f_secret_hint")}]}
    return table(f"{c.t('hooks')}: {repo}", [("url", c.t("hook_url")), ("events", c.t("events")), ("active", c.t("active"))], rows, [add])


def users_view(c):
    rows = [{"cells": {"login": u.get("login", ""), "email": u.get("email", ""), "admin": c.t("yes") if u.get("is_admin") else "",
                       "last_login": short_time(u.get("last_login")) or c.t("never"), "state": c.t("blocked") if u.get("prohibit_login") else ""}}
            for u in sorted(users(c), key=lambda u: (not u.get("is_admin"), str(u.get("login") or "").lower()))]
    return table(c.t("users"), [("login", c.t("login")), ("email", c.t("email")), ("admin", c.t("admin")), ("last_login", c.t("last_login")), ("state", c.t("state"))], rows)


# --- команда сценария -------------------------------------------------------------

def command(c, argv, dry, check):
    if len(argv) == 2 and argv[1] == "repos":
        return {"ok": True} if check else {"ok": True, "text": "\n".join(r.get("full_name", "") for r in repos(c))}
    if len(argv) == 3 and argv[1] in ("mirror-sync", "archive"):
        check_repo(c, argv[2])
        if check:
            return {"ok": True}
        return {"ok": True, "text": mirror_sync(c, argv[2], dry) if argv[1] == "mirror-sync" else repo_archive(c, argv[2], dry)}
    if len(argv) == 5 and argv[1:3] == ["hook", "add"]:
        check_repo(c, argv[3])
        check_url(c, argv[4])
        return {"ok": True} if check else {"ok": True, "text": hook_add(c, {"repo": argv[3], "url": argv[4]}, dry)}
    if len(argv) >= 6 and argv[1] == "runner-token":
        rest = argv[2:]
        repo = rest.pop(0) if len(rest) == 5 else ""
        if rest[0] != "to-pipeline" or rest[2] != "env":
            raise Fail(c.t("usage"))
        args = {"repo": repo, "to_pipeline": rest[1], "env": rest[3]}
        if repo:
            check_repo(c, repo)
        if not PIPE_RE.match(rest[1]) or not ENV_RE.match(rest[3]):
            raise c.fail("bad_env", env=rest[3])
        return {"ok": True} if check else runner_token(c, args, dry, True)
    raise Fail(c.t("usage"))


# --- манифест и вызовы -----------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}
REPO = {"repo": {"type": "string", "description": "owner/name"}}


def manifest(c):
    return {
        "protocol": 1, "name": "forgejo", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True,
             "description": "Forgejo version, counts and findings: failed last Actions runs, mirrors not synced for over 7 days, many admins."},
            {"name": "repos", "title": c.t("repos"), "read_only": True, "mcp": True, "description": "Repositories: owner/name, visibility, mirror or archived, size, last update."},
            {"name": "users", "title": c.t("users"), "read_only": True, "mcp": True, "description": "Users (admin token): login, email, admin, last login, blocked."},
            {"name": "runs", "title": c.t("runs"), "read_only": True, "mcp": True, "description": "Recent Actions runs of a repository.", "input": schema(REPO, ["repo"])},
            {"name": "hooks", "title": c.t("hooks"), "read_only": True, "mcp": True, "description": "Webhooks of a repository.", "input": schema(REPO, ["repo"])},
            {"name": "repo_create", "title": c.t("create"), "mcp": True, "description": "Create a repository (in an organization if org is set).",
             "input": schema({"name": S, "org": S, "private": {"type": "boolean"}, "description": S}, ["name"])},
            {"name": "repo_archive", "title": c.t("archive"), "mcp": True, "description": "Archive a repository (read-only).", "input": schema(REPO, ["repo"])},
            {"name": "mirror_sync", "title": c.t("sync"), "mcp": True, "description": "Start a mirror sync.", "input": schema(REPO, ["repo"])},
            {"name": "repo_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a repository with its history.", "input": schema(REPO, ["repo"])},
            {"name": "hook_add", "title": c.t("hook_add"), "mcp": True,
             "description": "Add a push webhook to a repository — e.g. the deploy webhook of an nkt pipeline.",
             "input": schema({**REPO, "url": S, "secret": S}, ["repo", "url"])},
            {"name": "hook_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a repository webhook by id (from hooks).",
             "input": schema({**REPO, "id": S}, ["repo", "id"])},
            {"name": "runner_token", "title": c.t("runner_token"), "mcp": True, "reveals": True,
             "description": "An Actions runner registration token (repo — for one repository, empty — the whole instance). For an agent it goes only into a pipeline .env: to_pipeline and env; the value is never returned.",
             "input": schema({"repo": S, "to_pipeline": S, "env": S}, ["to_pipeline", "env"])},
        ],
        "pages": [
            {"id": "summary", "title": c.t("summary"), "op": "summary"},
            {"id": "repos", "title": c.t("repos"), "op": "repos"},
            {"id": "users", "title": c.t("users"), "op": "users"},
            {"id": "runs", "title": c.t("runs"), "op": "runs", "hidden": True},
            {"id": "hooks", "title": c.t("hooks"), "op": "hooks", "hidden": True},
        ],
        "commands": [{"word": "forgejo", "usage": c.t("usage"),
                      "example": "forgejo mirror-sync team/site\nforgejo hook add team/site https://hub.example.com/hooks/abc\nforgejo runner-token to-pipeline runner env RUNNER_TOKEN"}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    views = {"summary": lambda: summary_view(c), "repos": lambda: repos_view(c), "users": lambda: users_view(c),
             "runs": lambda: runs_view(c, args.get("repo")), "hooks": lambda: hooks_view(c, args.get("repo"))}
    if op in views:
        return {"ok": True, "view": views[op]()}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        out = command(c, [str(a) for a in args.get("argv") or []], dry, False)
        out.pop("reveal", None)  # из сценария — только в .env конвейера
        return out
    if op == "runner_token":
        out = runner_token(c, args, dry, False)
        if not dry:
            log(f"{req.get('user', '')}: runner_token {args.get('repo') or '(instance)'}")
        return out
    changes = {"repo_create": lambda: repo_create(c, args, dry), "repo_archive": lambda: repo_archive(c, args.get("repo"), dry),
               "mirror_sync": lambda: mirror_sync(c, args.get("repo"), dry), "repo_delete": lambda: repo_delete(c, args.get("repo"), dry),
               "hook_add": lambda: hook_add(c, args, dry), "hook_delete": lambda: hook_delete(c, args, dry)}
    if op not in changes:
        return {"ok": False, "error": f"no op {op}"}
    text = changes[op]()
    if not dry:
        log(f"{req.get('user', '')}: {op} {args.get('repo') or args.get('name') or ''}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-forgejo/" + VERSION

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
    log(f"{VERSION}: {LISTEN}:{PORT}, {API}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
