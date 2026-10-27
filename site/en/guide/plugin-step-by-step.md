---
title: 'Writing a plugin step by step'
---

# Writing a plugin step by step

This article builds a working hub plugin from scratch. The plugin is
**`rgstr-lite`**, a tutorial plugin for the
[rgstr](https://github.com/piqab/rgstr) image registry. You can download
it, read it and upload it to your hub:

- archive: [rgstr-lite.tar.gz](https://piqab.github.io/nkt/samples/rgstr-lite.tar.gz);
- source: [`examples/_samples/rgstr-lite`](https://github.com/piqab/nkt/tree/main/examples/_samples/rgstr-lite).

The steps below match the "шаг 1…6" (step 1…6) comments in the code.

The full version of the same plugin is `rgstr` from the catalog. See
[rgstr and rgstroperator in nkt](/en/guide/hub-plugin-rgstr). For the
protocol and the rules, see:

- [Writing a plugin](/en/guide/plugin-authoring);
- [Plugins → Protocol](/en/guide/hub-plugins#protocol-for-your-own-plugins).

## What you get

- A **"Summary"** section: the rgstr version, login state, repository
  count and findings (login off, registry over HTTP).
- A **"Repositories"** section with the "Tags", "Collect garbage" and
  "A robot for a pipeline" buttons.
- A hidden **"Tags"** section where a tag can be deleted, with a
  confirmation and a dry run.
- A **robot**. Its secret goes either once to the window, or straight into
  a pipeline: into `.env` or as the registry key (`@registry`).
- The script commands `rgstr-lite gc` and `rgstr-lite robot …`.
- The MCP tools `rgstr_lite_*` for agents.

The plugin is one Python file of about 330 lines, using only the standard
library. Any language works: the hub only needs an HTTP server.

## The plugin folder

```
rgstr-lite/
  README.md, README.en.md
  deploy/
    nkt-plugin.json     package: name, version, port, settings — for installing on a host
    nkt-plugin.py       the plugin itself
    test_plugin.py      a test against a fake rgstr (the hub does not keep it)
```

The hub reads `nkt-plugin.json` when it **installs** the plugin on a host:

```json
{
  "name": "rgstr-lite", "version": "1.0.0", "min_nkt": "1.17.15", "protocol": 1,
  "title": {"ru": "rgstr (учебный)", "en": "rgstr (tutorial)"},
  "run": "python3", "file": "nkt-plugin.py", "port": 9281,
  "settings": [
    {"env": "RGSTR_URL", "title": {"ru": "Адрес rgstr", "en": "rgstr address"}, "default": "http://127.0.0.1:5000"},
    {"env": "RGSTR_USER", "title": {"ru": "Администратор rgstr", "en": "rgstr administrator"}, "default": "admin"},
    {"env": "RGSTR_PASSWORD", "title": {"ru": "Пароль администратора", "en": "Administrator password"}, "secret": true},
    {"env": "RGSTR_PUBLIC", "title": {"ru": "Адрес реестра для docker", "en": "Registry address for docker"}}
  ]
}
```

- `settings` are the fields of the "Install on host" form. They become the
  service environment: `/etc/nkt-plugins/rgstr-lite.env`, mode `0600`.
- A `secret: true` field is hidden. Its value is never in history or logs.
- `version` must equal `VERSION` in the code; lint checks this.
- The hub also passes `NKT_PLUGIN_SECRET`, `NKT_PLUGIN_PORT` and
  `NKT_PLUGIN_LISTEN` itself.

## Step 1. Server, signature, manifest

The hub makes two kinds of request to a plugin: `GET /nkt/manifest` and
`POST /nkt/call`. Every request is signed:

- `X-NKT-Signature` is an HMAC-SHA256 of `timestamp.path.body` with the
  shared secret;
- `X-NKT-Timestamp` carries the timestamp, which must be no older than
  5 minutes.

```python
def signed(self, body):
    ts = self.headers.get("X-NKT-Timestamp", "")
    try:
        fresh = abs(time.time() - int(ts)) < 300
    except ValueError:
        return False
    want = hmac.new(SECRET.encode(), f"{ts}.{urllib.parse.urlsplit(self.path).path}.".encode() + body,
                    hashlib.sha256).hexdigest()
    return fresh and hmac.compare_digest(want, self.headers.get("X-NKT-Signature", ""))
```

- A request without a valid signature gets `401`, and the plugin does
  nothing.
- A body over 1 MB gets `413`.
- Without a secret of at least 16 characters, the plugin does not start.

**The manifest** says what the plugin can do. From it, the hub builds the
sections, buttons, MCP tools and script commands:

```python
"ops": [
  {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True, "description": "…"},
  {"name": "tag_delete", "title": c.t("delete"), "mcp": True, "destructive": True,
   "input": {"type": "object", "properties": {"repo": s, "tag": s}, "required": ["repo", "tag"]}},
  {"name": "robot_create", "title": c.t("robot"), "mcp": True, "reveals": True, …},
],
"pages": [{"id": "summary", "title": c.t("summary"), "op": "summary"},
          {"id": "repos", "title": c.t("repos"), "op": "repos"},
          {"id": "tags", "title": c.t("tags"), "op": "tags", "hidden": True}],
"commands": [{"word": "rgstr-lite", "usage": c.t("usage"), "example": "rgstr-lite gc"}],
```

| Op field | What it means to the hub |
|---|---|
| `read_only` | anyone can read it, including a read-only token |
| `mcp` | agents see the op as the tool `rgstr_lite_<op>`; a dash in the name becomes an underscore |
| `destructive` | marked as deleting in MCP |
| `reveals` | the reply may carry a secret; a token gets the op only with `to_pipeline`/`env` |
| `input` | the JSON Schema of the arguments; the MCP tool is built from it |

All texts exist in two languages. The language arrives in the
`X-NKT-Lang` header, and the plugin answers in it (`T["ru"]`, `T["en"]`).

## Step 2. A summary section with findings

A section is an op that returns a **view**: a table, text, or `sections`
(several views in a row).

```python
def summary_view(c):
    health = api(c, "GET", "/healthz", auth=False)
    me = api(c, "GET", "/ui/api/me")
    …
    if not me.get("auth_enabled"):
        found.append(c.t("f_auth_off"))
    return {"type": "sections", "items": [table(c.t("summary"), …), table(c.t("findings"), …)]}
```

The data comes from the API of the program the plugin manages. For rgstr
that is `/ui/api/*`:

- sign-in is Basic or Bearer;
- **every** POST needs a JSON body, at least `{}`;
- errors are `{"error": "…"}`.

All of this is visible in the rgstr source, and `api()` follows each
rule. A `401` becomes a clear message: "login rejected — check RGSTR_USER
and RGSTR_PASSWORD".

**Findings** are things an administrator should fix. Phrase them as a
statement of the problem, not as the name of a check: "Login is off:
anyone can push and delete images".

## Step 3. A table and a hidden section

```python
rows = [{"cells": {"repo": r["name"], "n_tags": len(r.get("tags") or [])},
         "actions": [{"title": c.t("open"), "open": "tags", "args": {"repo": r["name"]}}]}
        for r in repositories if REPO_RE.match(r["name"])]
```

- `actions` on a row are its buttons.
- `"open": "tags"` opens the `tags` section with arguments. That section
  is `"hidden": true`, so it is not in the menu and is reachable only from
  a row.
- `actions` on the table are buttons above it. `{"op": "gc_run"}` calls an
  op; with `fields`, a form opens first. This is how "A robot for a
  pipeline" works.

**Everything that comes from outside is validated before use**, even a
repository name from rgstr's own reply. The regular expressions `REPO_RE`,
`TAG_RE` and `NAME_RE` are at the top of the file. A value that fails
validation never reaches a path or a request body.

## Step 4. Changes: confirmation and dry run

```python
{"title": c.t("delete"), "op": "tag_delete", "args": {"repo": repo, "tag": tag}, "danger": True,
 "confirm": c.t("confirm", repo=repo, tag=tag)}
```

- `confirm`: the hub asks "Delete the tag shop/api:1.0?".
- `danger`: the button is red.

Every op that changes something understands `dry_run`. The hub sends it in
a script dry run, from an agent with `dry_run: true`, and from the API.

```python
def tag_delete(c, args, dry):
    repo, tag = check(c, REPO_RE, args.get("repo")), check(c, TAG_RE, args.get("tag"))
    what = c.t("deleted", repo=repo, tag=tag)
    if dry:
        return c.t("would", what=what)        # "(dry run) shop/api:1.0 deleted"
    api(c, "POST", "/ui/api/tags/delete", {"repo": repo, "tags": [tag]})
    return what
```

A dry run validates input exactly like a real call and changes nothing.

## Step 5. A secret: to the window or into a pipeline

rgstr returns a robot's secret once. The plugin neither stores nor logs
it. There are two ways for the secret to go.

**To a person in the window** — a reply with `reveal`. The hub shows the
secret once, with a "Copy" button:

```python
return {"ok": True, "text": c.t("robot_done", name=name),
        "reveal": {"title": c.t("secret", name=name), "value": secret,
                   "note": c.t("note", host=PUBLIC, name=name)}}
```

**Into a pipeline** — a reply with `store`. The hub writes the value
itself and strips it from the reply:

```python
store = {"pipeline": pipe, "env": env, "value": secret}
if env == "@registry":                       # not into .env but as the pipeline's registry key
    store.update({"registry": PUBLIC, "user": name})
return {"ok": True, "text": c.t("robot_done", name=name), "store": store}
```

- **`env` is a variable name.** The value goes into the pipeline `.env`,
  with version history.
- **`env` is `"@registry"`.** The value goes into the pipeline's
  "Secrets" → "Registries" as the key for the registry `registry`, with
  the login `user`.
  - A key with the same address is replaced, and its CA is kept.
  - The pipeline pulls images with this key, so hosts need no
    `docker login`.

Rules the hub enforces on behalf of the plugin:

- `pipeline` and `env` in the reply must match what was **in the request**
  (`to_pipeline`, `env`). The plugin cannot choose the pipeline itself.
- The hub checks the right to write to that pipeline **before** calling
  the plugin.
- A token (API, MCP) gets a `reveals` op only with `to_pipeline` and
  `env`, so an agent never sees the value.
- From a script, the secret goes only into a pipeline, which is why
  `_command` drops `reveal`.

## Step 6. A script command

The hub passes the command words (`argv`) through two service ops:

- `_check` — "Check" on a script. The reply is an error text or `ok`.
- `_command` — execution, with `dry_run` in a dry run. `text` goes to the
  job log.

```python
def command(c, argv, dry, only_check):
    a = argv[1:]                              # argv[0] is the word "rgstr-lite" itself
    if a == ["gc"]:
        return {"ok": True} if only_check else {"ok": True, "text": gc_run(c, dry)}
    if len(a) == 8 and a[0] == "robot" and a[2] == "project" and a[4] == "to-pipeline" and a[6] == "env":
        …
    raise Fail(c.t("usage"))
```

In a script:

```
rgstr-lite gc
rgstr-lite robot ci project shop to-pipeline shop env @registry
deploy shop
```

If no other plugin uses the word, it works on its own. Otherwise write
`plugin rgstr-lite gc`.

## Step 7. A test against a fake

`deploy/test_plugin.py` starts a **fake rgstr**, whose replies follow the
real source, and runs the plugin as a separate process. It then calls the
plugin with signed requests, just like the hub does:

```sh
python3 -I examples/_samples/rgstr-lite/deploy/test_plugin.py
```

It checks:

- the manifest and the sections;
- that bad input is refused (`../x`, `a b`, `x;rm`);
- that a dry run sends rgstr no POST at all;
- `reveal` and `store`, including `@registry`, and that a bad variable
  name is refused;
- `_check` and `_command`, and that `_command` returns no `reveal`;
- `401` without a signature, and an unknown op is refused.

## Step 8. Lint and archive

```sh
go run ./cmd/nkt-catalog lint -dir examples/_samples/rgstr-lite
tar czf rgstr-lite.tar.gz -C examples/_samples rgstr-lite
```

`lint` runs the same check the hub runs on upload:

- the package, and a version equal to `VERSION` in the code;
- ru and en texts;
- secret settings;
- forbidden code: `shell=True`, `os.system`, `eval`, a command as a
  string.

## Step 9. Upload and install

1. "About" → "Plugins" → "Catalog" → **"Upload your own plugin"**, then
   pick the archive.
2. The hub shows the whole code. Read it, tick **"Code reviewed"**, then
   click "Save". The plugin never runs on the hub.
3. Open "rgstr (tutorial)" (marked "own") → **"Install on host"**.
4. Choose a host that can reach rgstr, fill in the settings, and start
   the job. The result is the `nkt-plugin-rgstr-lite` service on port
   9281.
5. The plugin's "Summary" and "Repositories" sections appear. "For
   agents" lists the tools `rgstr_lite_summary`, `rgstr_lite_repos`,
   `rgstr_lite_tag_delete`, `rgstr_lite_gc_run` and
   `rgstr_lite_robot_create`.

A call from the API:

```sh
curl -sS -X POST -H "Authorization: Bearer $NKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"op":"tag_delete","args":{"repo":"shop/api","tag":"1.0"},"dry_run":true}' \
  https://hub.example.com/api/hub/plugins/rgstr-lite/call
```

**A new version:**

1. Raise `VERSION` in the code and `version` in `nkt-plugin.json`.
2. Upload the archive again; the hub shows a diff against the previous
   version.
3. Click "Update on host".

## Next

- [Writing a plugin](/en/guide/plugin-authoring) has all the rules:
  - forms and documents with history (`edit`);
  - DNS records from a plugin;
  - `provides: dns` and `provides: hosts`;
  - `_connect` with a hub token;
  - the security checklist.
- The full `rgstr` (`examples/rgstr/deploy/nkt-plugin.py`) covers the same
  steps 1–6, plus:
  - projects;
  - policies edited as text;
  - webhooks and the audit log;
  - ten findings.
