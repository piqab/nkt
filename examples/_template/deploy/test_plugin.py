"""Тест заготовки на поддельном API: так же проверяйте свой плагин.

Подделка — HTTP-сервер со стандартными ответами настоящего API (по его
документации) и проверкой токена; плагин — отдельным процессом, вызовы —
подписанные, как от хаба. Обязательно: манифест на двух языках, виды,
формы с проверкой ввода (опасное имя — отказ), сухой прогон ничего не
меняет, команда сценария (check и выполнение), неверная подпись — 401,
секреты не попадают в журнал плагина.
Запуск: python3 -I test_plugin.py (его же вызывает go test ./examples и CI)."""
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
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"
TOKEN = "example-api-token-xyz"


class FakeAPI:
    def __init__(self):
        fake = self
        self.items = [{"name": "alpha", "size": 10, "state": "ok"}, {"name": "beta", "size": 3, "state": "broken"}]

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, obj=None):
                raw = json.dumps(obj).encode() if obj is not None else b""
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def authed(self):
                if self.headers.get("Authorization") != "Bearer " + TOKEN:
                    self.send(401, {"error": "unauthorized"})
                    return False
                return True

            def do_GET(self):
                if self.authed():
                    self.send(200, fake.items) if self.path == "/api/items" else self.send(404, {})

            def do_POST(self):
                if self.authed():
                    body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                    fake.items.append({"name": body["name"], "size": 0, "state": "ok"})
                    self.send(201, {})

            def do_DELETE(self):
                if self.authed():
                    name = self.path.rsplit("/", 1)[-1]
                    fake.items = [x for x in fake.items if x["name"] != name]
                    self.send(204)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api = FakeAPI()
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   EXAMPLE_URL=f"http://127.0.0.1:{cls.api.srv.server_address[1]}", EXAMPLE_TOKEN=TOKEN)
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
        assert TOKEN.encode() not in out and SECRET.encode() not in out, out

    def req(self, path, body=b"", lang="en"):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": lang})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest(self):
        en, ru = self.req("/nkt/manifest"), self.req("/nkt/manifest", lang="ru")
        self.assertEqual((en["name"], en["title"], ru["title"]), ("example", "Plugin template", "Заготовка плагина"))
        self.assertEqual([o["name"] for o in en["ops"] if o.get("mcp")], ["items", "summary", "item_add", "item_delete"])

    def test_2_views(self):
        v = self.call("items")["view"]
        self.assertEqual([r["cells"]["name"] for r in v["rows"]], ["alpha", "beta"])
        self.assertTrue(v["rows"][0]["actions"][0]["danger"])
        s = self.call("summary")["view"]
        self.assertEqual(s["rows"][1]["cells"]["size"], 1)

    def test_3_changes(self):
        self.assertIn("(dry run)", self.call("item_add", dry=True, name="gamma")["text"])
        self.assertNotIn("gamma", [x["name"] for x in self.api.items])  # сухой прогон ничего не менял
        self.assertTrue(self.call("item_add", name="gamma")["ok"])
        self.assertIn("gamma", [x["name"] for x in self.api.items])
        self.assertTrue(self.call("item_delete", name="gamma")["ok"])
        for bad in ("../etc", "a b", "-rf", "x;rm", ""):
            self.assertFalse(self.call("item_add", name=bad)["ok"], bad)

    def test_4_command(self):
        self.assertTrue(self.call("_check", argv=["example", "add", "delta"])["ok"])
        self.assertFalse(self.call("_check", argv=["example", "add", "../x"])["ok"])
        self.assertFalse(self.call("_check", argv=["example", "bogus"])["ok"])
        self.assertIn("alpha", self.call("_command", argv=["example", "list"])["text"])

    def test_5_signature(self):
        for headers in ({"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"}, {}):
            r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers=headers)
            with self.assertRaises(urllib.error.HTTPError) as e:
                urllib.request.urlopen(r, timeout=5)
            self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.call("no_such_op")["ok"])


if __name__ == "__main__":
    unittest.main(verbosity=1)
