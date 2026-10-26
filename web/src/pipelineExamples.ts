/**
 * Примеры выкладок для окна нового конвейера («Примеры ▾»): ссылка на
 * compose-файл, имя стека, сайт и то, что нужно поправить в чужом файле
 * (images, ports, env_keys) или привезти рядом (files), и шаблон .env.
 * Каждый проверен выкладкой на docker; подробности — README в examples/.
 *
 * С 1.16.0 сами примеры — не здесь, а в каталоге плагинов и примеров
 * (examples/<пример>/nkt-example.json → /hub/catalog): новый пример
 * появляется без выпуска nkt.
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
  /** Порядок в меню. */
  order?: number
  /** Заголовок README из каталога — если перевода заголовка нет. */
  catalogTitle?: string
}

/** Строки-предупреждения в начале описания из примера — те же, что в
 * pipeline.yaml примеров в examples/. */
export const EXAMPLE_WARNING = [
  '# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.',
  '# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.',
]

/** Примеры из ответа /hub/catalog (уже по порядку). */
export function catalogExamples(data?: { examples?: { key: string; title: string; ui?: PipelineExample }[] } | null): PipelineExample[] {
  return (data?.examples ?? []).filter((e) => e.ui?.key).map((e) => ({ ...(e.ui as PipelineExample), catalogTitle: e.title }))
}
