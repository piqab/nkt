"""Проверка плагина Yandex Cloud на поддельном API (без облака): IAM
проверяет подпись JWT (PS256) настоящим openssl по открытому ключу, дальше —
ВМ, диски и снимки, группы безопасности, бакеты, Cloud DNS, базы, баланс.
Плагин запускается отдельным процессом, вызовы — подписанные, как от хаба.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import base64
import hashlib
import hmac
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"


def b64u_dec(s):
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


class FakeYC:
    def __init__(self, pubkey):
        self.pubkey = pubkey
        self.calls = []
        self.jwts = []
        self.lock = threading.Lock()
        self.vms = [
            {"id": "fhm1", "name": "web-1", "folderId": "b1gf", "zoneId": "ru-central1-a", "status": "RUNNING", "platformId": "standard-v3",
             "resources": {"cores": "2", "memory": str(4 * 2**30), "coreFraction": "100"}, "metadata": {"user-data": "SECRET-CLOUD-INIT", "ssh-keys": "x"},
             "bootDisk": {"diskId": "d1", "deviceName": "boot", "autoDelete": True, "mode": "READ_WRITE"},
             "networkInterfaces": [{"subnetId": "e9b", "macAddress": "d0:0d:aa:bb:cc:dd", "securityGroupIds": ["sg1"],
                                    "primaryV4Address": {"address": "10.128.0.5", "oneToOneNat": {"address": "51.250.1.2"}}}]},
            {"id": "fhm2", "name": "db-1", "folderId": "b1gf", "zoneId": "ru-central1-b", "status": "STOPPED", "platformId": "standard-v3",
             "resources": {"cores": "4", "memory": str(8 * 2**30)}, "schedulingPolicy": {"preemptible": True}, "bootDisk": {"diskId": "d2"},
             "networkInterfaces": [{"subnetId": "e9c", "primaryV4Address": {"address": "10.129.0.7"}}]},
        ]
        self.records = {"dz1": [{"name": "example.com.", "type": "NS", "ttl": 3600, "data": ["ns1.yandexcloud.net."]},
                                {"name": "fly.example.com.", "type": "A", "ttl": 300, "data": ["192.0.2.9"]}]}
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, obj, code=200):
                raw = json.dumps(obj).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def route(self, method):
                u = urllib.parse.urlsplit(self.path)
                q = dict(urllib.parse.parse_qsl(u.query))
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                p = u.path
                if p == "/iam/v1/tokens":
                    return self.send({"iamToken": "t1.test"} if fake.verify(body.get("jwt", "")) else {"message": "bad jwt"}, 200 if fake.verify(body.get("jwt", "")) else 401)
                if self.headers.get("Authorization") != "Bearer t1.test":
                    return self.send({"message": "unauthenticated"}, 401)
                with fake.lock:
                    fake.calls.append((method, p, body))
                if p == "/compute/v1/instances":
                    return self.send({"instances": [v for v in fake.vms if v["folderId"] == q.get("folderId")]})
                if p.startswith("/compute/v1/instances/") and ":" in p:
                    return self.send({"id": "op-" + p.rsplit(":", 1)[1]})
                if p == "/compute/v1/disks":
                    return self.send({"disks": [{"id": "d1", "name": "web-1-boot", "size": str(20 * 2**30), "typeId": "network-ssd", "instanceIds": ["fhm1"], "status": "READY"},
                                                {"id": "d2", "name": "db-1-boot", "size": str(50 * 2**30), "typeId": "network-hdd", "instanceIds": ["fhm2"], "status": "READY"}]})
                if p == "/compute/v1/snapshots" and method == "GET":
                    recent = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                    return self.send({"snapshots": [{"id": "fd8snap0000001", "name": "web-1-daily", "sourceDiskId": "d1", "storageSize": str(2**30), "createdAt": recent}]})
                if p == "/compute/v1/snapshots" and method == "POST":
                    return self.send({"id": "op-snap"})
                if p.startswith("/compute/v1/snapshots/") and method == "DELETE":
                    return self.send({"id": "op-del"})
                if p == "/vpc/v1/securityGroups":
                    return self.send({"securityGroups": [{"id": "sg1", "name": "web", "rules": [
                        {"direction": "INGRESS", "ports": {"fromPort": "443", "toPort": "443"}, "protocolName": "TCP", "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}},
                        {"direction": "INGRESS", "ports": {"fromPort": "22", "toPort": "22"}, "protocolName": "TCP", "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}},
                        {"direction": "EGRESS", "protocolName": "ANY", "cidrBlocks": {"v4CidrBlocks": ["0.0.0.0/0"]}}]}]})
                if p == "/storage/v1/buckets":
                    return self.send({"buckets": [{"name": "site-assets", "anonymousAccessFlags": {"read": True}}, {"name": "backups", "anonymousAccessFlags": {}}]})
                if p.startswith("/storage/v1/buckets/") and p.endswith(":getStats"):
                    return self.send({"usedSize": str(3 * 2**30)})
                if p == "/dns/v1/zones":
                    return self.send({"dnsZones": [{"id": "dz1", "zone": "example.com.", "publicVisibility": {}}]})
                if p == "/dns/v1/zones/dz1:listRecordSets":
                    return self.send({"recordSets": fake.records["dz1"]})
                if p == "/dns/v1/zones/dz1:upsertRecordSets":
                    with fake.lock:
                        rs = fake.records["dz1"]
                        for d in body.get("deletions") or []:
                            rs[:] = [r for r in rs if not (r["name"] == d["name"] and r["type"] == d["type"])]
                        for r in body.get("replacements") or []:
                            rs[:] = [x for x in rs if not (x["name"] == r["name"] and x["type"] == r["type"])] + [r]
                    return self.send({"id": "op-dns"})
                if p == "/managed-postgresql/v1/clusters":
                    return self.send({"clusters": [{"name": "pg-main", "environment": "PRODUCTION", "status": "RUNNING", "health": "ALIVE"}]})
                if p == "/managed-mysql/v1/clusters":
                    return self.send({"clusters": []})
                if p == "/billing/v1/billingAccounts/dn2acc":
                    return self.send({"id": "dn2acc", "name": "main", "balance": "1234.56", "currency": "RUB"})
                return self.send({"message": "no route " + p}, 404)

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

            def do_DELETE(self):
                self.route("DELETE")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def verify(self, jwt):
        """Подпись PS256 — настоящим openssl по открытому ключу; поля JWT."""
        try:
            head, body, sig = jwt.split(".")
            h, b = json.loads(b64u_dec(head)), json.loads(b64u_dec(body))
        except ValueError:
            return False
        if h != {"typ": "JWT", "alg": "PS256", "kid": "ajekey123"} or b.get("iss") != "ajesa456" or b.get("aud") != "https://iam.api.cloud.yandex.net/iam/v1/tokens":
            return False
        d = tempfile.mkdtemp()
        try:
            sp = os.path.join(d, "sig")
            with open(sp, "wb") as f:
                f.write(b64u_dec(sig))
            r = subprocess.run(["openssl", "dgst", "-sha256", "-verify", self.pubkey, "-sigopt", "rsa_padding_mode:pss", "-sigopt", "rsa_pss_saltlen:32", "-signature", sp],
                               input=f"{head}.{body}".encode(), capture_output=True)
            ok = r.returncode == 0
        finally:
            shutil.rmtree(d, ignore_errors=True)
        with self.lock:
            self.jwts.append(ok)
        return ok


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@unittest.skipIf(shutil.which("openssl") is None, "no openssl")
class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        key, pub = os.path.join(cls.tmp, "k.pem"), os.path.join(cls.tmp, "pub.pem")
        subprocess.run(["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", key], check=True, capture_output=True)
        subprocess.run(["openssl", "pkey", "-in", key, "-pubout", "-out", pub], check=True, capture_output=True)
        with open(key) as f:
            pem = f.read()
        sa_key = {"id": "ajekey123", "service_account_id": "ajesa456", "created_at": "2026-01-01T00:00:00Z", "key_algorithm": "RSA_2048",
                  "public_key": "-", "private_key": "PLEASE DO NOT REMOVE THIS LINE! Yandex.Cloud SA Key ID <ajekey123>\n" + pem}
        cls.api = FakeYC(pub)
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   YC_ENDPOINT=f"http://127.0.0.1:{cls.api.srv.server_address[1]}", YC_FOLDERS="b1gf", YC_BILLING_ACCOUNT="dn2acc",
                   YC_SA_KEY_B64=base64.b64encode(json.dumps(sa_key).encode()).decode())
        cls.proc = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "nkt-plugin.py")], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", cls.port), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.1)

    @classmethod
    def tearDownClass(cls):
        cls.proc.terminate()
        out, _ = cls.proc.communicate(timeout=5)
        cls.api.srv.shutdown()
        shutil.rmtree(cls.tmp, ignore_errors=True)
        assert b"BEGIN PRIVATE KEY" not in out and b"t1.test" not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest_and_signed_login(self):
        m = self.req("/nkt/manifest")
        self.assertEqual((m["name"], sorted(m["provides"])), ("yc", ["dns", "hosts"]))
        self.assertEqual(m["host_types"][0]["id"], "yc-vm")
        v = self.call("vms")["view"]
        self.assertEqual([r["cells"]["name"] for r in v["rows"]], ["db-1", "web-1"])
        self.assertIn("51.250.1.2 (pub)", v["rows"][1]["cells"]["ip"])
        self.assertTrue(self.api.jwts and all(self.api.jwts))  # подпись PS256 проверена openssl

    def test_2_views(self):
        sec = self.call("security")["view"]["rows"][0]["cells"]
        self.assertEqual(sec["open"], "443/TCP, 22/TCP ⚠")
        st = {r["cells"]["bucket"]: r["cells"]["access"] for r in self.call("storage")["view"]["rows"]}
        self.assertEqual(st, {"backups": "private", "site-assets": "public: read ⚠"})
        d = self.call("disks")["view"]["items"]
        self.assertEqual([r["cells"]["attached"] for r in d[0]["rows"]], ["db-1", "web-1"])
        self.assertEqual(self.call("databases")["view"]["rows"][0]["cells"]["engine"], "PostgreSQL")
        self.assertIn("1234.56 RUB", self.call("billing")["view"]["text"])

    def test_3_vm_actions(self):
        self.assertIn("(dry run)", self.call("vm_stop", dry=True, name="web-1")["text"])
        self.assertIn("operation op-restart", self.call("vm_restart", name="web-1")["text"])
        self.assertFalse(self.call("vm_start", name="nope")["ok"])
        self.assertIn("operation op-snap", self.call("vm_snapshot", name="web-1")["text"])
        snap = next(b for m, p, b in self.api.calls if p == "/compute/v1/snapshots" and m == "POST")
        self.assertEqual((snap["diskId"], snap["folderId"]), ("d1", "b1gf"))
        self.assertFalse(self.call("snapshot_delete", id="../x")["ok"])
        self.assertTrue(self.call("_check", argv=["yc", "vm", "restart", "web-1"])["ok"])
        self.assertFalse(self.call("_check", argv=["yc", "vm", "explode", "web-1"])["ok"])

    def test_4_vm_as_host(self):
        conn = {"address": "web-1"}
        info = self.call("_host_check", type="yc-vm", conn=conn)["result"]
        self.assertEqual((info["name"], info["address"]), ("web-1", "51.250.1.2"))
        snap = self.call("_host_snapshot", type="yc-vm", conn=conn)["result"]
        self.assertEqual(snap["interfaces"][0]["addrs"], ["10.128.0.5", "51.250.1.2"])
        self.assertEqual([p["port"] for p in snap["ports"]], [443, 22])
        ids = [f["id"] for f in self.call("_host_findings", type="yc-vm", conn=conn)["result"]["findings"]]
        self.assertEqual(ids, ["open-sg1-22"])                      # снимок свежий, 443 — не опасный
        ids2 = [f["id"] for f in self.call("_host_findings", type="yc-vm", conn={"address": "db-1"})["result"]["findings"]]
        self.assertEqual(ids2, ["stopped", "preemptible", "no-snapshot"])
        cfg = self.call("_host_config", type="yc-vm", conn=conn)["result"]["text"]
        self.assertIn('"metadataKeys"', cfg)
        self.assertNotIn("SECRET-CLOUD-INIT", cfg)                  # значения метаданных — не в конфигурацию
        self.assertIn("op-stop", self.call("_host_action", type="yc-vm", conn=conn, action="stop")["text"])

    def test_5_dns(self):
        self.assertEqual(self.call("_dns_zones")["result"]["zones"], ["example.com"])
        rec = [{"name": "fly.example.com", "type": "A", "values": ["192.0.2.1"], "ttl": 300}]
        self.call("_dns_sync", owner="fly:x", records=rec, previous=[])
        a = next(r for r in self.api.records["dz1"] if r["name"] == "fly.example.com.")
        self.assertEqual(sorted(a["data"]), ["192.0.2.1", "192.0.2.9"])   # чужое значение осталось
        self.call("_dns_sync", owner="fly:x", records=[], previous=rec)
        a = next(r for r in self.api.records["dz1"] if r["name"] == "fly.example.com.")
        self.assertEqual(a["data"], ["192.0.2.9"])
        self.assertIn("app.example.com.", self.call("dns_set", name="app.example.com", type="A", values=["203.0.113.5"], ttl=120)["text"])
        self.assertIn("deleted", self.call("_command", argv=["yc", "dns", "rm", "app.example.com", "A"])["text"])
        self.assertFalse(self.call("dns_set", name="x.other.org", type="A", values=["1.1.1.1"])["ok"])
        rows = self.call("dns_records", zone="example.com")["view"]["rows"]
        self.assertTrue(any(r["cells"]["name"] == "fly.example.com." for r in rows))

    def test_6_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
