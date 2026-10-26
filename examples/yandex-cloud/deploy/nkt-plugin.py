"""Плагин хаба nkt для Yandex Cloud (протокол плагинов nkt v1, provides
dns и hosts).

Ставится на машину хаба или любой хост с интернетом: «О системе» →
«Плагины» → «Установить на хост». Вход — авторизованный ключ сервисного
аккаунта (JSON из `yc iam key create`, в настройку — base64 одной строкой):
плагин сам подписывает JWT (PS256 — через openssl на своём хосте) и меняет
его на IAM-токен.

Что умеет: ВМ (включить, выключить, перезагрузить, снимок загрузочного
диска; ВМ — и как хост nkt: «Хосты» → «Добавить устройство» → «ВМ Yandex
Cloud»), диски и снимки, группы безопасности (правила, открытые всему
интернету), бакеты Object Storage (публичные, размер), управляемые
PostgreSQL и MySQL, баланс платёжного аккаунта; Cloud DNS — зоны, записи
и DNS-функции хаба (Traffic Fly, сайты, автоимена); команды «yc …»,
инструменты MCP.
Без зависимостей: стандартная библиотека Python и openssl.
"""
import base64
import hashlib
import hmac
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9240") or 9240)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
FOLDERS = [f.strip() for f in os.environ.get("YC_FOLDERS", "").split(",") if f.strip()]
BILLING = os.environ.get("YC_BILLING_ACCOUNT", "").strip()
OPENSSL = os.environ.get("OPENSSL", "openssl")
_ONE = os.environ.get("YC_ENDPOINT", "").rstrip("/")  # для проверки: один адрес на все службы
API = {k: _ONE or v for k, v in {
    "iam": "https://iam.api.cloud.yandex.net", "compute": "https://compute.api.cloud.yandex.net", "vpc": "https://vpc.api.cloud.yandex.net",
    "storage": "https://storage.api.cloud.yandex.net", "dns": "https://dns.api.cloud.yandex.net", "mdb": "https://mdb.api.cloud.yandex.net",
    "billing": "https://billing.api.cloud.yandex.net"}.items()}


def load_key():
    raw = os.environ.get("YC_SA_KEY_B64", "").strip()
    if not raw:
        return None
    try:
        k = json.loads(base64.b64decode(raw + "=" * (-len(raw) % 4)))
    except ValueError:
        return None
    pk = str(k.get("private_key") or "")
    # У ключей Yandex Cloud перед PEM бывает строка «PLEASE DO NOT REMOVE
    # THIS LINE!…» — openssl её не примет.
    i = pk.find("-----BEGIN")
    return {"id": k.get("id", ""), "sa": k.get("service_account_id", ""), "pem": pk[i:] if i >= 0 else ""}


KEY = load_key()

T = {
    "ru": {
        "title": "Yandex Cloud",
        "desc": "ВМ, диски и снимки, группы безопасности, Object Storage, Cloud DNS (и DNS-функции хаба), PostgreSQL и MySQL, баланс; ВМ — и как хосты nkt.",
        "vms": "ВМ", "disks": "Диски и снимки", "security": "Группы безопасности", "storage": "Object Storage", "dns": "Cloud DNS", "databases": "Базы данных", "billing": "Баланс",
        "name": "Имя", "folder": "Каталог", "zone": "Зона", "status": "Состояние", "res": "Ресурсы", "ip": "Адреса", "public": "Публичный",
        "size": "Размер", "type": "Тип", "attached": "Подключён к", "created": "Создан", "disk": "Диск", "snapshots": "Снимки",
        "group": "Группа", "rule": "Правило", "open": "Открыто всем", "bucket": "Бакет", "access": "Доступ", "records": "Записей", "ttl": "TTL", "value": "Значение",
        "engine": "СУБД", "env": "Окружение", "health": "Здоровье", "balance": "Баланс", "account": "Платёжный аккаунт",
        "start": "Включить", "stop": "Выключить", "restart": "Перезагрузить", "snapshot": "Снимок диска", "delete_snapshot": "Удалить снимок",
        "stop_confirm": "Выключить ВМ {name}? Сервисы на ней станут недоступны.", "restart_confirm": "Перезагрузить ВМ {name}?",
        "stop_this": "Выключить эту ВМ? Сервисы на ней станут недоступны.", "restart_this": "Перезагрузить эту ВМ?",
        "delsnap_confirm": "Удалить снимок {name}? Восстановить из него будет нельзя.", "open_records": "Записи",
        "done": "{what}: {name} — операция {op} запущена", "would": "(сухой прогон) {what}",
        "no_key": "не задан ключ сервисного аккаунта (YC_SA_KEY_B64) или он не читается", "no_openssl": "на хосте плагина нет openssl — нужен для подписи JWT",
        "no_folders": "не заданы каталоги (YC_FOLDERS)", "api_failed": "Yandex Cloud {what}: {err}", "sign_failed": "подпись JWT: {err}",
        "no_vm": "ВМ {name} нет в каталогах плагина", "no_disk": "диска {name} нет", "no_zone": "зоны {zone} нет в каталогах плагина",
        "no_billing": "не задан платёжный аккаунт (YC_BILLING_ACCOUNT)",
        "f_stopped": "ВМ выключена ({status})", "f_open": "Группа {group}: {port} открыт всему интернету ({proto})",
        "f_open_fix": "ограничьте cidr своими адресами или уберите правило", "f_no_sg": "У ВМ с публичным адресом нет групп безопасности — открыто всё",
        "f_no_snap": "У загрузочного диска нет снимка за 7 дней", "f_no_snap_fix": "действие «Снимок диска» или расписание снимков",
        "f_preempt": "Прерываемая ВМ: облако может выключить её в любой момент",
        "vm_type": "ВМ Yandex Cloud", "vm_hint": "ВМ из каталогов плагина: состояние, адреса, диски, группы безопасности, находки, действия. Учётки облака — в настройках плагина.",
        "vm_addr": "ВМ (имя или id)", "vm_addr_hint": "имя ВМ в каталоге или её id",
        "usage": "yc vm start|stop|restart ИМЯ | yc snapshot ИМЯ-ВМ | yc dns set ИМЯ ТИП ЗНАЧЕНИЕ[,…] [ttl N] | yc dns rm ИМЯ ТИП",
        "dns_set": "{name} {type}: {values}", "dns_rm": "{name} {type}: удалено", "nothing": "ничего не изменилось",
        "check_ok": "проверено", "public_bucket": "публичный: {what}", "private": "закрытый",
    },
    "en": {
        "title": "Yandex Cloud",
        "desc": "VMs, disks and snapshots, security groups, Object Storage, Cloud DNS (and the hub's DNS functions), PostgreSQL and MySQL, balance; VMs — also as nkt hosts.",
        "vms": "VMs", "disks": "Disks and snapshots", "security": "Security groups", "storage": "Object Storage", "dns": "Cloud DNS", "databases": "Databases", "billing": "Balance",
        "name": "Name", "folder": "Folder", "zone": "Zone", "status": "Status", "res": "Resources", "ip": "Addresses", "public": "Public",
        "size": "Size", "type": "Type", "attached": "Attached to", "created": "Created", "disk": "Disk", "snapshots": "Snapshots",
        "group": "Group", "rule": "Rule", "open": "Open to all", "bucket": "Bucket", "access": "Access", "records": "Records", "ttl": "TTL", "value": "Value",
        "engine": "DBMS", "env": "Environment", "health": "Health", "balance": "Balance", "account": "Billing account",
        "start": "Start", "stop": "Stop", "restart": "Restart", "snapshot": "Disk snapshot", "delete_snapshot": "Delete snapshot",
        "stop_confirm": "Stop VM {name}? Services on it become unavailable.", "restart_confirm": "Restart VM {name}?",
        "stop_this": "Stop this VM? Services on it become unavailable.", "restart_this": "Restart this VM?",
        "delsnap_confirm": "Delete snapshot {name}? It can't be restored from anymore.", "open_records": "Records",
        "done": "{what}: {name} — operation {op} started", "would": "(dry run) {what}",
        "no_key": "the service account key (YC_SA_KEY_B64) isn't set or can't be read", "no_openssl": "openssl is not on the plugin host — it signs the JWT",
        "no_folders": "folders (YC_FOLDERS) aren't set", "api_failed": "Yandex Cloud {what}: {err}", "sign_failed": "JWT signature: {err}",
        "no_vm": "no VM {name} in the plugin's folders", "no_disk": "no disk {name}", "no_zone": "no zone {zone} in the plugin's folders",
        "no_billing": "the billing account (YC_BILLING_ACCOUNT) isn't set",
        "f_stopped": "The VM is stopped ({status})", "f_open": "Group {group}: {port} is open to the whole internet ({proto})",
        "f_open_fix": "limit the cidr to your addresses or remove the rule", "f_no_sg": "A VM with a public address has no security groups — everything is open",
        "f_no_snap": "The boot disk has no snapshot in 7 days", "f_no_snap_fix": "the “Disk snapshot” action or a snapshot schedule",
        "f_preempt": "A preemptible VM: the cloud may stop it at any moment",
        "vm_type": "Yandex Cloud VM", "vm_hint": "A VM from the plugin's folders: status, addresses, disks, security groups, findings, actions. The cloud credentials are in the plugin's settings.",
        "vm_addr": "VM (name or id)", "vm_addr_hint": "the VM's name in the folder or its id",
        "usage": "yc vm start|stop|restart NAME | yc snapshot VM-NAME | yc dns set NAME TYPE VALUE[,…] [ttl N] | yc dns rm NAME TYPE",
        "dns_set": "{name} {type}: {values}", "dns_rm": "{name} {type}: deleted", "nothing": "nothing changed",
        "check_ok": "checked", "public_bucket": "public: {what}", "private": "private",
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
    print(f"nkt-plugin-yc: {msg}", flush=True)


# --- вход и API ---------------------------------------------------------------------

TOKEN = {"value": "", "until": 0.0}
TOKEN_LOCK = threading.Lock()


def b64u(b):
    return base64.urlsafe_b64encode(b).decode().rstrip("=")


def sign_jwt(c):
    if not KEY or not KEY["pem"] or not KEY["id"] or not KEY["sa"]:
        raise c.fail("no_key")
    if not shutil.which(OPENSSL):
        raise c.fail("no_openssl")
    now = int(time.time())
    head = b64u(json.dumps({"typ": "JWT", "alg": "PS256", "kid": KEY["id"]}).encode())
    body = b64u(json.dumps({"iss": KEY["sa"], "aud": "https://iam.api.cloud.yandex.net/iam/v1/tokens", "iat": now, "exp": now + 3600}).encode())
    d = tempfile.mkdtemp(prefix="nkt-yc-")
    try:
        kp = os.path.join(d, "k.pem")
        with open(os.open(kp, os.O_WRONLY | os.O_CREAT, 0o600), "w") as f:
            f.write(KEY["pem"])
        r = subprocess.run([OPENSSL, "dgst", "-sha256", "-sign", kp, "-sigopt", "rsa_padding_mode:pss", "-sigopt", "rsa_pss_saltlen:32"],
                           input=f"{head}.{body}".encode(), capture_output=True, timeout=20)
    finally:
        shutil.rmtree(d, ignore_errors=True)
    if r.returncode != 0:
        raise c.fail("sign_failed", err=r.stderr.decode(errors="replace").strip()[:200])
    return f"{head}.{body}.{b64u(r.stdout)}"


def raw_call(method, url, body=None, token=""):
    data = json.dumps(body).encode() if body is not None else None
    h = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, method=method, headers=h)
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read() or b"{}")


def iam_token(c):
    with TOKEN_LOCK:
        if TOKEN["value"] and time.time() < TOKEN["until"]:
            return TOKEN["value"]
        try:
            res = raw_call("POST", API["iam"] + "/iam/v1/tokens", {"jwt": sign_jwt(c)})
        except urllib.error.HTTPError as e:
            raise c.fail("api_failed", what="IAM", err=f"HTTP {e.code}: {e.read()[:200].decode(errors='replace')}")
        except urllib.error.URLError as e:
            raise c.fail("api_failed", what="IAM", err=str(e.reason))
        TOKEN["value"], TOKEN["until"] = res.get("iamToken", ""), time.time() + 3000
        return TOKEN["value"]


def yc(c, svc, method, path, body=None, params=None):
    url = API[svc] + path
    if params:
        url += ("&" if "?" in url else "?") + urllib.parse.urlencode(params)
    try:
        return raw_call(method, url, body, iam_token(c))
    except urllib.error.HTTPError as e:
        msg = e.read()[:300].decode(errors="replace")
        try:
            msg = json.loads(msg).get("message") or msg
        except ValueError:
            pass
        if e.code == 401:
            with TOKEN_LOCK:
                TOKEN["value"] = ""
        raise c.fail("api_failed", what=f"{method} {path}", err=f"HTTP {e.code}: {msg}")
    except urllib.error.URLError as e:
        raise c.fail("api_failed", what=f"{method} {path}", err=str(e.reason))


def listing(c, svc, path, key, folder, **params):
    out, page = [], ""
    for _ in range(50):
        p = dict(params, folderId=folder, pageSize=1000)
        if page:
            p["pageToken"] = page
        res = yc(c, svc, "GET", path, params=p)
        out += res.get(key) or []
        page = res.get("nextPageToken") or ""
        if not page:
            return out
    return out


def folders(c):
    if not FOLDERS:
        raise c.fail("no_folders")
    return FOLDERS


def each_folder(c, svc, path, key, **params):
    out = []
    for f in folders(c):
        for x in listing(c, svc, path, key, f, **params):
            x.setdefault("folderId", f)
            out.append(x)
    return out


# --- ВМ, диски, снимки --------------------------------------------------------------

def instances(c):
    return each_folder(c, "compute", "/compute/v1/instances", "instances")


def ips(vm):
    out = []
    for n in vm.get("networkInterfaces") or []:
        v4 = n.get("primaryV4Address") or {}
        if v4.get("address"):
            out.append(v4["address"])
        nat = (v4.get("oneToOneNat") or {}).get("address")
        if nat:
            out.append(nat + " (pub)")
    return out


def public_ip(vm):
    for n in vm.get("networkInterfaces") or []:
        nat = ((n.get("primaryV4Address") or {}).get("oneToOneNat") or {}).get("address")
        if nat:
            return nat
    return ""


def gb(v):
    try:
        return f"{int(v) / 2**30:.0f} GB"
    except (TypeError, ValueError):
        return ""


def find_vm(c, ref, folder=""):
    ref = str(ref or "").strip()
    for vm in instances(c):
        if (vm.get("id") == ref or vm.get("name") == ref) and (not folder or vm.get("folderId") == folder):
            return vm
    raise c.fail("no_vm", name=ref)


def vm_action(c, ref, act, dry, folder=""):
    vm = find_vm(c, ref, folder)
    what = c.t(act)
    if dry:
        return c.t("would", what=f"{what}: {vm['name']}")
    op = yc(c, "compute", "POST", f"/compute/v1/instances/{vm['id']}:{act}", {})
    return c.t("done", what=what, name=vm["name"], op=op.get("id", "?"))


def snapshot_disk(c, disk_id, name, folder, dry):
    sname = re.sub(r"[^a-z0-9-]", "-", f"nkt-{name}-{time.strftime('%Y%m%d-%H%M')}".lower())[:63]
    if dry:
        return c.t("would", what=f"{c.t('snapshot')}: {name} → {sname}")
    op = yc(c, "compute", "POST", "/compute/v1/snapshots", {"folderId": folder, "diskId": disk_id, "name": sname})
    return c.t("done", what=c.t("snapshot"), name=sname, op=op.get("id", "?"))


def vm_snapshot(c, ref, dry, folder=""):
    vm = find_vm(c, ref, folder)
    disk = (vm.get("bootDisk") or {}).get("diskId")
    if not disk:
        raise c.fail("no_disk", name=f"{vm['name']} boot")
    return snapshot_disk(c, disk, vm["name"], vm["folderId"], dry)


def security_groups(c):
    return each_folder(c, "vpc", "/vpc/v1/securityGroups", "securityGroups")


def open_rules(sg):
    """Входящие правила, открытые всему интернету."""
    out = []
    for r in sg.get("rules") or []:
        if r.get("direction") != "INGRESS":
            continue
        cidrs = ((r.get("cidrBlocks") or {}).get("v4CidrBlocks") or []) + ((r.get("cidrBlocks") or {}).get("v6CidrBlocks") or [])
        if any(x in ("0.0.0.0/0", "::/0") for x in cidrs):
            p = r.get("ports") or {}
            port = "any" if not p else (str(p.get("fromPort")) if str(p.get("fromPort")) == str(p.get("toPort")) else f"{p.get('fromPort')}-{p.get('toPort')}")
            out.append({"port": port, "proto": r.get("protocolName") or "ANY", "desc": r.get("description", "")})
    return out


RISKY_PORTS = {"22", "3389", "5432", "3306", "6379", "9200", "27017", "any"}


# --- Cloud DNS ----------------------------------------------------------------------

def zones(c):
    return {z["zone"].rstrip(".").lower(): z for z in each_folder(c, "dns", "/dns/v1/zones", "dnsZones")}


def zone_of(c, fqdn, zs):
    fqdn = fqdn.rstrip(".").lower()
    best = ""
    for z in zs:
        if (fqdn == z or fqdn.endswith("." + z)) and len(z) > len(best):
            best = z
    return best


def record_sets(c, zone_id):
    out, page = [], ""
    for _ in range(50):
        p = {"pageSize": 1000}
        if page:
            p["pageToken"] = page
        res = yc(c, "dns", "GET", f"/dns/v1/zones/{zone_id}:listRecordSets", params=p)
        out += res.get("recordSets") or []
        page = res.get("nextPageToken") or ""
        if not page:
            return out
    return out


NAME_RE = re.compile(r"^(\*\.)?([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.)*[a-z0-9-]{1,63}\.?$")
TYPES = ("A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "CAA")


def upsert(c, zone, name, typ, values, ttl, dry):
    fq = name.rstrip(".").lower() + "."
    if not values:
        cur = [r for r in record_sets(c, zone["id"]) if r["name"] == fq and r["type"] == typ]
        if not cur:
            return []
        if not dry:
            yc(c, "dns", "POST", f"/dns/v1/zones/{zone['id']}:upsertRecordSets", {"deletions": cur})
        return [c.t("dns_rm", name=fq, type=typ)]
    rs = {"name": fq, "type": typ, "ttl": int(ttl), "data": values}
    if not dry:
        yc(c, "dns", "POST", f"/dns/v1/zones/{zone['id']}:upsertRecordSets", {"replacements": [rs]})
    return [c.t("dns_set", name=fq, type=typ, values=", ".join(values))]


def dns_set(c, name, typ, values, ttl, dry):
    typ = str(typ).upper()
    name = str(name).rstrip(".").lower()
    if typ not in TYPES or not NAME_RE.match(name):
        raise c.fail("no_zone", zone=name)
    zs = zones(c)
    z = zone_of(c, name, zs)
    if not z:
        raise c.fail("no_zone", zone=name)
    lines = upsert(c, zs[z], name, typ, [str(v) for v in values], ttl or 300, dry)
    return [c.t("would", what=l) for l in lines] if dry else lines


def dns_sync(c, args, dry):
    """Записи владельца (Traffic Fly, сайт, автоимя): привести к records,
    убрав только прежние значения владельца — чужие у тех же имён
    остаются."""
    zs = zones(c)
    want = {(r["name"].rstrip(".").lower(), r["type"].upper()): r for r in args.get("records") or []}
    prev = {(r["name"].rstrip(".").lower(), r["type"].upper()): set(r.get("values") or []) for r in args.get("previous") or []}
    lines = []
    for key in sorted(set(want) | set(prev)):
        name, typ = key
        z = zone_of(c, name, zs)
        if not z:
            continue  # зоны нет у этого плагина — запись не его
        fq = name + "."
        cur = next((r for r in record_sets(c, zs[z]["id"]) if r["name"] == fq and r["type"] == typ), None)
        have = set(cur.get("data") or []) if cur else set()
        mine = set(want[key]["values"]) if key in want else set()
        keep = have - prev.get(key, set()) - mine
        target = sorted(keep | mine)
        if set(target) == have:
            continue
        ttl = (want.get(key) or {}).get("ttl") or (cur or {}).get("ttl") or 300
        for l in upsert(c, zs[z], name, typ, target, ttl, dry):
            lines.append(c.t("would", what=l) if dry else l)
    return lines


# --- виды ---------------------------------------------------------------------------

def vms_view(c):
    rows = []
    for vm in sorted(instances(c), key=lambda v: v.get("name", "")):
        r = vm.get("resources") or {}
        args = {"name": vm["id"]}
        actions = []
        if vm.get("status") == "RUNNING":
            actions += [{"title": c.t("restart"), "op": "vm_restart", "args": args, "confirm": c.t("restart_confirm", name=vm["name"])},
                        {"title": c.t("stop"), "op": "vm_stop", "args": args, "danger": True, "confirm": c.t("stop_confirm", name=vm["name"])}]
        elif vm.get("status") in ("STOPPED", "ERROR", "CRASHED"):
            actions.append({"title": c.t("start"), "op": "vm_start", "args": args})
        actions.append({"title": c.t("snapshot"), "op": "vm_snapshot", "args": args})
        rows.append({"cells": {"name": vm.get("name", ""), "folder": vm.get("folderId", ""), "zone": vm.get("zoneId", ""), "status": vm.get("status", ""),
                               "res": f"{r.get('cores', '?')} vCPU · {gb(r.get('memory'))}" + (f" · {r['coreFraction']}%" if r.get("coreFraction") and r["coreFraction"] != "100" else ""),
                               "ip": ", ".join(ips(vm))}, "actions": actions})
    return {"type": "table", "title": c.t("vms"), "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("name", "folder", "zone", "status", "res", "ip")]}


def disks_view(c):
    names = {vm["id"]: vm["name"] for vm in instances(c)}
    disks = each_folder(c, "compute", "/compute/v1/disks", "disks")
    snaps = each_folder(c, "compute", "/compute/v1/snapshots", "snapshots")
    drows = [{"cells": {"name": d.get("name") or d["id"], "size": gb(d.get("size")), "type": d.get("typeId", ""), "attached": ", ".join(names.get(i, i) for i in d.get("instanceIds") or []),
                        "status": d.get("status", "")},
              "actions": [{"title": c.t("snapshot"), "op": "disk_snapshot", "args": {"disk": d["id"]}}]} for d in sorted(disks, key=lambda d: d.get("name", ""))]
    dn = {d["id"]: d.get("name") or d["id"] for d in disks}
    srows = [{"cells": {"name": s.get("name") or s["id"], "disk": dn.get(s.get("sourceDiskId"), s.get("sourceDiskId", "")), "size": gb(s.get("storageSize")),
                        "created": str(s.get("createdAt", ""))[:16].replace("T", " ")},
              "actions": [{"title": c.t("delete_snapshot"), "op": "snapshot_delete", "args": {"id": s["id"]}, "danger": True,
                           "confirm": c.t("delsnap_confirm", name=s.get("name") or s["id"])}]} for s in sorted(snaps, key=lambda s: s.get("createdAt", ""), reverse=True)]
    return {"type": "sections", "items": [
        {"type": "table", "title": c.t("disks"), "rows": drows, "columns": [{"key": k, "title": c.t(k)} for k in ("name", "size", "type", "attached", "status")]},
        {"type": "table", "title": c.t("snapshots"), "rows": srows, "columns": [{"key": k, "title": c.t(k)} for k in ("name", "disk", "size", "created")]}]}


def security_view(c):
    rows = []
    for sg in sorted(security_groups(c), key=lambda s: s.get("name", "")):
        opened = open_rules(sg)
        rows.append({"cells": {"group": sg.get("name", ""), "folder": sg.get("folderId", ""), "rule": len(sg.get("rules") or []),
                               "open": ", ".join(f"{o['port']}/{o['proto']}" + (" ⚠" if o["port"] in RISKY_PORTS else "") for o in opened)}})
    return {"type": "table", "title": c.t("security"), "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("group", "folder", "rule", "open")]}


def storage_view(c):
    rows = []
    for b in sorted(each_folder(c, "storage", "/storage/v1/buckets", "buckets"), key=lambda b: b.get("name", "")):
        f = b.get("anonymousAccessFlags") or {}
        pub = [k for k in ("read", "list", "configRead") if f.get(k)]
        size = ""
        try:
            size = gb(yc(c, "storage", "GET", f"/storage/v1/buckets/{urllib.parse.quote(b['name'])}:getStats").get("usedSize"))
        except Fail:
            pass
        rows.append({"cells": {"bucket": b.get("name", ""), "folder": b.get("folderId", ""), "access": c.t("public_bucket", what=", ".join(pub)) + " ⚠" if pub else c.t("private"), "size": size}})
    return {"type": "table", "title": c.t("storage"), "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("bucket", "folder", "access", "size")]}


def dns_view(c):
    rows = [{"cells": {"zone": name, "public": "✓" if z.get("publicVisibility") is not None else "", "folder": z.get("folderId", "")},
             "actions": [{"title": c.t("open_records"), "open": "dns_records", "args": {"zone": name}}]} for name, z in sorted(zones(c).items())]
    return {"type": "table", "title": c.t("dns"), "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("zone", "public", "folder")]}


def records_view(c, zone):
    zs = zones(c)
    z = zs.get(str(zone or "").rstrip(".").lower())
    if not z:
        raise c.fail("no_zone", zone=zone)
    rows = [{"cells": {"name": r["name"], "type": r["type"], "ttl": r.get("ttl", ""), "value": ", ".join(r.get("data") or [])}} for r in sorted(record_sets(c, z["id"]), key=lambda r: (r["name"], r["type"]))]
    return {"type": "table", "title": f"{c.t('dns')} — {zone}", "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("name", "type", "ttl", "value")]}


def databases_view(c):
    rows = []
    for eng, path in (("PostgreSQL", "/managed-postgresql/v1/clusters"), ("MySQL", "/managed-mysql/v1/clusters")):
        for cl in each_folder(c, "mdb", path, "clusters"):
            rows.append({"cells": {"name": cl.get("name", ""), "engine": eng, "folder": cl.get("folderId", ""), "env": cl.get("environment", ""),
                                   "status": cl.get("status", ""), "health": cl.get("health", "")}})
    return {"type": "table", "title": c.t("databases"), "rows": rows, "columns": [{"key": k, "title": c.t(k)} for k in ("name", "engine", "folder", "env", "status", "health")]}


def billing_view(c):
    if not BILLING:
        return {"type": "text", "title": c.t("billing"), "text": c.t("no_billing")}
    a = yc(c, "billing", "GET", f"/billing/v1/billingAccounts/{urllib.parse.quote(BILLING)}")
    return {"type": "text", "title": c.t("billing"), "text": f"{c.t('account')}: {a.get('name', BILLING)}\n{c.t('balance')}: {a.get('balance', '?')} {a.get('currency', '')}"}


# --- ВМ как хост nkt ----------------------------------------------------------------

def vm_info(c, vm):
    r = vm.get("resources") or {}
    return {"name": vm.get("name", ""), "os": "Yandex Cloud VM", "version": vm.get("platformId", ""), "model": f"{r.get('cores', '?')} vCPU, {gb(r.get('memory'))}",
            "serial": vm.get("id", ""), "address": public_ip(vm) or (ips(vm) or [""])[0]}


def vm_snapshot_model(c, vm):
    sgs = {s["id"]: s for s in security_groups(c)}
    ifs = []
    for i, n in enumerate(vm.get("networkInterfaces") or []):
        v4 = n.get("primaryV4Address") or {}
        addrs = [a for a in (v4.get("address"), (v4.get("oneToOneNat") or {}).get("address")) if a]
        ifs.append({"name": f"eth{i}", "type": n.get("subnetId", ""), "mac": n.get("macAddress", ""), "addrs": addrs, "up": vm.get("status") == "RUNNING",
                    "comment": ", ".join(sgs.get(s, {}).get("name", s) for s in n.get("securityGroupIds") or [])})
    ports = []
    for n in vm.get("networkInterfaces") or []:
        for sid in n.get("securityGroupIds") or []:
            for o in open_rules(sgs.get(sid, {})):
                if o["port"].isdigit():
                    ports.append({"proto": o["proto"].lower(), "port": int(o["port"]), "service": sgs[sid].get("name", ""), "allowed": ""})
    disks = [{"name": d.get("deviceName") or d.get("diskId", ""), "mount": d.get("diskId", "")} for d in [vm.get("bootDisk") or {}] + (vm.get("secondaryDisks") or []) if d]
    r = vm.get("resources") or {}
    return {"info": vm_info(c, vm), "cpu": {"cores": int(r.get("cores") or 0)}, "mem_total": int(r.get("memory") or 0), "interfaces": ifs, "ports": ports, "disks": disks,
            "extra": [{"type": "table", "title": c.t("security"), "columns": [{"key": "group", "title": c.t("group")}, {"key": "open", "title": c.t("open")}],
                       "rows": [{"cells": {"group": sgs.get(s, {}).get("name", s), "open": ", ".join(f"{o['port']}/{o['proto']}" for o in open_rules(sgs.get(s, {})))}}
                                for n in vm.get("networkInterfaces") or [] for s in n.get("securityGroupIds") or []]}]}


def vm_findings(c, vm):
    out = []
    if vm.get("status") not in ("RUNNING", "PROVISIONING", "STARTING"):
        out.append({"id": "stopped", "severity": "info", "title": c.t("f_stopped", status=vm.get("status", "?"))})
    if (vm.get("schedulingPolicy") or {}).get("preemptible"):
        out.append({"id": "preemptible", "severity": "info", "title": c.t("f_preempt")})
    sgs = {s["id"]: s for s in security_groups(c)}
    if public_ip(vm):
        ids = [s for n in vm.get("networkInterfaces") or [] for s in n.get("securityGroupIds") or []]
        if not ids:
            out.append({"id": "no-sg", "severity": "warning", "title": c.t("f_no_sg")})
        for sid in ids:
            for o in open_rules(sgs.get(sid, {})):
                if o["port"] in RISKY_PORTS:
                    out.append({"id": f"open-{sid}-{o['port']}", "severity": "warning" if o["port"] != "any" else "critical",
                                "title": c.t("f_open", group=sgs[sid].get("name", sid), port=o["port"], proto=o["proto"]), "fix": c.t("f_open_fix")})
    boot = (vm.get("bootDisk") or {}).get("diskId")
    if boot:
        snaps = [s for s in each_folder(c, "compute", "/compute/v1/snapshots", "snapshots") if s.get("sourceDiskId") == boot]
        recent = [s for s in snaps if str(s.get("createdAt", "")) >= time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(time.time() - 7 * 86400))]
        if not recent:
            out.append({"id": "no-snapshot", "severity": "warning", "title": c.t("f_no_snap"), "fix": c.t("f_no_snap_fix")})
    return out


def vm_config(vm):
    keep = {k: vm.get(k) for k in ("name", "description", "zoneId", "platformId", "resources", "labels", "schedulingPolicy", "serviceAccountId", "networkSettings")}
    keep["metadataKeys"] = sorted((vm.get("metadata") or {}).keys())  # значения метаданных — секреты (user-data), только имена
    keep["bootDisk"] = {k: (vm.get("bootDisk") or {}).get(k) for k in ("autoDelete", "deviceName", "diskId", "mode")}
    keep["secondaryDisks"] = [{k: d.get(k) for k in ("autoDelete", "deviceName", "diskId", "mode")} for d in vm.get("secondaryDisks") or []]
    keep["networkInterfaces"] = [{"subnetId": n.get("subnetId"), "securityGroupIds": sorted(n.get("securityGroupIds") or []),
                                  "nat": bool((n.get("primaryV4Address") or {}).get("oneToOneNat"))} for n in vm.get("networkInterfaces") or []]
    return json.dumps(keep, ensure_ascii=False, indent=2, sort_keys=True) + "\n"


def host_call(c, op, args, dry):
    conn = args.get("conn") or {}
    vm = find_vm(c, conn.get("address"), str(conn.get("folder") or "").strip())
    if op == "_host_check":
        return {"ok": True, "result": vm_info(c, vm)}
    if op == "_host_snapshot":
        return {"ok": True, "result": vm_snapshot_model(c, vm)}
    if op == "_host_findings":
        return {"ok": True, "result": {"findings": vm_findings(c, vm)}}
    if op == "_host_config":
        return {"ok": True, "result": {"text": vm_config(vm)}}
    if op == "_host_action":
        act = str(args.get("action") or "")
        if act in ("start", "stop", "restart"):
            return {"ok": True, "text": vm_action(c, vm["id"], act, dry)}
        if act == "snapshot":
            return {"ok": True, "text": vm_snapshot(c, vm["id"], dry)}
        return {"ok": False, "error": f"no action {act}"}
    return {"ok": False, "error": f"no op {op}"}


# --- команды ------------------------------------------------------------------------

def command(c, argv, dry, check):
    if len(argv) < 3 or argv[0] != "yc":
        raise Fail(c.t("usage"))
    sub, rest = argv[1], argv[2:]
    if sub == "vm" and len(rest) == 2 and rest[0] in ("start", "stop", "restart"):
        return [] if check else [vm_action(c, rest[1], rest[0], dry)]
    if sub == "snapshot" and len(rest) == 1:
        return [] if check else [vm_snapshot(c, rest[0], dry)]
    if sub == "dns" and rest and rest[0] == "set" and len(rest) >= 4:
        ttl = 300
        if len(rest) >= 6 and rest[-2] == "ttl":
            ttl, rest = int(rest[-1]), rest[:-2]
        if rest[2].upper() not in TYPES:
            raise Fail(c.t("usage"))
        return [] if check else dns_set(c, rest[1], rest[2], [v.strip() for v in " ".join(rest[3:]).split(",") if v.strip()], ttl, dry)
    if sub == "dns" and len(rest) == 3 and rest[0] == "rm":
        return [] if check else dns_set(c, rest[1], rest[2], [], 300, dry)
    raise Fail(c.t("usage"))


# --- манифест и вызовы --------------------------------------------------------------

def schema(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)}


def manifest(c):
    S = {"type": "string"}
    vm = {"name": {"type": "string", "description": "VM name or id"}}
    return {
        "protocol": 1, "name": "yc", "title": c.t("title"), "version": VERSION, "description": c.t("desc"),
        "ops": [
            {"name": "vms", "title": c.t("vms"), "read_only": True, "mcp": True, "description": "Yandex Cloud VMs: folder, zone, status, resources, addresses."},
            {"name": "disks", "title": c.t("disks"), "read_only": True, "mcp": True, "description": "Disks and snapshots."},
            {"name": "security", "title": c.t("security"), "read_only": True, "mcp": True, "description": "Security groups with ingress rules open to the whole internet."},
            {"name": "storage", "title": c.t("storage"), "read_only": True, "mcp": True, "description": "Object Storage buckets: public access, size."},
            {"name": "dns", "title": c.t("dns"), "read_only": True, "mcp": True, "description": "Cloud DNS zones."},
            {"name": "dns_records", "read_only": True, "mcp": True, "description": "Record sets of a Cloud DNS zone.", "input": schema({"zone": S}, ["zone"])},
            {"name": "databases", "title": c.t("databases"), "read_only": True, "mcp": True, "description": "Managed PostgreSQL and MySQL clusters: status, health."},
            {"name": "billing", "title": c.t("billing"), "read_only": True, "mcp": True, "description": "Billing account balance."},
            {"name": "vm_start", "title": c.t("start"), "mcp": True, "description": "Start a VM.", "input": schema(vm, ["name"])},
            {"name": "vm_stop", "title": c.t("stop"), "mcp": True, "destructive": True, "description": "Stop a VM.", "input": schema(vm, ["name"])},
            {"name": "vm_restart", "title": c.t("restart"), "mcp": True, "destructive": True, "description": "Restart a VM.", "input": schema(vm, ["name"])},
            {"name": "vm_snapshot", "title": c.t("snapshot"), "mcp": True, "description": "Snapshot a VM's boot disk.", "input": schema(vm, ["name"])},
            {"name": "disk_snapshot", "title": c.t("snapshot"), "description": "Snapshot a disk.", "input": schema({"disk": S}, ["disk"])},
            {"name": "snapshot_delete", "title": c.t("delete_snapshot"), "mcp": True, "destructive": True, "description": "Delete a snapshot.", "input": schema({"id": S}, ["id"])},
            {"name": "dns_set", "mcp": True, "description": "Set exactly these values of a type for a name in Cloud DNS.",
             "input": schema({"name": S, "type": {"type": "string", "enum": list(TYPES)}, "values": {"type": "array", "items": S}, "ttl": {"type": "integer"}}, ["name", "type", "values"])},
        ],
        "pages": [{"id": p, "title": c.t(p), "op": p} for p in ("vms", "disks", "security", "storage", "dns", "databases", "billing")] +
                 [{"id": "dns_records", "title": c.t("open_records"), "op": "dns_records", "hidden": True}],
        "commands": [{"word": "yc", "usage": c.t("usage"), "example": "yc vm restart web-1\nyc snapshot web-1\nyc dns set app.example.com A 203.0.113.10 ttl 300"}],
        "provides": ["dns", "hosts"],
        "host_types": [{"id": "yc-vm", "title": c.t("vm_type"), "hint": c.t("vm_hint"),
                        "fields": [{"name": "address", "title": c.t("vm_addr"), "required": True, "hint": c.t("vm_addr_hint")},
                                   {"name": "folder", "title": c.t("folder")}],
                        "sections": ["overview", "findings", "config", "actions"],
                        "actions": [{"title": c.t("start"), "op": "start"}, {"title": c.t("restart"), "op": "restart", "confirm": c.t("restart_this")},
                                    {"title": c.t("stop"), "op": "stop", "danger": True, "confirm": c.t("stop_this")},
                                    {"title": c.t("snapshot"), "op": "snapshot"}]}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


VIEWS = {"vms": vms_view, "disks": disks_view, "security": security_view, "storage": storage_view, "dns": dns_view, "databases": databases_view, "billing": billing_view}


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if op in VIEWS:
        return {"ok": True, "view": VIEWS[op](c)}
    if op == "dns_records":
        return {"ok": True, "view": records_view(c, args.get("zone"))}
    if op.startswith("_host_"):
        return host_call(c, op, args, dry)
    if op == "_dns_zones":
        return {"ok": True, "result": {"zones": sorted(zones(c))}}
    if op == "_check":
        command(c, [str(a) for a in args.get("argv") or []], True, True)
        return {"ok": True, "text": c.t("check_ok")}
    if op == "_command":
        lines = command(c, [str(a) for a in args.get("argv") or []], dry, False)
    elif op == "_dns_sync":
        lines = dns_sync(c, args, dry)
    elif op in ("vm_start", "vm_stop", "vm_restart"):
        lines = [vm_action(c, args.get("name"), op[3:], dry)]
    elif op == "vm_snapshot":
        lines = [vm_snapshot(c, args.get("name"), dry)]
    elif op == "disk_snapshot":
        d = next((x for x in each_folder(c, "compute", "/compute/v1/disks", "disks") if x["id"] == args.get("disk")), None)
        if not d:
            raise c.fail("no_disk", name=args.get("disk"))
        lines = [snapshot_disk(c, d["id"], d.get("name") or d["id"], d["folderId"], dry)]
    elif op == "snapshot_delete":
        sid = str(args.get("id") or "")
        if not re.match(r"^[a-z0-9]{10,40}$", sid):
            raise c.fail("no_disk", name=sid)
        lines = [c.t("would", what=f"{c.t('delete_snapshot')}: {sid}")] if dry else \
            [c.t("done", what=c.t("delete_snapshot"), name=sid, op=yc(c, "compute", "DELETE", f"/compute/v1/snapshots/{sid}").get("id", "?"))]
    elif op == "dns_set":
        vals = args.get("values")
        if isinstance(vals, str):
            vals = [v.strip() for v in vals.split(",") if v.strip()]
        lines = dns_set(c, args.get("name", ""), args.get("type", ""), vals or [], args.get("ttl") or 300, dry)
    else:
        return {"ok": False, "error": f"no op {op}"}
    if not dry and lines:
        log(f"{req.get('user', '')}: {op} → {len(lines)}")
    return {"ok": True, "text": "\n".join(lines) or c.t("nothing")}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-yc/" + VERSION

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
    log(f"{VERSION}: {LISTEN}:{PORT}, каталогов: {len(FOLDERS)}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
