"""Проверка плагина LiteLLM на поддельном API LiteLLM (без сети и Docker):
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
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"
MASTER_HEX = "0123456789abcdef0123456789abcdef"


class FakeLiteLLM:
    """Ключи (список постранично, создание, блокировка, удаление) и модели."""

    def __init__(self):
        self.keys = []
        self.models = [{"model_name": "from-config", "litellm_params": {"model": "openai/x"}, "model_info": {"id": "cfg1", "db_model": False}}]
        self.calls = []
        self.n = 0
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
                if self.headers.get("Authorization") != "Bearer sk-" + MASTER_HEX:
                    return self.send({"error": {"message": "Authentication Error"}}, 401)
                path, _, query = self.path.partition("?")
                q = dict(urllib.parse.parse_qsl(query))
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                with fake.lock:
                    fake.calls.append((method, path, body))
                    if path == "/key/list":
                        page, size = int(q.get("page", 1)), 2  # по две — проверка постраничности
                        total = max(1, (len(fake.keys) + size - 1) // size)
                        return self.send({"keys": fake.keys[(page - 1) * size: page * size], "total_count": len(fake.keys), "current_page": page, "total_pages": total})
                    if path == "/key/generate":
                        fake.n += 1
                        key = f"sk-generated-{fake.n:04d}"
                        fake.keys.append({"token": f"hash{fake.n}", "key_alias": body["key_alias"], "key_name": f"sk-...{fake.n:04d}", "spend": 1.5 * fake.n,
                                          "models": body.get("models") or [], "max_budget": body.get("max_budget"), "budget_duration": body.get("budget_duration"),
                                          "rpm_limit": body.get("rpm_limit"), "tpm_limit": body.get("tpm_limit"), "blocked": None, "expires": None})
                        return self.send({"key": key, "token": f"hash{fake.n}", "key_alias": body["key_alias"]})
                    if path in ("/key/block", "/key/unblock"):
                        for k in fake.keys:
                            if k["token"] == body["key"]:
                                k["blocked"] = path == "/key/block"
                                return self.send(k)
                        return self.send({"error": "no key"}, 404)
                    if path == "/key/delete":
                        fake.keys = [k for k in fake.keys if k["key_alias"] not in body.get("key_aliases", [])]
                        return self.send({"deleted_keys": body.get("key_aliases")})
                    if path == "/model/info":
                        return self.send({"data": fake.models})
                    if path == "/model/new":
                        fake.n += 1
                        fake.models.append({"model_name": body["model_name"], "litellm_params": {k: v for k, v in body["litellm_params"].items() if k != "api_key"},
                                            "model_info": {"id": f"m{fake.n}", "db_model": True}})
                        return self.send({"model_id": f"m{fake.n}"})
                    if path == "/model/delete":
                        fake.models = [m for m in fake.models if m["model_info"]["id"] != body["id"]]
                        return self.send({"message": "deleted"})
                return self.send({"error": "no route"}, 404)

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def url(self):
        return f"http://127.0.0.1:{self.srv.server_address[1]}"


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api = FakeLiteLLM()
        cls.port = free_port()
        # Мастер-ключ без «sk-» — как в .env конвейера примера.
        env = dict(os.environ, LITELLM_URL=cls.api.url(), LITELLM_MASTER_KEY=MASTER_HEX, NKT_PLUGIN_SECRET=SECRET,
                   NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1")
        cls.proc = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "nkt-plugin.py")], env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
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
        # Журнал службы — без значений ключей.
        assert b"sk-generated" not in out and MASTER_HEX.encode() not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=10) as resp:
            return json.load(resp)

    def call(self, op, args=None, dry=False):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args or {}, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest(self):
        m = self.req("/nkt/manifest")
        self.assertEqual(m["name"], "litellm")
        gen = next(o for o in m["ops"] if o["name"] == "key_generate")
        self.assertTrue(gen["reveals"])
        self.assertIn("to_pipeline", gen["input"]["required"])

    def test_2_keys(self):
        dry = self.call("key_generate", {"alias": "app1"}, dry=True)
        self.assertNotIn("reveal", dry)
        self.assertEqual(self.api.keys, [])
        r = self.call("key_generate", {"alias": "app1", "models": "qwen, gpt-4o", "max_budget": "20", "budget_duration": "30d", "rpm_limit": "60"})
        self.assertTrue(r["ok"], r)
        self.assertTrue(r["reveal"]["value"].startswith("sk-generated-"))
        self.assertNotIn("store", r)
        sent = next(b for m, p, b in self.api.calls if p == "/key/generate")
        self.assertEqual((sent["models"], sent["max_budget"], sent["budget_duration"], sent["rpm_limit"]), (["qwen", "gpt-4o"], 20.0, "30d", 60))
        # В .env конвейера — store, без показа.
        r = self.call("key_generate", {"alias": "app2", "to_pipeline": "videolog", "env": "LLM_API_KEY"})
        self.assertNotIn("reveal", r)
        self.assertEqual((r["store"]["pipeline"], r["store"]["env"]), ("videolog", "LLM_API_KEY"))
        self.call("key_generate", {"alias": "app3"})
        self.assertFalse(self.call("key_generate", {"alias": "app1"})["ok"])         # имя занято
        self.assertFalse(self.call("key_generate", {"alias": "bad name"})["ok"])
        self.assertFalse(self.call("key_generate", {"alias": "x", "env": "A"})["ok"])  # env без конвейера
        rows = self.call("keys")["view"]["rows"]                                      # три ключа — две страницы
        self.assertEqual([r["cells"]["alias"] for r in rows], ["app1", "app2", "app3"])
        self.assertTrue(self.call("key_block", {"alias": "app1"})["ok"])
        self.assertTrue(self.api.keys[0]["blocked"])
        self.assertEqual(self.call("keys")["view"]["rows"][0]["actions"][0]["op"], "key_unblock")
        self.call("key_unblock", {"alias": "app1"})
        self.assertFalse(self.api.keys[0]["blocked"])
        self.call("key_delete", {"alias": "app3"})
        self.assertEqual([k["key_alias"] for k in self.api.keys], ["app1", "app2"])
        self.assertFalse(self.call("key_delete", {"alias": "nope"})["ok"])
        spend = self.call("spend")["view"]["rows"]
        self.assertEqual(spend[-1]["cells"]["spent"], "4.50")

    def test_3_models(self):
        self.assertFalse(self.call("model_add", {"name": "q", "model": "qwen"})["ok"])                       # без провайдера
        self.assertFalse(self.call("model_add", {"name": "q", "model": "openai/q", "api_base": "ftp://x"})["ok"])
        self.assertTrue(self.call("model_add", {"name": "qwen", "model": "openai/qwen3-8b", "api_base": "http://llama:8080/v1", "api_key": "none-key"})["ok"])
        rows = self.call("models")["view"]["rows"]
        self.assertEqual([r["cells"]["name"] for r in rows], ["from-config", "qwen"])
        self.assertEqual(rows[0]["actions"], [])  # из файла настроек — не удалить
        mid = rows[1]["actions"][0]["args"]["id"]
        self.assertFalse(self.call("model_delete", {"id": "cfg1"})["ok"])
        self.assertTrue(self.call("model_delete", {"id": mid})["ok"])

    def test_4_command(self):
        self.assertFalse(self.call("_check", {"argv": ["llm", "key", "add", "app9"]})["ok"])  # в сценарии — только в .env
        argv = ["llm", "key", "add", "app9", "models", "qwen", "budget", "5", "to-pipeline", "videolog", "env", "LLM_KEY"]
        self.assertTrue(self.call("_check", {"argv": argv})["ok"])
        r = self.call("_command", {"argv": argv})
        self.assertNotIn("reveal", r)
        self.assertEqual(r["store"]["env"], "LLM_KEY")
        self.assertTrue(self.call("_command", {"argv": ["llm", "key", "block", "app9"]})["ok"])
        self.assertIn("app9", self.call("_command", {"argv": ["llm", "keys"]})["text"])
        self.assertFalse(self.call("_check", {"argv": ["llm", "key", "zap", "x"]})["ok"])

    def test_5_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
