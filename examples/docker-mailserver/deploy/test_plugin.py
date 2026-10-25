"""Проверка плагина docker-mailserver с поддельным docker (без Docker):
поддельный docker exec имитирует setup и пишет вызовы в журнал — по нему
видно, что пароль ящика не попадает в командную строку.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import hashlib
import hmac
import json
import os
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
SECRET = "test-plugin-secret-0123456789"

FAKE = r'''#!/usr/bin/env python3
import json, os, sys
state_path = os.environ["FAKE_STATE"]
st = json.load(open(state_path)) if os.path.exists(state_path) else {"boxes": {}, "aliases": [], "dkim": {}}
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps(sys.argv[1:]) + "\n")
a = sys.argv[1:]
assert a[0] == "exec"
a = a[1:]
envs = {}
while a and a[0] in ("-i", "-e"):
    if a[0] == "-e":
        envs[a[1]] = os.environ.get(a[1], "")
        a = a[2:]
    else:
        a = a[1:]
if a[0] != "mailserver":
    print("No such container", file=sys.stderr); sys.exit(1)
a = a[1:]
out = ""
if a[:2] == ["bash", "-c"]:
    verb = "add" if "email add" in a[2] else "update"
    email, pw = a[4], envs.get("NKT_MAIL_PASS", "")
    if len(pw) < 8:
        print("bad password", file=sys.stderr); sys.exit(1)
    st["boxes"][email] = "hash:" + str(len(pw))
elif a[:3] == ["setup", "email", "list"]:
    out = "".join(f"* {e} ( 12K / ~ ) [0%]\n" for e in sorted(st["boxes"]))
elif a[:3] == ["setup", "email", "del"]:
    st["boxes"].pop(a[-1], None)
elif a[:3] == ["setup", "alias", "list"]:
    out = "".join(f"* {x} {y}\n" for x, y in st["aliases"])
elif a[:3] == ["setup", "alias", "add"]:
    st["aliases"].append([a[3], a[4]])
elif a[:3] == ["setup", "alias", "del"]:
    st["aliases"] = [p for p in st["aliases"] if p != [a[3], a[4]]]
elif a[:4] == ["setup", "config", "dkim", "domain"]:
    st["dkim"][a[4]] = 'mail._domainkey IN TXT ( "v=DKIM1; k=rsa; "\n  "p=MIIBIjANBgkq" "hkiG9w0BAQ" ) ;'
elif a[:2] == ["sh", "-c"]:
    # Домен — аргументом оболочки ($1), не текстом команды.
    assert '"$1"' in a[2] and len(a) == 5, a
    for d, body in st["dkim"].items():
        if d == a[4]:
            out += f"== /tmp/docker-mailserver/rspamd/dkim/rsa-2048-mail-{d}.public.dns.txt\n{body}\n"
else:
    print("unknown " + " ".join(a), file=sys.stderr); sys.exit(1)
json.dump(st, open(state_path, "w"))
sys.stdout.write(out)
'''


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dir = tempfile.mkdtemp()
        cls.docker = os.path.join(cls.dir, "docker")
        with open(cls.docker, "w") as f:
            f.write(FAKE)
        os.chmod(cls.docker, os.stat(cls.docker).st_mode | stat.S_IEXEC)
        cls.log = os.path.join(cls.dir, "calls.log")
        cls.port = free_port()
        env = dict(os.environ, DOCKER=cls.docker, FAKE_STATE=os.path.join(cls.dir, "state.json"), FAKE_LOG=cls.log,
                   NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   MAIL_HOSTNAME="mail.example.com", MAIL_PUBLIC_IP="203.0.113.25")
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
        shutil.rmtree(cls.dir, ignore_errors=True)

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=10) as resp:
            return json.load(resp)

    def call(self, op, args=None, dry=False):
        return self.req("/nkt/call", json.dumps({"op": op, "args": args or {}, "dry_run": dry}).encode())

    def test_1_mailboxes_and_password_not_in_argv(self):
        self.assertEqual(self.req("/nkt/manifest")["name"], "mail")
        self.assertTrue(self.call("mailbox_add", {"email": "User@Example.com", "password": "s3cret-pass-1"})["ok"])
        self.assertFalse(self.call("mailbox_add", {"email": "x@example.com", "password": "short"})["ok"])
        self.assertFalse(self.call("mailbox_add", {"email": "bad address", "password": "s3cret-pass-1"})["ok"])
        boxes = self.call("mailboxes", {"domain": "example.com"})["view"]["rows"]
        self.assertEqual([r["cells"]["email"] for r in boxes], ["user@example.com"])
        self.assertTrue(self.call("mailbox_password", {"email": "user@example.com", "password": "another-pass-2"})["ok"])
        with open(self.log) as f:
            calls = f.read()
        self.assertNotIn("s3cret-pass-1", calls)
        self.assertNotIn("another-pass-2", calls)
        dom = self.call("domains")["view"]["rows"]
        self.assertEqual(dom[0]["cells"]["domain"], "example.com")
        self.assertEqual(dom[0]["cells"]["dkim"], "no")

    def test_2_aliases_dkim_dns(self):
        self.assertTrue(self.call("alias_add", {"alias": "info@example.com", "target": "user@example.com"})["ok"])
        self.assertEqual(self.call("aliases")["view"]["rows"][0]["cells"], {"alias": "info@example.com", "target": "user@example.com"})
        view = self.call("dns", {"domain": "example.com"})["view"]
        self.assertIn("No DKIM key", view["note"])
        self.assertIn("dkim key for example.com created", self.call("dkim_generate", {"domain": "example.com"})["text"].lower())
        view = self.call("dns", {"domain": "example.com"})["view"]
        recs = view["actions"][0]["dns"]["records"]
        by = {(r["name"], r["type"]): r["values"][0] for r in recs}
        self.assertEqual(by[("example.com", "MX")], "10 mail.example.com.")
        self.assertEqual(by[("mail._domainkey.example.com", "TXT")], '"v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQ"')
        self.assertEqual(by[("mail.example.com", "A")], "203.0.113.25")
        self.assertEqual(view["actions"][0]["dns"]["owner"], "domain:example.com")
        self.assertIn("already", self.call("dkim_generate", {"domain": "example.com"})["text"])

    def test_3_commands_and_delete(self):
        self.assertTrue(self.call("_check", {"argv": ["mail", "alias", "add", "a@example.com", "b@example.com"]})["ok"])
        self.assertFalse(self.call("_check", {"argv": ["mail", "add", "x@example.com", "pass"]})["ok"])
        out = self.call("_command", {"argv": ["mail", "rm", "user@example.com"]}, dry=True)["text"]
        self.assertIn("(dry run)", out)
        self.assertTrue(self.call("mailbox_delete", {"email": "user@example.com"})["ok"])
        self.assertEqual(self.call("mailboxes")["view"]["rows"], [])


if __name__ == "__main__":
    unittest.main(verbosity=1)
