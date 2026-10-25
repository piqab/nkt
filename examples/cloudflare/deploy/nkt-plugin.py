# WARNING: пример использует сторонний сервис (Cloudflare) — его доступность и условия не гарантируются.
# WARNING: this example uses a third-party service (Cloudflare) — its availability and terms are not guaranteed.
"""Плагин хаба nkt для DNS у Cloudflare (протокол плагинов nkt v1).

Ставится на машину хаба или любой хост с выходом в интернет: «О системе» →
«Плагины» → «Установить на хост» (служба systemd) или конвейером
(сервис plugin). Слушает 127.0.0.1, хаб ходит по SSH и подписывает каждый
вызов секретом NKT_PLUGIN_SECRET. В Cloudflare ходит API-токеном
CF_API_TOKEN — шаблон «Edit zone DNS», только нужные зоны.

Что умеет:
  - зоны аккаунта (CF_ZONES — ограничить списком): записи таблицей,
    добавить, удалить, «задать» ровно эти значения, вся зона текстом
    «ИМЯ TTL ТИП ЗНАЧЕНИЕ [proxied]» с применением только разницы;
  - делегирование: NS, которые назначил Cloudflare, и что видят публичные
    резолверы;
  - команда сценариев «cf …», инструменты MCP cf_*;
  - для хаба (provides dns): записи маршрутов Traffic Fly, сайтов и имён
    хостов (_dns_sync). Failover у Cloudflare — только платный Load
    Balancing: при нескольких адресах ставятся все (по кругу).
Зоны заводятся в панели Cloudflare (нужен аккаунт и смена NS у
регистратора) — плагин их не создаёт и не удаляет.
Без зависимостей: только стандартная библиотека Python.
"""
import hashlib
import hmac
import ipaddress
import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
API = os.environ.get("CF_API", "https://api.cloudflare.com/client/v4").rstrip("/")
TOKEN = os.environ.get("CF_API_TOKEN", "").strip()
ONLY = [z.strip().rstrip(".").lower() for z in os.environ.get("CF_ZONES", "").split(",") if z.strip()]
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9190") or 9190)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
DEFAULT_TTL = 300
RESOLVERS = [("Cloudflare", "https://cloudflare-dns.com/dns-query"), ("Google", "https://dns.google/resolve")]

T = {
    "ru": {
        "title": "Cloudflare DNS",
        "desc": "Зоны своего аккаунта Cloudflare: записи из хаба (таблицей и текстом с диффом), делегирование, команды «cf …», инструменты MCP, записи для Traffic Fly, сайтов и имён хостов.",
        "zones": "Зоны", "records": "Записи", "zone": "Зона", "status": "Состояние", "ns": "NS Cloudflare",
        "name": "Имя", "ttl": "TTL", "type": "Тип", "value": "Значение", "proxied": "Прокси Cloudflare",
        "zones_note": "Зоны заводятся в панели Cloudflare; здесь — их записи. Прокси (оранжевое облако) — только для HTTP(S) на 80/443; для Traffic Fly и почты — выключен.",
        "open_records": "Записи", "as_text": "Текстом", "delegation": "Делегирование", "delete": "Удалить",
        "add_record": "Добавить запись", "delete_record_confirm": "Удалить {name} {type} {value}?",
        "name_hint": "@ — сама зона; относительное (www) или полное (www.example.com)",
        "value_hint": "A — 203.0.113.10; MX — 10 mail.example.com; TXT — текст; SRV — 10 5 5060 sip.example.com; CAA — 0 issue \"letsencrypt.org\"",
        "ttl_hint": "секунды или auto",
        "proxied_hint": "оранжевое облако: трафик через Cloudflare (A, AAAA, CNAME)",
        "added": "+ {name} {type} {value}", "removed": "- {name} {type} {value}", "nothing": "ничего не изменилось",
        "dry": "(сухой прогон) ",
        "bad_name": "имя {name}: латиница, цифры, дефис, _ и *, метки через точку",
        "bad_type": "тип {type}: умею A, AAAA, CNAME, NS, MX, TXT, SRV, CAA",
        "bad_value": "{type} {value}: {why}",
        "bad_ttl": "TTL {ttl}: auto или число секунд от 60 до 86400",
        "bad_proxied": "proxied — только у A, AAAA и CNAME",
        "no_zone": "имя {name} не входит ни в одну зону этого токена Cloudflare",
        "zone_missing": "зоны {zone} нет у этого токена",
        "no_token": "CF_API_TOKEN не задан — плагин не может войти в Cloudflare",
        "text_header": "# Зона {zone} (Cloudflare): строки «ИМЯ TTL ТИП ЗНАЧЕНИЕ [proxied]», @ — сама зона, TTL — секунды или auto.\n",
        "line_bad": "строка {line}: {why}",
        "line_format": "нужно «ИМЯ TTL ТИП ЗНАЧЕНИЕ [proxied]»",
        "cname_mix": "{name}: CNAME не может стоять рядом с другими записями",
        "apex_ns": "назначает Cloudflare",
        "deleg_title": "Делегирование {zone}",
        "deleg_ns": "Cloudflare назначил серверы имён: {list}",
        "deleg_registrar": "У регистратора домена должны стоять именно они (NS).",
        "deleg_ok": "{who}: делегирована — {list}", "deleg_wrong": "{who}: NS другие — {list}",
        "deleg_none": "{who}: зона не видна (NXDOMAIN или без NS)", "deleg_err": "{who}: не проверить — {err}",
        "no_failover": "{name}: у Cloudflare Failover — только платный Load Balancing; поставлены все адреса ({list}), отдаются по кругу",
        "usage": "cf set ИМЯ ТИП ЗНАЧЕНИЕ[,…] [ttl СЕК] [proxied] | cf add ИМЯ ТИП ЗНАЧЕНИЕ [ttl СЕК] [proxied] | cf rm ИМЯ [ТИП [ЗНАЧЕНИЕ]] | cf check ЗОНА",
        "check_ok": "проверено",
        "op_zones": "Список зон", "op_records": "Записи зоны", "op_record_set": "Задать записи имени: ровно эти значения типа",
        "op_record_add": "Добавить запись", "op_record_delete": "Удалить записи", "op_delegation": "Проверить делегирование",
    },
    "en": {
        "title": "Cloudflare DNS",
        "desc": "Zones of your Cloudflare account: records from the hub (as a table and as text with a diff), delegation, “cf …” commands, MCP tools, records for Traffic Fly, sites and host names.",
        "zones": "Zones", "records": "Records", "zone": "Zone", "status": "Status", "ns": "Cloudflare NS",
        "name": "Name", "ttl": "TTL", "type": "Type", "value": "Value", "proxied": "Cloudflare proxy",
        "zones_note": "Zones are created in the Cloudflare dashboard; here are their records. The proxy (orange cloud) is for HTTP(S) on 80/443 only; off for Traffic Fly and mail.",
        "open_records": "Records", "as_text": "As text", "delegation": "Delegation", "delete": "Delete",
        "add_record": "Add record", "delete_record_confirm": "Delete {name} {type} {value}?",
        "name_hint": "@ — the zone itself; relative (www) or full (www.example.com)",
        "value_hint": "A — 203.0.113.10; MX — 10 mail.example.com; TXT — text; SRV — 10 5 5060 sip.example.com; CAA — 0 issue \"letsencrypt.org\"",
        "ttl_hint": "seconds or auto",
        "proxied_hint": "orange cloud: traffic through Cloudflare (A, AAAA, CNAME)",
        "added": "+ {name} {type} {value}", "removed": "- {name} {type} {value}", "nothing": "nothing changed",
        "dry": "(dry run) ",
        "bad_name": "name {name}: Latin letters, digits, hyphens, _ and *, labels separated by dots",
        "bad_type": "type {type}: A, AAAA, CNAME, NS, MX, TXT, SRV, CAA are supported",
        "bad_value": "{type} {value}: {why}",
        "bad_ttl": "TTL {ttl}: auto or seconds from 60 to 86400",
        "bad_proxied": "proxied — only for A, AAAA and CNAME",
        "no_zone": "name {name} is in no zone of this Cloudflare token",
        "zone_missing": "this token has no zone {zone}",
        "no_token": "CF_API_TOKEN is not set — the plugin can't sign in to Cloudflare",
        "text_header": "# Zone {zone} (Cloudflare): lines “NAME TTL TYPE VALUE [proxied]”, @ is the zone itself, TTL — seconds or auto.\n",
        "line_bad": "line {line}: {why}",
        "line_format": "expected “NAME TTL TYPE VALUE [proxied]”",
        "cname_mix": "{name}: a CNAME can't stand next to other records",
        "apex_ns": "assigned by Cloudflare",
        "deleg_title": "Delegation of {zone}",
        "deleg_ns": "Cloudflare assigned the name servers: {list}",
        "deleg_registrar": "Exactly these must be set (NS) at the domain registrar.",
        "deleg_ok": "{who}: delegated — {list}", "deleg_wrong": "{who}: different NS — {list}",
        "deleg_none": "{who}: the zone isn't visible (NXDOMAIN or no NS)", "deleg_err": "{who}: can't check — {err}",
        "no_failover": "{name}: Cloudflare has Failover only in paid Load Balancing; all addresses are set ({list}), served round-robin",
        "usage": "cf set NAME TYPE VALUE[,…] [ttl SEC] [proxied] | cf add NAME TYPE VALUE [ttl SEC] [proxied] | cf rm NAME [TYPE [VALUE]] | cf check ZONE",
        "check_ok": "checked",
        "op_zones": "List zones", "op_records": "Zone records", "op_record_set": "Set a name's records: exactly these values of the type",
        "op_record_add": "Add a record", "op_record_delete": "Delete records", "op_delegation": "Check delegation",
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
    print(f"nkt-plugin-cloudflare: {msg}", flush=True)


# --- Cloudflare API -----------------------------------------------------------

def cf(c, method, path, body=None, params=None):
    if not TOKEN:
        raise c.fail("no_token")
    url = f"{API}{path}"
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={"Authorization": f"Bearer {TOKEN}", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            res = json.load(r)
    except urllib.error.HTTPError as e:
        try:
            res = json.load(e)
        except ValueError:
            raise Fail(f"Cloudflare {method} {path}: HTTP {e.code}")
    if not res.get("success"):
        errs = "; ".join(f"{x.get('code')}: {x.get('message')}" for x in res.get("errors") or []) or "error"
        raise Fail(f"Cloudflare {method} {path}: {errs}")
    return res


def paged(c, path, params=None):
    out, page = [], 1
    while True:
        res = cf(c, "GET", path, params={**(params or {}), "page": page, "per_page": 100})
        out += res.get("result") or []
        info = res.get("result_info") or {}
        if page >= int(info.get("total_pages") or 1):
            return out
        page += 1


ZONES_CACHE = {"at": 0.0, "zones": {}}
LOCK = threading.Lock()


def zones(c, fresh=False):
    """Зоны токена: имя → {id, status, name_servers}."""
    with LOCK:
        if not fresh and time.time() - ZONES_CACHE["at"] < 60:
            return ZONES_CACHE["zones"]
        out = {}
        for z in paged(c, "/zones"):
            name = z["name"].lower()
            if ONLY and name not in ONLY:
                continue
            out[name] = {"id": z["id"], "status": z.get("status", ""), "ns": z.get("name_servers") or []}
        ZONES_CACHE.update(at=time.time(), zones=out)
        return out


# --- имена и значения -----------------------------------------------------------

LABEL = r"(?:\*|_?[a-z0-9](?:[a-z0-9_-]{0,61}[a-z0-9])?)"
NAME_RE = re.compile(rf"^{LABEL}(?:\.{LABEL})*$")
TYPES = ["A", "AAAA", "CNAME", "NS", "MX", "TXT", "SRV", "CAA"]
PROXIABLE = {"A", "AAAA", "CNAME"}


def zone_of(c, fqdn, zs=None):
    zs = zs if zs is not None else zones(c)
    best = ""
    for z in zs:
        if (fqdn == z or fqdn.endswith("." + z)) and len(z) > len(best):
            best = z
    if not best:
        raise c.fail("no_zone", name=fqdn)
    return best


def to_fqdn(c, name, zone=None):
    n = str(name or "").strip().lower()
    if n in ("@", "") and zone:
        return zone
    if n.endswith("."):
        n = n[:-1]
    elif zone and not (n == zone or n.endswith("." + zone)):
        n = f"{n}.{zone}"
    if not NAME_RE.match(n) or len(n) > 253:
        raise c.fail("bad_name", name=name)
    return n


def rel(fqdn, zone):
    if fqdn == zone:
        return "@"
    return fqdn[: -(len(zone) + 1)] if fqdn.endswith("." + zone) else fqdn + "."


def host(c, typ, value, zone):
    v = value.strip().lower()
    if v == "@" and zone:
        return zone
    absolute = v.endswith(".")
    v = v.rstrip(".")
    if not NAME_RE.match(v):
        raise c.fail("bad_value", type=typ, value=value, why="host name")
    if not absolute and zone and not (v == zone or v.endswith("." + zone)) and "." not in v:
        v = f"{v}.{zone}"
    return v


def parse_value(c, typ, value, zone=None):
    """Значение → нормальная форма (кортеж) для сравнения и тела API."""
    value = str(value).strip()
    try:
        if typ in ("A", "AAAA"):
            ip = ipaddress.ip_address(value)
            if (typ == "A") != (ip.version == 4):
                raise ValueError("IPv4 for A, IPv6 for AAAA")
            return (str(ip),)
        if typ in ("CNAME", "NS"):
            return (host(c, typ, value, zone),)
        if typ == "MX":
            pref, exch = value.split(None, 1)
            return (int(pref), host(c, typ, exch, zone))
        if typ == "TXT":
            text = value[1:-1] if len(value) >= 2 and value[0] == value[-1] == '"' else value
            if not text or len(text) > 2048:
                raise ValueError("empty or longer than 2048")
            return (text.replace('\\"', '"'),)
        if typ == "SRV":
            prio, weight, port, target = value.split()
            return (int(prio), int(weight), int(port), host(c, typ, target, zone))
        if typ == "CAA":
            flags, tag, val = value.split(None, 2)
            return (int(flags), tag, val.strip('"'))
    except Fail:
        raise
    except (ValueError, TypeError) as e:
        raise c.fail("bad_value", type=typ, value=value, why=str(e) or "format")
    raise c.fail("bad_type", type=typ)


def show(typ, v, zone):
    """Нормальная форма → как пишется в тексте зоны."""
    short = lambda h: rel(h, zone)
    if typ in ("A", "AAAA"):
        return v[0]
    if typ in ("CNAME", "NS"):
        return short(v[0])
    if typ == "MX":
        return f"{v[0]} {short(v[1])}"
    if typ == "TXT":
        return '"' + v[0].replace('"', '\\"') + '"'
    if typ == "SRV":
        return f"{v[0]} {v[1]} {v[2]} {short(v[3])}"
    if typ == "CAA":
        return f'{v[0]} {v[1]} "{v[2]}"'
    return str(v)


def from_cf(r):
    """Запись Cloudflare → (fqdn, type, нормальная форма, ttl, proxied, id)."""
    typ, name = r["type"], r["name"].lower()
    d = r.get("data") or {}
    if typ in ("A", "AAAA"):
        v = (r["content"],)
    elif typ in ("CNAME", "NS"):
        v = (r["content"].lower().rstrip("."),)
    elif typ == "MX":
        v = (int(r.get("priority") or 0), r["content"].lower().rstrip("."))
    elif typ == "TXT":
        t = r["content"]
        v = (t[1:-1] if len(t) >= 2 and t[0] == t[-1] == '"' else t,)
    elif typ == "SRV":
        v = (int(d.get("priority", r.get("priority") or 0)), int(d.get("weight", 0)), int(d.get("port", 0)), str(d.get("target", "")).lower().rstrip("."))
    elif typ == "CAA":
        v = (int(d.get("flags", 0)), d.get("tag", ""), d.get("value", ""))
    else:
        return None
    return (name, typ, v, int(r.get("ttl") or 1), bool(r.get("proxied")), r["id"])


def to_cf(typ, fqdn, v, ttl, proxied):
    body = {"type": typ, "name": fqdn, "ttl": ttl}
    if typ in PROXIABLE:
        body["proxied"] = bool(proxied)
    if typ in ("A", "AAAA", "CNAME", "NS"):
        body["content"] = v[0]
    elif typ == "MX":
        body["content"], body["priority"] = v[1], v[0]
    elif typ == "TXT":
        body["content"] = '"' + v[0].replace('"', '\\"') + '"'
    elif typ == "SRV":
        body["data"] = {"priority": v[0], "weight": v[1], "port": v[2], "target": v[3]}
    elif typ == "CAA":
        body["data"] = {"flags": v[0], "tag": v[1], "value": v[2]}
    return body


def check_ttl(c, ttl):
    if str(ttl).strip().lower() in ("auto", "1"):
        return 1
    try:
        n = int(ttl)
    except (TypeError, ValueError):
        raise c.fail("bad_ttl", ttl=ttl)
    if n < 60 or n > 86400:
        raise c.fail("bad_ttl", ttl=ttl)
    return n


def ttl_text(ttl):
    return "auto" if ttl == 1 else str(ttl)


def zone_records(c, zone):
    zid = zones(c)[zone]["id"]
    out = []
    for r in paged(c, f"/zones/{zid}/dns_records"):
        x = from_cf(r)
        if x:
            out.append(x)
    return out


# --- изменения ----------------------------------------------------------------

def apply_changes(c, zone, adds, dels, dry):
    """adds — (fqdn, type, v, ttl, proxied); dels — записи from_cf."""
    zid = zones(c)[zone]["id"]
    lines = []
    for fqdn, typ, v, _, _, rid in dels:
        lines.append(c.t("removed", name=fqdn, type=typ, value=show(typ, v, zone)))
        if not dry:
            cf(c, "DELETE", f"/zones/{zid}/dns_records/{rid}")
    for fqdn, typ, v, ttl, proxied in adds:
        lines.append(c.t("added", name=fqdn, type=typ, value=show(typ, v, zone)) + (" proxied" if proxied else ""))
        if not dry:
            cf(c, "POST", f"/zones/{zid}/dns_records", to_cf(typ, fqdn, v, ttl, proxied))
    return lines


def check_type(c, typ, proxied=False):
    typ = str(typ or "").upper()
    if typ not in TYPES:
        raise c.fail("bad_type", type=typ)
    if proxied and typ not in PROXIABLE:
        raise c.fail("bad_proxied")
    return typ


def record_set(c, name, typ, values, ttl, proxied, dry, zs=None):
    typ = check_type(c, typ, proxied)
    zone = zone_of(c, to_fqdn(c, name), zs)
    fqdn = to_fqdn(c, name, zone)
    ttl = check_ttl(c, ttl)
    want = {parse_value(c, typ, v, zone) for v in values if str(v).strip()}
    have = [r for r in zone_records(c, zone) if r[0] == fqdn and r[1] == typ]
    have_v = {r[2] for r in have}
    adds = [(fqdn, typ, v, ttl, proxied) for v in sorted(want, key=str) if v not in have_v]
    dels = [r for r in have if r[2] not in want]
    return apply_changes(c, zone, adds, dels, dry)


def record_add(c, name, typ, value, ttl, proxied, dry):
    typ = check_type(c, typ, proxied)
    zone = zone_of(c, to_fqdn(c, name))
    fqdn = to_fqdn(c, name, zone)
    v = parse_value(c, typ, value, zone)
    if any(r[0] == fqdn and r[1] == typ and r[2] == v for r in zone_records(c, zone)):
        return []
    return apply_changes(c, zone, [(fqdn, typ, v, check_ttl(c, ttl), proxied)], [], dry)


def record_delete(c, name, typ, value, dry):
    zone = zone_of(c, to_fqdn(c, name))
    fqdn = to_fqdn(c, name, zone)
    typ = str(typ or "").upper()
    want = parse_value(c, typ, value, zone) if typ and value else None
    dels = [r for r in zone_records(c, zone) if r[0] == fqdn and (not typ or r[1] == typ) and (want is None or r[2] == want)]
    return apply_changes(c, zone, [], dels, dry)


# --- зона текстом -------------------------------------------------------------

def zone_text(c, zone):
    zone = str(zone or "").strip().rstrip(".").lower()
    if zone not in zones(c):
        raise c.fail("zone_missing", zone=zone)
    rows = sorted(zone_records(c, zone), key=lambda r: (rel(r[0], zone) != "@", rel(r[0], zone), TYPES.index(r[1]) if r[1] in TYPES else 99, str(r[2])))
    lines = [c.t("text_header", zone=zone).rstrip("\n")]
    width = max([len(rel(r[0], zone)) for r in rows] + [1])
    for fqdn, typ, v, ttl, proxied, _ in rows:
        line = f"{rel(fqdn, zone).ljust(width)}  {ttl_text(ttl).ljust(6)} {typ.ljust(5)} {show(typ, v, zone)}" + (" proxied" if proxied else "")
        # NS вершины назначает Cloudflare — видно, но не правится.
        lines.append(f"# {line}  ({c.t('apex_ns')})" if typ == "NS" and fqdn == zone else line)
    return "\n".join(lines) + "\n"


def parse_text(c, zone, text):
    out = {}
    names = {}
    for i, line in enumerate(str(text).splitlines(), 1):
        s = line.strip()
        if not s or s.startswith("#") or s.startswith(";"):
            continue
        parts = s.split(None, 3)
        if len(parts) < 4:
            raise c.fail("line_bad", line=i, why=c.t("line_format"))
        name, ttl, typ, value = parts
        proxied = False
        if value.endswith(" proxied") or value == "proxied":
            proxied, value = True, value[: -len("proxied")].rstrip()
        try:
            fqdn = to_fqdn(c, name, zone)
            typ = check_type(c, typ, proxied)
            ttl = check_ttl(c, ttl)
            v = parse_value(c, typ, value, zone)
        except Fail as e:
            raise c.fail("line_bad", line=i, why=str(e))
        out[(fqdn, typ, v)] = (fqdn, typ, v, ttl, proxied)
        names.setdefault(fqdn, set()).add(typ)
    for fqdn, types in names.items():
        if "CNAME" in types and len(types) > 1:
            raise c.fail("cname_mix", name=fqdn)
    return out


def zone_apply(c, zone, text, dry):
    zone = str(zone or "").strip().rstrip(".").lower()
    if zone not in zones(c):
        raise c.fail("zone_missing", zone=zone)
    # NS вершины назначает сам Cloudflare — не трогаем ни в тексте, ни в зоне.
    want = {k: w for k, w in parse_text(c, zone, text).items() if not (w[1] == "NS" and w[0] == zone)}
    have = {(r[0], r[1], r[2]): r for r in zone_records(c, zone) if not (r[1] == "NS" and r[0] == zone)}
    dels = [r for k, r in have.items() if k not in want or (want[k][3], want[k][4]) != (r[3], r[4])]
    adds = [w for k, w in want.items() if k not in have or (w[3], w[4]) != (have[k][3], have[k][4])]
    return apply_changes(c, zone, adds, dels, dry)


# --- делегирование ------------------------------------------------------------

def doh_ns(url, zone):
    q = urllib.parse.urlencode({"name": zone, "type": "NS"})
    req = urllib.request.Request(f"{url}?{q}", headers={"Accept": "application/dns-json"})
    with urllib.request.urlopen(req, timeout=10) as r:
        res = json.load(r)
    if res.get("Status") == 3:
        return None
    return sorted({a["data"].rstrip(".").lower() for a in res.get("Answer", []) if a.get("type") == 2})


def delegation(c, zone):
    zone = str(zone or "").strip().rstrip(".").lower()
    zs = zones(c)
    if zone not in zs:
        raise c.fail("zone_missing", zone=zone)
    ns = sorted(n.lower() for n in zs[zone]["ns"])
    lines = [c.t("deleg_ns", list=", ".join(ns) or "—"), c.t("deleg_registrar"), ""]
    for who, url in RESOLVERS:
        try:
            got = doh_ns(url, zone)
            if not got:
                lines.append(c.t("deleg_none", who=who))
            elif set(got) == set(ns):
                lines.append(c.t("deleg_ok", who=who, list=", ".join(got)))
            else:
                lines.append(c.t("deleg_wrong", who=who, list=", ".join(got)))
        except Exception as e:  # сеть до резолвера — не повод падать
            lines.append(c.t("deleg_err", who=who, err=e))
    return "\n".join(lines)


# --- записи хаба (_dns_sync) ----------------------------------------------------

def dns_sync(c, args, dry):
    """Записи владельца: привести к records, убрав из previous то, чего в
    records нет; чужие значения у того же имени не трогаются. Прокси —
    выключен (шлюз Traffic Fly и сайт сами держат TLS)."""
    zs = zones(c)
    new = args.get("records") or []
    prev = args.get("previous") or []
    key = lambda r: (to_fqdn(c, r["name"]), str(r.get("type", "A")).upper())
    want = {key(r): r for r in new}
    lines = []
    cache = {}

    def recs(zone):
        if zone not in cache:
            cache[zone] = zone_records(c, zone)
        return cache[zone]

    for r in prev:
        k = key(r)
        try:
            zone = zone_of(c, k[0], zs)
        except Fail:
            continue  # зону убрали — убирать нечего
        keep = {parse_value(c, k[1], v, zone) for v in (want[k].get("values") or [])} if k in want else set()
        gone = {parse_value(c, k[1], v, zone) for v in r.get("values") or []} - keep
        dels = [x for x in recs(zone) if x[0] == k[0] and x[1] == k[1] and x[2] in gone]
        lines += apply_changes(c, zone, [], dels, dry)
        cache.pop(zone, None)
    for k, r in want.items():
        zone = zone_of(c, k[0], zs)
        values = [str(v) for v in r.get("values") or []]
        if r.get("failover") and len(values) > 1:
            lines.append(c.t("no_failover", name=k[0], list=", ".join(values)))
        ttl = check_ttl(c, r.get("ttl") or DEFAULT_TTL)
        have = {x[2] for x in recs(zone) if x[0] == k[0] and x[1] == k[1]}
        adds = [(k[0], k[1], v, ttl, False) for v in (parse_value(c, k[1], s, zone) for s in values) if v not in have]
        lines += apply_changes(c, zone, adds, [], dry)
        cache.pop(zone, None)
    return lines


# --- виды ---------------------------------------------------------------------

def zones_view(c):
    rows = []
    for name, z in sorted(zones(c, fresh=True).items()):
        args = {"zone": name}
        rows.append({"cells": {"zone": name, "status": z["status"], "ns": ", ".join(z["ns"])}, "actions": [
            {"title": c.t("open_records"), "open": "records", "args": args},
            {"title": c.t("as_text"), "args": args, "edit": {"get": "zone_text", "put": "zone_apply", "doc": "cf-" + name}},
            {"title": c.t("delegation"), "open": "delegation", "args": args},
        ]})
    return {"type": "table", "title": c.t("zones"), "note": c.t("zones_note"), "rows": rows,
            "columns": [{"key": "zone", "title": c.t("zone")}, {"key": "status", "title": c.t("status")}, {"key": "ns", "title": c.t("ns")}]}


def records_view(c, zone):
    zone = str(zone or "").strip().rstrip(".").lower()
    if zone not in zones(c):
        raise c.fail("zone_missing", zone=zone)
    rows = []
    for fqdn, typ, v, ttl, proxied, _ in sorted(zone_records(c, zone), key=lambda r: (rel(r[0], zone) != "@", rel(r[0], zone), r[1])):
        val = show(typ, v, zone)
        actions = [] if typ == "NS" and fqdn == zone else [
            {"title": c.t("delete"), "op": "record_delete", "danger": True, "args": {"name": fqdn, "type": typ, "value": val},
             "confirm": c.t("delete_record_confirm", name=fqdn, type=typ, value=val)}]
        rows.append({"cells": {"name": rel(fqdn, zone), "ttl": ttl_text(ttl), "type": typ, "value": val, "proxied": "✓" if proxied else ""}, "actions": actions})
    add = {"title": c.t("add_record"), "op": "record_add", "args": {"zone": zone}, "fields": [
        {"name": "name", "title": c.t("name"), "required": True, "default": "@", "hint": c.t("name_hint")},
        {"name": "type", "title": c.t("type"), "type": "select", "required": True, "default": "A", "options": [{"value": t} for t in TYPES]},
        {"name": "value", "title": c.t("value"), "required": True, "hint": c.t("value_hint")},
        {"name": "ttl", "title": c.t("ttl"), "default": "auto", "hint": c.t("ttl_hint")},
        {"name": "proxied", "title": c.t("proxied"), "type": "bool", "hint": c.t("proxied_hint")}]}
    edit = {"title": c.t("as_text"), "args": {"zone": zone}, "edit": {"get": "zone_text", "put": "zone_apply", "doc": "cf-" + zone}}
    return {"type": "table", "title": f"{c.t('records')} — {zone}", "rows": rows, "actions": [add, edit], "columns": [
        {"key": "name", "title": c.t("name")}, {"key": "ttl", "title": c.t("ttl")}, {"key": "type", "title": c.t("type")},
        {"key": "value", "title": c.t("value")}, {"key": "proxied", "title": c.t("proxied")}]}


# --- команда сценариев ----------------------------------------------------------

def command(c, argv, dry, check):
    if not argv or argv[0] != "cf" or len(argv) < 3:
        raise Fail(c.t("usage"))
    sub, rest = argv[1], argv[2:]
    proxied = bool(rest) and rest[-1] == "proxied"
    if proxied:
        rest = rest[:-1]
    ttl = DEFAULT_TTL
    if len(rest) >= 2 and rest[-2] == "ttl":
        ttl, rest = rest[-1], rest[:-2]
    if sub == "check" and len(rest) == 1 and not proxied:
        return [] if check else delegation(c, rest[0]).splitlines()
    if sub in ("set", "add") and len(rest) >= 3:
        name, typ, value = rest[0], check_type(c, rest[1], proxied), " ".join(rest[2:])
        check_ttl(c, ttl)
        to_fqdn(c, name)
        values = [v.strip() for v in value.split(",")] if sub == "set" and typ in ("A", "AAAA", "NS") else [value]
        if check:
            for v in values:
                parse_value(c, typ, v)
            return []
        if sub == "set":
            return record_set(c, name, typ, values, ttl, proxied, dry)
        return record_add(c, name, typ, value, ttl, proxied, dry)
    if sub == "rm" and rest and not proxied:
        typ = rest[1].upper() if len(rest) > 1 else ""
        if typ:
            check_type(c, typ)
        to_fqdn(c, rest[0])
        return [] if check else record_delete(c, rest[0], typ, " ".join(rest[2:]), dry)
    raise Fail(c.t("usage"))


# --- манифест и вызовы --------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}


def manifest(c):
    zone = {"zone": {"type": "string", "description": "zone, e.g. example.com"}}
    return {
        "protocol": 1, "name": "cloudflare", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "zones", "title": c.t("op_zones"), "read_only": True, "mcp": True, "description": "List Cloudflare DNS zones of the token (status, assigned NS)."},
            {"name": "records", "title": c.t("op_records"), "read_only": True, "mcp": True, "description": "Records of a Cloudflare zone.", "input": schema(zone, ["zone"])},
            {"name": "zone_text", "read_only": True, "mcp": True, "description": "A zone as text lines “NAME TTL TYPE VALUE [proxied]” (for zone_apply).", "input": schema(zone, ["zone"])},
            {"name": "delegation", "title": c.t("op_delegation"), "read_only": True, "mcp": True, "description": "Delegation check: NS assigned by Cloudflare and what public resolvers see.", "input": schema(zone, ["zone"])},
            {"name": "zone_apply", "mcp": True, "destructive": True, "description": "Replace a zone's records with text lines (from zone_text); apex NS are kept; dry_run shows the changes.",
             "input": schema({**zone, "text": S, "dry_run": {"type": "boolean"}}, ["zone", "text"])},
            {"name": "record_set", "title": c.t("op_record_set"), "mcp": True, "description": "Set exactly these values of a type for a name (adds missing, removes others of that type). proxied — Cloudflare proxy for A/AAAA/CNAME.",
             "input": schema({"name": S, "type": {"type": "string", "enum": TYPES}, "values": {"type": "array", "items": S}, "ttl": S, "proxied": {"type": "boolean"}}, ["name", "type", "values"])},
            {"name": "record_add", "title": c.t("op_record_add"), "mcp": True, "description": "Add one record.",
             "input": schema({"name": S, "type": {"type": "string", "enum": TYPES}, "value": S, "ttl": S, "proxied": {"type": "boolean"}, "zone": S}, ["name", "type", "value"])},
            {"name": "record_delete", "title": c.t("op_record_delete"), "mcp": True, "destructive": True, "description": "Delete a name's records (all, of a type, or one value).",
             "input": schema({"name": S, "type": S, "value": S}, ["name"])},
        ],
        "pages": [
            {"id": "zones", "title": c.t("zones"), "op": "zones"},
            {"id": "records", "title": c.t("records"), "op": "records", "hidden": True},
            {"id": "delegation", "title": c.t("delegation"), "op": "delegation", "hidden": True},
        ],
        "commands": [{"word": "cf", "usage": c.t("usage"), "example": "cf set app.example.com A 203.0.113.10 ttl 300\ncf add example.com TXT \"verify=abc\""}],
        "provides": ["dns"],
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
    if op == "zones":
        return {"ok": True, "view": zones_view(c)}
    if op == "records":
        zone = args.get("zone") or next(iter(sorted(zones(c))), "")
        return {"ok": True, "view": records_view(c, zone) if zone else zones_view(c)}
    if op == "zone_text":
        return {"ok": True, "result": {"text": zone_text(c, args.get("zone"))}}
    if op == "delegation":
        zone = args.get("zone") or next(iter(sorted(zones(c))), "")
        return {"ok": True, "view": {"type": "text", "title": c.t("deleg_title", zone=zone), "text": delegation(c, zone)}}
    if op == "_dns_zones":
        return {"ok": True, "result": {"zones": sorted(zones(c))}}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    proxied = truthy(args.get("proxied"))
    if op == "_command":
        lines = command(c, [str(a) for a in args.get("argv") or []], dry, False)
    elif op == "_dns_sync":
        lines = dns_sync(c, args, dry)
    elif op == "zone_apply":
        lines = zone_apply(c, args.get("zone"), args.get("text", ""), dry)
    elif op == "record_set":
        values = args.get("values")
        if isinstance(values, str):
            values = [v.strip() for v in values.split(",")]
        lines = record_set(c, args.get("name"), args.get("type"), values or [], args.get("ttl") or DEFAULT_TTL, proxied, dry)
    elif op == "record_add":
        name = args.get("name")
        if args.get("zone"):
            name = to_fqdn(c, name, str(args["zone"]).strip().rstrip(".").lower())
        lines = record_add(c, name, args.get("type"), str(args.get("value", "")), args.get("ttl") or "auto", proxied, dry)
    elif op == "record_delete":
        lines = record_delete(c, args.get("name"), args.get("type"), args.get("value"), dry)
    else:
        return {"ok": False, "error": f"no op {op}"}
    text = "\n".join(lines) or c.t("nothing")
    if dry:
        text = "\n".join(c.t("dry") + line for line in text.splitlines())
    else:
        log(f"{req.get('user', '')}: {op} → {len(lines)}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-cloudflare/" + VERSION

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
        if size > 4 << 20:
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
        except urllib.error.URLError as e:
            self.reply({"ok": False, "error": f"Cloudflare {API}: {e}"})
        except Exception as e:  # ошибка плагина — текстом хабу, а не обрывом
            log(f"ошибка: {e!r}")
            self.reply({"ok": False, "error": f"plugin error: {e}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, {API}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
