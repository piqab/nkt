# n8n-nodes-nkt

n8n nodes for the [NetKnownsThat (nkt)](https://github.com/piqab/nkt) hub.

- **nkt**: hosts (list, overview, findings, vulnerabilities, services),
  service and container actions, pipelines (deploy, dry run with selectable
  checks, waiting for the result, deployments), hub jobs (get, log, wait),
  fail2ban (ban, unban, banned list), alerts, and any API call the token
  allows.
- **nkt Trigger**: receives the hub's outgoing webhooks (alerts and
  deployment outcomes) and checks their HMAC signature.
- **nkt Alert Trigger (Polling)**: polls the hub for new alerts when n8n
  cannot receive webhooks.

Credentials: a hub **API token** ("About" → "API tokens"), signed requests
by default (the secret never crosses the network, which also works through
nkt-edge with the API role) or Bearer.

## Install from a release

Every nkt release has `n8n-nodes-nkt-<version>.tgz` attached:

```sh
V=1.11.79
curl -fLO https://github.com/piqab/nkt/releases/download/v$V/n8n-nodes-nkt-$V.tgz
mkdir -p ~/.n8n/custom/n8n-nodes-nkt
tar xzf n8n-nodes-nkt-$V.tgz -C ~/.n8n/custom/n8n-nodes-nkt --strip-components=1
```

## Build and install from source

```sh
npm ci && npm run build        # dist/
npm test                       # signature vectors shared with the hub's Go tests
mkdir -p ~/.n8n/custom/n8n-nodes-nkt
cp -r package.json dist ~/.n8n/custom/n8n-nodes-nkt/
```

In Docker, mount that directory as `/home/node/.n8n/custom/n8n-nodes-nkt`
and restart n8n. Example workflows are in `examples/`.

Documentation: <https://piqab.github.io/nkt/en/guide/n8n> (in Russian:
<https://piqab.github.io/nkt/guide/n8n>).
