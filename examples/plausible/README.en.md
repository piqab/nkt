# Plausible: three services, configs alongside, ~2 GB of memory

[Plausible CE](https://github.com/plausible/community-edition) is web
analytics: the app, PostgreSQL and ClickHouse. The project's compose file
(branch `v3.2.1`: the repository itself pins the version).

| In the file | nkt |
|---|---|
| `./clickhouse/*.xml` for ClickHouse | `files: [clickhouse/]`, the whole directory next to the compose file |
| `POSTGRES_PASSWORD=postgres` as a value | `env_keys`: the password from `.env`; `DATABASE_URL` in `.env` uses the same password |
| `BASE_URL`, `SECRET_KEY_BASE` as `${…}` | the pipeline `.env` |
| the app port is not published | the site publishes `8000` (the image's EXPOSE) on 127.0.0.1 itself |
| healthchecks on the databases | `up --wait` waits; the app starts after migrations |

Needs ~2 GB of free memory (ClickHouse). Registration:
`DISABLE_REGISTRATION=invite_only`: the first user is created on first
login, the rest by invitation.

Verified: deployed in ~55 seconds, `/api/health` reports `postgres: ok,
clickhouse: ok`.
