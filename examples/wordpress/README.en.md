# WordPress + MariaDB: a site behind the HTTPS proxy

[`deploy/docker-compose.yml`](deploy/docker-compose.yml): WordPress 7.1
(apache) and MariaDB 11.8; passwords from `.env`, data in the `wp-data` and
`db-data` volumes.

- **Behind an HTTPS proxy** WordPress must know it is served over https:
  `WORDPRESS_CONFIG_EXTRA` in the file trusts the `X-Forwarded-Proto` header
  from the nkt site proxy; without it you get endless redirects.
- Container port 80 is not published; the site publishes it on 127.0.0.1.
- Healthchecks on both services: `up --wait` waits for the database and
  WordPress to be ready.

"Examples" → "WordPress + MariaDB", a host, your name in `site:`, `.env`
from the template. After deploying, open the site: the WordPress installer.

Verified: deployed on docker 29 in ~18 seconds, both services healthy; behind a proxy with `X-Forwarded-Proto: https` WordPress redirects to `https://…` without a loop.
