# URL check — an example of your own plugin

An example plugin uploaded to the hub as **your own** (Plugins → Catalog →
Upload your own plugin) rather than taken from the catalog. Written by the
[guide for agents](../../AGENTS.en.md) from the [template](../../_template).

- **Addresses** — from the `URL_CHECK_TARGETS` setting (comma-separated):
  the response code, time, days until the certificate expires.
- **Findings:** not answering or 5xx; slower than the threshold
  (`URL_CHECK_SLOW_MS`, 2000 ms by default); the certificate expires in
  under 14 days or has expired.
- **Script command** `urlcheck ADDRESS` — check one address; a failure
  fails the script step (handy after a deploy).
- **MCP:** `url-check_status`, `url-check_findings`, `url-check_check`.

Security: addresses — `http://` and `https://` only, no login in the
address; the answer is read up to 64 KB, a 10 s timeout; certificates are
verified (your own CA — `URL_CHECK_CA_FILE`). Checking an arbitrary
address (`check`, `urlcheck`) is not for read tokens: it is a request from
the plugin host anywhere.

## Upload to the hub

1. The archive: ready-made — `https://piqab.github.io/nkt/samples/url-check.tar.gz`
   (the Download the example link in the upload window), or from the
   repository: `tar czf url-check.tar.gz -C examples/_samples url-check`.
2. About → Plugins → Catalog → **Upload your own plugin** → the archive →
   read the code → Code reviewed → Save.
3. In the catalog — “URL check” marked “own” → Install on a host → the
   addresses in the form → a job.

Checks: `python3 -I deploy/test_plugin.py`,
`go run ./cmd/nkt-catalog lint -dir examples/_samples/url-check`.
