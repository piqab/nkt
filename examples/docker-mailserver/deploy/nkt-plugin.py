# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
"""Плагин хаба nkt для docker-mailserver (протокол плагинов nkt v1).

Ставится на почтовый хост: «О системе» → «Плагины» → «Установить на хост»
— служба systemd в группе docker (своего API у docker-mailserver нет:
ящики, алиасы и DKIM — командой setup внутри контейнера, docker exec).
Доступ к Docker — это права root на хосте: ставить только туда, где
работает сама почта. Слушает 127.0.0.1, хаб ходит по SSH и подписывает
вызовы секретом NKT_PLUGIN_SECRET.

Что умеет: домены и ящики (добавить, сменить пароль, удалить), алиасы,
ключ DKIM, записи DNS домена (MX, SPF, DKIM, DMARC; A — если задан
MAIL_PUBLIC_IP) — кнопкой «Поставить через DNS-плагин» они уходят в
подключённый к хабу DNS-плагин (Technitium, Cloudflare). Команды
сценариев «mail …», инструменты MCP.
Без зависимостей: только стандартная библиотека Python и docker CLI.
"""
import hashlib
import hmac
import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
CONTAINER = os.environ.get("DMS_CONTAINER", "mailserver").strip() or "mailserver"
DOCKER = os.environ.get("DOCKER", "docker")
HOSTNAME = os.environ.get("MAIL_HOSTNAME", "").strip().rstrip(".").lower()
PUBLIC_IP = os.environ.get("MAIL_PUBLIC_IP", "").strip()
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9200") or 9200)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
DKIM_DIR = "/tmp/docker-mailserver/rspamd/dkim"

T = {
    "ru": {
        "title": "Почта (docker-mailserver)",
        "desc": "Ящики, алиасы и DKIM docker-mailserver из хаба; записи DNS домена (MX, SPF, DKIM, DMARC) — в DNS-плагин хаба одной кнопкой.",
        "domains": "Домены", "mailboxes": "Ящики", "aliases": "Алиасы", "dns": "Записи DNS",
        "domain": "Домен", "boxes": "Ящиков", "dkim": "DKIM", "email": "Адрес", "used": "Занято", "quota": "Квота",
        "alias": "Алиас", "target": "Куда", "password": "Пароль", "password_hint": "не короче 8 знаков; хранится только хешем в docker-mailserver",
        "yes": "есть", "no": "нет",
        "domains_note": "Домен появляется с первым ящиком. Записи DNS домена — «Записи DNS» → «Поставить через DNS-плагин».",
        "add_box": "Добавить ящик", "set_password": "Сменить пароль", "delete": "Удалить", "add_alias": "Добавить алиас",
        "gen_dkim": "Создать ключ DKIM", "open_boxes": "Ящики", "open_dns": "Записи DNS",
        "delete_box_confirm": "Удалить ящик {email} вместе с письмами?",
        "delete_alias_confirm": "Удалить алиас {alias} → {target}?",
        "to_dns": "Поставить через DNS-плагин",
        "dns_note": "Записи для {domain}. PTR (обратную запись) адреса сервера ставят у хостинга — её здесь нет.",
        "dkim_missing": "Ключа DKIM для {domain} ещё нет — «Создать ключ DKIM».",
        "no_public_ip": "A-записи {host} нет: не задан MAIL_PUBLIC_IP плагина (или A ставит Traffic Fly/сайт).",
        "added_box": "ящик {email} добавлен", "deleted_box": "ящик {email} удалён", "password_set": "пароль {email} сменён",
        "added_alias": "алиас {alias} → {target} добавлен", "deleted_alias": "алиас {alias} → {target} удалён",
        "dkim_done": "ключ DKIM для {domain} создан — перезапустите контейнер {container}, чтобы Rspamd начал подписывать; записи DNS: «Записи DNS» домена",
        "dkim_exists": "ключ DKIM для {domain} уже есть",
        "dry": "(сухой прогон) ", "would": "(сухой прогон) {what}",
        "bad_email": "адрес {email}: user@example.com",
        "bad_domain": "домен {domain}: example.com",
        "bad_password": "пароль: от 8 до 128 знаков, одна строка",
        "docker_failed": "docker exec {container}: {err}",
        "usage": "mail dkim ДОМЕН | mail alias add АЛИАС КУДА | mail alias rm АЛИАС КУДА | mail rm АДРЕС | mail records ДОМЕН",
        "check_ok": "проверено",
    },
    "en": {
        "title": "Mail (docker-mailserver)",
        "desc": "docker-mailserver mailboxes, aliases and DKIM from the hub; the domain's DNS records (MX, SPF, DKIM, DMARC) — to the hub's DNS plugin with one button.",
        "domains": "Domains", "mailboxes": "Mailboxes", "aliases": "Aliases", "dns": "DNS records",
        "domain": "Domain", "boxes": "Mailboxes", "dkim": "DKIM", "email": "Address", "used": "Used", "quota": "Quota",
        "alias": "Alias", "target": "To", "password": "Password", "password_hint": "8 characters or more; docker-mailserver keeps only its hash",
        "yes": "yes", "no": "no",
        "domains_note": "A domain appears with its first mailbox. The domain's DNS records — DNS records → Set through the DNS plugin.",
        "add_box": "Add mailbox", "set_password": "Change password", "delete": "Delete", "add_alias": "Add alias",
        "gen_dkim": "Create a DKIM key", "open_boxes": "Mailboxes", "open_dns": "DNS records",
        "delete_box_confirm": "Delete mailbox {email} with its mail?",
        "delete_alias_confirm": "Delete alias {alias} → {target}?",
        "to_dns": "Set through the DNS plugin",
        "dns_note": "Records for {domain}. The PTR (reverse) record of the server address is set at the hosting provider — it isn't here.",
        "dkim_missing": "No DKIM key for {domain} yet — Create a DKIM key.",
        "no_public_ip": "No A record for {host}: the plugin's MAIL_PUBLIC_IP isn't set (or Traffic Fly/a site sets the A).",
        "added_box": "mailbox {email} added", "deleted_box": "mailbox {email} deleted", "password_set": "password of {email} changed",
        "added_alias": "alias {alias} → {target} added", "deleted_alias": "alias {alias} → {target} deleted",
        "dkim_done": "DKIM key for {domain} created — restart the {container} container so Rspamd starts signing; DNS records: the domain's DNS records",
        "dkim_exists": "a DKIM key for {domain} already exists",
        "dry": "(dry run) ", "would": "(dry run) {what}",
        "bad_email": "address {email}: user@example.com",
        "bad_domain": "domain {domain}: example.com",
        "bad_password": "password: 8 to 128 characters, one line",
        "docker_failed": "docker exec {container}: {err}",
        "usage": "mail dkim DOMAIN | mail alias add ALIAS TO | mail alias rm ALIAS TO | mail rm ADDRESS | mail records DOMAIN",
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
    print(f"nkt-plugin-mail: {msg}", flush=True)


DOMAIN_RE = re.compile(r"^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{1,62}$")
LOCAL_RE = re.compile(r"^[a-z0-9][a-z0-9._+-]{0,63}$")


def domain_ok(c, d):
    d = str(d or "").strip().rstrip(".").lower()
    if not DOMAIN_RE.match(d):
        raise c.fail("bad_domain", domain=d)
    return d


def email_ok(c, e):
    e = str(e or "").strip().lower()
    local, _, dom = e.partition("@")
    if not LOCAL_RE.match(local) or not DOMAIN_RE.match(dom):
        raise c.fail("bad_email", email=e)
    return e


def password_ok(c, p):
    p = str(p or "")
    if not 8 <= len(p) <= 128 or any(ch in p for ch in "\r\n"):
        raise c.fail("bad_password")
    return p


# --- docker exec setup --------------------------------------------------------

def dms(c, *args, env=None):
    """setup … в контейнере. Пароль — переменной окружения exec (в
    командной строке хоста её значения нет: «-e ИМЯ» берёт его из
    окружения docker CLI)."""
    cmd = [DOCKER, "exec", "-i"]
    for k in (env or {}):
        cmd += ["-e", k]
    cmd += [CONTAINER] + list(args)
    run_env = dict(os.environ, HOME=os.environ.get("HOME") or "/tmp", **(env or {}))
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=120, env=run_env)
    except (OSError, subprocess.TimeoutExpired) as e:
        raise c.fail("docker_failed", container=CONTAINER, err=e)
    if r.returncode != 0:
        raise c.fail("docker_failed", container=CONTAINER, err=(r.stderr or r.stdout).strip()[:500])
    return r.stdout


BOX_RE = re.compile(r"^\*\s+(\S+@\S+)\s+\(\s*([^/]*?)\s*/\s*([^)]*?)\s*\)")
ALIAS_RE = re.compile(r"^\*\s+(\S+@\S+)\s+(.+)$")


def mailboxes(c):
    out = []
    for line in dms(c, "setup", "email", "list").splitlines():
        m = BOX_RE.match(line.strip())
        if m:
            out.append({"email": m.group(1).lower(), "used": m.group(2), "quota": m.group(3)})
    return out


def aliases(c):
    out = []
    for line in dms(c, "setup", "alias", "list").splitlines():
        m = ALIAS_RE.match(line.strip())
        if m:
            for target in re.split(r"[,\s]+", m.group(2).strip()):
                if target:
                    out.append({"alias": m.group(1).lower(), "target": target.lower()})
    return out


def dkim_record(c, domain):
    """(селектор, значение TXT) ключа DKIM домена или None. Имя файла у
    версий разное — ищется по маске и разбирается любой из форматов."""
    # Домен бывает и из вывода контейнера (список ящиков) — проверка здесь,
    # в оболочку он уходит аргументом, не текстом команды.
    if not DOMAIN_RE.match(domain):
        return None
    out = dms(c, "sh", "-c", f'for f in {DKIM_DIR}/*"$1"*public*; do [ -f "$f" ] && echo "== $f" && cat "$f"; done; true', "_", domain)
    best = None
    for block in out.split("== ")[1:]:
        path, _, body = block.partition("\n")
        name = os.path.basename(path.strip())
        sel = "mail"
        m = re.match(rf"^(?:rsa|ed25519)-\d+-(.+)-{re.escape(domain)}\.public", name)
        if m:
            sel = m.group(1)
        quoted = re.findall(r'"([^"]*)"', body)
        value = "".join(quoted) if quoted else ""
        if "p=" not in value:
            key = "".join(line.strip() for line in body.splitlines() if line.strip() and not line.startswith("-----"))
            if not key or "p=" in key:
                value = key
            else:
                value = f"v=DKIM1; k=rsa; p={key}"
        if "p=" in value:
            best = (sel, re.sub(r"\s+", " ", value).strip())
            if name.endswith(".dns.txt"):
                break
    return best


def domains(c):
    doms = {}
    for b in mailboxes(c):
        doms.setdefault(b["email"].split("@", 1)[1], 0)
        doms[b["email"].split("@", 1)[1]] += 1
    return doms


# --- записи DNS домена --------------------------------------------------------

def dns_records(c, domain):
    """Записи для DNS-плагина хаба (формат _dns_sync) и строки-пояснения."""
    domain = domain_ok(c, domain)
    host = HOSTNAME or f"mail.{domain}"
    recs = [
        {"name": domain, "type": "MX", "values": [f"10 {host}."], "ttl": 3600},
        {"name": domain, "type": "TXT", "values": ['"v=spf1 mx -all"'], "ttl": 3600},
        {"name": f"_dmarc.{domain}", "type": "TXT", "values": [f'"v=DMARC1; p=quarantine; rua=mailto:postmaster@{domain}"'], "ttl": 3600},
    ]
    notes = []
    dk = dkim_record(c, domain)
    if dk:
        recs.append({"name": f"{dk[0]}._domainkey.{domain}", "type": "TXT", "values": ['"' + dk[1] + '"'], "ttl": 3600})
    else:
        notes.append(c.t("dkim_missing", domain=domain))
    if PUBLIC_IP:
        recs.append({"name": host, "type": "AAAA" if ":" in PUBLIC_IP else "A", "values": [PUBLIC_IP], "ttl": 3600})
    else:
        notes.append(c.t("no_public_ip", host=host))
    return recs, notes


# --- операции -----------------------------------------------------------------

def mailbox_add(c, email, password, dry):
    email, password = email_ok(c, email), password_ok(c, password)
    if dry:
        return [c.t("would", what=c.t("added_box", email=email))]
    dms(c, "bash", "-c", 'setup email add "$1" "$NKT_MAIL_PASS"', "_", email, env={"NKT_MAIL_PASS": password})
    return [c.t("added_box", email=email)]


def mailbox_password(c, email, password, dry):
    email, password = email_ok(c, email), password_ok(c, password)
    if dry:
        return [c.t("would", what=c.t("password_set", email=email))]
    dms(c, "bash", "-c", 'setup email update "$1" "$NKT_MAIL_PASS"', "_", email, env={"NKT_MAIL_PASS": password})
    return [c.t("password_set", email=email)]


def mailbox_delete(c, email, dry):
    email = email_ok(c, email)
    if dry:
        return [c.t("would", what=c.t("deleted_box", email=email))]
    dms(c, "setup", "email", "del", "-y", email)
    return [c.t("deleted_box", email=email)]


def alias_add(c, alias, target, dry):
    alias, target = email_ok(c, alias), email_ok(c, target)
    if dry:
        return [c.t("would", what=c.t("added_alias", alias=alias, target=target))]
    dms(c, "setup", "alias", "add", alias, target)
    return [c.t("added_alias", alias=alias, target=target)]


def alias_delete(c, alias, target, dry):
    alias, target = email_ok(c, alias), email_ok(c, target)
    if dry:
        return [c.t("would", what=c.t("deleted_alias", alias=alias, target=target))]
    dms(c, "setup", "alias", "del", alias, target)
    return [c.t("deleted_alias", alias=alias, target=target)]


def dkim_generate(c, domain, dry):
    domain = domain_ok(c, domain)
    if dkim_record(c, domain):
        return [c.t("dkim_exists", domain=domain)]
    if dry:
        return [c.t("would", what=c.t("dkim_done", domain=domain, container=CONTAINER))]
    dms(c, "setup", "config", "dkim", "domain", domain)
    return [c.t("dkim_done", domain=domain, container=CONTAINER)]


# --- виды ---------------------------------------------------------------------

def password_field(c):
    return {"name": "password", "title": c.t("password"), "type": "password", "required": True, "hint": c.t("password_hint")}


def domains_view(c):
    rows = []
    for d, n in sorted(domains(c).items()):
        args = {"domain": d}
        rows.append({"cells": {"domain": d, "boxes": n, "dkim": c.t("yes") if dkim_record(c, d) else c.t("no")}, "actions": [
            {"title": c.t("open_boxes"), "open": "mailboxes", "args": args},
            {"title": c.t("open_dns"), "open": "dns", "args": args},
            {"title": c.t("gen_dkim"), "op": "dkim_generate", "args": args},
        ]})
    add = {"title": c.t("add_box"), "op": "mailbox_add", "fields": [
        {"name": "email", "title": c.t("email"), "required": True, "placeholder": "user@example.com"}, password_field(c)]}
    return {"type": "table", "title": c.t("domains"), "note": c.t("domains_note"), "rows": rows, "actions": [add],
            "columns": [{"key": "domain", "title": c.t("domain")}, {"key": "boxes", "title": c.t("boxes")}, {"key": "dkim", "title": c.t("dkim")}]}


def mailboxes_view(c, domain=None):
    rows = []
    for b in mailboxes(c):
        if domain and not b["email"].endswith("@" + domain):
            continue
        args = {"email": b["email"]}
        rows.append({"cells": b, "actions": [
            {"title": c.t("set_password"), "op": "mailbox_password", "args": args, "fields": [password_field(c)]},
            {"title": c.t("delete"), "op": "mailbox_delete", "args": args, "danger": True, "confirm": c.t("delete_box_confirm", email=b["email"])},
        ]})
    add = {"title": c.t("add_box"), "op": "mailbox_add", "fields": [
        {"name": "email", "title": c.t("email"), "required": True, "placeholder": f"user@{domain or 'example.com'}"}, password_field(c)]}
    return {"type": "table", "title": c.t("mailboxes") + (f" — {domain}" if domain else ""), "rows": rows, "actions": [add],
            "columns": [{"key": "email", "title": c.t("email")}, {"key": "used", "title": c.t("used")}, {"key": "quota", "title": c.t("quota")}]}


def aliases_view(c):
    rows = [{"cells": a, "actions": [{"title": c.t("delete"), "op": "alias_delete", "args": a, "danger": True,
                                      "confirm": c.t("delete_alias_confirm", alias=a["alias"], target=a["target"])}]} for a in aliases(c)]
    add = {"title": c.t("add_alias"), "op": "alias_add", "fields": [
        {"name": "alias", "title": c.t("alias"), "required": True, "placeholder": "info@example.com"},
        {"name": "target", "title": c.t("target"), "required": True, "placeholder": "user@example.com"}]}
    return {"type": "table", "title": c.t("aliases"), "rows": rows, "actions": [add],
            "columns": [{"key": "alias", "title": c.t("alias")}, {"key": "target", "title": c.t("target")}]}


def dns_view(c, domain):
    recs, notes = dns_records(c, domain)
    rows = [{"cells": {"name": r["name"], "type": r["type"], "value": ", ".join(r["values"])}} for r in recs]
    note = " ".join([c.t("dns_note", domain=domain)] + notes)
    action = {"title": c.t("to_dns"), "dns": {"owner": "domain:" + domain, "records": recs}}
    return {"type": "table", "title": f"{c.t('dns')} — {domain}", "note": note, "rows": rows, "actions": [action],
            "columns": [{"key": "name", "title": "Name"}, {"key": "type", "title": "Type"}, {"key": "value", "title": "Value"}]}


# --- команда сценариев ----------------------------------------------------------

def command(c, argv, dry, check):
    """mail … — без паролей: ящики с паролем заводятся в разделе или по MCP
    (пароль из сценария попал бы в журнал действий)."""
    if len(argv) < 3 or argv[0] != "mail":
        raise Fail(c.t("usage"))
    sub, rest = argv[1], argv[2:]
    if sub == "dkim" and len(rest) == 1:
        domain_ok(c, rest[0])
        return [] if check else dkim_generate(c, rest[0], dry)
    if sub == "records" and len(rest) == 1:
        domain_ok(c, rest[0])
        if check:
            return []
        recs, notes = dns_records(c, rest[0])
        return [f"{r['name']} {r['type']} {', '.join(r['values'])}" for r in recs] + notes
    if sub == "alias" and len(rest) == 3 and rest[0] in ("add", "rm"):
        email_ok(c, rest[1])
        email_ok(c, rest[2])
        if check:
            return []
        return alias_add(c, rest[1], rest[2], dry) if rest[0] == "add" else alias_delete(c, rest[1], rest[2], dry)
    if sub == "rm" and len(rest) == 1:
        email_ok(c, rest[0])
        return [] if check else mailbox_delete(c, rest[0], dry)
    raise Fail(c.t("usage"))


def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


S = {"type": "string"}


def manifest(c):
    dom = {"domain": {"type": "string", "description": "mail domain, e.g. example.com"}}
    return {
        "protocol": 1, "name": "mail", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "domains", "title": c.t("domains"), "read_only": True, "mcp": True, "description": "Mail domains with mailbox counts and DKIM state."},
            {"name": "mailboxes", "title": c.t("mailboxes"), "read_only": True, "mcp": True, "description": "Mailboxes (optionally of one domain) with usage.", "input": schema(dom)},
            {"name": "aliases", "title": c.t("aliases"), "read_only": True, "mcp": True, "description": "Mail aliases."},
            {"name": "dns", "title": c.t("dns"), "read_only": True, "mcp": True, "description": "DNS records a mail domain needs (MX, SPF, DKIM, DMARC, A).", "input": schema(dom, ["domain"])},
            {"name": "mailbox_add", "mcp": True, "description": "Add a mailbox with a password.", "input": schema({"email": S, "password": S}, ["email", "password"])},
            {"name": "mailbox_password", "mcp": True, "description": "Change a mailbox password.", "input": schema({"email": S, "password": S}, ["email", "password"])},
            {"name": "mailbox_delete", "mcp": True, "destructive": True, "description": "Delete a mailbox with its mail.", "input": schema({"email": S}, ["email"])},
            {"name": "alias_add", "mcp": True, "description": "Add an alias.", "input": schema({"alias": S, "target": S}, ["alias", "target"])},
            {"name": "alias_delete", "mcp": True, "destructive": True, "description": "Delete an alias.", "input": schema({"alias": S, "target": S}, ["alias", "target"])},
            {"name": "dkim_generate", "mcp": True, "description": "Create a DKIM key for a domain (Rspamd, 2048 bits).", "input": schema(dom, ["domain"])},
        ],
        "pages": [
            {"id": "domains", "title": c.t("domains"), "op": "domains"},
            {"id": "aliases", "title": c.t("aliases"), "op": "aliases"},
            {"id": "mailboxes", "title": c.t("mailboxes"), "op": "mailboxes", "hidden": True},
            {"id": "dns", "title": c.t("dns"), "op": "dns", "hidden": True},
        ],
        "commands": [{"word": "mail", "usage": c.t("usage"), "example": "mail dkim example.com\nmail alias add info@example.com user@example.com"}],
    }


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if op == "domains":
        return {"ok": True, "view": domains_view(c)}
    if op == "mailboxes":
        return {"ok": True, "view": mailboxes_view(c, domain_ok(c, args["domain"]) if args.get("domain") else None)}
    if op == "aliases":
        return {"ok": True, "view": aliases_view(c)}
    if op == "dns":
        return {"ok": True, "view": dns_view(c, domain_ok(c, args.get("domain")))}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        lines = command(c, [str(a) for a in args.get("argv") or []], dry, False)
    elif op == "mailbox_add":
        lines = mailbox_add(c, args.get("email"), args.get("password"), dry)
    elif op == "mailbox_password":
        lines = mailbox_password(c, args.get("email"), args.get("password"), dry)
    elif op == "mailbox_delete":
        lines = mailbox_delete(c, args.get("email"), dry)
    elif op == "alias_add":
        lines = alias_add(c, args.get("alias"), args.get("target"), dry)
    elif op == "alias_delete":
        lines = alias_delete(c, args.get("alias"), args.get("target"), dry)
    elif op == "dkim_generate":
        lines = dkim_generate(c, args.get("domain"), dry)
    else:
        return {"ok": False, "error": f"no op {op}"}
    if not dry:
        log(f"{req.get('user', '')}: {op}")
    return {"ok": True, "text": "\n".join(lines)}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-mail/" + VERSION

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
        except Exception as e:  # ошибка плагина — текстом хабу
            log(f"ошибка: {e!r}")
            self.reply({"ok": False, "error": f"plugin error: {e}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}, контейнер {CONTAINER}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
