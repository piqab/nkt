# Forgejo + PostgreSQL: web through the site, SSH to the outside, no install wizard

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Review them yourself before deploying to production.

[Forgejo](https://forgejo.org) is a community git server (Codeberg). The
project has no compose file for deployment, so it is here:
[`deploy/docker-compose.yml`](deploy/docker-compose.yml): Forgejo 16.0 (the
rootless image) and PostgreSQL 17, passwords and addresses from `.env`.

**Two ports, two ways out:**
- the web (3000) goes through the site: the `site:` block publishes it on
  127.0.0.1 and issues a certificate;
- SSH for `git clone ssh://git@…:2222/…` is Forgejo's built-in server on
  2222, published directly: `ports: forgejo: ["0.0.0.0:2222:2222"]` in the
  pipeline. Without that line the default `bind` would keep SSH on
  127.0.0.1 only. **Open 2222/tcp in the host firewall** (the site opens
  only 80/443).

**No install page.** Settings come from the environment (`FORGEJO__…`,
`INSTALL_LOCK=true`), and registration is closed. The administrator is
created by the one-shot `forgejo-admin` service: it waits until Forgejo is
healthy, runs `forgejo admin user create --admin` with the login, password
and email from `.env`, and exits; nkt counts that as a successful
deployment. If the administrator already exists, later deployments leave it
alone; change the password in Forgejo itself.

"Examples" → "Forgejo + PostgreSQL", a host, your name in `site:`; the `.env`
values are in the `env:` block of `pipeline.yaml` (the hub creates the
passwords; adjust the administrator's login and email). The administrator
password: "Secrets" → ".env" → history → "Show". After the deployment, sign in as the
administrator right away.

Gitea data does not move to Forgejo 16: remove the old gitea example's stack
(with its volumes) and deploy this one fresh.

## Traffic Fly variant

[`pipeline.fly.yaml`](pipeline.fly.yaml) is the same stack with access through
a VPS gateway (nkt-edge in the fly role) instead of a proxy on the host: an
`https` route for `git.example.com` (the gateway issues the certificate) and
`tcp` 2222 for git over SSH. Nothing is opened on the host itself and the A
record points at the gateway. Such a stack is easy to move between hosts:
the routes switch to the new host by themselves and DNS stays the same — see
the step-by-step guide
["Moving a stack with Traffic Fly"](https://piqab.github.io/nkt/en/guide/migrate-with-fly).

## Hub plugin

The `forgejo` plugin manages Forgejo (and Gitea — the same API) right from
the hub. Written by the [guide for agents](../AGENTS.en.md) from the
[template](../_template).

- **Summary and findings:** version, repository, user and administrator
  counts; a failed last Actions run, a mirror not synced for over 7 days,
  more than three administrators.
- **Repositories:** create (your own or in an organization), archive, sync
  a mirror, delete — with a confirmation; a repository's Actions runs and
  webhooks.
- **Webhooks:** add one — for example, an nkt pipeline deploy webhook
  (Deployments → pipeline → Webhook), delete.
- **Users:** administrators, last login, blocked.
- **A runner registration token** — for the whole instance or one
  repository: once to the hub window, to an agent or a script — only
  straight into a pipeline `.env` (for example, the `forgejo-runner`
  pipeline's).
- **Scripts:** `forgejo repos`, `forgejo mirror-sync OWNER/REPO`,
  `forgejo archive OWNER/REPO`, `forgejo hook add OWNER/REPO URL`,
  `forgejo runner-token [OWNER/REPO] to-pipeline PIPELINE env NAME`.
  **MCP:** `forgejo_*`.

**Install:** About → Plugins → Catalog → Forgejo → Install on a host (the
Forgejo host — then the address is `http://127.0.0.1:3000`). The token: in
Forgejo as an administrator → Settings → Applications → Generate token,
with `admin`, `repository`, `user`, `organization` (read and write). The
token lives only in the service settings file (`0600`), never in the log
or answers.

The Forgejo API exposes neither users' 2FA state nor the open
registration setting — the plugin has no such findings. Tested against a
fake API (`python3 -I deploy/test_plugin.py`), not a live Forgejo.
