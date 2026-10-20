# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
"""Первичная настройка Garage через админ-API (v2) — разовый сервис
garage-init, при каждой выкладке, повтор ничего не ломает:

  - раскладка: узлу — роль с ёмкостью CAPACITY (без неё Garage не хранит
    данные);
  - бакет BUCKET;
  - ключ доступа: идентификатор GK + S3_KEY_HEX, секрет S3_SECRET — оба из
    .env (создаёт хаб), права на бакет — чтение, запись, владелец.
"""
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

API = os.environ.get("GARAGE_API", "http://garage:3903") + "/v2"
TOKEN = os.environ["GARAGE_ADMIN_TOKEN"]


def log(msg):
    print(f"garage-init: {msg}", flush=True)


def call(name, body=None, query=""):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(f"{API}/{name}{query}", data=data, headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            raw = r.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise RuntimeError(f"{name}: HTTP {e.code} {e.read()[:300]!r}")


def capacity():
    """CAPACITY (100G, 500G, 2T…) — в байтах, степени 1000, как у Garage."""
    m = re.fullmatch(r"(\d+)\s*([KMGT]?)B?", os.environ.get("CAPACITY", "100G").strip().upper())
    if not m:
        raise RuntimeError("CAPACITY: число с K, M, G или T, например 100G")
    power = " KMGT".index(m.group(2) or " ")
    return int(m.group(1)) * 1000**power


def wait():
    deadline = time.time() + 180
    while True:
        try:
            return call("GetClusterStatus")
        except (urllib.error.URLError, ConnectionError) as e:
            if time.time() > deadline:
                raise RuntimeError(f"admin API {API} не отвечает: {e}")
            time.sleep(3)


def main():
    st = wait()
    node = st["nodes"][0]
    if node.get("role") is None:
        layout = call("GetClusterLayout")
        call("UpdateClusterLayout", {"roles": [{"id": node["id"], "zone": "dc1", "capacity": capacity(), "tags": ["nkt"]}]})
        call("ApplyClusterLayout", {"version": layout["version"] + 1})
        log(f"раскладка: узел {node['id'][:16]}…, ёмкость {os.environ.get('CAPACITY', '100G')}")
    bucket = os.environ.get("BUCKET", "").strip()
    if not bucket:
        log("готово")
        return
    info = call("GetBucketInfo", query="?globalAlias=" + bucket)
    if info is None:
        info = call("CreateBucket", {"globalAlias": bucket})
        log(f"бакет {bucket} создан")
    key_id = "GK" + os.environ["S3_KEY_HEX"].strip().lower()
    if call("GetKeyInfo", query="?id=" + key_id) is None:
        call("ImportKey", {"accessKeyId": key_id, "secretAccessKey": os.environ["S3_SECRET"].strip(), "name": "nkt-" + bucket})
        log(f"ключ {key_id} заведён")
    call("AllowBucketKey", {"bucketId": info["id"], "accessKeyId": key_id, "permissions": {"read": True, "write": True, "owner": True}})
    log(f"ключ {key_id}: чтение, запись, владелец бакета {bucket}")
    log("готово")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        log(f"ошибка: {e}")
        sys.exit(1)
