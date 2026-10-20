# Garage: your own S3 storage

> **WARNING:** this example uses a third-party repository and images — their security and stability are not guaranteed. Check them yourself before deploying to production.

[Garage](https://git.deuxfleurs.fr/Deuxfleurs/garage) is a lightweight
S3-compatible object store. This example runs **one node** on one host: files,
backups, media for your own apps (anything that speaks S3: aws-cli, rclone,
restic, Nextcloud, PostgreSQL backups…).

- **S3 API** — `https://s3.example.com` through an nkt site with HTTPS and no
  file size limit (`max_body: "0"`);
- **admin panel** [garage-webui](https://github.com/khairul169/garage-webui)
  (buckets, keys, layout, object browser) — on `127.0.0.1:3909` only;
- **the bucket and the access key** are created by the one-shot `garage-init`
  through the admin API on every deploy — nothing to do by hand in a console;
- **web access to files for clients** —
  [Filestash](https://github.com/mickael-kerjean/filestash), commented out in
  the compose file (see below).

Files:

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — `garage`, `garage-init`, `garage-webui`, (`filestash`);
- [`deploy/garage.toml`](deploy/garage.toml) — config template, `${NAME}` is filled in from `.env`;
- [`deploy/init.py`](deploy/init.py) — initial setup through the admin API v2;
- [`pipeline.yaml`](pipeline.yaml) — the pipeline.

## Why a custom compose file

| Usual instructions | Here |
|---|---|
| `garage.toml` with secrets written by hand on the host | a template in the repository, the pipeline puts it at `/etc/garage.toml` (`configs:`, `0400`), secrets come from the pipeline's `.env` |
| `garage layout assign …`, `bucket create`, `key create` in a console | the one-shot `garage-init` through the admin API; running it again breaks nothing |
| admin API `3903` on all addresses | `127.0.0.1` only |
| `image: …:latest` | pinned versions: `dxflrs/garage:v2.4.1`, `garage-webui:1.1.0`, Filestash by digest |
| the proxy's `client_max_body_size` edited by hand | `max_body: "0"` in the pipeline's site: no limit and no buffering at the proxy |

## 1. Deploy

1. An A record `s3.example.com` → the host's address.
2. "Deployments" → "New pipeline" → **"Examples" → "Garage (S3 storage)"**
   (or the text of [`pipeline.yaml`](pipeline.yaml)), pick the host.
3. Set the name in `S3_DOMAIN` and uncomment `site:` with the same name.
   `CAPACITY` is how much space Garage counts as its own on this host
   (`100G`, `1T`); it is accounting, not a disk reservation.
4. "Save" → "Deploy". The hub creates the `generate:` values:
   `GARAGE_RPC_SECRET`, `GARAGE_ADMIN_TOKEN`, the key `S3_KEY_HEX` and the
   secret `S3_SECRET`.

The deploy log (the `garage-init` service) shows (messages are in Russian):

```
garage-init: раскладка: узел 638f282206f95118…, ёмкость 100G
garage-init: бакет files создан
garage-init: ключ GKc9f714659c3d9a79c2ee624b заведён
garage-init: ключ GKc9f714659c3d9a79c2ee624b: чтение, запись, владелец бакета files
garage-init: готово
```

## 2. S3 access

The values are in the pipeline's "Secrets" → ".env" → "Show":

- **Access key ID** — `GK` + the value of `S3_KEY_HEX` (26 characters in total);
- **Secret access key** — `S3_SECRET`;
- **region** — `garage`; **addressing** — path-style
  (`https://s3.example.com/files/…`).

aws-cli:

```sh
export AWS_ACCESS_KEY_ID=GK… AWS_SECRET_ACCESS_KEY=… AWS_DEFAULT_REGION=garage
aws --endpoint-url https://s3.example.com s3 cp backup.tar.gz s3://files/
aws --endpoint-url https://s3.example.com s3 ls s3://files/
```

rclone (`~/.config/rclone/rclone.conf`):

```ini
[garage]
type = s3
provider = Other
endpoint = https://s3.example.com
region = garage
access_key_id = GK…
secret_access_key = …
force_path_style = true
```

Give other apps separate keys with rights to their own bucket: in the admin
panel ("Keys" → "Create key", "Buckets" → the bucket → "Allow key").

Virtual-host addressing (`files.s3.example.com`) is also enabled in
`garage.toml` (`root_domain`), but it needs a wildcard DNS record and a
wildcard certificate — path-style is simpler.

## 3. Admin panel (garage-webui)

On the host's `127.0.0.1` only — it holds Garage's admin token, which is full
access. Open it through an SSH tunnel:

```sh
ssh -L 3909:127.0.0.1:3909 host
# in the browser: http://localhost:3909
```

The password is enabled by the `WEBUI_AUTH` variable — `login:bcrypt-hash`:

```sh
docker run --rm httpd:2.4-alpine htpasswd -nbB admin 'your-password'
# admin:$2y$05$…
```

Put the whole line into "Secrets" → ".env" → `WEBUI_AUTH` (not into the
description: the hash contains `$`), then "Deploy". Without `WEBUI_AUTH` the
panel lets in everyone who reaches the port — that is, everyone with SSH to
the host.

## 4. Web access to files for clients (Filestash)

garage-webui is for the administrator. For people who just need to upload or
download a file in a browser there is Filestash: they sign in with their own
S3 key and see only their buckets.

1. Uncomment `filestash` and the `filestash` volume in `docker-compose.yml`
   (your own copy of the repository) and deploy.
2. Set the admin password **right away**: whoever opens Filestash first lands
   on `/admin/setup` and sets it. So do it through a tunnel first:
   ```sh
   ssh -L 8334:127.0.0.1:8334 host
   # http://localhost:8334 → admin password
   ```
3. In the Filestash admin: "Backend" → S3, endpoint `http://garage:3900`,
   region `garage`.
4. Only then publish it: "Sites" → new site → `127.0.0.1:8334` (for large
   files set "Request body limit" to `0`).

## 5. Backups and upgrades

- Data lives in the volumes `garage_meta` (metadata, small, **essential**) and
  `garage_data` (blocks). Blocks are useless without `meta`. Garage snapshots
  its metadata itself (`metadata_auto_snapshot_interval = "6h"`,
  `/var/lib/garage/meta/snapshots`); copy both volumes, preferably with
  `garage` stopped.
- Upgrade by editing the `dxflrs/garage:` tag and "Deploy". Major versions
  (1.x → 2.x) may migrate metadata — read the release notes and back up
  `meta` first.
- One node has no redundancy (`replication_factor = 1`): if the disk dies,
  the data is gone. For real storage use three nodes in different places (a
  different `garage.toml`, port `3901` between nodes) or copy the buckets
  somewhere else (`rclone sync`).

## Troubleshooting

| Symptom | Cause |
|---|---|
| `garage-init: … 401` | `GARAGE_ADMIN_TOKEN` in `.env` changed but `garage` was not restarted — "Deploy" again |
| `413 Request Entity Too Large` at the client | `site:` lacks `max_body: "0"` (the default is 64m) |
| `SignatureDoesNotMatch` | the region is not `garage`, or the client uses virtual-host style without a wildcard — switch to path-style |
| `NoSuchBucket` | the bucket name differs from `BUCKET`, or the key has no rights on that bucket |
| admin panel: `Unauthorized` | wrong password, or `WEBUI_AUTH` lacks the `admin:` prefix |
