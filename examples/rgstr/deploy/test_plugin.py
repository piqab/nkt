"""Проверка плагина rgstr без rgstr: поддельный API веб-интерфейса
(/ui/api/…, /healthz, /metrics — по исходникам rgstr: вход Basic, ответы и
ошибки {"error": …}, POST только с телом JSON) с проверкой учётки и прав
администратора. Плагин — отдельным процессом, вызовы — подписанные.
Покрыто всё из п. 8 examples/AGENTS.md. Запуск: python3 -I test_plugin.py
(его вызывает go test ./examples)."""
import base64
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
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"
PASSWORD = "rgstr-admin-password-9"
ROBOT_SECRET = "robot-secret-abcdefghijklmnopqrstuvwxyz0123456"


def ago(days):
    return (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=days)).isoformat()


class FakeRgstr:
    def __init__(self):
        fake = self
        self.auth_enabled = True
        self.metrics = True
        self.repos = [
            {"name": "shop/api", "pulls": 12, "tags": [
                {"name": "1.0", "digest": "sha256:" + "a" * 64, "size": 10 << 20, "platforms": [{"os": "linux", "architecture": "amd64"}], "pushed_at": ago(3),
                 "vulns": {"status": "done", "counts": {"critical": 1, "high": 2}, "finished": ago(1)}},
                {"name": "1.1", "digest": "sha256:" + "b" * 64, "size": 11 << 20, "platforms": [{"os": "linux", "architecture": "amd64"}], "pushed_at": ago(1), "immutable": True}]},
            {"name": "tools", "pulls": 0, "tags": [{"name": "latest", "digest": "sha256:" + "c" * 64, "size": 1 << 20, "platforms": [], "pushed_at": ago(9)}]},
        ]
        self.users = [{"name": "admin", "source": "bootstrap", "role": "admin", "actions": ["pull", "push", "delete"], "must_change": False, "managed": True},
                      {"name": "dev", "source": "admin", "role": "user", "actions": ["pull", "push"], "must_change": True, "managed": True}]
        self.robots = [{"name": "robot$ci", "permissions": [{"project": "*", "actions": ["pull", "push"]}], "expired": False, "disabled": False}]
        self.projects = [{"name": "shop", "description": "магазин", "public": True, "members": 2, "repos": 1, "quota_bytes": 20 << 30, "used_bytes": 21 << 20, "block_severity": ""},
                         {"name": "mirror", "public": True, "members": 0, "repos": 0, "used_bytes": 0, "proxy": {"url": "http://mirror.lan:5000", "has_password": False}}]
        self.scan = True
        self.legacy = False  # rgstr до 1.8: нет /ui/api/scan и /ui/api/backup
        self.backup = {"enabled": True, "status": {"target": "s3://backups/rgstr", "schedule": "0 3 * * *", "keep": 7, "running": False,
                                                   "last_success": ago(3), "last_size": 30 << 20, "next": ago(-1)},
                       "backups": [{"name": "rgstr-backup-20261024T030000Z.tar", "size": 30 << 20, "time": ago(3)}]}
        self.policies = {"retention": [], "immutable": [{"repos": "shop/**", "tags": "1.*"}], "retention_history": [], "gc_interval": "1h0m0s"}
        self.gc = {"interval": "1h0m0s", "running": False, "history": [{"start": ago(0), "bytes_freed": 5 << 20, "blobs_removed": 3}]}
        self.calls = []

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

            def who(self):
                h = self.headers.get("Authorization", "")
                if h.startswith("Basic "):
                    user, _, pw = base64.b64decode(h[6:]).decode().partition(":")
                    if user == "admin" and pw == PASSWORD:
                        return "admin"
                return None

            def route(self, method):
                u = urllib.parse.urlsplit(self.path)
                path, q = u.path, urllib.parse.parse_qs(u.query)
                n = int(self.headers.get("Content-Length") or 0)
                raw = self.rfile.read(n) if n else b""
                if path == "/healthz":
                    return self.send(200, {"status": "ok", "version": "v1.10.1"})
                if path == "/metrics":
                    return self.send(200 if fake.metrics else 404, {} if fake.metrics else {"error": "not found"})
                user = self.who()
                if path == "/ui/api/me":
                    return self.send(200, {"version": "v1.10.1", "auth_enabled": fake.auth_enabled, "user": user or "", "admin": bool(user) or not fake.auth_enabled,
                                           "ldap": True, "sso": "Keycloak"})
                if fake.auth_enabled and not user:
                    return self.send(401, {"error": "authentication required"})
                body = None
                if method == "POST":
                    if not raw:
                        return self.send(400, {"error": "bad request: EOF"})
                    body = json.loads(raw)
                fake.calls.append((method, path, body))
                if fake.legacy and path.startswith(("/ui/api/scan", "/ui/api/backup")):
                    return self.send(404, {"error": "not found"})
                if method == "GET" and path == "/ui/api/scan":
                    if not fake.scan:
                        return self.send(200, {"enabled": False})
                    vulns = [{"id": "CVE-2026-0002", "severity": "high", "package": "openssl", "installed": "3.0.1", "fixed": "3.0.9", "title": "TLS"},
                             {"id": "CVE-2026-0001", "severity": "critical", "package": "zlib", "installed": "1.2", "fixed": "1.3"}]
                    return self.send(200, {"enabled": True, "summary": {"status": "done", "counts": {"critical": 1, "high": 1}, "finished": ago(1)},
                                           "digest": "sha256:" + "a" * 64, "report": {"vulnerabilities": vulns}, "q": q})
                if method == "GET" and path == "/ui/api/backup":
                    return self.send(200, fake.backup)
                if method == "GET":
                    data = {"/ui/api/repos": {"repositories": fake.repos, "total_pulls": 12}, "/ui/api/users": {"users": fake.users, "me": "admin", "auth_enabled": True},
                            "/ui/api/robots": {"robots": fake.robots, "projects": ["shop"]}, "/ui/api/projects": {"projects": fake.projects, "can_create": True},
                            "/ui/api/policies": fake.policies, "/ui/api/gc": fake.gc,
                            "/ui/api/disk": {"supported": True, "total": 100 << 30, "free": 5 << 30, "low": True},
                            "/ui/api/webhooks": {"webhooks": [{"id": "h1", "name": "ci", "url": "https://ci.example.com/x", "project": "", "events": ["push"]}], "projects": [], "is_admin": True},
                            "/ui/api/audit": {"entries": [{"time": ago(0), "user": "admin", "action": "push", "resource": "shop/api:1.1"}], "actions": ["push"]}}
                    if path in data:
                        return self.send(200, data[path])
                    return self.send(404, {"error": "not found"})
                if path == "/ui/api/tags/delete":
                    for r in fake.repos:
                        if r["name"] == body["repo"]:
                            r["tags"] = [t for t in r["tags"] if t["name"] not in body["tags"]]
                    return self.send(200, {"results": [{"tag": t} for t in body["tags"]], "gc": None})
                if path == "/ui/api/gc/run":
                    return self.send(200, {"start": ago(0), "dry_run": body.get("dry_run", False), "bytes_freed": 7 << 20, "blobs_removed": 4, "manifests_removed": 1})
                if path == "/ui/api/policies/retention/run":
                    return self.send(200, {"start": ago(0), "dry_run": body.get("dry_run", False), "tags_count": 2})
                if path == "/ui/api/projects/create":
                    if any(p["name"] == body["name"] for p in fake.projects):
                        return self.send(400, {"error": "project already exists"})
                    fake.projects.append({"name": body["name"], "public": body.get("public", False), "quota_bytes": body.get("quota_bytes", 0), "used_bytes": 0, "members": 0, "repos": 0})
                    return self.send(204)
                if path == "/ui/api/projects/delete":
                    fake.projects = [p for p in fake.projects if p["name"] != body["name"]]
                    return self.send(204)
                if path == "/ui/api/robots/create":
                    fake.robots.append({"name": "robot$" + body["name"], "permissions": body["permissions"]})
                    return self.send(200, {"name": "robot$" + body["name"], "secret": ROBOT_SECRET})
                if path == "/ui/api/robots/secret":
                    return self.send(200, {"name": body["name"], "secret": ROBOT_SECRET + "2"})
                if path == "/ui/api/robots/delete":
                    fake.robots = [r for r in fake.robots if r["name"] != body["name"]]
                    return self.send(204)
                if path == "/ui/api/policies/retention":
                    fake.policies["retention"] = body["rules"]
                    return self.send(200, fake.policies)
                if path == "/ui/api/policies/immutable":
                    fake.policies["immutable"] = body["rules"]
                    return self.send(200, fake.policies)
                if path in ("/ui/api/scan/run", "/ui/api/backup/run"):
                    return self.send(202)
                if path == "/ui/api/projects/update":
                    for p in fake.projects:
                        if p["name"] == body["name"]:
                            p.update(description=body["description"], public=body["public"], block_severity=body.get("block_severity", p.get("block_severity")))
                    return self.send(204)
                if path == "/ui/api/webhooks/test":
                    return self.send(200, {"status": 200, "event": "ping"})
                self.send(404, {"error": "not found"})

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def start(port, rg, password):
    env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(port), NKT_PLUGIN_LISTEN="127.0.0.1",
               RGSTR_URL=f"http://127.0.0.1:{rg.srv.server_address[1]}", RGSTR_USER="admin", RGSTR_PASSWORD=password, RGSTR_PUBLIC="registry.example.com")
    proc = subprocess.Popen([sys.executable, "-I", os.path.join(HERE, "nkt-plugin.py")], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    for _ in range(50):
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                break
        except OSError:
            time.sleep(0.1)
    return proc


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.rg = FakeRgstr()
        cls.port = free_port()
        cls.proc = start(cls.port, cls.rg, PASSWORD)

    @classmethod
    def tearDownClass(cls):
        cls.proc.terminate()
        out, _ = cls.proc.communicate(timeout=5)
        cls.rg.srv.shutdown()
        for s in (PASSWORD, SECRET, ROBOT_SECRET):
            assert s.encode() not in out, (s, out)

    def req(self, path, body=b"", lang="en", port=None):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{port or self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": lang})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, port=None, **args):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode(), port=port)

    def test_1_manifest(self):
        en, ru = self.req("/nkt/manifest"), self.req("/nkt/manifest", lang="ru")
        self.assertEqual((en["name"], en["pages"][0]["title"], ru["pages"][0]["title"]), ("rgstr", "Summary", "Сводка"))
        ops = {o["name"]: o for o in en["ops"]}
        self.assertTrue(ops["robot_create"]["reveals"] and ops["tag_delete"]["destructive"] and ops["repos"]["read_only"])
        self.assertEqual(en["commands"][0]["word"], "rgstr")

    def test_2_summary_findings(self):
        v = self.call("summary")["view"]
        params = {r["cells"]["param"]: r["cells"]["value"] for r in v["items"][0]["rows"]}
        self.assertEqual((params["Version"], params["Login"], params["Repositories"], params["Scanning"]), ("v1.10.1", "on", 2, "on"))
        self.assertIn("0 3 * * * UTC, keep 7", params["Backups"])
        self.assertEqual(params["Sign-in via"], "LDAP, OIDC (Keycloak)")
        found = "\n".join(r["cells"]["value"] for r in v["items"][1]["rows"])
        for want in ("temporary password", "Low disk space", "No retention policies", "Metrics /metrics are open", "Robot robot$ci: write access to all",
                     "Critical vulnerabilities: shop/api:1.0", "The backup is late", "Cache mirror reaches its upstream over HTTP"):
            self.assertIn(want, found)
        self.assertNotIn("answers over HTTP", found)  # 127.0.0.1 — не находка
        self.assertNotIn("scanning is off", found)

    def test_2b_summary_scan_backup_off_and_legacy(self):
        rg = self.rg
        rg.scan, saved = False, rg.backup
        rg.backup = {"enabled": False}
        try:
            v = self.call("summary")["view"]
            found = "\n".join(r["cells"]["value"] for r in v["items"][1]["rows"])
            self.assertIn("Vulnerability scanning is off", found)
            self.assertIn("Built-in backups are not configured", found)
            self.assertEqual(self.call("backups")["view"]["type"], "text")
            rg.backup = {"enabled": True, "status": {"schedule": "0 3 * * *", "keep": 7, "last_error": "s3: access denied", "last_error_at": ago(0)}}
            found = "\n".join(r["cells"]["value"] for r in self.call("summary")["view"]["items"][1]["rows"])
            self.assertIn("The last backup failed: s3: access denied", found)
            rg.legacy = True  # старый rgstr: сводка работает, находок о бэкапах нет
            v = self.call("summary")["view"]
            params = {r["cells"]["param"]: r["cells"]["value"] for r in v["items"][0]["rows"]}
            self.assertEqual(params["Scanning"], "—")
            self.assertNotIn("backup", "\n".join(r["cells"]["value"] for r in v["items"][1]["rows"]).lower())
        finally:
            rg.scan, rg.backup, rg.legacy = True, saved, False

    def test_3_views(self):
        repos = {r["cells"]["repo"]: r for r in self.call("repos")["view"]["rows"]}
        self.assertEqual(sorted(repos), ["shop/api", "tools"])
        self.assertEqual(repos["shop/api"]["cells"]["n_tags"], 2)
        self.assertEqual(repos["shop/api"]["cells"]["vulns"], "critical")
        tags = {r["cells"]["tag"]: r for r in self.call("tags", repo="shop/api")["view"]["rows"]}
        self.assertNotIn("tag_delete", [a.get("op") for a in tags["1.1"]["actions"]])  # неизменяемый — без «Удалить»
        self.assertEqual([a.get("op") or a.get("open") for a in tags["1.0"]["actions"]], ["vulns", "scan_run", "tag_delete"])
        self.assertEqual(tags["1.0"]["cells"]["vulns"], "critical 1, high 2")
        vv = self.call("vulns", repo="shop/api", tag="1.0")["view"]
        self.assertEqual([r["cells"]["vuln_id"] for r in vv["rows"]], ["CVE-2026-0001", "CVE-2026-0002"])  # сначала критические
        b = self.call("backups")["view"]["items"]
        self.assertEqual(b[1]["rows"][0]["cells"]["backup_file"], "rgstr-backup-20261024T030000Z.tar")
        projects = {r["cells"]["project"]: r for r in self.call("projects")["view"]["rows"]}
        self.assertEqual(projects["mirror"]["cells"]["cache"], "http://mirror.lan:5000")
        for op in ("projects", "robots", "users", "policies", "webhooks", "audit"):
            self.assertTrue(self.call(op)["ok"], op)
        doc = json.loads(self.call("policies_get")["result"]["text"])
        self.assertEqual(doc["immutable"][0]["repos"], "shop/**")

    def test_4_changes_and_dry_run(self):
        n = len(self.rg.calls)
        self.assertIn("(dry run)", self.call("tag_delete", dry=True, repo="tools", tag="latest")["text"])
        self.assertIn("(dry run)", self.call("project_create", dry=True, name="team-b")["text"])
        self.assertIn("(dry run)", self.call("robot_delete", dry=True, name="ci")["text"])
        self.assertFalse(any(m == "POST" for m, _, _ in self.rg.calls[n:]))  # сухой прогон ничего не менял
        self.assertIn("would free 7.0 MB", self.call("gc_run", dry=True)["text"])  # сухой прогон rgstr — без изменений
        self.assertTrue(self.rg.calls[-1][2]["dry_run"])
        self.assertTrue(self.call("tag_delete", repo="tools", tag="latest")["ok"])
        self.assertEqual(self.rg.calls[-1], ("POST", "/ui/api/tags/delete", {"repo": "tools", "tags": ["latest"], "free_space": False}))
        self.assertTrue(self.call("project_create", name="team-b", quota_gb=5, public=True)["ok"])
        self.assertEqual(self.rg.calls[-1][2]["quota_bytes"], 5 << 30)
        self.assertIn("deleted tags 2", self.call("retention_run")["text"])
        doc = json.dumps({"retention": [{"repos": "**", "tags": "*", "keep_last": 10}], "immutable": []})
        self.assertTrue(self.call("policies_put", text=doc)["ok"])
        self.assertEqual(self.rg.policies["retention"][0]["keep_last"], 10)
        self.assertFalse(self.call("policies_put", text="{not json")["ok"])
        self.assertIn("answer 200", self.call("webhook_test", id="h1")["text"])
        n = len(self.rg.calls)
        for op, a in (("scan_run", {"repo": "shop/api", "tag": "1.0"}), ("backup_run", {}), ("project_block", {"name": "shop", "severity": "high"})):
            self.assertIn("(dry run)", self.call(op, dry=True, **a)["text"])
        self.assertFalse(any(m == "POST" for m, _, _ in self.rg.calls[n:]))
        self.assertTrue(self.call("scan_run", repo="shop/api", tag="1.0")["ok"])
        self.assertEqual(self.rg.calls[-1], ("POST", "/ui/api/scan/run", {"repo": "shop/api", "digest": "sha256:" + "a" * 64}))
        self.assertIn("backup started", self.call("backup_run")["text"])
        self.assertTrue(self.call("project_block", name="shop", severity="critical")["ok"])
        self.assertEqual(self.rg.calls[-1][2], {"name": "shop", "description": "магазин", "public": True, "block_severity": "critical"})
        self.assertTrue(self.call("project_block", name="shop", severity="none")["ok"])
        self.assertEqual(self.rg.calls[-1][2]["block_severity"], "")
        self.assertTrue(self.call("project_create", name="dockerhub", proxy_url="https://registry-1.docker.io/", block_severity="critical")["ok"])
        self.assertEqual(self.rg.calls[-1][2]["proxy"], {"url": "https://registry-1.docker.io"})
        self.assertEqual(self.rg.calls[-1][2]["block_severity"], "critical")

    def test_5_input_checks(self):
        for bad in ("../etc", "Shop/API", "a b", "x;rm", "-x", "shop//api"):
            self.assertFalse(self.call("tag_delete", repo=bad, tag="1.0")["ok"], bad)
        for bad in ("../x", "a b", "-rf", "x;y", "."):
            self.assertFalse(self.call("tag_delete", repo="shop/api", tag=bad)["ok"], bad)
        for bad in ("Bad", "a b", "../x", "-x", ""):
            self.assertFalse(self.call("project_create", name=bad)["ok"], bad)
        self.assertFalse(self.call("project_create", name="ok", quota_gb="-1")["ok"])
        self.assertFalse(self.call("webhook_test", id="h1/../x")["ok"])
        for bad in ("ftp://x", "https://user:pass@hub.example.com", "http://a b", "-x", "https://", "https://x?y=1", "http://x:99999"):
            self.assertFalse(self.call("project_create", name="ok", proxy_url=bad)["ok"], bad)
        for bad in ("severe", "x;y", "../x"):
            self.assertFalse(self.call("project_block", name="shop", severity=bad)["ok"], bad)
        self.assertFalse(self.call("scan_run", repo="../x", tag="1")["ok"])

    def test_6_robot_secret(self):
        r = self.call("robot_create", name="ci-shop", project="shop")
        self.assertEqual(r["reveal"]["value"], ROBOT_SECRET)  # окну — один раз
        self.assertIn("docker login registry.example.com -u 'robot$ci-shop'", r["reveal"]["note"])
        self.assertEqual(self.rg.calls[-1][2]["permissions"], [{"project": "shop", "actions": ["pull"]}])
        s = self.call("robot_create", name="ci-2", project="shop", push=True, to_pipeline="shop", env="@registry")
        self.assertEqual(s["store"], {"pipeline": "shop", "env": "@registry", "value": ROBOT_SECRET, "registry": "registry.example.com", "user": "robot$ci-2"})
        self.assertNotIn("reveal", s)
        e = self.call("robot_secret", name="ci-2", to_pipeline="shop", env="REGISTRY_TOKEN")
        self.assertEqual(e["store"], {"pipeline": "shop", "env": "REGISTRY_TOKEN", "value": ROBOT_SECRET + "2"})
        self.assertFalse(self.call("robot_create", name="x", project="shop", to_pipeline="shop", env="bad-env")["ok"])
        self.assertFalse(self.call("robot_create", name="x", project="shop", to_pipeline="shop")["ok"])

    def test_7_commands(self):
        for argv in (["rgstr", "gc"], ["rgstr", "tags", "shop/api"], ["rgstr", "retention", "run"], ["rgstr", "project", "team-c", "quota", "10"],
                     ["rgstr", "robot", "ci", "project", "shop", "push", "to-pipeline", "shop", "env", "@registry"], ["rgstr", "scan", "shop/api", "1.0"],
                     ["rgstr", "backup", "run"], ["rgstr", "project", "hub", "proxy", "https://registry-1.docker.io", "block", "high", "quota", "5"]):
            self.assertTrue(self.call("_check", argv=argv)["ok"], argv)
        for argv in (["rgstr", "tags", "../x"], ["rgstr", "bogus"], ["rgstr", "robot", "ci", "project", "shop"], ["rgstr", "project", "x", "size", "1"],
                     ["rgstr", "project", "x", "proxy", "ftp://y"], ["rgstr", "project", "x", "quota", "1", "quota", "2"], ["rgstr", "scan", "shop/api"]):
            self.assertFalse(self.call("_check", argv=argv)["ok"], argv)
        out = self.call("_command", argv=["rgstr", "robot", "ci3", "project", "*", "to-pipeline", "shop", "env", "@registry"])
        self.assertEqual(out["store"]["user"], "robot$ci3")
        self.assertNotIn("reveal", out)
        self.assertIn("shop/api", self.call("_command", argv=["rgstr", "repos"])["text"])
        self.assertIn("(dry run)", self.call("_command", dry=True, argv=["rgstr", "gc"])["text"])

    def test_8_errors_and_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.call("no_such_op")["ok"])
        port = free_port()
        bad = start(port, self.rg, "wrong-password")
        try:
            err = self.call("repos", port=port)
            self.assertIn("login rejected", err["error"])
        finally:
            bad.terminate()
            bad.communicate(timeout=5)


if __name__ == "__main__":
    unittest.main(verbosity=1)
