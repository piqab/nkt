---
title: Development
---

# Development

Building from source, the test stand, tests, code layout, the site and
translations.

## Requirements

Docker 29.6 (release builds and the stand), Go 1.26 (at least 1.25 —
required by `modernc.org/sqlite`), Node 24 / npm 11 (Node 20 is enough).
On the target host — nothing but the binary.

## Build commands

`make` without arguments prints the list of targets.

| Command | What it does |
|---|---|
| `make build` | **The main target.** The production binary `dist/nkt` (a static ELF, version from `VERSION` and `git describe`). With Docker — in containers, without — through `native-build`. Another architecture: `make build GOARCH=arm64` |
| `make web` | Only the web UI into `internal/webui/dist`, where `go:embed` picks it up. `make build` calls it itself |
| `make native-build` | Build without Docker: if Go or Node is missing, asks and installs them into `$HOME/.local`, without sudo |
| `make build-dev` | A binary for the current OS — **only for fixtures mode** |
| `make edge` | `nkt-edge` (deployment webhooks for a VPS) into `dist/` |
| `make test` | `go test ./...` and the frontend type check |
| `make check` | The same plus `go vet` and `gofmt` — run before committing |
| `make stand` / `make stand-down` | The test stand (below) |
| `make hub` / `make hub-down` | The hub in Docker from source |
| `make install` | Install on **this** Linux host; an existing `nkt.env` is kept |
| `make hub-install` | Install the hub on this host as a service |
| `make bump-version` | Bump the patch version in `VERSION` |
| `make clean` | Remove `dist/` and local binaries |

The Go and npm caches live in named docker volumes, so rebuilds don't
fetch dependencies again. Reset: `docker volume rm nkt-gomod nkt-gocache nkt-npm`.

On Windows `make` is usually missing, and `native-build` targets Linux —
Docker is needed:

```powershell
docker run --rm -v "${PWD}:/src" -w /src/web node:22-alpine sh -c "npm ci && npm run build"
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux golang:1.26-alpine `
  go build -trimpath -ldflags "-s -w" -o dist/nkt ./cmd/nkt
```

## Releases

For every `vX.Y.Z` tag `.github/workflows/release.yml` builds and
publishes the `nkt` and `nkt-edge` binaries for amd64/arm64/arm, the hub
image `ghcr.io/piqab/nkt-hub`, the `docker-compose.hub.release.yml` and
`k8s-hub.yaml` manifests with the exact image version, and `SHA256SUMS`.
A `vX.Y.Z-beta` tag (`git tag v$(cat VERSION)-beta`) builds a beta: a
pre-release, version `X.Y.Z-beta`, images `:X.Y.Z-beta` and `:beta`; the
hub offers it only with the "use beta versions" checkbox.

Release notes come not from commits but from `WHATSNEW.md` and
`WHATSNEW.en.md`: a `## vX.Y.Z` section is written in the same commit as
the `VERSION` bump, and on release `scripts/release-notes.sh` collects
the sections accumulated since the previous tag into the GitHub Release
body (the English part — after the `<!-- en -->` marker). The hub shows
it in "About" in the UI language — so the text is written for users. A
forgotten section doesn't break a release: the workflow falls back to a
commit list.

A new feature is also a line in `FEATURES.ru.md` and `FEATURES.md` and a
site update in both languages.

## Test stand

Real nginx and haproxy with backends, next to them — nkt in `local` mode
reading the same files and working with real docker.

```bash
make stand              # docker compose -f stand/docker-compose.yml up -d --build
docker logs nkt         # admin password
make stand-down         # stop and remove volumes
```

| Address | What |
|---|---|
| <http://127.0.0.1:8077> | nkt |
| <http://127.0.0.1:8081> | nginx, proxying to two backends |
| <http://127.0.0.1:8082> | haproxy, balancing the same backends |
| <http://127.0.0.1:8404> | haproxy stats page (deliberately without a password) |
| `127.0.0.1:6380` | redis, deliberately published on all interfaces |

The stand checks parsing of live configs, the Docker Engine API,
container management and the config edit cycle with a real `nginx -t`,
including a failure with automatic rollback. It doesn't cover the host's
systemd and firewall (they aren't in a container) and "declared vs
listening" for nginx and haproxy — they are in their own network
namespaces; the application recognizes that and stays silent.

`make stand-down` removes the volumes on purpose: the account lives in a
volume, and without removing it a recreated stand won't print a new
password.

## Frontend with hot reload

```bash
./nkt                        # API on :8077
cd web && npm run dev        # UI on :5173, proxies /api
```

## Tests

```bash
make test        # go test ./... and npm run typecheck in web/
make check       # plus go vet and gofmt
```

::: warning Stop the local nkt on 8077
Hub tests start their own nkt processes; a stand or demo running on
`127.0.0.1:8077` interferes with them (`401` and `429` errors). Stop it
before `go test ./...`.
:::

- Tests run against `fixtures/host` — a snapshot with planted problems;
  `TestScanFindsPlantedProblems` checks that the analyzer finds each one
  with the expected severity.
- The terminal UI is tested headless: `tcell` provides a simulated
  screen, the test presses keys and checks the frames.
- The hub is tested against a **real** `sshd` and a real nkt process —
  installation over SSH, API proxying, terminal WebSocket sessions
  through the tunnel. There are deliberately no mocks in this layer.
- The `examples/hello-app` example is checked by `TestHelloAppExample`
  and `TestHelloAppHookScript` (`internal/hub/examples_test.go`):
  pipeline descriptions, the manifest, values, the script, the CI files
  and a real call of `scripts/nkt-hook.sh` with a signature check.

Live tests that go to the network are skipped by default:

| Variable | What it enables |
|---|---|
| `NKT_TEST_LIVE_VULN=1` | Downloading trivy and its database, a real scan (`internal/vuln`) |
| `NKT_TEST_LIVE_REGISTRY=1` | Requests to an image registry (`internal/aptcache`) |
| `NKT_TEST_LIVE_GO_INSTALL=1` | Installing Go when building from source (`internal/hub`) |
| `NKT_TEST_LIVE_RELEASE_DOWNLOAD=1` | Downloading a release binary from GitHub (`internal/hub`) |
| `NKT_TEST_LIVE_RELEASE_VERSION` | Which release version that test downloads |
| `NKT_TEST_LIVE_VERSION_CHECK=1` | Checking the latest version through the GitHub API (`internal/hub`) |

## Security checks

CI (`.github/workflows/security.yml`) runs govulncheck, gitleaks, CodeQL,
gosec and Trivy. Locally before a commit it's worth running the same:

```bash
govulncheck ./...
gosec -conf .gosec.json ./...
trivy fs --scanners vuln,misconfig,secret .
```

## Layout

```
cmd/nkt                 subcommands: serve, tui, scan, hub
cmd/nkt-edge            the webhook entry service for a VPS
internal/config         every setting from NKT_* variables
internal/collect        the ONLY place where fixtures and a real host differ
  ├── local.go          the real FS, exec, docker/podman unix sockets (Linux only)
  └── fixtures.go       an on-disk snapshot, canned command output
internal/parse          nginx, haproxy, caddy, docker, podman, lxd, libvirt,
                        iptables, ufw, firewalld, ss, systemd
internal/model          a vendor-neutral description of what was found
internal/analyze        finding rules
internal/topology       the resource graph
internal/inventory      scan orchestration, snapshots, monitoring targets
internal/monitor        probes, metrics, access logs, scheduler
internal/tlscheck       live TLS: what the socket really serves
internal/control        services, versioned config edits, firewall,
                        certificates, podman/lxd/libvirt
internal/k8s            Kubernetes: objects, actions, Helm, forwards
internal/jobs           background jobs with a log and resumption
internal/profile        profiles: desired state, plan, apply
internal/script         the hub script language
internal/deploy         deployments: pipeline spec, git, webhooks, registry
internal/edge           the hub ↔ nkt-edge tunnel protocol
internal/aptcache       the hub's package, file and image cache
internal/store          SQLite (modernc, pure Go — a static build)
internal/secretbox      secret encryption (AES-256-GCM) for the hub
internal/hub            the hub: SSH installs, proxying, clusters, deployments
internal/msgs           the ru/en server message catalog
internal/api            the HTTP API on chi
internal/webui          the embedded web UI
internal/tui            the terminal UI on tview
web/                    React + TypeScript + antd, charts in plain SVG
```

The key decision is the `collect.Collector` interface. The rest of the
application works with the host's POSIX paths and doesn't know whether it
reads a real server or a snapshot, so parsers and rules behave the same
everywhere.

The web UI and the TUI are equal consumers of the same packages: both
call `inventory`, `analyze`, `topology` and `control` directly. A fix in
the rules shows up in both.

Each runtime (docker, podman, lxd, libvirt) is a separate module: its own
model, parser and page. There is deliberately no shared "workload"
abstraction between them.

**A new parser**: a file in `internal/parse` returning `model.Endpoint` /
`model.Upstream` / `model.SourceStatus`, and a call in
`internal/inventory/scan.go`. The map, analysis, monitoring and both UIs
pick it up by themselves.

External dependencies: `nginx-go-crossplane`, `haproxytech/config-parser`,
`modernc.org/sqlite`, `go-chi/chi`, `rivo/tview`, `golang.org/x/crypto`,
`pkg/sftp`, `coder/websocket`, `creack/pty`, `hashicorp/yamux`,
`gopkg.in/yaml.v3`. Docker and Podman are queried with a minimal Engine
API client of our own; LXD and libvirt — through their CLIs (`lxc`,
`virsh`).

## Messages and translations

The UI is bilingual: the language follows the browser (Russian →
Russian, anything else → English), the switch remembers the choice. The
frontend takes strings from `web/src/i18n/{ru,en}.json`.

Server text that reaches the UI (API errors, job logs) is written as a
key of the `internal/msgs` catalog with translations in `ru.go` and
`en.go`:

- an error — `msgs.Errorf("pkg.key", args...)` instead of `fmt.Errorf`;
  in the catalog `%v` is used instead of `%w`, `errors.Is/As` still work;
- text in place — `msgs.Tc(ctx, "pkg.key", args...)`; for a job the
  language is the author's (`jc.Log("pkg.key", …)`, `jc.Lang()`);
- at the API boundary — `writeErr(w, r, status, err)`: a catalog error in
  the request language, a foreign one (from `os`, `exec`) as is.

A key is `<package>.<meaning>` (`files.pathOutsideAllowed`). Output of
external programs (nginx -t, apt, certbot, git) isn't translated.

## Site and screenshots

The site (VitePress) is in `site/`, with its own `package.json`:

```bash
cd site && npm ci && npm run dev      # http://localhost:5173/nkt/
npm run build                         # site/.vitepress/dist; catches dead links
```

It's published to GitHub Pages by `.github/workflows/pages.yml` on a push
to `main`. Russian is the root, English is `/en/`; the first visit
redirects by browser language.

Screenshots (`site/public/screens/{ru,en}`) are taken by
`scripts/screenshots.py` through the Chrome DevTools Protocol from two
stands: nkt in fixtures mode (`NKT_MODE=fixtures` on `127.0.0.1:8077`,
`NKT_DATA_DIR` — a neutral directory like `/tmp/nkt-demo/host`, it's
visible on the images screen) and a demo hub whose hosts lead through a
local sshd to the same fixtures nkt. The demo hub is seeded with
`go run scripts/demo/seed.go`; sshd is started like `startTestSSHD` in
`internal/hub`. Usage — in the script header.

Demo availability and usage history is seeded once per database and only
with `NKT_SCHEDULER_ENABLED=true`: if the charts are empty, start the
stand on a fresh database. The `job` screen needs at least one job — for
example, `POST /api/lxd/instances?job=1`.
