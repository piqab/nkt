"""Проверка плагина Windows без Windows: поддельные ssh и ssh-keyscan.
Поддельный ssh проверяет загрузчик (короткая командная строка, -EncodedCommand
читает сценарий из стандартного ввода) и отвечает JSON, как PowerShell, по
маркеру сценария «# nkt:…». Пароль — не в аргументах и не в журнале.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import base64
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
PASS = "Win-Pass-2026!"
HOSTKEY = base64.b64encode(b"\x00\x00\x00\x0bssh-ed25519" + b"\x00\x00\x00\x20" + bytes(range(32, 64))).decode()

ANSWERS = {
    "info": {"name": "WIN-SRV01", "os": "Microsoft Windows Server 2022 Standard", "version": "10.0.20348 (build 20348)", "model": "QEMU Standard PC",
             "serial": "VM-123", "arch": "64-bit", "uptime_s": 7200, "domain": "WORKGROUP"},
    "snapshot": {"cpu": {"model": "Intel Xeon", "cores": 4}, "mem_total": 8589934592,
                 "interfaces": {"name": "Ethernet", "type": "Red Hat VirtIO", "mac": "52-54-00-12-34-56", "up": True, "addrs": "10.0.0.20"},
                 "services": [{"name": "Spooler", "state": "running", "enabled": True, "detail": "Диспетчер печати"}, {"name": "sshd", "state": "running", "enabled": True}],
                 "packages": [{"name": "7-Zip 23.01 (x64)", "version": "23.01"}], "ports": [{"proto": "tcp", "port": 22, "service": "sshd", "allowed": ""},
                                                                                       {"proto": "tcp", "port": 3389, "service": "svchost", "allowed": ""}],
                 "users": {"name": "Administrator", "group": ""}, "admins": {"name": "WIN-SRV01\\Administrator"},
                 "disks": [{"name": "C:", "size": 107374182400, "used": 53687091200, "mount": "C:"}], "kb": [{"id": "KB5034439", "installed": "01/10/2026 00:00:00"}],
                 "roles": [{"feature": "Веб-сервер (IIS)"}]},
    "metrics": {"cpu_pct": 17.0, "mem_total": 8589934592, "mem_used": 4294967296, "disk_size": 107374182400, "disk_used": 53687091200, "uptime_s": 7200,
                "net": {"Ethernet": {"rx": 123456, "tx": 654321}}},
    "findings": {"updates": 3, "reboot": True, "defender": True, "rtp": False, "sig_age": 12, "firewall": [{"name": "Domain", "enabled": True}, {"name": "Public", "enabled": False}],
                 "rdp": True, "nla": False, "smb1": True, "guest": False, "bitlocker": "Off", "sysdrive": "C:", "disks": {"drive": "C:", "pct": 50},
                 "errors": 7, "top": "Service Control Manager (5), DCOM (2)"},
}

FAKE_SSH = r'''#!/usr/bin/env python3
import base64, json, os, sys
args = sys.argv[1:]
script = sys.stdin.read()
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps({"argv": args, "script": script}) + "\n")
if "-i" not in args and os.environ.get("NKT_SSH_PASS") != os.environ["FAKE_PASS"]:
    print("Permission denied (password,keyboard-interactive).", file=sys.stderr); sys.exit(255)
cmd = args[-1]
pre = "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand "
if not cmd.startswith(pre) or len(cmd) > 1500 or "ReadToEnd" not in base64.b64decode(cmd[len(pre):]).decode("utf-16-le"):
    print("not the bootstrap", file=sys.stderr); sys.exit(1)
answers = json.load(open(os.environ["FAKE_ANSWERS"]))
for key, ans in answers.items():
    if script.startswith("# nkt:" + key):
        sys.stdout.write("\ufeff" + json.dumps(ans, ensure_ascii=False)); sys.exit(0)
if script.startswith("# nkt:config"):
    sys.stdout.write("### Firewall: inbound allow\r\nRemote Desktop [Any]\r\n### Services: start type\r\nSpooler: Automatic\r\n"); sys.exit(0)
if script.startswith("# nkt:updates"):
    print("started"); sys.exit(0)
sys.stdout.write("ran: " + script.strip())
'''


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        sshp, ksp = os.path.join(cls.tmp, "ssh"), os.path.join(cls.tmp, "ssh-keyscan")
        with open(sshp, "w") as f:
            f.write(FAKE_SSH)
        with open(ksp, "w") as f:
            f.write("#!/bin/sh\necho \"$5 ssh-ed25519 " + HOSTKEY + "\"\n")
        for p in (sshp, ksp):
            os.chmod(p, stat.S_IRWXU)
        cls.log = os.path.join(cls.tmp, "ssh.log")
        with open(os.path.join(cls.tmp, "answers.json"), "w") as f:
            json.dump(ANSWERS, f, ensure_ascii=False)
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1", WINDOWS_SSH=sshp, WINDOWS_SSH_KEYSCAN=ksp,
                   FAKE_LOG=cls.log, FAKE_PASS=PASS, FAKE_ANSWERS=os.path.join(cls.tmp, "answers.json"))
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
        shutil.rmtree(cls.tmp, ignore_errors=True)
        assert PASS.encode() not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, dry=False, conn=None, **args):
        args.update(type="windows", conn=conn or {"address": "10.0.0.20", "user": "Administrator", "password": PASS})
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def logs(self):
        with open(self.log) as f:
            return [json.loads(l) for l in f]

    def test_1_manifest(self):
        m = self.req("/nkt/manifest")
        t = m["host_types"][0]
        self.assertEqual((t["id"], t["exec"], m["provides"]), ("windows", "powershell", ["hosts"]))

    def test_2_check(self):
        r = self.call("_host_check")["result"]
        self.assertEqual((r["name"], r["os"], r["uptime_s"]), ("WIN-SRV01", "Microsoft Windows Server 2022 Standard", 7200))
        self.assertTrue(r["fingerprint"].startswith("SHA256:"))
        self.assertIn("host key changed", self.call("_host_check", conn={"address": "10.0.0.20", "password": PASS, "fingerprint": "SHA256:other"})["error"])
        self.assertIn("SSH refused", self.call("_host_check", conn={"address": "10.0.0.20", "password": "nope"})["error"])
        self.assertFalse(any(PASS in " ".join(x["argv"]) or PASS in x["script"] for x in self.logs()))

    def test_3_snapshot_metrics(self):
        s = self.call("_host_snapshot")["result"]
        self.assertEqual(s["interfaces"], [{"name": "Ethernet", "type": "Red Hat VirtIO", "mac": "52-54-00-12-34-56", "up": True, "addrs": ["10.0.0.20"]}])
        self.assertEqual(s["users"], [{"name": "Administrator", "group": ""}])   # один объект → список
        self.assertEqual(s["services"][0]["detail"], "Диспетчер печати")           # UTF-8
        self.assertEqual([x["title"] for x in s["extra"]], ["Administrators", "Updates (KB)", "Roles and features"])
        m = self.call("_host_metrics")["result"]
        self.assertEqual((m["cpu_pct"], m["net"]["Ethernet"]["tx"]), (17.0, 654321))

    def test_4_findings_config(self):
        ids = {f["id"]: f["severity"] for f in self.call("_host_findings")["result"]["findings"]}
        self.assertEqual(ids, {"updates": "warning", "reboot": "warning", "rtp": "warning", "signatures": "warning", "firewall-public": "warning",
                               "rdp-nla": "warning", "smb1": "critical", "bitlocker": "info", "event-errors": "info"})
        self.assertEqual(self.call("_host_config")["result"]["text"], "### Firewall: inbound allow\nRemote Desktop [Any]\n### Services: start type\nSpooler: Automatic\n")

    def test_5_actions_exec(self):
        self.assertIn("(dry run)", self.call("_host_action", dry=True, action="reboot")["text"])
        self.assertTrue(self.call("_host_action", action="service", args={"name": "Spooler", "action": "restart"})["ok"])
        self.assertIn("Restart-Service -Force -Name 'Spooler'", self.logs()[-1]["script"])
        self.assertFalse(self.call("_host_action", action="service", args={"name": "x'; Remove-Item C:\\", "action": "restart"})["ok"])
        self.assertIn("NktWindowsUpdate", self.call("_host_action", action="updates")["text"])
        self.assertIn("--id 'Mozilla.Firefox'", self.call("_host_action", dry=True, action="winget", args={"id": "Mozilla.Firefox"})["text"])
        self.assertEqual(self.call("_host_exec", command="Get-Date", check=True)["text"], "checked")
        self.assertIn("(dry run)", self.call("_host_exec", dry=True, command="Get-Date")["text"])
        self.assertEqual(self.call("_host_exec", command="Get-Process | Select -First 1")["text"], "ran: Get-Process | Select -First 1")
        self.assertFalse(self.call("_host_exec", command="  ")["ok"])

    def test_6_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
