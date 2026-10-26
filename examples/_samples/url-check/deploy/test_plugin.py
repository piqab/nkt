"""Тест примера url-check на подделках: HTTP (быстрый, медленный, 5xx) и
HTTPS с сертификатом на 5 дней (свой УЦ через URL_CHECK_CA_FILE; нужен
openssl). Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import hashlib
import hmac
import json
import os
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path == "/slow":
            time.sleep(0.4)
        code = 503 if self.path == "/broken" else 200
        self.send_response(code)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        cls.http = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=cls.http.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{cls.http.server_address[1]}"
        targets = [f"{base}/fine", f"{base}/slow", f"{base}/broken", f"http://127.0.0.1:{free_port()}/closed", "ftp://bad.example"]
        cls.https = None
        if shutil.which("openssl"):
            crt, key = os.path.join(cls.tmp, "c.pem"), os.path.join(cls.tmp, "k.pem")
            subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "5",
                            "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1", "-keyout", key, "-out", crt],
                           check=True, capture_output=True)
            cls.https = ThreadingHTTPServer(("127.0.0.1", 0), H)
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            ctx.load_cert_chain(crt, key)
            # Рукопожатие — в потоке обработчика, не в accept: иначе параллельные
            # проверки ждут друг друга (у настоящих серверов так не бывает).
            cls.https.socket = ctx.wrap_socket(cls.https.socket, server_side=True, do_handshake_on_connect=False)
            threading.Thread(target=cls.https.serve_forever, daemon=True).start()
            targets.append(f"https://127.0.0.1:{cls.https.server_address[1]}/fine")
            cls.crt = crt
        cls.targets = targets
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   URL_CHECK_TARGETS=",".join(targets), URL_CHECK_SLOW_MS="200", URL_CHECK_CA_FILE=getattr(cls, "crt", ""))
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
        cls.http.shutdown()
        if cls.https:
            cls.https.shutdown()
        shutil.rmtree(cls.tmp, ignore_errors=True)
        assert SECRET.encode() not in out, out

    def req(self, path, body=b"", lang="en"):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": lang})
        with urllib.request.urlopen(r, timeout=60) as resp:
            return json.load(resp)

    def call(self, op, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "user": "test"}).encode())

    def test_1_manifest(self):
        m = self.req("/nkt/manifest")
        ops = {o["name"]: o for o in m["ops"]}
        self.assertTrue(ops["status"]["read_only"])
        self.assertFalse(ops["check"].get("read_only"))  # любой адрес — не для токена чтения
        self.assertEqual(self.req("/nkt/manifest", lang="ru")["title"], "Проверка адресов")

    def test_2_status_findings(self):
        rows = {r["cells"]["url"]: r["cells"] for r in self.call("status")["view"]["rows"]}
        base = self.targets[0].rsplit("/", 1)[0]
        self.assertEqual((rows[f"{base}/fine"]["code"], rows[f"{base}/fine"]["result"]), (200, "fine"))
        self.assertIn("answered in", rows[f"{base}/slow"]["result"])
        self.assertIn("server error 503", rows[f"{base}/broken"]["result"])
        self.assertIn("not answering", rows[self.targets[3]]["result"])
        self.assertIn("http:// or https:// only", rows["ftp://bad.example"]["result"])
        found = [r["cells"]["f"] for r in self.call("findings")["view"]["rows"]]
        self.assertEqual(len([f for f in found if f.startswith("http://")]), 3)  # медленный, 5xx, закрытый порт
        if self.https:
            https = self.targets[-1]
            self.assertLessEqual(rows[https]["cert"], 5)
            self.assertIn("certificate expires in", rows[https]["result"])

    def test_3_check_and_command(self):
        self.assertTrue(self.call("check", url=self.targets[0])["ok"])
        self.assertFalse(self.call("check", url=self.targets[2])["ok"])
        for bad in ("file:///etc/passwd", "http://user:pw@x.example/", "https://a b", "javascript:alert(1)", ""):
            self.assertFalse(self.call("check", url=bad)["ok"], bad)
        self.assertTrue(self.call("_check", argv=["urlcheck", "https://x.example/health"])["ok"])
        self.assertFalse(self.call("_check", argv=["urlcheck", "gopher://x"])["ok"])
        self.assertFalse(self.call("_check", argv=["urlcheck"])["ok"])
        self.assertIn("200", self.call("_command", argv=["urlcheck", self.targets[0]])["text"])
        self.assertFalse(self.call("_command", argv=["urlcheck", self.targets[2]])["ok"])

    def test_4_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.call("no_such_op")["ok"])


if __name__ == "__main__":
    unittest.main(verbosity=1)
