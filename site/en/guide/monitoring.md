---
title: Monitoring
---

# Availability, load, logs, jobs

## Availability

![Availability](/screens/en/availability.png)

Every declared listener and every pool backend is probed on a schedule
(`NKT_PROBE_INTERVAL`, once a minute by default): a TCP connection or an
HTTP request with the right `Host` header. The history turns into:

- a “weekday hour × downtime” heatmap — you see when a service fails
  regularly;
- availability and latency graphs for a period;
- a list of outages with start, end and the error text.

Besides the auto-discovered ones you can add **your own targets** — any
address and port, internal or external.

## Load

![Load](/screens/en/usage.png)

- Load graphs from iptables counters, `docker stats` and nginx/haproxy
  access logs for the chosen period; log entries are sorted by their own
  timestamp, so the graph shows when the load happened, not when it was
  collected.
- A ranking of the busiest resources and a load schedule by hour.
- The **btop** tab — a live `btop` in a terminal window; installed with a
  button if the host lacks it.

## Logs

journald logs by unit and files from `/var/log`, including rotated and
compressed ones. Live following over WebSocket with a filter,
highlighting and autoscroll; the log window can be detached into a
separate browser window.

## Jobs

Everything long — installing packages, renewing a certificate, applying a
profile, creating a machine — runs as a background job with a log, steps
and cancellation. The host job list: kind, step, status, author,
duration; the log is live. A job interrupted by a service restart resumes
or is honestly marked interrupted.

## Audit log

![Audit log](/screens/en/audit.png)

Every change through the UI and the API: who, what, when, with what
result and command output; filters by action and result. The scheduler's
background tasks are here too: interval, last run, how many processed,
errors.
