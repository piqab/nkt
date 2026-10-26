"""Проверка плагина Home Assistant без Home Assistant: поддельный REST API
(/api/config, /api/states, /api/template, /api/services) с проверкой
Bearer-токена. Токен — не в журнале плагина.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import datetime
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
TOKEN = "ha-long-lived-token-xyz"


def ago(minutes):
    return (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(minutes=minutes)).isoformat()


STATES = [
    {"entity_id": "light.hall", "state": "on", "attributes": {"friendly_name": "Свет в прихожей"}, "last_changed": ago(5)},
    {"entity_id": "sensor.door_battery", "state": "12", "attributes": {"friendly_name": "Дверь", "device_class": "battery", "unit_of_measurement": "%"}, "last_changed": ago(60)},
    {"entity_id": "sensor.window_battery", "state": "85", "attributes": {"friendly_name": "Окно", "device_class": "battery", "unit_of_measurement": "%"}, "last_changed": ago(60)},
    {"entity_id": "sensor.garage_temp", "state": "unavailable", "attributes": {"friendly_name": "Гараж"}, "last_changed": ago(120)},
    {"entity_id": "switch.kettle", "state": "unavailable", "attributes": {"friendly_name": "Чайник"}, "last_changed": ago(3)},
    {"entity_id": "update.home_assistant_core_update", "state": "on",
     "attributes": {"friendly_name": "Core", "title": "Home Assistant Core", "installed_version": "2026.9.3", "latest_version": "2026.10.1"}, "last_changed": ago(600)},
    {"entity_id": "update.zigbee2mqtt", "state": "off", "attributes": {"title": "Zigbee2MQTT"}, "last_changed": ago(600)},
    {"entity_id": "automation.lights_off", "state": "on", "attributes": {"friendly_name": "Выключить свет"}, "last_changed": ago(600)},
    {"entity_id": "automation.old", "state": "off", "attributes": {"friendly_name": "Старая"}, "last_changed": ago(600)},
    {"entity_id": "script.good_night", "state": "off", "attributes": {"friendly_name": "Спокойной ночи"}, "last_changed": ago(600)},
    {"entity_id": "sensor.backup_last_successful_automatic_backup", "state": ago(60 * 24 * 10), "attributes": {}, "last_changed": ago(600)},
]


class FakeHA:
    def __init__(self):
        fake = self
        self.calls = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, obj):
                raw = (obj if isinstance(obj, str) else json.dumps(obj)).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json" if not isinstance(obj, str) else "text/plain")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def authed(self):
                if self.headers.get("Authorization") != "Bearer " + TOKEN:
                    self.send(401, {"message": "Unauthorized"})
                    return False
                return True

            def do_GET(self):
                if not self.authed():
                    return
                if self.path == "/api/config":
                    return self.send(200, {"location_name": "Дом", "version": "2026.9.3", "time_zone": "Europe/Moscow",
                                           "components": ["zha", "mqtt", "sensor.mqtt", "automation", "backup"], "installation_type": "Home Assistant OS"})
                if self.path == "/api/states":
                    return self.send(200, STATES)
                self.send(404, {"message": "not found"})

            def do_POST(self):
                if not self.authed():
                    return
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
                if self.path == "/api/template":
                    t = body.get("template", "")
                    return self.send(200, "7" if "device_id" in t else "Прихожая|3\nГараж|2\n")
                if self.path.startswith("/api/services/"):
                    fake.calls.append((self.path, body))
                    return self.send(200, [{"entity_id": body.get("entity_id", "light.hall"), "state": "off"}])
                self.send(404, {"message": "not found"})

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.ha = FakeHA()
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1")
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
        cls.ha.srv.shutdown()
        assert TOKEN.encode() not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, conn=None, **args):
        args.update(type="home-assistant", conn=conn or {"address": f"127.0.0.1:{self.ha.srv.server_address[1]}", "password": TOKEN, "tls": "http"})
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def test_1_manifest(self):
        t = self.req("/nkt/manifest")["host_types"][0]
        self.assertEqual((t["id"], t["icon"], t["exec"]), ("home-assistant", "homeassistant", "ha-service"))

    def test_2_check(self):
        r = self.call("_host_check")["result"]
        self.assertEqual((r["name"], r["os"], r["version"], r["model"]), ("Дом", "Home Assistant", "2026.9.3", "Home Assistant OS"))
        bad = self.call("_host_check", conn={"address": f"127.0.0.1:{self.ha.srv.server_address[1]}", "password": "nope"})
        self.assertIn("token was rejected", bad["error"])
        self.assertFalse(self.call("_host_check", conn={"address": "-oProxy", "password": TOKEN})["ok"])

    def test_3_snapshot(self):
        s = self.call("_host_snapshot")["result"]
        ex = {x["title"]: x["rows"] for x in s["extra"]}
        summary = {r["cells"]["name"]: r["cells"]["value"] for r in ex["Summary"]}
        self.assertEqual((summary["Entities"], summary["Devices"], summary["Unavailable"], summary["Integrations"]), ("11", "7", "2", "5"))
        self.assertEqual([r["cells"]["name"] for r in ex["Areas"]], ["Прихожая", "Гараж"])
        self.assertEqual([r["cells"]["entity"] for r in ex["Available updates"]], ["update.home_assistant_core_update"])
        self.assertEqual([r["cells"]["entity"] for r in ex["Unavailable entities"]], ["sensor.garage_temp"])  # чайник — 3 мин
        self.assertEqual([(x["name"], x["enabled"]) for x in s["services"]], [("Выключить свет", True), ("Старая", False)])

    def test_4_findings_config(self):
        f = {x["id"]: x for x in self.call("_host_findings")["result"]["findings"]}
        self.assertEqual(sorted(f), ["backup", "battery-door_battery", "unavailable", "update-home_assistant_core_update"])
        self.assertIn("Гараж", f["unavailable"]["title"])
        self.assertEqual(f["backup"]["title"], "No backup for 10 days")
        text = self.call("_host_config")["result"]["text"]
        self.assertIn("light.hall  # Свет в прихожей", text)
        self.assertNotIn("unavailable", text)  # состояний в конфигурации нет

    def test_5_actions_exec(self):
        self.assertIn("(dry run)", self.call("_host_action", dry=True, action="restart")["text"])
        self.assertTrue(self.call("_host_action", action="update", args={"entity": "update.home_assistant_core_update"})["ok"])
        self.assertEqual(self.ha.calls[-1], ("/api/services/update/install", {"entity_id": "update.home_assistant_core_update"}))
        self.assertFalse(self.call("_host_action", action="update", args={"entity": "light.hall"})["ok"])
        self.assertFalse(self.call("_host_action", action="script", args={"entity": "script.x/../../api"})["ok"])
        self.assertEqual(self.call("_host_exec", command='light.turn_off {"entity_id": "light.hall"}', check=True)["text"], "checked")
        self.assertFalse(self.call("_host_exec", command="light.turn_off [1]", check=True)["ok"])
        self.assertFalse(self.call("_host_exec", command="../config/x", check=True)["ok"])
        self.assertEqual(self.call("_host_exec", command='light.turn_off {"entity_id": "light.hall"}')["text"], "light.hall: off")
        self.assertEqual(self.ha.calls[-1], ("/api/services/light/turn_off", {"entity_id": "light.hall"}))

    def test_6_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
