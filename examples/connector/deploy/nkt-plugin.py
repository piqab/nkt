"""Плагин хаба nkt «connector» (протокол плагинов nkt v1, provides hosts):
устройства без агента nkt по описанию — профилю, без своего кода.

Профиль — документ JSON: как подключиться (ssh — команды; http — запросы
к API; snmp — OID), что прочитать и как разобрать ответ (JSON-путь,
регулярное выражение, столбцы, ключ=значение), куда положить (общая модель
nkt: состав, метрики, находки, конфигурация, действия, команды). Каждый
профиль — тип устройства в «Хосты» → «Добавить устройство». Встроенные:
macOS, FreeBSD, OpenWrt, Linux без агента, Keenetic, принтер и коммутатор
по SNMP; свои — раздел плагина «Профили» (правка с диффом и историей в
хабе), хранятся в STATE_DIRECTORY службы.

Учётки устройств хранит хаб и передаёт в каждом вызове (args.conn); плагин
их не хранит и не пишет в журнал. SSH — системный клиент (ssh, ssh-keyscan):
ключ хоста запоминается при «Проверить» и сверяется; пароль — через
SSH_ASKPASS, не в командной строке.
Без зависимостей: только стандартная библиотека Python.
"""
import base64
import calendar
import hashlib
import hmac
import http.client
import json
import os
import re
import shlex
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import xml.etree.ElementTree as ET
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.1.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9230") or 9230)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
STATE = os.environ.get("CONNECTOR_STATE") or os.environ.get("STATE_DIRECTORY") or os.path.join(os.getcwd(), "connector-state")
SSH = os.environ.get("CONNECTOR_SSH", "ssh")
SSH_KEYSCAN = os.environ.get("CONNECTOR_SSH_KEYSCAN", "ssh-keyscan")
TIMEOUT = float(os.environ.get("CONNECTOR_TIMEOUT", "25") or 25)

T = {
    "ru": {
        "title": "Connector (профили устройств)",
        "desc": "Устройства без агента nkt по профилям — без своего кода: SSH (macOS, FreeBSD, OpenWrt, Linux без агента), HTTP API (Keenetic), ИБП (SNMP, NUT), камеры и регистраторы (ONVIF), SNMP (принтеры, коммутаторы). Свои профили — в разделе «Профили».",
        "profiles": "Профили", "id": "Профиль", "ptitle": "Название", "transport": "Связь", "source": "Откуда",
        "builtin": "встроенный", "custom": "свой", "override": "свой (вместо встроенного)",
        "edit": "Править", "new": "Новый профиль", "delete": "Удалить", "base": "Взять за основу",
        "delete_confirm": "Удалить свой профиль {id}? Устройства этого типа перестанут опрашиваться (встроенный с тем же именем вернётся).",
        "profiles_note": "Каждый профиль — тип устройства в «Хосты» → «Добавить устройство». Править встроенный — значит завести свой с тем же именем (встроенный останется в плагине). Формат — справка «Хосты плагинов: connector».",
        "f_address": "Адрес", "f_address_hint": "IP или имя; порт — через двоеточие",
        "f_user": "Пользователь", "f_password": "Пароль", "f_key": "Закрытый ключ SSH",
        "f_key_hint": "вставьте целиком (BEGIN … END) — вместо пароля; хранит хаб",
        "f_fingerprint": "Отпечаток ключа или сертификата", "f_fingerprint_hint": "пусто — запомнится при «Проверить»",
        "f_community": "Community (SNMP v2c)", "f_community_hint": "только чтение; не public",
        "f_tls": "Связь", "tls_pin": "HTTPS, свой сертификат (запомнить отпечаток)", "tls_verify": "HTTPS, проверять сертификат", "tls_http": "HTTP без шифрования (только в своей сети)",
        "no_ssh": "на хосте плагина нет {tool} — поставьте openssh-client",
        "ssh_auth": "{addr}: SSH не пустил ({err})", "ssh_fail": "{addr}: SSH — {err}",
        "key_changed": "{addr}: ключ или сертификат устройства изменился (был {old}, сейчас {new}) — если это ожидаемо, очистите отпечаток в учётках хоста и нажмите «Проверить»",
        "unreachable": "{addr}: нет связи — {err}", "http_error": "{addr}: {path} — HTTP {code}", "auth_failed": "{addr}: неверный логин или пароль",
        "snmp_timeout": "{addr}: SNMP не отвечает (адрес, порт 161/udp, community?)",
        "bad_profile": "профиль {id}: {err}", "no_profile": "нет профиля {id}", "builtin_only": "{id} — встроенный: удалять нечего",
        "bad_id": "имя профиля {id}: латиница, цифры, - (до 40)", "exists": "профиль {id} уже есть",
        "saved": "профиль {id} сохранён", "created": "профиль {id} заведён", "deleted": "профиль {id} удалён",
        "no_action": "нет действия {action}", "no_exec": "у профиля {id} нет команд",
        "exec_empty": "команда пуста", "exec_syntax": "ошибка разбора sh: {err}", "would": "(сухой прогон) {what}", "would_exec": "выполнить на {addr}: {cmd}",
        "check_ok": "проверено",
        "f_ups": "Имя ИБП", "f_ups_hint": "как в ups.conf сервера NUT (upsc -l)", "f_nut_addr_hint": "сервер NUT (upsd); порт — через двоеточие, если не 3493",
        "f_nut_user_hint": "только для самотеста: пользователь upsd.users с instcmds",
        "nut_err": "{addr}: NUT — {err}", "onvif_err": "{addr}: ONVIF — {err}",
    },
    "en": {
        "title": "Connector (device profiles)",
        "desc": "Devices without the nkt agent by profiles — no code of your own: SSH (macOS, FreeBSD, OpenWrt, agentless Linux), HTTP API (Keenetic), UPSes (SNMP, NUT), cameras and NVRs (ONVIF), SNMP (printers, switches). Your own profiles — the Profiles section.",
        "profiles": "Profiles", "id": "Profile", "ptitle": "Title", "transport": "Link", "source": "Source",
        "builtin": "built-in", "custom": "custom", "override": "custom (instead of built-in)",
        "edit": "Edit", "new": "New profile", "delete": "Delete", "base": "Start from",
        "delete_confirm": "Delete your profile {id}? Devices of this type stop being polled (a built-in one with the same name comes back).",
        "profiles_note": "Each profile is a device type in Hosts → Add a device. Editing a built-in one creates your own with the same name (the built-in stays in the plugin). The format — the “Plugin hosts: connector” guide.",
        "f_address": "Address", "f_address_hint": "IP or name; a port after a colon",
        "f_user": "User", "f_password": "Password", "f_key": "SSH private key",
        "f_key_hint": "paste it whole (BEGIN … END) — instead of a password; the hub keeps it",
        "f_fingerprint": "Key or certificate fingerprint", "f_fingerprint_hint": "empty — remembered on Check",
        "f_community": "Community (SNMP v2c)", "f_community_hint": "read-only; not public",
        "f_tls": "Link", "tls_pin": "HTTPS, its own certificate (remember the fingerprint)", "tls_verify": "HTTPS, verify the certificate", "tls_http": "HTTP without encryption (own network only)",
        "no_ssh": "{tool} is not on the plugin host — install openssh-client",
        "ssh_auth": "{addr}: SSH refused ({err})", "ssh_fail": "{addr}: SSH — {err}",
        "key_changed": "{addr}: the device key or certificate changed (was {old}, now {new}) — if expected, clear the fingerprint in the host's login and press Check",
        "unreachable": "{addr}: unreachable — {err}", "http_error": "{addr}: {path} — HTTP {code}", "auth_failed": "{addr}: wrong login or password",
        "snmp_timeout": "{addr}: SNMP doesn't answer (address, port 161/udp, community?)",
        "bad_profile": "profile {id}: {err}", "no_profile": "no profile {id}", "builtin_only": "{id} is built-in: nothing to delete",
        "bad_id": "profile name {id}: latin letters, digits, - (up to 40)", "exists": "profile {id} already exists",
        "saved": "profile {id} saved", "created": "profile {id} created", "deleted": "profile {id} deleted",
        "no_action": "no action {action}", "no_exec": "profile {id} has no commands",
        "exec_empty": "the command is empty", "exec_syntax": "sh parse error: {err}", "would": "(dry run) {what}", "would_exec": "run on {addr}: {cmd}",
        "check_ok": "checked",
        "f_ups": "UPS name", "f_ups_hint": "as in the NUT server's ups.conf (upsc -l)", "f_nut_addr_hint": "the NUT server (upsd); a port after a colon if not 3493",
        "f_nut_user_hint": "only for the self-test: an upsd.users user with instcmds",
        "nut_err": "{addr}: NUT — {err}", "onvif_err": "{addr}: ONVIF — {err}",
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

    def txt(self, v):
        """Текст профиля: строка или {ru, en}."""
        if isinstance(v, dict):
            return str(v.get(self.lang) or v.get("ru") or v.get("en") or "")
        return "" if v is None else str(v)


def log(msg):
    print(f"nkt-plugin-connector: {msg}", flush=True)


def split_addr(addr, port):
    addr = str(addr or "").strip()
    # Первый знак — не «-»: адрес уходит в argv ssh и ssh-keyscan.
    if not re.match(r"^[A-Za-z0-9\[][A-Za-z0-9.\-:\[\]]{0,254}$", addr):
        raise Fail(f"bad address {addr!r}")
    m = re.match(r"^\[([^\]]+)\](?::(\d{1,5}))?$", addr)
    if m:
        return m.group(1), int(m.group(2) or port)
    if addr.count(":") == 1:
        h, p = addr.split(":")
        if not h or not p.isdigit() or not 0 < int(p) < 65536:
            raise Fail(f"bad address {addr!r}")
        return h, int(p)
    return addr, port

def ssh_user(user):
    """Имя для ssh -l: не с «-» и без управляющих знаков; пробел и «@» можно
    (локальные имена Windows с пробелом, домен user@corp.local)."""
    user = user.strip()
    if not user or user.startswith("-") or len(user) > 256 or any(ord(ch) < 32 or ord(ch) == 127 for ch in user):
        raise Fail(f"bad user {user!r}")
    return user



# --- SSH -------------------------------------------------------------------------

PEM_RE = re.compile(r"(-----BEGIN [A-Z ]+-----)\s*(.*?)\s*(-----END [A-Z ]+-----)", re.S)


def pem_lines(key):
    """Ключ из однострочного поля: переводы строк браузер заменил пробелами."""
    m = PEM_RE.search(key or "")
    if not m:
        return None
    body = "\n".join(re.findall(r"\S{1,70}", re.sub(r"\s+", "", m.group(2))))
    return f"{m.group(1)}\n{body}\n{m.group(3)}\n"


def key_fingerprint(b64):
    raw = base64.b64decode(b64 + "=" * (-len(b64) % 4))
    return "SHA256:" + base64.b64encode(hashlib.sha256(raw).digest()).decode().rstrip("=")


class SSHLink:
    def __init__(self, c, conn):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.host, self.port = split_addr(self.addr, 22)
        self.user = ssh_user(str(conn.get("user") or "root"))
        self.password = str(conn.get("password") or "")
        self.key = pem_lines(str(conn.get("key") or ""))
        self.pin = str(conn.get("fingerprint") or "").strip()
        self.seen = ""
        self.tmp = None

    def __enter__(self):
        for tool in (SSH, SSH_KEYSCAN):
            if not shutil.which(tool):
                raise self.c.fail("no_ssh", tool=tool)
        self.tmp = tempfile.mkdtemp(prefix="nkt-connector-")
        os.chmod(self.tmp, 0o700)
        try:
            out = subprocess.run([SSH_KEYSCAN, "-T", "8", "-p", str(self.port), "--", self.host], capture_output=True, text=True, timeout=TIMEOUT).stdout
        except subprocess.TimeoutExpired:
            raise self.c.fail("unreachable", addr=self.addr, err="ssh-keyscan timeout")
        keys = [l for l in out.splitlines() if l.strip() and not l.startswith("#") and len(l.split()) >= 3]
        if not keys:
            raise self.c.fail("unreachable", addr=self.addr, err="no SSH host key")
        fps = [key_fingerprint(l.split()[2]) for l in keys]
        prefer = [f for l, f in zip(keys, fps) if l.split()[1] == "ssh-ed25519"]
        self.seen = (prefer or fps)[0]
        if self.pin and self.pin not in fps:
            raise self.c.fail("key_changed", addr=self.addr, old=self.pin[:24] + "…", new=self.seen[:24] + "…")
        with open(os.path.join(self.tmp, "known_hosts"), "w") as f:
            f.write("\n".join(keys) + "\n")
        if self.key:
            kp = os.path.join(self.tmp, "id")
            with open(os.open(kp, os.O_WRONLY | os.O_CREAT, 0o600), "w") as f:
                f.write(self.key)
        else:
            ap = os.path.join(self.tmp, "askpass")
            with open(os.open(ap, os.O_WRONLY | os.O_CREAT, 0o700), "w") as f:
                f.write('#!/bin/sh\nprintf \'%s\\n\' "$NKT_SSH_PASS"\n')
        return self

    def __exit__(self, *a):
        if self.tmp:
            shutil.rmtree(self.tmp, ignore_errors=True)

    def run(self, cmd, timeout=None):
        args = [SSH, "-F", "/dev/null", "-p", str(self.port), "-o", "StrictHostKeyChecking=yes",
                "-o", "UserKnownHostsFile=" + os.path.join(self.tmp, "known_hosts"), "-o", "ConnectTimeout=10",
                "-o", "LogLevel=ERROR", "-o", "ServerAliveInterval=10"]
        env = dict(os.environ)
        if self.key:
            args += ["-i", os.path.join(self.tmp, "id"), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes"]
        else:
            args += ["-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "NumberOfPasswordPrompts=1"]
            env.update(SSH_ASKPASS=os.path.join(self.tmp, "askpass"), SSH_ASKPASS_REQUIRE="force", DISPLAY=env.get("DISPLAY", ":0"), NKT_SSH_PASS=self.password)
        args += ["-l", self.user, "--", self.host, cmd]
        try:
            r = subprocess.run(args, capture_output=True, text=True, timeout=timeout or TIMEOUT, env=env, stdin=subprocess.DEVNULL, start_new_session=True)
        except subprocess.TimeoutExpired:
            raise self.c.fail("ssh_fail", addr=self.addr, err="timeout")
        if r.returncode == 255:
            err = (r.stderr.strip().splitlines() or ["?"])[-1][:200]
            if "Permission denied" in err or "Authentication" in err:
                raise self.c.fail("ssh_auth", addr=self.addr, err=err)
            raise self.c.fail("ssh_fail", addr=self.addr, err=err)
        return r.stdout


# --- HTTP ------------------------------------------------------------------------

class HTTPLink:
    def __init__(self, c, conn, auth):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.mode = str(conn.get("tls") or "http")
        self.host, self.port = split_addr(self.addr, 80 if self.mode == "http" else 443)
        self.user, self.password = str(conn.get("user") or ""), str(conn.get("password") or "")
        self.pin = str(conn.get("fingerprint") or "").strip().lower().replace(":", "")
        self.auth = auth
        self.cookie = ""
        self.seen = ""
        self.logged = False

    def __enter__(self):
        return self

    def __exit__(self, *a):
        pass

    def _req(self, method, path, body=None, headers=None):
        c = self.c
        if self.mode == "http":
            conn = http.client.HTTPConnection(self.host, self.port, timeout=TIMEOUT)
        else:
            ctx = ssl.create_default_context()
            if self.mode != "verify":
                ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
            conn = http.client.HTTPSConnection(self.host, self.port, timeout=TIMEOUT, context=ctx)
        h = {"Accept": "application/json"}
        h.update(headers or {})
        if self.cookie:
            h["Cookie"] = self.cookie
        if self.auth == "basic":
            h["Authorization"] = "Basic " + base64.b64encode(f"{self.user}:{self.password}".encode()).decode()
        elif self.auth == "bearer":
            h["Authorization"] = "Bearer " + self.password
        if isinstance(body, bytes):  # SOAP и прочее не-JSON — как есть
            data = body
        else:
            data = json.dumps(body).encode() if body is not None else None
            if data is not None:
                h["Content-Type"] = "application/json"
        try:
            conn.connect()
            if self.mode != "http":
                self.seen = hashlib.sha256(conn.sock.getpeercert(binary_form=True) or b"").hexdigest()
                if self.mode == "pin" and self.pin and not hmac.compare_digest(self.pin, self.seen):
                    raise c.fail("key_changed", addr=self.addr, old=self.pin[:16] + "…", new=self.seen[:16] + "…")
            conn.request(method, path, body=data, headers=h)
            r = conn.getresponse()
            raw = r.read()
            cookies = r.headers.get_all("Set-Cookie") or []
        except Fail:
            raise
        except (OSError, http.client.HTTPException) as e:
            raise c.fail("unreachable", addr=self.addr, err=type(e).__name__)
        finally:
            conn.close()
        for ck in cookies:
            self.cookie = ck.split(";", 1)[0]
        return r, raw

    def login(self):
        """Keenetic: вызов X-NDM-Challenge → sha256(challenge + md5(login:realm:password))."""
        if self.auth != "keenetic" or self.logged:
            return
        r, _ = self._req("GET", "/auth")
        if r.status == 401:
            realm, ch = r.headers.get("X-NDM-Realm", ""), r.headers.get("X-NDM-Challenge", "")
            md5 = hashlib.md5(f"{self.user}:{realm}:{self.password}".encode()).hexdigest()
            r2, _ = self._req("POST", "/auth", {"login": self.user, "password": hashlib.sha256((ch + md5).encode()).hexdigest()})
            if r2.status != 200:
                raise self.c.fail("auth_failed", addr=self.addr)
        elif r.status != 200:
            raise self.c.fail("http_error", addr=self.addr, path="/auth", code=r.status)
        self.logged = True

    def request(self, method, path, body=None, headers=None):
        """Ответ как есть; digest — второй запрос с ответом на вызов сервера
        (камеры и регистраторы: ISAPI Hikvision, CGI Dahua)."""
        r, raw = self._req(method, path, body, headers)
        ch = r.headers.get("WWW-Authenticate", "")
        if r.status == 401 and self.auth == "digest" and ch.lower().startswith("digest"):
            h = dict(headers or {})
            h["Authorization"] = digest_header(self.user, self.password, method, path, ch)
            r, raw = self._req(method, path, body, h)
        return r, raw

    def call(self, method, path, body=None):
        self.login()
        r, raw = self.request(method, path, body)
        if r.status in (401, 403):
            raise self.c.fail("auth_failed", addr=self.addr)
        if r.status >= 400:
            raise self.c.fail("http_error", addr=self.addr, path=path, code=r.status)
        try:
            return json.loads(raw)
        except ValueError:
            return raw.decode(errors="replace")


def digest_header(user, password, method, uri, challenge):
    """HTTP Digest (RFC 7616, MD5 и MD5-sess, qop=auth)."""
    p = {k.lower(): a or b for k, a, b in re.findall(r'(\w+)=(?:"([^"]*)"|([^,\s]*))', challenge[6:])}
    md5 = lambda x: hashlib.md5(x.encode()).hexdigest()
    realm, nonce, alg = p.get("realm", ""), p.get("nonce", ""), p.get("algorithm", "MD5")
    cnonce, nc = os.urandom(8).hex(), "00000001"
    ha1 = md5(f"{user}:{realm}:{password}")
    if alg.lower() == "md5-sess":
        ha1 = md5(f"{ha1}:{nonce}:{cnonce}")
    ha2 = md5(f"{method}:{uri}")
    qop = "auth" if "auth" in [q.strip() for q in p.get("qop", "").split(",")] else ""
    resp = md5(f"{ha1}:{nonce}:{nc}:{cnonce}:auth:{ha2}") if qop else md5(f"{ha1}:{nonce}:{ha2}")
    q = lambda v: '"' + str(v).replace("\\", "\\\\").replace('"', '\\"') + '"'
    parts = [f"username={q(user)}", f"realm={q(realm)}", f"nonce={q(nonce)}", f"uri={q(uri)}", f"response={q(resp)}", f"algorithm={alg}"]
    if qop:
        parts += [f"qop={qop}", f"nc={nc}", f"cnonce={q(cnonce)}"]
    if p.get("opaque"):
        parts.append(f"opaque={q(p['opaque'])}")
    return "Digest " + ", ".join(parts)


def xml_obj(v):
    """XML → вложенные dict: имена без пространств имён, атрибуты — «@имя»,
    повторяющиеся элементы — список."""
    if isinstance(v, bytes):
        v = v.decode(errors="replace")
    def conv(e):
        kids = list(e)
        attrs = {"@" + k.split("}")[-1]: x for k, x in e.attrib.items()}
        if not kids:
            text = (e.text or "").strip()
            return dict(attrs, **({"#text": text} if text else {})) if attrs else text
        out = dict(attrs)
        for k in kids:
            name, val = k.tag.split("}")[-1], conv(k)
            if name in out:
                out[name] = (out[name] if isinstance(out[name], list) else [out[name]]) + [val]
            else:
                out[name] = val
        return out
    root = ET.fromstring(str(v).strip())
    return {root.tag.split("}")[-1]: conv(root)}


def dahua_disks(v):
    """CGI Dahua storageDevice getDeviceAllInfo: «list.info[0].State=Success»
    → строки по дискам."""
    rows = {}
    for line in str(v or "").splitlines():
        m = re.match(r"^list\.info\[(\d+)\]\.(\w+)(?:\[0\]\.(\w+))?=(.*)$", line.strip())
        if not m:
            continue
        r = rows.setdefault(int(m.group(1)), {"index": m.group(1)})
        key = (m.group(3) or m.group(2)).lower()
        r[{"state": "state", "name": "name", "totalbytes": "total", "usedbytes": "used"}.get(key, key)] = m.group(4)
    return [rows[k] for k in sorted(rows)]


# --- NUT (Network UPS Tools, upsd — порт 3493) --------------------------------------

NUT_UPS_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
NUT_CMD_RE = re.compile(r"^[a-z0-9._]{1,64}$")


class NUTLink:
    def __init__(self, c, conn):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.host, self.port = split_addr(self.addr, 3493)
        self.ups = str(conn.get("ups") or "ups").strip()
        if not NUT_UPS_RE.match(self.ups):
            raise Fail(f"bad UPS name {self.ups!r}")
        self.user, self.password = str(conn.get("user") or ""), str(conn.get("password") or "")
        self.seen = ""

    def __enter__(self):
        try:
            self.sock = socket.create_connection((self.host, self.port), timeout=TIMEOUT)
        except OSError as e:
            raise self.c.fail("unreachable", addr=self.addr, err=type(e).__name__)
        self.f = self.sock.makefile("rwb")
        return self

    def __exit__(self, *a):
        try:
            self.f.write(b"LOGOUT\n")
            self.f.flush()
        except OSError:
            pass
        self.sock.close()

    def _say(self, line):
        if "\n" in line or "\r" in line:
            raise Fail("bad NUT command")
        try:
            self.f.write(line.encode() + b"\n")
            self.f.flush()
        except OSError as e:
            raise self.c.fail("unreachable", addr=self.addr, err=type(e).__name__)

    def _hear(self):
        try:
            line = self.f.readline(65536)
        except OSError as e:
            raise self.c.fail("unreachable", addr=self.addr, err=type(e).__name__)
        if not line:
            raise self.c.fail("unreachable", addr=self.addr, err="closed")
        return line.decode(errors="replace").rstrip("\r\n")

    def nut(self, what):
        if what != "vars":
            raise ValueError(f"nut: {what}")
        self._say(f"LIST VAR {self.ups}")
        first = self._hear()
        if first.startswith("ERR"):
            raise self.c.fail("nut_err", addr=self.addr, err=first[4:])
        out = {}
        while True:
            line = self._hear()
            if line.startswith("END LIST"):
                return out
            m = re.match(r'^VAR \S+ (\S+) "(.*)"$', line)
            if m:
                out[m.group(1)] = m.group(2).replace('\\"', '"').replace("\\\\", "\\")

    def instcmd(self, cmd):
        if not NUT_CMD_RE.match(cmd):
            raise Fail(f"bad NUT command {cmd!r}")
        q = lambda v: '"' + v.replace("\\", "\\\\").replace('"', '\\"') + '"'
        for line in (f"USERNAME {q(self.user)}", f"PASSWORD {q(self.password)}"):
            self._say(line)
            if not self._hear().startswith("OK"):
                raise self.c.fail("auth_failed", addr=self.addr)
        self._say(f"INSTCMD {self.ups} {cmd}")
        r = self._hear()
        if "ACCESS-DENIED" in r or "USERNAME-REQUIRED" in r or "PASSWORD-REQUIRED" in r:
            raise self.c.fail("auth_failed", addr=self.addr)  # upsd отвечает на сам INSTCMD
        if not r.startswith("OK"):
            raise self.c.fail("nut_err", addr=self.addr, err=r[4:] if r.startswith("ERR") else r)
        return r


# --- ONVIF (SOAP, WS-Security UsernameToken) ----------------------------------------

ONVIF_DEV = "/onvif/device_service"
NS_DEV = "http://www.onvif.org/ver10/device/wsdl"
NS_MEDIA = "http://www.onvif.org/ver10/media/wsdl"


def xml_text(v):
    return str(v).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


class ONVIFLink(HTTPLink):
    """Камера или регистратор: ONVIF по SOAP (пароль — дайджестом WS-Security,
    не открытым текстом) и HTTP digest для API производителя (ISAPI, CGI)."""

    def __init__(self, c, conn):
        super().__init__(c, conn, "digest")

    def soap(self, path, inner):
        created = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        nonce = os.urandom(16)
        digest = base64.b64encode(hashlib.sha1(nonce + created.encode() + self.password.encode()).digest()).decode()
        env = ('<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header>'
               '<Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">'
               f'<UsernameToken><Username>{xml_text(self.user)}</Username>'
               '<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">'
               f'{digest}</Password><Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">'
               f'{base64.b64encode(nonce).decode()}</Nonce>'
               f'<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">{created}</Created>'
               f'</UsernameToken></Security></s:Header><s:Body>{inner}</s:Body></s:Envelope>')
        r, raw = self.request("POST", path, env.encode(), {"Content-Type": "application/soap+xml; charset=utf-8", "Accept": "*/*"})
        try:
            body = xml_obj(raw).get("Envelope", {}).get("Body", {})
        except ET.ParseError:
            body = None
        if not isinstance(body, dict):
            if r.status in (401, 403):
                raise self.c.fail("auth_failed", addr=self.addr)
            raise self.c.fail("http_error", addr=self.addr, path=path, code=r.status)
        if "Fault" in body:
            text = json.dumps(body["Fault"], ensure_ascii=False)
            if "NotAuthorized" in text or r.status in (401, 403):
                raise self.c.fail("auth_failed", addr=self.addr)
            raise self.c.fail("onvif_err", addr=self.addr, err=text[:300])
        return body

    def _media_path(self):
        try:
            caps = self.soap(ONVIF_DEV, f'<GetCapabilities xmlns="{NS_DEV}"><Category>Media</Category></GetCapabilities>')
            x = path_get(caps, "GetCapabilitiesResponse.Capabilities.Media.XAddr")
            return urllib.parse.urlparse(str(x)).path or "/onvif/media_service"
        except Fail:
            return "/onvif/media_service"

    def onvif(self, what):
        if what == "info":
            return self.soap(ONVIF_DEV, f'<GetDeviceInformation xmlns="{NS_DEV}"/>').get("GetDeviceInformationResponse", {})
        if what == "time":
            r = path_get(self.soap(ONVIF_DEV, f'<GetSystemDateAndTime xmlns="{NS_DEV}"/>'), "GetSystemDateAndTimeResponse.SystemDateAndTime") or {}
            u = r.get("UTCDateTime") or {}
            d, t = u.get("Date") or {}, u.get("Time") or {}
            out = {"type": r.get("DateTimeType", ""), "tz": path_get(r, "TimeZone.TZ") or ""}
            try:
                ts = calendar.timegm((int(d["Year"]), int(d["Month"]), int(d["Day"]), int(t["Hour"]), int(t["Minute"]), int(t["Second"]), 0, 0, 0))
                out["drift_s"] = int(ts - time.time())
                out["drift_abs"] = abs(out["drift_s"])
            except (KeyError, ValueError, TypeError):
                pass
            return out
        if what == "streams":
            media = self._media_path()
            profiles = path_get(self.soap(media, f'<GetProfiles xmlns="{NS_MEDIA}"/>'), "GetProfilesResponse.Profiles")
            rows = []
            for pr in (profiles if isinstance(profiles, list) else [profiles] if profiles else [])[:8]:
                token = str(pr.get("@token", ""))
                enc = pr.get("VideoEncoderConfiguration") or {}
                res = enc.get("Resolution") or {}
                uri = ""
                try:
                    uri = path_get(self.soap(media, f'<GetStreamUri xmlns="{NS_MEDIA}"><StreamSetup><Stream xmlns="http://www.onvif.org/ver10/schema">RTP-Unicast</Stream>'
                                                   f'<Transport xmlns="http://www.onvif.org/ver10/schema"><Protocol>RTSP</Protocol></Transport></StreamSetup>'
                                                   f'<ProfileToken>{xml_text(token)}</ProfileToken></GetStreamUri>'), "GetStreamUriResponse.MediaUri.Uri") or ""
                except Fail:
                    pass
                rows.append({"name": str(pr.get("Name") or token), "token": token, "encoding": str(enc.get("Encoding") or ""),
                             "resolution": f"{res.get('Width')}x{res.get('Height')}" if res else "", "uri": str(uri)})
            return rows
        if what == "config":
            out = {}
            for name in ("GetHostname", "GetNTP", "GetDNS", "GetNetworkProtocols", "GetNetworkInterfaces"):
                try:
                    body = self.soap(ONVIF_DEV, f'<{name} xmlns="{NS_DEV}"/>')
                    out[name[3:]] = body.get(name + "Response", body)
                except Fail:
                    continue
            return out
        raise ValueError(f"onvif: {what}")

    def onvif_call(self, name):
        if name != "SystemReboot":
            raise ValueError(f"onvif action {name}")
        body = self.soap(ONVIF_DEV, f'<SystemReboot xmlns="{NS_DEV}"/>')
        return str(path_get(body, "SystemRebootResponse.Message") or "")


# --- SNMP v2c (BER — без библиотек) ----------------------------------------------

def ber_len(n):
    if n < 0x80:
        return bytes([n])
    b = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return bytes([0x80 | len(b)]) + b


def ber(tag, payload):
    return bytes([tag]) + ber_len(len(payload)) + payload


def ber_int(v):
    return ber(0x02, v.to_bytes(max(1, (v.bit_length() + 8) // 8), "big", signed=True))


def ber_oid(oid):
    parts = [int(x) for x in oid.strip(".").split(".")]
    out = bytes([parts[0] * 40 + parts[1]])
    for p in parts[2:]:
        chunk = [p & 0x7F]
        p >>= 7
        while p:
            chunk.append(0x80 | (p & 0x7F))
            p >>= 7
        out += bytes(reversed(chunk))
    return ber(0x06, out)


def ber_read(data, i):
    tag = data[i]
    n = data[i + 1]
    i += 2
    if n & 0x80:
        k = n & 0x7F
        n = int.from_bytes(data[i:i + k], "big")
        i += k
    return tag, data[i:i + n], i + n


def oid_str(b):
    out = [b[0] // 40, b[0] % 40]
    v = 0
    for x in b[1:]:
        v = (v << 7) | (x & 0x7F)
        if not x & 0x80:
            out.append(v)
            v = 0
    return ".".join(map(str, out))


def snmp_value(tag, b):
    if tag == 0x02:
        return int.from_bytes(b, "big", signed=True)
    if tag in (0x41, 0x42, 0x43, 0x46):
        return int.from_bytes(b, "big")
    if tag == 0x06:
        return oid_str(b)
    if tag == 0x40:
        return ".".join(str(x) for x in b)
    if tag == 0x04:
        try:
            s = b.decode()
            if s.isprintable() or not s:
                return s
        except UnicodeDecodeError:
            pass
        return ":".join(f"{x:02x}" for x in b)
    return None  # NULL, noSuchObject, noSuchInstance, endOfMibView


def snmp_packet(community, pdu_tag, reqid, oid):
    vb = ber(0x30, ber(0x30, ber_oid(oid) + ber(0x05, b"")))
    pdu = ber(pdu_tag, ber_int(reqid) + ber_int(0) + ber_int(0) + vb)
    return ber(0x30, ber_int(1) + ber(0x04, community.encode()) + pdu)


def snmp_parse(data):
    """Ответ: (reqid, error_status, [(oid, tag, value)])."""
    _, msg, _ = ber_read(data, 0)
    i = 0
    _, _, i = ber_read(msg, i)  # version
    _, _, i = ber_read(msg, i)  # community
    _, pdu, _ = ber_read(msg, i)
    j = 0
    _, rid, j = ber_read(pdu, j)
    _, es, j = ber_read(pdu, j)
    _, _, j = ber_read(pdu, j)
    _, vbl, _ = ber_read(pdu, j)
    out, k = [], 0
    while k < len(vbl):
        _, vb, k = ber_read(vbl, k)
        _, ob, m = ber_read(vb, 0)
        vt, vv, _ = ber_read(vb, m)
        out.append((oid_str(ob), vt, snmp_value(vt, vv)))
    return int.from_bytes(rid, "big", signed=True), int.from_bytes(es, "big"), out


class SNMPLink:
    def __init__(self, c, conn):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.host, self.port = split_addr(self.addr, 161)
        self.community = str(conn.get("password") or conn.get("community") or "public")
        self.rid = int(time.time()) & 0x7FFFFF
        self.seen = ""

    def __enter__(self):
        self.sock = socket.socket(socket.AF_INET6 if ":" in self.host else socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.settimeout(3)
        return self

    def __exit__(self, *a):
        self.sock.close()

    def _ask(self, tag, oid):
        self.rid += 1
        pkt = snmp_packet(self.community, tag, self.rid, oid)
        for _ in range(2):
            try:
                self.sock.sendto(pkt, (self.host, self.port))
                while True:
                    data, _ = self.sock.recvfrom(65535)
                    rid, _, vbs = snmp_parse(data)
                    if rid == self.rid:
                        return vbs
            except socket.timeout:
                continue
            except OSError as e:
                raise self.c.fail("unreachable", addr=self.addr, err=type(e).__name__)
        raise self.c.fail("snmp_timeout", addr=self.addr)

    def get(self, oid):
        vbs = self._ask(0xA0, oid)
        return vbs[0][2] if vbs else None

    def walk(self, oid):
        prefix, cur, out = oid.strip(".") + ".", oid.strip("."), []
        for _ in range(4000):
            vbs = self._ask(0xA1, cur)
            if not vbs:
                break
            o, tag, v = vbs[0]
            if tag == 0x82 or not o.startswith(prefix):
                break
            out.append((o[len(prefix):], v))
            cur = o
        return out


# --- язык профилей ------------------------------------------------------------------

def truthy(v):
    return v is True or v == 1 or str(v).strip().lower() in ("true", "yes", "up", "1", "running", "active", "on", "enabled")


def path_get(v, path):
    """«a.b.0.c»; «a[].b» — из каждого элемента списка."""
    if path in ("", None):
        return v
    head, _, rest = path.partition(".")
    if head.endswith("[]"):
        lst = path_get(v, head[:-2])
        return [path_get(x, rest) for x in lst] if isinstance(lst, list) else []
    if isinstance(v, dict):
        v = v.get(head)
    elif isinstance(v, list) and head.isdigit():
        v = v[int(head)] if int(head) < len(v) else None
    else:
        return None
    return path_get(v, rest) if rest else v


def to_num(v):
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        return float(v)
    m = re.search(r"-?\d+(?:[.,]\d+)?", str(v or ""))
    return float(m.group(0).replace(",", ".")) if m else None


def step(v, s):
    k, a = next(iter(s.items()))
    if k == "json":
        if isinstance(v, str):
            v = json.loads(v) if v.strip() else None
        return path_get(v, a)
    if k == "kv":
        out = {}
        for line in str(v or "").splitlines():
            if a in line:
                key, _, val = line.partition(a)
                out[key.strip()] = val.strip().strip('"').strip("'")
        return out
    if k == "key":
        return v.get(a) if isinstance(v, dict) else None
    if k == "regex":
        m = re.search(a, str(v or ""), re.M)
        if not m:
            return None
        if m.groupdict():
            return m.groupdict()
        return m.group(1) if m.groups() else m.group(0)
    if k == "each":
        r = re.compile(a)
        return [m.groupdict() for m in (r.search(l) for l in str(v or "").splitlines()) if m]
    if k == "lines":
        return [l for l in str(v or "").splitlines()[int(a or 0):] if l.strip()]
    if k == "cols":
        rows = v if isinstance(v, list) else [l for l in str(v or "").splitlines() if l.strip()]
        return [dict(zip(a, str(l).split(None, len(a) - 1))) for l in rows]
    if k == "num":
        n = to_num(v)
        return None if n is None else n * float(a)
    if k == "rev":
        n = to_num(v)
        return None if n is None else float(a) - n
    if k == "pick":
        rows = v if isinstance(v, list) else []
        return [{dst: path_get(r, src) for dst, src in a.items()} for r in rows]
    if k == "items":
        if not isinstance(v, dict):
            return []
        return [dict(x, key=key) if isinstance(x, dict) else {"key": key, "value": x} for key, x in v.items()]
    if k == "filter":
        rows = v if isinstance(v, list) else []
        return [r for r in rows if test_cond(path_get(r, a["field"]), a)]
    if k == "pct":
        num, den, dst = a
        for r in v if isinstance(v, list) else []:
            n, d = to_num(r.get(num)), to_num(r.get(den))
            r[dst] = round(100 * n / d) if n is not None and d and d > 0 and n >= 0 else None
        return v
    if k == "join":
        return a.join(str(x) for x in v) if isinstance(v, list) else v
    if k == "count":
        return len(v) if isinstance(v, list) else 0
    if k == "first":
        return v[0] if isinstance(v, list) and v else None
    if k == "trim":
        return str(v or "").strip()
    if k == "mask":
        # Секреты из выгрузки — не в историю конфигураций хаба: группа «v»
        # совпадения заменяется на ***.
        r = re.compile(a, re.M)
        return r.sub(lambda m: m.group(0)[:m.start("v") - m.start(0)] + "***" + m.group(0)[m.end("v") - m.start(0):], str(v or ""))
    if k == "xml":
        return path_get(xml_obj(v), a) if str(v or "").strip() else None
    if k == "list":
        return v if isinstance(v, list) else [] if v in (None, "") else [v]
    if k == "map":
        return a.get(str(v).strip(), v) if v is not None else None
    if k == "dahua":
        return dahua_disks(v)
    raise ValueError(f"unknown step {k}")


def test_cond(v, cond):
    for k, a in cond.items():
        if k == "field":
            continue
        if k in ("gt", "lt", "ge", "le"):
            n = to_num(v)
            if n is None or not {"gt": n > a, "lt": n < a, "ge": n >= a, "le": n <= a}[k]:
                return False
        elif k == "eq" and str(v if v is not None else "").strip() != str(a):
            return False
        elif k == "ne" and str(v if v is not None else "").strip() == str(a):
            return False
        elif k == "contains" and str(a).lower() not in str(v or "").lower():
            return False
        elif k == "empty" and bool(v) == bool(a):
            return False
        elif k == "nonempty" and bool(v) != bool(a):
            return False
        elif k == "prefix" and not str(v or "").startswith(str(a)):
            return False
        elif k == "in" and str(v if v is not None else "").strip() not in [str(x) for x in a]:
            return False
        elif k == "nin" and (v in (None, "") or str(v).strip() in [str(x) for x in a]):
            return False
        elif k == "word" and not re.search(r"(?<![A-Za-z0-9])" + re.escape(str(a)) + r"(?![A-Za-z0-9])", str(v or "")):
            return False
    return True


class Eval:
    """Значения профиля для одного вызова: ответы — по разу на источник."""

    def __init__(self, link, profile, conn, c=None):
        self.link, self.profile, self.conn, self.cache, self.c = link, profile, conn, {}, c

    def raw(self, spec):
        if "const" in spec:
            return spec["const"]
        if "conn" in spec:
            return self.conn.get(spec["conn"])
        key = json.dumps({k: spec[k] for k in ("cmd", "get", "oid", "table", "nut", "onvif") if k in spec}, sort_keys=True)
        if key in self.cache:
            return self.cache[key]
        if "cmd" in spec:
            v = self.link.run(spec["cmd"])
        elif "get" in spec:
            v = self.link.call("GET", spec["get"])
        elif "oid" in spec:
            v = self.link.get(spec["oid"])
        elif "nut" in spec:
            v = self.link.nut(spec["nut"])
        elif "onvif" in spec:
            v = self.link.onvif(spec["onvif"])
        elif "table" in spec:
            cols, rows = spec["table"], {}
            for col, oid in cols.items():
                for idx, val in self.link.walk(oid):
                    rows.setdefault(idx, {"index": idx})[col] = val
            v = [rows[k] for k in sorted(rows, key=lambda s: [int(x) if x.isdigit() else x for x in s.split(".")])]
        else:
            raise ValueError("no source (cmd, get, oid, table, nut, onvif, const, conn, any, fields)")
        self.cache[key] = v
        return v

    def value(self, spec, default=None):
        if spec is None:
            return default
        if not isinstance(spec, dict):
            return spec
        if "any" in spec:  # первое непустое (UPS-MIB или APC PowerNet)
            for alt in spec["any"]:
                v = self.value(alt)
                if v not in (None, "", [], {}):
                    return v
            return default
        if "fields" in spec:  # таблица «параметр — значение» из разных источников
            rows = []
            for title, alt in spec["fields"]:
                v = self.value(alt)
                if v not in (None, ""):
                    rows.append({"name": self.c.txt(title) if self.c else str(title), "value": int(v) if isinstance(v, float) and v.is_integer() else v})
            return rows
        try:
            v = self.raw(spec)
            for s in spec.get("parse") or []:
                v = step(v, s)
        except Fail:
            # optional — API, которого у устройства может не быть (ISAPI у
            # Hikvision, CGI у Dahua): нет ответа — поле пустое, а не ошибка.
            if spec.get("optional"):
                return default
            raise
        except Exception:  # профиль не подошёл к этому устройству — поле пустое
            return default
        return default if v is None else v


def as_rows(v):
    return [r for r in v if isinstance(r, dict)] if isinstance(v, list) else []


def addrs_of(v):
    if isinstance(v, list):
        return [str(x) for x in v if x]
    return [a.strip() for a in str(v or "").replace(";", ",").split(",") if a.strip() and a.strip() != "-"]


def num_or(v):
    n = to_num(v)
    return int(n) if n is not None else None


def info_of(c, ev, p):
    i = p.get("info") or {}
    out = {k: str(ev.value(i.get(k), "") or "").strip() for k in ("name", "os", "version", "model", "serial", "arch", "firmware")}
    out["uptime_s"] = num_or(ev.value(i.get("uptime_s"))) or 0
    out["address"] = ev.conn.get("address", "")
    out["fingerprint"] = getattr(ev.link, "seen", "")
    if not out["name"]:
        out["name"] = re.sub(r"[^a-z0-9.-]", "-", str(ev.conn.get("address", "")).split(":")[0].lower())
    return {k: v for k, v in out.items() if v not in ("", None)}


def snapshot(c, ev, p):
    s = p.get("snapshot") or {}
    out = {"info": info_of(c, ev, p)}
    if s.get("mem_total"):
        out["mem_total"] = num_or(ev.value(s["mem_total"])) or 0
    if s.get("cpu"):
        out["cpu"] = {"model": str(ev.value(s["cpu"].get("model"), "")), "cores": num_or(ev.value(s["cpu"].get("cores"))) or 0}
    if s.get("interfaces"):
        out["interfaces"] = [{"name": str(r.get("name", "")), "type": str(r.get("type") or ""), "mac": str(r.get("mac") or ""),
                              "addrs": addrs_of(r.get("addrs")), "up": truthy(r.get("up")), "comment": str(r.get("comment") or "")}
                             for r in as_rows(ev.value(s["interfaces"], [])) if r.get("name")]
    if s.get("services"):
        out["services"] = [{"name": str(r.get("name", "")), "state": str(r.get("state") or ""), "enabled": truthy(r.get("enabled", r.get("state")))}
                           for r in as_rows(ev.value(s["services"], [])) if r.get("name")]
    if s.get("packages"):
        out["packages"] = [{"name": str(r.get("name", "")), "version": str(r.get("version") or "")} for r in as_rows(ev.value(s["packages"], [])) if r.get("name")]
    if s.get("ports"):
        out["ports"] = [{"proto": str(r.get("proto") or "tcp").lower(), "port": num_or(r.get("port")) or 0, "service": str(r.get("service") or ""),
                         "allowed": "" if str(r.get("allowed") or "") in ("*", "0.0.0.0", "[::]", "::", "*:*") else str(r.get("allowed") or "")}
                        for r in as_rows(ev.value(s["ports"], [])) if num_or(r.get("port"))]
    if s.get("users"):
        out["users"] = [{"name": str(r.get("name", "")), "group": str(r.get("group") or "")} for r in as_rows(ev.value(s["users"], [])) if r.get("name")]
    if s.get("neighbors"):
        out["neighbors"] = [{k: str(r.get(k) or "") for k in ("name", "address", "mac", "interface", "platform")} for r in as_rows(ev.value(s["neighbors"], []))]
    if s.get("disks"):
        out["disks"] = [{"name": str(r.get("name", "")), "size": num_or(r.get("size")) or 0, "used": num_or(r.get("used")) or 0}
                        for r in as_rows(ev.value(s["disks"], [])) if r.get("name")]
    extra = []
    for x in p.get("extra") or []:
        rows = as_rows(ev.value(x.get("rows"), []))
        if rows:
            extra.append({"type": "table", "title": c.txt(x.get("title")), "columns": [{"key": k, "title": c.txt(t)} for k, t in x.get("columns", [])],
                          "rows": [{"cells": {k: "" if r.get(k) is None else str(r.get(k)) for k, _ in x.get("columns", [])}} for r in rows]})
    out["extra"] = extra
    return out


def metrics(c, ev, p):
    m = p.get("metrics") or {}
    out = {}
    for k in ("cpu_pct", "temp_c"):
        if m.get(k):
            v = to_num(ev.value(m[k]))
            if v is not None:
                out[k] = round(v, 1)
    for k in ("mem_used", "mem_total", "disk_used", "disk_size", "uptime_s"):
        if m.get(k):
            v = num_or(ev.value(m[k]))
            if v is not None:
                out[k] = v
    if m.get("net"):
        out["net"] = {str(r.get("name")): {"rx": num_or(r.get("rx")) or 0, "tx": num_or(r.get("tx")) or 0}
                      for r in as_rows(ev.value(m["net"], [])) if r.get("name")}
    return out


def fmt(c, tmpl, row):
    s = c.txt(tmpl)
    for k, v in (row or {}).items():
        s = s.replace("{" + k + "}", str(v))
    return s


def findings(c, ev, p):
    out = []
    for f in p.get("findings") or []:
        cond = f.get("if") or {"nonempty": True}
        v = ev.value(f.get("value"))
        rows = as_rows(v) if isinstance(v, list) else None
        if rows is not None and "field" in cond:
            for r in rows:
                if test_cond(path_get(r, cond["field"]), cond):
                    out.append({"id": f"{f['id']}-{r.get('name') or r.get('index') or len(out)}", "severity": f.get("severity", "warning"),
                                "title": fmt(c, f.get("title"), r), "detail": fmt(c, f.get("detail"), r), "fix": fmt(c, f.get("fix"), r)})
            continue
        if test_cond(v, cond):
            row = {"value": "" if v is None else (int(v) if isinstance(v, float) and v.is_integer() else v)}
            out.append({"id": f["id"], "severity": f.get("severity", "warning"), "title": fmt(c, f.get("title"), row),
                        "detail": fmt(c, f.get("detail"), row), "fix": fmt(c, f.get("fix"), row)})
    return [{k: v for k, v in x.items() if v not in ("", None)} for x in out]


VOLATILE = re.compile(r"^#.*(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}|\d{2}:\d{2}:\d{2}).*$")


def config(c, ev, p):
    v = ev.value(p.get("config"), "")
    if isinstance(v, (dict, list)):
        v = json.dumps(v, ensure_ascii=False, indent=2, sort_keys=True)
    return "\n".join(l for l in str(v).replace("\r\n", "\n").split("\n") if not VOLATILE.match(l)).strip() + "\n"


ARG_RE = re.compile(r"^[A-Za-z0-9._@/:+-]{1,200}$")


def action(c, ev, p, name, args, dry):
    a = next((x for x in p.get("actions") or [] if x.get("op") == name), None)
    if not a:
        raise c.fail("no_action", action=name)
    vals = {}
    for f in a.get("fields") or []:
        v = str((args or {}).get(f["name"], "")).strip()
        if f.get("required") and not v:
            raise c.fail("no_action", action=f"{name}: {f['name']}")
        if v and not ARG_RE.match(v):
            raise c.fail("no_action", action=f"{name}: {f['name']}")
        vals[f["name"]] = v
    title = c.txt(a.get("title")) or name
    if dry:
        return c.t("would", what=title)
    if "cmd" in a:
        cmd = a["cmd"]
        for k, v in vals.items():
            cmd = cmd.replace("{" + k + "}", shlex.quote(v))
        try:
            return ev.link.run(cmd).strip() or title
        except Fail:
            if a.get("disconnects"):
                return title  # перезагрузка рвёт соединение — это и ожидалось
            raise
    if "instcmd" in a:
        return ev.link.instcmd(a["instcmd"]) and title
    if "onvif" in a:
        try:
            return ev.link.onvif_call(a["onvif"]) or title
        except Fail:
            if a.get("disconnects"):
                return title
            raise
    if "post" in a:
        body = json.loads(fmt(c, json.dumps(a.get("body", {})), vals))
        try:
            res = ev.link.call("POST", a["post"], body)
        except Fail:
            if a.get("disconnects"):
                return title
            raise
        return title if res in ("", None, {}, []) else json.dumps(res, ensure_ascii=False)[:2000]
    raise c.fail("no_action", action=name)


def exec_cmd(c, ev, p, cmd, check, dry):
    cmd = str(cmd or "").replace("\r\n", "\n").strip()
    if not cmd:
        raise c.fail("exec_empty")
    lang = exec_lang(p)
    if not lang:
        raise c.fail("no_exec", id=p["id"])
    if check:
        if lang == "sh" and shutil.which("sh"):
            r = subprocess.run(["sh", "-n", "-c", cmd], capture_output=True, text=True, timeout=10)
            if r.returncode != 0:
                raise c.fail("exec_syntax", err=(r.stderr.strip() or "?")[:300])
        return c.t("check_ok")
    if dry:
        return c.t("would", what=c.t("would_exec", addr=ev.conn.get("address"), cmd=cmd))
    if lang == "sh":
        return ev.link.run(cmd, timeout=300)
    ex = p["exec"]
    res = ev.link.call("POST", ex["post"], json.loads(json.dumps(ex.get("body", [])).replace('"{command}"', json.dumps(cmd))))
    return res if isinstance(res, str) else json.dumps(res, ensure_ascii=False, indent=2)


def exec_lang(p):
    ex = p.get("exec")
    if p.get("transport") == "ssh":
        return "sh" if ex in (None, True, "sh") else ""
    return ex.get("lang", "") if isinstance(ex, dict) else ""


# --- профили: встроенные и свои ------------------------------------------------------

def C(cmd, *parse):
    return {"cmd": cmd, "parse": list(parse)}


NET_DEV = C("cat /proc/net/dev", {"each": r"^\s*(?P<name>[^:\s]+):\s*(?P<rx>\d+)(?:\s+\d+){7}\s+(?P<tx>\d+)"})
DF_ROOT_USED = C("df -kP / | awk 'NR==2{print $3*1024}'", {"num": 1})
DF_ROOT_SIZE = C("df -kP / | awk 'NR==2{print $2*1024}'", {"num": 1})
BOOT_UPTIME = C("echo $(( $(date +%s) - $(sysctl -n kern.boottime | sed 's/.*sec = \\([0-9]*\\).*/\\1/') ))", {"num": 1})
IFLIST_BSD = C("for i in $(ifconfig -l); do s=$(ifconfig $i | awk '/status:/{print $2; exit}'); m=$(ifconfig $i | awk '/ether /{print $2; exit}'); "
               "a=$(ifconfig $i | awk '/inet /{print $2}' | paste -sd, -); echo \"$i ${s:-active} ${m:--} ${a:--}\"; done",
               {"cols": ["name", "up", "mac", "addrs"]})
REBOOT = {"op": "reboot", "title": {"ru": "Перезагрузить", "en": "Reboot"}, "danger": True, "disconnects": True,
          "confirm": {"ru": "Перезагрузить устройство? Связь пропадёт на время загрузки.", "en": "Reboot the device? The link drops while it boots."}}

# OpenWrt: разбор uci короткими циклами sh (busybox ash) — строки «a|b|c».
OWRT_PKGS = r"""if command -v apk >/dev/null; then apk info -v 2>/dev/null | sed -n 's/^\(.*\)-\([0-9][^-]*-r[0-9]*\)$/\1 \2/p'; else opkg list-installed 2>/dev/null | awk '{print $1, $3}'; fi"""
OWRT_UPGRADABLE = r"""if command -v apk >/dev/null; then apk list --upgradable 2>/dev/null | awk '{print $1, "-"}'; else opkg list-upgradable 2>/dev/null | awk '{print $1, $5}'; fi"""
OWRT_WIFI = r"""for s in $(uci -q show wireless | sed -n 's/^wireless\.\([^.=]*\)=wifi-iface$/\1/p'); do g(){ uci -q get "wireless.$s.$1"; }; st=on; [ "$(g disabled)" = 1 ] && st=off; echo "$s|$(g ssid)|$(g mode)|$(g encryption)|$(g device)|$(g network)|$st"; done"""
OWRT_CLIENTS = r"""for i in $(iwinfo 2>/dev/null | awk '/ESSID/{print $1}'); do iwinfo "$i" assoclist 2>/dev/null | awk -v i="$i" '/dBm/{print i, $1, $2}'; done"""
OWRT_STATIC = r"""for s in $(uci -q show dhcp | sed -n 's/^dhcp\.\([^.=]*\)=host$/\1/p'); do g(){ uci -q get "dhcp.$s.$1"; }; echo "$(g name)|$(g mac)|$(g ip)"; done"""
OWRT_ZONES = r"""for s in $(uci -q show firewall | sed -n 's/^firewall\.\([^.=]*\)=zone$/\1/p'); do g(){ uci -q get "firewall.$s.$1"; }; echo "$(g name)|$(g input)|$(g forward)|$(g masq)|$(g network)"; done"""
OWRT_WAN_INPUT = r"""for s in $(uci -q show firewall | sed -n 's/^firewall\.\([^.=]*\)=zone$/\1/p'); do [ "$(uci -q get firewall.$s.name)" = wan ] && uci -q get firewall.$s.input; done; true"""
OWRT_REDIRECTS = r"""for s in $(uci -q show firewall | sed -n 's/^firewall\.\([^.=]*\)=redirect$/\1/p'); do g(){ uci -q get "firewall.$s.$1"; }; [ "$(g target)" = SNAT ] && continue; e=1; [ "$(g enabled)" = 0 ] && e=0; echo "$(g name)|$(g proto)|$(g src_dport)|$(g dest_ip)|$(g dest_port)|$e"; done"""
OWRT_WAN_ADMIN = r"""for s in $(uci -q show firewall | sed -n 's/^firewall\.\([^.=]*\)=rule$/\1/p'); do g(){ uci -q get "firewall.$s.$1"; }; [ "$(g src)" = wan ] && [ -z "$(g dest)" ] && [ "$(g target)" = ACCEPT ] && [ "$(g enabled)" != 0 ] || continue; for p in $(g dest_port); do case "$p" in 22|23|80|443|8080|8443) echo "$(g name)|$p";; esac; done; done"""
OWRT_WIFI_ISSUES = r"""for s in $(uci -q show wireless | sed -n 's/^wireless\.\([^.=]*\)=wifi-iface$/\1/p'); do g(){ uci -q get "wireless.$s.$1"; }; [ "$(g disabled)" = 1 ] && continue; [ "$(g mode)" = ap ] || continue; n=$(g ssid); e=$(g encryption); case "$e" in ""|none) echo "open|$n";; wep*) echo "wep|$n";; psk|psk+*|psk-mixed*|wpa|wpa+*|wpa-mixed*) echo "weak|$n";; esac; case "$e" in *tkip*) echo "tkip|$n";; esac; [ "$(g wps_pushbutton)" = 1 ] && echo "wps|$n"; done; true"""
# Секреты в «uci export»: значение — группа «v».
OWRT_SECRETS = r"^\s*(?:option|list)\s+(?:key[1-4]?|password|psk|sae_password|wpa_psk|private_key|preshared_key|auth_secret|acct_secret|secret|pass|passwd|token|priv_key|wep_key[1-4]?)\s+'(?P<v>[^']*)'"

# ИБП: UPS-MIB (RFC 1628) и APC PowerNet — что ответит.
UPS = "1.3.6.1.2.1.33.1."
APC = "1.3.6.1.4.1.318.1.1.1."
UPS_TEMP = {"any": [{"oid": UPS + "2.7.0"}, {"oid": APC + "2.2.2.0"}]}
UPS_LOAD = {"any": [{"oid": UPS + "4.4.1.5.1"}, {"oid": APC + "4.2.3.0"}]}


def NV(key):
    """Переменная NUT (upsc)."""
    return {"nut": "vars", "parse": [{"key": key}]}


HIK_HDD = {"get": "/ISAPI/ContentMgmt/Storage", "optional": True, "parse": [{"xml": "storage.hddList.hdd"}, {"list": 1},
                                                          {"pick": {"name": "id", "status": "status", "capacity": "capacity", "free": "freeSpace", "label": "hddName"}}]}
DAHUA_HDD = {"get": "/cgi-bin/storageDevice.cgi?action=getDeviceAllInfo", "optional": True, "parse": [{"dahua": 1}]}


BUILTIN = {
    "linux-ssh": {
        "id": "linux-ssh", "title": {"ru": "Linux без агента (SSH)", "en": "Agentless Linux (SSH)"}, "transport": "ssh",
        "hint": {"ru": "Старые или закрытые Linux, куда нельзя поставить nkt: только SSH и стандартные утилиты (ip, ss, df, systemctl).",
                 "en": "Old or locked-down Linux where nkt can't be installed: SSH and standard tools only (ip, ss, df, systemctl)."},
        "info": {"name": C("hostname", {"trim": 1}), "os": C("cat /etc/os-release", {"kv": "="}, {"key": "NAME"}),
                 "version": C("cat /etc/os-release", {"kv": "="}, {"key": "VERSION_ID"}), "arch": C("uname -m", {"trim": 1}),
                 "uptime_s": C("cat /proc/uptime", {"num": 1})},
        "snapshot": {
            "mem_total": C("awk '/MemTotal/{print $2*1024}' /proc/meminfo", {"num": 1}),
            "cpu": {"model": C("awk -F: '/model name/{print $2; exit}' /proc/cpuinfo", {"trim": 1}), "cores": C("nproc", {"num": 1})},
            "interfaces": C("ip -j addr", {"json": ""}, {"pick": {"name": "ifname", "mac": "address", "up": "operstate", "addrs": "addr_info[].local", "type": "link_type"}}),
            "services": C("systemctl list-units --type=service --all --no-legend --plain", {"each": r"^(?P<name>\S+)\.service\s+\S+\s+(?P<enabled>\S+)\s+(?P<state>\S+)"}),
            "packages": C("dpkg-query -W -f '${Package} ${Version}\\n' 2>/dev/null || rpm -qa --qf '%{NAME} %{VERSION}\\n' 2>/dev/null", {"cols": ["name", "version"]}),
            "ports": C("ss -Hltnu", {"each": r"^(?P<proto>\S+)\s+\S+\s+\d+\s+\d+\s+(?P<allowed>\S*):(?P<port>\d+)\s"}),
            "users": C("getent passwd", {"each": r"^(?P<name>[^:]+):[^:]*:(?P<uid>\d+):"}, {"filter": {"field": "uid", "ge": 1000, "lt": 65534}}),
        },
        "extra": [{"title": {"ru": "Диски", "en": "Disks"}, "columns": [["mount", {"ru": "Точка", "en": "Mount"}], ["size", {"ru": "Размер", "en": "Size"}],
                                                                       ["used", {"ru": "Занято", "en": "Used"}], ["pct", "%"]],
                   "rows": C("df -hP -x tmpfs -x devtmpfs -x overlay 2>/dev/null || df -hP", {"lines": 1}, {"cols": ["fs", "size", "used", "avail", "pct", "mount"]})}],
        "metrics": {"cpu_pct": C("top -bn1 | head -5", {"regex": r"(\d+[.,]\d+)\s*id"}, {"rev": 100}),
                    "mem_used": C("awk '/MemTotal/{t=$2}/MemAvailable/{a=$2}END{print (t-a)*1024}' /proc/meminfo", {"num": 1}),
                    "mem_total": C("awk '/MemTotal/{print $2*1024}' /proc/meminfo", {"num": 1}),
                    "disk_used": DF_ROOT_USED, "disk_size": DF_ROOT_SIZE,
                    "temp_c": C("cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null", {"num": 0.001}),
                    "uptime_s": C("cat /proc/uptime", {"num": 1}), "net": NET_DEV},
        "findings": [
            {"id": "reboot-required", "severity": "warning", "value": C("test -f /var/run/reboot-required && echo yes || echo no", {"trim": 1}), "if": {"eq": "yes"},
             "title": {"ru": "Нужна перезагрузка после обновлений", "en": "A reboot is needed after updates"}},
            {"id": "updates", "severity": "warning", "value": C("apt list --upgradable 2>/dev/null | grep -c upgradable || dnf -q check-update 2>/dev/null | grep -c . || true", {"num": 1}),
             "if": {"gt": 0}, "title": {"ru": "Ожидают обновления: {value}", "en": "Updates pending: {value}"}},
            {"id": "disk-root", "severity": "warning", "value": C("df -P / | awk 'NR==2{print $5+0}'", {"num": 1}), "if": {"gt": 90},
             "title": {"ru": "Корневой диск заполнен на {value}%", "en": "The root disk is {value}% full"}},
            {"id": "ssh-root", "severity": "warning", "value": C("sshd -T 2>/dev/null | awk '/^permitrootlogin/{print $2}'", {"trim": 1}), "if": {"eq": "yes"},
             "title": {"ru": "SSH пускает root по паролю", "en": "SSH lets root in with a password"},
             "fix": {"ru": "PermitRootLogin prohibit-password в /etc/ssh/sshd_config", "en": "PermitRootLogin prohibit-password in /etc/ssh/sshd_config"}},
        ],
        "config": C("for f in /etc/hostname /etc/hosts /etc/fstab /etc/ssh/sshd_config; do echo \"### $f\"; cat $f 2>/dev/null; done"),
        "actions": [REBOOT | {"cmd": "sudo -n reboot || reboot"},
                    {"op": "service_restart", "title": {"ru": "Перезапустить службу", "en": "Restart a service"},
                     "fields": [{"name": "name", "title": {"ru": "Служба", "en": "Service"}, "required": True}],
                     "cmd": "sudo -n systemctl restart {name} || systemctl restart {name}"}],
    },
    "macos": {
        "id": "macos", "title": "macOS", "transport": "ssh", "icon": "macos",
        "hint": {"ru": "Системные настройки → Общий доступ → Удалённый вход (SSH). Часть сведений (FileVault, обновления) — без sudo.",
                 "en": "System Settings → Sharing → Remote Login (SSH). Some data (FileVault, updates) — without sudo."},
        "info": {"name": C("scutil --get ComputerName 2>/dev/null || hostname", {"trim": 1}), "os": {"const": "macOS"},
                 "version": C("sw_vers -productVersion", {"trim": 1}), "model": C("sysctl -n hw.model", {"trim": 1}),
                 "arch": C("uname -m", {"trim": 1}), "uptime_s": BOOT_UPTIME},
        "snapshot": {
            "mem_total": C("sysctl -n hw.memsize", {"num": 1}),
            "cpu": {"model": C("sysctl -n machdep.cpu.brand_string", {"trim": 1}), "cores": C("sysctl -n hw.ncpu", {"num": 1})},
            "interfaces": IFLIST_BSD,
            "services": C("launchctl list | awk 'NR>1{print $3, ($1==\"-\"?\"stopped\":\"running\")}'", {"cols": ["name", "state"]}),
            "packages": C("brew list --versions 2>/dev/null", {"cols": ["name", "version"]}),
            "ports": C("lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk 'NR>1{print $9, $1}' | sed 's/:\\([0-9]*\\) / \\1 /' | sort -u", {"cols": ["allowed", "port", "service"]}),
            "users": C("dscl . list /Users UniqueID | awk '$2>=500{print $1}'", {"cols": ["name"]}),
        },
        "metrics": {"cpu_pct": C("top -l 1 -n 0 | awk '/CPU usage/{gsub(\"%\",\"\",$7); print 100-$7}'", {"num": 1}),
                    "mem_used": C("vm_stat | awk '/page size of/{p=$8}/Pages active/{a=$3}/Pages wired/{w=$4}/occupied by compressor/{c=$5}END{print (a+w+c)*p}'", {"num": 1}),
                    "mem_total": C("sysctl -n hw.memsize", {"num": 1}), "disk_used": DF_ROOT_USED, "disk_size": DF_ROOT_SIZE, "uptime_s": BOOT_UPTIME,
                    "net": C("netstat -ibn | awk '$3 ~ /Link/ {print $1, $7, $10}' | sort -u -k1,1", {"cols": ["name", "rx", "tx"]})},
        "findings": [
            {"id": "firewall", "severity": "warning", "value": C("/usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate"), "if": {"contains": "disabled"},
             "title": {"ru": "Брандмауэр macOS выключен", "en": "The macOS firewall is off"}},
            {"id": "filevault", "severity": "warning", "value": C("fdesetup status"), "if": {"contains": "Off"},
             "title": {"ru": "FileVault выключен — диск не зашифрован", "en": "FileVault is off — the disk isn't encrypted"}},
            {"id": "sip", "severity": "critical", "value": C("csrutil status"), "if": {"contains": "disabled"},
             "title": {"ru": "Защита целостности системы (SIP) выключена", "en": "System Integrity Protection (SIP) is off"}},
            {"id": "updates", "severity": "warning", "value": C("softwareupdate -l 2>&1 | grep -c '^\\* '", {"num": 1}), "if": {"gt": 0},
             "title": {"ru": "Ожидают обновления: {value}", "en": "Updates pending: {value}"}},
        ],
        "config": C("echo '### firewall'; /usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate --getstealthmode --listapps 2>/dev/null; echo '### /etc/hosts'; cat /etc/hosts"),
        "actions": [REBOOT | {"cmd": "sudo -n shutdown -r now"}],
    },
    "freebsd": {
        "id": "freebsd", "title": "FreeBSD", "transport": "ssh", "icon": "freebsd",
        "hint": {"ru": "sshd включён (sysrc sshd_enable=YES); pkg audit — после pkg audit -F.", "en": "sshd on (sysrc sshd_enable=YES); pkg audit — after pkg audit -F."},
        "info": {"name": C("hostname", {"trim": 1}), "os": {"const": "FreeBSD"}, "version": C("freebsd-version", {"trim": 1}),
                 "model": C("sysctl -n hw.model", {"trim": 1}), "arch": C("uname -m", {"trim": 1}), "uptime_s": BOOT_UPTIME},
        "snapshot": {
            "mem_total": C("sysctl -n hw.physmem", {"num": 1}), "interfaces": IFLIST_BSD,
            "services": C("service -e | xargs -n1 basename | awk '{print $1, \"enabled\"}'", {"cols": ["name", "state"]}),
            "packages": C("pkg info -q 2>/dev/null | sed 's/-\\([^-]*\\)$/ \\1/'", {"cols": ["name", "version"]}),
            "ports": C("sockstat -46l | awk 'NR>1{print $5, $6}' | sed 's/:\\([0-9*]*\\)$/ \\1/'", {"cols": ["proto", "allowed", "port"]}),
            "users": C("awk -F: '$3>=1000 && $3<65534{print $1}' /etc/passwd", {"cols": ["name"]}),
        },
        "metrics": {"cpu_pct": C("top -b -d1 | awk -F'[ ,%]+' '/^CPU:/{for(i=1;i<NF;i++) if($(i+1)==\"idle\") {print 100-$i; exit}}'", {"num": 1}),
                    "mem_used": C("echo $(( ($(sysctl -n vm.stats.vm.v_active_count) + $(sysctl -n vm.stats.vm.v_wire_count)) * $(sysctl -n hw.pagesize) ))", {"num": 1}),
                    "mem_total": C("sysctl -n hw.physmem", {"num": 1}), "disk_used": DF_ROOT_USED, "disk_size": DF_ROOT_SIZE, "uptime_s": BOOT_UPTIME,
                    "net": C("netstat -ibn | awk '$3 ~ /Link/ {print $1, $8, $11}' | sort -u -k1,1", {"cols": ["name", "rx", "tx"]})},
        "findings": [
            {"id": "pkg-audit", "severity": "critical", "value": C("pkg audit -q 2>/dev/null | wc -l", {"num": 1}), "if": {"gt": 0},
             "title": {"ru": "Уязвимые пакеты: {value}", "en": "Vulnerable packages: {value}"}, "fix": "pkg upgrade"},
            {"id": "freebsd-update", "severity": "warning", "value": C("freebsd-update updatesready >/dev/null 2>&1 && echo yes || echo no", {"trim": 1}), "if": {"eq": "yes"},
             "title": {"ru": "Скачаны обновления системы — установите", "en": "System updates are downloaded — install them"}, "fix": "freebsd-update install"},
        ],
        "config": C("echo '### /etc/rc.conf'; cat /etc/rc.conf; echo '### /etc/pf.conf'; cat /etc/pf.conf 2>/dev/null"),
        "actions": [REBOOT | {"cmd": "sudo -n shutdown -r now || shutdown -r now"}],
    },
    "openwrt": {
        "id": "openwrt", "title": "OpenWrt", "transport": "ssh", "icon": "openwrt",
        "hint": {"ru": "SSH (dropbear) с паролем root или ключом; ubus и uci — штатные. Ключи Wi-Fi и VPN в конфигурации хаб не хранит — они под ***.",
                 "en": "SSH (dropbear) with the root password or a key; ubus and uci are standard. Wi-Fi and VPN keys are not kept by the hub — the configuration shows them as ***."},
        "info": {"name": C("ubus call system board", {"json": "hostname"}), "os": C("ubus call system board", {"json": "release.distribution"}),
                 "version": C("ubus call system board", {"json": "release.version"}), "model": C("ubus call system board", {"json": "model"}),
                 "arch": C("ubus call system board", {"json": "release.target"}), "uptime_s": C("ubus call system info", {"json": "uptime"})},
        "snapshot": {
            "mem_total": C("ubus call system info", {"json": "memory.total"}),
            "interfaces": C("ubus call network.interface dump", {"json": "interface"}, {"pick": {"name": "interface", "up": "up", "type": "proto", "addrs": "ipv4-address[].address", "comment": "l3_device"}}),
            "services": C("for s in /etc/init.d/*; do n=$(basename $s); $s enabled 2>/dev/null && e=enabled || e=disabled; echo \"$n $e\"; done", {"cols": ["name", "state"]}),
            "packages": C(OWRT_PKGS, {"cols": ["name", "version"]}),
            "ports": C("netstat -lnt 2>/dev/null | awk 'NR>2{print \"tcp\", $4}' | sed 's/:\\([0-9]*\\)$/ \\1/'", {"cols": ["proto", "allowed", "port"]}),
            "neighbors": C("awk 'NR>1{print $1, $4, $6}' /proc/net/arp", {"cols": ["address", "mac", "interface"]}),
        },
        "extra": [
            {"title": {"ru": "Wi-Fi — сети", "en": "Wi-Fi networks"},
             "columns": [["ssid", "SSID"], ["mode", {"ru": "Режим", "en": "Mode"}], ["encryption", {"ru": "Шифрование", "en": "Encryption"}],
                         ["device", {"ru": "Радио", "en": "Radio"}], ["network", {"ru": "Сеть", "en": "Network"}], ["state", {"ru": "Состояние", "en": "State"}]],
             "rows": C(OWRT_WIFI, {"each": r"^(?P<name>[^|]*)\|(?P<ssid>[^|]*)\|(?P<mode>[^|]*)\|(?P<encryption>[^|]*)\|(?P<device>[^|]*)\|(?P<network>[^|]*)\|(?P<state>[^|]*)$"})},
            {"title": {"ru": "Wi-Fi — клиенты", "en": "Wi-Fi clients"},
             "columns": [["iface", {"ru": "Интерфейс", "en": "Interface"}], ["mac", "MAC"], ["signal", {"ru": "Сигнал, дБм", "en": "Signal, dBm"}]],
             "rows": C(OWRT_CLIENTS, {"cols": ["iface", "mac", "signal"]})},
            {"title": {"ru": "DHCP — выданные адреса", "en": "DHCP leases"}, "columns": [["addr", {"ru": "Адрес", "en": "Address"}], ["mac", "MAC"], ["name", {"ru": "Имя", "en": "Name"}]],
             "rows": C("cat /tmp/dhcp.leases 2>/dev/null", {"cols": ["ts", "mac", "addr", "name", "id"]})},
            {"title": {"ru": "DHCP — закреплённые адреса", "en": "DHCP static leases"}, "columns": [["name", {"ru": "Имя", "en": "Name"}], ["mac", "MAC"], ["ip", {"ru": "Адрес", "en": "Address"}]],
             "rows": C(OWRT_STATIC, {"each": r"^(?P<name>[^|]*)\|(?P<mac>[^|]*)\|(?P<ip>[^|]*)$"})},
            {"title": {"ru": "Firewall — зоны", "en": "Firewall zones"},
             "columns": [["name", {"ru": "Зона", "en": "Zone"}], ["input", "input"], ["forward", "forward"], ["masq", "masq"], ["network", {"ru": "Сети", "en": "Networks"}]],
             "rows": C(OWRT_ZONES, {"each": r"^(?P<name>[^|]*)\|(?P<input>[^|]*)\|(?P<forward>[^|]*)\|(?P<masq>[^|]*)\|(?P<network>[^|]*)$"})},
            {"title": {"ru": "Проброс портов", "en": "Port forwards"},
             "columns": [["name", {"ru": "Правило", "en": "Rule"}], ["proto", {"ru": "Протокол", "en": "Protocol"}], ["src_dport", {"ru": "Внешний порт", "en": "External port"}],
                         ["dest_ip", {"ru": "Куда", "en": "To"}], ["dest_port", {"ru": "Порт", "en": "Port"}], ["enabled", {"ru": "Включено", "en": "Enabled"}]],
             "rows": C(OWRT_REDIRECTS, {"each": r"^(?P<name>[^|]*)\|(?P<proto>[^|]*)\|(?P<src_dport>[^|]*)\|(?P<dest_ip>[^|]*)\|(?P<dest_port>[^|]*)\|(?P<enabled>[^|]*)$"})},
            {"title": {"ru": "Пакеты с обновлениями", "en": "Packages with updates"}, "columns": [["name", {"ru": "Пакет", "en": "Package"}], ["version", {"ru": "Новая версия", "en": "New version"}]],
             "rows": C(OWRT_UPGRADABLE, {"cols": ["name", "version"]})},
        ],
        "metrics": {"cpu_pct": C("top -bn1 | awk '/^CPU:/{for(i=1;i<=NF;i++) if($(i+1)==\"idle\") print 100-$i}'", {"num": 1}),
                    "mem_used": C("ubus call system info | jsonfilter -e '@.memory.total' -e '@.memory.available' | awk 'NR==1{t=$1}NR==2{a=$1}END{print t-a}'", {"num": 1}),
                    "mem_total": C("ubus call system info", {"json": "memory.total"}),
                    "disk_used": C("df -kP /overlay 2>/dev/null | awk 'NR==2{print $3*1024}'", {"num": 1}),
                    "disk_size": C("df -kP /overlay 2>/dev/null | awk 'NR==2{print $2*1024}'", {"num": 1}),
                    "temp_c": C("cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null", {"num": 0.001}),
                    "uptime_s": C("ubus call system info", {"json": "uptime"}), "net": NET_DEV},
        "findings": [
            {"id": "root-nopass", "severity": "critical", "value": C("awk -F: '$1==\"root\"{print ($2==\"\"?\"empty\":\"set\")}' /etc/shadow", {"trim": 1}), "if": {"eq": "empty"},
             "title": {"ru": "У root нет пароля", "en": "root has no password"}, "fix": "passwd"},
            {"id": "wan-input", "severity": "critical", "value": C(OWRT_WAN_INPUT, {"trim": 1}), "if": {"eq": "ACCEPT"},
             "title": {"ru": "Зона wan пускает все входящие (input ACCEPT)", "en": "The wan zone accepts all inbound traffic (input ACCEPT)"},
             "fix": {"ru": "Сеть → Firewall → зона wan: input REJECT", "en": "Network → Firewall → the wan zone: input REJECT"}},
            {"id": "wan-admin", "severity": "warning", "value": C(OWRT_WAN_ADMIN, {"each": r"^(?P<name>[^|]*)\|(?P<port>\d+)$"}), "if": {"field": "port", "nonempty": True},
             "title": {"ru": "Из интернета (wan) открыт порт {port} — правило «{name}»", "en": "Port {port} is open from the internet (wan) — rule “{name}”"},
             "fix": {"ru": "SSH и веб-интерфейс роутера — только из LAN или через VPN", "en": "SSH and the router's web UI — from the LAN or over a VPN only"}},
            {"id": "wifi-open", "severity": "critical", "value": C(OWRT_WIFI_ISSUES, {"each": r"^(?P<kind>\w+)\|(?P<name>.*)$"}, {"filter": {"field": "kind", "eq": "open"}}),
             "if": {"field": "name", "nonempty": True}, "title": {"ru": "Открытая сеть Wi-Fi «{name}»", "en": "Open Wi-Fi network “{name}”"},
             "fix": {"ru": "шифрование WPA2-PSK (psk2) или WPA3 (sae)", "en": "WPA2-PSK (psk2) or WPA3 (sae) encryption"}},
            {"id": "wifi-wep", "severity": "critical", "value": C(OWRT_WIFI_ISSUES, {"each": r"^(?P<kind>\w+)\|(?P<name>.*)$"}, {"filter": {"field": "kind", "eq": "wep"}}),
             "if": {"field": "name", "nonempty": True}, "title": {"ru": "Wi-Fi «{name}» — WEP (вскрывается за минуты)", "en": "Wi-Fi “{name}” — WEP (broken in minutes)"},
             "fix": {"ru": "psk2 или sae", "en": "psk2 or sae"}},
            {"id": "wifi-weak", "severity": "warning", "value": C(OWRT_WIFI_ISSUES, {"each": r"^(?P<kind>\w+)\|(?P<name>.*)$"}, {"filter": {"field": "kind", "eq": "weak"}}),
             "if": {"field": "name", "nonempty": True}, "title": {"ru": "Wi-Fi «{name}» — WPA1 или смешанный режим WPA/WPA2", "en": "Wi-Fi “{name}” — WPA1 or mixed WPA/WPA2"},
             "fix": {"ru": "psk2 (WPA2) или sae-mixed (WPA2/WPA3)", "en": "psk2 (WPA2) or sae-mixed (WPA2/WPA3)"}},
            {"id": "wifi-tkip", "severity": "warning", "value": C(OWRT_WIFI_ISSUES, {"each": r"^(?P<kind>\w+)\|(?P<name>.*)$"}, {"filter": {"field": "kind", "eq": "tkip"}}),
             "if": {"field": "name", "nonempty": True}, "title": {"ru": "Wi-Fi «{name}» — шифр TKIP", "en": "Wi-Fi “{name}” — the TKIP cipher"},
             "fix": {"ru": "только CCMP (без +tkip)", "en": "CCMP only (no +tkip)"}},
            {"id": "wps", "severity": "warning", "value": C(OWRT_WIFI_ISSUES, {"each": r"^(?P<kind>\w+)\|(?P<name>.*)$"}, {"filter": {"field": "kind", "eq": "wps"}}),
             "if": {"field": "name", "nonempty": True}, "title": {"ru": "Wi-Fi «{name}» — включён WPS", "en": "Wi-Fi “{name}” — WPS is on"},
             "fix": "uci set wireless.<iface>.wps_pushbutton=0; uci commit wireless; wifi reload"},
            {"id": "ssh-password", "severity": "info", "value": C("uci -q get dropbear.@dropbear[0].PasswordAuth", {"trim": 1}), "if": {"eq": "on"},
             "title": {"ru": "SSH пускает по паролю", "en": "SSH accepts passwords"}, "fix": "uci set dropbear.@dropbear[0].PasswordAuth=off; uci commit"},
            {"id": "forwards", "severity": "info", "value": C(OWRT_REDIRECTS, {"each": r"^(?P<name>[^|]*)\|[^|]*\|[^|]*\|[^|]*\|[^|]*\|(?P<enabled>[^|]*)$"},
                                                               {"filter": {"field": "enabled", "ne": "0"}}, {"count": 1}), "if": {"gt": 0},
             "title": {"ru": "Проброс портов из интернета в LAN: {value}", "en": "Port forwards from the internet into the LAN: {value}"},
             "detail": {"ru": "список — в составе («Проброс портов»); лишние — убрать", "en": "the list is in the inventory (Port forwards); remove the ones not needed"}},
            {"id": "upgradable", "severity": "info", "value": C(OWRT_UPGRADABLE, {"lines": 0}, {"count": 1}), "if": {"gt": 0},
             "title": {"ru": "Пакетов с обновлениями: {value}", "en": "Packages with updates: {value}"},
             "fix": {"ru": "действие «Обновить пакет» или opkg upgrade <пакет>", "en": "the Upgrade a package action or opkg upgrade <package>"}},
            {"id": "overlay", "severity": "warning", "value": C("df -P /overlay 2>/dev/null | awk 'NR==2{print $5+0}'", {"num": 1}), "if": {"gt": 90},
             "title": {"ru": "Место под настройки и пакеты (/overlay) занято на {value}%", "en": "/overlay (settings and packages) is {value}% full"}},
            {"id": "old-release", "severity": "warning", "value": C(". /etc/openwrt_release 2>/dev/null; echo \"$DISTRIB_RELEASE\"", {"num": 1}), "if": {"lt": 23.05},
             "title": {"ru": "Ветка OpenWrt {value} больше не получает исправлений", "en": "OpenWrt {value} no longer gets fixes"},
             "fix": {"ru": "обновить прошивку (sysupgrade) до поддерживаемой ветки", "en": "upgrade the firmware (sysupgrade) to a supported release"}},
        ],
        # Ключи Wi-Fi, VPN и прочие секреты — под ***: история конфигураций хаба
        # их не хранит, дифф показывает только «изменился».
        "config": C("uci export", {"mask": OWRT_SECRETS}),
        "actions": [
            REBOOT | {"cmd": "reboot"},
            {"op": "wifi_reload", "title": {"ru": "Перезапустить Wi-Fi", "en": "Reload Wi-Fi"}, "cmd": "wifi reload"},
            {"op": "service_restart", "title": {"ru": "Перезапустить службу", "en": "Restart a service"}, "cmd": "test -x /etc/init.d/{name} && /etc/init.d/{name} restart && echo restarted",
             "fields": [{"name": "name", "title": {"ru": "Служба", "en": "Service"}, "required": True}]},
            {"op": "pkg_upgrade", "title": {"ru": "Обновить пакет", "en": "Upgrade a package"},
             "confirm": {"ru": "Обновить пакет на роутере? Идёт в фоне, журнал — /tmp/nkt-upgrade.log.", "en": "Upgrade the package on the router? It runs in the background, the log is /tmp/nkt-upgrade.log."},
             "cmd": "( if command -v apk >/dev/null; then apk update && apk upgrade {name}; else opkg update && opkg upgrade {name}; fi ) >/tmp/nkt-upgrade.log 2>&1 </dev/null & echo started: /tmp/nkt-upgrade.log",
             "fields": [{"name": "name", "title": {"ru": "Пакет", "en": "Package"}, "required": True}]},
            {"op": "backup", "title": {"ru": "Резервная копия настроек", "en": "Settings backup"},
             "cmd": "f=/tmp/nkt-backup-$(date +%Y%m%d-%H%M%S).tar.gz; sysupgrade -b $f && ls -l $f"},
        ],
    },
    "keenetic": {
        "id": "keenetic", "title": "Keenetic", "transport": "http", "icon": "keenetic", "auth": "keenetic",
        "hint": {"ru": "Веб-интерфейс роутера (RCI API) в своей сети; пользователь с правами администратора.", "en": "The router's web interface (RCI API) on your network; a user with admin rights."},
        "info": {"name": {"get": "/rci/show/system", "parse": [{"json": "hostname"}]}, "os": {"const": "KeeneticOS"},
                 "version": {"get": "/rci/show/version", "parse": [{"json": "release"}]}, "model": {"get": "/rci/show/version", "parse": [{"json": "model"}]},
                 "uptime_s": {"get": "/rci/show/system", "parse": [{"json": "uptime"}]}},
        "snapshot": {
            "interfaces": {"get": "/rci/show/interface", "parse": [{"items": True}, {"pick": {"name": "id", "type": "type", "mac": "mac", "addrs": "address", "up": "link", "comment": "description"}}]},
        },
        "extra": [{"title": {"ru": "Устройства в сети", "en": "Devices on the network"}, "columns": [["name", {"ru": "Имя", "en": "Name"}], ["ip", "IP"], ["mac", "MAC"], ["active", {"ru": "В сети", "en": "Online"}]],
                   "rows": {"get": "/rci/show/ip/hotspot", "parse": [{"json": "host"}]}}],
        "metrics": {"cpu_pct": {"get": "/rci/show/system", "parse": [{"json": "cpuload"}]},
                    "mem_used": {"get": "/rci/show/system", "parse": [{"json": "memory"}, {"regex": r"^(\d+)/"}, {"num": 1024}]},
                    "mem_total": {"get": "/rci/show/system", "parse": [{"json": "memory"}, {"regex": r"/(\d+)$"}, {"num": 1024}]},
                    "uptime_s": {"get": "/rci/show/system", "parse": [{"json": "uptime"}]}},
        "findings": [
            {"id": "cpu", "severity": "warning", "value": {"get": "/rci/show/system", "parse": [{"json": "cpuload"}]}, "if": {"gt": 90},
             "title": {"ru": "Загрузка процессора {value}%", "en": "CPU load {value}%"}},
            {"id": "http", "severity": "warning", "value": {"conn": "tls"}, "if": {"eq": "http"},
             "title": {"ru": "Хаб ходит на роутер без шифрования", "en": "The hub reaches the router without encryption"}},
        ],
        "config": {"get": "/rci/show/running-config", "parse": [{"json": "message"}, {"join": "\n"}]},
        "actions": [REBOOT | {"post": "/rci/system/reboot", "body": {}}],
        "exec": {"lang": "keenetic-cli", "post": "/rci/", "body": [{"parse": "{command}"}]},
    },
    "snmp-printer": {
        "id": "snmp-printer", "title": {"ru": "Принтер (SNMP)", "en": "Printer (SNMP)"}, "transport": "snmp", "icon": "printer",
        "hint": {"ru": "SNMP v2c включён на принтере, community только для чтения.", "en": "SNMP v2c on the printer, a read-only community."},
        "info": {"name": {"oid": "1.3.6.1.2.1.1.5.0"}, "os": {"oid": "1.3.6.1.2.1.1.1.0"}, "model": {"oid": "1.3.6.1.2.1.25.3.2.1.3.1"},
                 "serial": {"oid": "1.3.6.1.2.1.43.5.1.1.17.1"}, "uptime_s": {"oid": "1.3.6.1.2.1.1.3.0", "parse": [{"num": 0.01}]}},
        "extra": [{"title": {"ru": "Расходники", "en": "Supplies"}, "columns": [["name", {"ru": "Что", "en": "What"}], ["pct", "%"], ["level", {"ru": "Осталось", "en": "Left"}], ["max", {"ru": "Всего", "en": "Max"}]],
                   "rows": {"table": {"name": "1.3.6.1.2.1.43.11.1.1.6.1", "level": "1.3.6.1.2.1.43.11.1.1.9.1", "max": "1.3.6.1.2.1.43.11.1.1.8.1"}, "parse": [{"pct": ["level", "max", "pct"]}]}},
                  {"title": {"ru": "Счётчик страниц", "en": "Page counter"}, "columns": [["count", {"ru": "Страниц", "en": "Pages"}]],
                   "rows": {"table": {"count": "1.3.6.1.2.1.43.10.2.1.4.1"}}}],
        "findings": [
            {"id": "supply", "severity": "warning",
             "value": {"table": {"name": "1.3.6.1.2.1.43.11.1.1.6.1", "level": "1.3.6.1.2.1.43.11.1.1.9.1", "max": "1.3.6.1.2.1.43.11.1.1.8.1"}, "parse": [{"pct": ["level", "max", "pct"]}]},
             "if": {"field": "pct", "lt": 10}, "title": {"ru": "{name}: осталось {pct}%", "en": "{name}: {pct}% left"}},
            {"id": "community-public", "severity": "warning", "value": {"conn": "password"}, "if": {"eq": "public"},
             "title": {"ru": "SNMP community «public» — его знают все", "en": "SNMP community “public” — everyone knows it"}},
        ],
    },
    "snmp-switch": {
        "id": "snmp-switch", "title": {"ru": "Коммутатор (SNMP)", "en": "Switch (SNMP)"}, "transport": "snmp", "icon": "switch",
        "hint": {"ru": "SNMP v2c, community только для чтения; соседи — по LLDP, если он включён.", "en": "SNMP v2c, a read-only community; neighbors — via LLDP if it's on."},
        "info": {"name": {"oid": "1.3.6.1.2.1.1.5.0"}, "os": {"oid": "1.3.6.1.2.1.1.1.0"}, "uptime_s": {"oid": "1.3.6.1.2.1.1.3.0", "parse": [{"num": 0.01}]}},
        "snapshot": {
            "interfaces": {"table": {"name": "1.3.6.1.2.1.31.1.1.1.1", "comment": "1.3.6.1.2.1.31.1.1.1.18", "up": "1.3.6.1.2.1.2.2.1.8", "mac": "1.3.6.1.2.1.2.2.1.6"}},
            "neighbors": {"table": {"name": "1.0.8802.1.1.2.1.4.1.1.9", "interface": "1.0.8802.1.1.2.1.4.1.1.8"}},
        },
        "metrics": {"uptime_s": {"oid": "1.3.6.1.2.1.1.3.0", "parse": [{"num": 0.01}]},
                    "net": {"table": {"name": "1.3.6.1.2.1.31.1.1.1.1", "rx": "1.3.6.1.2.1.31.1.1.1.6", "tx": "1.3.6.1.2.1.31.1.1.1.10"}}},
        "findings": [
            {"id": "community-public", "severity": "warning", "value": {"conn": "password"}, "if": {"eq": "public"},
             "title": {"ru": "SNMP community «public» — его знают все", "en": "SNMP community “public” — everyone knows it"}},
        ],
    },
    "snmp-ups": {
        "id": "snmp-ups", "title": {"ru": "ИБП (SNMP)", "en": "UPS (SNMP)"}, "transport": "snmp", "icon": "ups",
        "hint": {"ru": "Сетевая карта ИБП (APC, Eaton, CyberPower…) с SNMP v2c: стандартный UPS-MIB (RFC 1628) и APC PowerNet — что ответит. Community — только чтение.",
                 "en": "A UPS network card (APC, Eaton, CyberPower…) with SNMP v2c: the standard UPS-MIB (RFC 1628) and APC PowerNet — whichever answers. A read-only community."},
        "info": {"name": {"oid": "1.3.6.1.2.1.1.5.0"}, "os": {"any": [{"oid": UPS + "1.1.0"}, {"oid": "1.3.6.1.2.1.1.1.0"}]},
                 "model": {"any": [{"oid": UPS + "1.2.0"}, {"oid": APC + "1.1.1.0"}]}, "version": {"any": [{"oid": UPS + "1.3.0"}, {"oid": APC + "1.2.1.0"}]},
                 "serial": {"oid": APC + "1.2.3.0"}, "uptime_s": {"oid": "1.3.6.1.2.1.1.3.0", "parse": [{"num": 0.01}]}},
        "extra": [{"title": {"ru": "Состояние ИБП", "en": "UPS status"}, "columns": [["name", {"ru": "Параметр", "en": "Parameter"}], ["value", {"ru": "Значение", "en": "Value"}]],
                   "rows": {"fields": [
                       [{"ru": "Питание выхода", "en": "Output source"}, {"any": [{"oid": UPS + "4.1.0", "parse": [{"map": {"1": "other", "2": "none", "3": "normal", "4": "bypass", "5": "battery", "6": "booster", "7": "reducer"}}]},
                                                                               {"oid": APC + "4.1.1.0", "parse": [{"map": {"2": "normal", "3": "battery", "4": "smart boost", "6": "bypass", "12": "smart trim"}}]}]}],
                       [{"ru": "Заряд, %", "en": "Charge, %"}, {"any": [{"oid": UPS + "2.4.0"}, {"oid": APC + "2.2.1.0"}]}],
                       [{"ru": "Осталось от батареи, мин", "en": "Runtime left, min"}, {"any": [{"oid": UPS + "2.3.0"}, {"oid": APC + "2.2.3.0", "parse": [{"num": 1 / 6000}]}]}],
                       [{"ru": "Нагрузка, %", "en": "Load, %"}, UPS_LOAD],
                       [{"ru": "Вход, В", "en": "Input, V"}, {"any": [{"oid": UPS + "3.3.1.3.1"}, {"oid": APC + "3.2.1.0"}]}],
                       [{"ru": "Выход, В", "en": "Output, V"}, {"any": [{"oid": UPS + "4.4.1.2.1"}, {"oid": APC + "4.2.1.0"}]}],
                       [{"ru": "Температура батареи, °C", "en": "Battery temperature, °C"}, UPS_TEMP],
                       [{"ru": "Батарея заменена", "en": "Battery replaced"}, {"oid": APC + "2.1.3.0"}],
                       [{"ru": "Самотест", "en": "Self-test"}, {"any": [{"oid": UPS + "7.3.0", "parse": [{"map": {"1": "passed", "2": "warning", "3": "error", "4": "aborted", "5": "in progress", "6": "none"}}]},
                                                                         {"oid": APC + "7.2.3.0", "parse": [{"map": {"1": "ok", "2": "failed", "3": "invalid", "4": "in progress"}}]}]}],
                   ]}}],
        "metrics": {"temp_c": UPS_TEMP, "uptime_s": {"oid": "1.3.6.1.2.1.1.3.0", "parse": [{"num": 0.01}]}},
        "findings": [
            {"id": "on-battery", "severity": "critical", "value": {"any": [{"oid": UPS + "4.1.0"}, {"oid": APC + "4.1.1.0", "parse": [{"map": {"3": "5"}}]}]}, "if": {"eq": "5"},
             "title": {"ru": "ИБП работает от батареи — нет сетевого питания", "en": "The UPS runs on battery — no mains power"}},
            {"id": "battery-low", "severity": "critical", "value": {"any": [{"oid": UPS + "2.1.0"}, {"oid": APC + "2.1.1.0"}]}, "if": {"in": ["3", "4"]},
             "title": {"ru": "Батарея ИБП разряжена или неисправна", "en": "The UPS battery is low or faulty"}},
            {"id": "replace-battery", "severity": "warning", "value": {"oid": APC + "2.2.4.0"}, "if": {"eq": "2"},
             "title": {"ru": "ИБП просит заменить батарею", "en": "The UPS asks for a battery replacement"}},
            {"id": "overload", "severity": "warning", "value": UPS_LOAD, "if": {"gt": 90}, "title": {"ru": "Нагрузка ИБП {value}%", "en": "UPS load {value}%"},
             "fix": {"ru": "снять часть нагрузки — при перегрузке ИБП переходит на байпас", "en": "shed some load — an overloaded UPS goes to bypass"}},
            {"id": "self-test", "severity": "warning", "value": {"any": [{"oid": UPS + "7.3.0", "parse": [{"map": {"3": "failed"}}]}, {"oid": APC + "7.2.3.0", "parse": [{"map": {"2": "failed"}}]}]},
             "if": {"eq": "failed"}, "title": {"ru": "Самотест ИБП не пройден", "en": "The UPS self-test failed"}},
            {"id": "battery-temp", "severity": "warning", "value": UPS_TEMP, "if": {"gt": 40}, "title": {"ru": "Батарея ИБП нагрета до {value} °C", "en": "The UPS battery is at {value} °C"}},
            {"id": "alarms", "severity": "warning", "value": {"oid": UPS + "6.1.0"}, "if": {"gt": 0}, "title": {"ru": "Тревог ИБП: {value}", "en": "UPS alarms: {value}"}},
            {"id": "community-public", "severity": "warning", "value": {"conn": "password"}, "if": {"eq": "public"},
             "title": {"ru": "SNMP community «public»", "en": "SNMP community “public”"}},
        ],
    },
    "nut-ups": {
        "id": "nut-ups", "title": {"ru": "ИБП (NUT)", "en": "UPS (NUT)"}, "transport": "nut", "icon": "ups",
        "hint": {"ru": "Любой ИБП на сервере NUT (upsd, порт 3493): Synology, Raspberry Pi, хост с nut-server. На сервере — LISTEN на адресе сети в upsd.conf. Пользователь и пароль — только для самотеста.",
                 "en": "Any UPS on a NUT server (upsd, port 3493): Synology, Raspberry Pi, a host with nut-server. On the server — LISTEN on a network address in upsd.conf. The user and password are only for the self-test."},
        "info": {"name": {"conn": "ups"}, "os": {"any": [NV("device.mfr"), NV("ups.mfr")]}, "model": {"any": [NV("device.model"), NV("ups.model")]},
                 "serial": {"any": [NV("device.serial"), NV("ups.serial")]}, "firmware": NV("ups.firmware"), "version": NV("driver.name")},
        "extra": [{"title": {"ru": "Переменные NUT", "en": "NUT variables"}, "columns": [["key", {"ru": "Переменная", "en": "Variable"}], ["value", {"ru": "Значение", "en": "Value"}]],
                   "rows": {"nut": "vars", "parse": [{"items": 1}]}}],
        "metrics": {"temp_c": {"any": [NV("ups.temperature"), NV("battery.temperature")]}},
        "findings": [
            {"id": "on-battery", "severity": "critical", "value": NV("ups.status"), "if": {"word": "OB"},
             "title": {"ru": "ИБП работает от батареи — нет сетевого питания", "en": "The UPS runs on battery — no mains power"}},
            {"id": "battery-low", "severity": "critical", "value": NV("ups.status"), "if": {"word": "LB"}, "title": {"ru": "Батарея ИБП почти разряжена", "en": "The UPS battery is almost empty"}},
            {"id": "replace-battery", "severity": "warning", "value": NV("ups.status"), "if": {"word": "RB"}, "title": {"ru": "ИБП просит заменить батарею", "en": "The UPS asks for a battery replacement"}},
            {"id": "overload", "severity": "warning", "value": NV("ups.status"), "if": {"word": "OVER"}, "title": {"ru": "ИБП перегружен", "en": "The UPS is overloaded"}},
            {"id": "charge", "severity": "warning", "value": NV("battery.charge"), "if": {"lt": 30}, "title": {"ru": "Заряд батареи {value}%", "en": "Battery charge {value}%"}},
            {"id": "runtime", "severity": "warning", "value": NV("battery.runtime"), "if": {"lt": 300},
             "title": {"ru": "От батареи осталось {value} с", "en": "{value} s of battery runtime left"}},
            {"id": "self-test", "severity": "warning", "value": NV("ups.test.result"), "if": {"contains": "fail"}, "title": {"ru": "Самотест: {value}", "en": "Self-test: {value}"}},
            {"id": "temp", "severity": "warning", "value": {"any": [NV("ups.temperature"), NV("battery.temperature")]}, "if": {"gt": 40},
             "title": {"ru": "Температура ИБП {value} °C", "en": "UPS temperature {value} °C"}},
            {"id": "alarm", "severity": "warning", "value": NV("ups.alarm"), "if": {"nonempty": True}, "title": {"ru": "Тревога ИБП: {value}", "en": "UPS alarm: {value}"}},
        ],
        "actions": [{"op": "self_test", "title": {"ru": "Быстрый самотест", "en": "Quick self-test"},
                     "confirm": {"ru": "Запустить быстрый самотест ИБП? Нагрузка на секунды перейдёт на батарею.", "en": "Start a quick UPS self-test? The load moves to the battery for a few seconds."},
                     "instcmd": "test.battery.start.quick"}],
    },
    "onvif-camera": {
        "id": "onvif-camera", "title": {"ru": "IP-камера или регистратор (ONVIF)", "en": "IP camera or NVR (ONVIF)"}, "transport": "onvif", "icon": "camera",
        "hint": {"ru": "ONVIF включён на устройстве (у Hikvision — «Сеть» → «Расширенные» → «Интеграция» и свой пользователь ONVIF). Для перезагрузки нужен администратор. Диски регистратора — через API Hikvision (ISAPI) или Dahua, если отвечает.",
                 "en": "ONVIF is on (on Hikvision — Network → Advanced → Integration and a separate ONVIF user). Rebooting needs an administrator. NVR disks — through the Hikvision (ISAPI) or Dahua API, if it answers."},
        "info": {"name": {"onvif": "info", "parse": [{"key": "Model"}]}, "os": {"onvif": "info", "parse": [{"key": "Manufacturer"}]},
                 "model": {"onvif": "info", "parse": [{"key": "Model"}]}, "version": {"onvif": "info", "parse": [{"key": "FirmwareVersion"}]},
                 "serial": {"onvif": "info", "parse": [{"key": "SerialNumber"}]}, "firmware": {"onvif": "info", "parse": [{"key": "HardwareId"}]}},
        "extra": [
            {"title": {"ru": "Потоки", "en": "Streams"}, "columns": [["name", {"ru": "Профиль", "en": "Profile"}], ["encoding", {"ru": "Кодек", "en": "Codec"}],
                                                                    ["resolution", {"ru": "Разрешение", "en": "Resolution"}], ["uri", "RTSP"]],
             "rows": {"onvif": "streams"}},
            {"title": {"ru": "Время", "en": "Time"}, "columns": [["name", {"ru": "Параметр", "en": "Parameter"}], ["value", {"ru": "Значение", "en": "Value"}]],
             "rows": {"fields": [[{"ru": "Расхождение с хостом плагина, с", "en": "Offset from the plugin host, s"}, {"onvif": "time", "parse": [{"key": "drift_s"}]}],
                                 [{"ru": "Установка времени", "en": "Time set by"}, {"onvif": "time", "parse": [{"key": "type"}]}],
                                 [{"ru": "Часовой пояс", "en": "Time zone"}, {"onvif": "time", "parse": [{"key": "tz"}]}]]}},
            {"title": {"ru": "Диски регистратора (Hikvision)", "en": "NVR disks (Hikvision)"},
             "columns": [["name", "№"], ["label", {"ru": "Диск", "en": "Disk"}], ["status", {"ru": "Состояние", "en": "Status"}],
                         ["capacity", {"ru": "Ёмкость, МБ", "en": "Capacity, MB"}], ["free", {"ru": "Свободно, МБ", "en": "Free, MB"}]], "rows": HIK_HDD},
            {"title": {"ru": "Диски регистратора (Dahua)", "en": "NVR disks (Dahua)"},
             "columns": [["name", {"ru": "Диск", "en": "Disk"}], ["state", {"ru": "Состояние", "en": "State"}], ["total", {"ru": "Всего, байт", "en": "Total, bytes"}],
                         ["used", {"ru": "Занято, байт", "en": "Used, bytes"}]], "rows": DAHUA_HDD},
        ],
        "findings": [
            {"id": "clock", "severity": "warning", "value": {"onvif": "time", "parse": [{"key": "drift_abs"}]}, "if": {"gt": 60},
             "title": {"ru": "Часы устройства расходятся на {value} с — время на записях неверное", "en": "The device clock is off by {value} s — recordings carry the wrong time"},
             "fix": {"ru": "включить NTP на устройстве", "en": "turn on NTP on the device"}},
            {"id": "hdd", "severity": "warning", "value": HIK_HDD, "if": {"field": "status", "nin": ["ok", "idle", "sleep", "sleeping", "standby"]},
             "title": {"ru": "Диск {name} регистратора: {status}", "en": "NVR disk {name}: {status}"}},
            {"id": "dahua-hdd", "severity": "warning", "value": DAHUA_HDD, "if": {"field": "state", "nin": ["Success"]},
             "title": {"ru": "Диск {name} регистратора: {state}", "en": "NVR disk {name}: {state}"}},
            {"id": "http", "severity": "info", "value": {"conn": "tls"}, "if": {"eq": "http"},
             "title": {"ru": "Связь с устройством — по HTTP без шифрования", "en": "The device is reached over plain HTTP"},
             "detail": {"ru": "пароль ONVIF уходит дайджестом, но остальное — открытым текстом", "en": "the ONVIF password goes as a digest, but everything else is in clear text"}},
        ],
        "config": {"onvif": "config"},
        "actions": [REBOOT | {"onvif": "SystemReboot"}],
    },
}

TRANSPORTS = ("ssh", "http", "snmp", "nut", "onvif")
ID_RE = re.compile(r"^[a-z][a-z0-9-]{1,39}$")
LOCK = threading.Lock()


def profiles_dir():
    d = os.path.join(STATE, "profiles")
    os.makedirs(d, exist_ok=True)
    return d


def custom_profiles():
    out = {}
    try:
        names = os.listdir(profiles_dir())
    except OSError:
        return out
    for n in sorted(names):
        if n.endswith(".json") and ID_RE.match(n[:-5]):
            try:
                with open(os.path.join(profiles_dir(), n)) as f:
                    p = json.load(f)
                validate(p)
                out[p["id"]] = p
            except (OSError, ValueError, Fail):
                continue
    return out


def all_profiles():
    out = dict(BUILTIN)
    out.update(custom_profiles())
    return out


def validate(p):
    if not isinstance(p, dict):
        raise ValueError("not an object")
    if not ID_RE.match(str(p.get("id", ""))):
        raise ValueError("id: latin letters, digits, -")
    if p.get("transport") not in TRANSPORTS:
        raise ValueError("transport: " + " | ".join(TRANSPORTS))
    if p.get("icon") and not re.match(r"^[a-z][a-z0-9_-]{0,47}$", str(p["icon"])):
        raise ValueError("icon: latin letters, digits, -")
    if not p.get("title"):
        raise ValueError("no title")
    for key in ("info", "snapshot", "metrics"):
        if key in p and not isinstance(p[key], dict):
            raise ValueError(f"{key}: an object")
    for key in ("findings", "actions", "extra"):
        if key in p and not isinstance(p[key], list):
            raise ValueError(f"{key}: a list")
    for a in p.get("actions") or []:
        if not ID_RE.match(str(a.get("op", "")).replace("_", "-")):
            raise ValueError(f"action op {a.get('op')!r}")
        if not any(k in a for k in ("cmd", "post", "instcmd", "onvif")):
            raise ValueError(f"action {a.get('op')}: cmd, post, instcmd or onvif")


def fields_for(c, p):
    t = p["transport"]
    fp = {"name": "fingerprint", "title": c.t("f_fingerprint"), "hint": c.t("f_fingerprint_hint")}
    addr = {"name": "address", "title": c.t("f_address"), "required": True, "hint": c.t("f_address_hint"), "placeholder": "192.168.1.10"}
    if t == "ssh":
        return [addr, {"name": "user", "title": c.t("f_user"), "default": "root"}, {"name": "password", "title": c.t("f_password"), "type": "password"},
                {"name": "key", "title": c.t("f_key"), "type": "password", "hint": c.t("f_key_hint")}, fp]
    if t == "http":
        return [addr, {"name": "user", "title": c.t("f_user"), "default": "admin"}, {"name": "password", "title": c.t("f_password"), "type": "password", "required": True},
                {"name": "tls", "title": c.t("f_tls"), "type": "select", "default": "http",
                 "options": [{"value": "http", "title": c.t("tls_http")}, {"value": "pin", "title": c.t("tls_pin")}, {"value": "verify", "title": c.t("tls_verify")}]}, fp]
    if t == "nut":
        return [dict(addr, hint=c.t("f_nut_addr_hint"), placeholder="192.168.1.5"), {"name": "ups", "title": c.t("f_ups"), "default": "ups", "hint": c.t("f_ups_hint")},
                {"name": "user", "title": c.t("f_user"), "hint": c.t("f_nut_user_hint")}, {"name": "password", "title": c.t("f_password"), "type": "password"}]
    if t == "onvif":
        return [dict(addr, placeholder="192.168.1.64"), {"name": "user", "title": c.t("f_user"), "default": "admin"},
                {"name": "password", "title": c.t("f_password"), "type": "password", "required": True},
                {"name": "tls", "title": c.t("f_tls"), "type": "select", "default": "http",
                 "options": [{"value": "http", "title": c.t("tls_http")}, {"value": "pin", "title": c.t("tls_pin")}, {"value": "verify", "title": c.t("tls_verify")}]}, fp]
    return [addr, {"name": "password", "title": c.t("f_community"), "type": "password", "required": True, "hint": c.t("f_community_hint")}]


def host_type(c, p):
    sections = []
    if p.get("info") or p.get("snapshot"):
        sections.append("overview")
    for k, s in (("metrics", "metrics"), ("findings", "findings"), ("config", "config"), ("actions", "actions")):
        if p.get(k):
            sections.append(s)
    lang = exec_lang(p)
    if lang:
        sections.append("exec")
    out = {"id": p["id"], "title": c.txt(p.get("title")), "hint": c.txt(p.get("hint")), "fields": fields_for(c, p), "sections": sections,
           "actions": [{"title": c.txt(a.get("title")) or a["op"], "op": a["op"], "confirm": c.txt(a.get("confirm")), "danger": bool(a.get("danger")),
                        "fields": [{"name": f["name"], "title": c.txt(f.get("title")) or f["name"], "required": bool(f.get("required"))} for f in a.get("fields") or []]}
                       for a in p.get("actions") or []]}
    if lang:
        out["exec"] = lang
    if p.get("icon"):
        out["icon"] = p["icon"]
    return out


def link_for(c, p, conn):
    t = p["transport"]
    if t == "ssh":
        return SSHLink(c, conn)
    if t == "http":
        return HTTPLink(c, conn, p.get("auth", "basic"))
    if t == "nut":
        return NUTLink(c, conn)
    if t == "onvif":
        return ONVIFLink(c, conn)
    return SNMPLink(c, conn)


# --- раздел «Профили» ---------------------------------------------------------------

def profiles_view(c):
    custom = custom_profiles()
    rows = []
    for pid, p in sorted(all_profiles().items()):
        src = "override" if pid in custom and pid in BUILTIN else ("custom" if pid in custom else "builtin")
        actions = [{"title": c.t("edit"), "args": {"id": pid}, "edit": {"get": "profile_text", "put": "profile_save", "doc": "connector-" + pid, "language": "json"}}]
        if pid in custom:
            actions.append({"title": c.t("delete"), "op": "profile_delete", "args": {"id": pid}, "danger": True, "confirm": c.t("delete_confirm", id=pid)})
        rows.append({"cells": {"id": pid, "title": c.txt(p.get("title")), "transport": p["transport"], "source": c.t(src)}, "actions": actions})
    new = {"title": c.t("new"), "op": "profile_new", "fields": [
        {"name": "id", "title": c.t("id"), "required": True, "placeholder": "my-device"},
        {"name": "base", "title": c.t("base"), "type": "select", "default": "linux-ssh", "options": [{"value": k} for k in sorted(BUILTIN)]}]}
    return {"type": "table", "title": c.t("profiles"), "note": c.t("profiles_note"), "rows": rows, "actions": [new],
            "columns": [{"key": "id", "title": c.t("id")}, {"key": "title", "title": c.t("ptitle")}, {"key": "transport", "title": c.t("transport")},
                        {"key": "source", "title": c.t("source")}]}


def profile_save(c, pid, text, dry):
    try:
        p = json.loads(text)
        validate(p)
    except (ValueError, TypeError) as e:
        raise c.fail("bad_profile", id=pid, err=str(e)[:300])
    if p["id"] != pid:
        raise c.fail("bad_profile", id=pid, err=f"id {p['id']!r} ≠ {pid!r}")
    if dry:
        return c.t("would", what=c.t("saved", id=pid))
    with LOCK:
        tmp = os.path.join(profiles_dir(), f".{pid}.tmp")
        with open(tmp, "w") as f:
            json.dump(p, f, ensure_ascii=False, indent=2)
        os.replace(tmp, os.path.join(profiles_dir(), f"{pid}.json"))
    return c.t("saved", id=pid)


# --- манифест и вызовы --------------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


def manifest(c):
    S = {"type": "string"}
    return {
        "protocol": 1, "name": "connector", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "profiles", "title": c.t("profiles"), "read_only": True, "mcp": True, "description": "Device profiles (types): id, title, transport, built-in or custom."},
            {"name": "profile_text", "read_only": True, "mcp": True, "description": "A profile as JSON text (for profile_save).", "input": schema({"id": S}, ["id"])},
            {"name": "profile_save", "mcp": True, "description": "Save a profile from JSON text (overrides a built-in one with the same id).", "input": schema({"id": S, "text": S}, ["id", "text"])},
            {"name": "profile_new", "title": c.t("new"), "description": "Create a custom profile from a built-in one.", "input": schema({"id": S, "base": S}, ["id"])},
            {"name": "profile_delete", "title": c.t("delete"), "destructive": True, "description": "Delete a custom profile.", "input": schema({"id": S}, ["id"])},
        ],
        "pages": [{"id": "profiles", "title": c.t("profiles"), "op": "profiles"}],
        "provides": ["hosts"],
        "host_types": [host_type(c, p) for _, p in sorted(all_profiles().items())],
    }


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if op == "profiles":
        return {"ok": True, "view": profiles_view(c)}
    if op == "profile_text":
        p = all_profiles().get(str(args.get("id", "")))
        if not p:
            raise c.fail("no_profile", id=args.get("id"))
        return {"ok": True, "result": {"text": json.dumps(p, ensure_ascii=False, indent=2) + "\n"}}
    if op == "profile_save":
        return {"ok": True, "text": profile_save(c, str(args.get("id", "")), str(args.get("text", "")), dry)}
    if op == "profile_new":
        pid = str(args.get("id", "")).strip()
        if not ID_RE.match(pid):
            raise c.fail("bad_id", id=pid)
        if pid in all_profiles():
            raise c.fail("exists", id=pid)
        base = dict(all_profiles().get(str(args.get("base") or "linux-ssh")) or BUILTIN["linux-ssh"])
        base["id"] = pid
        base["title"] = pid
        profile_save(c, pid, json.dumps(base), dry)
        return {"ok": True, "text": c.t("would", what=c.t("created", id=pid)) if dry else c.t("created", id=pid)}
    if op == "profile_delete":
        pid = str(args.get("id", ""))
        path = os.path.join(profiles_dir(), f"{pid}.json")
        if not ID_RE.match(pid) or not os.path.exists(path):
            raise c.fail("builtin_only", id=pid)
        if not dry:
            os.remove(path)
        return {"ok": True, "text": c.t("would", what=c.t("deleted", id=pid)) if dry else c.t("deleted", id=pid)}
    if not op.startswith("_host_"):
        return {"ok": False, "error": f"no op {op}"}
    p = all_profiles().get(str(args.get("type", "")))
    if not p:
        raise c.fail("no_profile", id=args.get("type"))
    conn = args.get("conn") or {}
    if op == "_host_exec" and truthy(args.get("check")):
        return {"ok": True, "text": exec_cmd(c, None, p, args.get("command"), True, False)}
    with link_for(c, p, conn) as link:
        ev = Eval(link, p, conn, c)
        if op == "_host_check":
            return {"ok": True, "result": info_of(c, ev, p)}
        if op == "_host_snapshot":
            return {"ok": True, "result": snapshot(c, ev, p)}
        if op == "_host_metrics":
            return {"ok": True, "result": metrics(c, ev, p)}
        if op == "_host_findings":
            return {"ok": True, "result": {"findings": findings(c, ev, p)}}
        if op == "_host_config":
            return {"ok": True, "result": {"text": config(c, ev, p)}}
        if op == "_host_action":
            text = action(c, ev, p, str(args.get("action") or ""), args.get("args") or {}, dry)
            if not dry:
                log(f"{req.get('user', '')}: {args.get('action')} на {conn.get('address')}")
            return {"ok": True, "text": text}
        if op == "_host_exec":
            text = exec_cmd(c, ev, p, args.get("command"), False, dry)
            if not dry:
                log(f"{req.get('user', '')}: команда на {conn.get('address')} ({len(str(args.get('command') or ''))} знаков)")
            return {"ok": True, "text": text}
    return {"ok": False, "error": f"no op {op}"}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-connector/" + VERSION

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
        if size > 2 << 20:
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
    log(f"{VERSION}: {LISTEN}:{PORT}, профили: {STATE}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
