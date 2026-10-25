# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
"""Плагин хаба nkt для Technitium DNS Server (протокол плагинов nkt v1).

Сервис plugin стека technitium: слушает 0.0.0.0:9180 в контейнере
(наружу — только 127.0.0.1 хоста), хаб ходит к нему по SSH и подписывает
каждый запрос секретом NKT_PLUGIN_SECRET. С Technitium говорит по его API
внутри стека (http://dns:5380) — консоль наружу не нужна.

Что умеет:
  - зоны: список, добавить (первичной на ns1; вторичной на ns2 её делает
    зона каталога — init.py), удалить, шаблоны (сайт, почта);
  - записи: таблица, добавить, удалить, «задать» (ровно эти значения),
    правка зоны текстом — построчно «ИМЯ TTL ТИП ЗНАЧЕНИЕ» с диффом;
  - делегирование: что у регистратора и что видят публичные резолверы;
  - DNSSEC: подписать, снять подпись, записи DS для регистратора;
  - команда сценариев «dns …», инструменты MCP dns_*;
  - для хаба (provides dns): записи маршрутов Traffic Fly и сайтов
    (_dns_sync), Failover при нескольких адресах.
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
API = os.environ.get("DNS_API", "http://dns:5380/api")
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9180") or 9180)
# Где слушать: в контейнере — 0.0.0.0 (наружу публикует compose на
# 127.0.0.1), службой на хосте — 127.0.0.1.
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
NS1 = os.environ.get("DNS_DOMAIN", "").strip().rstrip(".").lower()
NS2 = os.environ.get("SECONDARY_NS", "").strip().rstrip(".").lower()
SECONDARY_IP = os.environ.get("SECONDARY_IP", "").strip()
CATALOG = os.environ.get("CATALOG_ZONE", "catalog.nkt.invalid").strip().rstrip(".").lower()
ACME_ZONE = os.environ.get("ACME_ZONE", "").strip().rstrip(".").lower()
DEFAULT_TTL = 300
# Публичные резолверы для проверки делегирования (DNS over HTTPS, JSON).
RESOLVERS = [("Cloudflare", "https://cloudflare-dns.com/dns-query"), ("Google", "https://dns.google/resolve")]

# --- тексты ------------------------------------------------------------------

T = {
    "ru": {
        "title": "Technitium DNS",
        "desc": "Зоны и записи своих доменов на Technitium (ns1 — первичный, ns2 — вторичный через зону каталога), делегирование, DNSSEC, записи для Traffic Fly и сайтов.",
        "zones": "Зоны", "records": "Записи", "zone": "Зона", "type": "Тип", "serial": "Серийный номер",
        "dnssec": "DNSSEC", "name": "Имя", "ttl": "TTL", "value": "Значение", "values": "Значения",
        "on_ns2": "на ns2", "catalog_yes": "да (каталог)", "catalog_no": "нет",
        "zones_note": "Новая зона — первичная на ns1; ns2 получает её сам (зона каталога). Свои записи — «Записи», целиком — «Текстом».",
        "zones_note_single": "Зоны этого сервера. Вторичный, если он есть, получает их своими средствами Technitium: перенос, зона каталога или кластер.",
        "add_zone": "Добавить зону", "template": "Шаблон", "tpl_none": "без шаблона",
        "tpl_site": "сайт: @ и www на адрес", "tpl_mail": "почта: MX, SPF, DMARC", "ip": "Адрес (для шаблона)",
        "ip_hint": "IPv4 или IPv6 сервера сайта или почты",
        "open_records": "Записи", "as_text": "Текстом", "delegation": "Делегирование", "delete": "Удалить",
        "delete_zone_confirm": "Удалить зону {zone} со всеми записями? Она пропадёт и с ns2.",
        "add_record": "Добавить запись", "delete_record_confirm": "Удалить {name} {type} {value}?",
        "apply_template": "Шаблон", "sign": "Подписать (DNSSEC)", "unsign": "Снять подпись",
        "unsign_confirm": "Снять подпись DNSSEC с {zone}? Сначала уберите DS у регистратора — иначе зона перестанет открываться у проверяющих резолверов.",
        "name_hint": "@ — сама зона; относительное (www) или полное (www.example.com)",
        "value_hint": "A — 203.0.113.10; MX — 10 mail.example.com; TXT — текст; SRV — 10 5 5060 sip.example.com; CAA — 0 issue \"letsencrypt.org\"",
        "zone_added": "зона {zone} добавлена{secondary}", "zone_added_ns2": "; ns2 получит её через каталог",
        "zone_deleted": "зона {zone} удалена",
        "added": "+ {name} {type} {value}", "removed": "- {name} {type} {value}", "nothing": "ничего не изменилось",
        "dry": "(сухой прогон) ",
        "bad_zone": "имя зоны {zone}: латиница, цифры, дефис, точки — example.com",
        "bad_name": "имя {name}: латиница, цифры, дефис, _ и *, метки через точку",
        "bad_type": "тип {type}: умею A, AAAA, CNAME, NS, MX, TXT, SRV, CAA, PTR, ANAME",
        "bad_value": "{type} {value}: {why}",
        "bad_ttl": "TTL {ttl}: число секунд от 30 до 604800",
        "no_zone": "имя {name} не входит ни в одну зону этого сервера",
        "zone_exists": "зона {zone} уже есть", "zone_missing": "зоны {zone} нет",
        "unsupported": "запись {type} здесь не правится — только в консоли Technitium",
        "text_header": "# Зона {zone}: строки «ИМЯ TTL ТИП ЗНАЧЕНИЕ», @ — сама зона.\n# SOA, DNSSEC и записи приложений (APP) — не правятся здесь: только в консоли.\n",
        "line_bad": "строка {line}: {why}",
        "line_format": "нужно «ИМЯ TTL ТИП ЗНАЧЕНИЕ»",
        "cname_mix": "{name}: CNAME не может стоять рядом с другими записями",
        "deleg_title": "Делегирование {zone}",
        "deleg_ns": "Серверы имён зоны (NS): {list}",
        "deleg_registrar": "У регистратора домена укажите эти серверы имён (NS).",
        "deleg_glue": "Серверы имён внутри самой зоны — нужны glue-записи у регистратора: {list}.",
        "deleg_ok": "{who}: делегирована — {list}", "deleg_wrong": "{who}: NS другие — {list}",
        "deleg_none": "{who}: зона не видна (NXDOMAIN или без NS)", "deleg_err": "{who}: не проверить — {err}",
        "deleg_parent_only": "Подзона ({zone}): NS-записи — в родительской зоне {parent}.",
        "ds_title": "DNSSEC {zone}", "ds_unsigned": "Зона не подписана.",
        "ds_signed": "Подписана ({status}). Запись DS для регистратора:",
        "signed": "зона {zone} подписана (ECDSA P-256); DS для регистратора — «DNSSEC»",
        "unsigned": "подпись DNSSEC с {zone} снята",
        "failover_set": "{name}: Failover — {list} (живые адреса, проверка tcp{port})",
        "usage": "dns zone add ЗОНА [template site|mail] [ip АДРЕС] | dns zone rm ЗОНА | dns set ИМЯ ТИП ЗНАЧЕНИЕ[,…] [ttl СЕК] | dns add ИМЯ ТИП ЗНАЧЕНИЕ [ttl СЕК] | dns rm ИМЯ [ТИП [ЗНАЧЕНИЕ]] | dns check ЗОНА | dns sign ЗОНА",
        "check_ok": "проверено",
        "op_zones": "Список зон", "op_records": "Записи зоны", "op_zone_add": "Добавить зону",
        "op_zone_delete": "Удалить зону", "op_record_set": "Задать записи имени: ровно эти значения типа",
        "op_record_add": "Добавить запись", "op_record_delete": "Удалить записи",
        "op_delegation": "Проверить делегирование", "op_dnssec": "DNSSEC и DS",
    },
    "en": {
        "title": "Technitium DNS",
        "desc": "Zones and records of your domains on Technitium (ns1 is primary, ns2 is secondary through a catalog zone), delegation, DNSSEC, records for Traffic Fly and sites.",
        "zones": "Zones", "records": "Records", "zone": "Zone", "type": "Type", "serial": "Serial",
        "dnssec": "DNSSEC", "name": "Name", "ttl": "TTL", "value": "Value", "values": "Values",
        "on_ns2": "on ns2", "catalog_yes": "yes (catalog)", "catalog_no": "no",
        "zones_note": "A new zone is primary on ns1; ns2 gets it by itself (the catalog zone). Records — “Records”, the whole zone — “As text”.",
        "zones_note_single": "This server's zones. A secondary, if any, gets them by Technitium's own means: transfer, a catalog zone or a cluster.",
        "add_zone": "Add zone", "template": "Template", "tpl_none": "no template",
        "tpl_site": "site: @ and www at the address", "tpl_mail": "mail: MX, SPF, DMARC", "ip": "Address (for the template)",
        "ip_hint": "IPv4 or IPv6 of the site or mail server",
        "open_records": "Records", "as_text": "As text", "delegation": "Delegation", "delete": "Delete",
        "delete_zone_confirm": "Delete zone {zone} with all its records? It disappears from ns2 too.",
        "add_record": "Add record", "delete_record_confirm": "Delete {name} {type} {value}?",
        "apply_template": "Template", "sign": "Sign (DNSSEC)", "unsign": "Remove signing",
        "unsign_confirm": "Remove DNSSEC signing from {zone}? Remove the DS at the registrar first — otherwise validating resolvers stop resolving the zone.",
        "name_hint": "@ — the zone itself; relative (www) or full (www.example.com)",
        "value_hint": "A — 203.0.113.10; MX — 10 mail.example.com; TXT — text; SRV — 10 5 5060 sip.example.com; CAA — 0 issue \"letsencrypt.org\"",
        "zone_added": "zone {zone} added{secondary}", "zone_added_ns2": "; ns2 gets it through the catalog",
        "zone_deleted": "zone {zone} deleted",
        "added": "+ {name} {type} {value}", "removed": "- {name} {type} {value}", "nothing": "nothing changed",
        "dry": "(dry run) ",
        "bad_zone": "zone name {zone}: Latin letters, digits, hyphens, dots — example.com",
        "bad_name": "name {name}: Latin letters, digits, hyphens, _ and *, labels separated by dots",
        "bad_type": "type {type}: A, AAAA, CNAME, NS, MX, TXT, SRV, CAA, PTR, ANAME are supported",
        "bad_value": "{type} {value}: {why}",
        "bad_ttl": "TTL {ttl}: seconds from 30 to 604800",
        "no_zone": "name {name} is not in any zone of this server",
        "zone_exists": "zone {zone} already exists", "zone_missing": "no zone {zone}",
        "unsupported": "{type} records aren't edited here — only in the Technitium console",
        "text_header": "# Zone {zone}: lines “NAME TTL TYPE VALUE”, @ is the zone itself.\n# SOA, DNSSEC and app (APP) records aren't edited here: only in the console.\n",
        "line_bad": "line {line}: {why}",
        "line_format": "expected “NAME TTL TYPE VALUE”",
        "cname_mix": "{name}: a CNAME can't stand next to other records",
        "deleg_title": "Delegation of {zone}",
        "deleg_ns": "The zone's name servers (NS): {list}",
        "deleg_registrar": "Set these name servers (NS) at the domain registrar.",
        "deleg_glue": "Name servers inside the zone itself need glue records at the registrar: {list}.",
        "deleg_ok": "{who}: delegated — {list}", "deleg_wrong": "{who}: different NS — {list}",
        "deleg_none": "{who}: the zone isn't visible (NXDOMAIN or no NS)", "deleg_err": "{who}: can't check — {err}",
        "deleg_parent_only": "A subzone ({zone}): the NS records belong in the parent zone {parent}.",
        "ds_title": "DNSSEC of {zone}", "ds_unsigned": "The zone isn't signed.",
        "ds_signed": "Signed ({status}). The DS record for the registrar:",
        "signed": "zone {zone} signed (ECDSA P-256); the DS for the registrar — “DNSSEC”",
        "unsigned": "DNSSEC signing removed from {zone}",
        "failover_set": "{name}: Failover — {list} (live addresses, tcp{port} check)",
        "usage": "dns zone add ZONE [template site|mail] [ip ADDR] | dns zone rm ZONE | dns set NAME TYPE VALUE[,…] [ttl SEC] | dns add NAME TYPE VALUE [ttl SEC] | dns rm NAME [TYPE [VALUE]] | dns check ZONE | dns sign ZONE",
        "check_ok": "checked",
        "op_zones": "List zones", "op_records": "Zone records", "op_zone_add": "Add a zone",
        "op_zone_delete": "Delete a zone", "op_record_set": "Set a name's records: exactly these values of the type",
        "op_record_add": "Add a record", "op_record_delete": "Delete records",
        "op_delegation": "Check delegation", "op_dnssec": "DNSSEC and DS",
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
    print(f"nkt-plugin: {msg}", flush=True)


# --- Technitium API ---------------------------------------------------------

class Technitium:
    def __init__(self):
        self.token = ""
        self.lock = threading.Lock()

    def _raw(self, path, params, body=None):
        q = urllib.parse.urlencode(params)
        if body is None:
            req = urllib.request.Request(f"{API}/{path}", data=q.encode())
        else:
            req = urllib.request.Request(f"{API}/{path}?{q}", data=body.encode(), headers={"Content-Type": "text/plain"})
        with urllib.request.urlopen(req, timeout=60) as r:
            text = r.read().decode()
        try:
            return json.loads(text)
        except ValueError:
            return {"status": "ok", "text": text}

    def login(self):
        # API-токен из консоли Technitium (Administration → Sessions →
        # Create Token) — без пароля администратора; не принят — ошибка,
        # а не повторный вход.
        token = os.environ.get("DNS_API_TOKEN", "").strip()
        if token:
            self.token = token
            return
        res = self._raw("user/login", {"user": "admin", "pass": os.environ.get("DNS_ADMIN_PASSWORD", "")})
        if res.get("status") != "ok":
            raise Fail(f"Technitium: {res.get('errorMessage') or res.get('status')}")
        self.token = res["token"]

    def call(self, path, **params):
        for attempt in range(2):
            if not self.token:
                self.login()
            res = self._raw(path, {**params, "token": self.token})
            if res.get("status") == "invalid-token" and attempt == 0 and not os.environ.get("DNS_API_TOKEN"):
                self.token = ""
                continue
            if res.get("status") != "ok":
                raise Fail(f"Technitium {path}: {res.get('errorMessage') or res.get('status')}")
            return res.get("response", res)
        raise Fail("Technitium: login failed")


TECH = Technitium()
WRITE = threading.Lock()

# --- имена и записи ---------------------------------------------------------

LABEL = r"(?:\*|_?[a-z0-9](?:[a-z0-9_-]{0,61}[a-z0-9])?)"
NAME_RE = re.compile(rf"^{LABEL}(?:\.{LABEL})*$")
ZONE_RE = re.compile(r"^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|xn--[a-z0-9-]{1,59})$")
TYPES = ["A", "AAAA", "CNAME", "NS", "MX", "TXT", "SRV", "CAA", "PTR", "ANAME"]
HOSTNAME_TYPES = {"CNAME": "cname", "NS": "nameServer", "PTR": "ptrName", "ANAME": "aname"}


def norm_zone(c, zone):
    z = str(zone or "").strip().rstrip(".").lower()
    if not ZONE_RE.match(z) or len(z) > 253:
        raise c.fail("bad_zone", zone=zone)
    return z


def all_zones():
    return {z["name"].lower(): z for z in TECH.call("zones/list").get("zones", [])}


def primary_zones():
    return {n: z for n, z in all_zones().items() if z.get("type") == "Primary"}


def zone_of(c, fqdn, zones=None):
    zones = zones if zones is not None else primary_zones()
    best = ""
    for z in zones:
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


def host(c, typ, value):
    v = value.strip().rstrip(".").lower()
    if v == "@":
        return v
    if not NAME_RE.match(v):
        raise c.fail("bad_value", type=typ, value=value, why="host name")
    return v


def parse_value(c, typ, value, zone=None):
    """Значение записи → параметры API Technitium (для add и delete)."""
    value = str(value).strip()
    absolute = lambda h: zone if h == "@" and zone else (h if "." in h or not zone else f"{h}.{zone}")
    try:
        if typ in ("A", "AAAA"):
            ip = ipaddress.ip_address(value)
            if (typ == "A") != (ip.version == 4):
                raise ValueError("IPv4 for A, IPv6 for AAAA")
            return {"ipAddress": str(ip)}
        if typ in HOSTNAME_TYPES:
            return {HOSTNAME_TYPES[typ]: absolute(host(c, typ, value))}
        if typ == "MX":
            pref, exch = value.split(None, 1)
            return {"preference": str(int(pref)), "exchange": absolute(host(c, typ, exch))}
        if typ == "TXT":
            text = value[1:-1] if len(value) >= 2 and value[0] == value[-1] == '"' else value
            if not text or len(text) > 4000:
                raise ValueError("empty or longer than 4000")
            return {"text": text.replace('\\"', '"'), "splitText": "false"}
        if typ == "SRV":
            prio, weight, port, target = value.split()
            return {"priority": str(int(prio)), "weight": str(int(weight)), "port": str(int(port)), "target": absolute(host(c, typ, target))}
        if typ == "CAA":
            flags, tag, val = value.split(None, 2)
            return {"flags": str(int(flags)), "tag": tag, "value": val.strip('"')}
    except Fail:
        raise
    except (ValueError, TypeError) as e:
        raise c.fail("bad_value", type=typ, value=value, why=str(e) or "format")
    raise c.fail("bad_type", type=typ)


def value_text(typ, r, zone):
    """rData Technitium → значение строки (как пишется в тексте зоны)."""
    short = lambda h: rel(h.rstrip(".").lower(), zone) if zone else h
    if typ in ("A", "AAAA"):
        return r.get("ipAddress", "")
    if typ in HOSTNAME_TYPES:
        return short(r.get(HOSTNAME_TYPES[typ], ""))
    if typ == "MX":
        return f"{r.get('preference')} {short(r.get('exchange', ''))}"
    if typ == "TXT":
        return '"' + r.get("text", "").replace('"', '\\"') + '"'
    if typ == "SRV":
        return f"{r.get('priority')} {r.get('weight')} {r.get('port')} {short(r.get('target', ''))}"
    if typ == "CAA":
        return f"{r.get('flags')} {r.get('tag')} \"{r.get('value', '')}\""
    return json.dumps(r, ensure_ascii=False)


def canon(c, typ, value, zone):
    """Значение в одном виде — для сравнения строк."""
    p = parse_value(c, typ, value, zone)
    return json.dumps(p, sort_keys=True)


def check_ttl(c, ttl):
    try:
        n = int(ttl)
    except (TypeError, ValueError):
        raise c.fail("bad_ttl", ttl=ttl)
    if n < 30 or n > 604800:
        raise c.fail("bad_ttl", ttl=ttl)
    return n


def zone_records(zone):
    return TECH.call("zones/records/get", domain=zone, zone=zone, listZone="true").get("records", [])


def records_of(zone, fqdn, typ):
    out = []
    for r in zone_records(zone):
        if r["name"].lower() == fqdn and r["type"] == typ:
            out.append(r)
    return out


# --- действия над записями (общие для операций, команд и _dns_sync) ---------

def apply_changes(c, zone, adds, dels, dry):
    """adds/dels — [(fqdn, type, value, ttl)]; сначала удаления."""
    lines = []
    for fqdn, typ, value, _ in dels:
        lines.append(c.t("removed", name=fqdn, type=typ, value=value))
        if not dry:
            TECH.call("zones/records/delete", zone=zone, domain=fqdn, type=typ, **parse_value(c, typ, value, zone))
    for fqdn, typ, value, ttl in adds:
        lines.append(c.t("added", name=fqdn, type=typ, value=value))
        if not dry:
            TECH.call("zones/records/add", zone=zone, domain=fqdn, type=typ, ttl=str(ttl), **parse_value(c, typ, value, zone))
    return lines


def record_set(c, name, typ, values, ttl, dry, zones=None):
    """Ровно эти значения типа у имени: недостающие — добавить, лишние — убрать."""
    typ = typ.upper()
    if typ not in TYPES:
        raise c.fail("bad_type", type=typ)
    zone = zone_of(c, to_fqdn(c, name), zones)
    fqdn = to_fqdn(c, name, zone)
    ttl = check_ttl(c, ttl)
    want = {canon(c, typ, v, zone): v for v in values if str(v).strip()}
    have = {canon(c, typ, value_text(typ, r["rData"], zone), zone): r for r in records_of(zone, fqdn, typ)}
    adds = [(fqdn, typ, v, ttl) for k, v in want.items() if k not in have]
    dels = [(fqdn, typ, value_text(typ, r["rData"], zone), r["ttl"]) for k, r in have.items() if k not in want]
    return apply_changes(c, zone, adds, dels, dry)


def record_add(c, name, typ, value, ttl, dry):
    typ = typ.upper()
    if typ not in TYPES:
        raise c.fail("bad_type", type=typ)
    zone = zone_of(c, to_fqdn(c, name))
    fqdn = to_fqdn(c, name, zone)
    ttl = check_ttl(c, ttl)
    k = canon(c, typ, value, zone)
    if any(canon(c, typ, value_text(typ, r["rData"], zone), zone) == k for r in records_of(zone, fqdn, typ)):
        return []
    return apply_changes(c, zone, [(fqdn, typ, value, ttl)], [], dry)


def record_delete(c, name, typ, value, dry):
    zone = zone_of(c, to_fqdn(c, name))
    fqdn = to_fqdn(c, name, zone)
    typ = (typ or "").upper()
    dels = []
    for r in zone_records(zone):
        if r["name"].lower() != fqdn or r["type"] not in TYPES or (typ and r["type"] != typ):
            continue
        v = value_text(r["type"], r["rData"], zone)
        if value and canon(c, r["type"], v, zone) != canon(c, r["type"], value, zone):
            continue
        dels.append((fqdn, r["type"], v, r["ttl"]))
    return apply_changes(c, zone, [], dels, dry)


# --- зоны -------------------------------------------------------------------

def has_catalog():
    return CATALOG in all_zones()


def zone_add(c, zone, template, ip, dry):
    zone = norm_zone(c, zone)
    if zone in all_zones():
        raise c.fail("zone_exists", zone=zone)
    catalog = has_catalog()
    lines = [c.t("zone_added", zone=zone, secondary=c.t("zone_added_ns2") if catalog else "")]
    if not dry:
        params = {"zone": zone, "type": "Primary"}
        if catalog:
            params["catalog"] = CATALOG
        TECH.call("zones/create", **params)
        if NS2:
            TECH.call("zones/records/add", zone=zone, domain=zone, type="NS", nameServer=NS2, ttl="3600")
    if NS2:
        lines.append(c.t("added", name=zone, type="NS", value=NS2))
    if template and template != "none":
        lines += template_records(c, zone, template, ip, dry, fresh=True)
    return lines


def template_records(c, zone, template, ip, dry, fresh=False):
    if template not in ("site", "mail"):
        raise c.fail("bad_value", type="template", value=template, why="site | mail")
    adds = []
    if template == "site":
        typ = "AAAA" if ":" in str(ip or "") else "A"
        parse_value(c, typ, ip or "", zone)
        adds = [(zone, typ, ip, 3600), (f"www.{zone}", "CNAME", zone + ".", 3600)]
    else:
        adds = [(zone, "MX", f"10 mail.{zone}.", 3600), (zone, "TXT", '"v=spf1 mx -all"', 3600),
                (f"_dmarc.{zone}", "TXT", f'"v=DMARC1; p=quarantine; rua=mailto:postmaster@{zone}"', 3600)]
        if ip:
            typ = "AAAA" if ":" in ip else "A"
            parse_value(c, typ, ip, zone)
            adds.append((f"mail.{zone}", typ, ip, 3600))
    if dry and fresh:
        return [c.t("added", name=a[0], type=a[1], value=a[2]) for a in adds]
    out = []
    for fqdn, typ, value, ttl in adds:
        out += record_add(c, fqdn, typ, value, ttl, dry)
    return out


def zone_delete(c, zone, dry):
    zone = norm_zone(c, zone)
    if zone not in primary_zones():
        raise c.fail("zone_missing", zone=zone)
    if not dry:
        TECH.call("zones/delete", zone=zone)
    return [c.t("zone_deleted", zone=zone)]


# --- текст зоны -------------------------------------------------------------

def zone_text(c, zone):
    zone = norm_zone(c, zone)
    lines = [c.t("text_header", zone=zone).rstrip("\n")]
    rows = []
    for r in zone_records(zone):
        typ = r["type"]
        if typ not in TYPES:
            continue
        rows.append((rel(r["name"].lower(), zone), r["ttl"], typ, value_text(typ, r["rData"], zone)))
    rows.sort(key=lambda x: (x[0] != "@", x[0], TYPES.index(x[2]), x[3]))
    width = max([len(x[0]) for x in rows] + [1])
    for name, ttl, typ, value in rows:
        lines.append(f"{name.ljust(width)}  {str(ttl).ljust(6)} {typ.ljust(5)} {value}")
    return "\n".join(lines) + "\n"


def parse_text(c, zone, text):
    """Строки «ИМЯ TTL ТИП ЗНАЧЕНИЕ» → множество записей."""
    out = {}
    for i, line in enumerate(str(text).splitlines(), 1):
        s = line.strip()
        if not s or s.startswith("#") or s.startswith(";"):
            continue
        parts = s.split(None, 3)
        if len(parts) < 4:
            raise c.fail("line_bad", line=i, why=c.t("line_format"))
        name, ttl, typ, value = parts
        try:
            fqdn = to_fqdn(c, name, zone)
            typ = typ.upper()
            if typ not in TYPES:
                raise c.fail("bad_type", type=typ)
            ttl = check_ttl(c, ttl)
            key = (fqdn, typ, canon(c, typ, value, zone))
        except Fail as e:
            raise c.fail("line_bad", line=i, why=str(e))
        out[key] = (fqdn, typ, value.strip(), ttl)
    names = {}
    for fqdn, typ, _, _ in out.values():
        names.setdefault(fqdn, set()).add(typ)
    for fqdn, types in names.items():
        if "CNAME" in types and len(types) > 1:
            raise c.fail("cname_mix", name=fqdn)
    return out


def zone_apply(c, zone, text, dry):
    zone = norm_zone(c, zone)
    want = parse_text(c, zone, text)
    have = {}
    for r in zone_records(zone):
        if r["type"] not in TYPES:
            continue
        v = value_text(r["type"], r["rData"], zone)
        have[(r["name"].lower(), r["type"], canon(c, r["type"], v, zone))] = (r["name"].lower(), r["type"], v, r["ttl"])
    dels = [v for k, v in have.items() if k not in want or want[k][3] != v[3]]
    adds = [v for k, v in want.items() if k not in have or have[k][3] != v[3]]
    return apply_changes(c, zone, adds, dels, dry)


# --- делегирование и DNSSEC -------------------------------------------------

def doh_ns(url, zone):
    q = urllib.parse.urlencode({"name": zone, "type": "NS"})
    req = urllib.request.Request(f"{url}?{q}", headers={"Accept": "application/dns-json"})
    with urllib.request.urlopen(req, timeout=10) as r:
        res = json.load(r)
    if res.get("Status") == 3:
        return None
    return sorted({a["data"].rstrip(".").lower() for a in res.get("Answer", []) if a.get("type") == 2})


def delegation(c, zone):
    zone = norm_zone(c, zone)
    zones = primary_zones()
    if zone not in zones:
        raise c.fail("zone_missing", zone=zone)
    ns = sorted({value_text("NS", r["rData"], None).rstrip(".").lower() for r in records_of(zone, zone, "NS")})
    lines = [c.t("deleg_ns", list=", ".join(ns) or "—")]
    parent = next((z for z in zones if zone.endswith("." + z)), "")
    if parent:
        lines.append(c.t("deleg_parent_only", zone=zone, parent=parent))
    else:
        lines.append(c.t("deleg_registrar"))
    glue = []
    for n in ns:
        if n == zone or n.endswith("." + zone):
            addrs = [r["rData"]["ipAddress"] for t in ("A", "AAAA") for r in records_of(zone, n, t)]
            glue.append(f"{n} → {', '.join(addrs) or '?'}")
    if glue:
        lines.append(c.t("deleg_glue", list="; ".join(glue)))
    lines.append("")
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


def dnssec_text(c, zone):
    zone = norm_zone(c, zone)
    info = TECH.call("zones/dnssec/viewDS", zone=zone)
    status = info.get("dnssecStatus", "Unsigned")
    if status == "Unsigned":
        return c.t("ds_unsigned"), False
    lines = [c.t("ds_signed", status=status)]
    for ds in info.get("dsRecords", []):
        for d in ds.get("digests", []):
            lines.append(f"{zone}. IN DS {ds['keyTag']} {ds['algorithmNumber']} {d['digestTypeNumber']} {d['digest']}  ; {ds.get('dnsKeyState', '')}")
    return "\n".join(lines), True


def dnssec_sign(c, zone, dry):
    zone = norm_zone(c, zone)
    if not dry:
        TECH.call("zones/dnssec/sign", zone=zone, algorithm="ECDSA", curve="P256", dnsKeyTtl="3600",
                  zskRolloverDays="30", nxProof="NSEC", nsec3Iterations="0", nsec3SaltLength="0")
    return [c.t("signed", zone=zone)]


def dnssec_unsign(c, zone, dry):
    zone = norm_zone(c, zone)
    if not dry:
        TECH.call("zones/dnssec/unsign", zone=zone)
    return [c.t("unsigned", zone=zone)]


# --- Failover и записи хаба (_dns_sync) --------------------------------------

def failover_records(zone, fqdn):
    return [r for r in records_of(zone, fqdn, "APP") if r["rData"].get("appName") == "Failover"]


def dns_sync(c, args, dry):
    """Записи владельца (маршрут Traffic Fly, сайт): привести к records,
    убрав из previous то, чего в records нет. Чужие значения у того же
    имени не трогаются."""
    zones = primary_zones()
    new = args.get("records") or []
    prev = args.get("previous") or []
    key = lambda r: (to_fqdn(c, r["name"]), str(r.get("type", "A")).upper())
    lines = []
    want = {key(r): r for r in new}
    for r in prev:
        k = key(r)
        try:
            zone = zone_of(c, k[0], zones)
        except Fail:
            continue  # зону удалили — убирать нечего
        if r.get("failover") and (k not in want or not want[k].get("failover")):
            for f in failover_records(zone, k[0]):
                lines.append(c.t("removed", name=k[0], type="APP", value="Failover"))
                if not dry:
                    TECH.call("zones/records/delete", zone=zone, domain=k[0], type="APP", appName="Failover", classPath=f["rData"].get("classPath", "Failover.Address"))
            continue
        keep = {canon(c, k[1], v, zone) for v in (want[k].get("values") or [])} if k in want and not want[k].get("failover") else set()
        dels = [(k[0], k[1], v, 0) for v in r.get("values") or [] if canon(c, k[1], v, zone) not in keep
                and any(canon(c, k[1], value_text(k[1], x["rData"], zone), zone) == canon(c, k[1], v, zone) for x in records_of(zone, k[0], k[1]))]
        lines += apply_changes(c, zone, [], dels, dry)
    for k, r in want.items():
        zone = zone_of(c, k[0], zones)
        values = [str(v) for v in r.get("values") or []]
        ttl = check_ttl(c, r.get("ttl") or DEFAULT_TTL)
        if r.get("failover") and len(values) > 1:
            port = int(r.get("port") or 443)
            data = json.dumps({"primary": values, "secondary": [], "serverDown": [], "healthCheck": f"tcp{port}", "allowTxtStatus": True})
            lines.append(c.t("failover_set", name=k[0], list=", ".join(values), port=port))
            if not dry:
                for f in failover_records(zone, k[0]):
                    TECH.call("zones/records/delete", zone=zone, domain=k[0], type="APP", appName="Failover", classPath=f["rData"].get("classPath", "Failover.Address"))
                TECH.call("zones/records/add", zone=zone, domain=k[0], type="APP", ttl=str(ttl), appName="Failover",
                          classPath="Failover.Address", recordData=data)
            continue
        have = {canon(c, k[1], value_text(k[1], x["rData"], zone), zone) for x in records_of(zone, k[0], k[1])}
        adds = [(k[0], k[1], v, ttl) for v in values if canon(c, k[1], v, zone) not in have]
        lines += apply_changes(c, zone, adds, [], dry)
    return lines


# --- виды разделов ----------------------------------------------------------

def zones_view(c):
    zones = all_zones()
    catalog = CATALOG in zones
    rows = []
    for name, z in sorted(zones.items()):
        if z.get("type") != "Primary":
            continue
        args = {"zone": name}
        actions = [
            {"title": c.t("open_records"), "open": "records", "args": args},
            {"title": c.t("as_text"), "args": args, "edit": {"get": "zone_text", "put": "zone_apply", "doc": name}},
            {"title": c.t("delegation"), "open": "delegation", "args": args},
            {"title": c.t("dnssec"), "open": "dnssec", "args": args},
            {"title": c.t("apply_template"), "op": "template_apply", "args": args, "fields": [
                {"name": "template", "title": c.t("template"), "type": "select", "required": True,
                 "options": [{"value": "site", "title": c.t("tpl_site")}, {"value": "mail", "title": c.t("tpl_mail")}]},
                {"name": "ip", "title": c.t("ip"), "hint": c.t("ip_hint")}]},
            {"title": c.t("delete"), "op": "zone_delete", "args": args, "danger": True, "confirm": c.t("delete_zone_confirm", zone=name)},
        ]
        rows.append({"cells": {"zone": name, "serial": z.get("soaSerial"), "dnssec": z.get("dnssecStatus", ""),
                               "ns2": c.t("catalog_yes") if z.get("catalog") == CATALOG else c.t("catalog_no")},
                     "actions": actions})
    columns = [{"key": "zone", "title": c.t("zone")}, {"key": "serial", "title": c.t("serial")}, {"key": "dnssec", "title": c.t("dnssec")}]
    if catalog:
        columns.append({"key": "ns2", "title": c.t("on_ns2")})
    add = {"title": c.t("add_zone"), "op": "zone_add", "fields": [
        {"name": "zone", "title": c.t("zone"), "required": True, "placeholder": "example.com"},
        {"name": "template", "title": c.t("template"), "type": "select", "default": "none",
         "options": [{"value": "none", "title": c.t("tpl_none")}, {"value": "site", "title": c.t("tpl_site")}, {"value": "mail", "title": c.t("tpl_mail")}]},
        {"name": "ip", "title": c.t("ip"), "hint": c.t("ip_hint")}]}
    return {"type": "table", "title": c.t("zones"), "note": c.t("zones_note") if catalog else c.t("zones_note_single"),
            "columns": columns, "rows": rows, "actions": [add]}


def records_view(c, zone):
    zone = norm_zone(c, zone)
    rows = []
    for r in zone_records(zone):
        typ = r["type"]
        if typ not in TYPES:
            continue
        name = r["name"].lower()
        v = value_text(typ, r["rData"], zone)
        # NS вершины зоны — делегирование: удалять отсюда нельзя.
        actions = [] if typ == "NS" and name == zone else [
            {"title": c.t("delete"), "op": "record_delete", "danger": True, "args": {"name": name, "type": typ, "value": v},
             "confirm": c.t("delete_record_confirm", name=name, type=typ, value=v)}]
        rows.append({"cells": {"name": rel(name, zone), "ttl": r["ttl"], "type": typ, "value": v}, "actions": actions})
    rows.sort(key=lambda x: (x["cells"]["name"] != "@", x["cells"]["name"], TYPES.index(x["cells"]["type"])))
    add = {"title": c.t("add_record"), "op": "record_add", "args": {"zone": zone}, "fields": [
        {"name": "name", "title": c.t("name"), "required": True, "default": "@", "hint": c.t("name_hint")},
        {"name": "type", "title": c.t("type"), "type": "select", "required": True, "default": "A", "options": [{"value": t} for t in TYPES]},
        {"name": "value", "title": c.t("value"), "required": True, "hint": c.t("value_hint")},
        {"name": "ttl", "title": c.t("ttl"), "type": "number", "default": 3600}]}
    edit = {"title": c.t("as_text"), "args": {"zone": zone}, "edit": {"get": "zone_text", "put": "zone_apply", "doc": zone}}
    return {"type": "table", "title": f"{c.t('records')} — {zone}", "columns": [
        {"key": "name", "title": c.t("name")}, {"key": "ttl", "title": c.t("ttl")},
        {"key": "type", "title": c.t("type")}, {"key": "value", "title": c.t("value")}], "rows": rows, "actions": [add, edit]}


def dnssec_view(c, zone):
    text, signed = dnssec_text(c, zone)
    action = ({"title": c.t("unsign"), "op": "dnssec_unsign", "args": {"zone": zone}, "danger": True, "confirm": c.t("unsign_confirm", zone=zone)}
              if signed else {"title": c.t("sign"), "op": "dnssec_sign", "args": {"zone": zone}})
    return {"type": "text", "title": c.t("ds_title", zone=zone), "text": text, "actions": [action]}


# --- команда сценариев -------------------------------------------------------

def split_ttl(argv):
    if len(argv) >= 2 and argv[-2] == "ttl":
        return argv[:-2], argv[-1]
    return argv, None


def command(c, argv, dry, check):
    """dns … из сценария: check — только разобрать."""
    if not argv or argv[0] != "dns" or len(argv) < 3:
        raise Fail(c.t("usage"))
    sub = argv[1]
    rest = argv[2:]
    if sub == "zone" and len(rest) >= 2 and rest[0] in ("add", "rm"):
        zone = norm_zone(c, rest[1])
        opts = dict(zip(rest[2::2], rest[3::2]))
        if rest[0] == "add":
            if len(rest[2:]) % 2 or set(opts) - {"template", "ip"}:
                raise Fail(c.t("usage"))
            if check:
                return []
            return zone_add(c, zone, opts.get("template"), opts.get("ip"), dry)
        if len(rest) != 2:
            raise Fail(c.t("usage"))
        return [] if check else zone_delete(c, zone, dry)
    if sub in ("check", "sign") and len(rest) == 1:
        zone = norm_zone(c, rest[0])
        if check:
            return []
        return delegation(c, zone).splitlines() if sub == "check" else dnssec_sign(c, zone, dry)
    if sub in ("set", "add"):
        body, ttl = split_ttl(rest)
        if len(body) < 3:
            raise Fail(c.t("usage"))
        name, typ = body[0], body[1].upper()
        if typ not in TYPES:
            raise c.fail("bad_type", type=typ)
        value = " ".join(body[2:])
        ttl = check_ttl(c, ttl or DEFAULT_TTL)
        to_fqdn(c, name)
        values = [v.strip() for v in value.split(",")] if sub == "set" and typ in ("A", "AAAA", "NS") else [value]
        if check:
            for v in values:
                parse_value(c, typ, v)
            return []
        return record_set(c, name, typ, values, ttl, dry) if sub == "set" else record_add(c, name, typ, value, ttl, dry)
    if sub == "rm" and 1 <= len(rest):
        name = rest[0]
        typ = rest[1].upper() if len(rest) > 1 else ""
        value = " ".join(rest[2:])
        if typ and typ not in TYPES:
            raise c.fail("bad_type", type=typ)
        to_fqdn(c, name)
        return [] if check else record_delete(c, name, typ, value, dry)
    raise Fail(c.t("usage"))


# --- манифест и вызовы ------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}


def manifest(c):
    zone = {"zone": {"type": "string", "description": "zone, e.g. example.com"}}
    return {
        "protocol": 1, "name": "dns", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "zones", "title": c.t("op_zones"), "read_only": True, "mcp": True, "description": "List DNS zones on the Technitium server (serial, DNSSEC, on ns2)."},
            {"name": "records", "title": c.t("op_records"), "read_only": True, "mcp": True, "description": "Records of a zone.", "input": schema(zone, ["zone"])},
            {"name": "zone_text", "read_only": True, "mcp": True, "description": "A zone as text lines “NAME TTL TYPE VALUE” (for zone_apply).", "input": schema(zone, ["zone"])},
            {"name": "delegation", "title": c.t("op_delegation"), "read_only": True, "mcp": True, "description": "Delegation check: the zone's NS, glue for the registrar, what public resolvers see.", "input": schema(zone, ["zone"])},
            {"name": "dnssec", "title": c.t("op_dnssec"), "read_only": True, "mcp": True, "description": "DNSSEC status and DS records for the registrar.", "input": schema(zone, ["zone"])},
            {"name": "zone_add", "title": c.t("op_zone_add"), "mcp": True, "description": "Create a primary zone (ns2 gets it through the catalog). template: site | mail, ip — for the template.",
             "input": schema({**zone, "template": {"type": "string", "enum": ["none", "site", "mail"]}, "ip": S}, ["zone"])},
            {"name": "zone_delete", "title": c.t("op_zone_delete"), "mcp": True, "destructive": True, "description": "Delete a zone with all its records.", "input": schema(zone, ["zone"])},
            {"name": "zone_apply", "mcp": True, "destructive": True, "description": "Replace a zone's editable records with text lines “NAME TTL TYPE VALUE” (from zone_text); dry_run shows the changes.",
             "input": schema({**zone, "text": S, "dry_run": {"type": "boolean"}}, ["zone", "text"])},
            {"name": "template_apply", "mcp": True, "description": "Add template records to a zone: site (@ and www at ip) or mail (MX, SPF, DMARC, mail at ip).",
             "input": schema({**zone, "template": {"type": "string", "enum": ["site", "mail"]}, "ip": S}, ["zone", "template"])},
            {"name": "record_set", "title": c.t("op_record_set"), "mcp": True, "description": "Set exactly these values of a type for a name (adds missing, removes others of that type).",
             "input": schema({"name": S, "type": {"type": "string", "enum": TYPES}, "values": {"type": "array", "items": S}, "ttl": {"type": "integer"}}, ["name", "type", "values"])},
            {"name": "record_add", "title": c.t("op_record_add"), "mcp": True, "description": "Add one record.",
             "input": schema({"name": S, "type": {"type": "string", "enum": TYPES}, "value": S, "ttl": {"type": "integer"}, "zone": S}, ["name", "type", "value"])},
            {"name": "record_delete", "title": c.t("op_record_delete"), "mcp": True, "destructive": True, "description": "Delete a name's records (all, of a type, or one value).",
             "input": schema({"name": S, "type": S, "value": S}, ["name"])},
            {"name": "dnssec_sign", "mcp": True, "description": "Sign a zone with DNSSEC (ECDSA P-256).", "input": schema(zone, ["zone"])},
            {"name": "dnssec_unsign", "mcp": True, "destructive": True, "description": "Remove DNSSEC signing (remove the DS at the registrar first).", "input": schema(zone, ["zone"])},
        ],
        "pages": [
            {"id": "zones", "title": c.t("zones"), "op": "zones"},
            # Открываются из строки зоны (с её именем).
            {"id": "records", "title": c.t("records"), "op": "records", "hidden": True},
            {"id": "delegation", "title": c.t("delegation"), "op": "delegation", "hidden": True},
            {"id": "dnssec", "title": c.t("dnssec"), "op": "dnssec", "hidden": True},
        ],
        "commands": [{"word": "dns", "usage": c.t("usage"), "example": "dns zone add example.com template site ip 203.0.113.10\ndns set app.example.com A 203.0.113.10 ttl 300"}],
        "provides": ["dns"],
    }


def first_zone():
    zones = sorted(primary_zones())
    return zones[0] if zones else ""


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or bool(args.get("dry_run"))
    user = req.get("user", "")
    writes = op not in ("zones", "records", "zone_text", "delegation", "dnssec", "_check", "_dns_zones", "_connect")
    if op == "_connect":
        log(f"подключён к хабу как {args.get('name')} ({user})")
        return {"ok": True}
    if op == "zones":
        return {"ok": True, "view": zones_view(c)}
    if op == "records":
        zone = args.get("zone") or first_zone()
        return {"ok": True, "view": records_view(c, zone) if zone else zones_view(c)}
    if op == "zone_text":
        return {"ok": True, "result": {"text": zone_text(c, args.get("zone"))}}
    if op == "delegation":
        zone = args.get("zone") or first_zone()
        return {"ok": True, "view": {"type": "text", "title": c.t("deleg_title", zone=zone), "text": delegation(c, zone)}}
    if op == "dnssec":
        zone = norm_zone(c, args.get("zone") or first_zone())
        return {"ok": True, "view": dnssec_view(c, zone)}
    if op == "_dns_zones":
        return {"ok": True, "result": {"zones": sorted(primary_zones())}}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    with WRITE:
        if op == "_command":
            lines = command(c, [str(a) for a in args.get("argv") or []], dry, False)
        elif op == "_dns_sync":
            lines = dns_sync(c, args, dry)
        elif op == "zone_add":
            lines = zone_add(c, args.get("zone"), args.get("template"), args.get("ip"), dry)
        elif op == "zone_delete":
            lines = zone_delete(c, args.get("zone"), dry)
        elif op == "zone_apply":
            lines = zone_apply(c, args.get("zone"), args.get("text", ""), dry)
        elif op == "template_apply":
            lines = template_records(c, norm_zone(c, args.get("zone")), args.get("template"), args.get("ip"), dry)
        elif op == "record_set":
            values = args.get("values")
            if isinstance(values, str):
                values = [v.strip() for v in values.split(",")]
            lines = record_set(c, args.get("name"), str(args.get("type", "")), values or [], args.get("ttl") or DEFAULT_TTL, dry)
        elif op == "record_add":
            name = args.get("name")
            if args.get("zone"):
                name = to_fqdn(c, name, norm_zone(c, args.get("zone")))
            lines = record_add(c, name, str(args.get("type", "")), str(args.get("value", "")), args.get("ttl") or 3600, dry)
        elif op == "record_delete":
            lines = record_delete(c, args.get("name"), args.get("type"), args.get("value"), dry)
        elif op == "dnssec_sign":
            lines = dnssec_sign(c, args.get("zone"), dry)
        elif op == "dnssec_unsign":
            lines = dnssec_unsign(c, args.get("zone"), dry)
        else:
            return {"ok": False, "error": f"no op {op}"}
    text = "\n".join(lines) or c.t("nothing")
    if dry:
        text = "\n".join(c.t("dry") + line for line in text.splitlines())
    elif writes:
        log(f"{user}: {op} {json.dumps(args, ensure_ascii=False)[:300]} → {len(lines)}")
    return {"ok": True, "text": text}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-technitium/" + VERSION

    def log_message(self, fmt, *a):  # тихо: без строки на каждый запрос
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
            req = json.loads(body or b"{}")
            self.reply(handle(c, req))
        except Fail as e:
            self.reply({"ok": False, "error": str(e)})
        except urllib.error.URLError as e:
            self.reply({"ok": False, "error": f"Technitium {API}: {e}"})
        except Exception as e:  # ошибка плагина — текстом хабу, а не обрывом
            log(f"ошибка: {e!r}")
            self.reply({"ok": False, "error": f"plugin error: {e}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, Technitium {API}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
