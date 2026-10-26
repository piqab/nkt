# Plugin template

A working skeleton for your own nkt hub plugin: for a made-up “items” REST
API — a list section, an Add form, deletion with a confirmation, a
summary, the `example …` script command, MCP tools and a test against a
fake API. Not part of the catalog (the folder name starts with `_`).

The guide is [examples/AGENTS.en.md](../AGENTS.en.md): rules, the manifest
schema, a checklist. In short:

1. Copy the folder: `cp -r examples/_template examples/<name>`.
2. Replace the name (`example` → yours) in `deploy/nkt-plugin.json` and
   `deploy/nkt-plugin.py` (the manifest, `log`, `server_version`), the port —
   one free among catalog plugins (`grep '"port"' examples/*/deploy/nkt-plugin.json`).
3. Describe the settings in `nkt-plugin.json` (secrets — `"secret": true`).
4. Replace `api()` and the views with the real API, texts — in `T` in both languages.
5. Rewrite the fake in `deploy/test_plugin.py` from the API documentation.
6. `python3 -I deploy/test_plugin.py` and
   `go run ./cmd/nkt-catalog lint -dir examples/<name>` — no remarks.
7. README.md and README.en.md, screenshots — `screens/ru/`, `screens/en/`.

Template settings: `EXAMPLE_URL` (API address), `EXAMPLE_TOKEN` (a secret).
