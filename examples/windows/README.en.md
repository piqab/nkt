# Windows plugin (OpenSSH + PowerShell)

Windows servers and workstations as nkt hosts — no agent and no WinRM,
via the built-in OpenSSH Server and PowerShell 5.1+.

- **Inventory:** OS and build, CPU, memory, disks, adapters and addresses,
  services, programs, updates (KB), ports and their processes, users and
  administrators, server roles.
- **Metrics** (every minute — into the hub's Monitoring): CPU, memory,
  system drive, adapter traffic.
- **Findings:** updates pending; a reboot is needed; Defender or real-time
  protection is off, old signatures; the firewall is off in a profile; RDP
  without NLA; SMBv1; Guest is enabled; BitLocker is off; a full drive;
  System and Application log errors over a day.
- **Configuration** with a diff: allowing inbound firewall rules, service
  start types, local administrators.
- **Actions:** reboot; a service — start, stop, restart; install updates
  (in the background as the `NktWindowsUpdate` scheduled task, log —
  `C:\ProgramData\nkt\updates.log`); `winget upgrade`.
- **PowerShell commands:** on the device page, in scripts
  (`on win1 run "Get-Process | Sort CPU -desc | Select -First 5"`) and MCP
  (`device_exec`).

## Windows

```powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service sshd -StartupType Automatic
```

The user — an administrator (some findings and all actions need rights):
the local name from `whoami`; for a Microsoft account also the local name,
not the email, and the account password, not the PIN. Log in with a
password or a key: for administrators the keys go into
`C:\ProgramData\ssh\administrators_authorized_keys` (rights via `icacls`:
Administrators and SYSTEM only). A “Public” network needs the SSH rule for
every profile: `Set-NetFirewallRule -Name OpenSSH-Server-In-TCP -Enabled
True -Profile Any`. Details, and where to install the plugin when Windows
is a VM (macvtap: the KVM host cannot reach its own VM) — in the
[Plugin hosts](https://piqab.github.io/nkt/en/guide/hub-plugin-hosts) guide.

## Install

About → Plugins → **Install on a host** → Windows — the hub machine or a
host the Windows machines are reachable from (needs the OpenSSH client).
Then Hosts → **Add a device** → “Windows” → address, user, password or
key → Check → Add.

## How it works

The PowerShell script goes into SSH's standard input, while the command
line carries only a short `powershell -EncodedCommand …` bootstrap
(`cmd.exe` has an 8191-character limit); the answer is JSON in UTF-8. The
host key is remembered on Check and verified; the password goes through
`SSH_ASKPASS`, not the command line; the hub keeps the logins.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against a fake `ssh` (the
bootstrap, PowerShell answers, no password in arguments); `go test
./examples` runs it too.
