---
title: n8n
---

# n8n: automation around the hub

There are nkt nodes for [n8n](https://n8n.io): the `n8n-nodes-nkt` package
in the repository's `integrations/n8n` directory. They call the
[hub API](/en/guide/hub-api) with an API token and receive its
[outgoing webhooks](/en/guide/hub-api#outgoing-webhooks).

| Node | What |
|---|---|
| **nkt** | hosts (list, overview, findings, vulnerabilities, services), service and container actions, pipelines (deploy, dry run with selectable checks, waiting for the result, history), hub jobs (state, log, waiting), fail2ban (ban, unban, list), alerts, and any API call the token allows |
| **nkt Trigger** | receives the hub's outgoing webhooks (alerts, deployment outcomes) and checks the signature |
| **nkt Alert Trigger (Polling)** | polls the hub for new alerts when n8n cannot receive webhooks from outside |

## Installation

**From a release**: every [nkt release](https://github.com/piqab/nkt/releases)
has an `n8n-nodes-nkt-<version>.tgz` archive attached (its checksum is in
`SHA256SUMS`). Unpack it into your n8n custom nodes directory and restart
n8n:

```sh
V=1.11.79
curl -fLO https://github.com/piqab/nkt/releases/download/v$V/n8n-nodes-nkt-$V.tgz
mkdir -p ~/.n8n/custom/n8n-nodes-nkt
tar xzf n8n-nodes-nkt-$V.tgz -C ~/.n8n/custom/n8n-nodes-nkt --strip-components=1
```

**From the repository** (Node.js 18+):

```sh
cd integrations/n8n
npm ci && npm run build
npm test        # signatures are checked against the same vectors as the hub's tests
mkdir -p ~/.n8n/custom/n8n-nodes-nkt
cp -r package.json dist ~/.n8n/custom/n8n-nodes-nkt/
```

The node's version is the hub's version; the node works with a hub of
the same or a newer version. The package is not published to npm, so
n8n's "Community Nodes" settings will not find it; these are the ways.

For n8n in Docker, mount the same directory as a volume and restart n8n:

```yaml
services:
  n8n:
    volumes:
      - ./n8n-nodes-nkt:/home/node/.n8n/custom/n8n-nodes-nkt:ro
```

The nodes show up in the n8n search as "nkt", "nkt Trigger" and "nkt
Alert Trigger".

## Credentials

**nkt Hub API**:

- **Hub URL**: the hub address without `/api` (or an edge with the API
  role: `https://api.example.com`);
- **Token**: the `nkt_<key>_<secret>` string the hub showed when the
  token was created ("About" → "API tokens");
- **Authentication**: **Signed Requests** (the default: an HMAC signature,
  the secret never crosses the network, the only way through nkt-edge) or
  **Bearer**;
- "Test" in the credentials window calls `/api/auth/me` and shows the
  token's name and role.

Permissions come from the token: the read role reads and the admin role
also acts; a host and group scope narrows both lists and actions.

## The nkt node

- **Pipeline → Deploy / Dry Run** starts a hub job and, with "Wait for
  Result", waits for it and returns `succeeded`, the job state and the log
  (`log` as lines). A dry run has **Skip Checks**: which checks to skip.
- **Host**: the host list (one item per host) and host sections:
  `overview`, `findings` (one item per finding), `vulnerabilities`,
  `services`. The hub machine is number `-1`.
- **Service / Container**: start, stop, restart (and reload for services).
- **Fail2ban → Ban / Unban**: addresses on every host with fail2ban or on
  the selected ones, waiting for the job outcome.
- **Job**: a hub job by number: state, log, waiting.
- **Alert**: alerts newer than a number (`After ID`).
- **API Call**: any `/api/…` path the token allows, with a JSON body.

## Triggers

**nkt Trigger** (webhook): add the node, copy its Production URL into the
hub ("Alerts" → "Outgoing webhooks" → "New recipient") and paste the
secret the hub shows into the node's **Signing Secret** field. A request
with a wrong signature or older than 10 minutes gets 401 and does not
start the workflow. **Events** chooses which events start it (the
recipient on the hub has its own choice too). An item is the event body:
`kind`, `text`, `host` or `pipeline`, `job_id`, `error` and the delivery
number `delivery`.

**nkt Alert Trigger (Polling)** is for n8n that does not accept requests
from outside: it polls `GET /api/hub/events?after=N` at the n8n interval.
The first run remembers the latest alert and outputs nothing; after that
only new ones, oldest first.

## Examples

`integrations/n8n/examples` has workflows to import into n8n ("Import from
File"):

| File | What it does |
|---|---|
| `alerts-to-telegram.json` | unreachable hosts, findings, failed jobs and deployments to Telegram |
| `nightly-dry-run.json` | a pipeline dry run every night; if it fails, the log goes to Telegram |
| `ban-from-blocklist.json` | every hour, ban the addresses from an external list on every host |

In the examples, replace the pipeline number, the recipient secret and the
chat id, and pick the nkt and Telegram credentials.

## A hub behind NAT

- **n8n in the same network**: the Hub URL is the hub address; the hub
  sends webhooks to n8n directly.
- **n8n in the cloud**: API calls go through an [nkt-edge](/en/guide/edge)
  with the API role (the Hub URL is the edge name, the token has the
  "through nkt-edge" box, Signed Requests only). The hub sends outgoing
  webhooks to the cloud by itself, so no way into the hub is needed for
  them.

## If it doesn't work

| Response | Cause |
|---|---|
| 401 "Invalid API token or request signature" | the token was revoked or replaced ("New secret"), or the Token field lacks part of the string |
| 401 "The request signature is stale" | the n8n and hub clocks differ by more than 5 minutes |
| 403 "The API token cannot call" | the call is closed to tokens or needs the admin role |
| 403 "The host is outside the API token's scope" | the host is not in the token's hosts or groups |
| 403 "not allowed through nkt-edge" | the token lacks the "through nkt-edge" box |
| 401 in the recipient's delivery status on the hub | the secret in the nkt Trigger node is wrong (after "New secret", paste it again) |

## The whole picture

How n8n combines with the bot, the edge and CI into one setup:
[example: automation around the hub](/en/guide/case-automation).
