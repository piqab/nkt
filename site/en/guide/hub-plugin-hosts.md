---
title: 'Plugin hosts: MikroTik and other devices'
---

# Plugin hosts: MikroTik and other devices

Not everything you need to manage is Linux with the nkt agent. A MikroTik
router, a Windows server, a cloud VM: they have no agent, but they do
have an API or SSH. A **plugin host** is such a device in nkt's host list:
a [plugin](/en/guide/hub-plugins) reads and manages it, and the hub gives
everything else — groups, token limits, alerts, Monitoring, scripts, MCP,
export.

The first such plugin is **MikroTik (RouterOS 7)**: [README](https://github.com/piqab/nkt/tree/main/examples/mikrotik).

## Add a device

1. Install the plugin: About → Plugins → Install on a host → MikroTik — on
   the hub machine or any host the devices are reachable from.
2. Hosts → **Add a device** → type → fields (address, user, password…) →
   **Check** → Add. The name can be left empty — it's taken from the
   device.

![Add a device](/screens/en/hub-device-add.png)

- **The hub keeps the login**, encrypted with the master key like the
  hosts' SSH keys, and passes it to the plugin in each call. The password
  is shown to no one — the card only says “set”; it reaches neither the
  action log nor job parameters.
- **The device's own certificate** (usual for routers): the fingerprint
  is remembered on Check and compared on every connection — like a host's
  SSH key.

## What you see and can do

The row in the host list is like any other: state, findings, address,
“seen”. Open — the device panel with the sections its type has:

| Section | What |
|---|---|
| **Inventory** | model, version, interfaces and addresses, service ports and where they're open from, network neighbors, the type's tables (MikroTik — routes, firewall, DHCP) |
| **Metrics** | now; the hub polls the device every minute — the history is in Monitoring alongside the other hosts |
| **Findings** | what's wrong (updates, services open to the outside…) with a hint how to fix; critical ones go to alerts, as with agent hosts |
| **Configuration** | the hub reads it every hour and keeps 30 versions: changed on the device — a new version and a diff, whoever changed it |
| **Actions** | reboot, update and whatever else the type declares; with a confirmation and a dry run |
| **Commands** | a command in the device's language (RouterOS, PowerShell) — with a confirmation, into the action log |
| **Login** | change the login and password (empty keeps the current one), with Check and a diff |

![Device: inventory](/screens/en/hub-device-overview.png)

![Device: configuration diff](/screens/en/hub-device-config.png)

Anyone who sees the host can view (and a read token within its hosts);
actions, commands and the login — an admin or a by-permission token with
the “plugin NAME” action. Removing a device from nkt changes nothing on
the device itself.

## Scripts and MCP

The script command `on HOST run "COMMAND"` — or a block up to `end`:

```
on router-office run "/ip dns cache flush"
on router-office run
/ip firewall address-list add list=blocked address=198.51.100.7
/ip firewall address-list print where list=blocked
end
```

Check hands the command to the plugin to parse; a dry run — the plugin
says what it would run. A plugin host takes only `run` (it has no nkt
agent); an agent host can't take `run`.

MCP: `list_hosts` shows plugin hosts too (`kind: plugin`), and
`device_snapshot`, `device_findings`, `device_config` (read),
`device_action` and `device_exec` (admin) — their sections.

## Your own device type (for plugin authors)

The plugin declares `provides: ["hosts"]` and `host_types` in its
manifest:

```json
{"provides": ["hosts"],
 "host_types": [{
   "id": "routeros", "title": "MikroTik RouterOS", "hint": "what to turn on on the device",
   "fields": [{"name": "address", "title": "Address", "required": true},
              {"name": "password", "title": "Password", "type": "password", "required": true},
              {"name": "fingerprint", "title": "Fingerprint"}],
   "sections": ["overview", "metrics", "findings", "config", "actions", "exec"],
   "actions": [{"title": "Reboot", "op": "reboot", "confirm": "Sure?", "danger": true}],
   "exec": "routeros"}]}
```

The `address` field is required; `type: password` is a secret (the hub
doesn't show it); the hub fills the `fingerprint` field with what the
check returned. The hub calls service operations with
`args: {type, conn, …}` (`conn` — the form fields):

| Operation | Answer |
|---|---|
| `_host_check` | `result`: `name`, `os`, `version`, `model`, `serial`, `firmware`, `uptime_s`, `fingerprint` |
| `_host_snapshot` | `result`: `info`, `cpu`, `mem_total`, `disks`, `interfaces`, `services`, `packages`, `ports`, `users`, `neighbors`, `extra` (table views) |
| `_host_metrics` | `result`: `cpu_pct`, `mem_used`, `mem_total`, `disk_used`, `disk_size`, `temp_c`, `uptime_s`, `net` (`{interface: {rx, tx}}` — bytes since start) |
| `_host_findings` | `result.findings`: `id`, `severity` (`critical` / `warning` / `info`), `title`, `detail`, `fix` |
| `_host_action` | `action`, `args`, `dry_run` → `text` |
| `_host_config` | `result.text` (without changing lines — the export time) |
| `_host_exec` | `command`, `check` (only parse), `dry_run` → `text` |

The sample is `examples/mikrotik/deploy/nkt-plugin.py` and its test on a
fake device.

## Export and import

Plugin hosts travel with the hub export along with their group, plugin
and type; the logins are re-encrypted with the receiving hub's key.
Configuration versions don't travel — the new hub collects them by
polling.
