"""Тест учебного плагина rgstr-lite на поддельном rgstr (статья «Пишем
плагин по шагам», шаг 7): манифест, разделы, проверка ввода, сухой прогон,
секрет окну и в конвейер, команда сценария, подпись, секреты не в журнале.
Запуск: python3 -I test_plugin.py."""
import base64
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
PASSWORD = "lite-admin-password-1"
ROBOT = "robot-secret-lite-0123456789abcdef"


class Fake:
    """Поддельный rgstr: ответы — как у настоящего (по его исходникам)."""

    def __init__(self):
        fake = self
        self.repos = [{"name": "shop/api", "tags": [{"name": "1.0", "size": 3 << 20}]}]
        self.calls = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, obj):
                raw = json.dumps(obj).encode()
                self.send_response(code)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def route(self, method):
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n)) if n else None
                if self.path == "/healthz":
                    return self.send(200, {"status": "ok", "version": "v1.7.0"})
                ok = self.headers.get("Authorization") == "Basic " + base64.b64encode(f"admin:{PASSWORD}".encode()).decode()
                if not ok:
                    return self.send(401, {"error": "authentication required"})
                fake.calls.append((method, self.path, body))
                if self.path == "/ui/api/me":
                    return self.send(200, {"version": "v1.7.0", "auth_enabled": True, "user": "admin", "admin": True})
                if self.path == "/ui/api/repos":
                    return self.send(200, {"repositories": fake.repos, "total_pulls": 0})
                if method == "POST" and body is None:
                    return self.send(400, {"error": "bad request: EOF"})
                if self.path == "/ui/api/tags/delete":
                    fake.repos[0]["tags"] = [t for t in fake.repos[0]["tags"] if t["name"] not in body["tags"]]
                    return self.send(200, {"results": [{"tag": t} for t in body["tags"]]})
                if self.path == "/ui/api/gc/run":
                    return self.send(200, {"bytes_freed": 2 << 20})
                if self.path == "/ui/api/robots/create":
                    return self.send(200, {"name": "robot$" + body["name"], "secret": ROBOT})
                self.send(404, {"error": "not found"})

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


class LiteTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fake = Fake()
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            cls.port = s.getsockname()[1]
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   RGSTR_URL=f"http://127.0.0.1:{cls.fake.srv.server_address[1]}", RGSTR_USER="admin", RGSTR_PASSWORD=PASSWORD,
                   RGSTR_PUBLIC="registry.example.com")
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
        cls.fake.srv.shutdown()
        for s in (SECRET, PASSWORD, ROBOT):
            assert s.encode() not in out, s

    def req(self, path, body=b"", lang="en"):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None, headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": lang})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest_and_views(self):
        self.assertEqual(self.req("/nkt/manifest", lang="ru")["pages"][0]["title"], "Сводка")
        v = self.call("summary")["view"]
        self.assertEqual(v["items"][1]["rows"][0]["cells"]["value"], "No findings")
        self.assertEqual(self.call("repos")["view"]["rows"][0]["actions"][0]["open"], "tags")
        self.assertEqual(self.call("tags", repo="shop/api")["view"]["rows"][0]["cells"]["size"], 3.0)

    def test_2_input_dry_run_changes(self):
        for bad in ("../x", "Shop", "a b", "x;rm"):
            self.assertFalse(self.call("tag_delete", repo=bad, tag="1.0")["ok"], bad)
        n = len(self.fake.calls)
        self.assertIn("(dry run)", self.call("tag_delete", dry=True, repo="shop/api", tag="1.0")["text"])
        self.assertFalse(any(m == "POST" for m, _, _ in self.fake.calls[n:]))
        self.assertTrue(self.call("tag_delete", repo="shop/api", tag="1.0")["ok"])
        self.assertEqual(self.fake.repos[0]["tags"], [])
        self.assertIn("freed 2.0 MB", self.call("gc_run")["text"])

    def test_3_secret(self):
        self.assertEqual(self.call("robot_create", name="ci", project="shop")["reveal"]["value"], ROBOT)
        st = self.call("robot_create", name="ci", project="shop", to_pipeline="shop", env="@registry")["store"]
        self.assertEqual(st, {"pipeline": "shop", "env": "@registry", "value": ROBOT, "registry": "registry.example.com", "user": "robot$ci"})
        self.assertFalse(self.call("robot_create", name="ci", project="shop", to_pipeline="shop", env="bad")["ok"])

    def test_4_command_and_signature(self):
        argv = ["rgstr-lite", "robot", "ci", "project", "shop", "to-pipeline", "shop", "env", "@registry"]
        self.assertTrue(self.call("_check", argv=argv)["ok"])
        self.assertFalse(self.call("_check", argv=["rgstr-lite", "nope"])["ok"])
        out = self.call("_command", argv=argv)
        self.assertNotIn("reveal", out)
        self.assertEqual(out["store"]["user"], "robot$ci")
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1"}), timeout=5)
        self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.call("no_such_op")["ok"])


if __name__ == "__main__":
    unittest.main(verbosity=1)
