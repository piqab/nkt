---
title: 'Плагин: Cloudflare DNS'
---

# Плагин Cloudflare DNS

Для зон, которые живут у Cloudflare: всё, что умеет DNS-плагин хаба, — без
своего сервера имён. Общий принцип плагинов — [Плагины](/guide/hub-plugins);
свой сервер имён — [Technitium DNS](/guide/hub-plugin-dns).

## Поставить

1. **Токен:** dash.cloudflare.com → My Profile → API Tokens → **Create
   Token** → шаблон **Edit zone DNS** → Zone Resources: Include → Specific
   zone — только нужные зоны.
2. «О системе» → «Плагины» → **«Установить на хост»** → Cloudflare DNS →
   машина хаба или любой хост с выходом в интернет → токен; «Только эти
   зоны» — по желанию.
3. Хаб поставит службу `nkt-plugin-cloudflare` (127.0.0.1:9190) и подключит
   её: «Плагины» → Cloudflare DNS.

Второй аккаунт Cloudflare — ещё раз с другим именем (`cloudflare2`).
Контейнером — пример конвейера «Плагин Cloudflare DNS» и «Подключить» → «С
конвейера».

## Что умеет

- **Записи:** таблица (имя, TTL, тип, значение, прокси), добавить,
  удалить; вся зона **текстом** `ИМЯ TTL ТИП ЗНАЧЕНИЕ [proxied]` в окне с
  диффом и историей — меняется только разница. TTL — секунды или `auto`.
  `proxied` — оранжевое облако (только A, AAAA, CNAME). NS вершины
  назначает Cloudflare: в тексте он комментарием и не правится.
- **Делегирование:** какие NS назначил Cloudflare и что видят публичные
  резолверы.
- **Сценарии:**
  ```
  cf set app.example.com A 203.0.113.10 ttl 300
  cf add www.example.com CNAME example.com proxied
  cf rm old.example.com
  cf check example.com
  ```
- **MCP:** `cloudflare_zones`, `cloudflare_records`, `cloudflare_zone_text`,
  `cloudflare_delegation` (чтение), `cloudflare_zone_apply`,
  `cloudflare_record_set`, `cloudflare_record_add`,
  `cloudflare_record_delete`.
- **Для хаба** (`provides: dns`): имена маршрутов Traffic Fly, домены
  сайтов (`site.dns`), автоимена хостов — без прокси Cloudflare (TLS держат
  шлюз или сайт). Failover у Cloudflare — только платный Load Balancing:
  при нескольких шлюзах ставятся все адреса, по кругу.

Зоны заводятся в панели Cloudflare — плагин их не создаёт и не удаляет.
