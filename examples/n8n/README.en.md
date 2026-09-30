# n8n + PostgreSQL: three services, a file alongside, everything from .env

[n8n](https://n8n.io) is automation. The compose file comes from
[n8n-io/n8n-hosting](https://github.com/n8n-io/n8n-hosting/tree/main/docker-compose/withPostgres)
(the `withPostgres` variant): PostgreSQL, n8n and a task runner.

| In the file | nkt |
|---|---|
| all settings are `${…}` from `.env` | the pipeline `.env` ("Access" → "Stack .env"), including `N8N_VERSION`, which pins the version |
| `./init-data.sh` in the database service | `files:` brings it next to the compose file; it creates the database user for n8n |
| `ports: 5678:5678` | `bind`: `127.0.0.1:5678`, the site points there |
| no `WEBHOOK_URL`, `N8N_PROXY_HOPS` | `env_keys` adds them to n8n's `environment` with values from `.env`; needed behind the HTTPS proxy |

The `.env` template is at the end of `pipeline.yaml`. n8n has no
healthcheck: `up --wait` waits for it to start, the web UI answers a few
seconds later.

Verified: deployed in ~20 seconds (images pulled), `init-data.sh` ran
(`CREATE ROLE`), `/healthz` answers `ok`.
