# umami: several services and secrets from .env

[umami](https://umami.is) is web analytics: an app and PostgreSQL. The
example shows a two-service stack from **someone else's** repository without
a fork: the compose file is taken from
[umami-software/umami](https://github.com/umami-software/umami/blob/master/docker-compose.yml)
as is, and nkt changes what is needed in the copy that goes to the host.

## What the umami compose file has and what nkt does

| In the file | nkt |
|---|---|
| `APP_SECRET: replace-me…`, `TWO_FACTOR_ENCRYPTION_KEY`, the DB password `umami` as values | `env_keys`: these variables come from the pipeline `.env` (encrypted on the hub, 0600 on the host); the file in the repository is unchanged |
| `ports: "3000:3000"` on all addresses | the default `bind`: `127.0.0.1:3000:3000`, reachable from outside only through the site proxy |
| a healthcheck on both services | `up --wait` waits until the database and umami are healthy |
| the `umami-db-data` volume | the database data; deleting the pipeline without the "volumes" tick keeps it |

## 1. Pipeline

"Deployments" → "New pipeline" → **"Examples" → "umami + PostgreSQL"**, pick
a host and the description is ready (or paste `pipeline.yaml`, adjusting
`hosts` and `domains`). Save.

## 2. Stack .env

The pipeline's "Access" → **"Stack .env"**:

```
APP_SECRET=<openssl rand -hex 32>
TWO_FACTOR_ENCRYPTION_KEY=<openssl rand -hex 32>
POSTGRES_PASSWORD=<database password>
DATABASE_URL=postgresql://umami:<database password>@db:5432/umami
```

The password in `POSTGRES_PASSWORD` and in `DATABASE_URL` is the same.
Without any of the variables the deployment does not start (the dry run
names the missing ones).

## 3. Deployment and site

"Dry run" → "Deploy". The first start takes about a minute (migrations).
The `site:` block gives a site with a certificate on your name (an A record
→ the host); the proxy points at `127.0.0.1:3000`. The umami login is
`admin` / `umami`: **change the password right away** in umami's settings.

Verified: deployment on docker 29, both services healthy in ~80 seconds,
`/api/heartbeat` answers.

## Version

The umami file refers to `ghcr.io/umami-software/umami:latest`, so each
deployment takes a fresh image. To pin a version, use `images:` in the
pipeline (`umami: ghcr.io/umami-software/umami:<version>`).

## Deletion

"Delete" the pipeline: containers and the network go, the stack directory
moves to `.nkt-removed`, the database volume stays (tick "volumes" to
delete it too).
