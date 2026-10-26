"""Пример своего плагина хаба nkt: проверка адресов сайтов и сервисов.

Написан по examples/AGENTS.md из заготовки examples/_template; в каталог
не входит — его загружают в хаб как свой («Плагины» → «Каталог» →
«Загрузить свой плагин»). Проверяет адреса из настроек (URL_CHECK_TARGETS):
код ответа, время, сколько дней до конца сертификата; находки — не
отвечает или 5xx, медленно, сертификат истекает. Команда сценария
«urlcheck АДРЕС» — проверить один адрес (после выкладки: упавшая проверка
останавливает сценарий). Инструменты MCP.
Без зависимостей: только стандартная библиотека Python.
"""
import datetime
import hashlib
import hmac
import json
import os
import re
import socket
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9298") or 9298)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
TARGETS = os.environ.get("URL_CHECK_TARGETS", "")
SLOW_MS = int(os.environ.get("URL_CHECK_SLOW_MS", "2000") or 2000)
# Свой удостоверяющий центр (внутренние сайты) — файл PEM; сертификаты
# проверяются по-настоящему, отключить проверку нельзя.
CA_FILE = os.environ.get("URL_CHECK_CA_FILE", "").strip()
TIMEOUT = 10
READ_LIMIT = 64 << 10
CERT_DAYS = 14

T = {
    "ru": {
        "title": "Проверка адресов", "desc": "Адреса сайтов и сервисов из настроек: код ответа, время, срок сертификата; находки и команда «urlcheck».",
        "targets": "Адреса", "findings": "Находки", "url": "Адрес", "code": "Код", "ms": "Время, мс", "cert": "Сертификат, дн.",
        "result": "Итог", "ok": "в порядке", "none": "Находок нет", "no_targets": "адресов нет — URL_CHECK_TARGETS в «Настройках» плагина",
        "f_down": "{url}: не отвечает — {err}", "f_5xx": "{url}: ошибка сервера {code}", "f_slow": "{url}: ответ за {ms} мс (порог {slow})",
        "f_cert": "{url}: сертификат истекает через {days} дн.", "f_expired": "{url}: сертификат истёк",
        "bad_url": "адрес {url}: только http:// или https://, без пробелов и логина", "check_ok": "проверено",
        "usage": "urlcheck АДРЕС", "checked": "{url}: {code}, {ms} мс",
    },
    "en": {
        "title": "URL check", "desc": "Site and service addresses from the settings: response code, time, certificate expiry; findings and the “urlcheck” command.",
        "targets": "Addresses", "findings": "Findings", "url": "Address", "code": "Code", "ms": "Time, ms", "cert": "Certificate, days",
        "result": "Result", "ok": "fine", "none": "No findings", "no_targets": "no addresses — URL_CHECK_TARGETS in the plugin's Settings",
        "f_down": "{url}: not answering — {err}", "f_5xx": "{url}: server error {code}", "f_slow": "{url}: answered in {ms} ms (threshold {slow})",
        "f_cert": "{url}: the certificate expires in {days} days", "f_expired": "{url}: the certificate has expired",
        "bad_url": "address {url}: http:// or https:// only, with no spaces or login", "check_ok": "checked",
        "usage": "urlcheck ADDRESS", "checked": "{url}: {code}, {ms} ms",
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
    print(f"nkt-plugin-url-check: {msg}", flush=True)


URL_RE = re.compile(r"^https?://[A-Za-z0-9.-]{1,253}(:\d{1,5})?(/[A-Za-z0-9._~/%?&=+:@!$,;*()-]*)?$")


def check_url(c, url):
    url = str(url or "").strip()
    if not valid(url):
        raise c.fail("bad_url", url=url or "—")
    return url


def targets():
    out = []
    for t in re.split(r"[,\s]+", TARGETS):
        if t and t not in out:
            out.append(t)
    return out[:100]


def tls_context():
    ctx = ssl.create_default_context()
    if CA_FILE:
        ctx.load_verify_locations(CA_FILE)
    return ctx


def cert_days(host, port):
    """Дней до конца сертификата (проверенного) или None."""
    with socket.create_connection((host, port), timeout=TIMEOUT) as sock:
        with tls_context().wrap_socket(sock, server_hostname=host) as tls:
            cert = tls.getpeercert()
    end = datetime.datetime.fromtimestamp(ssl.cert_time_to_seconds(cert["notAfter"]), datetime.timezone.utc)
    return int((end - datetime.datetime.now(datetime.timezone.utc)).total_seconds() // 86400)


def probe(url):
    """{url, code, ms, cert, err} — без исключений (ошибка — в err)."""
    out = {"url": url, "code": 0, "ms": 0, "cert": None, "err": ""}
    u = urllib.parse.urlsplit(url)
    start = time.monotonic()
    try:
        req = urllib.request.Request(url, headers={"User-Agent": f"nkt-url-check/{VERSION}"})
        opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=tls_context()))
        try:
            with opener.open(req, timeout=TIMEOUT) as r:
                r.read(READ_LIMIT)
                out["code"] = r.status
        except urllib.error.HTTPError as e:
            out["code"] = e.code
        out["ms"] = int((time.monotonic() - start) * 1000)
        if u.scheme == "https":
            out["cert"] = cert_days(u.hostname, u.port or 443)
    except (urllib.error.URLError, OSError, ssl.SSLError, ValueError) as e:
        out["ms"] = int((time.monotonic() - start) * 1000)
        reason = getattr(e, "reason", e)
        out["err"] = (type(reason).__name__ + ": " + str(reason))[:200]
    return out


def valid(url):
    u = urllib.parse.urlsplit(url) if URL_RE.match(url) else None
    return bool(u) and not u.username and not u.password and len(url) <= 500


def probe_all():
    # Только прошедшие проверку адреса: остальные показываются ошибкой и
    # никуда не ходят (urllib умеет и ftp://, и file://).
    lst = [t for t in targets() if valid(t)]
    if not lst:
        return []
    with ThreadPoolExecutor(max_workers=8) as pool:
        return list(pool.map(probe, lst))


def issues(c, r):
    if r["err"]:
        return [c.t("f_down", url=r["url"], err=r["err"])]
    out = []
    if r["code"] >= 500:
        out.append(c.t("f_5xx", url=r["url"], code=r["code"]))
    if r["ms"] > SLOW_MS:
        out.append(c.t("f_slow", url=r["url"], ms=r["ms"], slow=SLOW_MS))
    if r["cert"] is not None and r["cert"] < 0:
        out.append(c.t("f_expired", url=r["url"]))
    elif r["cert"] is not None and r["cert"] < CERT_DAYS:
        out.append(c.t("f_cert", url=r["url"], days=r["cert"]))
    return out


def status_view(c):
    rows = []
    for t in targets():
        try:
            check_url(c, t)
        except Fail as e:
            rows.append({"cells": {"url": t, "result": str(e)}})
    for r in probe_all():
        rows.append({"cells": {"url": r["url"], "code": r["code"] or "", "ms": r["ms"], "cert": "" if r["cert"] is None else r["cert"],
                               "result": "; ".join(issues(c, r)) or c.t("ok")}})
    cols = [("url", c.t("url")), ("code", c.t("code")), ("ms", c.t("ms")), ("cert", c.t("cert")), ("result", c.t("result"))]
    return {"type": "table", "title": c.t("targets"), "note": "" if targets() else c.t("no_targets"),
            "columns": [{"key": k, "title": t} for k, t in cols], "rows": rows}


def findings_view(c):
    found = [f for r in probe_all() for f in issues(c, r)]
    return {"type": "table", "title": c.t("findings"), "columns": [{"key": "f", "title": c.t("findings")}],
            "rows": [{"cells": {"f": f}} for f in found] or [{"cells": {"f": c.t("none")}}]}


def check_one(c, url):
    url = check_url(c, url)
    r = probe(url)
    bad = issues(c, r)
    if bad:
        return {"ok": False, "error": "; ".join(bad)}
    return {"ok": True, "text": c.t("checked", url=url, code=r["code"], ms=r["ms"])}


def command(c, argv, check):
    if len(argv) != 2:
        raise Fail(c.t("usage"))
    url = check_url(c, argv[1])
    return {"ok": True} if check else check_one(c, url)


def manifest(c):
    return {
        "protocol": 1, "name": "url-check", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "status", "title": c.t("targets"), "read_only": True, "mcp": True,
             "description": "Configured addresses: HTTP code, response time in ms, days until the TLS certificate expires, problems."},
            {"name": "findings", "title": c.t("findings"), "read_only": True, "mcp": True,
             "description": "Problems only: not answering or 5xx, slow, certificate expiring within 14 days."},
            # Не только чтение: проверка любого адреса — запрос с хоста
            # плагина куда угодно; токену чтения её давать нельзя.
            {"name": "check", "title": "urlcheck", "mcp": True, "description": "Check one http(s) address now: code, time, certificate; ok=false if it fails.",
             "input": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]}},
        ],
        "pages": [{"id": "status", "title": c.t("targets"), "op": "status"}, {"id": "findings", "title": c.t("findings"), "op": "findings"}],
        "commands": [{"word": "urlcheck", "usage": c.t("usage"), "example": "urlcheck https://shop.example.com/health"}],
    }


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    if op == "_connect":
        return {"ok": True}
    if op == "status":
        return {"ok": True, "view": status_view(c)}
    if op == "findings":
        return {"ok": True, "view": findings_view(c)}
    if op == "check":
        return check_one(c, args.get("url"))
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        return command(c, [str(a) for a in args.get("argv") or []], False)
    return {"ok": False, "error": f"no op {op}"}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-url-check/" + VERSION

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
    log(f"{VERSION}: {LISTEN}:{PORT}, адресов: {len(targets())}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
