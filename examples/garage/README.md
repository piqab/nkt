# Garage: своё S3-хранилище

> **WARNING:** пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются. Проверьте их сами, прежде чем выкладывать в рабочую среду.

[Garage](https://git.deuxfleurs.fr/Deuxfleurs/garage) — лёгкое
S3-совместимое хранилище объектов. В этом примере — **один узел** на одном
хосте: файлы, резервные копии, медиа для своих приложений (всё, что умеет
S3: aws-cli, rclone, restic, Nextcloud, бэкапы PostgreSQL…).

- **S3 API** — `https://s3.example.com` через сайт nkt с HTTPS, без
  предела размера файла (`max_body: "0"`);
- **админка** [garage-webui](https://github.com/khairul169/garage-webui)
  (бакеты, ключи, раскладка, просмотр объектов) — только на `127.0.0.1:3909`;
- **бакет и ключ доступа** заводит разовый `garage-init` через админ-API
  при каждой выкладке — руками в консоли ничего делать не нужно;
- **веб-доступ к файлам для клиентов** —
  [Filestash](https://github.com/mickael-kerjean/filestash), в compose
  закомментирован (см. ниже).

Файлы:

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — `garage`, `garage-init`, `garage-webui`, (`filestash`);
- [`deploy/garage.toml`](deploy/garage.toml) — шаблон конфига, `${ИМЯ}` подставляются из `.env`;
- [`deploy/init.py`](deploy/init.py) — первичная настройка через админ-API v2;
- [`pipeline.yaml`](pipeline.yaml) — конвейер.

## Почему свой compose

| Обычно в инструкциях | Здесь |
|---|---|
| `garage.toml` с секретами руками на хосте | шаблон в репозитории, конвейер кладёт его в `/etc/garage.toml` (`configs:`, `0400`), секреты — из `.env` конвейера |
| `garage layout assign …`, `bucket create`, `key create` в консоли | разовый `garage-init` через админ-API, повтор ничего не ломает |
| админ-API `3903` на всех адресах | только `127.0.0.1` |
| `image: …:latest` | версии закреплены: `dxflrs/garage:v2.4.1`, `garage-webui:1.1.0`, Filestash — по дайджесту |
| `client_max_body_size` у прокси правят руками | `max_body: "0"` в сайте конвейера: без предела и без буферизации у прокси |

## 1. Выкладка

1. A-запись `s3.example.com` → адрес хоста.
2. «Выкладки» → «Новый конвейер» → **«Примеры» → «Garage (S3-хранилище)»**
   (или текст [`pipeline.yaml`](pipeline.yaml)), выберите хост.
3. Поправьте имя в `S3_DOMAIN` и раскомментируйте `site:` с тем же именем.
   `CAPACITY` — сколько места Garage считает своим на этом хосте (`100G`,
   `1T`); это учёт, а не резерв на диске.
4. «Сохранить» → «Выложить». `generate:` создаст хаб: `GARAGE_RPC_SECRET`,
   `GARAGE_ADMIN_TOKEN`, ключ `S3_KEY_HEX` и секрет `S3_SECRET`.

В журнале выкладки (сервис `garage-init`):

```
garage-init: раскладка: узел 638f282206f95118…, ёмкость 100G
garage-init: бакет files создан
garage-init: ключ GKc9f714659c3d9a79c2ee624b заведён
garage-init: ключ GKc9f714659c3d9a79c2ee624b: чтение, запись, владелец бакета files
garage-init: готово
```

## 2. Доступ по S3

Значения — в «Секреты» конвейера → «.env» → «Показать»:

- **Access key ID** — `GK` + значение `S3_KEY_HEX` (всего 26 символов);
- **Secret access key** — `S3_SECRET`;
- **регион** — `garage`; **адресация** — path-style
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

Другим приложениям — отдельные ключи с правами на свой бакет: в админке
(«Keys» → «Create key», «Buckets» → бакет → «Allow key»).

Virtual-host адресация (`files.s3.example.com`) тоже включена в
`garage.toml` (`root_domain`), но для неё нужны wildcard-запись DNS и
wildcard-сертификат — проще path-style.

## 3. Админка (garage-webui)

Только на `127.0.0.1` хоста — у неё админ-токен Garage, то есть полный
доступ. Открывать через SSH-туннель:

```sh
ssh -L 3909:127.0.0.1:3909 хост
# в браузере: http://localhost:3909
```

Пароль включается переменной `WEBUI_AUTH` — `логин:bcrypt-хеш`:

```sh
docker run --rm httpd:2.4-alpine htpasswd -nbB admin 'ваш-пароль'
# admin:$2y$05$…
```

Строку целиком — в «Секреты» → «.env» → `WEBUI_AUTH` (в описании не
держите: `$` в хеше), затем «Выложить». Без `WEBUI_AUTH` админка пускает
всех, кто дошёл до порта, — то есть всех, у кого есть SSH на хост.

## 4. Веб-доступ к файлам для клиентов (Filestash)

garage-webui — для администратора. Людям, которым нужно просто положить
или забрать файл через браузер, — Filestash: входят своим ключом S3 и
видят только свои бакеты.

1. Раскомментируйте `filestash` и том `filestash` в `docker-compose.yml`
   (своя копия репозитория) и выложите.
2. **Сразу** задайте пароль администратора: первый, кто откроет Filestash,
   попадёт на `/admin/setup` и задаст его сам. Поэтому сначала через
   туннель:
   ```sh
   ssh -L 8334:127.0.0.1:8334 хост
   # http://localhost:8334 → пароль администратора
   ```
3. В админке Filestash: «Backend» → S3, endpoint `http://garage:3900`,
   регион `garage`.
4. Только потом публикуйте: «Сайты» → новый сайт → `127.0.0.1:8334`
   (если файлы большие — «Предел тела запроса» `0`).

## 5. Резервные копии и обновление

- Данные — в томах `garage_meta` (метаданные, маленький, **важный**) и
  `garage_data` (блоки). Без `meta` блоки бесполезны. Бэкап метаданных
  делает сам Garage (`metadata_auto_snapshot_interval = "6h"`,
  `/var/lib/garage/meta/snapshots`); копируйте оба тома, лучше при
  остановленном `garage`.
- Обновление — правкой тега `dxflrs/garage:` и «Выложить». Между большими
  версиями (1.x → 2.x) бывают миграции метаданных — сначала заметки к
  выпуску и бэкап `meta`.
- Один узел — без избыточности (`replication_factor = 1`): диск умер —
  данные потеряны. Для настоящего хранения — три узла в разных местах
  (другой `garage.toml`, порт `3901` между узлами) или копия бакетов
  куда-то ещё (`rclone sync`).

## Если не работает

| Симптом | Причина |
|---|---|
| `garage-init: … 401` | `GARAGE_ADMIN_TOKEN` в `.env` сменили, а `garage` не перезапустился — «Выложить» ещё раз |
| `413 Request Entity Too Large` у клиента | в `site:` нет `max_body: "0"` (по умолчанию 64m) |
| `SignatureDoesNotMatch` | регион не `garage` или клиент в virtual-host режиме без wildcard — включите path-style |
| `NoSuchBucket` | имя бакета не то, что в `BUCKET`, или ключу не выданы права на этот бакет |
| админка: `Unauthorized` | неверный пароль, или `WEBUI_AUTH` без `admin:` в начале |
