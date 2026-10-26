# The connector plugin: devices by profiles

Devices without the nkt agent — **no code of your own**: a device type is
described by a profile (JSON), and the plugin connects, reads and parses
the answers itself. Each profile is a type in Hosts → Add a device.

| Profile | Link | What |
|---|---|---|
| `linux-ssh` | SSH | old and locked-down Linux where nkt can't be installed |
| `macos` | SSH | macOS: launchd services, brew, firewall, FileVault, SIP, updates |
| `freebsd` | SSH | FreeBSD: services, pkg and `pkg audit`, `freebsd-update`, rc.conf, pf.conf |
| `openwrt` | SSH | OpenWrt: ubus, interfaces, opkg packages, DHCP, `uci export` with a diff |
| `keenetic` | HTTP (RCI) | Keenetic: interfaces, devices on the network, running-config, CLI commands |
| `snmp-printer` | SNMP v2c | a printer: supplies (a finding below 10 %), page counter |
| `snmp-switch` | SNMP v2c | a switch: ports, LLDP neighbors, port traffic |

Your own profiles — the plugin's Profiles section: New profile (based on a
built-in one) and Edit — a window with a diff and version history on the
hub. Your profile with a built-in's name replaces it. The format — the
“[Plugin hosts](https://piqab.github.io/nkt/en/guide/hub-plugin-hosts)”
guide.

## Install

About → Plugins → **Install on a host** → Connector — on the hub machine
or any host the devices are reachable from. SSH profiles need the OpenSSH
client on that host (`ssh`, `ssh-keyscan`). Service
`nkt-plugin-connector`, 127.0.0.1:9230; your profiles live in its data
directory (`/var/lib/nkt-plugin-connector`).

## Security

- The hub keeps device logins (encrypted) and passes them in each call;
  the plugin neither stores nor logs them.
- SSH: the host key is remembered on Check and verified on every
  connection (`StrictHostKeyChecking=yes` against the remembered one); the
  password goes through `SSH_ASKPASS`, not the command line; a private key
  is in a 0600 temp file removed after the call.
- Action arguments from a form — only latin letters, digits and
  `._@/:+-`, quoted into the command (`shlex.quote`).
- SNMP — read-only (v2c); the `public` community is a finding.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against fake `ssh` and
`ssh-keyscan`, a fake Keenetic and an SNMP agent; `go test ./examples`
runs it too.
