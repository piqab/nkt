# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
"""Плагин хаба nkt для LiteLLM (протокол плагинов nkt v1).

Ставится на хост LiteLLM: «О системе» → «Плагины» → «Установить на хост»
— служба systemd без Docker, ходит в API администратора LiteLLM
(127.0.0.1:4000) мастер-ключом. Слушает 127.0.0.1, хаб ходит по SSH и
подписывает вызовы секретом NKT_PLUGIN_SECRET.

Что умеет: виртуальные ключи (создать — с моделями, бюджетом, лимитами и
сроком; заблокировать, разблокировать, удалить), модели (добавить
OpenAI-совместимую или облачную с её ключом, удалить), расход по ключам.
Новый ключ показывается один раз — только в окне хаба (reveal); из
сценария или агенту — сразу в .env конвейера (store: пишет хаб). Команды
сценариев «llm …», инструменты MCP.
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

VERSION = "1.0.0"
API = (os.environ.get("LITELLM_URL", "").strip() or "http://127.0.0.1:4000").rstrip("/")
MASTER = os.environ.get("LITELLM_MASTER_KEY", "").strip()
if MASTER and not MASTER.startswith("sk-"):
    # В .env конвейера примера — без «sk-» (его добавляет compose).
    MASTER = "sk-" + MASTER
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9210") or 9210)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"

T = {
    "ru": {
        "title": "LiteLLM",
        "desc": "Шлюз к моделям LiteLLM: виртуальные ключи с бюджетом и лимитами, модели, расход.",
        "keys": "Ключи", "models": "Модели", "spend": "Расход",
        "alias": "Имя ключа", "key": "Ключ", "models_col": "Модели", "spent": "Потрачено, $", "budget": "Бюджет, $",
        "limits": "Лимиты", "expires": "Истекает", "state": "Состояние", "blocked": "заблокирован", "active": "работает",
        "all_models": "все", "never": "—",
        "add_key": "Создать ключ", "block": "Заблокировать", "unblock": "Разблокировать", "delete": "Удалить",
        "delete_key_confirm": "Удалить ключ {alias}? Приложения с ним перестанут работать.",
        "block_confirm": "Заблокировать ключ {alias}? Запросы с ним будут отклоняться, пока не разблокируете.",
        "alias_hint": "латиница, цифры, - и _: по нему ключ видно в списке и в сценариях",
        "models_hint": "через запятую; пусто — все модели",
        "budget_hint": "долларов; пусто — без бюджета",
        "budget_duration": "Бюджет обнуляется", "bd_none": "никогда", "bd_1d": "каждый день", "bd_7d": "каждую неделю", "bd_30d": "каждый месяц",
        "rpm": "Запросов в минуту", "tpm": "Токенов в минуту", "limit_hint": "пусто — без предела",
        "duration": "Срок действия", "duration_hint": "30d, 12h; пусто — бессрочно",
        "to_pipeline": "Сразу в .env конвейера", "to_pipeline_hint": "имя конвейера: ключ не покажется, хаб запишет его в .env",
        "env": "Имя переменной", "env_hint": "например, LLM_API_KEY (вместе с конвейером)",
        "key_created": "ключ {alias} создан", "key_reveal": "Ключ {alias}",
        "key_reveal_note": "Показывается один раз: скопируйте сейчас. В хабе и LiteLLM хранится только его отпечаток.",
        "blocked_done": "ключ {alias} заблокирован", "unblocked_done": "ключ {alias} разблокирован", "deleted_done": "ключ {alias} удалён",
        "no_key": "ключа {alias} нет", "alias_taken": "ключ {alias} уже есть — выберите другое имя",
        "bad_alias": "имя ключа {alias}: латиница, цифры, - и _, до 64 знаков",
        "bad_number": "{what}: число", "bad_duration": "срок {v}: число и s, m, h или d (30d)",
        "env_needs_pipeline": "имя переменной — вместе с конвейером",
        "add_model": "Добавить модель", "model_name": "Имя для клиентов", "model_name_hint": "так модель будут звать приложения (qwen, gpt-4o)",
        "model": "Модель у провайдера", "model_hint": "провайдер/модель: openai/qwen3-8b (OpenAI-совместимая — llama.cpp, Ollama, vLLM), anthropic/claude-sonnet-4-5, openai/gpt-4o",
        "api_base": "Адрес API", "api_base_hint": "для OpenAI-совместимой: http://llama:8080/v1; облачной — пусто",
        "api_key": "Ключ провайдера", "api_key_hint": "LiteLLM хранит его в базе зашифрованным; пусто — без ключа",
        "model_added": "модель {name} добавлена", "model_deleted": "модель {name} удалена",
        "delete_model_confirm": "Удалить модель {name}? Ключи с ней перестанут её получать.",
        "provider": "Провайдер / модель", "base": "Адрес", "source": "Где задана", "in_db": "плагином / в базе", "in_config": "в файле настроек",
        "bad_model_name": "имя модели {v}: латиница, цифры и . _ : / -", "bad_model": "модель {v}: провайдер/модель, например openai/qwen3-8b",
        "bad_url": "адрес {v}: http:// или https://", "no_model": "модели {name} нет (или она из файла настроек — её здесь не удалить)",
        "spend_note": "Расход — с создания ключа, по ценам LiteLLM; у своих моделей (llama.cpp) цена 0 — смотрите на число запросов в журнале LiteLLM.",
        "total": "Всего", "no_master": "не задан LITELLM_MASTER_KEY плагина", "api_failed": "LiteLLM {method} {path}: {err}",
        "dry": "(сухой прогон) ", "would": "(сухой прогон) {what}", "nothing": "ничего не изменилось",
        "usage": "llm keys | llm key add ИМЯ [models a,b] [budget 20] [rpm 60] [tpm 100000] [expires 30d] to-pipeline КОНВЕЙЕР env ПЕРЕМЕННАЯ | llm key block|unblock|rm ИМЯ",
        "script_needs_pipeline": "в сценарии новый ключ — только сразу в .env конвейера: to-pipeline КОНВЕЙЕР env ПЕРЕМЕННАЯ",
        "check_ok": "проверено",
    },
    "en": {
        "title": "LiteLLM",
        "desc": "LiteLLM model gateway: virtual keys with budgets and limits, models, spend.",
        "keys": "Keys", "models": "Models", "spend": "Spend",
        "alias": "Key name", "key": "Key", "models_col": "Models", "spent": "Spent, $", "budget": "Budget, $",
        "limits": "Limits", "expires": "Expires", "state": "State", "blocked": "blocked", "active": "active",
        "all_models": "all", "never": "—",
        "add_key": "Create a key", "block": "Block", "unblock": "Unblock", "delete": "Delete",
        "delete_key_confirm": "Delete key {alias}? Apps using it will stop working.",
        "block_confirm": "Block key {alias}? Requests with it are refused until you unblock it.",
        "alias_hint": "latin letters, digits, - and _: the key is shown by it in the list and in scripts",
        "models_hint": "comma-separated; empty — all models",
        "budget_hint": "dollars; empty — no budget",
        "budget_duration": "Budget resets", "bd_none": "never", "bd_1d": "daily", "bd_7d": "weekly", "bd_30d": "monthly",
        "rpm": "Requests per minute", "tpm": "Tokens per minute", "limit_hint": "empty — no limit",
        "duration": "Valid for", "duration_hint": "30d, 12h; empty — no expiry",
        "to_pipeline": "Straight into a pipeline .env", "to_pipeline_hint": "pipeline name: the key isn't shown, the hub writes it into the .env",
        "env": "Variable name", "env_hint": "e.g. LLM_API_KEY (together with the pipeline)",
        "key_created": "key {alias} created", "key_reveal": "Key {alias}",
        "key_reveal_note": "Shown once: copy it now. The hub and LiteLLM keep only its hash.",
        "blocked_done": "key {alias} blocked", "unblocked_done": "key {alias} unblocked", "deleted_done": "key {alias} deleted",
        "no_key": "no key {alias}", "alias_taken": "key {alias} already exists — pick another name",
        "bad_alias": "key name {alias}: latin letters, digits, - and _, up to 64 characters",
        "bad_number": "{what}: a number", "bad_duration": "validity {v}: a number and s, m, h or d (30d)",
        "env_needs_pipeline": "the variable name goes together with a pipeline",
        "add_model": "Add a model", "model_name": "Name for clients", "model_name_hint": "apps call the model by this name (qwen, gpt-4o)",
        "model": "Provider model", "model_hint": "provider/model: openai/qwen3-8b (OpenAI-compatible — llama.cpp, Ollama, vLLM), anthropic/claude-sonnet-4-5, openai/gpt-4o",
        "api_base": "API address", "api_base_hint": "for an OpenAI-compatible one: http://llama:8080/v1; a cloud one — empty",
        "api_key": "Provider key", "api_key_hint": "LiteLLM keeps it encrypted in its database; empty — no key",
        "model_added": "model {name} added", "model_deleted": "model {name} deleted",
        "delete_model_confirm": "Delete model {name}? Keys stop getting it.",
        "provider": "Provider / model", "base": "Address", "source": "Defined in", "in_db": "plugin / database", "in_config": "config file",
        "bad_model_name": "model name {v}: latin letters, digits and . _ : / -", "bad_model": "model {v}: provider/model, e.g. openai/qwen3-8b",
        "bad_url": "address {v}: http:// or https://", "no_model": "no model {name} (or it comes from the config file — it can't be deleted here)",
        "spend_note": "Spend since each key was created, at LiteLLM prices; your own models (llama.cpp) cost 0 — see the request count in the LiteLLM log.",
        "total": "Total", "no_master": "the plugin's LITELLM_MASTER_KEY isn't set", "api_failed": "LiteLLM {method} {path}: {err}",
        "dry": "(dry run) ", "would": "(dry run) {what}", "nothing": "nothing changed",
        "usage": "llm keys | llm key add NAME [models a,b] [budget 20] [rpm 60] [tpm 100000] [expires 30d] to-pipeline PIPELINE env VARIABLE | llm key block|unblock|rm NAME",
        "script_needs_pipeline": "in a script a new key goes only straight into a pipeline .env: to-pipeline PIPELINE env VARIABLE",
        "check_ok": "checked",
    },
}


class Fail(Exception):
    """Ошибка для человека (уходит хабу как error)."""


class Ctx:
    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        return T[self.lang][key].format(**kw)

    def fail(self, key, **kw):
        return Fail(self.t(key, **kw))


def log(msg):
    print(f"nkt-plugin-litellm: {msg}", flush=True)


# --- API LiteLLM --------------------------------------------------------------

def llm(c, method, path, body=None, params=None):
    if not MASTER:
        raise c.fail("no_master")
    url = API + path
    if params:
        url += "?" + urllib.parse.urlencode(params, doseq=True)
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Authorization": f"Bearer {MASTER}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")[:400]
        try:
            d = json.loads(detail)
            detail = (d.get("detail") or d.get("error") or d)
            if isinstance(detail, dict):
                detail = detail.get("error") or detail.get("message") or json.dumps(detail)
        except ValueError:
            pass
        raise c.fail("api_failed", method=method, path=path, err=f"HTTP {e.code}: {detail}")
    except urllib.error.URLError as e:
        raise c.fail("api_failed", method=method, path=path, err=e.reason)
    return json.loads(raw or b"null")


def list_keys(c):
    out, page = [], 1
    while True:
        res = llm(c, "GET", "/key/list", params={"return_full_object": "true", "size": 100, "page": page})
        out += [k for k in res.get("keys") or [] if isinstance(k, dict)]
        if page >= int(res.get("total_pages") or 1):
            return out
        page += 1


ALIAS_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
DURATION_RE = re.compile(r"^\d{1,5}[smhd]$")
MODEL_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$")
MODEL_RE = re.compile(r"^[a-z0-9_-]+/[A-Za-z0-9._:/@-]{1,200}$")


def alias_ok(c, a):
    a = str(a or "").strip()
    if not ALIAS_RE.match(a):
        raise c.fail("bad_alias", alias=a)
    return a


def find_key(c, alias):
    alias = alias_ok(c, alias)
    for k in list_keys(c):
        if k.get("key_alias") == alias:
            return k
    raise c.fail("no_key", alias=alias)


def num(c, v, what, cast=int):
    if v is None or str(v).strip() == "":
        return None
    try:
        n = cast(str(v).strip())
    except ValueError:
        raise c.fail("bad_number", what=what)
    if n < 0:
        raise c.fail("bad_number", what=what)
    return n


def key_generate(c, args, dry):
    """Новый ключ. to_pipeline и env — сразу в .env конвейера (store),
    иначе — показ один раз (reveal)."""
    alias = alias_ok(c, args.get("alias"))
    models = args.get("models") or []
    if isinstance(models, str):
        models = [m.strip() for m in models.split(",") if m.strip()]
    for m in models:
        if not MODEL_NAME_RE.match(m):
            raise c.fail("bad_model_name", v=m)
    body = {"key_alias": alias, "models": models, "metadata": {"created_by": "nkt", "nkt_user": str(args.get("_user") or "")}}
    for field, what, cast in (("max_budget", c.t("budget"), float), ("rpm_limit", c.t("rpm"), int), ("tpm_limit", c.t("tpm"), int)):
        n = num(c, args.get(field), what, cast)
        if n is not None:
            body[field] = n
    bd = str(args.get("budget_duration") or "").strip()
    if bd and bd != "none":
        if not DURATION_RE.match(bd):
            raise c.fail("bad_duration", v=bd)
        body["budget_duration"] = bd
    dur = str(args.get("duration") or "").strip()
    if dur:
        if not DURATION_RE.match(dur):
            raise c.fail("bad_duration", v=dur)
        body["duration"] = dur
    pipeline, env = str(args.get("to_pipeline") or "").strip(), str(args.get("env") or "").strip()
    if env and not pipeline or pipeline and not env:
        raise c.fail("env_needs_pipeline")
    if any(k.get("key_alias") == alias for k in list_keys(c)):
        raise c.fail("alias_taken", alias=alias)
    if dry:
        return {"ok": True, "text": c.t("would", what=c.t("key_created", alias=alias))}
    res = llm(c, "POST", "/key/generate", body)
    key = res.get("key") or ""
    out = {"ok": True, "text": c.t("key_created", alias=alias)}
    if pipeline:
        out["store"] = {"pipeline": pipeline, "env": env, "value": key}
    else:
        out["reveal"] = {"title": c.t("key_reveal", alias=alias), "value": key, "note": c.t("key_reveal_note")}
    log(f"ключ {alias} создан")
    return out


def key_block(c, alias, block, dry):
    k = find_key(c, alias)
    what = c.t("blocked_done" if block else "unblocked_done", alias=k["key_alias"])
    if dry:
        return [c.t("would", what=what)]
    llm(c, "POST", "/key/block" if block else "/key/unblock", {"key": k.get("token")})
    return [what]


def key_delete(c, alias, dry):
    k = find_key(c, alias)
    what = c.t("deleted_done", alias=k["key_alias"])
    if dry:
        return [c.t("would", what=what)]
    llm(c, "POST", "/key/delete", {"key_aliases": [k["key_alias"]]})
    return [what]


def models(c):
    res = llm(c, "GET", "/model/info")
    return [m for m in (res or {}).get("data") or [] if isinstance(m, dict)]


def model_add(c, args, dry):
    name = str(args.get("name") or "").strip()
    model = str(args.get("model") or "").strip()
    base = str(args.get("api_base") or "").strip()
    key = str(args.get("api_key") or "").strip()
    if not MODEL_NAME_RE.match(name):
        raise c.fail("bad_model_name", v=name)
    if not MODEL_RE.match(model):
        raise c.fail("bad_model", v=model)
    if base and not re.match(r"^https?://[A-Za-z0-9.\-\[\]:]+(/[A-Za-z0-9._~/%-]*)?$", base):
        raise c.fail("bad_url", v=base)
    params = {"model": model}
    if base:
        params["api_base"] = base
    if key:
        params["api_key"] = key
    what = c.t("model_added", name=name)
    if dry:
        return [c.t("would", what=what)]
    llm(c, "POST", "/model/new", {"model_name": name, "litellm_params": params, "model_info": {"created_by": "nkt"}})
    return [what]


def model_delete(c, ident, dry):
    ident = str(ident or "").strip()
    for m in models(c):
        info = m.get("model_info") or {}
        if info.get("id") == ident and info.get("db_model"):
            what = c.t("model_deleted", name=m.get("model_name"))
            if dry:
                return [c.t("would", what=what)]
            llm(c, "POST", "/model/delete", {"id": ident})
            return [what]
    raise c.fail("no_model", name=ident)


# --- виды ---------------------------------------------------------------------

def money(v):
    try:
        return f"{float(v):.2f}"
    except (TypeError, ValueError):
        return ""


def limits(c, k):
    parts = []
    if k.get("rpm_limit"):
        parts.append(f"{k['rpm_limit']} rpm")
    if k.get("tpm_limit"):
        parts.append(f"{k['tpm_limit']} tpm")
    return ", ".join(parts) or c.t("never")


def key_fields(c):
    return [
        {"name": "alias", "title": c.t("alias"), "required": True, "hint": c.t("alias_hint"), "placeholder": "videolog"},
        {"name": "models", "title": c.t("models_col"), "hint": c.t("models_hint")},
        {"name": "max_budget", "title": c.t("budget"), "type": "number", "hint": c.t("budget_hint")},
        {"name": "budget_duration", "title": c.t("budget_duration"), "type": "select", "default": "none",
         "options": [{"value": "none", "title": c.t("bd_none")}, {"value": "1d", "title": c.t("bd_1d")},
                     {"value": "7d", "title": c.t("bd_7d")}, {"value": "30d", "title": c.t("bd_30d")}]},
        {"name": "rpm_limit", "title": c.t("rpm"), "type": "number", "hint": c.t("limit_hint")},
        {"name": "tpm_limit", "title": c.t("tpm"), "type": "number", "hint": c.t("limit_hint")},
        {"name": "duration", "title": c.t("duration"), "hint": c.t("duration_hint")},
        {"name": "to_pipeline", "title": c.t("to_pipeline"), "hint": c.t("to_pipeline_hint")},
        {"name": "env", "title": c.t("env"), "hint": c.t("env_hint"), "placeholder": "LLM_API_KEY"},
    ]


def keys_view(c):
    rows = []
    for k in sorted(list_keys(c), key=lambda k: str(k.get("key_alias") or "")):
        alias = k.get("key_alias") or ""
        blocked = bool(k.get("blocked"))
        actions = []
        if alias:
            args = {"alias": alias}
            actions = [
                {"title": c.t("unblock"), "op": "key_unblock", "args": args} if blocked else
                {"title": c.t("block"), "op": "key_block", "args": args, "confirm": c.t("block_confirm", alias=alias)},
                {"title": c.t("delete"), "op": "key_delete", "args": args, "danger": True, "confirm": c.t("delete_key_confirm", alias=alias)},
            ]
        exp = str(k.get("expires") or "")[:16].replace("T", " ") or c.t("never")
        budget = money(k.get("max_budget"))
        if budget and k.get("budget_duration"):
            budget += f" / {k['budget_duration']}"
        rows.append({"cells": {"alias": alias or c.t("never"), "key": k.get("key_name") or "", "models": ", ".join(k.get("models") or []) or c.t("all_models"),
                               "spent": money(k.get("spend")), "budget": budget or c.t("never"), "limits": limits(c, k), "expires": exp,
                               "state": c.t("blocked") if blocked else c.t("active")}, "actions": actions})
    return {"type": "table", "title": c.t("keys"), "rows": rows,
            "actions": [{"title": c.t("add_key"), "op": "key_generate", "fields": key_fields(c)}],
            "columns": [{"key": "alias", "title": c.t("alias")}, {"key": "key", "title": c.t("key")}, {"key": "models", "title": c.t("models_col")},
                        {"key": "spent", "title": c.t("spent")}, {"key": "budget", "title": c.t("budget")}, {"key": "limits", "title": c.t("limits")},
                        {"key": "expires", "title": c.t("expires")}, {"key": "state", "title": c.t("state")}]}


def models_view(c):
    rows = []
    for m in sorted(models(c), key=lambda m: str(m.get("model_name") or "")):
        info, params = m.get("model_info") or {}, m.get("litellm_params") or {}
        name = m.get("model_name") or ""
        actions = []
        if info.get("db_model") and info.get("id"):
            actions = [{"title": c.t("delete"), "op": "model_delete", "args": {"id": info["id"]}, "danger": True, "confirm": c.t("delete_model_confirm", name=name)}]
        rows.append({"cells": {"name": name, "model": params.get("model") or "", "base": params.get("api_base") or "",
                               "source": c.t("in_db") if info.get("db_model") else c.t("in_config")}, "actions": actions})
    add = {"title": c.t("add_model"), "op": "model_add", "fields": [
        {"name": "name", "title": c.t("model_name"), "required": True, "hint": c.t("model_name_hint"), "placeholder": "qwen"},
        {"name": "model", "title": c.t("model"), "required": True, "hint": c.t("model_hint"), "placeholder": "openai/qwen3-8b"},
        {"name": "api_base", "title": c.t("api_base"), "hint": c.t("api_base_hint"), "placeholder": "http://llama:8080/v1"},
        {"name": "api_key", "title": c.t("api_key"), "type": "password", "hint": c.t("api_key_hint")}]}
    return {"type": "table", "title": c.t("models"), "rows": rows, "actions": [add],
            "columns": [{"key": "name", "title": c.t("model_name")}, {"key": "model", "title": c.t("provider")},
                        {"key": "base", "title": c.t("base")}, {"key": "source", "title": c.t("source")}]}


def spend_view(c):
    keys = list_keys(c)
    total = sum(float(k.get("spend") or 0) for k in keys)
    rows = [{"cells": {"alias": k.get("key_alias") or k.get("key_name") or "", "spent": money(k.get("spend")), "budget": money(k.get("max_budget")) or c.t("never")}}
            for k in sorted(keys, key=lambda k: -float(k.get("spend") or 0))]
    rows.append({"cells": {"alias": c.t("total"), "spent": money(total), "budget": ""}})
    return {"type": "table", "title": c.t("spend"), "note": c.t("spend_note"), "rows": rows,
            "columns": [{"key": "alias", "title": c.t("alias")}, {"key": "spent", "title": c.t("spent")}, {"key": "budget", "title": c.t("budget")}]}


# --- команда сценариев ----------------------------------------------------------

def parse_key_add(c, rest):
    """llm key add ИМЯ [models a,b] [budget N] [rpm N] [tpm N] [expires 30d]
    [budget-reset 30d] to-pipeline P env K."""
    if not rest:
        raise Fail(c.t("usage"))
    args = {"alias": rest[0]}
    names = {"models": "models", "budget": "max_budget", "rpm": "rpm_limit", "tpm": "tpm_limit", "expires": "duration",
             "budget-reset": "budget_duration", "to-pipeline": "to_pipeline", "env": "env"}
    i = 1
    while i < len(rest):
        if rest[i] not in names or i + 1 >= len(rest):
            raise Fail(c.t("usage"))
        args[names[rest[i]]] = rest[i + 1]
        i += 2
    if not args.get("to_pipeline") or not args.get("env"):
        raise c.fail("script_needs_pipeline")
    return args


def command(c, argv, dry, check):
    if not argv or argv[0] != "llm" or len(argv) < 2:
        raise Fail(c.t("usage"))
    if argv[1:] == ["keys"]:
        if check:
            return {"ok": True}
        lines = [f"{k.get('key_alias') or k.get('key_name')}: {money(k.get('spend'))}$" + (f" ({c.t('blocked')})" if k.get("blocked") else "") for k in list_keys(c)]
        return {"ok": True, "text": "\n".join(lines)}
    if len(argv) >= 4 and argv[1] == "key":
        sub, rest = argv[2], argv[3:]
        if sub == "add":
            args = parse_key_add(c, rest)
            alias_ok(c, args["alias"])
            if check:
                return {"ok": True}
            return key_generate(c, args, dry)
        if sub in ("block", "unblock", "rm") and len(rest) == 1:
            alias_ok(c, rest[0])
            if check:
                return {"ok": True}
            lines = key_delete(c, rest[0], dry) if sub == "rm" else key_block(c, rest[0], sub == "block", dry)
            return {"ok": True, "text": "\n".join(lines)}
    raise Fail(c.t("usage"))


# --- манифест и вызовы --------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}
N = {"type": "number"}


def manifest(c):
    alias = {"alias": {"type": "string", "description": "key name (key_alias)"}}
    gen = {"alias": S, "models": {"type": "array", "items": S}, "max_budget": N, "budget_duration": {"type": "string", "description": "1d, 7d, 30d"},
           "rpm_limit": N, "tpm_limit": N, "duration": {"type": "string", "description": "validity, e.g. 30d"},
           "to_pipeline": {"type": "string", "description": "pipeline name: the key goes straight into its .env (required for agents)"},
           "env": {"type": "string", "description": ".env variable name, e.g. LLM_API_KEY"}}
    return {
        "protocol": 1, "name": "litellm", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "keys", "title": c.t("keys"), "read_only": True, "mcp": True, "description": "LiteLLM virtual keys: name, models, spend, budget, limits, expiry, blocked."},
            {"name": "models", "title": c.t("models"), "read_only": True, "mcp": True, "description": "Models behind LiteLLM (name for clients, provider model, address)."},
            {"name": "spend", "title": c.t("spend"), "read_only": True, "mcp": True, "description": "Spend per key and total."},
            {"name": "key_generate", "title": c.t("add_key"), "mcp": True, "reveals": True,
             "description": "Create a virtual key. For an agent the key goes only straight into a pipeline .env: pass to_pipeline and env; the value is never returned.",
             "input": schema(gen, ["alias", "to_pipeline", "env"])},
            {"name": "key_block", "title": c.t("block"), "mcp": True, "description": "Block a key by name.", "input": schema(alias, ["alias"])},
            {"name": "key_unblock", "title": c.t("unblock"), "mcp": True, "description": "Unblock a key by name.", "input": schema(alias, ["alias"])},
            {"name": "key_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a key by name.", "input": schema(alias, ["alias"])},
            {"name": "model_add", "title": c.t("add_model"), "mcp": True,
             "description": "Add a model: name for clients, provider/model (openai/… for OpenAI-compatible servers), api_base, api_key.",
             "input": schema({"name": S, "model": S, "api_base": S, "api_key": S}, ["name", "model"])},
            {"name": "model_delete", "title": c.t("delete"), "mcp": True, "destructive": True, "description": "Delete a model added through the API (by model id from models).",
             "input": schema({"id": S}, ["id"])},
        ],
        "pages": [
            {"id": "keys", "title": c.t("keys"), "op": "keys"},
            {"id": "models", "title": c.t("models"), "op": "models"},
            {"id": "spend", "title": c.t("spend"), "op": "spend"},
        ],
        "commands": [{"word": "llm", "usage": c.t("usage"), "example": "llm key add videolog models qwen budget 20 budget-reset 30d to-pipeline videolog env LLM_API_KEY\nllm key block videolog"}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        log(f"подключён к хабу как {args.get('name')}")
        return {"ok": True}
    if op == "keys":
        return {"ok": True, "view": keys_view(c)}
    if op == "models":
        return {"ok": True, "view": models_view(c)}
    if op == "spend":
        return {"ok": True, "view": spend_view(c)}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        out = command(c, [str(a) for a in args.get("argv") or []], dry, False)
        out.pop("reveal", None)  # из сценария — только в .env конвейера
        return out
    if op == "key_generate":
        return key_generate(c, dict(args, _user=req.get("user", "")), dry)
    if op in ("key_block", "key_unblock"):
        lines = key_block(c, args.get("alias"), op == "key_block", dry)
    elif op == "key_delete":
        lines = key_delete(c, args.get("alias"), dry)
    elif op == "model_add":
        lines = model_add(c, args, dry)
    elif op == "model_delete":
        lines = model_delete(c, args.get("id"), dry)
    else:
        return {"ok": False, "error": f"no op {op}"}
    if not dry:
        log(f"{req.get('user', '')}: {op}")
    return {"ok": True, "text": "\n".join(lines) or c.t("nothing")}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-litellm/" + VERSION

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
