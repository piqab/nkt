---
title: Hub assistant
---

# Hub assistant

![Assistant](/screens/en/hub-assistant.png)

The **“Assistant”** section in the hub menu (for an admin) is a
conversation with the model about your fleet: “which critical findings are
there and where do I start”, “why did the last job fail”, “which stacks can
move off the busy host”. The model is the same as for [finding
analysis](/en/guide/hub-operations#model-analysis-ai): set up in “About”;
without it the section points to where to turn it on.

## How it answers

The assistant doesn't guess: it **looks at the hub's data** itself — the
host list, a host's findings, services and containers, alerts, jobs and
their logs, pipelines, scripts, moves, clusters, the Traffic Fly map and
state. Above the answer you see what it looked at (“Looked at: list_hosts,
host_findings host_id=1 …”); a red tag is a call that failed.

- **Read only** and **with your rights**: each look is the same request
  the interface makes when you open the section.
- **Addresses and names are hidden**, as everywhere in the hub's AI (the
  “hide addresses and names” box): the model sees `host-1`, `ip-2`, the
  answer shows the real names again. Passwords, keys and tokens are always
  cut out.
- One question takes up to 10 model requests; each counts against the
  daily limit.

## Changes — as a script

The assistant cannot change anything. When an action is needed, it sends
a **script** (an `nkt` block): the hub runs it through “Check” right away
and says under the answer whether it passed; under the block is **“Open as
a script”**: “Profiles” → “Scripts” opens with that text, then “Check”, a
dry run and the run — by you. A stack move (`migrate`) and a deployment
(`deploy`) come the same way.

## A model without tools

Local models can't always call tools. If the provider refuses such a
request, the assistant answers **from a snapshot**: the request carries
the hosts, recent alerts, pipelines and moves, and the answer gets a “from
a snapshot” tag. In this mode the model doesn't see host details
(findings, services).

## The conversation

The conversation lives in the browser tab while it is open; “New
conversation” starts over. Ctrl+Enter sends. Every question goes to the
audit log (`ai.assistant`).

An external agent (Claude Code, Cursor) gets the same data — and actions,
with an admin token — through [MCP](/en/guide/hub-mcp).
