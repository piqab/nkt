/**
 * Примеры выкладок для окна нового конвейера («Примеры ▾»): ссылка на
 * compose-файл, имя стека, сайт и то, что нужно поправить в чужом файле
 * (images, ports, env_keys) или привезти рядом (files), и шаблон .env.
 * Каждый проверен выкладкой на docker; подробности — README в examples/.
 */
export interface PipelineExample {
  key: string
  link: string
  project: string
  readme: string
  /** maxBody — предел тела запроса у прокси сайта (max_body; "0" — без
   * предела и без буферизации). */
  site?: { service: string; port: number; maxBody?: string }
  /** Образы вместо compose-файла; {hubversion} — версия этого хаба. */
  images?: Record<string, string>
  files?: string[]
  /** Конфиги сервисов из репозитория (configs: target, file, mode). */
  configs?: Record<string, { target: string; file: string; mode?: string }[]>
  /** Публикации вместо compose-файла; {hostip} — адрес выбранного хоста
   * (IPv4 из «Хостов»), иначе заглушка 203.0.113.10 — поправить. */
  ports?: Record<string, string[]>
  envKeys?: Record<string, string[]>
  waitTimeout?: string
  /** Добавить закомментированный вариант входа через Traffic Fly (блок
   * fly:) с HTTP-проверкой по этому пути. */
  flyCheckPath?: string
  /** Блок env описания: имя → значение по порядку. generate:вид — значение
   * создаст хаб при сохранении; {domain} — имя сайта примера. При
   * сохранении всё уходит в .env конвейера («Секреты»). */
  env?: [string, string][]
  /** Свой код и образ nkt — без предупреждения о стороннем коде. */
  own?: boolean
}

const NKT = 'https://github.com/piqab/nkt'

/** Строки-предупреждения в начале описания из примера — те же, что в
 * pipeline.yaml примеров в examples/. */
export const EXAMPLE_WARNING = [
  '# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.',
  '# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.',
]

export const PIPELINE_EXAMPLES: PipelineExample[] = [
  {
    key: 'httpbin',
    link: `${NKT}/blob/main/examples/httpbin/deploy/docker-compose.yml`,
    project: 'httpbin',
    readme: `${NKT}/tree/main/examples/httpbin`,
    site: { service: 'httpbin', port: 8080 },
    // Только latest и только amd64; на arm64 — убрать (останется
    // go-httpbin из compose-файла).
    images: { httpbin: 'cyd01/httpbin:latest' },
  },
  {
    key: 'uptimekuma',
    link: 'https://github.com/louislam/uptime-kuma/blob/master/compose.yaml',
    project: 'uptime-kuma',
    readme: `${NKT}/tree/main/examples/uptime-kuma`,
    site: { service: 'uptime-kuma', port: 3001 },
  },
  {
    key: 'umami',
    link: 'https://github.com/umami-software/umami/blob/master/docker-compose.yml',
    project: 'umami',
    readme: `${NKT}/tree/main/examples/umami`,
    site: { service: 'umami', port: 3000 },
    images: { umami: 'ghcr.io/umami-software/umami:3.4.0' },
    envKeys: { umami: ['APP_SECRET', 'TWO_FACTOR_ENCRYPTION_KEY', 'DATABASE_URL'], db: ['POSTGRES_PASSWORD'] },
    env: [
      ['APP_SECRET', 'generate:hex32'],
      ['TWO_FACTOR_ENCRYPTION_KEY', 'generate:hex32'],
      ['POSTGRES_PASSWORD', 'generate:password'],
      ['DATABASE_URL', 'postgresql://umami:${POSTGRES_PASSWORD}@db:5432/umami'],
    ],
  },
  {
    key: 'n8n',
    link: 'https://github.com/n8n-io/n8n-hosting/blob/main/docker-compose/withPostgres/docker-compose.yml',
    project: 'n8n',
    readme: `${NKT}/tree/main/examples/n8n`,
    site: { service: 'n8n', port: 5678 },
    files: ['docker-compose/withPostgres/init-data.sh'],
    envKeys: { n8n: ['WEBHOOK_URL', 'N8N_PROXY_HOPS'] },
    env: [
      ['N8N_VERSION', '2.41.4'],
      ['POSTGRES_USER', 'postgres'],
      ['POSTGRES_PASSWORD', 'generate:password'],
      ['POSTGRES_DB', 'n8n'],
      ['POSTGRES_NON_ROOT_USER', 'n8n'],
      ['POSTGRES_NON_ROOT_PASSWORD', 'generate:password'],
      ['RUNNERS_AUTH_TOKEN', 'generate:hex16'],
      ['WEBHOOK_URL', 'https://{domain}/'],
      ['N8N_PROXY_HOPS', '1'],
    ],
  },
  {
    key: 'forgejo',
    link: `${NKT}/blob/main/examples/forgejo/deploy/docker-compose.yml`,
    project: 'forgejo',
    readme: `${NKT}/tree/main/examples/forgejo`,
    site: { service: 'forgejo', port: 3000 },
    ports: { forgejo: ['0.0.0.0:2222:2222'] },
    envKeys: { 'forgejo-admin': ['FORGEJO_ADMIN_USER', 'FORGEJO_ADMIN_PASSWORD', 'FORGEJO_ADMIN_EMAIL'] },
    env: [
      ['POSTGRES_PASSWORD', 'generate:password'],
      ['FORGEJO_DOMAIN', '{domain}'],
      ['FORGEJO_ROOT_URL', 'https://{domain}/'],
      ['FORGEJO_ADMIN_USER', 'gitadmin'],
      ['FORGEJO_ADMIN_PASSWORD', 'generate:password'],
      ['FORGEJO_ADMIN_EMAIL', 'admin@example.com'],
    ],
  },
  {
    key: 'forgejo-runner',
    link: `${NKT}/blob/main/examples/forgejo-runner/deploy/docker-compose.yml`,
    project: 'forgejo-runner',
    readme: `${NKT}/tree/main/examples/forgejo-runner`,
    files: ['examples/forgejo-runner/deploy/config.yml'],
    envKeys: { 'runner-register': ['FORGEJO_URL', 'RUNNER_TOKEN', 'RUNNER_NAME', 'RUNNER_LABELS'] },
    env: [
      ['FORGEJO_URL', 'https://git.example.com'],
      ['RUNNER_TOKEN', 'paste-registration-token-here'],
      ['RUNNER_NAME', 'build1'],
      ['RUNNER_LABELS', 'ubuntu-latest:docker://ghcr.io/catthehacker/ubuntu:act-22.04,docker:docker://docker:29.9-cli'],
    ],
  },
  {
    key: 'wordpress',
    link: `${NKT}/blob/main/examples/wordpress/deploy/docker-compose.yml`,
    project: 'wordpress',
    readme: `${NKT}/tree/main/examples/wordpress`,
    site: { service: 'wordpress', port: 80 },
    env: [
      ['DB_PASSWORD', 'generate:password'],
      ['DB_ROOT_PASSWORD', 'generate:password'],
    ],
  },
  {
    key: 'authorizer',
    link: `${NKT}/blob/main/examples/authorizer/deploy/docker-compose.yml`,
    project: 'authorizer',
    readme: `${NKT}/tree/main/examples/authorizer`,
    site: { service: 'authorizer', port: 8080 },
    flyCheckPath: '/healthz',
    env: [
      ['AUTH_DOMAIN', '{domain}'],
      ['ALLOWED_ORIGINS', 'https://{domain}'],
      ['CLIENT_ID', 'generate:hex16'],
      ['CLIENT_SECRET', 'generate:hex32'],
      ['ADMIN_SECRET', 'generate:hex32'],
      ['JWT_SECRET', 'generate:hex32'],
      ['ENCRYPTION_KEY', 'generate:hex32'],
      ['POSTGRES_PASSWORD', 'generate:password'],
    ],
  },
  {
    key: 'technitium',
    link: `${NKT}/blob/main/examples/technitium/deploy/docker-compose.yml`,
    project: 'technitium',
    readme: `${NKT}/tree/main/examples/technitium`,
    // DoH: прокси сайта перед DNS-over-HTTP (порт 80 контейнера).
    site: { service: 'dns', port: 80 },
    // init.py — первичная настройка; nkt-plugin.py — плагин хаба (сервис plugin).
    files: ['examples/technitium/deploy/init.py', 'examples/technitium/deploy/nkt-plugin.py'],
    // DNS — на адрес хоста, не 0.0.0.0 (127.0.0.53:53 у systemd-resolved).
    ports: { dns: ['{hostip}:53:53/udp', '{hostip}:53:53/tcp', '127.0.0.1:5380:5380'] },
    env: [
      ['DNS_DOMAIN', 'ns1.example.com'],
      ['DNS_ADMIN_PASSWORD', 'generate:password'],
      ['ZONES', 'example.com'],
      ['ACME_ZONE', 'acme.example.com'],
      ['SECONDARY_IP', ''],
      ['SECONDARY_NS', 'ns2.example.com'],
      ['TSIG_CERT_MANAGER', 'generate:base64-32'],
      ['TSIG_TRANSFER', 'generate:base64-32'],
      // Секрет подписи вызовов плагина: хаб возьмёт его из .env сам.
      ['NKT_PLUGIN_SECRET', 'generate:hex32'],
    ],
  },
  {
    key: 'cloudflare-plugin',
    link: `${NKT}/blob/main/examples/cloudflare/deploy/docker-compose.yml`,
    project: 'cloudflare-plugin',
    readme: `${NKT}/tree/main/examples/cloudflare`,
    files: ['examples/cloudflare/deploy/nkt-plugin.py'],
    envKeys: { plugin: ['CF_API_TOKEN', 'NKT_PLUGIN_SECRET'] },
    env: [
      ['CF_API_TOKEN', 'paste-cloudflare-token-here'],
      ['NKT_PLUGIN_SECRET', 'generate:hex32'],
    ],
  },
  {
    key: 'docker-mailserver',
    link: `${NKT}/blob/main/examples/docker-mailserver/deploy/docker-compose.yml`,
    project: 'mail',
    readme: `${NKT}/tree/main/examples/docker-mailserver`,
    // Почта — наружу на все адреса хоста, не 127.0.0.1.
    ports: { mailserver: ['0.0.0.0:25:25', '0.0.0.0:465:465', '0.0.0.0:587:587', '0.0.0.0:993:993'] },
    envKeys: { 'mail-init': ['MAIL_DOMAIN', 'MAIL_ADMIN_PASSWORD'], mailserver: ['MAIL_HOSTNAME', 'MAIL_DOMAIN'] },
    env: [
      ['MAIL_HOSTNAME', 'mail.example.com'],
      ['MAIL_DOMAIN', 'example.com'],
      ['MAIL_ADMIN_PASSWORD', 'generate:password'],
    ],
  },
  {
    key: 'litellm',
    link: `${NKT}/blob/main/examples/litellm/deploy/docker-compose.yml`,
    project: 'litellm',
    readme: `${NKT}/tree/main/examples/litellm`,
    // API наружу: длинные контексты и файлы — без предела размера.
    site: { service: 'litellm', port: 4000, maxBody: '0' },
    envKeys: { 'litellm-db': ['LITELLM_DB_PASSWORD'], litellm: ['LITELLM_DB_PASSWORD', 'LITELLM_MASTER_KEY', 'LITELLM_SALT_KEY'] },
    env: [
      ['LITELLM_DB_PASSWORD', 'generate:hex32'],
      ['LITELLM_MASTER_KEY', 'generate:hex32'],
      ['LITELLM_SALT_KEY', 'generate:hex32'],
    ],
  },
  {
    key: 'garage',
    link: `${NKT}/blob/main/examples/garage/deploy/docker-compose.yml`,
    project: 'garage',
    readme: `${NKT}/tree/main/examples/garage`,
    // S3 API за прокси: файлы любого размера — потоком.
    site: { service: 'garage', port: 3900, maxBody: '0' },
    files: ['examples/garage/deploy/init.py'],
    configs: { garage: [{ target: '/etc/garage.toml', file: 'examples/garage/deploy/garage.toml', mode: '0400' }] },
    env: [
      ['S3_DOMAIN', '{domain}'],
      ['BUCKET', 'files'],
      ['CAPACITY', '100G'],
      ['GARAGE_RPC_SECRET', 'generate:hex32'],
      ['GARAGE_ADMIN_TOKEN', 'generate:hex32'],
      ['S3_KEY_HEX', 'generate:hex12'],
      ['S3_SECRET', 'generate:hex32'],
    ],
  },
  {
    key: 'nkthub',
    link: `${NKT}/blob/main/deploy/docker-compose.hub.release.yml`,
    project: 'nkt-hub-test',
    readme: `${NKT}/tree/main/examples/nkt-hub-test`,
    site: { service: 'hub', port: 8077 },
    own: true,
    // Версия — как у этого хаба: в томе тестового хаба ключ и секреты,
    // latest сдвинулся бы при следующем релизе.
    images: { hub: 'ghcr.io/piqab/nkt-hub:{hubversion}' },
    // Не 8443/8446 — их может держать сам хаб на этой машине.
    ports: { hub: ['127.0.0.1:18443:8077', '127.0.0.1:18446:8446'] },
    envKeys: { hub: ['NKT_BOOTSTRAP_ADMIN_PASSWORD'] },
    env: [['NKT_BOOTSTRAP_ADMIN_PASSWORD', 'generate:password']],
  },
  {
    key: 'plausible',
    link: 'https://github.com/plausible/community-edition/blob/v3.2.1/compose.yml',
    project: 'plausible',
    readme: `${NKT}/tree/main/examples/plausible`,
    site: { service: 'plausible', port: 8000 },
    files: ['clickhouse/'],
    envKeys: { plausible_db: ['POSTGRES_PASSWORD'] },
    waitTimeout: '10m',
    env: [
      ['BASE_URL', 'https://{domain}'],
      ['SECRET_KEY_BASE', 'generate:base64-48'],
      ['TOTP_VAULT_KEY', 'generate:base64-32'],
      ['POSTGRES_PASSWORD', 'generate:password'],
      ['DATABASE_URL', 'postgres://postgres:${POSTGRES_PASSWORD}@plausible_db:5432/plausible_db'],
      ['DISABLE_REGISTRATION', 'invite_only'],
    ],
  },
]
