---
title: MCP for AI agents
---

# MCP: the hub as an AI agent's tools

::: tip Turned on in “About”
MCP works when “MCP for AI agents” is on in “About” → “Experimental
features” (off by default; when off it answers 403 to any token).
:::

The hub is an [MCP](https://modelcontextprotocol.io) (Model Context
Protocol) server: Claude Code, Claude Desktop, Cursor and other agents see
nkt as a set of tools — hosts, findings, pipelines, scripts, stack moves,
Traffic Fly, clusters — and call them while working on a task (“why did
web-2 go down”, “move forgejo to web-3 and switch the route”).

The address is `POST /api/mcp` (Streamable HTTP transport, one JSON
response). For clients that start the server as a command there is the
[`nkt mcp`](#nkt-mcp-stdio-bridge) bridge.

## Access — API token only

MCP works with an [API token](/en/guide/hub-api#tokens), not with an
interface login. Every tool is a call to the same hub API with the same
token, so the agent's rights are the token's rights, with no separate
rules:

- a **read** token sees only read tools (hosts, findings, services,
  containers, alerts, pipelines, jobs, scripts, moves, clusters, the
  Traffic Fly map and status);
- an **admin** token also gets actions: running a script, deploying,
  moving a stack, Traffic Fly, manifests to clusters, services and
  containers, banning addresses;
- a token **by permission** — only the chosen actions with resource limits
  ([Token by permission](/en/guide/hub-api#token-by-permission)): the agent
  sees only the allowed tools, deployments and machines stay within limits;
- the **host and group limit** works as in the API: other hosts are out
  of reach, the shared Traffic Fly map is only for a token without a limit;
- the address list, expiry and nkt-edge — as for any token call.

::: warning Actions run without confirmation
With an admin token the agent **runs actions right away**; no person
confirms them. That is intended, so the agent can finish the task. For
an agent you don't trust with everything, issue a read token or a token
[by permission](/en/guide/hub-api#token-by-permission): only the needed
actions, its own objects and CPU, memory, disk and machine limits. Everything the agent did is in the hub audit log
as `token:<token name>`.
:::

The hub's built-in AI (finding analysis, AI in the script editor, the
[assistant](/en/guide/hub-assistant)) still
runs nothing by itself — only MCP with an admin token executes.

## Tools

| Tool | Role | What |
|---|---|---|
| `list_hosts` | read | hosts: status, version, problems |
| `host_overview`, `host_findings`, `host_services`, `host_containers` | read | host sections (`host_id`; the hub machine is `-1`) |
| `list_alerts` | read | alerts newer than `after` |
| `list_pipelines` | read | pipelines and their last deployment |
| `job_status`, `job_log` | read | a hub job and its log |
| `list_scripts`, `get_script` | read | saved scripts |
| `list_migrations`, `list_clusters` | read | stack moves, clusters |
| `fly_map`, `fly_status` | read, no limit | Traffic Fly map and state |
| `check_script` | admin | “Check” for a script text, changes nothing |
| `run_script` | admin | run a script (`dry_run`; `dry_first` — a dry run, then the run if it passes; `values` — answers to its questions) |
| `deploy_pipeline`, `dry_run_pipeline` | admin | deploy, dry run |
| `pipeline_power` | admin | stop a deployment or bring up the same version (`action`: `stop` / `start`) |
| `create_vm`, `vm_power` | admin | a machine on a host from a cloud image; start, shut down, force off (a token by permission — its own, within limits) |
| `migrate_preview`, `migrate_stack`, `migration_action` | admin | move plan, move, rollback / removing the old copy |
| `fly_apply`, `fly_ops` | admin, no limit | apply the map, point edits of targets |
| `manifest_diff`, `manifest_apply` | admin | a manifest to clusters |
| `service_action`, `container_action` | admin | start / stop / restart services and containers |
| `fail2ban_ban` | admin | ban / unban addresses on hosts |

Tools carry `readOnlyHint` and `destructiveHint`: clients that honour
them will ask you before a dangerous call themselves.

**Jobs.** Long operations (script, deploy, move, Traffic Fly) return a
`job_id`. With `"wait": true` the tool waits for the job to end (up to 10
minutes by default, `timeout` — up to an hour) and returns the outcome
and the log tail — the agent doesn't have to poll.

## Example: a job with waiting

The agent runs a dry run of a saved script and waits for the outcome in
one call:

```json
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{
  "name":"run_script",
  "arguments":{"script_id":4,"dry_run":true,
    "values":{"param:FROM":"web1","param:TO":"web2"},"wait":true,"timeout":900}}}
```

The answer arrives when the job has finished (or the time ran out — then
`"timed_out": true` and the `job_id` to ask later with `job_status`):

```json
{"jsonrpc":"2.0","id":7,"result":{"isError":false,"content":[{"type":"text",
  "text":"{\"job_id\":123,\"status\":\"succeeded\",\"succeeded\":true,\"log\":[\"…\"]}"}]}}
```

`log` holds the last 200 lines of the job log. A job that ended with an
error comes with `"isError": true` and the error text — the agent sees
what went wrong and can read the whole log with `job_log`.

## An agent at work

A request in Claude Code: “why does the app deployment fail on web-2 and
what do I do?”. The agent calls the tools itself:

1. `list_hosts` — finds `web-2` and its `host_id`;
2. `list_pipelines` — the `app` pipeline and its last deployment;
3. `job_log` — the failed job's log: “port 8080 is taken”;
4. `host_containers` — who holds 8080 on web-2;

and answers with the cause and the fix. With a read token that's all;
with an admin token the agent can stop the extra container itself
(`container_action`) and rerun the deployment (`deploy_pipeline` with
`"wait": true`) — without confirmation, all in the audit log as
`token:<name>`.

## Claude Code

```sh
claude mcp add --transport http nkt https://hub.example.com/api/mcp \
  --header "Authorization: Bearer nkt_…" --header "X-NKT-Lang: en"
```

`X-NKT-Lang` (`en` or `ru`) is the language of the hub's answers (errors,
job logs); the `nkt mcp` bridge sets it from the locale.

## nkt mcp: stdio bridge

Clients like Claude Desktop start an MCP server as a command. The
`nkt mcp` bridge forwards messages from stdin to the hub and the answers
to stdout. The address and token come only from the environment
(arguments are visible in the process list):

```json
{
  "mcpServers": {
    "nkt": {
      "command": "/usr/local/bin/nkt",
      "args": ["mcp"],
      "env": {
        "NKT_HUB_URL": "https://hub.example.com",
        "NKT_API_TOKEN": "nkt_…"
      }
    }
  }
}
```

A hub with a self-signed certificate — `NKT_HUB_CA=/path/to/cert.pem`.
Any `nkt` binary of the same version will do — the agent's machine needs
no hub or service.

## With a local model

MCP doesn't need the hub's model: the agent's model does the thinking,
the hub only provides tools. With a local model any MCP client that works
with Ollama or an OpenAI-compatible address will do — for example Cline
or Continue in VS Code, Goose, Open WebUI. Many of them describe an MCP
server with the same `mcpServers` block as Claude Desktop above (with the
`nkt mcp` bridge), or with the `https://hub/api/mcp` address and an
`Authorization` header; the exact place is in the client's docs. The
agent's model needs tool calling — the same model advice as for the
[assistant](/en/guide/hub-operations#local-and-other-models).

## Checking by hand

```sh
curl -s -H 'Authorization: Bearer nkt_…' -H 'Content-Type: application/json' \
  https://hub.example.com/api/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_hosts","arguments":{}}}'
```
