---
title: Alerts, jobs, AI
---

# Alerts, jobs, model analysis

## Alerts

![Alerts](/screens/en/hub-alerts.png)

The hub notices transitions itself: a host is **down** / **back**,
**rebooted** (its uptime dropped, which only happens after a boot),
**serious findings** appeared / **were fixed**, **a job failed**. An alert
log with settings for what to record and what to notify about; short
episodes (“down” → “back” a couple of minutes later) collapse into one
line “was down for N min”.

The **“Notify on problems”** switch turns on browser notifications — they
arrive in any hub section while the tab is open. The unread counter is on
the menu item.

## Hub jobs

Installing and updating nkt on a host (including “update all”), creating
a machine, applying a profile to a group, a script, a cluster — all of
these are hub jobs with a live
log; a job interrupted by a hub restart resumes. A failed job has **“try
again”**: a new job with the same parameters continues from the saved
state (created machines, the tunnel, installed roles are skipped), the
old log stays. Job logs, titles and errors, like alerts, are shown **in
the reader's language** — a job started from a Russian UI reads in
English for an English user; raw tool output stays as is.

A job that resumes after a service restart remembers how many times it
was resumed: if the service restarted in the middle of it more than
three times, it stops with an error instead of looping. An error inside
a job runner doesn't crash the service — the job ends with "internal
runner error".

## Export, updates, cache

- Hub export and import — on the [Hosts](/en/guide/hub-hosts#export-and-import) page.
- Updating the hub and hosts, the beta channel, the vulnerability and
  ClamAV databases — [Updates](/en/guide/hub-updates).
- The package, file and image cache — [Package cache](/en/guide/hub-cache).

## Model analysis (AI)

A card in "About" — one setting for the whole installation: the provider
(**Anthropic** or **OpenAI-compatible**, local ones included — Ollama,
vLLM, LM Studio), address, model, key. The key is stored encrypted on
the hub and never handed out; requests go from the hub — hosts need no
internet. Off by default.

The **Test** button sends the model a short probe using whatever is in
the form right now: a wrong address or key shows up immediately.
**Answer wait time** is how long to wait for the model (90 s by default;
a local model on a weak machine needs 300+); the analysis window shows
a running seconds counter.

When on, a bulb button appears in the row of a finding (including
"What's broken" on the overview), a vulnerability (packages and images),
a malware hit (both heuristic hits and ClamAV findings), an alert and
next to a job error: what it means, why it
matters here, what to do — with commands. Commands are only shown; you
apply them.

The "Resource map" page gets an **architecture review**: single points
of failure, needless exposure, inconsistencies, where to start; on the
hub — across all hosts at once. Reviews are kept with their date.

The answer stays with the finding: a bulb without an answer is outlined
blue, one with an answer is **orange** and opens the saved answer
without a request ("ask again" and "delete answer" sit under the
answer). The same finding already analysed on another host is **filled
blue**: that answer is shown first with a note where it
came from, and a request for this host is a separate button.

**Model instructions** (prompts) are edited in the same card: one for
finding analysis and one for the architecture review, in Russian and
English; saving goes through a window with a diff against the default,
"restore default" removes the edit.

In "Configs" every open file (any service) and the selected program have
a bulb: "what is configured / what to fix / example", with a field for a
task-specific question. Passwords, keys, tokens and password hashes are
always cut from the text.

When writing a configuration fails (validation or apply), the failure
banner carries the same bulb: the model gets the check output and the
diff of the edit, with the task to explain and show a corrected
fragment. Passwords, tokens and keys are always cut from the request,
regardless of the checkbox below.

The "hide addresses and names" checkbox (on by default) replaces host
names, IPs, domains and e-mail with aliases before sending and puts them
back in the answer; "show request" shows exactly what left.  Answers are
cached and spending is capped by a daily limit.

## Privacy mode

The **“hide sensitive data”** checkbox in “About” — for
screen sharing and screenshots: addresses and names of hosts and
machines, users, keys and tokens, domains, cluster API addresses, IP/MAC
are blurred on every page and in modal windows; in logs, alerts, findings
and audit — addresses, e-mail, domains and the host names from the list;
terminal, logs and topology — as a whole. Nothing shows on hover — turn
the mode off to read. The mode mark is the orange “nkt” badge in the
header; the state is remembered in the browser.
