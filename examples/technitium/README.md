# Technitium DNS Server: свой сервер имён, DNS-01 для сертификатов, failover

> **WARNING:** пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются. Проверьте их сами, прежде чем выкладывать в рабочую среду.

[Technitium DNS Server](https://github.com/TechnitiumSoftware/DnsServer) —
авторитетный и рекурсивный DNS-сервер с веб-консолью. В этом примере он —
**сервер имён ваших зон**: два сервера (`ns1` и `ns2`) на разных хостах,
зоны переносятся с первичного на вторичный. На нём:

- **проверка DNS-01** для сертификатов Let's Encrypt — cert-manager в
  Kubernetes (и certbot на хостах) кладёт TXT-запись по RFC 2136 с ключом
  TSIG: wildcard-сертификаты и сертификаты сервисов без открытого 80-го
  порта;
- **failover-записи** — имя отдаёт только живые адреса (приложение
  Failover проверяет их по TCP, HTTP или ping);
- **DoH** `https://dns.example.com/dns-query` за прокси сайта, **DoT** —
  по желанию (Android «Частный DNS»).

Файлы:

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — первичный (ns1): `dns` и разовый `dns-init`;
- [`deploy/docker-compose.secondary.yml`](deploy/docker-compose.secondary.yml) — вторичный (ns2);
- [`deploy/init.py`](deploy/init.py) — первичная настройка через API (ключи, зоны, зона каталога, перенос, Failover);
- [`deploy/nkt-plugin.py`](deploy/nkt-plugin.py) — плагин хаба nkt: зоны и записи из хаба, сценарии, MCP (§8);
- [`pipeline.yaml`](pipeline.yaml), [`pipeline.secondary.yaml`](pipeline.secondary.yaml) — конвейеры;
- [`deploy/cert-manager-issuer.yaml`](deploy/cert-manager-issuer.yaml) — `ClusterIssuer` и пример сертификата;
- [`deploy/certbot-dot-hook.sh`](deploy/certbot-dot-hook.sh) — сертификат для DoT после каждого продления.

## Почему свой compose, а не из их репозитория

| В их `docker-compose.yml` | Здесь |
|---|---|
| `image: …:latest` | версия закреплена (`15.6.2`), новая — правкой тега |
| консоль `5380` на всех адресах | только `127.0.0.1`: `ssh -L 5380:127.0.0.1:5380 хост` |
| `53` на всех адресах | на адрес интерфейса хоста — `ports:` в конвейере (см. ниже) |
| рекурсия по умолчанию | только частным сетям — не открытый резолвер |
| проверка здоровья `/api/dnsClient/healthCheck` | в 15.x требует токен и всегда «плохо» — проверяется ответ консоли |
| всё настраивается в консоли | ключи TSIG, зоны, перенос, DoH за прокси, Failover — разовым `dns-init` через API при каждой выкладке |

## 1. Адреса и имена

Нужно два хоста с белыми адресами (здесь `203.0.113.10` — ns1,
`198.51.100.20` — ns2) и домен (`example.com`).

У регистратора домена:

1. **Glue-записи** (часто «хосты имён» / «child nameservers»):
   `ns1.example.com → 203.0.113.10`, `ns2.example.com → 198.51.100.20`.
2. **A-запись** `dns.example.com → 203.0.113.10` — сайт DoH.
3. Дальше — одно из двух:
   - **только проверки DNS-01** (основной DNS остаётся у регистратора):
     NS-записи подзоны `acme.example.com` → `ns1.example.com` и
     `ns2.example.com`;
   - **весь домен на своих серверах** (нужно для failover-записей в
     `example.com`): NS домена у регистратора — `ns1.example.com` и
     `ns2.example.com`. Сначала заведите в консоли все записи зоны
     (A, MX, TXT…), потом меняйте NS.

## 2. Первичный (ns1)

«Выкладки» → «Новый конвейер» → **«Примеры» → «Technitium DNS Server»**,
выберите хост — описание готово (адрес хоста подставлен в `ports:`), или
вставьте [`pipeline.yaml`](pipeline.yaml). Поправьте:

- `ports:` — адрес **интерфейса** хоста (`ip -4 addr`; в облаке за NAT —
  внутренний адрес). Не `0.0.0.0`: на Ubuntu `127.0.0.53:53` держит
  systemd-resolved, и публикация на все адреса упадёт с «адрес занят».
  Другой путь — выключить заглушку: `DNSStubListener=no` в
  `/etc/systemd/resolved.conf` и `systemctl restart systemd-resolved`;
- в `env:` — `DNS_DOMAIN`, `ZONES`, `ACME_ZONE`, `SECONDARY_IP`,
  `SECONDARY_NS`. Пароль администратора и оба ключа TSIG создаст хаб
  (`generate:`);
- `site:` — `dns.example.com` (DoH).

Сохраните и выложите. `dns-init` в журнале выкладки пишет, что сделал:
ключи `cert-manager` и `zone-transfer`, зоны, перенос только на
`SECONDARY_IP` и только с ключом, NS вторичного в зонах, в `ACME_ZONE` —
обновления только TXT и только с ключом `cert-manager`, приложение
Failover. Повтор при каждой выкладке ничего не ломает; чужие ключи TSIG,
заведённые в консоли, остаются.

Консоль: `ssh -L 5380:127.0.0.1:5380 ns1` → `http://127.0.0.1:5380`,
пользователь `admin`, пароль — `DNS_ADMIN_PASSWORD` («Секреты» → «.env» →
«Показать»). Пароль Technitium берёт из `.env` только при первом запуске:
сменили в консоли — поменяйте и в `.env`, иначе `dns-init` не войдёт.

## 3. Вторичный (ns2)

Второй конвейер на **другом** хосте — [`pipeline.secondary.yaml`](pipeline.secondary.yaml):

- `ports:` — адрес интерфейса этого хоста;
- `PRIMARY_IP` — адрес первичного;
- `TSIG_TRANSFER` — **тот же**, что у первичного (скопируйте из его
  секретов); `ZONES` и `ACME_ZONE` — те же.

`dns-init` заведёт вторичную **зону каталога** (`catalog.nkt.invalid`) и
Failover: зоны первичного приходят с каталогом сами — и те, что из
`ZONES`, и новые, добавленные позже в консоли или из хаба. Изменения с
первичного приходят по NOTIFY примерно через 10 секунд. Проверка:

```bash
dig +short @203.0.113.10 acme.example.com SOA
dig +short @198.51.100.20 acme.example.com SOA   # тот же серийный номер
```

## 4. Сертификаты через cert-manager (DNS-01)

В основной зоне (у регистратора или в Technitium) для каждого имени —
CNAME в подзону проверок:

```
_acme-challenge.app.example.com.  CNAME  app.acme.example.com.
```

В кластере — Secret с ключом и `ClusterIssuer` из
[`deploy/cert-manager-issuer.yaml`](deploy/cert-manager-issuer.yaml):

```bash
kubectl -n cert-manager create secret generic technitium-tsig \
  --from-literal=secret='<TSIG_CERT_MANAGER из секретов конвейера>'
kubectl apply -f cert-manager-issuer.yaml
```

`rfc2136.nameserver` — адрес первичного; поды должны доходить до него по
53/tcp и udp. `cnameStrategy: Follow` — cert-manager пишет TXT туда, куда
ведёт CNAME. Начните с staging-адреса Let's Encrypt (в файле — в
комментарии).

Тот же ключ годится и для certbot на хосте (плагин `dns-rfc2136`):

```ini
# /etc/letsencrypt/rfc2136.ini (chmod 600)
dns_rfc2136_server = 203.0.113.10
dns_rfc2136_name = cert-manager
dns_rfc2136_secret = <TSIG_CERT_MANAGER>
dns_rfc2136_algorithm = HMAC-SHA256
```

Ключ `cert-manager` может менять **только TXT** и **только в
`ACME_ZONE`** — даже утёкший, он не перепишет A-записи.

## 5. Failover-записи

Нужна зона на своих серверах (`ZONES`) и короткий TTL. В консоли
первичного: зона → **Add Record** → тип **APP** → приложение
**Failover**, класс **Failover.Address**, данные:

```json
{
  "primary": ["203.0.113.21"],
  "secondary": ["203.0.113.22"],
  "serverDown": [],
  "healthCheck": "tcp443",
  "allowTxtStatus": true
}
```

TTL — 30–60 с. Имя отдаёт `primary`, пока проверка его проходит, иначе —
`secondary`. С `allowTxtStatus` запрос TXT того же имени показывает
состояние проверок. Проверки (`tcp443`, `https` с адресом, `ping`,
интервал, повторы) — «Apps» → Failover → **Config**.

- **Приложение и его проверки — на обоих серверах.** Запись приходит на
  вторичный переносом, но отвечает на неё приложение самого сервера: без
  Failover вторичный отвечает **SERVFAIL** (`dns-init` ставит
  приложение на оба). Свои проверки, поменянные в Config первичного,
  повторите на вторичном.
- Переключение — не мгновенное: клиенты держат прежний ответ до конца TTL.
  Мгновенно и прозрачно для клиентов переключает Traffic Fly (через свой
  шлюз); можно и вместе — failover-запись выбирает между шлюзами.

## 6. DoH и DoT

**DoH** — сайт `dns.example.com` из конвейера: прокси с сертификатом
Let's Encrypt перед DNS-over-HTTP контейнера. Адрес для браузеров
(«Безопасный DNS»), Windows 11, iOS и macOS:
`https://dns.example.com/dns-query`.

**DoT и DoQ** (Android «Частный DNS» — только DoT, имя
`dns.example.com`): TLS держит сам Technitium, ему нужен сертификат в
формате PFX.

1. На хосте первичного — хук certbot (поправьте `DOMAIN`):
   ```bash
   sudo install -m 0755 certbot-dot-hook.sh /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   sudo sed -i 's/^DOMAIN=.*/DOMAIN=dns.example.com/' /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   sudo RENEWED_LINEAGE=/etc/letsencrypt/live/dns.example.com /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
   ```
   Он кладёт `dns.pfx` и `pfx.pass` в `/srv/compose/technitium/tls/` —
   сейчас и после каждого продления.
2. В `docker-compose.yml` раскомментируйте том `./tls:/etc/dns/tls:ro`
   (каталогом, не файлом — иначе подмену при продлении не видно), в
   конвейере — порты `853/tcp` и `853/udp`. Выложите.
3. В консоли: **Settings → Optional Protocols** — DNS-over-TLS (и
   DNS-over-QUIC), сертификат `/etc/dns/tls/dns.pfx`, пароль — из
   `pfx.pass`.

Новый сертификат после продления Technitium перечитывает сам — меньше
чем за минуту, без перезапуска.

**Свои имена через DoH/DoT отвечают всем, чужие — нет.** Рекурсия
(резолвер для любых имён) открыта только частным сетям: за прокси
Technitium видит настоящий адрес клиента (`X-Real-IP`), и телефону из
мобильной сети придёт REFUSED. Пользоваться сервером как своим
резолвером — перечислите свои сети:

```yaml
env:
  RECURSION: UseSpecifiedNetworkACL
  RECURSION_ACL: 203.0.113.0/24, 2001:db8::/32
```

`RECURSION: Allow` превращает порт 53 в **открытый резолвер** — его
находят за часы и используют для DDoS-усиления. Не включайте.

## 7. Резервные копии и обновление

- Всё состояние — в томе `technitium_config` (`/etc/dns`): зоны, ключи
  TSIG, настройки, приложения. Вторичный восстанавливается переносом, а
  первичный — только из копии:
  ```bash
  docker run --rm -v technitium_config:/data -v "$PWD":/out alpine tar czf /out/technitium-config.tgz -C /data .
  ```
  Или в консоли: **Settings → Backup Settings**.
- Новая версия — правкой тега в обоих compose-файлах и выкладкой: сначала
  вторичный, потом первичный.

## 8. Плагин хаба nkt

Сервис `plugin` — плагин хаба: зоны и записи прямо в хабе, команды
`dns …` в сценариях, инструменты MCP для агентов. Он слушает только
`127.0.0.1:9180` хоста, хаб ходит к нему по SSH, каждый вызов подписан
`NKT_PLUGIN_SECRET` (его создаёт хаб, `generate:hex32`).

Подключить: «О системе» → «Плагины» → «Подключить» → «С конвейера» →
этот конвейер → «Проверить» → «Подключить». Дальше — «Плагины» →
**Technitium DNS**: добавить зону (`example1.com` — ns2 получит её через
каталог), шаблон сайта или почты, записи таблицей или всей зоной текстом
с диффом, проверка делегирования у регистратора, DNSSEC и DS.
Подробно — справка «[Плагин: Technitium DNS](https://piqab.github.io/nkt/guide/hub-plugin-dns)».

Плагин не нужен — удалите сервис `plugin` из compose-файла или просто не
подключайте его в хабе.

## Если не работает

- **Выкладка: «address already in use» на 53** — systemd-resolved, см. §2.
- **`dns-init`: «Failover не установлен … timed out»** — сервер не дошёл
  до каталога приложений (часто — закрыт исходящий DNS по UDP). Включите
  пересылку по DoH: `FORWARDERS: https://cloudflare-dns.com/dns-query (1.1.1.1)`,
  `FORWARDER_PROTOCOL: Https`; или поставьте Failover в консоли («Apps» →
  «App Store»).
- **Вторичный отстал** — серийные номера SOA разные: проверьте
  `SECONDARY_IP` на первичном, `PRIMARY_IP` и одинаковый `TSIG_TRANSFER`,
  53/tcp между хостами.
- **cert-manager: «REFUSED»** — не тот ключ или имя вне `ACME_ZONE`
  (нет CNAME `_acme-challenge` в подзону).
