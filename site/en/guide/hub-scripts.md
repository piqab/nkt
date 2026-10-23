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

Where: **"Profiles" → the "Scripts" tab**. "New script" opens the editor
window; **"Ready-made scripts ▾"** in it inserts an example whole. Then — a
name, "Save", "Check", "Dry run", "Run".

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

Every command with its syntax, arguments and examples, plus ready-made
scripts, is on the **[Script commands](/en/guide/script-commands)** page.
It is built from the same command descriptions in nkt code that “Check”,
the “Reference” tab and AI work from, so it always matches what the hub
understands. In short:

- variables and questions at start: `set`, `param`;
- the hub: `group`, `host`, `install`, `wait`, `wait url`;
- on a host (`on HOST… …`): packages, services, firewall, Docker and
  compose stacks, machines, profiles, files, accounts, system settings,
  certificates, `git clone`, Kubernetes clusters;
- deployments and moves: `pipeline`, `deploy`, `migrate`;
- clusters and Traffic Fly: `k8s … ingress|apply|wait`, `fly route`, `fly apply`.

![The “Reference” tab: the “Script commands” page from the site](/screens/en/hub-script-help.png)

Command help is in the editor too: the “?” icon in the window title and
the “Reference” tab on the scripts page (the same site page; without
access to the site — the help built into nkt); ready-made scripts —
“Ready-made scripts ▾” in the editor window.

## AI in the editor

When model analysis is on in "About", the editor window has an **"AI ▾"**
button:

- **"Explain the selection"** (with no selection — the whole text): what each
  step does on the hosts, what changes and what is risky;
- **"Write from „# AI: …“ lines"**: a line `# AI: install nginx on web1 and
  open 443` is a task; the model writes commands under it and leaves the rest
  alone;
- **"Script from a task description"**: a "what to do" field → a whole script.

The model sees the language reference (the same as the "Reference" tab) and
the **names** of hosts, groups, profiles, pipelines, clusters and gateways —
no addresses, passwords or secrets; with "anonymize" in the AI settings host
names go out as aliases. The answer goes through "Check" right away; errors
go back to the model for one more attempt, the rest are shown by line. The
new text gets into the editor only through a diff window ("Insert into the
editor"); **AI does not run the script** — then the usual "Save", "Dry run",
"Run".

Example. The editor has one task line:

```
# AI: install nginx on web1 and open 443
```

"AI ▾" → "Write from „# AI: …“ lines" — the diff window shows commands
under the line, the task line stays a comment:

```
# AI: install nginx on web1 and open 443
on web1 packages install nginx
on web1 service nginx enable
on web1 firewall allow 443/tcp
wait web1 port 443 1m
```

Under the diff is the "Check" result (no errors here); "Insert into the
editor" — and on as with any text. The script-from-a-task item does the
same for the whole script: "nginx on web1 and web2, ports 80 and 443, wait
for 80 on web1" → a whole script.

## Through the API and n8n

A saved script also runs without the UI — with an admin API token
(`POST /api/hub/scripts/{id}/run` with the start answers and `dry_run`) or
the n8n node **Script → Run**. A token cannot create or edit scripts — only
here, with version history. A token limited by hosts runs only a script
entirely on its hosts. Details — [Hub API](/en/guide/hub-api#what-a-token-can-call),
[n8n](/en/guide/n8n).

## Check, dry run, run

![An assistant draft in the editor: name, note and check are filled in](/screens/en/hub-script-editor.png)

- **Check** parses the text and verifies host, group and profile names
  without changing anything. The button is in the editor window too: the
  result is under the text, errors by line. It also catches a block closed
  in the wrong place: if script commands (`on …`, `wait …`) ended up in a
  block's YAML — “`end` is misplaced”.
- **Save** in the editor window is available when the script has a name
  and the current text passed “Check”; any edit needs a new check. Next to
  the disabled button it says what is missing.
- **A draft from AI** (“Open as a script” in an assistant answer or a
  finding analysis) opens with a Latin name (`ai-20261023-1532-web-1` or
  `fix-web-1-20261023-1532`), a note “Suggested by the assistant: …” or
  “From a finding analysis: …” and already checked — “Save” is available
  right away. Such scripts carry an “AI” tag in the list. A downloaded
  script's file name is in Latin letters.
- **Dry run** walks the script: hosts are asked for plans (what would
  change), other steps are logged as “would do”; no hosts or machines are
  created.
- **Run** saves, checks, asks for passwords, parameters and tokens (they
  never go into the text) and starts a hub job. After a hub restart the
  job resumes from the same line; hosts already registered are not
  registered twice.

## Version history

![A script's version history: the difference with the previous one](/screens/en/hub-script-history.png)

Each save is a new revision. “History” (on the script card and in the
editor window) is a table: No., time, author, change note, size, a
“current” label. Each revision has:

- **“Content”** — the revision's whole text, read only;
- **“Difference”** — a diff with the current or the previous revision
  (a switch above the diff);
- **“Back to the editor”** — the revision's text goes into the editing
  window; nothing changes on the hub until you press “Check” and “Save”
  (with a diff before writing) — so a rollback is a new revision too.

Profile and service description histories work the same way.

## Scheme

![Script scheme](/screens/en/hub-script-scheme.png)

The “Scheme” tab draws the script: groups → hosts → actions, machines
inside the host they are created on. The text is what you edit; the
scheme is there to take the script in at a glance.
