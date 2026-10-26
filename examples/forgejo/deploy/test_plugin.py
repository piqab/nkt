"""Проверка плагина Forgejo без Forgejo: поддельный REST API /api/v1 (по
документации Forgejo: version, repos/search, admin/users, actions/tasks,
hooks, mirror-sync, registration-token) с проверкой токена. Плагин —
отдельным процессом, вызовы — подписанные. Покрыто всё из п. 8
examples/AGENTS.md. Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
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
TOKEN = "forgejo-admin-token-xyz"
RUNNER = "runner-registration-token-777"


def ago(days):
    return (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=days)).isoformat()


class FakeForgejo:
    def __init__(self):
        fake = self
        self.repos = [
            {"full_name": "team/site", "private": False, "mirror": False, "archived": False, "empty": False, "size": 2048, "updated_at": ago(1)},
            {"full_name": "team/upstream", "private": False, "mirror": True, "archived": False, "empty": False, "size": 512, "updated_at": ago(2), "mirror_updated": ago(12)},
            {"full_name": "alex/notes", "private": True, "mirror": False, "archived": False, "empty": False, "size": 10, "updated_at": ago(3)},
        ]
        self.users = [{"login": "root", "email": "root@example.com", "is_admin": True, "last_login": ago(0)},
                      {"login": "dev", "email": "dev@example.com", "is_admin": False, "last_login": "1970-01-01T00:00:00Z", "prohibit_login": True}]
        self.hooks = {"team/site": [{"id": 5, "config": {"url": "https://ci.example.com/x"}, "events": ["push"], "active": True}]}
        self.runs = {"team/site": [{"id": 9, "run_number": 42, "name": "build", "head_branch": "main", "event": "push", "status": "failure", "created_at": ago(0)}],
                     "alex/notes": [{"id": 3, "run_number": 7, "name": "lint", "head_branch": "main", "event": "push", "status": "success", "created_at": ago(1)}]}
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

            def route(self, method):
                if self.headers.get("Authorization") != "token " + TOKEN:
                    return self.send(401, {"message": "token is required"})
                u = urllib.parse.urlsplit(self.path)
                path = urllib.parse.unquote(u.path).removeprefix("/api/v1")
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n)) if n else None
                fake.calls.append((method, path, body))
                page = int(urllib.parse.parse_qs(u.query).get("page", ["1"])[0])
                if method == "GET" and path == "/version":
                    return self.send(200, {"version": "9.0.1+gitea-1.22.0"})
                if method == "GET" and path == "/repos/search":
                    return self.send(200, {"ok": True, "data": fake.repos if page == 1 else []})
                if method == "GET" and path == "/admin/users":
                    return self.send(200, fake.users if page == 1 else [])
                if method == "GET" and path == "/admin/runners/registration-token":
                    return self.send(200, {"token": RUNNER})
                if method in ("POST",) and path == "/user/repos":
                    fake.repos.append({"full_name": "root/" + body["name"], "private": body["private"], "size": 0, "updated_at": ago(0)})
                    return self.send(201, {"full_name": "root/" + body["name"]})
                if method == "POST" and path.startswith("/orgs/"):
                    org = path.split("/")[2]
                    fake.repos.append({"full_name": f"{org}/{body['name']}", "private": body["private"], "size": 0, "updated_at": ago(0)})
                    return self.send(201, {"full_name": f"{org}/{body['name']}"})
                parts = path.split("/")
                if len(parts) >= 4 and parts[1] == "repos":
                    repo = f"{parts[2]}/{parts[3]}"
                    rest = "/".join(parts[4:])
                    if not any(r["full_name"] == repo for r in fake.repos):
                        return self.send(404, {"message": "not found"})
                    if method == "GET" and rest == "actions/tasks":
                        return self.send(200, {"workflow_runs": fake.runs.get(repo, []), "total_count": len(fake.runs.get(repo, []))})
                    if method == "GET" and rest == "actions/runners/registration-token":
                        return self.send(200, {"token": RUNNER + "-repo"})
                    if method == "GET" and rest == "hooks":
                        return self.send(200, fake.hooks.get(repo, []))
                    if method == "POST" and rest == "hooks":
                        fake.hooks.setdefault(repo, []).append({"id": 6, "config": {"url": body["config"]["url"]}, "events": body["events"], "active": True})
                        return self.send(201, {})
                    if method == "DELETE" and rest.startswith("hooks/"):
                        fake.hooks[repo] = [h for h in fake.hooks.get(repo, []) if str(h["id"]) != rest.split("/")[1]]
                        return self.send(204)
                    if method == "POST" and rest == "mirror-sync":
                        return self.send(200)
                    if method == "PATCH" and rest == "":
                        next(r for r in fake.repos if r["full_name"] == repo)["archived"] = bool(body.get("archived"))
                        return self.send(200, {})
                    if method == "DELETE" and rest == "":
                        fake.repos = [r for r in fake.repos if r["full_name"] != repo]
                        return self.send(204)
                self.send(404, {"message": "not found"})

            def do_GET(self):
                self.route("GET")

            def do_POST(self):
                self.route("POST")

            def do_PATCH(self):
                self.route("PATCH")

            def do_DELETE(self):
                self.route("DELETE")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.fj = FakeForgejo()
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   FORGEJO_URL=f"http://127.0.0.1:{cls.fj.srv.server_address[1]}", FORGEJO_TOKEN=TOKEN)
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
        cls.fj.srv.shutdown()
        for s in (TOKEN, SECRET, RUNNER, "hook-secret-1"):
            assert s.encode() not in out, (s, out)

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
        self.assertEqual((en["name"], en["pages"][0]["title"], ru["pages"][0]["title"]), ("forgejo", "Summary", "Сводка"))
        ops = {o["name"]: o for o in en["ops"]}
        self.assertTrue(ops["runner_token"]["reveals"] and ops["repo_delete"]["destructive"] and ops["repos"]["read_only"])

    def test_2_summary_findings(self):
        v = self.call("summary")["view"]
        params = {r["cells"]["param"]: r["cells"]["value"] for r in v["items"][0]["rows"]}
        self.assertEqual((params["Version"], params["Repositories"], params["Users"], params["Administrators"]), ("9.0.1+gitea-1.22.0", 3, 2, 1))
        found = [r["cells"]["value"] for r in v["items"][1]["rows"]]
        self.assertEqual(len(found), 2, found)
        self.assertIn("team/site: the last run “build” — failure", found)
        self.assertTrue(any(f.startswith("team/upstream: the mirror has not synced for 12") for f in found))

    def test_3_views(self):
        v = self.call("repos")["view"]
        rows = {r["cells"]["repo"]: r for r in v["rows"]}
        self.assertEqual(sorted(rows), ["alex/notes", "team/site", "team/upstream"])
        self.assertEqual(rows["team/upstream"]["cells"]["kind"], "mirror")
        self.assertIn("mirror_sync", [a.get("op") for a in rows["team/upstream"]["actions"]])
        self.assertEqual(rows["team/site"]["cells"]["size"], 2.0)
        runs = self.call("runs", repo="team/site")["view"]
        self.assertEqual(runs["rows"][0]["cells"]["status"], "failure")
        hooks = self.call("hooks", repo="team/site")["view"]
        self.assertEqual(hooks["rows"][0]["cells"]["url"], "https://ci.example.com/x")
        users = self.call("users")["view"]
        self.assertEqual([(r["cells"]["login"], r["cells"]["state"]) for r in users["rows"]], [("root", ""), ("dev", "blocked")])

    def test_4_changes_and_dry_run(self):
        n = len(self.fj.calls)
        self.assertIn("(dry run)", self.call("repo_create", dry=True, name="new-app", org="team")["text"])
        self.assertIn("(dry run)", self.call("repo_delete", dry=True, repo="alex/notes")["text"])
        self.assertFalse(any(m != "GET" for m, _, _ in self.fj.calls[n:]))  # сухой прогон ничего не менял
        self.assertTrue(self.call("repo_create", name="new-app", org="team", private=True)["ok"])
        self.assertEqual(self.fj.calls[-1][:2], ("POST", "/orgs/team/repos"))
        self.assertTrue(self.call("hook_add", repo="team/site", url="https://hub.example.com/hooks/abc", secret="hook-secret-1")["ok"])
        self.assertEqual(self.fj.calls[-1][2]["config"], {"url": "https://hub.example.com/hooks/abc", "content_type": "json", "secret": "hook-secret-1"})
        self.assertTrue(self.call("hook_delete", repo="team/site", id="5")["ok"])
        self.assertTrue(self.call("mirror_sync", repo="team/upstream")["ok"])
        self.assertTrue(self.call("repo_archive", repo="alex/notes")["ok"])
        self.assertTrue(self.call("repo_delete", repo="team/new-app")["ok"])
        self.assertNotIn("team/new-app", [r["full_name"] for r in self.fj.repos])

    def test_5_input_checks(self):
        for bad in ("../etc/passwd", "team", "team/a b", "-x/y", "team/x;rm", "a/../b"):
            self.assertFalse(self.call("repo_delete", repo=bad)["ok"], bad)
        for bad in ("../x", "a b", "-rf", ""):
            self.assertFalse(self.call("repo_create", name=bad)["ok"], bad)
        for bad in ("ftp://x", "https://user:pw@x", "https://x y", "javascript:alert(1)"):
            self.assertFalse(self.call("hook_add", repo="team/site", url=bad)["ok"], bad)
        self.assertFalse(self.call("hook_delete", repo="team/site", id="5;1")["ok"])

    def test_6_runner_token(self):
        r = self.call("runner_token")
        self.assertEqual(r["reveal"]["value"], RUNNER)  # окну — один раз
        s = self.call("runner_token", repo="team/site", to_pipeline="runner", env="RUNNER_TOKEN")
        self.assertEqual(s["store"], {"pipeline": "runner", "env": "RUNNER_TOKEN", "value": RUNNER + "-repo"})
        self.assertNotIn("reveal", s)
        self.assertFalse(self.call("runner_token", to_pipeline="runner", env="bad-env")["ok"])

    def test_7_commands(self):
        self.assertTrue(self.call("_check", argv=["forgejo", "mirror-sync", "team/upstream"])["ok"])
        self.assertFalse(self.call("_check", argv=["forgejo", "mirror-sync", "../x"])["ok"])
        self.assertTrue(self.call("_check", argv=["forgejo", "runner-token", "to-pipeline", "runner", "env", "RUNNER_TOKEN"])["ok"])
        self.assertFalse(self.call("_check", argv=["forgejo", "bogus"])["ok"])
        out = self.call("_command", argv=["forgejo", "runner-token", "to-pipeline", "runner", "env", "RUNNER_TOKEN"])
        self.assertEqual(out["store"]["value"], RUNNER)
        self.assertNotIn("reveal", out)
        self.assertIn("team/site", self.call("_command", argv=["forgejo", "repos"])["text"])

    def test_8_errors_and_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)
        self.assertFalse(self.call("no_such_op")["ok"])
        self.assertIn("not found", self.call("runs", repo="nobody/none")["error"])


if __name__ == "__main__":
    unittest.main(verbosity=1)
