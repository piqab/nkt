"""Плагин хаба nkt для Windows (протокол плагинов nkt v1, provides hosts).

Windows-серверы и рабочие станции как хосты nkt — через встроенный
OpenSSH Server (Windows 10/11, Server 2019+) и PowerShell 5.1+, без WinRM и
без агента: «Хосты» → «Добавить устройство» → «Windows».

Сценарии PowerShell идут в стандартный ввод SSH (в командной строке — только
короткий загрузчик -EncodedCommand: у cmd.exe предел 8191 знак), ответы —
JSON в UTF-8. Учётки хранит хаб и передаёт в каждом вызове; ключ хоста SSH
запоминается при «Проверить» и сверяется; пароль — через SSH_ASKPASS.

Что умеет: состав (ОС и сборка, процессор, память, диски, адаптеры и
адреса, службы, программы, обновления KB, порты, пользователи и
администраторы, роли), метрики, находки (ожидают обновления, нужна
перезагрузка, Defender выключен или старые базы, брандмауэр выключен, RDP
без NLA, SMBv1, гость, BitLocker, ошибки журналов за сутки),
конфигурация (правила брандмауэра, типы запуска служб) с диффом, действия
(перезагрузка, службы, установка обновлений — в фоне, winget upgrade),
команды PowerShell.
Без зависимостей: стандартная библиотека Python и клиент OpenSSH.
"""
import base64
import hashlib
import hmac
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "1.0.1"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9250") or 9250)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
SSH = os.environ.get("WINDOWS_SSH", "ssh")
SSH_KEYSCAN = os.environ.get("WINDOWS_SSH_KEYSCAN", "ssh-keyscan")
TIMEOUT = float(os.environ.get("WINDOWS_TIMEOUT", "60") or 60)

T = {
    "ru": {
        "title": "Windows",
        "desc": "Windows как хосты nkt через встроенный OpenSSH и PowerShell: состав, метрики, находки, конфигурация с диффом, службы, обновления, команды PowerShell.",
        "type": "Windows", "hint": "OpenSSH Server: Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0; Start-Service sshd; Set-Service sshd -StartupType Automatic. Пользователь — администратор (для находок и действий).",
        "f_address": "Адрес", "f_address_hint": "IP или имя; порт — через двоеточие, если не 22", "f_user": "Пользователь", "f_user_hint": "локальное имя (whoami, часть после «\\»); для учётной записи Microsoft — тоже локальное имя, не почта; пароль — от учётной записи, не PIN", "f_password": "Пароль",
        "f_key": "Закрытый ключ SSH", "f_key_hint": "вставьте целиком (BEGIN … END) — вместо пароля; хранит хаб",
        "f_fingerprint": "Отпечаток ключа хоста", "f_fingerprint_hint": "пусто — запомнится при «Проверить»",
        "reboot": "Перезагрузить", "reboot_confirm": "Перезагрузить Windows? Пользователи и службы будут прерваны.",
        "svc": "Служба: запустить, остановить, перезапустить", "svc_name": "Служба (имя)", "svc_action": "Действие",
        "updates": "Установить обновления Windows", "updates_confirm": "Установить все ожидающие обновления? Идёт в фоне на сервере; перезагрузку делайте сами, когда удобно.",
        "winget": "winget upgrade", "winget_id": "Программа (id winget, пусто — все)",
        "no_ssh": "на хосте плагина нет {tool} — поставьте openssh-client",
        "ssh_auth": "{addr}: SSH не пустил ({err})", "ssh_fail": "{addr}: SSH — {err}", "unreachable": "{addr}: нет связи — {err}",
        "key_changed": "{addr}: ключ хоста изменился (был {old}, сейчас {new}) — если ожидаемо, очистите отпечаток в учётках хоста и нажмите «Проверить»",
        "not_windows": "{addr}: ответ не похож на PowerShell ({err}) — это Windows с OpenSSH Server?",
        "no_action": "нет действия {action}", "bad_arg": "{what}: латиница, цифры, . _ -",
        "exec_empty": "команда пуста", "would": "(сухой прогон) {what}", "would_exec": "выполнить на {addr}: {cmd}",
        "f_updates": "Ожидают установки обновлений: {n}", "f_updates_fix": "действие «Установить обновления Windows»",
        "f_reboot": "Нужна перезагрузка после обновлений", "f_defender": "Защитник Windows выключен", "f_rtp": "Защита в реальном времени выключена",
        "f_sigs": "Базы Защитника не обновлялись {n} дн.", "f_fw": "Брандмауэр выключен в профиле {name}",
        "f_rdp": "RDP включён без проверки подлинности на уровне сети (NLA)", "f_smb1": "Включён SMBv1 — устаревший и уязвимый",
        "f_smb1_fix": "Set-SmbServerConfiguration -EnableSMB1Protocol $false", "f_guest": "Учётная запись «Гость» включена",
        "f_bitlocker": "BitLocker выключен на системном диске {drive}", "f_events": "Ошибок в журналах System и Application за сутки: {n} (чаще всего: {top})",
        "f_disk": "Диск {drive} заполнен на {pct}%",
        "updates_started": "{addr}: установка обновлений запущена в фоне (задача NktWindowsUpdate); ход — C:\\ProgramData\\nkt\\updates.log",
        "check_ok": "проверено", "kb": "Обновления (KB)", "roles": "Роли и компоненты", "admins": "Администраторы",
        "installed": "Установлено", "id": "Номер", "name": "Имя", "feature": "Компонент",
    },
    "en": {
        "title": "Windows",
        "desc": "Windows as nkt hosts via the built-in OpenSSH and PowerShell: inventory, metrics, findings, configuration with a diff, services, updates, PowerShell commands.",
        "type": "Windows", "hint": "OpenSSH Server: Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0; Start-Service sshd; Set-Service sshd -StartupType Automatic. The user — an administrator (for findings and actions).",
        "f_address": "Address", "f_address_hint": "IP or name; a port after a colon if not 22", "f_user": "User", "f_user_hint": "the local name (whoami, the part after “\\”); for a Microsoft account also the local name, not the email; the account password, not the PIN", "f_password": "Password",
        "f_key": "SSH private key", "f_key_hint": "paste it whole (BEGIN … END) — instead of a password; the hub keeps it",
        "f_fingerprint": "Host key fingerprint", "f_fingerprint_hint": "empty — remembered on Check",
        "reboot": "Reboot", "reboot_confirm": "Reboot Windows? Users and services will be interrupted.",
        "svc": "Service: start, stop, restart", "svc_name": "Service (name)", "svc_action": "Action",
        "updates": "Install Windows updates", "updates_confirm": "Install all pending updates? It runs in the background on the server; reboot yourself when convenient.",
        "winget": "winget upgrade", "winget_id": "Program (winget id, empty — all)",
        "no_ssh": "{tool} is not on the plugin host — install openssh-client",
        "ssh_auth": "{addr}: SSH refused ({err})", "ssh_fail": "{addr}: SSH — {err}", "unreachable": "{addr}: unreachable — {err}",
        "key_changed": "{addr}: the host key changed (was {old}, now {new}) — if expected, clear the fingerprint in the host's login and press Check",
        "not_windows": "{addr}: the answer doesn't look like PowerShell ({err}) — is it Windows with OpenSSH Server?",
        "no_action": "no action {action}", "bad_arg": "{what}: latin letters, digits, . _ -",
        "exec_empty": "the command is empty", "would": "(dry run) {what}", "would_exec": "run on {addr}: {cmd}",
        "f_updates": "Updates waiting to install: {n}", "f_updates_fix": "the “Install Windows updates” action",
        "f_reboot": "A reboot is needed after updates", "f_defender": "Windows Defender is off", "f_rtp": "Real-time protection is off",
        "f_sigs": "Defender signatures haven't been updated for {n} days", "f_fw": "The firewall is off in the {name} profile",
        "f_rdp": "RDP is on without Network Level Authentication (NLA)", "f_smb1": "SMBv1 is on — outdated and vulnerable",
        "f_smb1_fix": "Set-SmbServerConfiguration -EnableSMB1Protocol $false", "f_guest": "The Guest account is enabled",
        "f_bitlocker": "BitLocker is off on the system drive {drive}", "f_events": "Errors in the System and Application logs over a day: {n} (most often: {top})",
        "f_disk": "Drive {drive} is {pct}% full",
        "updates_started": "{addr}: update installation started in the background (task NktWindowsUpdate); progress — C:\\ProgramData\\nkt\\updates.log",
        "check_ok": "checked", "kb": "Updates (KB)", "roles": "Roles and features", "admins": "Administrators",
        "installed": "Installed", "id": "Number", "name": "Name", "feature": "Feature",
    },
}


class Fail(Exception):
    """Ошибка для человека (уходит хабу как error)."""


class Ctx:
    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        return T[self.lang][key].format(**kw)

    def fail(self, key, **kw):
        return Fail(self.t(key, **kw))


def log(msg):
    print(f"nkt-plugin-windows: {msg}", flush=True)


# --- SSH (как у connector) ------------------------------------------------------------

PEM_RE = re.compile(r"(-----BEGIN [A-Z ]+-----)\s*(.*?)\s*(-----END [A-Z ]+-----)", re.S)


def pem_lines(key):
    m = PEM_RE.search(key or "")
    if not m:
        return None
    body = "\n".join(re.findall(r"\S{1,70}", re.sub(r"\s+", "", m.group(2))))
    return f"{m.group(1)}\n{body}\n{m.group(3)}\n"


def key_fingerprint(b64):
    raw = base64.b64decode(b64 + "=" * (-len(b64) % 4))
    return "SHA256:" + base64.b64encode(hashlib.sha256(raw).digest()).decode().rstrip("=")


def split_addr(addr):
    addr = str(addr or "").strip()
    # Первый знак — не «-»: адрес уходит в argv ssh и ssh-keyscan.
    if not re.match(r"^[A-Za-z0-9\[][A-Za-z0-9.\-:\[\]]{0,254}$", addr):
        raise Fail(f"bad address {addr!r}")
    m = re.match(r"^\[([^\]]+)\](?::(\d{1,5}))?$", addr)
    if m:
        return m.group(1), int(m.group(2) or 22)
    if addr.count(":") == 1:
        h, p = addr.split(":")
        if not h or not p.isdigit() or not 0 < int(p) < 65536:
            raise Fail(f"bad address {addr!r}")
        return h, int(p)
    return addr, 22

def ssh_user(user):
    """Имя для ssh -l: не с «-» и без управляющих знаков; пробел и «@» можно
    (локальные имена Windows с пробелом, домен user@corp.local)."""
    user = user.strip()
    if not user or user.startswith("-") or len(user) > 256 or any(ord(ch) < 32 or ord(ch) == 127 for ch in user):
        raise Fail(f"bad user {user!r}")
    return user



# Загрузчик: вывод в UTF-8, сценарий — целиком из стандартного ввода.
BOOT = "[Console]::OutputEncoding=[Text.Encoding]::UTF8;$ErrorActionPreference='SilentlyContinue';$ProgressPreference='SilentlyContinue';$s=[Console]::In.ReadToEnd();Invoke-Expression $s"
BOOT_CMD = "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + base64.b64encode(BOOT.encode("utf-16-le")).decode()


class Win:
    def __init__(self, c, conn):
        self.c = c
        self.addr = str(conn.get("address") or "")
        self.host, self.port = split_addr(self.addr)
        self.user = ssh_user(str(conn.get("user") or "Administrator"))
        self.password = str(conn.get("password") or "")
        self.key = pem_lines(str(conn.get("key") or ""))
        self.pin = str(conn.get("fingerprint") or "").strip()
        self.seen = ""
        self.tmp = None

    def __enter__(self):
        for tool in (SSH, SSH_KEYSCAN):
            if not shutil.which(tool):
                raise self.c.fail("no_ssh", tool=tool)
        self.tmp = tempfile.mkdtemp(prefix="nkt-windows-")
        os.chmod(self.tmp, 0o700)
        try:
            out = subprocess.run([SSH_KEYSCAN, "-T", "8", "-p", str(self.port), "--", self.host], capture_output=True, text=True, timeout=30).stdout
        except subprocess.TimeoutExpired:
            raise self.c.fail("unreachable", addr=self.addr, err="ssh-keyscan timeout")
        keys = [l for l in out.splitlines() if l.strip() and not l.startswith("#") and len(l.split()) >= 3]
        if not keys:
            raise self.c.fail("unreachable", addr=self.addr, err="no SSH host key")
        fps = [key_fingerprint(l.split()[2]) for l in keys]
        prefer = [f for l, f in zip(keys, fps) if l.split()[1] == "ssh-ed25519"]
        self.seen = (prefer or fps)[0]
        if self.pin and self.pin not in fps:
            raise self.c.fail("key_changed", addr=self.addr, old=self.pin[:24] + "…", new=self.seen[:24] + "…")
        with open(os.path.join(self.tmp, "known_hosts"), "w") as f:
            f.write("\n".join(keys) + "\n")
        if self.key:
            with open(os.open(os.path.join(self.tmp, "id"), os.O_WRONLY | os.O_CREAT, 0o600), "w") as f:
                f.write(self.key)
        else:
            with open(os.open(os.path.join(self.tmp, "askpass"), os.O_WRONLY | os.O_CREAT, 0o700), "w") as f:
                f.write('#!/bin/sh\nprintf \'%s\\n\' "$NKT_SSH_PASS"\n')
        return self

    def __exit__(self, *a):
        if self.tmp:
            shutil.rmtree(self.tmp, ignore_errors=True)

    def ps(self, script, timeout=None):
        """Сценарий PowerShell; ответ — текст (stdout)."""
        args = [SSH, "-F", "/dev/null", "-p", str(self.port), "-o", "StrictHostKeyChecking=yes",
                "-o", "UserKnownHostsFile=" + os.path.join(self.tmp, "known_hosts"), "-o", "ConnectTimeout=10", "-o", "LogLevel=ERROR"]
        env = dict(os.environ)
        if self.key:
            args += ["-i", os.path.join(self.tmp, "id"), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes"]
        else:
            args += ["-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "NumberOfPasswordPrompts=1"]
            env.update(SSH_ASKPASS=os.path.join(self.tmp, "askpass"), SSH_ASKPASS_REQUIRE="force", DISPLAY=env.get("DISPLAY", ":0"), NKT_SSH_PASS=self.password)
        args += ["-l", self.user, "--", self.host, BOOT_CMD]
        try:
            r = subprocess.run(args, input=script, capture_output=True, text=True, encoding="utf-8", errors="replace",
                               timeout=timeout or TIMEOUT, env=env, start_new_session=True)
        except subprocess.TimeoutExpired:
            raise self.c.fail("ssh_fail", addr=self.addr, err="timeout")
        if r.returncode == 255:
            err = (r.stderr.strip().splitlines() or ["?"])[-1][:200]
            if "Permission denied" in err or "Authentication" in err:
                raise self.c.fail("ssh_auth", addr=self.addr, err=err)
            raise self.c.fail("ssh_fail", addr=self.addr, err=err)
        return r.stdout

    def psj(self, script, timeout=None):
        out = self.ps(script + "\n", timeout).strip().lstrip("\ufeff")
        try:
            return json.loads(out) if out else {}
        except ValueError:
            raise self.c.fail("not_windows", addr=self.addr, err=out[:80] or "empty")


# --- сценарии PowerShell ------------------------------------------------------------

PS_INFO = r"""# nkt:info
$os=Get-CimInstance Win32_OperatingSystem; $cs=Get-CimInstance Win32_ComputerSystem; $b=Get-CimInstance Win32_BIOS
[pscustomobject]@{name=$cs.DNSHostName; os=$os.Caption; version="$($os.Version) (build $($os.BuildNumber))"; model="$($cs.Manufacturer) $($cs.Model)";
 serial=$b.SerialNumber; arch=$os.OSArchitecture; uptime_s=[int]((Get-Date)-$os.LastBootUpTime).TotalSeconds; domain=$cs.Domain} | ConvertTo-Json -Compress
"""

PS_SNAPSHOT = r"""# nkt:snapshot
$os=Get-CimInstance Win32_OperatingSystem; $cpu=@(Get-CimInstance Win32_Processor)
$ad=@(Get-NetAdapter | ForEach-Object { $a=$_; [pscustomobject]@{name=$a.Name; type=$a.InterfaceDescription; mac=$a.MacAddress; up=($a.Status -eq 'Up');
  addrs=@(Get-NetIPAddress -InterfaceIndex $a.ifIndex | Where-Object {$_.IPAddress -notlike 'fe80*'} | ForEach-Object { $_.IPAddress })} })
$svc=@(Get-Service | ForEach-Object { [pscustomobject]@{name=$_.Name; state="$($_.Status)".ToLower(); enabled=("$($_.StartType)" -eq 'Automatic'); detail=$_.DisplayName} })
$pk=@(Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' |
  Where-Object {$_.DisplayName} | Sort-Object DisplayName -Unique | ForEach-Object { [pscustomobject]@{name=$_.DisplayName; version="$($_.DisplayVersion)"} })
$procs=@{}; Get-Process | ForEach-Object { $procs[$_.Id]=$_.ProcessName }
$ports=@(Get-NetTCPConnection -State Listen | Sort-Object LocalPort -Unique | ForEach-Object { [pscustomobject]@{proto='tcp'; port=$_.LocalPort; service=$procs[[int]$_.OwningProcess]; allowed=$(if($_.LocalAddress -in '0.0.0.0','::'){''}else{$_.LocalAddress})} })
$users=@(Get-LocalUser | Where-Object Enabled | ForEach-Object { [pscustomobject]@{name=$_.Name; group=''} })
$admins=@(Get-LocalGroupMember -SID 'S-1-5-32-544' | ForEach-Object { [pscustomobject]@{name="$($_.Name)"} })
$disks=@(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object { [pscustomobject]@{name=$_.DeviceID; size=[int64]$_.Size; used=[int64]($_.Size-$_.FreeSpace); mount=$_.DeviceID} })
$kb=@(Get-HotFix | Sort-Object InstalledOn -Descending | Select-Object -First 50 | ForEach-Object { [pscustomobject]@{id=$_.HotFixID; installed="$($_.InstalledOn)"} })
$roles=@(); try { $roles=@(Get-WindowsFeature | Where-Object Installed | ForEach-Object { [pscustomobject]@{feature=$_.DisplayName} }) } catch {}
[pscustomobject]@{cpu=[pscustomobject]@{model=$cpu[0].Name; cores=($cpu | Measure-Object NumberOfLogicalProcessors -Sum).Sum}; mem_total=[int64]$os.TotalVisibleMemorySize*1024;
 interfaces=$ad; services=$svc; packages=$pk; ports=$ports; users=$users; admins=$admins; disks=$disks; kb=$kb; roles=$roles} | ConvertTo-Json -Depth 4 -Compress
"""

PS_METRICS = r"""# nkt:metrics
$os=Get-CimInstance Win32_OperatingSystem; $c=Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$env:SystemDrive'"
$net=@{}; Get-NetAdapterStatistics | ForEach-Object { $net[$_.Name]=[pscustomobject]@{rx=[int64]$_.ReceivedBytes; tx=[int64]$_.SentBytes} }
[pscustomobject]@{cpu_pct=[double](Get-CimInstance Win32_Processor | Measure-Object LoadPercentage -Average).Average;
 mem_total=[int64]$os.TotalVisibleMemorySize*1024; mem_used=[int64]($os.TotalVisibleMemorySize-$os.FreePhysicalMemory)*1024;
 disk_size=[int64]$c.Size; disk_used=[int64]($c.Size-$c.FreeSpace); uptime_s=[int]((Get-Date)-$os.LastBootUpTime).TotalSeconds; net=$net} | ConvertTo-Json -Depth 3 -Compress
"""

PS_FINDINGS = r"""# nkt:findings
$r=[ordered]@{}
try { $u=(New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'"); $r.updates=$u.Updates.Count } catch { $r.updates=-1 }
$r.reboot=(Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired') -or (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending')
try { $m=Get-MpComputerStatus; $r.defender=$m.AntivirusEnabled; $r.rtp=$m.RealTimeProtectionEnabled; $r.sig_age=$m.AntivirusSignatureAge } catch { $r.defender=$null }
$r.firewall=@(Get-NetFirewallProfile | ForEach-Object { [pscustomobject]@{name="$($_.Name)"; enabled=[bool]$_.Enabled} })
$ts=Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server'; $nla=Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp'
$r.rdp=($ts.fDenyTSConnections -eq 0); $r.nla=($nla.UserAuthentication -eq 1)
try { $r.smb1=(Get-SmbServerConfiguration).EnableSMB1Protocol } catch { $r.smb1=$null }
$g=Get-LocalUser | Where-Object { $_.SID -like '*-501' }; $r.guest=[bool]$g.Enabled
try { $bl=Get-BitLockerVolume -MountPoint $env:SystemDrive; $r.bitlocker="$($bl.ProtectionStatus)" } catch { $r.bitlocker='' }
$r.sysdrive=$env:SystemDrive
$r.disks=@(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | Where-Object Size | ForEach-Object { [pscustomobject]@{drive=$_.DeviceID; pct=[int](100*($_.Size-$_.FreeSpace)/$_.Size)} })
$ev=@(Get-WinEvent -FilterHashtable @{LogName='System','Application'; Level=1,2; StartTime=(Get-Date).AddDays(-1)} -MaxEvents 500)
$r.errors=$ev.Count; $r.top=@($ev | Group-Object ProviderName | Sort-Object Count -Descending | Select-Object -First 3 | ForEach-Object { "$($_.Name) ($($_.Count))" }) -join ', '
[pscustomobject]$r | ConvertTo-Json -Depth 3 -Compress
"""

PS_CONFIG = r"""# nkt:config
'### Firewall: inbound allow'
Get-NetFirewallRule -Enabled True -Direction Inbound -Action Allow | Sort-Object DisplayName | ForEach-Object { "$($_.DisplayName) [$($_.Profile)]" }
'### Services: start type'
Get-Service | Sort-Object Name | ForEach-Object { "$($_.Name): $($_.StartType)" }
'### Local administrators'
Get-LocalGroupMember -SID 'S-1-5-32-544' | Sort-Object Name | ForEach-Object { "$($_.Name)" }
"""

PS_UPDATES = r"""# nkt:updates
$d='C:\ProgramData\nkt'; New-Item -ItemType Directory -Force -Path $d | Out-Null
$job=@'
$log='C:\ProgramData\nkt\updates.log'; "$(Get-Date -Format s) start" | Out-File $log -Append
$s=New-Object -ComObject Microsoft.Update.Session; $r=$s.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
if ($r.Updates.Count -eq 0) { "$(Get-Date -Format s) nothing to install" | Out-File $log -Append; exit }
$dl=$s.CreateUpdateDownloader(); $dl.Updates=$r.Updates; $dl.Download() | Out-Null
$in=$s.CreateUpdateInstaller(); $in.Updates=$r.Updates; $res=$in.Install()
"$(Get-Date -Format s) result $($res.ResultCode), reboot required: $($res.RebootRequired)" | Out-File $log -Append
'@
Set-Content -Path "$d\updates.ps1" -Value $job -Encoding UTF8
$a=New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File $d\updates.ps1"
Register-ScheduledTask -TaskName 'NktWindowsUpdate' -Action $a -User 'SYSTEM' -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName 'NktWindowsUpdate'; 'started'
"""

NAME_RE = re.compile(r"^[A-Za-z0-9._-]{1,128}$")


def info(c, w):
    i = w.psj(PS_INFO)
    if not isinstance(i, dict) or not i.get("os"):
        raise c.fail("not_windows", addr=w.addr, err="no Win32_OperatingSystem")
    out = {k: str(i.get(k) or "") for k in ("name", "os", "version", "model", "serial", "arch")}
    out["uptime_s"] = int(i.get("uptime_s") or 0)
    out["address"] = w.addr
    out["fingerprint"] = w.seen
    return {k: v for k, v in out.items() if v not in ("", None)}


def lst(v):
    if v is None:
        return []
    return v if isinstance(v, list) else [v]  # ConvertTo-Json превращает массив из одного элемента в объект


def snapshot(c, w):
    s = w.psj(PS_SNAPSHOT, timeout=180)
    out = {"info": info(c, w), "cpu": s.get("cpu") or {}, "mem_total": int(s.get("mem_total") or 0)}
    for k in ("services", "packages", "users", "disks"):
        out[k] = lst(s.get(k))
    out["interfaces"] = [dict(x, addrs=lst(x.get("addrs"))) for x in lst(s.get("interfaces"))]
    out["ports"] = [dict(x, service=str(x.get("service") or "")) for x in lst(s.get("ports"))]
    out["extra"] = [
        {"type": "table", "title": c.t("admins"), "columns": [{"key": "name", "title": c.t("name")}], "rows": [{"cells": {"name": a.get("name", "")}} for a in lst(s.get("admins"))]},
        {"type": "table", "title": c.t("kb"), "columns": [{"key": "id", "title": c.t("id")}, {"key": "installed", "title": c.t("installed")}],
         "rows": [{"cells": {"id": k.get("id", ""), "installed": str(k.get("installed", ""))[:10]}} for k in lst(s.get("kb"))]},
    ]
    roles = lst(s.get("roles"))
    if roles:
        out["extra"].append({"type": "table", "title": c.t("roles"), "columns": [{"key": "feature", "title": c.t("feature")}],
                             "rows": [{"cells": {"feature": r.get("feature", "")}} for r in roles]})
    return out


def metrics(w):
    m = w.psj(PS_METRICS)
    out = {k: m[k] for k in ("cpu_pct", "mem_total", "mem_used", "disk_size", "disk_used", "uptime_s") if m.get(k) is not None}
    out["net"] = {k: {"rx": int(v.get("rx") or 0), "tx": int(v.get("tx") or 0)} for k, v in (m.get("net") or {}).items() if isinstance(v, dict)}
    return out


def findings(c, w):
    r = w.psj(PS_FINDINGS, timeout=240)
    out = []
    n = int(r.get("updates") or 0)
    if n > 0:
        out.append({"id": "updates", "severity": "warning", "title": c.t("f_updates", n=n), "fix": c.t("f_updates_fix")})
    if r.get("reboot"):
        out.append({"id": "reboot", "severity": "warning", "title": c.t("f_reboot")})
    if r.get("defender") is False:
        out.append({"id": "defender", "severity": "critical", "title": c.t("f_defender")})
    elif r.get("rtp") is False:
        out.append({"id": "rtp", "severity": "warning", "title": c.t("f_rtp")})
    if int(r.get("sig_age") or 0) > 7:
        out.append({"id": "signatures", "severity": "warning", "title": c.t("f_sigs", n=r["sig_age"]), "fix": "Update-MpSignature"})
    for p in lst(r.get("firewall")):
        if not p.get("enabled"):
            out.append({"id": f"firewall-{p.get('name', '').lower()}", "severity": "warning", "title": c.t("f_fw", name=p.get("name", "?")),
                        "fix": f"Set-NetFirewallProfile -Profile {p.get('name', '')} -Enabled True"})
    if r.get("rdp") and not r.get("nla"):
        out.append({"id": "rdp-nla", "severity": "warning", "title": c.t("f_rdp")})
    if r.get("smb1"):
        out.append({"id": "smb1", "severity": "critical", "title": c.t("f_smb1"), "fix": c.t("f_smb1_fix")})
    if r.get("guest"):
        out.append({"id": "guest", "severity": "warning", "title": c.t("f_guest"), "fix": "Disable-LocalUser -SID ((Get-LocalUser | ? SID -like '*-501').SID)"})
    if str(r.get("bitlocker") or "") in ("Off", "0"):
        out.append({"id": "bitlocker", "severity": "info", "title": c.t("f_bitlocker", drive=r.get("sysdrive", "C:"))})
    for d in lst(r.get("disks")):
        if int(d.get("pct") or 0) > 90:
            out.append({"id": f"disk-{d.get('drive', '')}", "severity": "warning", "title": c.t("f_disk", drive=d.get("drive", ""), pct=d.get("pct"))})
    if int(r.get("errors") or 0) > 0:
        out.append({"id": "event-errors", "severity": "info", "title": c.t("f_events", n=r["errors"], top=r.get("top") or "?")})
    return out


def config(w):
    text = w.ps(PS_CONFIG + "\n", timeout=120).replace("\r\n", "\n").lstrip("\ufeff")
    return text.strip() + "\n"


def ps_quote(s):
    return "'" + str(s).replace("'", "''") + "'"


def action(c, w, name, args, dry):
    args = args or {}
    if name == "reboot":
        if dry:
            return c.t("would", what=c.t("reboot"))
        w.ps("Restart-Computer -Force\n")
        return c.t("reboot")
    if name == "service":
        svc, act = str(args.get("name", "")).strip(), str(args.get("action", "restart")).strip()
        if not NAME_RE.match(svc):
            raise c.fail("bad_arg", what=c.t("svc_name"))
        if act not in ("start", "stop", "restart"):
            raise c.fail("no_action", action=act)
        cmd = {"start": "Start-Service", "stop": "Stop-Service -Force", "restart": "Restart-Service -Force"}[act]
        if dry:
            return c.t("would", what=f"{cmd} {svc}")
        out = w.ps(f"{cmd} -Name {ps_quote(svc)} -ErrorAction Stop; (Get-Service -Name {ps_quote(svc)}).Status\n")
        return f"{svc}: {out.strip() or act}"
    if name == "updates":
        if dry:
            return c.t("would", what=c.t("updates"))
        w.ps(PS_UPDATES + "\n", timeout=120)
        return c.t("updates_started", addr=w.addr)
    if name == "winget":
        pid = str(args.get("id", "")).strip()
        if pid and not NAME_RE.match(pid):
            raise c.fail("bad_arg", what=c.t("winget_id"))
        target = f"--id {ps_quote(pid)}" if pid else "--all"
        if dry:
            return c.t("would", what=f"winget upgrade {target}")
        return w.ps(f"winget upgrade {target} --silent --accept-source-agreements --accept-package-agreements --disable-interactivity 2>&1 | Out-String\n", timeout=900).strip()
    raise c.fail("no_action", action=name)


# --- манифест и вызовы --------------------------------------------------------------

def manifest(c):
    return {
        "protocol": 1, "name": "windows", "title": c.t("title"), "version": VERSION, "description": c.t("desc"), "ops": [], "provides": ["hosts"],
        "host_types": [{
            "id": "windows", "title": c.t("type"), "hint": c.t("hint"),
            "fields": [{"name": "address", "title": c.t("f_address"), "required": True, "hint": c.t("f_address_hint"), "placeholder": "192.168.1.20"},
                       {"name": "user", "title": c.t("f_user"), "hint": c.t("f_user_hint"), "default": "Administrator"},
                       {"name": "password", "title": c.t("f_password"), "type": "password"},
                       {"name": "key", "title": c.t("f_key"), "type": "password", "hint": c.t("f_key_hint")},
                       {"name": "fingerprint", "title": c.t("f_fingerprint"), "hint": c.t("f_fingerprint_hint")}],
            "sections": ["overview", "metrics", "findings", "config", "actions", "exec"],
            "actions": [{"title": c.t("reboot"), "op": "reboot", "confirm": c.t("reboot_confirm"), "danger": True},
                        {"title": c.t("svc"), "op": "service", "fields": [{"name": "name", "title": c.t("svc_name"), "required": True, "placeholder": "Spooler"},
                                                                         {"name": "action", "title": c.t("svc_action"), "placeholder": "restart"}]},
                        {"title": c.t("updates"), "op": "updates", "confirm": c.t("updates_confirm")},
                        {"title": c.t("winget"), "op": "winget", "fields": [{"name": "id", "title": c.t("winget_id"), "placeholder": "Mozilla.Firefox"}]}],
            "exec": "powershell"}],
    }


def truthy(v):
    return v is True or str(v).lower() in ("true", "1", "yes", "on")


def handle(c, req):
    op = req.get("op", "")
    args = req.get("args") or {}
    dry = bool(req.get("dry_run")) or truthy(args.get("dry_run"))
    if op == "_connect":
        return {"ok": True}
    if not op.startswith("_host_"):
        return {"ok": False, "error": f"no op {op}"}
    if args.get("type") != "windows":
        return {"ok": False, "error": f"unknown host type {args.get('type')}"}
    conn = args.get("conn") or {}
    if op == "_host_exec":
        cmd = str(args.get("command") or "").replace("\r\n", "\n").strip()
        if not cmd:
            raise c.fail("exec_empty")
        if truthy(args.get("check")):
            return {"ok": True, "text": c.t("check_ok")}
        if dry:
            return {"ok": True, "text": c.t("would", what=c.t("would_exec", addr=conn.get("address"), cmd=cmd))}
    with Win(c, conn) as w:
        if op == "_host_check":
            return {"ok": True, "result": info(c, w)}
        if op == "_host_snapshot":
            return {"ok": True, "result": snapshot(c, w)}
        if op == "_host_metrics":
            return {"ok": True, "result": metrics(w)}
        if op == "_host_findings":
            return {"ok": True, "result": {"findings": findings(c, w)}}
        if op == "_host_config":
            return {"ok": True, "result": {"text": config(w)}}
        if op == "_host_action":
            text = action(c, w, str(args.get("action") or ""), args.get("args") or {}, dry)
            if not dry:
                log(f"{req.get('user', '')}: {args.get('action')} на {w.addr}")
            return {"ok": True, "text": text}
        if op == "_host_exec":
            log(f"{req.get('user', '')}: команда на {w.addr} ({len(cmd)} знаков)")
            return {"ok": True, "text": w.ps(cmd + "\n", timeout=300).replace("\r\n", "\n").lstrip("\ufeff")}
    return {"ok": False, "error": f"no op {op}"}


class Handler(BaseHTTPRequestHandler):
    server_version = "nkt-plugin-windows/" + VERSION

    def log_message(self, fmt, *a):
        pass

    def reply(self, obj, code=200):
        raw = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def signed(self, body):
        ts = self.headers.get("X-NKT-Timestamp", "")
        try:
            fresh = abs(time.time() - int(ts)) < 300
        except ValueError:
            return False
        path = urllib.parse.urlsplit(self.path).path
        want = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
        return fresh and hmac.compare_digest(want, self.headers.get("X-NKT-Signature", ""))

    def do_GET(self):
        if not self.signed(b""):
            return self.reply({"error": "bad signature"}, 401)
        if urllib.parse.urlsplit(self.path).path != "/nkt/manifest":
            return self.reply({"error": "not found"}, 404)
        self.reply(manifest(Ctx(self.headers.get("X-NKT-Lang", "ru"))))

    def do_POST(self):
        size = int(self.headers.get("Content-Length") or 0)
        if size > 1 << 20:
            return self.reply({"error": "too large"}, 413)
        body = self.rfile.read(size)
        if not self.signed(body):
            return self.reply({"error": "bad signature"}, 401)
        if urllib.parse.urlsplit(self.path).path != "/nkt/call":
            return self.reply({"error": "not found"}, 404)
        c = Ctx(self.headers.get("X-NKT-Lang", "ru"))
        try:
            self.reply(handle(c, json.loads(body or b"{}")))
        except Fail as e:
            self.reply({"ok": False, "error": str(e)})
        except Exception as e:  # ошибка плагина — текстом хабу, а не обрывом
            log(f"ошибка: {type(e).__name__}")
            self.reply({"ok": False, "error": f"plugin error: {type(e).__name__}"})


def main():
    if len(SECRET) < 16:
        log("NKT_PLUGIN_SECRET не задан или короче 16 знаков — хаб не подключится")
        sys.exit(1)
    log(f"{VERSION}: {LISTEN}:{PORT}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
