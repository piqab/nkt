# MikroTik plugin (RouterOS 7)

MikroTik routers and switches as nkt hosts — the reference plugin for
[plugin hosts](https://piqab.github.io/nkt/en/guide/hub-plugin-hosts): a
device without the nkt agent that a plugin reads and manages.

- **Inventory:** model, version, firmware, CPU and memory, interfaces and
  addresses, service ports and where they're open from, neighbors
  (MNDP/LLDP), routes, firewall, DHCP leases, packages, users.
- **Metrics** (every minute — into the hub's Monitoring): CPU, memory,
  disk, temperature, interface speed.
- **Findings:** a RouterOS or firmware update is available; telnet;
  services (www, ftp, api) open to all addresses; an `admin` user; no
  input drop in the firewall; an unencrypted link.
- **Configuration** `/export terse` — the hub keeps versions and shows a
  diff: whatever changed on the router is visible, whoever changed it.
- **Actions:** reboot, RouterOS update, RouterBOARD firmware, a backup on
  the router.
- **Commands** in RouterOS: on the device page, in scripts
  (`on router run "/ip dns cache flush"`) and in MCP (`device_exec`).

## The router

RouterOS 7 (the REST API exists since 7.1; RouterOS 6 isn't supported).

```
/ip service enable www-ssl
/ip service set www-ssl address=<the plugin host's address>/32
/user add name=nkt group=full password=<password>
```

The `read` group is enough to view (inventory, metrics, findings,
configuration); actions and commands need `full`. The router has its own
certificate: nkt remembers its fingerprint on Check and compares it on
every connection — a substitution won't pass.

## Install and connect

1. About → Plugins → **Install on a host** → MikroTik → the hub machine or
   any host the routers are reachable from. Service
   `nkt-plugin-mikrotik`, 127.0.0.1:9220, no Docker.
2. Hosts → **Add a device** → the “MikroTik RouterOS” type → address,
   user, password → Check → Add.

The hub keeps the routers' logins (encrypted, like the hosts' SSH keys)
and passes them to the plugin in each call; the plugin neither stores nor
logs them.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against a fake RouterOS 7
REST API over HTTPS (no router); `go test ./examples` runs it too.
