# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
"""Первичная настройка Technitium DNS Server через его API — разовый сервис
dns-init стека, запускается при каждой выкладке и ничего не ломает
повтором.

Первичный сервер (ROLE=primary):
  - ключи TSIG: cert-manager (динамические обновления) и zone-transfer
    (перенос зон на вторичный); чужие ключи из консоли остаются;
  - DoH за прокси сайта: адреса прокси, которым верить в X-Real-IP;
  - зоны из ZONES (нет — создаёт); перенос и NOTIFY — только на
    SECONDARY_IP и только с ключом zone-transfer; NS вторичного сервера;
  - зона ACME_ZONE: динамические обновления только с ключом cert-manager и
    только TXT;
  - зона каталога CATALOG_ZONE (RFC 9432, если есть вторичный): все зоны —
    её члены, и новая зона (из консоли или плагина хаба nkt) сама
    появляется на вторичном;
  - приложение Failover (если не INSTALL_FAILOVER=false).
Вторичный (ROLE=secondary): ключ zone-transfer, вторичная зона каталога с
первичного PRIMARY_IP (зоны приходят с ней), приложение Failover. Каталог
выключен (CATALOG_ZONE пустая) — вторичные зоны из ZONES, как раньше.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

API = os.environ.get("DNS_API", "http://dns:5380/api")
ROLE = os.environ.get("ROLE", "primary")
ZONES = [z.strip().rstrip(".").lower() for z in os.environ.get("ZONES", "").split(",") if z.strip()]
ACME_ZONE = os.environ.get("ACME_ZONE", "").strip().rstrip(".").lower()
# Зоны стека: ZONES и зона проверок ACME_ZONE (если её нет в списке).
ALL_ZONES = ZONES + ([ACME_ZONE] if ACME_ZONE and ACME_ZONE not in ZONES else [])
# Зона каталога: служебная, в DNS не делегируется (.invalid).
CATALOG = os.environ.get("CATALOG_ZONE", "catalog.nkt.invalid").strip().rstrip(".").lower()
TOKEN = ""


def log(msg):
    print(f"dns-init: {msg}", flush=True)


def raw(path, params):
    """Вызов API (POST, форма): весь ответ; не ok — ошибка с текстом сервера."""
    data = urllib.parse.urlencode(params).encode()
    with urllib.request.urlopen(urllib.request.Request(f"{API}/{path}", data=data), timeout=120) as r:
        res = json.load(r)
    if res.get("status") != "ok":
        raise RuntimeError(f"{path}: {res.get('errorMessage') or res.get('status')}")
    return res


def call(path, **params):
    return raw(path, {**params, "token": TOKEN}).get("response", {})


def login():
    """Вход администратора; сервер ещё поднимается — ждём до трёх минут.
    Пароль — DNS_ADMIN_PASSWORD из .env: Technitium берёт его только при
    первом запуске, сменили в консоли — поменяйте и в .env."""
    global TOKEN
    deadline = time.time() + 180
    while True:
        try:
            TOKEN = raw("user/login", {"user": "admin", "pass": os.environ["DNS_ADMIN_PASSWORD"]})["token"]
            return
        except (urllib.error.URLError, ConnectionError) as e:
            if time.time() > deadline:
                raise RuntimeError(f"API {API} не отвечает: {e}")
            time.sleep(3)


def set_tsig(wanted):
    """Свои ключи — с нашими секретами; остальные ключи сервера — как были."""
    keys = {k["keyName"]: k for k in call("settings/get").get("tsigKeys", [])}
    for name, secret in wanted.items():
        keys[name] = {"keyName": name, "sharedSecret": secret, "algorithmName": "hmac-sha256"}
    flat = []
    for k in keys.values():
        flat += [k["keyName"], k["sharedSecret"], k["algorithmName"]]
    call("settings/set", tsigKeys="|".join(flat))


def zones():
    return {z["name"].lower(): z for z in call("zones/list").get("zones", [])}


def install_failover():
    if os.environ.get("INSTALL_FAILOVER", "true").lower() == "false":
        return
    try:
        if any(a["name"] == "Failover" for a in call("apps/list").get("apps", [])):
            return
        # Каталог — с go.technitium.com: сразу после запуска сервер сам
        # ещё не разогнал резолвинг, первая попытка часто по тайм-ауту.
        for attempt in range(4):
            try:
                store = call("apps/listStoreApps").get("storeApps", [])
                break
            except RuntimeError:
                if attempt == 3:
                    raise
                time.sleep(10)
        app = next((a for a in store if a["name"] == "Failover"), None)
        if not app:
            log("Failover нет в каталоге приложений — пропускаю")
            return
        call("apps/downloadAndInstall", name="Failover", url=app["url"])
        log(f"приложение Failover {app['version']} установлено")
    except Exception as e:  # каталог недоступен — не повод ронять выкладку
        log(f"Failover не установлен: {e} (поставьте из консоли: Apps → App Store)")


def primary():
    tsig = {"zone-transfer": os.environ["TSIG_TRANSFER"]}
    if ACME_ZONE:
        tsig["cert-manager"] = os.environ["TSIG_CERT_MANAGER"]
    set_tsig(tsig)
    acl = os.environ.get("REVERSE_PROXY_ACL", "172.16.0.0/12,192.168.0.0/16,10.0.0.0/8")
    call("settings/set", dnsReverseProxyNetworkACL=acl)
    secondary_ip = os.environ.get("SECONDARY_IP", "").strip()
    secondary_ns = os.environ.get("SECONDARY_NS", "").strip().rstrip(".")
    have = zones()
    catalog = bool(CATALOG and secondary_ip)
    if catalog:
        if CATALOG not in have:
            call("zones/create", zone=CATALOG, type="Catalog")
            log(f"зона каталога {CATALOG} создана")
        call("zones/options/set", zone=CATALOG, zoneTransfer="UseSpecifiedNetworkACL",
             zoneTransferNetworkACL=secondary_ip, zoneTransferTsigKeyNames="zone-transfer",
             notify="SpecifiedNameServers", notifyNameServers=secondary_ip)
    for z in ALL_ZONES:
        if z not in have:
            call("zones/create", zone=z, type="Primary", **({"catalog": CATALOG} if catalog else {}))
            log(f"зона {z} создана")
        elif catalog and not have[z].get("catalog"):
            call("zones/options/set", zone=z, catalog=CATALOG)
            log(f"{z}: в каталоге {CATALOG}")
        if secondary_ip:
            call("zones/options/set", zone=z, zoneTransfer="UseSpecifiedNetworkACL",
                 zoneTransferNetworkACL=secondary_ip, zoneTransferTsigKeyNames="zone-transfer",
                 notify="SpecifiedNameServers", notifyNameServers=secondary_ip)
        else:
            call("zones/options/set", zone=z, zoneTransfer="Deny", notify="None")
        if secondary_ns:
            ns = call("zones/records/get", domain=z, zone=z).get("records", [])
            if not any(r["type"] == "NS" and r["rData"].get("nameServer", "").rstrip(".").lower() == secondary_ns.lower() for r in ns):
                call("zones/records/add", zone=z, domain=z, type="NS", nameServer=secondary_ns, ttl="3600")
                log(f"{z}: NS {secondary_ns}")
    if ACME_ZONE:
        call("zones/options/set", zone=ACME_ZONE, update="Allow",
             updateSecurityPolicies=f"cert-manager|*.{ACME_ZONE}|TXT|cert-manager|{ACME_ZONE}|TXT")
        log(f"{ACME_ZONE}: обновления — только TXT и только с ключом cert-manager")
    install_failover()


def secondary():
    set_tsig({"zone-transfer": os.environ["TSIG_TRANSFER"]})
    primary_ip = os.environ["PRIMARY_IP"].strip()
    have = zones()
    if CATALOG:
        # Зоны приходят с каталогом; заведённые раньше вручную — остаются.
        if CATALOG not in have:
            call("zones/create", zone=CATALOG, type="SecondaryCatalog", primaryNameServerAddresses=primary_ip,
                 zoneTransferProtocol="Tcp", tsigKeyName="zone-transfer")
            log(f"вторичная зона каталога {CATALOG} с {primary_ip}: зоны первичного придут с ней")
        install_failover()
        return
    for z in ALL_ZONES:
        if z in have:
            continue
        call("zones/create", zone=z, type="Secondary", primaryNameServerAddresses=primary_ip,
             zoneTransferProtocol="Tcp", tsigKeyName="zone-transfer")
        log(f"вторичная зона {z} с {primary_ip}")
    install_failover()


def main():
    login()
    if ROLE == "secondary":
        secondary()
    else:
        primary()
    log("готово")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        log(f"ошибка: {e}")
        sys.exit(1)
