---
title: Updates and vulnerability DB
---

# Hub updates and the vulnerability database

![About](/screens/en/hub-about.png)

The hub's **"About"** section — the hub's own version, updates, the
shared vulnerability database, the package cache, the ClamAV database,
model analysis and privacy mode. Host versions are on the "Hosts" page.

## Version check

Every `NKT_HUB_UPDATE_CHECK_INTERVAL` (6 h) the hub asks GitHub Releases
(`NKT_HUB_RELEASE_REPO`) whether there is a newer version and shows a
badge in the menu — **for information only**: nothing is installed by
itself. "Check again" checks right away.

Below — the **release notes** in the UI language (from `WHATSNEW.md` and
`WHATSNEW.en.md`). While there is no update, it shows the notes of the
**installed** version — useful to read after an update.

## Update the hub

"Update to vX.Y.Z":

1. downloads the binary for this machine's architecture and the
   `deploy/netknownsthat-hub.service` of the same tag, verifies
   `SHA256SUMS`;
2. puts the binary into `/usr/local/bin/nkt`, the unit into
   `/etc/systemd/system/`, then `daemon-reload` and `restart` — via
   `systemd-run` in a separate unit, because the hub's own
   `ProtectSystem=strict` doesn't let it write there;
3. the hub is unavailable for a few seconds; the open page polls
   `/api/health` and reloads itself.

There is a **rollback** to the previous version if something goes wrong.

The update doesn't touch `hub.env` — unlike hosts' `nkt.env`, which the
hub regenerates on every install, it is entirely up to the operator.

The button is there only if the hub runs as a systemd unit
(`INVOCATION_ID` is in the environment). Otherwise there is an
explanation instead:

- **Docker / Kubernetes** — the binary is in the image layer: update with
  `docker compose pull && docker compose up -d` or a new image tag;
- **a binary started by hand** — install the hub as a service
  ([installation](/en/guide/install-hub)) and the button appears.

## Beta versions

A `vX.Y.Z-beta` tag builds the same release marked pre-release: a binary
with version `X.Y.Z-beta`, images `:X.Y.Z-beta` and `:beta` (`:latest` is
not touched). The **"use beta versions"** checkbox switches the hub to
the beta channel: betas count as updates, "latest available" is rechecked
right away. Without the checkbox a hub on a beta updates to the stable
build of the same version once it's out. Hosts follow the hub's version —
a beta hub hands out the beta. A beta is marked with a "beta" badge in
the header and in "About".

## Hosts after a hub update

A host whose version differs from the hub is updated first when opened;
"update all" in the host list handles all outdated ones at once (details
— in [Hosts](/en/guide/hub-hosts#host-list)).

## Vulnerability database

Without the hub every host downloads trivy (~50 MB) and its database
(~100–150 MB compressed, ~1 GB unpacked) for a vulnerability scan. On the
hub the database is **one**:

- every `NKT_HUB_VULNDB_REFRESH_INTERVAL` (12 h) the hub checks its
  freshness in the background; downloads happen at most once a day;
- "Refresh now" forces a check (a fresh database is not downloaded again);
- "Scan" on the "Vulnerabilities" page of a **host under the hub**: the
  hub takes the host's package manifest (a `dpkg` dump and
  `/etc/os-release`, hundreds of KB) over SSH and scans it with its own
  database. The host needs neither trivy nor the database for OS
  packages;
- **container images** are scanned on the host itself — its
  Docker/Podman socket is needed. A host with containers installs trivy
  and the database once, only for this part; a host without containers
  downloads nothing;
- "localhost" uses the same database directory (`NKT_DATA_DIR/vuln`).

## ClamAV database

A copy of the ClamAV signature database on the hub. Created with a button
in "About", then refreshed every `NKT_HUB_CLAMDB_REFRESH_INTERVAL` (24 h,
`0` — never), only changes are downloaded. On a host page, the "Malware"
tab's "database from hub" button uploads it over SSH — a host without
internet doesn't need to reach ClamAV mirrors.

## Package cache

Size, hits, limit and clearing — in the same place; how it works — on the
[Package cache](/en/guide/hub-cache) page.

## Model analysis and privacy mode

The model setup (Anthropic or OpenAI-compatible, local ones included),
instructions and the "hide sensitive" checkbox — on the
[Alerts, jobs, AI](/en/guide/hub-operations) page.

## Help

The **"?"** icon next to a section title (in place of the former "ⓘ")
shows a short hint on hover and, when clicked, opens this site's section
for the current page of the interface, in a window inside nkt;
**"Detach"** moves the help into a separate browser window (like the
terminal), **"In a new tab"** opens a regular tab. The pipeline editor,
"Access" and the "Sites" wizard have buttons straight to their sections.

![Help icon](/screens/en/help-hover.png)

![Help](/screens/en/help-modal.png)

**The site address** is set in the "Help" card on "About" (and via the
"Help address…" link in the help window, also on a standalone host without
a hub): `https://piqab.github.io/nkt/` by default, or your own copy, e.g.
on a local network without internet (`npm run build` in the project's
`site/`, the result is `site/.vitepress/dist`). Editing shows the
difference before saving and keeps a history of previous addresses. The
site describes the latest nkt version; your own copy built from the same
commit as the installed nkt matches it exactly. If your server forbids
being shown inside other pages (`X-Frame-Options`), the window stays
empty; use "Detach" or "In a new tab".
