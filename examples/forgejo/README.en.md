# Forgejo + PostgreSQL: web through the site, SSH to the outside, no install wizard

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

"Examples" → "Forgejo + PostgreSQL", a host, your name in `site:`; `.env`
from the template in `pipeline.yaml` (`env_keys` will not let it deploy
without the administrator's data). After the deployment, sign in as the
administrator right away.

Gitea data does not move to Forgejo 16: remove the old gitea example's stack
(with its volumes) and deploy this one fresh.
