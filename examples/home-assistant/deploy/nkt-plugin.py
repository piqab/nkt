"""Плагин хаба nkt для Home Assistant (протокол плагинов nkt v1, provides hosts).

Home Assistant — одно устройство в «Хостах»: «Добавить устройство» →
«Home Assistant» → адрес и долгоживущий токен (профиль пользователя →
«Безопасность» → «Долгоживущие токены доступа»). Всё — через REST API
Home Assistant (/api/…), без дополнений на стороне HA.

Что умеет: состав (версия, области, интеграции, автоматизации, число
устройств и сущностей), находки (недоступные дольше 30 минут сущности,
разряженные батареи, доступные обновления ядра, ОС, дополнений и прошивок,
давно не было резервной копии), конфигурация с диффом (какие сущности,
автоматизации и интеграции есть — без состояний), действия (перезапуск,
установка обновления, сценарий, автоматизация, перечитать автоматизации),
команды — вызов службы HA («light.turn_off {"entity_id": "light.hall"}»).

Токен хранит хаб и передаёт в каждом вызове; плагин его не хранит и не
пишет в журнал. Без зависимостей: стандартная библиотека Python.
"""
import datetime
import hashlib
import hmac
import http.client
import json
import os
import re
import ssl
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9260") or 9260)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
TIMEOUT = float(os.environ.get("HA_TIMEOUT", "20") or 20)
# Недоступна дольше — находка; батарея ниже — находка; копия старше — находка.
UNAVAILABLE_MIN = 30
BATTERY_LOW = 20
BACKUP_DAYS = 7

T = {
    "ru": {
        "title": "Home Assistant", "type": "Home Assistant",
        "desc": "Home Assistant как хост nkt: версия, области, интеграции, автоматизации; находки — недоступные устройства, разряженные батареи, обновления, резервные копии; конфигурация с диффом; перезапуск, обновления, сценарии; вызов служб HA.",
        "hint": "Адрес Home Assistant (порт 8123) и долгоживущий токен: профиль пользователя → «Безопасность» → «Долгоживущие токены доступа». Для действий нужен администратор.",
        "f_address": "Адрес", "f_address_hint": "IP или имя; порт — через двоеточие, если не 8123", "f_token": "Долгоживущий токен",
        "f_tls": "Связь", "tls_http": "HTTP без шифрования", "tls_pin": "HTTPS, свой сертификат (запомнить отпечаток)", "tls_verify": "HTTPS, проверять сертификат",
        "f_fingerprint": "Отпечаток сертификата", "f_fingerprint_hint": "пусто — запомнится при «Проверить»",
        "unreachable": "{addr}: нет связи — {err}", "auth_failed": "{addr}: токен не принят (401)", "http_error": "{addr}: {path} — HTTP {code}",
        "cert_changed": "{addr}: сертификат изменился (был {old}, сейчас {new}) — если это ожидаемо, очистите отпечаток в учётках устройства",
        "not_ha": "{addr}: это не Home Assistant (нет /api/)",
        "bad_entity": "сущность {entity}: нужен вид {domain}.имя (латиница, цифры, _)",
        "bad_service": "команда: «домен.служба» и, если нужно, данные JSON — например light.turn_off {{\"entity_id\": \"light.hall\"}}",
        "bad_json": "данные службы — не объект JSON: {err}",
        "restart": "Перезапустить Home Assistant", "restart_confirm": "Перезапустить Home Assistant? Автоматизации и интеграции остановятся на минуту-две.",
        "update": "Установить обновление", "update_entity": "Сущность update.*", "update_confirm": "Установить обновление? Устройство или Home Assistant может перезапуститься.",
        "script": "Запустить сценарий", "script_entity": "Сущность script.*",
        "automation": "Запустить автоматизацию", "automation_entity": "Сущность automation.*",
        "reload": "Перечитать автоматизации",
        "would": "(сухой прогон) {what}", "would_exec": "вызвать на {addr}: {cmd}", "check_ok": "проверено", "exec_empty": "команда пуста", "done": "выполнено",
        "x_overview": "Сводка", "x_param": "Параметр", "x_value": "Значение", "x_entities": "Сущностей", "x_devices": "Устройств", "x_unavailable": "Недоступно",
        "x_automations": "Автоматизаций", "x_scripts": "Сценариев", "x_integrations": "Интеграций", "x_tz": "Часовой пояс",
        "x_areas": "Области", "x_area": "Область", "x_count": "Сущностей",
        "x_updates": "Доступные обновления", "x_entity": "Сущность", "x_name": "Что", "x_installed": "Стоит", "x_latest": "Доступна",
        "x_unavail": "Недоступные сущности", "x_since": "С",
        "x_batteries": "Батареи", "x_level": "Заряд, %",
        "x_integr": "Интеграции", "x_domain": "Интеграция",
        "f_unavailable": "Недоступно дольше {min} мин: {n} — {names}", "f_unavailable_fix": "проверьте питание и связь устройств (Zigbee, Wi-Fi), «Настройки» → «Устройства»",
        "f_battery": "Батарея «{name}»: {level}%", "f_battery_fix": "заменить или зарядить",
        "f_update": "Доступно обновление: {name} {installed} → {latest}", "f_update_fix": "действие «Установить обновление» ({entity})",
        "f_backup": "Резервной копии нет {days} дн.", "f_backup_none": "Резервных копий нет", "f_backup_fix": "«Настройки» → «Система» → «Резервные копии»: копия и расписание",
    },
    "en": {
        "title": "Home Assistant", "type": "Home Assistant",
        "desc": "Home Assistant as an nkt host: version, areas, integrations, automations; findings — unavailable devices, low batteries, updates, backups; configuration with a diff; restart, updates, scripts; calling HA services.",
        "hint": "The Home Assistant address (port 8123) and a long-lived token: user profile → Security → Long-lived access tokens. Actions need an administrator.",
        "f_address": "Address", "f_address_hint": "IP or name; a port after a colon if not 8123", "f_token": "Long-lived token",
        "f_tls": "Link", "tls_http": "HTTP without encryption", "tls_pin": "HTTPS, own certificate (remember the fingerprint)", "tls_verify": "HTTPS, verify the certificate",
        "f_fingerprint": "Certificate fingerprint", "f_fingerprint_hint": "empty — remembered on Check",
        "unreachable": "{addr}: unreachable — {err}", "auth_failed": "{addr}: the token was rejected (401)", "http_error": "{addr}: {path} — HTTP {code}",
        "cert_changed": "{addr}: the certificate changed (was {old}, now {new}) — if expected, clear the fingerprint in the device login",
        "not_ha": "{addr}: not Home Assistant (no /api/)",
        "bad_entity": "entity {entity}: needs the {domain}.name form (latin letters, digits, _)",
        "bad_service": "the command: “domain.service” and, if needed, JSON data — for example light.turn_off {{“entity_id”: “light.hall”}}",
        "bad_json": "the service data is not a JSON object: {err}",
        "restart": "Restart Home Assistant", "restart_confirm": "Restart Home Assistant? Automations and integrations stop for a minute or two.",
        "update": "Install an update", "update_entity": "An update.* entity", "update_confirm": "Install the update? The device or Home Assistant may restart.",
        "script": "Run a script", "script_entity": "A script.* entity",
        "automation": "Trigger an automation", "automation_entity": "An automation.* entity",
        "reload": "Reload automations",
        "would": "(dry run) {what}", "would_exec": "call on {addr}: {cmd}", "check_ok": "checked", "exec_empty": "the command is empty", "done": "done",
        "x_overview": "Summary", "x_param": "Parameter", "x_value": "Value", "x_entities": "Entities", "x_devices": "Devices", "x_unavailable": "Unavailable",
        "x_automations": "Automations", "x_scripts": "Scripts", "x_integrations": "Integrations", "x_tz": "Time zone",
        "x_areas": "Areas", "x_area": "Area", "x_count": "Entities",
        "x_updates": "Available updates", "x_entity": "Entity", "x_name": "What", "x_installed": "Installed", "x_latest": "Available",
        "x_unavail": "Unavailable entities", "x_since": "Since",
        "x_batteries": "Batteries", "x_level": "Charge, %",
        "x_integr": "Integrations", "x_domain": "Integration",
        "f_unavailable": "Unavailable for over {min} min: {n} — {names}", "f_unavailable_fix": "check the devices' power and link (Zigbee, Wi-Fi), Settings → Devices",
        "f_battery": "Battery “{name}”: {level}%", "f_battery_fix": "replace or charge",
        "f_update": "Update available: {name} {installed} → {latest}", "f_update_fix": "the Install an update action ({entity})",
        "f_backup": "No backup for {days} days", "f_backup_none": "No backups", "f_backup_fix": "Settings → System → Backups: a backup and a schedule",
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
    print(f"nkt-plugin-home-assistant: {msg}", flush=True)


def split_addr(addr):
    addr = str(addr or "").strip()
    for p in ("http://", "https://"):
        if addr.lower().startswith(p):
            addr = addr[len(p):]
    addr = addr.rstrip("/")
    if not re.match(r"^[A-Za-z0-9\[][A-Za-z0-9.\-:\[\]]{0,254}$", addr):
        raise Fail(f"bad address {addr!r}")
    m = re.match(r"^\[([^\]]+)\](?::(\d{1,5}))?$", addr)
    if m:
        return m.group(1), int(m.group(2) or 8123)
    if addr.count(":") == 1:
        h, p = addr.split(":")
        if not h or not p.isdigit() or not 0 < int(p) < 65536:
            raise Fail(f"bad address {addr!r}")
        return h, int(p)
    return addr, 8123


class HA:
    """REST API Home Assistant: Bearer-токен, HTTPS с запомненным отпечатком."""

    def __init__(self, c, conn):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.host, self.port = split_addr(self.addr)
        self.token = str(conn.get("password") or conn.get("token") or "")
        self.mode = str(conn.get("tls") or "http")
        self.pin = str(conn.get("fingerprint") or "").strip().lower().replace(":", "")
        self.seen = ""
        if not self.token or any(ch in self.token for ch in "\r\n"):
            raise c.fail("auth_failed", addr=self.addr)

    def req(self, method, path, body=None):
        c = self.c
        if self.mode == "http":
            conn = http.client.HTTPConnection(self.host, self.port, timeout=TIMEOUT)
        else:
            ctx = ssl.create_default_context()
            if self.mode != "verify":
                ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
            conn = http.client.HTTPSConnection(self.host, self.port, timeout=TIMEOUT, context=ctx)
        h = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        try:
            conn.connect()
            if self.mode != "http":
                self.seen = hashlib.sha256(conn.sock.getpeercert(binary_form=True) or b"").hexdigest()
                if self.mode == "pin" and self.pin and not hmac.compare_digest(self.pin, self.seen):
                    raise c.fail("cert_changed", addr=self.addr, old=self.pin[:16] + "…", new=self.seen[:16] + "…")
            conn.request(method, path, body=data, headers=h)
            r = conn.getresponse()
            raw = r.read()
        except Fail:
            raise
        except (OSError, http.client.HTTPException) as e:
            raise c.fail("unreachable", addr=self.addr, err=type(e).__name__)
        finally:
            conn.close()
        if r.status in (401, 403):
            raise c.fail("auth_failed", addr=self.addr)
        if r.status >= 400:
            raise c.fail("http_error", addr=self.addr, path=path, code=r.status)
        try:
            return json.loads(raw)
        except ValueError:
            return raw.decode(errors="replace")

    def template(self, text):
        return str(self.req("POST", "/api/template", {"template": text}))


def parse_time(s):
    try:
        t = datetime.datetime.fromisoformat(str(s).replace("Z", "+00:00"))
        return t if t.tzinfo else t.replace(tzinfo=datetime.timezone.utc)
    except ValueError:
        return None


def to_num(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


class State:
    """Один снимок: /api/config и /api/states — по разу на вызов."""

    def __init__(self, ha):
        self.ha = ha
        cfg = ha.req("GET", "/api/config")
        if not isinstance(cfg, dict) or "version" not in cfg:
            raise ha.c.fail("not_ha", addr=ha.addr)
        self.cfg = cfg
        st = ha.req("GET", "/api/states")
        self.states = [s for s in st if isinstance(s, dict) and s.get("entity_id")] if isinstance(st, list) else []
        self.now = datetime.datetime.now(datetime.timezone.utc)

    def domain(self, d):
        return [s for s in self.states if s["entity_id"].startswith(d + ".")]

    @staticmethod
    def name(s):
        return str((s.get("attributes") or {}).get("friendly_name") or s["entity_id"])

    def unavailable(self):
        out = []
        for s in self.states:
            if s.get("state") != "unavailable":
                continue
            since = parse_time(s.get("last_changed"))
            if since and (self.now - since).total_seconds() >= UNAVAILABLE_MIN * 60:
                out.append((s, since))
        return sorted(out, key=lambda x: x[1])

    def batteries(self):
        out = []
        for s in self.domain("sensor"):
            a = s.get("attributes") or {}
            level = to_num(s.get("state"))
            if a.get("device_class") == "battery" and level is not None and a.get("unit_of_measurement", "%") == "%":
                out.append((s, level))
        return sorted(out, key=lambda x: x[1])

    def updates(self):
        return [s for s in self.domain("update") if s.get("state") == "on"]

    def backup_age_days(self):
        """None — интеграции резервных копий нет (не находка); inf — копий нет."""
        for eid in ("sensor.backup_last_successful_automatic_backup", "sensor.backup_last_successful_backup"):
            s = next((x for x in self.states if x["entity_id"] == eid), None)
            if s is None:
                continue
            t = parse_time(s.get("state"))
            return float("inf") if t is None else (self.now - t).total_seconds() / 86400
        return None


def info(c, ha, st=None):
    st = st or State(ha)
    cfg = st.cfg
    return {k: v for k, v in {
        "name": str(cfg.get("location_name") or "home-assistant"), "os": "Home Assistant", "version": str(cfg.get("version") or ""),
        "model": str(cfg.get("installation_type") or ""), "address": ha.addr, "fingerprint": ha.seen,
    }.items() if v}


def table(title, cols, rows):
    return {"type": "table", "title": title, "columns": [{"key": k, "title": t} for k, t in cols],
            "rows": [{"cells": {k: "" if r.get(k) is None else str(r.get(k)) for k, _ in cols}} for r in rows]}


def snapshot(c, ha):
    st = State(ha)
    cfg = st.cfg
    devices = ""
    areas = []
    try:
        devices = ha.template("{{ states | map(attribute='entity_id') | map('device_id') | reject('none') | unique | list | count }}").strip()
        raw = ha.template("{% for a in areas() %}{{ area_name(a) }}|{{ area_entities(a) | count }}\n{% endfor %}")
        areas = [{"name": l.split("|")[0], "count": l.split("|")[1]} for l in raw.splitlines() if "|" in l]
    except Fail:
        pass  # шаблоны выключены или старый HA — без устройств и областей
    integrations = sorted({x.split(".")[0] for x in cfg.get("components") or []})
    summary = [{"name": c.t("x_entities"), "value": len(st.states)}, {"name": c.t("x_devices"), "value": devices},
               {"name": c.t("x_unavailable"), "value": sum(1 for s in st.states if s.get("state") == "unavailable")},
               {"name": c.t("x_automations"), "value": len(st.domain("automation"))}, {"name": c.t("x_scripts"), "value": len(st.domain("script"))},
               {"name": c.t("x_integrations"), "value": len(integrations)}, {"name": c.t("x_tz"), "value": cfg.get("time_zone", "")}]
    extra = [table(c.t("x_overview"), [("name", c.t("x_param")), ("value", c.t("x_value"))], [r for r in summary if r["value"] not in ("", None)])]
    if areas:
        extra.append(table(c.t("x_areas"), [("name", c.t("x_area")), ("count", c.t("x_count"))], areas))
    ups = [{"entity": s["entity_id"], "name": (s.get("attributes") or {}).get("title") or st.name(s),
            "installed": (s.get("attributes") or {}).get("installed_version", ""), "latest": (s.get("attributes") or {}).get("latest_version", "")} for s in st.updates()]
    if ups:
        extra.append(table(c.t("x_updates"), [("name", c.t("x_name")), ("installed", c.t("x_installed")), ("latest", c.t("x_latest")), ("entity", c.t("x_entity"))], ups))
    un = [{"entity": s["entity_id"], "name": st.name(s), "since": since.strftime("%Y-%m-%d %H:%M")} for s, since in st.unavailable()[:100]]
    if un:
        extra.append(table(c.t("x_unavail"), [("name", c.t("x_name")), ("entity", c.t("x_entity")), ("since", c.t("x_since"))], un))
    bats = [{"name": st.name(s), "level": int(l), "entity": s["entity_id"]} for s, l in st.batteries()]
    if bats:
        extra.append(table(c.t("x_batteries"), [("name", c.t("x_name")), ("level", c.t("x_level")), ("entity", c.t("x_entity"))], bats))
    extra.append(table(c.t("x_integr"), [("name", c.t("x_domain"))], [{"name": d} for d in integrations]))
    return {
        "info": info(c, ha, st),
        # Автоматизации — как службы: включена ли и что с ней.
        "services": [{"name": st.name(s), "state": str(s.get("state")), "enabled": s.get("state") == "on"} for s in st.domain("automation")],
        "extra": extra,
    }


def findings(c, ha):
    st = State(ha)
    out = []
    un = st.unavailable()
    if un:
        names = ", ".join(st.name(s) for s, _ in un[:5]) + ("…" if len(un) > 5 else "")
        out.append({"id": "unavailable", "severity": "warning", "title": c.t("f_unavailable", min=UNAVAILABLE_MIN, n=len(un), names=names), "fix": c.t("f_unavailable_fix")})
    for s, level in st.batteries():
        if level < BATTERY_LOW:
            out.append({"id": "battery-" + s["entity_id"].split(".", 1)[1], "severity": "warning", "title": c.t("f_battery", name=st.name(s), level=int(level)),
                        "fix": c.t("f_battery_fix")})
    for s in st.updates():
        a = s.get("attributes") or {}
        out.append({"id": "update-" + s["entity_id"].split(".", 1)[1], "severity": "info",
                    "title": c.t("f_update", name=a.get("title") or st.name(s), installed=a.get("installed_version", "?"), latest=a.get("latest_version", "?")),
                    "fix": c.t("f_update_fix", entity=s["entity_id"])})
    age = st.backup_age_days()
    if age is not None and age > BACKUP_DAYS:
        out.append({"id": "backup", "severity": "warning", "title": c.t("f_backup_none") if age == float("inf") else c.t("f_backup", days=int(age)),
                    "fix": c.t("f_backup_fix")})
    return out


def config(ha):
    """Что есть, без состояний: сущности по доменам с именами, интеграции,
    версия — дифф показывает, что добавили, убрали или переименовали."""
    st = State(ha)
    lines = [f"# Home Assistant {st.cfg.get('version', '')}", "", "## integrations"]
    lines += sorted({x.split(".")[0] for x in st.cfg.get("components") or []})
    by = {}
    for s in st.states:
        d = s["entity_id"].split(".", 1)[0]
        by.setdefault(d, []).append(f"{s['entity_id']}  # {st.name(s)}")
    for d in sorted(by):
        lines += ["", f"## {d}"] + sorted(by[d])
    return "\n".join(lines) + "\n"


ENTITY_RE = re.compile(r"^[a-z_]+\.[a-z0-9_]{1,200}$")
SERVICE_RE = re.compile(r"^([a-z_]{1,64})\.([a-z0-9_]{1,64})$")


def entity(c, v, domain):
    v = str(v or "").strip()
    if not ENTITY_RE.match(v) or not v.startswith(domain + "."):
        raise c.fail("bad_entity", entity=v or "—", domain=domain)
    return v


def action(c, ha, name, args, dry):
    if name == "restart":
        what, path, body = c.t("restart"), "/api/services/homeassistant/restart", {}
    elif name == "update":
        e = entity(c, args.get("entity"), "update")
        what, path, body = f"{c.t('update')}: {e}", "/api/services/update/install", {"entity_id": e}
    elif name == "script":
        e = entity(c, args.get("entity"), "script")
        what, path, body = f"{c.t('script')}: {e}", "/api/services/script/turn_on", {"entity_id": e}
    elif name == "automation":
        e = entity(c, args.get("entity"), "automation")
        what, path, body = f"{c.t('automation')}: {e}", "/api/services/automation/trigger", {"entity_id": e}
    elif name == "reload":
        what, path, body = c.t("reload"), "/api/services/automation/reload", {}
    else:
        raise Fail(f"no action {name}")
    if dry:
        return c.t("would", what=what)
    try:
        ha.req("POST", path, body)
    except Fail:
        if name != "restart":
            raise  # перезапуск рвёт соединение — это и ожидалось
    return what


def parse_service(c, cmd):
    """«домен.служба» и необязательные данные JSON-объектом."""
    head, _, rest = cmd.strip().partition(" ")
    m = SERVICE_RE.match(head)
    if not m:
        raise c.fail("bad_service")
    data = {}
    if rest.strip():
        try:
            data = json.loads(rest)
        except ValueError as e:
            raise c.fail("bad_json", err=str(e)[:200])
        if not isinstance(data, dict):
            raise c.fail("bad_json", err=type(data).__name__)
    return m.group(1), m.group(2), data


def manifest(c):
    tls = [{"value": "http", "title": c.t("tls_http")}, {"value": "pin", "title": c.t("tls_pin")}, {"value": "verify", "title": c.t("tls_verify")}]
    return {
        "protocol": 1, "name": "home-assistant", "title": c.t("title"), "version": VERSION, "description": c.t("desc"), "ops": [], "provides": ["hosts"],
        "host_types": [{
            "id": "home-assistant", "title": c.t("type"), "hint": c.t("hint"), "icon": "homeassistant",
            "fields": [{"name": "address", "title": c.t("f_address"), "required": True, "hint": c.t("f_address_hint"), "placeholder": "192.168.1.30"},
                       {"name": "password", "title": c.t("f_token"), "type": "password", "required": True},
                       {"name": "tls", "title": c.t("f_tls"), "type": "select", "default": "http", "options": tls},
                       {"name": "fingerprint", "title": c.t("f_fingerprint"), "hint": c.t("f_fingerprint_hint")}],
            "sections": ["overview", "findings", "config", "actions", "exec"],
            "actions": [{"title": c.t("restart"), "op": "restart", "confirm": c.t("restart_confirm"), "danger": True},
                        {"title": c.t("update"), "op": "update", "confirm": c.t("update_confirm"),
                         "fields": [{"name": "entity", "title": c.t("update_entity"), "required": True, "placeholder": "update.home_assistant_core_update"}]},
                        {"title": c.t("script"), "op": "script", "fields": [{"name": "entity", "title": c.t("script_entity"), "required": True, "placeholder": "script.good_night"}]},
                        {"title": c.t("automation"), "op": "automation",
                         "fields": [{"name": "entity", "title": c.t("automation_entity"), "required": True, "placeholder": "automation.lights_off"}]},
                        {"title": c.t("reload"), "op": "reload"}],
            "exec": "ha-service"}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if not op.startswith("_host_"):
        return {"ok": False, "error": f"no op {op}"}
    if args.get("type") != "home-assistant":
        return {"ok": False, "error": f"unknown host type {args.get('type')}"}
    conn = args.get("conn") or {}
    ha = HA(c, conn)
    if op == "_host_check":
        return {"ok": True, "result": info(c, ha)}
    if op == "_host_snapshot":
        return {"ok": True, "result": snapshot(c, ha)}
    if op == "_host_findings":
        return {"ok": True, "result": {"findings": findings(c, ha)}}
    if op == "_host_config":
        return {"ok": True, "result": {"text": config(ha)}}
    if op == "_host_action":
        text = action(c, ha, str(args.get("action") or ""), args.get("args") or {}, dry)
        if not dry:
            log(f"{req.get('user', '')}: {args.get('action')} на {ha.addr}")
        return {"ok": True, "text": text}
    if op == "_host_exec":
        cmd = str(args.get("command") or "").strip()
        if not cmd:
            raise c.fail("exec_empty")
        domain, service, data = parse_service(c, cmd)
        if truthy(args.get("check")):
            return {"ok": True, "text": c.t("check_ok")}
        if dry:
            return {"ok": True, "text": c.t("would", what=c.t("would_exec", addr=ha.addr, cmd=cmd))}
        res = ha.req("POST", f"/api/services/{domain}/{service}", data)
        log(f"{req.get('user', '')}: {domain}.{service} на {ha.addr}")
        changed = [f"{s.get('entity_id')}: {s.get('state')}" for s in res if isinstance(s, dict)] if isinstance(res, list) else []
        return {"ok": True, "text": "\n".join(changed) or c.t("done")}
    return {"ok": False, "error": f"no op {op}"}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-home-assistant/" + VERSION

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
    log(f"{VERSION}: {LISTEN}:{PORT}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
