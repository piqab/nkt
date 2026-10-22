---
title: MCP for AI agents
---

# MCP: the hub as an AI agent's tools

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
- the **host and group limit** works as in the API: other hosts are out
  of reach, the shared Traffic Fly map is only for a token without a limit;
- the address list, expiry and nkt-edge — as for any token call.

::: warning Actions run without confirmation
With an admin token the agent **runs actions right away**; no person
confirms them. That is intended, so the agent can finish the task. For
an agent you don't trust with actions, issue a read token or a token
limited to some hosts. Everything the agent did is in the hub audit log
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
| `run_script` | admin | run a script (`dry_run`, `values` — answers to its questions) |
| `deploy_pipeline`, `dry_run_pipeline` | admin | deploy, dry run |
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

## Checking by hand

```sh
curl -s -H 'Authorization: Bearer nkt_…' -H 'Content-Type: application/json' \
  https://hub.example.com/api/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_hosts","arguments":{}}}'
```
