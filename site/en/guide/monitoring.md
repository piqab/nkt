---
title: Monitoring
---

# Availability, load, logs, jobs

## Availability

![Availability](/screens/en/availability.png)

On a schedule (`NKT_PROBE_INTERVAL`, once a minute by default) nkt probes
web server listeners and pool backends, published Docker and Podman ports,
forwarded LXD ports, and running LXD instances and libvirt machines by
ping at their address. A probe is a TCP connection, an HTTP request with
the right `Host` header, or a ping. The history turns into:

- a “weekday hour × downtime” heatmap — you see when a service fails
  regularly;
- availability and latency graphs for a period;
- a list of outages with start, end and the error text.

Besides the auto-discovered ones you can add **your own targets** with
"+ target": ping, TCP, HTTP or HTTPS to any address, internal or
external. Scans never touch your own targets; you delete them from the
list.

## Load

![Load](/screens/en/usage.png)

- Load graphs from iptables counters, nginx/haproxy access logs and the
  load of containers and machines for the chosen period: for network, CPU
  and memory you pick the source next to the metric — Docker, Podman, LXD
  or Libvirt (`virsh domstats`); log entries are sorted by their own
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

![Job window](/screens/en/job.png)

Everything long — installing packages, renewing a certificate, applying a
profile, creating a machine — runs as a background job with a log, steps
and cancellation. The host job list: kind, step, status, author,
duration; the log is live. A job interrupted by a service restart resumes
or is honestly marked interrupted.

Jobs also cover installing and removing apt, snap and flatpak packages,
the system upgrade, installing engines (LXD, Podman), btop, tmux, ufw and
firewalld, creating LXD instances and Podman containers, downloading LXD
images and changing guest passwords. The button opens the job log window
right away with percentages (apt's come from its status); a closed window
reopens from "Jobs". Through the hub it is the same: these are the host's
own jobs.

## Audit log

![Audit log](/screens/en/audit.png)

Every change through the UI and the API: who, what, when, with what
result and command output; filters by action and result. The scheduler's
background tasks are here too: interval, last run, how many processed,
errors.
