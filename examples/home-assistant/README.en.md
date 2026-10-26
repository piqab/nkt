# Home Assistant plugin

Home Assistant as an nkt host — one device in Hosts, with no add-ons on
the Home Assistant side: everything goes through its REST API.

- **Inventory:** version, installation type, areas with their entity
  counts, integrations, automations (on or off), device and entity counts,
  available updates, unavailable entities, batteries.
- **Findings:** entities unavailable for over 30 minutes; a battery below
  20 %; an update for the core, OS, an add-on or firmware (`update.*`
  entities); no backup for over 7 days (when the backup integration is on).
- **Configuration** with a diff: which entities (with names), automations
  and integrations exist — without states, so the diff shows what was
  added, removed or renamed.
- **Actions:** restart Home Assistant, install an update (`update.*`), run
  a script (`script.*`), trigger an automation (`automation.*`), reload
  automations.
- **Commands** — calling a Home Assistant service: on the device page, in
  hub scripts (`on home run "light.turn_off {\"entity_id\": \"light.hall\"}"`)
  and MCP (`device_exec`). The form is `domain.service` and, if needed, the
  data as a JSON object.

## Home Assistant

User profile → Security → **Long-lived access tokens** → Create token.
Actions and service calls need an administrator user.

## Install

About → Plugins → Catalog → Home Assistant → **Install on a host** — the
hub machine or any host Home Assistant is reachable from (port 8123). The
service `nkt-plugin-home-assistant`, 127.0.0.1:9260. Then Hosts → **Add a
device** → “Home Assistant” → address, token, link (HTTP or HTTPS with a
remembered fingerprint) → Check → Add.

The hub keeps the token (encrypted) and passes it on each call; the plugin
neither stores nor logs it. Entity names in actions are checked
(`domain.name`, latin letters, digits, `_`), service data is a JSON object
only.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against a fake Home
Assistant REST API; `go test ./examples` and CI run it before building the
catalog.
