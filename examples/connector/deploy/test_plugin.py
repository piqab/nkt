"""Проверка плагина connector без устройств: поддельные ssh и ssh-keyscan
(Linux, проверка ключа хоста, пароль не в аргументах), поддельный Keenetic
(вход по вызову X-NDM-Challenge) и поддельный SNMP-агент (v2c, get и walk).
Плагин запускается отдельным процессом, вызовы — подписанные, как от хаба.
Запуск: python3 -I test_plugin.py (его вызывает go test ./examples)."""
import base64
import hashlib
import hmac
import json
import os
import runpy
import shutil
import socket
import stat
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
PLUGIN = os.path.join(HERE, "nkt-plugin.py")
SECRET = "test-plugin-secret-0123456789"
SSH_PASS = "ssh-pass-777"
HOSTKEY = base64.b64encode(b"\x00\x00\x00\x0bssh-ed25519" + b"\x00\x00\x00\x20" + bytes(range(32))).decode()
# Ненастоящий ключ: только заголовок openssh-key-v1 без тела. Метка PEM
# собирается из частей, чтобы gitleaks не принимал строку за закрытый ключ.
PEM_TAG = "OPENSSH " + "PRIVATE KEY"
KEY_PEM = f"-----BEGIN {PEM_TAG}-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\nAAAAAAAAAAAAAAAA\n-----END {PEM_TAG}-----\n"

FAKE_SSH = r'''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
log = os.environ["FAKE_SSH_LOG"]
with open(log, "a") as f:
    f.write(json.dumps({"argv": args, "askpass": bool(os.environ.get("SSH_ASKPASS"))}) + "\n")
if "-i" in args:
    key = open(args[args.index("-i") + 1]).read()
    if "BEGIN OPENSSH PRIVATE KEY" not in key or "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU=AAAAAAAAAAAAAAAA" not in key.replace("\n", ""):
        print("Permission denied (publickey).", file=sys.stderr); sys.exit(255)
elif os.environ.get("NKT_SSH_PASS") != os.environ["FAKE_SSH_PASS"]:
    print("Permission denied (password).", file=sys.stderr); sys.exit(255)
cmd = args[-1]
for needle, out in json.load(open(os.environ["FAKE_SSH_MAP"])):
    if needle in cmd:
        sys.stdout.write(out); sys.exit(0)
sys.exit(0)
'''

FAKE_KEYSCAN = "#!/bin/sh\necho \"$6 ssh-ed25519 " + HOSTKEY + "\"\n"

OWRT_BOARD = {"hostname": "gw-home", "model": "GL.iNet GL-MT3000", "release": {"distribution": "OpenWrt", "version": "22.03.5", "target": "mediatek/filogic"}}
OWRT_UCI = ("package wireless\n\nconfig wifi-iface 'default_radio0'\n\toption ssid 'Home'\n\toption encryption 'psk2'\n\toption key 'SuperSecret123'\n\n"
            "package network\n\nconfig interface 'wg0'\n\toption proto 'wireguard'\n\toption private_key 'WGPRIVKEY+abc='\n\tlist addresses '10.9.0.1/24'\n")
SSH_MAP = [
    # OpenWrt — раньше общих: побеждает первая совпавшая подстрока.
    ["ubus call system board", json.dumps(OWRT_BOARD)],
    ["jsonfilter", "67108864\n"],
    ["ubus call system info", json.dumps({"uptime": 3600, "memory": {"total": 134217728, "available": 67108864}})],
    ["ubus call network.interface dump", json.dumps({"interface": [{"interface": "lan", "up": True, "proto": "static", "l3_device": "br-lan", "ipv4-address": [{"address": "192.168.8.1"}]}]})],
    ["st=on", "default_radio0|Home|ap|psk2|radio0|lan|on\nguest|Guest|ap|none|radio1|guest|on\nold|Old|ap|psk-mixed+tkip|radio0|lan|on\n"],
    ["echo \"open|$n\"", "open|Guest\nweak|Old\ntkip|Old\nwps|Home\n"],
    ["assoclist", "phy0-ap0 AA:BB:CC:DD:EE:01 -52\nphy0-ap0 AA:BB:CC:DD:EE:02 -71\n"],
    ["=host$", "nas|aa:bb:cc:00:00:01|192.168.8.10\n"],
    ["$(g masq)", "lan|ACCEPT|ACCEPT||lan\nwan|ACCEPT|REJECT|1|wan wan6\n"],
    ["firewall.$s.input", "ACCEPT\n"],
    ["=redirect$", "NAS-HTTPS|tcp|8443|192.168.8.10|443|1\nOld-game|udp|27015|192.168.8.20|27015|0\n"],
    ["=rule$", "Allow-SSH-WAN|22\n"],
    ["list-upgradable", "luci-base 23.051\nbusybox 1.36.1-r2\ndnsmasq 2.89-r6\n"],
    ["list-installed", "busybox 1.35.0-r17\ndnsmasq 2.86-r1\n"],
    ["/etc/shadow", "set\n"],
    ["PasswordAuth", "on\n"],
    ["df -P /overlay", "95\n"],
    ["DISTRIB_RELEASE", "22.03.5\n"],
    ["thermal_zone0", "52000\n"],
    ["uci export", OWRT_UCI],
    ["/tmp/dhcp.leases", "1700000000 aa:bb:cc:00:00:02 192.168.8.100 phone *\n"],
    ["netstat -lnt", "tcp 0.0.0.0 22\n"],
    ["/proc/net/arp", "192.168.8.100 aa:bb:cc:00:00:02 br-lan\n"],
    ["nkt-upgrade.log", "started: /tmp/nkt-upgrade.log\n"],
    ["### $f", "### /etc/hostname\nweb-old\n"],
    ["hostname", "web-old\n"],
    ["/etc/os-release", 'NAME="CentOS Linux"\nVERSION_ID="7"\n'],
    ["uname -m", "x86_64\n"],
    ["/proc/uptime", "86400.5 100.0\n"],
    ["ip -j addr", json.dumps([{"ifname": "eth0", "address": "52:54:00:aa:bb:cc", "operstate": "UP", "link_type": "ether",
                                 "addr_info": [{"local": "10.0.0.5", "prefixlen": 24}]}])],
    ["ss -Hltnu", "tcp   LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\ntcp   LISTEN 0 128 127.0.0.1:5432 0.0.0.0:*\n"],
    ["dpkg-query", "openssh-server 7.4p1\nbash 4.2\n"],
    ["top -bn1", "%Cpu(s):  3.0 us,  1.0 sy,  0.0 ni, 95.5 id\n"],
    ["MemAvailable", "1073741824\n"],
    ["apt list --upgradable", "12\n"],
    ["reboot-required", "yes\n"],
    ["sshd -T", "yes\n"],
    ["df -P / | awk", "42\n"],
    ["/proc/net/dev", "Inter-|   Receive\n face |bytes\n  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n"],
    ["systemctl restart", "restarted\n"],
    ["echo hi", "hi\n"],
]


class FakeKeenetic:
    def __init__(self):
        self.realm, self.challenge = "Keenetic Giga", "abc123"
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, obj=None, headers=None):
                raw = json.dumps(obj).encode() if obj is not None else b""
                self.send_response(code)
                for k, v in (headers or {}).items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def authed(self):
                return "session=good" in (self.headers.get("Cookie") or "")

            def do_GET(self):
                if self.path == "/auth":
                    if self.authed():
                        return self.send(200, {})
                    return self.send(401, {}, {"X-NDM-Realm": fake.realm, "X-NDM-Challenge": fake.challenge, "Set-Cookie": "session=pending; Path=/"})
                if not self.authed():
                    return self.send(401, {})
                data = {"/rci/show/system": {"hostname": "Giga-Home", "cpuload": 95, "memory": "100000/262144", "uptime": "3600"},
                        "/rci/show/version": {"release": "4.1.1", "model": "Keenetic Giga"},
                        "/rci/show/interface": {"GigabitEthernet1": {"id": "GigabitEthernet1", "type": "GigabitEthernet", "mac": "50:ff:20:00:00:01", "link": "up", "address": "192.168.1.1"}},
                        "/rci/show/ip/hotspot": {"host": [{"name": "laptop", "ip": "192.168.1.20", "mac": "aa:bb:cc:00:00:01", "active": True}]},
                        "/rci/show/running-config": {"message": ["! $$$ Model: Keenetic Giga", "system", "    hostname Giga-Home", "!"]}}
                return self.send(200, data[self.path]) if self.path in data else self.send(404, {})

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                if self.path == "/auth":
                    md5 = hashlib.md5(f"admin:{fake.realm}:router-pw".encode()).hexdigest()
                    ok = body.get("login") == "admin" and body.get("password") == hashlib.sha256((fake.challenge + md5).encode()).hexdigest()
                    return self.send(200 if ok else 401, {}, {"Set-Cookie": "session=good; Path=/"} if ok else {})
                if not self.authed():
                    return self.send(401, {})
                if self.path == "/rci/":
                    return self.send(200, [{"parse": {"echo": body[0]["parse"]}}])
                return self.send(404, {})

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


class FakeSNMP:
    """SNMP v2c агент на функциях BER самого плагина."""

    def __init__(self, community):
        mod = runpy.run_path(PLUGIN)
        self.m = mod
        ber, ber_int, ber_oid = mod["ber"], mod["ber_int"], mod["ber_oid"]
        s = lambda v: (0x04, v.encode())
        i = lambda v: (0x02, ber_int(v)[2:])
        self.data = {
            "1.3.6.1.2.1.1.1.0": s("HP LaserJet M404"), "1.3.6.1.2.1.1.5.0": s("printer-1"), "1.3.6.1.2.1.1.3.0": (0x43, (360000).to_bytes(4, "big")),
            "1.3.6.1.2.1.25.3.2.1.3.1": s("HP LaserJet Pro M404dn"), "1.3.6.1.2.1.43.5.1.1.17.1": s("PHB1234"),
            "1.3.6.1.2.1.43.11.1.1.6.1.1": s("Black Cartridge"), "1.3.6.1.2.1.43.11.1.1.9.1.1": i(5), "1.3.6.1.2.1.43.11.1.1.8.1.1": i(100),
            "1.3.6.1.2.1.43.11.1.1.6.1.2": s("Drum"), "1.3.6.1.2.1.43.11.1.1.9.1.2": i(80), "1.3.6.1.2.1.43.11.1.1.8.1.2": i(100),
            "1.3.6.1.2.1.43.10.2.1.4.1.1": (0x41, (12345).to_bytes(4, "big")),
            # ИБП: UPS-MIB и APC PowerNet.
            "1.3.6.1.2.1.33.1.1.1.0": s("APC"), "1.3.6.1.2.1.33.1.1.2.0": s("Smart-UPS 1500"), "1.3.6.1.2.1.33.1.1.3.0": s("UPS 09.3"),
            "1.3.6.1.2.1.33.1.2.1.0": i(2), "1.3.6.1.2.1.33.1.2.3.0": i(45), "1.3.6.1.2.1.33.1.2.4.0": i(100), "1.3.6.1.2.1.33.1.2.7.0": i(45),
            "1.3.6.1.2.1.33.1.3.3.1.3.1": i(230), "1.3.6.1.2.1.33.1.4.1.0": i(3), "1.3.6.1.2.1.33.1.4.4.1.5.1": i(95),
            "1.3.6.1.2.1.33.1.6.1.0": i(0), "1.3.6.1.2.1.33.1.7.3.0": i(3),
            "1.3.6.1.4.1.318.1.1.1.2.2.4.0": i(2), "1.3.6.1.4.1.318.1.1.1.1.2.3.0": s("AS1234"),
        }
        self.keys = sorted(self.data, key=lambda o: [int(x) for x in o.split(".")])
        self.community = community
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.bind(("127.0.0.1", 0))
        self.port = self.sock.getsockname()[1]
        self.ber, self.ber_oid, self.ber_int = ber, ber_oid, ber_int
        threading.Thread(target=self.loop, daemon=True).start()

    def loop(self):
        m = self.m
        while True:
            try:
                data, peer = self.sock.recvfrom(65535)
            except OSError:
                return
            _, msg, _ = m["ber_read"](data, 0)
            i = 0
            _, _, i = m["ber_read"](msg, i)
            _, comm, i = m["ber_read"](msg, i)
            if comm.decode() != self.community:
                continue  # чужой community — молчать, как настоящий агент
            ptag = msg[i]
            rid, _, vbs = m["snmp_parse"](data)
            oid = vbs[0][0]
            if ptag == 0xA0:
                tag, val = self.data.get(oid, (0x80, b""))
                ro = oid
            else:
                key = [int(x) for x in oid.split(".")]
                nxt = next((k for k in self.keys if [int(x) for x in k.split(".")] > key), None)
                ro, (tag, val) = (nxt, self.data[nxt]) if nxt else (oid, (0x82, b""))
            vb = self.ber(0x30, self.ber(0x30, self.ber_oid(ro) + self.ber(tag, val)))
            pdu = self.ber(0xA2, self.ber_int(rid) + self.ber_int(0) + self.ber_int(0) + vb)
            self.sock.sendto(self.ber(0x30, self.ber_int(1) + self.ber(0x04, comm) + pdu), peer)


class FakeNUT:
    """upsd: LIST VAR, USERNAME/PASSWORD, INSTCMD."""

    VARS = {"ups.status": "OB DISCHRG", "battery.charge": "25", "battery.runtime": "240", "ups.model": "Back-UPS ES 700", "device.mfr": "APC",
            "device.serial": "4B1234", "ups.firmware": "871.O4", "driver.name": "usbhid-ups", "ups.test.result": "Done and passed"}

    def __init__(self):
        self.sock = socket.socket()
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(5)
        self.port = self.sock.getsockname()[1]
        self.cmds = []
        threading.Thread(target=self.loop, daemon=True).start()

    def loop(self):
        while True:
            try:
                conn, _ = self.sock.accept()
            except OSError:
                return
            threading.Thread(target=self.serve, args=(conn,), daemon=True).start()

    def serve(self, conn):
        f = conn.makefile("rwb")
        user = pw = ""
        for raw in f:
            line = raw.decode().strip()
            if line == "LOGOUT":
                break
            if line.startswith("LIST VAR "):
                if line[9:] != "myups":
                    f.write(b"ERR UNKNOWN-UPS\n")
                else:
                    f.write(b"BEGIN LIST VAR myups\n" + b"".join(f'VAR myups {k} "{v}"\n'.encode() for k, v in self.VARS.items()) + b"END LIST VAR myups\n")
            elif line.startswith("USERNAME "):
                user = line[9:].strip('"')
                f.write(b"OK\n")
            elif line.startswith("PASSWORD "):
                pw = line[9:].strip('"')
                f.write(b"OK\n")
            elif line.startswith("INSTCMD "):
                ok = (user, pw) == ("monuser", "nut-pw")
                self.cmds.append(line)
                f.write(b"OK\n" if ok else b"ERR ACCESS-DENIED\n")
            else:
                f.write(b"ERR UNKNOWN-COMMAND\n")
            f.flush()
        conn.close()


class FakeCamera:
    """ONVIF (WS-Security PasswordDigest) и ISAPI Hikvision (HTTP digest)."""

    PASSWORD = "cam-pass-555"

    def __init__(self):
        cam = self
        self.calls = []

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def send(self, code, body, ctype="application/soap+xml", headers=None):
                data = body.encode()
                self.send_response(code)
                self.send_header("Content-Type", ctype)
                for k, v in (headers or {}).items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def env(self, body):
                return ('<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" '
                        'xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>' + body + '</s:Body></s:Envelope>')

            def do_POST(self):
                import re as _re, xml.etree.ElementTree as _ET
                raw = self.rfile.read(int(self.headers.get("Content-Length", 0))).decode()
                tag = lambda n: _re.search(r"<" + n + r"[^>]*>([^<]*)<", raw)
                nonce, created, digest = tag("Nonce"), tag("Created"), tag("Password")
                want = base64.b64encode(hashlib.sha1(base64.b64decode(nonce.group(1)) + created.group(1).encode() + cam.PASSWORD.encode()).digest()).decode() if nonce else ""
                if not digest or digest.group(1) != want or cam.PASSWORD in raw:
                    return self.send(400, self.env('<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>ter:NotAuthorized</s:Value></s:Subcode></s:Code></s:Fault>'))
                body = _ET.fromstring(raw).find("{http://www.w3.org/2003/05/soap-envelope}Body")[0]
                op = body.tag.split("}")[-1]
                cam.calls.append(op)
                port = self.server.server_address[1]
                now = time.gmtime(time.time() - 300)
                answers = {
                    "GetDeviceInformation": "<tds:GetDeviceInformationResponse><tds:Manufacturer>HIKVISION</tds:Manufacturer><tds:Model>DS-2CD2143G2-I</tds:Model>"
                                            "<tds:FirmwareVersion>V5.7.3 build 220112</tds:FirmwareVersion><tds:SerialNumber>DS-2CD2143G2-I20230101AAWRJ123</tds:SerialNumber>"
                                            "<tds:HardwareId>88</tds:HardwareId></tds:GetDeviceInformationResponse>",
                    "GetSystemDateAndTime": "<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime><tt:DateTimeType>Manual</tt:DateTimeType><tt:TimeZone><tt:TZ>UTC-3</tt:TZ></tt:TimeZone>"
                                            f"<tt:UTCDateTime><tt:Time><tt:Hour>{now.tm_hour}</tt:Hour><tt:Minute>{now.tm_min}</tt:Minute><tt:Second>{now.tm_sec}</tt:Second></tt:Time>"
                                            f"<tt:Date><tt:Year>{now.tm_year}</tt:Year><tt:Month>{now.tm_mon}</tt:Month><tt:Day>{now.tm_mday}</tt:Day></tt:Date></tt:UTCDateTime>"
                                            "</tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>",
                    "GetCapabilities": f"<tds:GetCapabilitiesResponse><tds:Capabilities><tt:Media><tt:XAddr>http://127.0.0.1:{port}/onvif/Media</tt:XAddr></tt:Media></tds:Capabilities></tds:GetCapabilitiesResponse>",
                    "GetProfiles": '<trt:GetProfilesResponse><trt:Profiles token="Profile_1"><tt:Name>mainStream</tt:Name><tt:VideoEncoderConfiguration><tt:Encoding>H264</tt:Encoding>'
                                   '<tt:Resolution><tt:Width>2560</tt:Width><tt:Height>1440</tt:Height></tt:Resolution></tt:VideoEncoderConfiguration></trt:Profiles>'
                                   '<trt:Profiles token="Profile_2"><tt:Name>subStream</tt:Name><tt:VideoEncoderConfiguration><tt:Encoding>H265</tt:Encoding>'
                                   '<tt:Resolution><tt:Width>640</tt:Width><tt:Height>360</tt:Height></tt:Resolution></tt:VideoEncoderConfiguration></trt:Profiles></trt:GetProfilesResponse>',
                    "GetStreamUri": "<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>rtsp://127.0.0.1:554/Streaming/Channels/" + ("101" if "Profile_1" in raw else "102") +
                                    "</tt:Uri></trt:MediaUri></trt:GetStreamUriResponse>",
                    "GetNTP": "<tds:GetNTPResponse><tds:NTPInformation><tt:FromDHCP>false</tt:FromDHCP></tds:NTPInformation></tds:GetNTPResponse>",
                    "GetHostname": "<tds:GetHostnameResponse><tds:HostnameInformation><tt:Name>cam-door</tt:Name></tds:HostnameInformation></tds:GetHostnameResponse>",
                    "SystemReboot": "<tds:SystemRebootResponse><tds:Message>Rebooting in 30 seconds</tds:Message></tds:SystemRebootResponse>",
                }
                if op not in answers:
                    return self.send(500, self.env('<s:Fault><s:Code><s:Value>s:Receiver</s:Value></s:Code><s:Reason><s:Text>ActionNotSupported</s:Text></s:Reason></s:Fault>'))
                self.send(200, self.env(answers[op]))

            def do_GET(self):
                if not self.path.startswith("/ISAPI/ContentMgmt/Storage"):
                    return self.send(404, "not found", "text/plain")
                auth = self.headers.get("Authorization", "")
                if not auth.startswith("Digest "):
                    return self.send(401, "", "text/plain", {"WWW-Authenticate": 'Digest realm="IP Camera", nonce="n0nce42", qop="auth", algorithm="MD5"'})
                import re as _re
                p = {k: a or b for k, a, b in _re.findall(r'(\w+)=(?:"([^"]*)"|([^,\s]*))', auth[7:])}
                md5 = lambda x: hashlib.md5(x.encode()).hexdigest()
                ha1, ha2 = md5(f"admin:IP Camera:{cam.PASSWORD}"), md5(f"GET:{p.get('uri')}")
                if p.get("response") != md5(f"{ha1}:n0nce42:{p.get('nc')}:{p.get('cnonce')}:auth:{ha2}"):
                    return self.send(401, "", "text/plain", {"WWW-Authenticate": 'Digest realm="IP Camera", nonce="n0nce42", qop="auth"'})
                self.send(200, '<?xml version="1.0" encoding="UTF-8"?><storage xmlns="http://www.hikvision.com/ver20/XMLSchema"><hddList>'
                               '<hdd><id>1</id><hddName>hdd1</hddName><status>ok</status><capacity>3815447</capacity><freeSpace>12000</freeSpace></hdd>'
                               '<hdd><id>2</id><hddName>hdd2</hddName><status>error</status><capacity>3815447</capacity><freeSpace>0</freeSpace></hdd>'
                               '</hddList></storage>', "application/xml")

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp()
        sshp, ksp = os.path.join(cls.tmp, "ssh"), os.path.join(cls.tmp, "ssh-keyscan")
        for path, text in ((sshp, FAKE_SSH), (ksp, FAKE_KEYSCAN)):
            with open(path, "w") as f:
                f.write(text)
            os.chmod(path, stat.S_IRWXU)
        cls.ssh_log = os.path.join(cls.tmp, "ssh.log")
        with open(os.path.join(cls.tmp, "map.json"), "w") as f:
            json.dump(SSH_MAP, f)
        cls.keenetic = FakeKeenetic()
        cls.snmp = FakeSNMP("s3cret-ro")
        cls.nut = FakeNUT()
        cls.cam = FakeCamera()
        cls.port = free_port()
        env = dict(os.environ, NKT_PLUGIN_SECRET=SECRET, NKT_PLUGIN_PORT=str(cls.port), NKT_PLUGIN_LISTEN="127.0.0.1",
                   CONNECTOR_SSH=sshp, CONNECTOR_SSH_KEYSCAN=ksp, CONNECTOR_STATE=os.path.join(cls.tmp, "state"),
                   FAKE_SSH_LOG=cls.ssh_log, FAKE_SSH_MAP=os.path.join(cls.tmp, "map.json"), FAKE_SSH_PASS=SSH_PASS)
        cls.proc = subprocess.Popen([sys.executable, "-I", PLUGIN], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
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
        cls.keenetic.srv.shutdown()
        cls.cam.srv.shutdown()
        cls.nut.sock.close()
        cls.snmp.sock.close()
        shutil.rmtree(cls.tmp, ignore_errors=True)
        assert SSH_PASS.encode() not in out and b"router-pw" not in out and FakeCamera.PASSWORD.encode() not in out and b"nut-pw" not in out, out

    def req(self, path, body=b""):
        ts = str(int(time.time()))
        sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}{path}", data=body or None,
                                   headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig, "X-NKT-Lang": "en"})
        with urllib.request.urlopen(r, timeout=30) as resp:
            return json.load(resp)

    def call(self, op, typ=None, conn=None, dry=False, **args):
        if typ:
            args.update(type=typ, conn=conn or {})
        return self.req("/nkt/call", json.dumps({"op": op, "args": args, "dry_run": dry, "user": "test"}).encode())

    def linux(self, **kw):
        c = {"address": "10.0.0.5", "user": "admin", "password": SSH_PASS}
        c.update(kw)
        return c

    def test_1_manifest(self):
        m = self.req("/nkt/manifest")
        types = {t["id"]: t for t in m["host_types"]}
        self.assertEqual(sorted(types), ["freebsd", "keenetic", "linux-ssh", "macos", "nut-ups", "onvif-camera", "openwrt", "snmp-printer", "snmp-switch", "snmp-ups"])
        self.assertEqual(types["linux-ssh"]["exec"], "sh")
        self.assertEqual(types["keenetic"]["exec"], "keenetic-cli")
        self.assertNotIn("exec", types["snmp-printer"])
        self.assertNotIn("metrics", types["snmp-printer"]["sections"])

    def test_2_ssh_check_and_pin(self):
        r = self.call("_host_check", "linux-ssh", self.linux())
        self.assertTrue(r["ok"], r)
        fp = r["result"]["fingerprint"]
        self.assertTrue(fp.startswith("SHA256:"))
        self.assertEqual((r["result"]["name"], r["result"]["os"], r["result"]["version"]), ("web-old", "CentOS Linux", "7"))
        self.assertIn("key or certificate changed", self.call("_host_check", "linux-ssh", self.linux(fingerprint="SHA256:other"))["error"])
        self.assertTrue(self.call("_host_check", "linux-ssh", self.linux(fingerprint=fp))["ok"])
        self.assertIn("SSH refused", self.call("_host_check", "linux-ssh", self.linux(password="nope"))["error"])
        # Ключ из однострочного поля (браузер склеил строки пробелами).
        one = KEY_PEM.replace("\n", " ")
        self.assertTrue(self.call("_host_check", "linux-ssh", {"address": "10.0.0.5", "key": one})["ok"])
        with open(self.ssh_log) as f:
            logs = [json.loads(l) for l in f]
        self.assertFalse(any(SSH_PASS in " ".join(x["argv"]) for x in logs))  # пароль — не в аргументах
        # Имя — отдельным -l, адрес — после «--»: ни то, ни другое не станет параметром ssh.
        self.assertEqual(logs[0]["argv"][-5:-1], ["-l", "admin", "--", "10.0.0.5"])
        self.assertIn("bad user", self.call("_host_check", "linux-ssh", self.linux(user="-oProxyCommand=touch /tmp/x"))["error"])
        self.assertIn("bad address", self.call("_host_check", "linux-ssh", self.linux(address="-oProxyCommand"))["error"])
        self.assertIn("bad address", self.call("_host_check", "linux-ssh", self.linux(address="10.0.0.5:ssh"))["error"])
        self.assertTrue(any(x["askpass"] for x in logs))

    def test_3_linux_snapshot_metrics_findings(self):
        s = self.call("_host_snapshot", "linux-ssh", self.linux())["result"]
        self.assertEqual(s["interfaces"][0], {"name": "eth0", "type": "ether", "mac": "52:54:00:aa:bb:cc", "addrs": ["10.0.0.5"], "up": True, "comment": ""})
        self.assertEqual([(p["port"], p["allowed"]) for p in s["ports"]], [(22, ""), (5432, "127.0.0.1")])
        self.assertEqual(s["packages"][0], {"name": "openssh-server", "version": "7.4p1"})
        m = self.call("_host_metrics", "linux-ssh", self.linux())["result"]
        self.assertEqual((m["cpu_pct"], m["net"]["eth0"]), (4.5, {"rx": 1000, "tx": 2000}))
        ids = {f["id"]: f for f in self.call("_host_findings", "linux-ssh", self.linux())["result"]["findings"]}
        self.assertEqual(ids["updates"]["title"], "Updates pending: 12")
        self.assertIn("reboot-required", ids)
        self.assertIn("ssh-root", ids)
        self.assertNotIn("disk-root", ids)  # 42% — не больше 90

    def test_4_linux_config_actions_exec(self):
        self.assertIn("### /etc/hostname", self.call("_host_config", "linux-ssh", self.linux())["result"]["text"])
        self.assertEqual(self.call("_host_exec", "linux-ssh", self.linux(), command="echo hi", check=True)["text"], "checked")
        self.assertIn("sh parse error", self.call("_host_exec", "linux-ssh", self.linux(), command="if then fi (", check=True)["error"])
        self.assertIn("(dry run)", self.call("_host_exec", "linux-ssh", self.linux(), dry=True, command="echo hi")["text"])
        self.assertEqual(self.call("_host_exec", "linux-ssh", self.linux(), command="echo hi")["text"], "hi\n")
        self.assertEqual(self.call("_host_action", "linux-ssh", self.linux(), action="service_restart", args={"name": "nginx"})["text"], "restarted")
        with open(self.ssh_log) as f:
            last = [json.loads(l) for l in f][-1]["argv"][-1]
        self.assertIn("systemctl restart nginx", last)
        self.assertFalse(self.call("_host_action", "linux-ssh", self.linux(), action="service_restart", args={"name": "x; rm -rf /"})["ok"])

    def test_5_keenetic(self):
        conn = {"address": f"127.0.0.1:{self.keenetic.srv.server_address[1]}", "user": "admin", "password": "router-pw", "tls": "http"}
        r = self.call("_host_check", "keenetic", conn)
        self.assertEqual((r["result"]["name"], r["result"]["model"], r["result"]["version"]), ("Giga-Home", "Keenetic Giga", "4.1.1"))
        self.assertIn("wrong login or password", self.call("_host_check", "keenetic", dict(conn, password="x"))["error"])
        m = self.call("_host_metrics", "keenetic", conn)["result"]
        self.assertEqual((m["cpu_pct"], m["mem_used"], m["mem_total"], m["uptime_s"]), (95.0, 102400000, 268435456, 3600))
        ids = [f["id"] for f in self.call("_host_findings", "keenetic", conn)["result"]["findings"]]
        self.assertEqual(ids, ["cpu", "http"])
        s = self.call("_host_snapshot", "keenetic", conn)["result"]
        self.assertEqual(s["interfaces"][0]["addrs"], ["192.168.1.1"])
        self.assertEqual(s["extra"][0]["rows"][0]["cells"]["name"], "laptop")
        self.assertIn("hostname Giga-Home", self.call("_host_config", "keenetic", conn)["result"]["text"])
        self.assertIn("show version", self.call("_host_exec", "keenetic", conn, command="show version")["text"])

    def test_6_snmp_printer(self):
        conn = {"address": f"127.0.0.1:{self.snmp.port}", "password": "s3cret-ro"}
        r = self.call("_host_check", "snmp-printer", conn)
        self.assertEqual((r["result"]["name"], r["result"]["serial"], r["result"]["uptime_s"]), ("printer-1", "PHB1234", 3600))
        f = self.call("_host_findings", "snmp-printer", conn)["result"]["findings"]
        self.assertEqual([x["title"] for x in f], ["Black Cartridge: 5% left"])
        s = self.call("_host_snapshot", "snmp-printer", conn)["result"]
        self.assertEqual([r["cells"]["pct"] for r in s["extra"][0]["rows"]], ["5", "80"])
        self.assertEqual(s["extra"][1]["rows"][0]["cells"]["count"], "12345")
        bad = self.call("_host_check", "snmp-printer", dict(conn, password="public"))
        self.assertIn("SNMP doesn't answer", bad["error"])

    def test_7_profiles(self):
        v = self.call("profiles")["view"]
        self.assertEqual(len(v["rows"]), 10)
        self.assertTrue(self.call("profile_new", id="my-nas", base="freebsd")["ok"])
        text = self.call("profile_text", id="my-nas")["result"]["text"]
        p = json.loads(text)
        p["title"] = "My NAS"
        self.assertTrue(self.call("profile_save", id="my-nas", text=json.dumps(p))["ok"])
        self.assertFalse(self.call("profile_save", id="my-nas", text="{bad json")["ok"])
        self.assertFalse(self.call("profile_save", id="my-nas", text=json.dumps(dict(p, transport="telnet")))["ok"])
        types = {t["id"]: t["title"] for t in self.req("/nkt/manifest")["host_types"]}
        self.assertEqual(types["my-nas"], "My NAS")
        # Свой с именем встроенного — вместо него.
        q = json.loads(self.call("profile_text", id="openwrt")["result"]["text"])
        q["title"] = "OpenWrt (mine)"
        self.call("profile_save", id="openwrt", text=json.dumps(q))
        rows = {r["cells"]["id"]: r["cells"]["source"] for r in self.call("profiles")["view"]["rows"]}
        self.assertEqual((rows["openwrt"], rows["my-nas"], rows["macos"]), ("custom (instead of built-in)", "custom", "built-in"))
        self.assertTrue(self.call("profile_delete", id="my-nas")["ok"])
        self.assertFalse(self.call("profile_delete", id="macos")["ok"])

    def test_9_openwrt(self):
        conn = self.linux(user="root")
        r = self.call("_host_check", "openwrt", conn)["result"]
        self.assertEqual((r["name"], r["model"], r["version"]), ("gw-home", "GL.iNet GL-MT3000", "22.03.5"))
        s = self.call("_host_snapshot", "openwrt", conn)["result"]
        titles = [x["title"] for x in s["extra"]]
        self.assertEqual(titles, ["Wi-Fi networks", "Wi-Fi clients", "DHCP leases", "DHCP static leases", "Firewall zones", "Port forwards", "Packages with updates"])
        self.assertEqual(s["extra"][0]["rows"][1]["cells"]["encryption"], "none")
        self.assertEqual(s["packages"], [{"name": "busybox", "version": "1.35.0-r17"}, {"name": "dnsmasq", "version": "2.86-r1"}])
        self.assertEqual(self.call("_host_metrics", "openwrt", conn)["result"]["temp_c"], 52.0)
        ids = {f["id"]: f["severity"] for f in self.call("_host_findings", "openwrt", conn)["result"]["findings"]}
        self.assertEqual(ids, {"wan-input": "critical", "wan-admin-Allow-SSH-WAN": "warning", "wifi-open-Guest": "critical", "wifi-weak-Old": "warning",
                               "wifi-tkip-Old": "warning", "wps-Home": "warning", "ssh-password": "info", "forwards": "info", "upgradable": "info",
                               "overlay": "warning", "old-release": "warning"})
        text = self.call("_host_config", "openwrt", conn)["result"]["text"]
        self.assertIn("option ssid 'Home'", text)
        self.assertIn("option key '***'", text)
        self.assertIn("option private_key '***'", text)
        self.assertNotIn("SuperSecret123", text)
        self.assertNotIn("WGPRIVKEY", text)
        self.assertIn("started", self.call("_host_action", "openwrt", conn, action="pkg_upgrade", args={"name": "luci-base"})["text"])
        self.assertFalse(self.call("_host_action", "openwrt", conn, action="pkg_upgrade", args={"name": "x; reboot"})["ok"])

    def test_10_ups(self):
        conn = {"address": f"127.0.0.1:{self.snmp.port}", "password": "s3cret-ro"}
        r = self.call("_host_check", "snmp-ups", conn)["result"]
        self.assertEqual((r["model"], r["os"], r["serial"]), ("Smart-UPS 1500", "APC", "AS1234"))
        rows = {x["cells"]["name"]: x["cells"]["value"] for x in self.call("_host_snapshot", "snmp-ups", conn)["result"]["extra"][0]["rows"]}
        self.assertEqual((rows["Output source"], rows["Load, %"], rows["Self-test"]), ("normal", "95", "error"))
        self.assertNotIn("Output, V", rows)
        ids = sorted(f["id"] for f in self.call("_host_findings", "snmp-ups", conn)["result"]["findings"])
        self.assertEqual(ids, ["battery-temp", "overload", "replace-battery", "self-test"])
        self.assertEqual(self.call("_host_metrics", "snmp-ups", conn)["result"]["temp_c"], 45.0)
        types = {t["id"]: t for t in self.req("/nkt/manifest")["host_types"]}
        self.assertEqual((types["snmp-ups"]["icon"], types["nut-ups"]["icon"], types["onvif-camera"]["icon"], types["openwrt"]["icon"]), ("ups", "ups", "camera", "openwrt"))
        nut = {"address": f"127.0.0.1:{self.nut.port}", "ups": "myups", "user": "monuser", "password": "nut-pw"}
        r = self.call("_host_check", "nut-ups", nut)["result"]
        self.assertEqual((r["name"], r["model"], r["os"], r["serial"]), ("myups", "Back-UPS ES 700", "APC", "4B1234"))
        ids = sorted(f["id"] for f in self.call("_host_findings", "nut-ups", nut)["result"]["findings"])
        self.assertEqual(ids, ["charge", "on-battery", "runtime"])
        self.assertIn("UNKNOWN-UPS", self.call("_host_check", "nut-ups", dict(nut, ups="other"))["error"])
        self.assertFalse(self.call("_host_check", "nut-ups", dict(nut, ups="x y"))["ok"])
        self.assertTrue(self.call("_host_action", "nut-ups", nut, action="self_test")["ok"])
        self.assertEqual(self.nut.cmds[-1], "INSTCMD myups test.battery.start.quick")
        self.assertIn("wrong login or password", self.call("_host_action", "nut-ups", dict(nut, password="x"), action="self_test")["error"])

    def test_11_camera(self):
        conn = {"address": f"127.0.0.1:{self.cam.srv.server_address[1]}", "user": "admin", "password": FakeCamera.PASSWORD, "tls": "http"}
        r = self.call("_host_check", "onvif-camera", conn)["result"]
        self.assertEqual((r["os"], r["model"], r["version"]), ("HIKVISION", "DS-2CD2143G2-I", "V5.7.3 build 220112"))
        self.assertIn("wrong login or password", self.call("_host_check", "onvif-camera", dict(conn, password="x"))["error"])
        s = self.call("_host_snapshot", "onvif-camera", conn)["result"]
        ex = {x["title"]: x["rows"] for x in s["extra"]}
        self.assertEqual([r["cells"]["uri"] for r in ex["Streams"]], ["rtsp://127.0.0.1:554/Streaming/Channels/101", "rtsp://127.0.0.1:554/Streaming/Channels/102"])
        self.assertEqual(ex["Streams"][0]["cells"]["resolution"], "2560x1440")
        self.assertEqual([r["cells"]["status"] for r in ex["NVR disks (Hikvision)"]], ["ok", "error"])
        self.assertNotIn("NVR disks (Dahua)", ex)
        ids = sorted(f["id"] for f in self.call("_host_findings", "onvif-camera", conn)["result"]["findings"])
        self.assertEqual(ids, ["clock", "hdd-2", "http"])
        cfg = self.call("_host_config", "onvif-camera", conn)["result"]["text"]
        self.assertIn("cam-door", cfg)
        self.assertIn("Rebooting", self.call("_host_action", "onvif-camera", conn, action="reboot")["text"])

    def test_8_bad_signature(self):
        r = urllib.request.Request(f"http://127.0.0.1:{self.port}/nkt/manifest", headers={"X-NKT-Timestamp": "1", "X-NKT-Signature": "x"})
        with self.assertRaises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(r, timeout=5)
        self.assertEqual(e.exception.code, 401)


if __name__ == "__main__":
    unittest.main(verbosity=1)
