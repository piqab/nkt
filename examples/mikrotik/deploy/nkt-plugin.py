"""Плагин хаба nkt для MikroTik RouterOS 7 (протокол плагинов nkt v1,
provides hosts) — эталон плагина «устройства как хосты».

Роутеры добавляются в хабе: «Хосты» → «Добавить» → тип «MikroTik
RouterOS» → адрес, логин, пароль. Учётки хранит хаб (зашифрованными) и
передаёт их в каждом вызове (args.conn) — плагин их не хранит и не пишет
в журнал. Связь с роутером — REST API RouterOS 7 по HTTPS (служба
www-ssl); сертификат роутера обычно свой — отпечаток запоминается при
добавлении и сверяется при каждом вызове.

Что умеет: состав (ресурсы, интерфейсы и адреса, маршруты, firewall,
DHCP, службы, пакеты, пользователи, соседи MNDP/LLDP — связи на карте),
метрики, находки (обновления, открытые наружу службы, admin, нет запрета
входящих), действия (перезагрузка, обновление, резервная копия,
прошивка), конфигурация «/export» (хаб хранит версии и показывает дифф),
команды RouterOS в сценариях («on роутер run …»).
Без зависимостей: только стандартная библиотека Python.
"""
import base64
import hashlib
import hmac
import http.client
import json
import os
import re
import socket
import ssl
import sys
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9220") or 9220)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
TIMEOUT = float(os.environ.get("MIKROTIK_TIMEOUT", "20") or 20)

T = {
    "ru": {
        "title": "MikroTik",
        "desc": "Роутеры и коммутаторы MikroTik (RouterOS 7) как хосты nkt: состав, метрики, находки, конфигурация с диффом, действия, команды RouterOS.",
        "type": "MikroTik RouterOS",
        "type_hint": "RouterOS 7 с включённым REST API: /ip service enable www-ssl (порт 443) и пользователь с правами read (смотреть) или full (действия и команды).",
        "address": "Адрес", "address_hint": "IP или имя роутера; порт — через двоеточие, если не 443",
        "user": "Пользователь", "user_hint": "отдельный пользователь для nkt, не admin",
        "password": "Пароль",
        "tls": "Сертификат роутера", "tls_pin": "свой (запомнить отпечаток)", "tls_verify": "проверять по удостоверяющим центрам", "tls_http": "без шифрования (http, порт 80) — не рекомендуется",
        "tls_hint": "у роутера обычно свой сертификат: отпечаток запоминается при «Проверить» и сверяется при каждом подключении",
        "fingerprint": "Отпечаток сертификата (SHA-256)", "fingerprint_hint": "пусто — запомнится при «Проверить»",
        "reboot": "Перезагрузить", "reboot_confirm": "Перезагрузить роутер? Связь пропадёт на минуту-две.",
        "update": "Установить обновление RouterOS", "update_confirm": "Скачать и установить обновление RouterOS? Роутер перезагрузится сам.",
        "update_channel": "Канал", "firmware": "Обновить прошивку RouterBOARD", "firmware_confirm": "Обновить прошивку загрузчика? Она применится после следующей перезагрузки.",
        "backup": "Резервная копия на роутере", "backup_name": "Имя файла", "backup_hint": "файл .backup в памяти роутера (Files)",
        "auth_failed": "{addr}: неверный логин или пароль (или у пользователя нет прав на REST)",
        "unreachable": "{addr}: нет связи — {err}. Включён ли REST API (/ip service enable www-ssl) и открыт ли порт?",
        "cert_changed": "{addr}: сертификат роутера изменился (был {old}, сейчас {new}) — если это вы его сменили, очистите отпечаток в учётке хоста и нажмите «Проверить»",
        "not_routeros7": "{addr}: это не RouterOS 7 с REST API ({err})",
        "http_error": "{addr}: {method} {path} — HTTP {code}: {msg}",
        "f_update": "Доступно обновление RouterOS {latest} (стоит {installed})",
        "f_update_fix": "действие «Установить обновление RouterOS» или /system package update install",
        "f_firmware": "Прошивка RouterBOARD {current} старше доступной {upgrade}",
        "f_firmware_fix": "действие «Обновить прошивку RouterBOARD», затем перезагрузка",
        "f_service": "Служба {name} (порт {port}) открыта для всех адресов",
        "f_service_fix": "/ip service set {name} disabled=yes — или address=<ваши сети>",
        "f_telnet": "Включён telnet — пароли идут по сети открытым текстом",
        "f_admin": "Есть пользователь «admin» — имя по умолчанию, его подбирают первым",
        "f_admin_fix": "заведите своего пользователя группы full и отключите или переименуйте admin",
        "f_no_drop": "В firewall нет запрета входящих (chain=input action=drop) — роутер открыт для всего, что пришло снаружи",
        "f_no_drop_fix": "добавьте правила input: accept established,related; accept из своих сетей; последним — drop",
        "f_http": "Хаб ходит на роутер без шифрования (http) — пароль идёт по сети открытым текстом",
        "f_http_fix": "включите www-ssl и смените «Сертификат роутера» в учётке хоста",
        "f_cpu": "Загрузка процессора {pct}%",
        "routes": "Маршруты", "firewall": "Firewall (filter)", "leases": "DHCP — выданные адреса", "dst": "Куда", "gateway": "Шлюз", "distance": "Метрика", "active": "Активен",
        "chain": "Цепочка", "action": "Действие", "src": "Откуда", "proto": "Протокол", "port": "Порт", "comment": "Комментарий",
        "lease_addr": "Адрес", "mac": "MAC", "host": "Имя", "status": "Состояние",
        "exec_empty": "команда пуста", "exec_long": "команда длиннее 8000 знаков", "would": "(сухой прогон) {what}",
        "would_exec": "выполнить на {addr}: {cmd}", "rebooting": "{addr}: перезагрузка запущена",
        "updating": "{addr}: обновление {latest} скачивается и установится с перезагрузкой", "no_update": "{addr}: обновлений нет ({installed})",
        "fw_done": "{addr}: прошивка обновится при следующей перезагрузке", "backup_done": "{addr}: резервная копия {name}.backup сохранена",
        "no_action": "нет действия {action}", "bad_backup": "имя резервной копии: латиница, цифры, - и _",
        "check_ok": "проверено",
    },
    "en": {
        "title": "MikroTik",
        "desc": "MikroTik routers and switches (RouterOS 7) as nkt hosts: inventory, metrics, findings, configuration with a diff, actions, RouterOS commands.",
        "type": "MikroTik RouterOS",
        "type_hint": "RouterOS 7 with the REST API on: /ip service enable www-ssl (port 443) and a user with read (view) or full (actions and commands) rights.",
        "address": "Address", "address_hint": "the router's IP or name; a port after a colon if not 443",
        "user": "User", "user_hint": "a separate user for nkt, not admin",
        "password": "Password",
        "tls": "Router certificate", "tls_pin": "its own (remember the fingerprint)", "tls_verify": "verify against certificate authorities", "tls_http": "no encryption (http, port 80) — not recommended",
        "tls_hint": "a router usually has its own certificate: the fingerprint is remembered on Check and compared on every connection",
        "fingerprint": "Certificate fingerprint (SHA-256)", "fingerprint_hint": "empty — remembered on Check",
        "reboot": "Reboot", "reboot_confirm": "Reboot the router? The link drops for a minute or two.",
        "update": "Install the RouterOS update", "update_confirm": "Download and install the RouterOS update? The router reboots by itself.",
        "update_channel": "Channel", "firmware": "Upgrade the RouterBOARD firmware", "firmware_confirm": "Upgrade the boot firmware? It applies after the next reboot.",
        "backup": "Backup on the router", "backup_name": "File name", "backup_hint": "a .backup file in the router's memory (Files)",
        "auth_failed": "{addr}: wrong login or password (or the user has no REST rights)",
        "unreachable": "{addr}: unreachable — {err}. Is the REST API on (/ip service enable www-ssl) and the port open?",
        "cert_changed": "{addr}: the router certificate changed (was {old}, now {new}) — if you changed it, clear the fingerprint in the host's login and press Check",
        "not_routeros7": "{addr}: not RouterOS 7 with the REST API ({err})",
        "http_error": "{addr}: {method} {path} — HTTP {code}: {msg}",
        "f_update": "RouterOS update {latest} is available ({installed} installed)",
        "f_update_fix": "the “Install the RouterOS update” action or /system package update install",
        "f_firmware": "RouterBOARD firmware {current} is older than the available {upgrade}",
        "f_firmware_fix": "the “Upgrade the RouterBOARD firmware” action, then a reboot",
        "f_service": "Service {name} (port {port}) is open to all addresses",
        "f_service_fix": "/ip service set {name} disabled=yes — or address=<your networks>",
        "f_telnet": "telnet is on — passwords travel in clear text",
        "f_admin": "There is an “admin” user — the default name is guessed first",
        "f_admin_fix": "create your own user in the full group and disable or rename admin",
        "f_no_drop": "The firewall has no input drop (chain=input action=drop) — the router is open to anything from outside",
        "f_no_drop_fix": "add input rules: accept established,related; accept from your networks; drop last",
        "f_http": "The hub reaches the router without encryption (http) — the password travels in clear text",
        "f_http_fix": "turn on www-ssl and change “Router certificate” in the host's login",
        "f_cpu": "CPU load {pct}%",
        "routes": "Routes", "firewall": "Firewall (filter)", "leases": "DHCP leases", "dst": "Destination", "gateway": "Gateway", "distance": "Distance", "active": "Active",
        "chain": "Chain", "action": "Action", "src": "Source", "proto": "Protocol", "port": "Port", "comment": "Comment",
        "lease_addr": "Address", "mac": "MAC", "host": "Name", "status": "Status",
        "exec_empty": "the command is empty", "exec_long": "the command is longer than 8000 characters", "would": "(dry run) {what}",
        "would_exec": "run on {addr}: {cmd}", "rebooting": "{addr}: reboot started",
        "updating": "{addr}: update {latest} is downloading and installs with a reboot", "no_update": "{addr}: no updates ({installed})",
        "fw_done": "{addr}: the firmware upgrades on the next reboot", "backup_done": "{addr}: backup {name}.backup saved",
        "no_action": "no action {action}", "bad_backup": "backup name: latin letters, digits, - and _",
        "check_ok": "checked",
    },
}

# Службы, которые не должны смотреть на весь интернет без address=.
RISKY_SERVICES = {"telnet": "critical", "ftp": "warning", "www": "warning", "api": "warning", "winbox": "info", "ssh": "info", "api-ssl": "info", "www-ssl": "info"}


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
    print(f"nkt-plugin-mikrotik: {msg}", flush=True)


# --- REST RouterOS 7 ------------------------------------------------------------

class Router:
    """Подключение к одному роутеру на время вызова (учётки — из args.conn)."""

    def __init__(self, c, conn):
        self.c = c
        addr = str(conn.get("address") or "").strip()
        if not re.match(r"^[A-Za-z0-9.\-:\[\]]{1,255}$", addr):
            raise c.fail("unreachable", addr=addr or "—", err="bad address")
        self.mode = str(conn.get("tls") or "pin")
        host, port = addr, None
        m = re.match(r"^\[?([^\]]+?)\]?(?::(\d{1,5}))?$", addr)
        if m and m.group(2) and addr.count(":") == 1 or (m and m.group(2) and addr.startswith("[")):
            host, port = m.group(1), int(m.group(2))
        self.host = host
        self.port = port or (80 if self.mode == "http" else 443)
        self.addr = addr
        self.auth = "Basic " + base64.b64encode(f"{conn.get('user') or ''}:{conn.get('password') or ''}".encode()).decode()
        self.pin = str(conn.get("fingerprint") or "").strip().lower().replace(":", "")
        self.seen = ""  # отпечаток, который увидели

    def _conn(self):
        if self.mode == "http":
            return http.client.HTTPConnection(self.host, self.port, timeout=TIMEOUT)
        if self.mode == "verify":
            ctx = ssl.create_default_context()
        else:
            # Свой сертификат роутера: доверие — по отпечатку, не по
            # удостоверяющему центру.
            ctx = ssl.create_default_context()
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
        return http.client.HTTPSConnection(self.host, self.port, timeout=TIMEOUT, context=ctx)

    def call(self, method, path, body=None):
        c = self.c
        conn = self._conn()
        try:
            conn.connect()
            if self.mode != "http":
                der = conn.sock.getpeercert(binary_form=True) or b""
                self.seen = hashlib.sha256(der).hexdigest()
                if self.mode == "pin" and self.pin and not hmac.compare_digest(self.pin, self.seen):
                    raise c.fail("cert_changed", addr=self.addr, old=self.pin[:16] + "…", new=self.seen[:16] + "…")
            data = json.dumps(body).encode() if body is not None else None
            headers = {"Authorization": self.auth, "Accept": "application/json"}
            if data is not None:
                headers["Content-Type"] = "application/json"
            conn.request(method, "/rest" + path, body=data, headers=headers)
            r = conn.getresponse()
            raw = r.read()
        except Fail:
            raise
        except (OSError, ssl.SSLError, http.client.HTTPException) as e:
            raise c.fail("unreachable", addr=self.addr, err=type(e).__name__ + (f": {e.strerror}" if getattr(e, "strerror", None) else ""))
        finally:
            conn.close()
        if r.status == 401:
            raise c.fail("auth_failed", addr=self.addr)
        if r.status >= 400:
            msg = raw[:300].decode(errors="replace")
            try:
                d = json.loads(raw)
                msg = d.get("detail") or d.get("message") or msg
            except ValueError:
                pass
            if r.status == 404 and path == "/system/resource":
                raise c.fail("not_routeros7", addr=self.addr, err=f"HTTP {r.status}")
            raise c.fail("http_error", addr=self.addr, method=method, path=path, code=r.status, msg=msg)
        try:
            return json.loads(raw or b"null")
        except ValueError:
            raise c.fail("not_routeros7", addr=self.addr, err="not JSON")

    def get(self, path, default=None):
        try:
            return self.call("GET", path)
        except Fail:
            if default is not None:
                return default
            raise

    def execute(self, script):
        """Команда RouterOS с выводом текстом (/execute as-string)."""
        res = self.call("POST", "/execute", {"script": script, "as-string": ""})
        if isinstance(res, dict):
            return str(res.get("ret", ""))
        return ""


def num(v, cast=int, default=None):
    try:
        return cast(v)
    except (TypeError, ValueError):
        return default


UPTIME_RE = re.compile(r"(\d+)([wdhms])")


def uptime_s(s):
    mult = {"w": 604800, "d": 86400, "h": 3600, "m": 60, "s": 1}
    return sum(int(n) * mult[u] for n, u in UPTIME_RE.findall(str(s or "")))


def yes(v):
    return v is True or str(v).lower() in ("true", "yes")


def info(r):
    res = r.call("GET", "/system/resource")  # первым: заодно проверка связи и RouterOS 7
    ident = r.get("/system/identity", {})
    board = r.get("/system/routerboard", {})
    return {
        "name": ident.get("name", "") if isinstance(ident, dict) else "",
        "os": "RouterOS", "version": str(res.get("version", "")).split(" ")[0],
        "model": res.get("board-name") or (board.get("model") if isinstance(board, dict) else "") or "",
        "serial": board.get("serial-number", "") if isinstance(board, dict) else "",
        "arch": res.get("architecture-name", ""), "uptime_s": uptime_s(res.get("uptime")),
        "address": r.addr, "firmware": board.get("current-firmware", "") if isinstance(board, dict) else "",
        "fingerprint": r.seen,
    }, res, board


def as_list(v):
    return v if isinstance(v, list) else []


def snapshot(c, r):
    inf, res, _ = info(r)
    ifaces = as_list(r.get("/interface", []))
    addrs = as_list(r.get("/ip/address", [])) + as_list(r.get("/ipv6/address", []))
    by_if = {}
    for a in addrs:
        if not yes(a.get("disabled")):
            by_if.setdefault(a.get("interface"), []).append(a.get("address"))
    services = as_list(r.get("/ip/service", []))
    total, free = num(res.get("total-hdd-space"), int, 0), num(res.get("free-hdd-space"), int, 0)
    routes = as_list(r.get("/ip/route", []))
    rules = as_list(r.get("/ip/firewall/filter", []))
    leases = as_list(r.get("/ip/dhcp-server/lease", []))
    snap = {
        "info": inf,
        "cpu": {"model": res.get("cpu", ""), "cores": num(res.get("cpu-count"), int, 0)},
        "mem_total": num(res.get("total-memory"), int, 0),
        "disks": [{"name": "flash", "size": total, "used": max(0, total - free)}] if total else [],
        "interfaces": [{"name": i.get("name", ""), "type": i.get("type", ""), "mac": i.get("mac-address", ""),
                        "addrs": by_if.get(i.get("name"), []), "up": yes(i.get("running")), "disabled": yes(i.get("disabled")),
                        "comment": i.get("comment", "")} for i in ifaces],
        "services": [{"name": s.get("name", ""), "state": "stopped" if yes(s.get("disabled")) else "running",
                      "enabled": not yes(s.get("disabled")), "detail": f"port {s.get('port', '')}" + (f", {s.get('address')}" if s.get("address") else "")}
                     for s in services],
        "packages": [{"name": p.get("name", ""), "version": p.get("version", "")} for p in as_list(r.get("/system/package", []))],
        "ports": [{"proto": "tcp", "port": num(s.get("port"), int, 0), "service": s.get("name", ""), "allowed": s.get("address", "")}
                  for s in services if not yes(s.get("disabled"))],
        "users": [{"name": u.get("name", ""), "group": u.get("group", "")} for u in as_list(r.get("/user", []))],
        "neighbors": [{"name": n.get("identity", ""), "address": n.get("address", ""), "mac": n.get("mac-address", ""),
                       "interface": n.get("interface", ""), "platform": " ".join(x for x in (n.get("platform"), n.get("board")) if x)}
                      for n in as_list(r.get("/ip/neighbor", []))],
        "extra": [
            {"type": "table", "title": c.t("routes"), "columns": [{"key": "dst", "title": c.t("dst")}, {"key": "gw", "title": c.t("gateway")},
                                                                 {"key": "dist", "title": c.t("distance")}, {"key": "active", "title": c.t("active")}],
             "rows": [{"cells": {"dst": x.get("dst-address", ""), "gw": x.get("gateway", ""), "dist": x.get("distance", ""),
                                 "active": "✓" if yes(x.get("active")) else ""}} for x in routes]},
            {"type": "table", "title": c.t("firewall"), "columns": [{"key": "chain", "title": c.t("chain")}, {"key": "action", "title": c.t("action")},
                                                                   {"key": "src", "title": c.t("src")}, {"key": "proto", "title": c.t("proto")},
                                                                   {"key": "port", "title": c.t("port")}, {"key": "comment", "title": c.t("comment")}],
             "rows": [{"cells": {"chain": x.get("chain", ""), "action": x.get("action", ""),
                                 "src": x.get("src-address", "") or x.get("src-address-list", "") or x.get("in-interface", "") or x.get("in-interface-list", ""),
                                 "proto": x.get("protocol", ""), "port": x.get("dst-port", ""), "comment": x.get("comment", "")}}
                      for x in rules if not yes(x.get("disabled"))]},
            {"type": "table", "title": c.t("leases"), "columns": [{"key": "addr", "title": c.t("lease_addr")}, {"key": "mac", "title": c.t("mac")},
                                                                 {"key": "host", "title": c.t("host")}, {"key": "status", "title": c.t("status")}],
             "rows": [{"cells": {"addr": x.get("address", ""), "mac": x.get("mac-address", ""), "host": x.get("host-name", ""),
                                 "status": x.get("status", "")}} for x in leases]},
        ],
    }
    return snap


def metrics(r):
    res = r.call("GET", "/system/resource")
    total, free = num(res.get("total-memory"), int), num(res.get("free-memory"), int)
    hdd, hfree = num(res.get("total-hdd-space"), int), num(res.get("free-hdd-space"), int)
    out = {"cpu_pct": num(res.get("cpu-load"), float), "uptime_s": uptime_s(res.get("uptime"))}
    if total:
        out["mem_total"], out["mem_used"] = total, total - (free or 0)
    if hdd:
        out["disk_size"], out["disk_used"] = hdd, hdd - (hfree or 0)
    for h in as_list(r.get("/system/health", [])):
        if "temperature" in str(h.get("name", "")) and num(h.get("value"), float) is not None:
            out["temp_c"] = max(out.get("temp_c", -1000.0), num(h.get("value"), float))
    out["net"] = {i.get("name", ""): {"rx": num(i.get("rx-byte"), int, 0), "tx": num(i.get("tx-byte"), int, 0)}
                  for i in as_list(r.get("/interface", [])) if not yes(i.get("disabled"))}
    return {k: v for k, v in out.items() if v is not None}


UPDATE_CACHE = {}
UPDATE_LOCK = threading.Lock()


def update_status(r):
    """Проверка обновлений — не чаще раза в 6 часов на роутер."""
    with UPDATE_LOCK:
        hit = UPDATE_CACHE.get(r.addr)
        if hit and time.time() - hit[0] < 6 * 3600:
            return hit[1]
    try:
        res = r.call("POST", "/system/package/update/check-for-updates", {})
        st = res[-1] if isinstance(res, list) and res else (res if isinstance(res, dict) else {})
    except Fail:
        st = {}
    with UPDATE_LOCK:
        UPDATE_CACHE[r.addr] = (time.time(), st)
    return st


def findings(c, r, mode):
    out = []
    inf, res, board = info(r)
    st = update_status(r)
    inst, latest = st.get("installed-version", ""), st.get("latest-version", "")
    if latest and inst and latest != inst:
        out.append({"id": "routeros-update", "severity": "warning", "title": c.t("f_update", latest=latest, installed=inst), "fix": c.t("f_update_fix")})
    if isinstance(board, dict) and board.get("current-firmware") and board.get("upgrade-firmware") and board["current-firmware"] != board["upgrade-firmware"]:
        out.append({"id": "firmware", "severity": "info", "title": c.t("f_firmware", current=board["current-firmware"], upgrade=board["upgrade-firmware"]), "fix": c.t("f_firmware_fix")})
    for s in as_list(r.get("/ip/service", [])):
        name = s.get("name", "")
        if yes(s.get("disabled")) or name not in RISKY_SERVICES:
            continue
        if name == "telnet":
            out.append({"id": "service-telnet", "severity": "critical", "title": c.t("f_telnet"), "fix": c.t("f_service_fix", name=name)})
        elif not s.get("address") and RISKY_SERVICES[name] != "info":
            out.append({"id": f"service-{name}", "severity": RISKY_SERVICES[name], "title": c.t("f_service", name=name, port=s.get("port", "")),
                        "fix": c.t("f_service_fix", name=name)})
    if any(u.get("name") == "admin" and not yes(u.get("disabled")) for u in as_list(r.get("/user", []))):
        out.append({"id": "user-admin", "severity": "info", "title": c.t("f_admin"), "fix": c.t("f_admin_fix")})
    rules = [x for x in as_list(r.get("/ip/firewall/filter", [])) if not yes(x.get("disabled"))]
    if not any(x.get("chain") == "input" and x.get("action") == "drop" for x in rules):
        out.append({"id": "firewall-input", "severity": "warning", "title": c.t("f_no_drop"), "fix": c.t("f_no_drop_fix")})
    if mode == "http":
        out.append({"id": "transport-http", "severity": "warning", "title": c.t("f_http"), "fix": c.t("f_http_fix")})
    cpu = num(res.get("cpu-load"), int, 0)
    if cpu >= 90:
        out.append({"id": "cpu", "severity": "warning", "title": c.t("f_cpu", pct=cpu)})
    return out


VOLATILE_RE = re.compile(r"^# (?:\d{4}-\d{2}-\d{2}|[a-z]{3}/\d{2}/\d{4}) \d{2}:\d{2}:\d{2} by RouterOS")


def config(r):
    text = r.execute("/export terse show-sensitive=no")
    # Строка с временем выгрузки меняется каждый раз — без неё дифф только
    # о настройках.
    return "\n".join(l for l in text.replace("\r\n", "\n").split("\n") if not VOLATILE_RE.match(l)).strip() + "\n"


BACKUP_RE = re.compile(r"^[A-Za-z0-9_-]{1,64}$")


def action(c, r, name, args, dry):
    if name == "reboot":
        if dry:
            return c.t("would", what=c.t("reboot"))
        try:
            r.call("POST", "/system/reboot", {})
        except Fail:
            pass  # роутер рвёт соединение, уходя в перезагрузку
        return c.t("rebooting", addr=r.addr)
    if name == "update":
        st = update_status(r)
        inst, latest = st.get("installed-version", ""), st.get("latest-version", "")
        if not latest or latest == inst:
            return c.t("no_update", addr=r.addr, installed=inst or "?")
        if dry:
            return c.t("would", what=c.t("updating", addr=r.addr, latest=latest))
        try:
            r.call("POST", "/system/package/update/install", {})
        except Fail:
            pass
        with UPDATE_LOCK:
            UPDATE_CACHE.pop(r.addr, None)
        return c.t("updating", addr=r.addr, latest=latest)
    if name == "firmware":
        if dry:
            return c.t("would", what=c.t("firmware"))
        r.call("POST", "/system/routerboard/upgrade", {})
        return c.t("fw_done", addr=r.addr)
    if name == "backup":
        bn = str(args.get("name") or "nkt-" + time.strftime("%Y%m%d-%H%M")).strip()
        if not BACKUP_RE.match(bn):
            raise c.fail("bad_backup")
        if dry:
            return c.t("would", what=c.t("backup_done", addr=r.addr, name=bn))
        r.call("POST", "/system/backup/save", {"name": bn, "dont-encrypt": "no"})
        return c.t("backup_done", addr=r.addr, name=bn)
    raise c.fail("no_action", action=name)


def exec_cmd(c, r, cmd, check, dry):
    cmd = str(cmd or "").replace("\r\n", "\n").strip()
    if not cmd:
        raise c.fail("exec_empty")
    if len(cmd) > 8000 or "\x00" in cmd:
        raise c.fail("exec_long")
    if check:
        return c.t("check_ok")
    if dry:
        return c.t("would", what=c.t("would_exec", addr=r.addr, cmd=cmd))
    return r.execute(cmd)


# --- манифест и вызовы --------------------------------------------------------

def manifest(c):
    return {
        "protocol": 1, "name": "mikrotik", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [], "provides": ["hosts"],
        "host_types": [{
            "id": "routeros", "title": c.t("type"), "hint": c.t("type_hint"),
            "fields": [
                {"name": "address", "title": c.t("address"), "required": True, "placeholder": "192.168.88.1", "hint": c.t("address_hint")},
                {"name": "user", "title": c.t("user"), "required": True, "default": "nkt", "hint": c.t("user_hint")},
                {"name": "password", "title": c.t("password"), "type": "password", "required": True},
                {"name": "tls", "title": c.t("tls"), "type": "select", "default": "pin", "hint": c.t("tls_hint"),
                 "options": [{"value": "pin", "title": c.t("tls_pin")}, {"value": "verify", "title": c.t("tls_verify")}, {"value": "http", "title": c.t("tls_http")}]},
                {"name": "fingerprint", "title": c.t("fingerprint"), "hint": c.t("fingerprint_hint")},
            ],
            "sections": ["overview", "metrics", "findings", "config", "actions", "exec"],
            "actions": [
                {"title": c.t("reboot"), "op": "reboot", "confirm": c.t("reboot_confirm"), "danger": True},
                {"title": c.t("update"), "op": "update", "confirm": c.t("update_confirm"), "danger": True},
                {"title": c.t("firmware"), "op": "firmware", "confirm": c.t("firmware_confirm")},
                {"title": c.t("backup"), "op": "backup", "fields": [{"name": "name", "title": c.t("backup_name"), "hint": c.t("backup_hint"), "placeholder": "nkt-20260101"}]},
            ],
            "exec": "routeros",
        }],
    }


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or yes(args.get("dry_run"))
    if op == "_connect":
        log(f"подключён к хабу как {args.get('name')}")
        return {"ok": True}
    if not op.startswith("_host_"):
        return {"ok": False, "error": f"no op {op}"}
    if args.get("type") != "routeros":
        return {"ok": False, "error": f"unknown host type {args.get('type')}"}
    conn = args.get("conn") or {}
    r = Router(c, conn)
    if op == "_host_check":
        inf, _, _ = info(r)
        return {"ok": True, "result": inf}
    if op == "_host_snapshot":
        return {"ok": True, "result": snapshot(c, r)}
    if op == "_host_metrics":
        return {"ok": True, "result": metrics(r)}
    if op == "_host_findings":
        return {"ok": True, "result": {"findings": findings(c, r, r.mode)}}
    if op == "_host_config":
        return {"ok": True, "result": {"text": config(r)}}
    if op == "_host_action":
        text = action(c, r, str(args.get("action") or ""), args.get("args") or {}, dry)
        if not dry:
            log(f"{req.get('user', '')}: {args.get('action')} на {r.addr}")
        return {"ok": True, "text": text}
    if op == "_host_exec":
        check = yes(args.get("check"))
        text = exec_cmd(c, r, args.get("command"), check, dry)
        if not check and not dry:
            log(f"{req.get('user', '')}: команда на {r.addr} ({len(str(args.get('command') or ''))} знаков)")
        return {"ok": True, "text": text}
    return {"ok": False, "error": f"no op {op}"}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-mikrotik/" + VERSION

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
    socket.setdefaulttimeout(TIMEOUT + 5)
    log(f"{VERSION}: {LISTEN}:{PORT}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
