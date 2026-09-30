# Gitea + PostgreSQL: web through the site, SSH to the outside

[Gitea](https://gitea.com) is a git server. The project has no compose file
for deployment, so it is here:
[`deploy/docker-compose.yml`](deploy/docker-compose.yml): Gitea 28.0 and
PostgreSQL 17, the password and addresses from `.env`.

**Two ports, two ways out:**
- the web (3000) goes through the site: the `site:` block publishes it on
  127.0.0.1 and issues a certificate;
- SSH for `git clone git@…` (2222 → 22 in the container) goes directly:
  `ports: gitea: ["0.0.0.0:2222:22"]` in the pipeline. Without that line the
  default `bind` would keep SSH on 127.0.0.1 only. **Open 2222/tcp in the
  host firewall** (the site opens only 80/443).

"Examples" → "Gitea + PostgreSQL", a host, your name in `site:`; `.env`
from the template in `pipeline.yaml`. The first visit is the install page
with the database settings filled from `.env`; create the administrator.

Verified: deployed on docker 29 in ~15 seconds, both services healthy, `/api/healthz` reports `pass`, SSH published on `0.0.0.0:2222`.
