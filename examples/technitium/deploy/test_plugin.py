"""Проверка плагина Technitium DNS без Technitium: поддельный HTTP API
(zones/list, zones/records/get|add|delete) с проверкой токена. Плагин —
отдельным процессом, вызовы — подписанные, как от хаба.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import hashlib
import hmac
import json
import os
import socket
import subprocess
import sys
import threading
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"
TOKEN = "tech-api-token-123"


class FakeTechnitium:
    def __init__(self):
        fake = self
        self.zones = {"example.com": {"name": "example.com", "type": "Primary", "soaSerial": 7, "dnssecStatus": "Unsigned"},
                      "other.net": {"name": "other.net", "type": "Secondary"}}
        self.records = {"example.com": [{"name": "www.example.com", "type": "A", "ttl": 300, "rData": {"ipAddress": "203.0.113.5"}},
                                        {"name": "app.example.com", "type": "A", "ttl": 300, "rData": {"ipAddress": "198.51.100.9"}}]}
        self.calls = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, obj):
                raw = json.dumps(obj).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def do_POST(self):
                q = {k: v[0] for k, v in urllib.parse.parse_qs(self.rfile.read(int(self.headers.get("Content-Length", 0))).decode()).items()}
                path = self.path.split("?")[0].removeprefix("/api/")
                if q.get("token") != TOKEN:
                    return self.send({"status": "invalid-token", "errorMessage": "Invalid token"})
                fake.calls.append((path, q))
                if path == "zones/list":
                    return self.send({"status": "ok", "response": {"zones": list(fake.zones.values())}})
                zone = q.get("zone", "")
                recs = fake.records.setdefault(zone, [])
                if path == "zones/records/get":
                    return self.send({"status": "ok", "response": {"records": recs}})
                if path == "zones/records/add":
                    recs.append({"name": q["domain"], "type": q["type"], "ttl": int(q.get("ttl", 300)), "rData": {"ipAddress": q.get("ipAddress", "")}})
                    return self.send({"status": "ok", "response": {}})
                if path == "zones/records/delete":
                    fake.records[zone] = [r for r in recs if not (r["name"] == q["domain"] and r["type"] == q["type"] and r["rData"].get("ipAddress") == q.get("ipAddress"))]
                    return self.send({"status": "ok", "response": {}})
                self.send({"status": "error", "errorMessage": f"no {path}"})

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def a(self, zone, name):
        return sorted(r["rData"]["ipAddress"] for r in self.records.get(zone, []) if r["name"] == name and r["type"] == "A")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dns = FakeTechnitium()
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   DNS_API=f"http://127.0.0.1:{cls.dns.srv.server_address[1]}/api", DNS_API_TOKEN=TOKEN)
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
        cls.dns.srv.shutdown()
        assert TOKEN.encode() not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest_zones(self):
        m = self.req("/nkt/manifest")
        self.assertIn("dns", m["provides"])
        self.assertEqual(self.call("_dns_zones")["result"]["zones"], ["example.com"])  # вторичная — не своя
        v = self.call("zones")["view"]
        self.assertEqual([r["cells"]["zone"] for r in v["rows"]], ["example.com"])

    def test_2_dns_sync(self):
        # Владелец ставит www на два шлюза и убирает прежний адрес; чужой app не трогается.
        prev = [{"name": "www.example.com", "type": "A", "values": ["203.0.113.5"]}]
        new = [{"name": "www.example.com", "type": "A", "values": ["203.0.113.10", "203.0.113.11"]}]
        dry = self.call("_dns_sync", dry=True, owner="fly:www", records=new, previous=prev)
        self.assertTrue(dry["ok"])
        self.assertEqual(self.dns.a("example.com", "www.example.com"), ["203.0.113.5"])  # сухой прогон ничего не менял
        self.assertTrue(self.call("_dns_sync", owner="fly:www", records=new, previous=prev)["ok"])
        self.assertEqual(self.dns.a("example.com", "www.example.com"), ["203.0.113.10", "203.0.113.11"])
        self.assertEqual(self.dns.a("example.com", "app.example.com"), ["198.51.100.9"])
        self.assertFalse(self.call("_dns_sync", owner="x", records=[{"name": "a.nozone.org", "type": "A", "values": ["1.2.3.4"]}])["ok"])

    def test_3_commands(self):
        self.assertTrue(self.call("_check", argv=["dns", "set", "api.example.com", "A", "192.0.2.1"])["ok"])
        self.assertFalse(self.call("_check", argv=["dns", "set", "api.example.com", "A", "not-an-ip"])["ok"])
        self.assertFalse(self.call("_check", argv=["dns", "set", "bad name;rm", "A", "192.0.2.1"])["ok"])
        self.assertTrue(self.call("_command", argv=["dns", "set", "api.example.com", "A", "192.0.2.1"])["ok"])
        self.assertEqual(self.dns.a("example.com", "api.example.com"), ["192.0.2.1"])

    def test_4_bad_token_and_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
