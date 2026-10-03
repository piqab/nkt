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
  site: { service: string; port: number }
  images?: Record<string, string>
  files?: string[]
  ports?: Record<string, string[]>
  envKeys?: Record<string, string[]>
  waitTimeout?: string
  /** Строки шаблона .env (в описание — комментарием). */
  envTemplate?: string[]
}

const NKT = 'https://github.com/piqab/nkt'

export const PIPELINE_EXAMPLES: PipelineExample[] = [
  {
    key: 'httpbin',
    link: `${NKT}/blob/main/examples/httpbin/deploy/docker-compose.yml`,
    project: 'httpbin',
    readme: `${NKT}/tree/main/examples/httpbin`,
    site: { service: 'httpbin', port: 8080 },
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
    envTemplate: [
      'APP_SECRET=<openssl rand -hex 32>',
      'TWO_FACTOR_ENCRYPTION_KEY=<openssl rand -hex 32>',
      'POSTGRES_PASSWORD=<пароль базы>',
      'DATABASE_URL=postgresql://umami:<пароль базы>@db:5432/umami',
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
    envTemplate: [
      'N8N_VERSION=2.41.4',
      'POSTGRES_USER=postgres',
      'POSTGRES_PASSWORD=<пароль администратора базы>',
      'POSTGRES_DB=n8n',
      'POSTGRES_NON_ROOT_USER=n8n',
      'POSTGRES_NON_ROOT_PASSWORD=<пароль базы n8n>',
      'RUNNERS_AUTH_TOKEN=<openssl rand -hex 16>',
      'WEBHOOK_URL=https://n8n.example.com/',
      'N8N_PROXY_HOPS=1',
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
    envTemplate: [
      'POSTGRES_PASSWORD=<пароль базы>',
      'FORGEJO_DOMAIN=git.example.com',
      'FORGEJO_ROOT_URL=https://git.example.com/',
      'FORGEJO_ADMIN_USER=<логин администратора>',
      'FORGEJO_ADMIN_PASSWORD=<пароль администратора>',
      'FORGEJO_ADMIN_EMAIL=<почта администратора>',
    ],
  },
  {
    key: 'wordpress',
    link: `${NKT}/blob/main/examples/wordpress/deploy/docker-compose.yml`,
    project: 'wordpress',
    readme: `${NKT}/tree/main/examples/wordpress`,
    site: { service: 'wordpress', port: 80 },
    envTemplate: ['DB_PASSWORD=<пароль базы WordPress>', 'DB_ROOT_PASSWORD=<пароль root MariaDB>'],
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
    envTemplate: [
      'BASE_URL=https://plausible.example.com',
      'SECRET_KEY_BASE=<openssl rand -base64 48>',
      'TOTP_VAULT_KEY=<openssl rand -base64 32>',
      'POSTGRES_PASSWORD=<пароль базы>',
      'DATABASE_URL=postgres://postgres:<пароль базы>@plausible_db:5432/plausible_db',
      'DISABLE_REGISTRATION=invite_only',
    ],
  },
]
