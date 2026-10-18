# Authorizer + PostgreSQL: sign-in and users for your own apps

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Review them yourself before deploying to production.

[Authorizer](https://github.com/authorizerdev/authorizer) is an
authorization server (OAuth2/OIDC, password, magic link and social login,
MFA) with an admin dashboard. The example is version 2.4.2 with
PostgreSQL 17.

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — the stack: `authorizer` and `db`;
- [`pipeline.yaml`](pipeline.yaml) — the pipeline.

## Why our own compose file rather than authorizer's

| In their `docker-compose.yaml` | Here |
|---|---|
| `build: .` — the image is built from source | the ready image `lakhansamani/authorizer:2.4.2` from Docker Hub (version pinned; a new one is a tag edit) |
| `env_file: .env` | v2 **does not read environment variables**: everything is a startup flag (`--client-id`, `--database-url`…); the flags in `command:` are filled from the pipeline's `.env` |
| publishes 8080 and 9091 (gRPC without TLS) | the site publishes 8080 on `127.0.0.1`, reachable only through the proxy; gRPC 9091 and metrics 8081 are not published |
| no database | PostgreSQL with a health check, data in the `db-data` volume |

Secrets passed as flags are visible on the host in `docker inspect` and
`ps` — that is how authorizer v2 works (configuration by flags only); do
not put this stack on a host shared with other users.

## 1. Pipeline

Deployments → New pipeline → **Examples → Authorizer + PostgreSQL**, pick
a host — the description is ready (or paste `pipeline.yaml`, adjusting
`hosts` and `domains`). Save.

## 2. The stack's .env — in the description

The `.env` values live in the description's `env:` block (`pipeline.yaml`):

```yaml
  env:
    AUTH_DOMAIN: auth.example.com
    ALLOWED_ORIGINS: https://auth.example.com
    CLIENT_ID: generate:hex16
    CLIENT_SECRET: generate:hex32
    ADMIN_SECRET: generate:hex32
    JWT_SECRET: generate:hex32
    ENCRYPTION_KEY: generate:hex32
    POSTGRES_PASSWORD: generate:password
```

When the pipeline is **saved**, the values move into its encrypted `.env`
("Secrets") and the description keeps `"@secret"`: they are not in the
database, the version history or the audit log. `generate:` lets the hub
create the value (once: saving again does not change a database
password). To see a generated value: "Secrets" → ".env" → history →
"Show". Replacing an already set key with your own value asks first.

- `AUTH_DOMAIN` is the same name as in `site:` (it becomes `--url`, the
  address in tokens and e-mails).
- `ALLOWED_ORIGINS` — the sites your apps call authorizer from
  (comma-separated, no spaces); include `AUTH_DOMAIN` itself.
- `ENCRYPTION_KEY` is separate from `JWT_SECRET`: it encrypts TOTP keys,
  and without it rotating `JWT_SECRET` would lock out everyone using
  two-factor sign-in.
- `ADMIN_SECRET` is the admin dashboard password: see it in the `.env`
  history as described above.

## 3. Deploy and sign in

Dry run → Deploy. The first start takes about 15 seconds (migrations).

- Admin dashboard — `https://auth.example.com/dashboard/`, the password is
  `ADMIN_SECRET`.
- The sign-in page for users — `https://auth.example.com/app/`.
- An app connects with `CLIENT_ID` and `https://auth.example.com`
  (authorizer SDKs, OIDC: `https://auth.example.com/.well-known/openid-configuration`).
- Sign-up is open by default; if only the admin creates users, uncomment
  `--enable-signup=false` in the compose file.

## Through Traffic Fly instead of the site proxy

If the host is not reachable from the internet, enter through a VPS
gateway with [Traffic Fly](https://piqab.github.io/nkt/en/guide/traffic-fly):

1. a gateway is installed (Deployments → Traffic Fly → Add gateway);
2. the `auth.example.com` A record points at the gateway, not the host;
3. in `pipeline.yaml` uncomment the `fly:` block and remove `site:`
   entirely (you don't need both);
4. Deploy — the deployment log says “route created”, and the Traffic Fly
   map shows the `authorizer` route.

The gateway obtains the certificate; authorizer gets HTTP with
`X-Forwarded-Proto: https`, so `secure` cookies work. Don't expose gRPC
9091 through the gateway: it has no TLS.

Tested: deployment on docker 29 in ~13 seconds, both services healthy;
the dashboard and sign-in page respond, the API accepts `ADMIN_SECRET`,
sign-up writes to PostgreSQL and survives a container restart.
