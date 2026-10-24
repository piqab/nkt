---
title: Plugins
---

# Plugins

A plugin is a separate program that adds its own sections, script
commands and MCP tools to the hub. Usually it is a container deployed by
an ordinary pipeline: updated and removed by the pipeline too, while the
hub only connects to it. Without connected plugins the hub works as
before.

<!-- plugin-dns -->

## How it works

- The plugin listens for HTTP on **127.0.0.1** of its host (in compose —
  `127.0.0.1:9180:9180`): it is not visible from outside.
- The hub reaches it **over the SSH connection to the host** (a plugin on
  the hub machine — directly). A host connected only through a tunnel
  (no SSH) doesn't suit a plugin.
- Every call is **signed** with a shared secret: `NKT_PLUGIN_SECRET` from
  the plugin pipeline's `.env`. The hub keeps it encrypted; it goes to the
  export together with the hub's other secrets.
- The plugin describes itself with a **manifest**: sections, operations,
  script commands, MCP tools, what it does for the hub and which rights it
  asks of the hub.

## Connect

1. Deploy the plugin with a pipeline (an example is in its guide). In the
   pipeline's `.env` — `NKT_PLUGIN_SECRET: generate:hex32` and, if the port
   isn't 9180, `NKT_PLUGIN_PORT`.
2. About → **Plugins** → Connect → From a pipeline, pick the pipeline. The
   hub takes the host, port and secret from it.
3. **Check** — the hub shows what the plugin adds: sections, script
   commands, MCP tools, what it does for the hub and which rights it asks
   for. Clashes (a script word or a tool already exists) are shown there
   too.
4. **Connect**. A Plugins section appears in the hub menu.

“By hand” — host, port and secret without a pipeline (a plugin installed
outside nkt). Disconnect removes the sections, commands and tools from the
hub and revokes the plugin's token; the plugin itself and its data stay on
the host.

## Rights

| Who | What |
|---|---|
| Anyone signed in, a read token | the plugin's sections and read-only operations |
| An administrator | everything the plugin can do |
| A token by permission | operations that change things — with the “plugin NAME” action in the token window |
| The plugin itself | calls to the hub — with a token by permission holding only the actions it asked for (`hub_actions`) and you confirmed when connecting |

Every plugin action (an operation, a document edit, a script command) is
in the hub's audit log: `plugin.op`, `plugin.doc`, `plugin.command`, the
connection — `plugin.connect`.

## Sections

A section is what the plugin shows: tables, text, buttons. Buttons call
the plugin's operations (with a form and a confirmation), open another
section (“open zone”) or **edit a document** — a text (a zone file, for
example) in the same window as everything else in nkt: a diff before
writing and a version history on the hub. If the document changed while
you were editing it, the write is refused and the hub shows the new text.

## Scripts

A plugin command is written as

```
plugin NAME WORD args…
```

or as one word, unless another plugin took it (`dns set …`). Check asks
the plugin whether it is written correctly; in a dry run the plugin only
says what it would do. Help on the words is in the plugin's section and in
[script commands](/en/guide/script-commands#cmd-plugin).

## MCP

Operations the plugin marked for agents are tools `<name>_<operation>`
(`dns_zones`, `dns_record_set`). A read token sees only read operations,
others need the “plugin NAME” action. Disconnect the plugin and the
tools are gone from `tools/list`.

## Protocol (for your own plugins)

Version 1. A plugin is an HTTP server in any language.

**Signature.** Every hub request carries headers:

- `X-NKT-Timestamp` — Unix time in seconds;
- `X-NKT-Signature` — hex HMAC-SHA256 of `<timestamp>.<path>.<body>`
  with the secret (`<path>` — `/nkt/manifest` or `/nkt/call`, a GET body
  is empty);
- `X-NKT-Lang` — `ru` or `en`: the plugin returns its texts in it.

A timestamp older than 5 minutes or a wrong signature — answer `401`.

**`GET /nkt/manifest`:**

```json
{
  "protocol": 1,
  "name": "dns",
  "title": "Technitium DNS",
  "version": "1.0.0",
  "description": "…",
  "ops": [
    {"name": "zones", "title": "Zones", "read_only": true, "mcp": true,
     "description": "List zones", "input": {"type": "object", "properties": {}}},
    {"name": "record_set", "mcp": true, "destructive": false,
     "input": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}
  ],
  "pages": [{"id": "zones", "title": "Zones", "op": "zones"}],
  "commands": [{"word": "dns", "usage": "dns set NAME TYPE VALUE [ttl SEC]"}],
  "provides": ["dns"],
  "hub_actions": []
}
```

- `name` — Latin letters, digits, hyphen (2–32); operations and sections —
  Latin letters, digits, `_`. A section refers to a read-only operation.
- `commands` — script words; the language's own words (`on`, `fly`,
  `deploy`…) can't be taken.
- `provides: ["dns"]` — the plugin keeps DNS records for the hub (below).
- `hub_actions` — token-by-permission actions (`pipeline.deploy`,
  `fly.manage`…) the plugin asks of the hub.

**`POST /nkt/call`** — `{"op": "…", "args": {…}, "user": "admin",
"dry_run": false}` → `{"ok": true, "text": "…", "result": …, "view": …}`
or `{"ok": false, "error": "a text for a person"}`.

A section's **view** (`view`):

```json
{"type": "table", "title": "Zones", "note": "…",
 "columns": [{"key": "zone", "title": "Zone"}],
 "rows": [{"cells": {"zone": "example.com"},
           "actions": [{"title": "Records", "open": "records", "args": {"zone": "example.com"}},
                       {"title": "Zone file", "args": {"zone": "example.com"},
                        "edit": {"get": "zone_text", "put": "zone_import", "doc": "example.com"}},
                       {"title": "Delete", "op": "zone_delete", "args": {"zone": "example.com"},
                        "confirm": "Delete zone example.com?", "danger": true}]}],
 "actions": [{"title": "Add zone", "op": "zone_add",
              "fields": [{"name": "zone", "title": "Zone", "required": true}]}]}
```

`type` — `table`, `text` (`text`) or `sections` (`items` — views one after
another). Form fields: `text`, `textarea`, `number`, `select`
(`options`), `bool`. A document: the `get` operation returns
`result.text`, `put` receives `args.text`; the hub keeps the version
history.

**Service operations** (not in `ops`, the hub calls them itself):

| Operation | Arguments | When |
|---|---|---|
| `_connect` | `name`, `hub_url`, `token` | on connecting (a token — if `hub_actions` were asked); not needed — answer with an error mentioning `_connect` |
| `_check` | `argv` | Check of a script with a plugin command: an error as text |
| `_command` | `argv`, the request's `dry_run` | running a script command; `text` — lines for the job log |
| `_dns_zones` | — | `provides: dns`: `{"result": {"zones": ["example.com"]}}` |
| `_dns_sync` | `owner`, `records`, `previous` | `provides: dns`: bring the owner's records (`fly:<route>`, `site:<domain>`) to `records`, removing whatever of `previous` isn't in `records` |

`argv` — the words of the script command starting with the command word:
`dns set app.example.com A 203.0.113.10` → `["dns", "set", "app.example.com", "A", "203.0.113.10"]`.

## Your own plugin: the shortest one

Python without dependencies, one section and one command:

```python
import hashlib, hmac, json, os, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SECRET = os.environ["NKT_PLUGIN_SECRET"].encode()

def signed(h, path, body):
    ts = h.headers.get("X-NKT-Timestamp", "0")
    want = hmac.new(SECRET, f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
    return abs(time.time() - int(ts)) < 300 and hmac.compare_digest(want, h.headers.get("X-NKT-Signature", ""))

MANIFEST = {"protocol": 1, "name": "hello", "title": "Hello", "version": "0.1",
            "ops": [{"name": "info", "read_only": True, "mcp": True, "description": "Say hello"}],
            "pages": [{"id": "info", "title": "Hello", "op": "info"}],
            "commands": [{"word": "hello", "usage": "hello NAME"}]}

class H(BaseHTTPRequestHandler):
    def reply(self, obj, code=200):
        raw = json.dumps(obj).encode()
        self.send_response(code); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def do_GET(self):
        if not signed(self, self.path, b""): return self.reply({"error": "signature"}, 401)
        self.reply(MANIFEST)
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if not signed(self, self.path, body): return self.reply({"error": "signature"}, 401)
        req = json.loads(body)
        if req["op"] == "info":
            return self.reply({"ok": True, "view": {"type": "text", "text": "Hello from the plugin"}})
        if req["op"] in ("_check", "_command"):
            argv = req["args"]["argv"]
            if len(argv) != 2: return self.reply({"ok": False, "error": "hello NAME"})
            return self.reply({"ok": True, "text": f"hello, {argv[1]}" + (" (dry run)" if req.get("dry_run") else "")})
        self.reply({"ok": False, "error": f"no op {req['op']}"})

ThreadingHTTPServer(("0.0.0.0", 9180), H).serve_forever()
```

In compose — `ports: ["127.0.0.1:9180:9180"]` and the secret from `.env`.
The full example is the Technitium DNS plugin in
[examples/technitium](https://github.com/piqab/nkt/tree/main/examples/technitium).
