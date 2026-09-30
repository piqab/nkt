/**
 * Примеры выкладок для окна нового конвейера («Примеры ▾»): ссылка на
 * compose-файл, имя стека, сайт и — для стеков с секретами в compose-файле
 * — env_keys и шаблон .env. Подробности — README в examples/ репозитория.
 */
export interface PipelineExample {
  key: string
  link: string
  project: string
  readme: string
  site: { service: string; port: number }
  envKeys?: Record<string, string[]>
  /** Строки шаблона .env (в описание — комментарием). */
  envTemplate?: string[]
}

export const PIPELINE_EXAMPLES: PipelineExample[] = [
  {
    key: 'httpbin',
    link: 'https://github.com/piqab/nkt/blob/main/examples/httpbin/deploy/docker-compose.yml',
    project: 'httpbin',
    readme: 'https://github.com/piqab/nkt/tree/main/examples/httpbin',
    site: { service: 'httpbin', port: 8080 },
  },
  {
    key: 'umami',
    link: 'https://github.com/umami-software/umami/blob/master/docker-compose.yml',
    project: 'umami',
    readme: 'https://github.com/piqab/nkt/tree/main/examples/umami',
    site: { service: 'umami', port: 3000 },
    envKeys: { umami: ['APP_SECRET', 'TWO_FACTOR_ENCRYPTION_KEY', 'DATABASE_URL'], db: ['POSTGRES_PASSWORD'] },
    envTemplate: [
      'APP_SECRET=<openssl rand -hex 32>',
      'TWO_FACTOR_ENCRYPTION_KEY=<openssl rand -hex 32>',
      'POSTGRES_PASSWORD=<пароль базы>',
      'DATABASE_URL=postgresql://umami:<пароль базы>@db:5432/umami',
    ],
  },
]
