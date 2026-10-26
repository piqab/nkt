"""Проверка плагина MikroTik на поддельном REST API RouterOS 7 (HTTPS со
своим сертификатом, без сети и без роутера): плагин запускается отдельным
процессом, вызовы — подписанные, как от хаба.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import base64
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
USER, PASSWORD = "nkt", "router-pass-123"


class FakeRouter:
    def __init__(self, certfile, keyfile):
        self.calls = []
        self.lock = threading.Lock()
        self.export_text = "# 2026-10-26 10:00:00 by RouterOS 7.15.3\n# software id = ABCD\n/ip address add address=192.168.88.1/24 interface=bridge\n"
        self.data = {
            "/system/resource": {"uptime": "1w2d3h4m5s", "version": "7.15.3 (stable)", "cpu-load": "12", "free-memory": "100000000",
                                 "total-memory": "268435456", "free-hdd-space": "50000000", "total-hdd-space": "134217728",
                                 "architecture-name": "arm64", "board-name": "hAP ax3", "cpu": "ARM64", "cpu-count": "4"},
            "/system/identity": {"name": "Router-Office"},
            "/system/routerboard": {"model": "C53UiG+5HPaxD2HPaxD", "serial-number": "HE1234", "current-firmware": "7.14", "upgrade-firmware": "7.15.3"},
            "/interface": [{"name": "ether1", "type": "ether", "mac-address": "48:A9:8A:00:00:01", "running": "true", "disabled": "false", "rx-byte": "1000", "tx-byte": "2000"},
                           {"name": "bridge", "type": "bridge", "mac-address": "48:A9:8A:00:00:02", "running": "true", "disabled": "false", "rx-byte": "500", "tx-byte": "700"}],
            "/ip/address": [{"address": "203.0.113.5/24", "interface": "ether1", "disabled": "false"}, {"address": "192.168.88.1/24", "interface": "bridge", "disabled": "false"}],
            "/ipv6/address": [],
            "/ip/service": [{"name": "telnet", "port": "23", "disabled": "false", "address": ""}, {"name": "www", "port": "80", "disabled": "false", "address": ""},
                            {"name": "www-ssl", "port": "443", "disabled": "false", "address": ""}, {"name": "ssh", "port": "22", "disabled": "false", "address": "192.168.88.0/24"},
                            {"name": "api", "port": "8728", "disabled": "true", "address": ""}],
            "/system/package": [{"name": "routeros", "version": "7.15.3"}],
            "/user": [{"name": "admin", "group": "full", "disabled": "false"}, {"name": "nkt", "group": "full", "disabled": "false"}],
            "/ip/neighbor": [{"identity": "Switch-1", "address": "192.168.88.2", "mac-address": "48:A9:8A:00:00:09", "interface": "bridge", "platform": "MikroTik", "board": "CRS326"}],
            "/ip/route": [{"dst-address": "0.0.0.0/0", "gateway": "203.0.113.1", "distance": "1", "active": "true"}],
            "/ip/firewall/filter": [{"chain": "input", "action": "accept", "connection-state": "established,related", "disabled": "false"}],
            "/ip/dhcp-server/lease": [{"address": "192.168.88.10", "mac-address": "AA:BB:CC:00:00:10", "host-name": "laptop", "status": "bound"}],
            "/system/health": [{"name": "cpu-temperature", "value": "48", "type": "C"}],
        }
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
                want = "Basic " + base64.b64encode(f"{USER}:{PASSWORD}".encode()).decode()
                if self.headers.get("Authorization") != want:
                    return self.send({"error": 401, "message": "Unauthorized"}, 401)
                path = self.path.split("?")[0]
                if not path.startswith("/rest/"):
                    return self.send({"error": 404}, 404)
                path = path[len("/rest"):]
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                with fake.lock:
                    fake.calls.append((method, path, body))
                if method == "GET" and path in fake.data:
                    return self.send(fake.data[path])
                if method == "POST" and path == "/system/package/update/check-for-updates":
                    return self.send([{"status": "New version is available", "installed-version": "7.15.3", "latest-version": "7.16.1", "channel": "stable"}])
                if method == "POST" and path == "/execute":
                    script = body.get("script", "")
                    if script.startswith("/export"):
                        return self.send({"ret": fake.export_text})
                    return self.send({"ret": f"ran: {script}"})
                if method == "POST" and path in ("/system/reboot", "/system/backup/save", "/system/routerboard/upgrade", "/system/package/update/install"):
                    return self.send([])
                return self.send({"error": 404, "message": "no such command"}, 404)

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

        class Quiet(ThreadingHTTPServer):
            def handle_error(self, request, client_address):
                pass  # плагин рвёт соединение при чужом отпечатке — так и надо

        self.srv = Quiet(("127.0.0.1", 0), H)
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(certfile, keyfile)
        self.srv.socket = ctx.wrap_socket(self.srv.socket, server_side=True)
        with open(certfile, "rb") as f:
            der = ssl.PEM_cert_to_DER_cert(f.read().decode())
        self.fingerprint = hashlib.sha256(der).hexdigest()
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def addr(self):
        return f"127.0.0.1:{self.srv.server_address[1]}"


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@unittest.skipIf(shutil.which("openssl") is None, "no openssl")
class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        cert, key = os.path.join(cls.tmp, "c.pem"), os.path.join(cls.tmp, "k.pem")
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", cert, "-days", "1",
                        "-subj", "/CN=router"], check=True, capture_output=True)
        cls.router = FakeRouter(cert, key)
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1")
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
        cls.router.srv.shutdown()
        shutil.rmtree(cls.tmp, ignore_errors=True)
        assert PASSWORD.encode() not in out, out  # пароль — не в журнал службы

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=20) as resp:
            return json.load(resp)

    def call(self, op, conn=None, dry=False, **extra):
        args = {"type": "routeros", "conn": conn if conn is not None else self.conn()}
        args.update(extra)
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def conn(self, **kw):
        c = {"address": self.router.addr(), "user": USER, "password": PASSWORD, "tls": "pin", "fingerprint": self.router.fingerprint}
        c.update(kw)
        return c

    def test_1_manifest(self):
        m = self.req("/nkt/manifest")
        self.assertEqual(m["provides"], ["hosts"])
        t = m["host_types"][0]
        self.assertEqual((t["id"], t["exec"]), ("routeros", "routeros"))
        self.assertIn("address", [f["name"] for f in t["fields"]])

    def test_2_check_and_pin(self):
        r = self.call("_host_check", conn=self.conn(fingerprint=""))
        self.assertTrue(r["ok"], r)
        self.assertEqual((r["result"]["name"], r["result"]["version"], r["result"]["fingerprint"]), ("Router-Office", "7.15.3", self.router.fingerprint))
        bad = self.call("_host_check", conn=self.conn(fingerprint="00" * 32))
        self.assertFalse(bad["ok"])
        self.assertIn("certificate changed", bad["error"])
        wrong = self.call("_host_check", conn=self.conn(password="nope"))
        self.assertIn("wrong login or password", wrong["error"])
        # Проверка по удостоверяющим центрам — свой сертификат не пройдёт.
        self.assertFalse(self.call("_host_check", conn=self.conn(tls="verify"))["ok"])

    def test_3_snapshot_metrics_findings(self):
        snap = self.call("_host_snapshot")["result"]
        self.assertEqual(snap["info"]["model"], "hAP ax3")
        self.assertEqual(snap["interfaces"][0]["addrs"], ["203.0.113.5/24"])
        self.assertEqual(snap["neighbors"][0]["name"], "Switch-1")
        self.assertEqual([v["title"] for v in snap["extra"]], ["Routes", "Firewall (filter)", "DHCP leases"])
        m = self.call("_host_metrics")["result"]
        self.assertEqual((m["cpu_pct"], m["temp_c"], m["net"]["ether1"]["rx"]), (12.0, 48.0, 1000))
        ids = {f["id"]: f["severity"] for f in self.call("_host_findings")["result"]["findings"]}
        self.assertEqual(ids.get("service-telnet"), "critical")
        self.assertIn("service-www", ids)
        self.assertNotIn("service-ssh", ids)       # ограничена своей сетью
        self.assertNotIn("service-api", ids)       # выключена
        self.assertIn("routeros-update", ids)
        self.assertIn("firmware", ids)
        self.assertIn("user-admin", ids)
        self.assertIn("firewall-input", ids)

    def test_4_config_actions_exec(self):
        text = self.call("_host_config")["result"]["text"]
        self.assertNotIn("by RouterOS", text)      # строка с временем выгрузки убрана
        self.assertIn("/ip address add", text)
        self.assertIn("(dry run)", self.call("_host_action", action="reboot", dry=True)["text"])
        n = len(self.router.calls)
        self.assertTrue(self.call("_host_action", action="backup", args={"name": "before-change"})["ok"])
        self.assertEqual(self.router.calls[n:][-1][1:], ("/system/backup/save", {"name": "before-change", "dont-encrypt": "no"}))
        self.assertFalse(self.call("_host_action", action="backup", args={"name": "../x"})["ok"])
        self.assertFalse(self.call("_host_action", action="format-disk")["ok"])
        self.assertEqual(self.call("_host_exec", command="/ip dns cache flush", check=True)["text"], "checked")
        self.assertIn("(dry run)", self.call("_host_exec", command="/ip dns cache flush", dry=True)["text"])
        self.assertEqual(self.call("_host_exec", command="/ip dns cache flush")["text"], "ran: /ip dns cache flush")
        self.assertFalse(self.call("_host_exec", command="  ")["ok"])

    def test_5_bad_signature_and_type(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.req("/nkt/call", json.dumps({"op": "_host_check", "args": {"type": "ios", "conn": {}}}).encode())["ok"])


if __name__ == "__main__":
    unittest.main(verbosity=1)
