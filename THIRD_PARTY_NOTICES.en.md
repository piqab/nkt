# Third-party components in NetKnownsThat

Русская версия: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

NetKnownsThat's own code is released under the MIT license (see
[LICENSE](LICENSE)). The `nkt` build also includes third-party components
under their own licenses. The MIT license does not cover them: each
component stays under its own license.

## Virtual machine screen clients

### spice-html5 — LGPL-3.0-or-later

- **What it is:** a SPICE client that runs in the browser. The "Screen"
  window uses it for libvirt machines with SPICE graphics and for LXD
  virtual machines.
- **Location:** `web/public/vendor/spice-html5/`. Inside the `nkt` binary
  these are separate files served at `/vendor/spice-html5/`.
- **Source:** <https://gitlab.freedesktop.org/spice/spice-html5>, a
  snapshot of the master branch from 2026-09-07.
- **Modifications:** none. The files are neither modified nor minified.
  They are not part of the nkt UI bundle: the browser loads them with a
  separate dynamic import, and only when a SPICE window opens.
- **License:** GNU LGPL version 3 or later. The license texts sit next to
  the files: `COPYING` (GPL-3.0) and `COPYING.LESSER` (LGPL-3.0).
- **Replacing the library.** To use your own or a modified version,
  replace the files in `web/public/vendor/spice-html5/` and rebuild `nkt`
  (`make build`). The rest of nkt's source code is MIT-licensed, so this
  replacement is always possible.

These files ship with spice-html5 in its `thirdparty/` directory:

| Files | Author | License |
|---|---|---|
| `jsbn.js`, `prng4.js`, `rng.js`, `rsa.js` | Tom Wu, 2003–2005 | MIT-style, text in each file header |
| `sha1.js` | Paul Johnston and contributors, 1998–2009 | BSD-3-Clause, text in the file header |

### noVNC 1.7.0 — MPL-2.0

- **What it is:** a VNC client that runs in the browser, used by the
  "Screen" window for libvirt machines.
- **How it ships:** the unmodified npm package `@novnc/novnc`, bundled
  into the UI's JavaScript bundle.
- **Source:** <https://github.com/novnc/noVNC>, tag `v1.7.0`. Files
  covered by MPL-2.0 remain under MPL-2.0.
- **License text:** <https://mozilla.org/MPL/2.0/>.

## Server (Go)

| Module | Version | License |
|---|---|---|
| github.com/coder/websocket | v1.8.15 | ISC |
| github.com/creack/pty | v1.1.24 | MIT |
| github.com/gdamore/tcell/v2 | v2.13.10 | Apache-2.0 |
| github.com/go-chi/chi/v5 | v5.3.2 | MIT |
| github.com/haproxytech/config-parser/v5 | v5.1.6 | Apache-2.0 |
| github.com/hashicorp/yamux | v0.1.2 | MPL-2.0 |
| github.com/nginxinc/nginx-go-crossplane | v0.4.89 | Apache-2.0 |
| github.com/pkg/sftp | v1.13.11 | BSD-2-Clause |
| github.com/rivo/tview | v0.42.0 | MIT |
| golang.org/x/crypto, x/net, x/term | v0.57.0, v0.58.0, v0.46.0 | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | MIT and Apache-2.0, NOTICE: Copyright 2011-2016 Canonical Ltd. |
| modernc.org/sqlite | v1.59.0 | BSD-3-Clause; SQLite itself is in the public domain |

`hashicorp/yamux` (MPL-2.0) is used unmodified. Its source:
<https://github.com/hashicorp/yamux>, tag `v0.1.2`.

Indirect dependencies are listed in `go.mod` and `go.sum`. Their license
texts sit next to their source in the module cache (`go env GOMODCACHE`).

## UI (npm)

| Package | Version | License |
|---|---|---|
| react, react-dom | 18.3.1 | MIT |
| antd | 6.6.4 | MIT |
| @ant-design/icons | 6.3.4 | MIT |
| i18next | 26.4.2 | MIT |
| react-i18next | 17.0.14 | MIT |
| @xterm/xterm | 5.5.0 | MIT |
| @xterm/addon-canvas, -fit, -search, -unicode11, -web-links | 0.7–0.16 | MIT |
| @novnc/novnc | 1.7.0 | MPL-2.0, see above |

The full package list with versions is in `web/package-lock.json`.
