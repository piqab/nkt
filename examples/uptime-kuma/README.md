# Uptime Kuma — один контейнер, данные в ./data

[Uptime Kuma](https://github.com/louislam/uptime-kuma) — мониторинг
доступности сайтов и сервисов. Compose-файл проекта
([compose.yaml](https://github.com/louislam/uptime-kuma/blob/master/compose.yaml))
выкладывается как есть.

| В файле | nkt |
|---|---|
| `image: louislam/uptime-kuma:2` | закреплена основная версия 2 — обновления внутри неё приходят при выкладке |
| `volumes: ./data:/app/data` | данные — в каталоге стека `/srv/compose/uptime-kuma/data`; при удалении конвейера каталог переносится в `.nkt-removed`, данные сохраняются |
| `ports: "3001:3001"` | `bind` по умолчанию: `127.0.0.1:3001`, наружу — через сайт |
| healthcheck в образе | `up --wait` ждёт healthy |

«Примеры» → «Uptime Kuma», выберите хост; блок `site:` — ваше имя. После
выкладки откройте сайт и заведите учётную запись администратора (первый
вход).

Проверено: выкладка на docker 29 за ~80 секунд, контейнер healthy.
