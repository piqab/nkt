# WordPress + MariaDB — сайт за прокси с HTTPS

[`deploy/docker-compose.yml`](deploy/docker-compose.yml) — WordPress 7.1
(apache) и MariaDB 11.8; пароли — из `.env`, данные — в томах
`wp-data` и `db-data`.

- **За прокси с HTTPS** WordPress должен знать, что открыт по https:
  `WORDPRESS_CONFIG_EXTRA` в файле доверяет заголовку `X-Forwarded-Proto`
  от прокси сайта nkt — без этого бесконечные перенаправления.
- Порт 80 контейнера не публикуется — сайт публикует его на 127.0.0.1.
- Healthcheck у обоих сервисов: `up --wait` ждёт готовности базы и
  WordPress.

«Примеры» → «WordPress + MariaDB», хост, имя в `site:`, `.env` по
шаблону. После выкладки откройте сайт — мастер установки WordPress.

Проверено: выкладка на docker 29 за ~18 секунд, оба сервиса healthy; за прокси с `X-Forwarded-Proto: https` WordPress перенаправляет на `https://…`, без петли.
