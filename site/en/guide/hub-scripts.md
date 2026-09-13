---
title: Scripts
---

# Scripts

::: warning Experimental feature
A script does exactly what it says on the hosts, with no undo. “Check” and
“Dry run” first, try it on test machines, review the job log.
:::

![Scripts](/screens/en/hub-scripts.png)

A script is a list of actions in a language of a few commands: create a
group and hosts, install nkt, packages, Docker, stacks, machines. The hub
runs it top to bottom with the same calls as the UI buttons and stops at
the first error. One line — one action; a compose file or a file's text
goes in as a block up to `end`.

```
param ADDR "address of the new server"
group prod profile web-base
host web1 ${ADDR} user root password ask group prod
install web1
wait web1 online 5m
on web1 packages install nginx htop
on web1 service nginx enable
on web1 firewall allow 80/tcp
on web1 firewall allow 443/tcp
wait web1 http http://${ADDR}/ 200 1m
```

## Commands

| Command | What it does |
|---|---|
| `set NAME value`, `set NAME ask` | a variable; `ask` — asked at run time, never stored |
| `param NAME "prompt"` | a parameter asked at run time — one script for different stands |
| `group NAME [profile PROFILE]` | a group on the hub |
| `host NAME ADDR[:PORT] user … (password ask \| password "…" \| key hub) [group …]` | register a host |
| `install HOST…` | install nkt |
| `wait HOST online \| port N \| http URL [CODE] \| service NAME active` | wait |
| `on HOST… packages install\|remove …` | packages; several hosts may be listed in `on` |
| `on HOST service NAME start\|stop\|restart\|reload\|enable\|disable` | services |
| `on HOST firewall allow\|deny PORT [from CIDR]` | firewall |
| `on HOST docker install`, `on HOST docker stack PATH up\|down … end` | Docker and a compose stack |
| `on HOST vm create NAME image IMAGE [cpu N] [mem MB] [disk GB] [profile …] [install]` | a machine on the host |
| `on HOST apply profile PROFILE` | apply a profile |
| `on HOST file put PATH … end` | write a file |
| `on HOST user add NAME [sudo] [key "…"]` | an account |
| `on HOST system hostname\|timezone\|locale\|ntp …` | system settings |
| `on HOST cert issue DOMAIN… \| cert renew DOMAIN` | a Let's Encrypt certificate |
| `on HOST git clone URL /DIR [branch …] [token ask]` | clone a repository |

The full reference with examples you can copy or insert into the editor is
the “Reference” tab in the section itself.

## Check, dry run, run

- **Check** parses the text and verifies host, group and profile names
  without changing anything.
- **Dry run** walks the script: hosts are asked for plans (what would
  change), other steps are logged as “would do”; no hosts or machines are
  created.
- **Run** saves, checks, asks for passwords, parameters and tokens (they
  never go into the text) and starts a hub job. After a hub restart the
  job resumes from the same line; hosts already registered are not
  registered twice.

## Scheme

![Script scheme](/screens/en/hub-script-scheme.png)

The “Scheme” tab draws the script: groups → hosts → actions, machines
inside the host they are created on. The text is what you edit; the
scheme is there to take the script in at a glance.
