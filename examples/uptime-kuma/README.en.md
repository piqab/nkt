# Uptime Kuma: one container, data in ./data

[Uptime Kuma](https://github.com/louislam/uptime-kuma) monitors the
availability of sites and services. The project's compose file
([compose.yaml](https://github.com/louislam/uptime-kuma/blob/master/compose.yaml))
is deployed as is.

| In the file | nkt |
|---|---|
| `image: louislam/uptime-kuma:2` | major version 2 is pinned; updates within it arrive on deployment |
| `volumes: ./data:/app/data` | data in the stack directory `/srv/compose/uptime-kuma/data`; deleting the pipeline moves the directory to `.nkt-removed`, so the data is kept |
| `ports: "3001:3001"` | the default `bind`: `127.0.0.1:3001`, outside through the site |
| a healthcheck in the image | `up --wait` waits for healthy |

"Examples" → "Uptime Kuma", pick a host; put your name in the `site:`
block. After deploying, open the site and create the administrator account
(first login).

Verified: deployed on docker 29 in ~80 seconds, the container healthy.
