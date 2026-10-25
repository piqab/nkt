"""Проверка плагина Cloudflare на поддельном API Cloudflare (без сети):
плагин запускается отдельным процессом, вызовы — подписанные, как от хаба.
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
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"
TOKEN = "cf-test-token"


class FakeCF:
    """Поддельный API Cloudflare: зоны, записи (список постранично,
    создание, удаление), проверка токена."""

    def __init__(self):
        self.zones = {"z1": {"id": "z1", "name": "example.com", "status": "active", "name_servers": ["ada.ns.cloudflare.com", "bob.ns.cloudflare.com"]},
                      "z2": {"id": "z2", "name": "other.org", "status": "pending", "name_servers": ["cid.ns.cloudflare.com"]}}
        self.records = {"z1": [{"id": "r1", "type": "NS", "name": "example.com", "content": "ada.ns.cloudflare.com", "ttl": 86400},
                               {"id": "r2", "type": "A", "name": "example.com", "content": "203.0.113.10", "ttl": 1, "proxied": True},
                               {"id": "r3", "type": "MX", "name": "example.com", "content": "mail.example.com", "priority": 10, "ttl": 1},
                               {"id": "r4", "type": "TXT", "name": "example.com", "content": "\"v=spf1 mx -all\"", "ttl": 1}],
                        "z2": []}
        self.n = 100
        self.lock = threading.Lock()
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
                if self.headers.get("Authorization") != f"Bearer {TOKEN}":
                    return self.send({"success": False, "errors": [{"code": 10000, "message": "Authentication error"}]}, 403)
                path, _, query = self.path.partition("?")
                q = dict(urllib.parse.parse_qsl(query))
                parts = path.strip("/").split("/")[2:]  # после /client/v4
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                with fake.lock:
                    if parts == ["zones"]:
                        return self.page(list(fake.zones.values()), q)
                    if len(parts) == 3 and parts[2] == "dns_records" and method == "GET":
                        return self.page(fake.records[parts[1]], q)
                    if len(parts) == 3 and parts[2] == "dns_records" and method == "POST":
                        fake.n += 1
                        r = dict(body, id=f"r{fake.n}")
                        fake.records[parts[1]].append(r)
                        return self.send({"success": True, "result": r})
                    if len(parts) == 4 and method == "DELETE":
                        fake.records[parts[1]] = [r for r in fake.records[parts[1]] if r["id"] != parts[3]]
                        return self.send({"success": True, "result": {"id": parts[3]}})
                return self.send({"success": False, "errors": [{"code": 7003, "message": "no route"}]}, 404)

            def page(self, items, q):
                per, page = int(q.get("per_page", 20)), int(q.get("page", 1))
                # Две записи на страницу — проверка постраничного чтения.
                per = min(per, 2)
                total = max(1, (len(items) + per - 1) // per)
                self.send({"success": True, "result": items[(page - 1) * per: page * per], "result_info": {"page": page, "total_pages": total}})

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

            def do_DELETE(self):
                self.route("DELETE")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def url(self):
        return f"http://127.0.0.1:{self.srv.server_address[1]}/client/v4"


import urllib.parse  # noqa: E402  (после FakeCF — им пользуется H)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cf = FakeCF()
        cls.port = free_port()
        env = dict(os.environ, CF_API=cls.cf.url(), CF_API_TOKEN=TOKEN, NKT_PLUGIN_SECRET=SECRET,
                   NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1")
        cls.proc = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "nkt-plugin.py")], env=env,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", cls.port), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.1)

    @classmethod
    def tearDownClass(cls):
        cls.proc.terminate()
        cls.proc.wait(5)
        cls.cf.srv.shutdown()

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=10) as resp:
            return json.load(resp)

    def call(self, op, args=None, dry=False):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args or {}, "dry_run": dry, "user": "test"}).encode())

    def names(self, zone="z1"):
        return sorted((r["type"], r["name"], r.get("content") or json.dumps(r.get("data"))) for r in self.cf.records[zone])

    def test_1_manifest_and_zones(self):
        m = self.req("/nkt/manifest")
        self.assertEqual(m["name"], "cloudflare")
        self.assertIn("dns", m["provides"])
        z = self.call("_dns_zones")
        self.assertEqual(z["result"]["zones"], ["example.com", "other.org"])
        v = self.call("zones")["view"]
        self.assertEqual([r["cells"]["zone"] for r in v["rows"]], ["example.com", "other.org"])

    def test_2_text_roundtrip_and_apply(self):
        text = self.call("zone_text", {"zone": "example.com"})["result"]["text"]
        self.assertIn(["@", "auto", "A", "203.0.113.10", "proxied"], [l.split() for l in text.splitlines()])
        self.assertIn('"v=spf1 mx -all"', text)
        # Тот же текст — ничего не меняется.
        self.assertEqual(self.call("zone_apply", {"zone": "example.com", "text": text})["text"], "nothing changed")
        new = text.replace("203.0.113.10 proxied", "203.0.113.11") + "www 300 CNAME @\n_sip._tcp 300 SRV 10 5 5060 sip.example.com.\n"
        dry = self.call("zone_apply", {"zone": "example.com", "text": new}, dry=True)["text"]
        self.assertIn("(dry run) + www.example.com CNAME @", dry)
        before = self.names()
        self.call("zone_apply", {"zone": "example.com", "text": new})
        self.assertNotEqual(before, self.names())
        self.assertIn(("CNAME", "www.example.com", "example.com"), self.names())
        # NS вершины Cloudflare не трогается, даже если его нет в тексте.
        self.assertTrue(any(r["type"] == "NS" for r in self.cf.records["z1"]))
        bad = self.call("zone_apply", {"zone": "example.com", "text": "www 300 CNAME @\nwww 300 A 1.2.3.4\n"})
        self.assertFalse(bad["ok"])

    def test_3_set_add_delete_command(self):
        out = self.call("_command", {"argv": ["cf", "set", "app.example.com", "A", "198.51.100.1,198.51.100.2", "ttl", "120"]})
        self.assertIn("+ app.example.com A 198.51.100.2", out["text"])
        self.call("_command", {"argv": ["cf", "set", "app.example.com", "A", "198.51.100.2"]})
        a = [r["content"] for r in self.cf.records["z1"] if r["name"] == "app.example.com"]
        self.assertEqual(a, ["198.51.100.2"])
        self.assertFalse(self.call("_check", {"argv": ["cf", "add", "x.example.com", "MX", "10 mail", "proxied"]})["ok"])
        self.assertTrue(self.call("_check", {"argv": ["cf", "add", "x.example.com", "TXT", "hello"]})["ok"])
        self.call("record_delete", {"name": "app.example.com"})
        self.assertFalse(any(r["name"] == "app.example.com" for r in self.cf.records["z1"]))
        self.assertFalse(self.call("record_add", {"name": "x.nowhere.net", "type": "A", "value": "1.2.3.4"})["ok"])

    def test_4_dns_sync(self):
        rec = [{"name": "fly.example.com", "type": "A", "values": ["192.0.2.1", "192.0.2.2"], "ttl": 300, "failover": True}]
        out = self.call("_dns_sync", {"owner": "fly:fly.example.com", "records": rec, "previous": []})["text"]
        self.assertIn("Failover", out)
        # Чужое значение у того же имени остаётся.
        self.cf.records["z1"].append({"id": "own", "type": "A", "name": "fly.example.com", "content": "192.0.2.9", "ttl": 1})
        self.call("_dns_sync", {"owner": "fly:fly.example.com", "records": [], "previous": rec})
        left = sorted(r["content"] for r in self.cf.records["z1"] if r["name"] == "fly.example.com")
        self.assertEqual(left, ["192.0.2.9"])
        # Прежние записи зоны, которой больше нет у токена, — пропуск.
        ok = self.call("_dns_sync", {"owner": "x", "records": [], "previous": [{"name": "a.gone.net", "type": "A", "values": ["1.1.1.1"]}]})
        self.assertTrue(ok["ok"])

    def test_5_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
