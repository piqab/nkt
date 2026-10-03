---
title: "Example: automation around the hub"
---

# Example: a hub behind NAT, n8n, a bot and CI

An end-to-end example of how everything around the hub fits together:
deployment webhooks, API tokens, nkt-edge with roles, n8n, the Telegram
bot and a check from CI. Each step links to its own page with details;
this page shows how they combine and in which order to set them up.

## What we have and what we want

**We have:**

- **the hub**: a mini PC in the office behind NAT, not exposed;
- **web1** and **db1**: VPSes in the `prod` group; web1 runs the shop, a
  compose stack with the `shop.example.com` site;
- **gate**: the cheapest VPS with a public address and the name
  `gate.example.com` (for nkt-edge);
- **n8n** in the cloud and a **team chat** in Telegram;
- the shop repository on GitHub, with images built by GitHub Actions.

**We want:**

1. a tag push → CI builds the image, **checks it with a dry run** and only
   then deploys;
2. a host goes down, new findings, a failed deployment → **a chat message
   with buttons** "Overview", "Log", "Retry";
3. every night, **a dry run** with all checks; if it fails, the log goes
   to the chat;
4. every hour, **ban the addresses from an external list** on every host;
5. a failed deployment → **an issue in GitHub**;
6. the shop's ports and certificate are checked **as the internet sees
   them**.

## The layout

```
GitHub Actions ──HTTPS──▶ gate: nkt-edge ◀── tunnel 8444 ── hub (behind NAT)
n8n (cloud)   ──HTTPS──▶   roles: webhooks, API,             │  │
                           outside checks                    │  └─SSH─▶ web1, db1
                                                             │
hub ──HTTPS──▶ api.telegram.org   (bot: the hub polls)       │
hub ──HTTPS──▶ n8n (cloud)        (outgoing webhook)  ◀──────┘
```

**Only gate** is exposed (443, 80 for certbot, 8444 for the tunnel). The
hub accepts no incoming connections at all: it reaches Telegram and n8n
by itself, and everything from outside comes through the tunnel the hub
keeps to the edge.

## Step 1. nkt-edge on gate

1. Add gate to the hub as a regular host (SSH is enough).
2. "Deployments" → **nkt-edge** → "Install on a host": host gate, name
   `gate.example.com`, roles **webhooks**, **API** and **outside
   checks**.
3. The job issues the certificate, installs the service and connects the
   hub; the card shows "connected" and the webhook and API addresses.

Details: [nkt-edge](/en/guide/edge#roles-and-several-edges). The API and
webhooks can be split across separate edges (different VPSes and names),
so compromising one VPS does not touch the other role.

**Check:** "Check from outside" on the edge → `shop.example.com`, ports
`80, 443`, HTTPS: DNS points at web1, both ports are `open`, the
certificate is valid. From now on the "Sites" wizard and the dry run also
check the site from the edge (marked "checked from outside via nkt-edge
gate.example.com").

## Step 2. The shop pipeline

"Deployments" → "New pipeline" → a compose stack on web1 with a `site:`
block (`shop.example.com`), as in the [examples](/en/guide/hub-deploy).
Run "Dry run" once by hand and pick the check boxes: the choice is
remembered on the pipeline, and the nightly run and the bot use the same.

The pipeline's "Webhook" → the **"Via edge"** tab gives the
`https://gate.example.com/hooks/…` address and the secret; put them into
GitHub secrets (`NKT_HOOK_URL`, `NKT_HOOK_SECRET`) as in the [CI/CD
examples](/en/guide/cicd-examples#github-actions).

## Step 3. Tokens: one for CI, one for n8n

"About" → **"API tokens"** → "New token": two separate ones, so each can
be revoked on its own:

| Token | Role | Scope | Through edge | Expiry |
|---|---|---|---|---|
| `ci` | admin (a dry run is an action) | host web1 | yes | 365 days |
| `n8n` | admin (bans, dry runs) | group `prod` | yes | 365 days |

Copy each `nkt_…` string right away; it is shown only once. The scopes
keep the tokens in bounds: `ci` reaches nothing but web1's pipelines, and
`n8n` nothing but the `prod` hosts. Details: [API and
tokens](/en/guide/hub-api).

## Step 4. CI: a dry run before deploying

In the GitHub Actions workflow, before calling the webhook, add the
["Dry run on the hub"](/en/guide/cicd-examples#a-dry-run-from-ci-before-deploying)
step: `NKT_URL=https://gate.example.com`, and the `ci` token's key and
secret from its `nkt_<key>_<secret>` string. If the dry run fails, the
step fails, the webhook is not called, and the check log is in the CI
output.

## Step 5. The Telegram bot

1. [@BotFather](https://t.me/BotFather) → `/newbot` → token.
2. "Alerts" → "Settings" → **"Telegram bot"** → "Configure": the token; then add the
   bot to the team chat and send `/start`, and it answers with the chat
   number.
3. The chat: role **admin**, alerts on. "Who may act": the on-call
   people's ids (the bot tells each one theirs on `/id`). Events:
   "unreachable", "new findings", "job failed", "deployment failed".

The chat now gets alerts with "Overview", "Findings", "Log" and "Retry"
buttons, and the commands `/status`, `/hosts`, `/deploy shop` (after the
"Deploy" button) and `/ban 198.51.100.7`. Details: [Bots](/en/guide/bots).

## Step 6. n8n: the nightly run and list bans

1. Put the nkt nodes from the release archive into your n8n custom nodes
   directory ([installation](/en/guide/n8n#installation)). For a cloud n8n
   without custom nodes, use n8n on your own server or the "HTTP Request"
   node with the signature from the
   [example](/en/guide/hub-api#presenting-the-token).
2. **nkt Hub API** credentials: Hub URL `https://gate.example.com`, the
   `n8n` token string, Signed Requests. "Test" shows "Token "n8n" (admin)".
3. Import from `integrations/n8n/examples`:
   - `nightly-dry-run.json`: set the shop pipeline number and the chat;
   - `ban-from-blocklist.json`: set your list's address.

## Step 7. A failed deployment → a GitHub issue

1. In n8n, a workflow: **nkt Trigger** (events: "Deployment Failed") →
   **GitHub: Create Issue**: an expression with `$json.pipeline.name` in
   the title, and `$json.error`, `$json.tag` and a link to the hub in the
   body.
2. Put the trigger's Production URL into the hub: "Alerts" → "Settings" → **"Outgoing
   webhooks"** → "New recipient", event "deployment failed". Paste the
   secret the hub shows into the trigger's Signing Secret field.
3. "Test" on the recipient: n8n receives a `test` event (the trigger skips
   it since the event is not selected) and answers 200.

The hub sends to n8n by itself, so no way in is needed for this. Details:
[outgoing webhooks](/en/guide/hub-api#outgoing-webhooks).

## The result

| Event | What happens |
|---|---|
| a tag push | CI builds the image → a dry run through the edge (`ci` token) → a webhook through the edge → a deployment; the outcome goes to the chat |
| a deployment fails | a chat message with "Log" and "Retry"; a GitHub issue through n8n |
| web1 is unreachable | a chat message with "Overview"; another when it is back |
| at night | n8n runs the shop's dry run (`n8n` token); if it fails, the log goes to the chat |
| every hour | n8n bans the list's addresses on the `prod` hosts |
| the sites wizard, a dry run | ports and the certificate are checked from gate, as the internet sees them |

## Check it end to end

- [ ] the edge is "connected" with the webhooks, API and outside checks
      roles;
- [ ] "Check from outside": both ports `open`, the certificate valid;
- [ ] the bot chat's "Test message" arrived;
- [ ] the n8n credentials "Test" shows "Token "n8n" (admin)";
- [ ] "Test" on the outgoing webhook answers 200;
- [ ] a tag on a test branch goes all the way to the chat message.

## What is exposed, and what if…

- **gate is compromised.** It holds no secrets: webhooks are signed, the
  API takes only signed requests (the token secret never passes through
  the edge), and signatures work once. Forget the edge and install a new
  one; tokens and pipeline secrets do not need changing.
- **The `n8n` token leaks.** "Revoke" in "API tokens" takes effect at
  once; until then it reaches only the `prod` hosts and only automation
  calls, without the terminal, files or hub management. Every call is in
  the audit log as `token:n8n`.
- **Someone presses "Retry" in the chat.** It works only for an on-call
  person on the list and only after the confirmation button; the audit
  log shows `telegram:<name>`.
