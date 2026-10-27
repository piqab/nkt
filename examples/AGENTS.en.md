# Writing an nkt hub plugin — a guide for AI agents

This guide is for an agent (or a person) writing a plugin for the nkt
catalog. Read it whole before the first line of code: it holds the rules
that `nkt-catalog lint`, the tests and CI check, and the ones they can't
check but that must hold anyway.

## 0. Do you need a plugin

- **A device over SSH, an HTTP API or SNMP** that is enough to read with
  commands (a router, a NAS, a printer, a UPS) — **don't write a plugin**:
  describe the device type as a JSON profile of the `connector` plugin
  (the guide “Plugin hosts” → “Connector”, examples — `BUILTIN` in
  [examples/connector/deploy/nkt-plugin.py](https://github.com/piqab/nkt/blob/main/examples/connector/deploy/nkt-plugin.py)).
- **A service with its own API** (Forgejo, LiteLLM, Home Assistant, a
  cloud) or a device needing more logic than a profile — a plugin.

## 1. Where to start

Copy the template — a working plugin with a test:

```sh
cp -r examples/_template examples/<name>
```

Models by kind of plugin:

| Kind | Model |
|---|---|
| A service API: sections, forms, commands, MCP | [litellm](https://github.com/piqab/nkt/tree/main/examples/litellm), [home-assistant](https://github.com/piqab/nkt/tree/main/examples/home-assistant) |
| Devices in Hosts (`provides: hosts`) | [mikrotik](https://github.com/piqab/nkt/tree/main/examples/mikrotik) (HTTPS API), [windows](https://github.com/piqab/nkt/tree/main/examples/windows) (SSH) |
| DNS for the hub (`provides: dns`) | [technitium](https://github.com/piqab/nkt/tree/main/examples/technitium), [cloudflare](https://github.com/piqab/nkt/tree/main/examples/cloudflare), [yandex-cloud](https://github.com/piqab/nkt/tree/main/examples/yandex-cloud) |
| A secret shown once / into a pipeline `.env` (`reveal`, `store`) | [litellm](https://github.com/piqab/nkt/tree/main/examples/litellm) (`key_generate`) |
| The plugin needs Docker, DNS records for the service | [docker-mailserver](https://github.com/piqab/nkt/tree/main/examples/docker-mailserver) |

## 2. The folder

| File | What |
|---|---|
| `deploy/nkt-plugin.py` | the plugin: Python 3, **standard library only** |
| `deploy/nkt-plugin.json` | the package for the catalog and the hub's installer |
| `deploy/test_plugin.py` | a test against a fake — required, without it the plugin stays out of the catalog |
| `README.md`, `README.en.md` | the guide: what it does, what the service side needs, how to install |
| `screens/ru/*.png`, `screens/en/*.png` | screenshots — shown in the hub's catalog and on the site |
| `pipeline*.yaml`, `nkt-example.json` | optional: a pipeline example that deploys the service itself |

`nkt-plugin.json`:

```json
{
  "name": "forgejo", "version": "1.0.0", "min_nkt": "1.16.0", "protocol": 1,
  "title": {"ru": "…", "en": "…"}, "description": {"ru": "…", "en": "…"},
  "run": "python3", "file": "nkt-plugin.py", "port": 9270,
  "docker": false,
  "settings": [
    {"env": "FORGEJO_URL", "title": {"ru": "…", "en": "…"}, "default": "http://127.0.0.1:3000", "hint": {"ru": "…", "en": "…"}},
    {"env": "FORGEJO_TOKEN", "title": {"ru": "…", "en": "…"}, "secret": true, "required": true}
  ]
}
```

- `name` — `^[a-z][a-z0-9-]{1,31}$`; the service is `nkt-plugin-<name>`,
  MCP tools are `<name>_<op>`.
- `version` — X.Y.Z and **exactly** the `VERSION` in the code. Any change
  to the plugin's files is a new version: CI compares with the published
  catalog and won't build it with the same version.
- `min_nkt` — the oldest nkt the plugin works with (the protocol fields it
  uses — see the “Plugins” guide).
- `port` — free among catalog plugins (`lint -all` checks).
- `settings` — the install form; the service's environment variables.
  Names — `A-Z0-9_`, not `NKT_` (the hub's). Anything that looks like a
  token, password or key — `"secret": true`: the hub never shows those.
- `docker: true` — the service needs the docker group (that is root): the
  hub warns the administrator before installing. Don't set it without need.

## 3. The protocol in short

In full — the “Plugins” guide → “Protocol”. A plugin is an HTTP server on
`NKT_PLUGIN_LISTEN:NKT_PLUGIN_PORT` (the hub sets `127.0.0.1` and the
package port).

- **Signature.** Every request: `X-NKT-Timestamp` (Unix seconds),
  `X-NKT-Signature` = hex HMAC-SHA256 of `<timestamp>.<path>.<body>` with
  `NKT_PLUGIN_SECRET`, `X-NKT-Lang` (`ru`/`en`). A timestamp over 300 s off
  or a wrong signature — `401`. Compare with `hmac.compare_digest`.
- `GET /nkt/manifest` — the manifest in the request's language.
- `POST /nkt/call` `{op, args, dry_run, user}` → `{ok, error?, text?,
  result?, view?, reveal?, store?}`. An unknown op —
  `{"ok": false, "error": "no op …"}` with HTTP 200, not an exception.
- Service ops (not in the manifest): `_connect` (the plugin is connected),
  `_check` (parse a script command without doing anything), `_command`
  (run it; `dry_run` — only say what would happen), `_dns_zones` /
  `_dns_sync` (`provides: dns`), `_host_*` (`provides: hosts`).

## 4. The manifest

```json
{"protocol": 1, "name": "…", "title": "…", "version": "…", "description": "…",
 "ops": [{"name": "repos", "title": "…", "read_only": true, "mcp": true, "description": "…"},
         {"name": "repo_create", "mcp": true, "description": "…",
          "input": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}},
         {"name": "repo_delete", "mcp": true, "destructive": true, "description": "…", "input": {…}}],
 "pages": [{"id": "repos", "title": "…", "op": "repos"}],
 "commands": [{"word": "git", "usage": "git repos | git mirror-sync REPO", "example": "…"}]}
```

- **Ops** (`^[a-z][a-z0-9_]{0,47}$`). `read_only` — visible to everyone
  who sees the hub and to read tokens; without it — to an admin or a token
  with the “plugin NAME” action. `destructive` — deletes (agents and tokens
  see it). `mcp` — a `<name>_<op>` tool for agents; an **English
  description** is required — agents read it. `input` — a JSON Schema of
  the arguments. `reveals` — returns a secret (see 5).
- **Pages** — a read-only op that returns a view. `hidden` — opened only
  by an `open` button from another page.
- **Views:** `{"type": "table", "title", "note", "columns": [{key, title}],
  "rows": [{"cells": {key: value}, "actions": [...]}], "actions": [...]}`,
  `{"type": "text", "text": "…"}`, `{"type": "sections", "items": [view, …]}`.
- **Buttons** (`actions`): `op` + `args` + `fields` (a form: `text`,
  `textarea`, `number`, `select` with `options`, `bool`, `password`) +
  `confirm` + `danger`; `open` — a page with `args`; `edit` — editing a
  document with a diff and history on the hub (`get` → `result.text`,
  `put` ← `args.text`, `doc`); `dns` — records the hub sets through the
  DNS plugin.
- **Script commands** — `word` (`^[a-z][a-z0-9-]{1,23}$`, not a word of
  the script language) and `usage`; `_command` runs it with `argv` — the
  words of the command, the word itself included.
- `provides: ["hosts"]` + `host_types` — devices in Hosts: form fields
  (`address` is required; `password` is a secret; the hub fills
  `fingerprint`), `sections`, `actions`, `exec` (the command language),
  `icon` (`mikrotik`, `openwrt`, `ups`, `camera`, `homeassistant`,
  `printer`, `switch`, `device`…). The `_host_*` ops — the “Plugin hosts”
  guide → “Your own device type”.
- `hub_actions` — rights in the hub itself (a token issued to the plugin).
  Ask only if you can't do without: the administrator sees the request.

## 5. Secrets

- **Settings** (`secret: true`) — only the service's environment: the file
  `/etc/nkt-plugins/<name>.env`, `0600`, no history. The plugin reads them
  from `os.environ` and **never** writes them to the log, answers or
  error texts.
- **Device logins** (`provides: hosts`) come from the hub on every call
  (`args.conn`) — don't save them to disk.
- **A new secret for a person** (an API key, a runner token): an op with
  `"reveals": true`, the answer `{"ok": true, "reveal": {"title", "value",
  "note"}}` — the hub shows it once, to the browser window only.
- **A secret for an agent or a script** — only into a pipeline `.env`:
  the `to_pipeline` and `env` arguments (a command — the words
  `to-pipeline P env K`), the answer `{"store": {"pipeline", "env",
  "value"}}`; the hub checks rights and writes it itself. The value never
  goes back to the agent.
- **A registry login and token** (a registry robot) — `env: "@registry"`
  plus `"registry"` (the address) and `"user"` in `store`: the hub writes
  the key into the pipeline's Registries. Example — `examples/rgstr`, the
  tutorial one — `examples/_samples/rgstr-lite` (the article “Writing a
  plugin step by step”).
- The hub's audit log masks arguments named `pass`, `secret`, `token`,
  `key` as `***` — but don't rely on it: don't take secrets as arguments
  without need.
- A password for an external program — through the environment or stdin,
  **never on the command line** (every process on the machine sees it):
  `SSH_ASKPASS` (windows, connector), `-e NAME` for `docker exec`
  (docker-mailserver).

## 6. Security — required

1. **Check every input** before use: a name, a path, an address, a number
   — with a regex or a parser (`ipaddress`, `int`). Only what passes goes
   on. Too long or with control characters — reject.
2. **No shell.** `subprocess.run([...])` with a list, no `shell=True`,
   `os.system`, `eval`, `exec` (`lint` checks). Before positional
   arguments from input — `--` (`ssh … -l USER -- HOST`), the user name as
   a separate argument. External programs — with a timeout.
3. **API paths and addresses** — `urllib.parse.quote(value, safe="")` for
   every path part; the host comes from settings, not from call arguments.
4. **TLS.** To someone else's server with its own certificate — the
   “remember the fingerprint” mode (pin, see mikrotik, connector): the hub
   keeps the fingerprint in the login, the plugin checks it every time.
   Don't turn certificate checks off for the whole process.
5. **Service answers are input too:** JSON — `json.loads`, XML — without
   external entities (the standard `xml.etree`), with a size limit.
6. **Errors.** For a person — `Fail` with a text in their language (no
   secrets, no traceback); the unexpected — `{"ok": false, "error":
   "plugin error: <type>"}`, only the type goes to the log.
7. **Dry run.** Every op that changes something, on `dry_run`, only says
   what it would do: `(dry run) …`. `_check` only parses, calls nothing.
8. **The service log** (`print(..., flush=True)`) — who did what, without
   secret values and request bodies.
9. **Python standard library only**: the hub installs the plugin as a
   file, no pip. Need an external program (ssh, openssl) — check
   `shutil.which` and give a clear error on how to install it.
10. The plugin **starts without settings** (they are set after install)
    and answers a call with a clear “… is not set” error.

## 7. Texts

All UI strings — in the `T` dictionary in `ru` and `en`, the language —
from `X-NKT-Lang`. MCP op descriptions — in English (agents read them).
Russian — plain, with «guillemets»; English — no ASCII `"` inside strings
(use “ ”).

## 8. The test against a fake (`deploy/test_plugin.py`)

The fake is an HTTP server (or ssh, SNMP — as in connector) answering like
the real API **by its documentation**, checking the token. The plugin runs
as a separate process (`python3 -I nkt-plugin.py`), calls are signed.
Check, at least:

- the manifest in both languages;
- every page (view) and form;
- input checks: dangerous values (`../x`, `a b`, `-oProxyCommand=…`,
  `x;rm`) are rejected;
- a dry run changes nothing in the fake;
- a change reaches the fake with the right arguments;
- script commands: `_check` and `_command`;
- a wrong service token — a clear error;
- a wrong signature — `401`;
- the plugin's output has neither the signing secret nor tokens (checked
  in `tearDownClass`).

Add the test to
[examples/plugins_test.go](https://github.com/piqab/nkt/blob/main/examples/plugins_test.go)
(`Test<Name>PluginPython`).

## 9. Checks and the catalog

```sh
python3 -I examples/<name>/deploy/test_plugin.py
go run ./cmd/nkt-catalog lint -dir examples/<name>     # folder, package, code, the live manifest
go run ./cmd/nkt-catalog lint -all examples           # and every plugin's port
go test ./examples ./internal/catalog
```

`lint` starts the plugin without settings and checks the manifest **with
the same function the hub uses when connecting**, the signature (`401`
unsigned and with an old timestamp), the answer to an unknown op, and that
the secret never reaches the log. CI (`.github/workflows/pages.yml`) runs
`lint` and the test of every plugin before building the catalog: a plugin
that fails stays out, the rest are published.

## 10. Checklist before committing

- [ ] `examples/<name>/` from the template; `name`, port, `server_version`, `log` — your own.
- [ ] `version` in the JSON = `VERSION` in the code; a change to the plugin — a new one.
- [ ] Secret settings — `"secret": true`; no secrets in the log, answers, errors.
- [ ] Every input checked before use; no `shell=True`, `os.system`, `eval`.
- [ ] Every changing op honours `dry_run`; `_check` calls nothing.
- [ ] All texts in `ru` and `en`; MCP descriptions in English.
- [ ] The fake test covers section 8 and is called from `examples/plugins_test.go`.
- [ ] `nkt-catalog lint` has no remarks.
- [ ] README.md and README.en.md: what it does, what the service side needs (token rights), how to install, security.
- [ ] Screenshots in `screens/ru/` and `screens/en/` if there is a UI.

## 11. Your own plugin without the catalog

A plugin not meant for the shared catalog — upload an archive of its folder
to the hub: Plugins → Catalog → Upload your own plugin (administrators
only). The hub checks it with the same static `lint`, shows the code and
installs it on hosts like a catalog plugin. An example —
[examples/_samples/url-check](https://github.com/piqab/nkt/tree/main/examples/_samples/url-check).

## 12. Live (optional)

Start the plugin with `NKT_PLUGIN_SECRET`, `NKT_PLUGIN_PORT`,
`NKT_PLUGIN_LISTEN=127.0.0.1` and its settings and connect it to a test
hub: About → Plugins → Connect → host `localhost`, the port, the secret.
Its pages, commands and For agents appear right away.
