import { useCallback, useEffect, useRef, useState } from 'react'
import { blurText } from '../privacy'
import { Button, Input, Progress, Select, Spin, Tag, Tooltip, type TableColumnsType } from 'antd'
import { RedoOutlined } from '@ant-design/icons'
import { AIExplain } from '../components/AIExplain'

/** Задания «Выкладок» — без разбора ошибки моделью: их журнал сам говорит,
 * что не так (сухой прогон, порт сайта, старый хост), и лампочка там
 * только мешает. */
const NO_AI_KINDS = new Set(['deploy.run', 'pipeline.remove', 'site.setup', 'site.remove', 'site.apply', 'compose.deploy', 'compose.remove'])
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import { wsURL } from '../hooks/usePty'
import type { Job, JobLogLine, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { confirmAction } from '../components/confirm'
import { TitleHelp } from '../components/Docs'

/** Как часто перечитывать список. Задание может закончиться в любой
 * момент, а список, который врёт полминуты, хуже пустого. */
const LIST_POLL_MS = 5_000

/** Запасной опрос журнала, когда сокет не открылся или оборвался. Живой
 * поток быстрее, но обязателен именно этот путь: с ним раздел работает и
 * там, где веб-сокеты режет прокси. */
const LOG_POLL_MS = 2_000

const STATUS_COLOR: Record<string, string> = {
  queued: 'default',
  running: 'processing',
  succeeded: 'success',
  failed: 'error',
  canceled: 'default',
  interrupted: 'warning',
}

const JOB_STATUSES = ['queued', 'running', 'succeeded', 'failed', 'canceled', 'interrupted'] as const

/** Фильтр раздела — запоминается в этом браузере, как у журнала
 * оповещений. */
interface JobFilter {
  statuses: string[]
  kinds: string[]
  q: string
  pageSize: number
  asc: boolean
}

const JOB_FILTER_KEY = 'nkt-jobs-filter'
const JOB_FILTER_DEFAULT: JobFilter = { statuses: [], kinds: [], q: '', pageSize: 50, asc: false }

function loadJobFilter(): JobFilter {
  try {
    const raw = localStorage.getItem(JOB_FILTER_KEY)
    if (raw) {
      const f = { ...JOB_FILTER_DEFAULT, ...JSON.parse(raw) }
      return [20, 50, 100].includes(f.pageSize) ? f : { ...f, pageSize: 50 }
    }
  } catch {
    // нет хранилища — фильтр по умолчанию
  }
  return JOB_FILTER_DEFAULT
}

export default function Jobs({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [filter, setFilterState] = useState<JobFilter>(loadJobFilter)
  const [text, setText] = useState(filter.q)
  const [page, setPage] = useState(1)
  const setFilter = (patch: Partial<JobFilter>) => {
    setPage(1)
    setFilterState((f) => {
      const next = { ...f, ...patch }
      try {
        localStorage.setItem(JOB_FILTER_KEY, JSON.stringify(next))
      } catch {
        // не запомнится — не страшно
      }
      return next
    })
  }
  const jobs = useApi<{ jobs: Job[]; active: number; total?: number; kinds?: string[] }>(
    `/jobs${qs({
      q: filter.q,
      status: filter.statuses.join(','),
      kind: filter.kinds.join(','),
      limit: filter.pageSize,
      offset: (page - 1) * filter.pageSize,
      order: filter.asc ? 'asc' : '',
    })}`,
    LIST_POLL_MS,
  )
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const filtered = filter.statuses.length > 0 || filter.kinds.length > 0 || !!filter.q

  const columns: TableColumnsType<Job> = [
    {
      title: t('jobs.colTitle'),
      key: 'title',
      render: (_, j) => (
        <div style={{ minWidth: '14rem' }}>
          <strong>{blurText(j.title || j.kind)}</strong>
          <div className="small muted mono">{j.kind}</div>
        </div>
      ),
    },
    {
      title: t('jobs.colStatus'),
      key: 'status',
      render: (_, j) => (
        <span className="nowrap">
          <Tag color={STATUS_COLOR[j.status] ?? 'default'}>{t(`jobs.status.${j.status}`)}</Tag>
          {j.error && <InfoHint>{j.error}</InfoHint>}
        </span>
      ),
    },
    {
      title: t('jobs.colStep'),
      key: 'step',
      render: (_, j) =>
        // steps === 100 — задание сообщает проценты (бэкап, копирование),
        // а не номер шага: полоса вместо «45/100».
        j.steps === 100 && !isJobDone(j) ? (
          <div style={{ minWidth: '9rem' }}>
            <Progress percent={j.step} size="small" status="active" />
            {j.step_name && <div className="small muted">{j.step_name}</div>}
          </div>
        ) : j.steps > 0 ? (
          <span className="small nowrap">
            {j.step}/{j.steps}
            {j.step_name && <div className="small muted">{j.step_name}</div>}
          </span>
        ) : (
          <span className="small muted">{j.step_name || '—'}</span>
        ),
    },
    {
      title: t('jobs.colStarted'),
      key: 'started',
      // Порядок — на сервере: по дате заводятся и номера заданий.
      sorter: true,
      sortOrder: filter.asc ? 'ascend' : 'descend',
      sortDirections: ['descend', 'ascend', 'descend'],
      render: (_, j) => (
        <span className="small nowrap">{j.started_at ? formatDateTime(j.started_at) : formatDateTime(j.created_at)}</span>
      ),
    },
    {
      title: t('jobs.colDuration'),
      key: 'duration',
      render: (_, j) => <span className="small nowrap">{duration(j, t)}</span>,
    },
    {
      title: '',
      key: 'actions',
      className: 'nowrap',
      render: (_, j) => (
        <div className="row row-nowrap">
          <RowAction action="logs" label={t('jobs.openLog')} onClick={() => setOpenJob(j)} />
          {!isJobDone(j) && me.is_admin && me.allow_mutations && (
            <RowAction
              action="destroy"
              label={t('jobs.cancel')}
              danger
              onClick={async () => {
                if (!(await confirmAction(t('jobs.confirmCancel', { title: j.title || j.kind })))) return
                await api(`/jobs/${j.id}/cancel`, { method: 'POST' }).catch(() => undefined)
                jobs.reload()
              }}
            />
          )}
        </div>
      ),
    },
  ]

  const list = jobs.data?.jobs ?? []
  const total = jobs.data?.total ?? list.length

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('jobs.title')}
          <TitleHelp>{t('jobs.hint')}</TitleHelp>
        </h1>
      </div>

      <ErrorNote error={jobs.error} />

      <Card
        title={t('jobs.listTitle')}
        subtitle={
          (filtered ? t('jobs.listFiltered', { total }) + ' · ' : '') + t('jobs.listSubtitle', { count: jobs.data?.active ?? 0 })
        }
      >
        <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center', marginBottom: '0.5rem' }}>
          <Select
            mode="multiple"
            allowClear
            style={{ minWidth: 200 }}
            placeholder={t('jobs.filterStatus')}
            value={filter.statuses}
            onChange={(v: string[]) => setFilter({ statuses: v })}
            options={JOB_STATUSES.map((s) => ({ value: s, label: t(`jobs.status.${s}`) }))}
          />
          <Select
            mode="multiple"
            allowClear
            style={{ minWidth: 220 }}
            placeholder={t('jobs.filterKind')}
            value={filter.kinds}
            onChange={(v: string[]) => setFilter({ kinds: v })}
            options={(jobs.data?.kinds ?? []).map((k) => ({ value: k, label: k }))}
          />
          <Input.Search
            allowClear
            style={{ maxWidth: 300 }}
            placeholder={t('jobs.filterText')}
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              if (!e.target.value) setFilter({ q: '' })
            }}
            onSearch={(v) => setFilter({ q: v.trim() })}
          />
          {filtered && (
            <Button
              size="small"
              type="link"
              onClick={() => {
                setText('')
                setFilter({ statuses: [], kinds: [], q: '' })
              }}
            >
              {t('jobs.filterReset')}
            </Button>
          )}
        </div>
        {jobs.loading && !jobs.data ? (
          <Loading what={t('jobs.loading')} />
        ) : list.length === 0 ? (
          <p className="small muted">{filtered ? t('jobs.emptyFiltered') : t('jobs.empty')}</p>
        ) : (
          <div className="table-wrap">
            <DataTable<Job>
              dataSource={list}
              columns={columns}
              rowKey="id"
              tableLayout="auto"
              pagination={{
                current: page,
                pageSize: filter.pageSize,
                total,
                showSizeChanger: true,
                pageSizeOptions: [20, 50, 100],
                size: 'small',
                onChange: (p, size) => {
                  if (size !== filter.pageSize) setFilter({ pageSize: size })
                  else setPage(p)
                },
              }}
              onChange={(_p, _f, sorter) => {
                const s = Array.isArray(sorter) ? sorter[0] : sorter
                if (s?.columnKey === 'started') setFilter({ asc: s.order === 'ascend' })
              }}
            />
          </div>
        )}
      </Card>

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
    </>
  )
}

export function isJobDone(j: Job): boolean {
  return ['succeeded', 'failed', 'canceled', 'interrupted'].includes(j.status)
}

function duration(j: Job, t: (k: string, o?: Record<string, unknown>) => string): string {
  const from = j.started_at || j.created_at
  if (!from) return '—'
  const end = j.finished_at ? new Date(j.finished_at).getTime() : Date.now()
  const secs = Math.max(0, Math.round((end - new Date(from).getTime()) / 1000))
  if (secs < 60) return t('jobs.seconds', { count: secs })
  if (secs < 3600) return t('jobs.minutes', { count: Math.round(secs / 60) })
  return t('jobs.hours', { count: Math.round(secs / 360) / 10 })
}

/**
 * Журнал одного задания. Экспортируется: то же окно открывают разделы,
 * которые задание запускают (профили, образы машин) — им незачем свой.
 *
 * Строки берутся из базы («после какой мы уже видели»), а сокет лишь
 * досылает новые. Поэтому вкладку можно закрыть и вернуться через час:
 * журнал соберётся целиком, а не с момента подключения. Если сокет не
 * открылся, включается опрос — раздел работает и без него.
 */
/** Строка чек-листа («  ✓ …», «  ✗ …», «  ! …») подсвечивается по знаку;
 * остальные строки — как есть. */
function LogLine({ text, last }: { text: string; last: boolean }) {
  // ✓ — успех (цветной значок); ✗ — ошибка, «!» и «?» — предупреждение
  // (журналы выкладки и сухого прогона): вся строка жирная, своим цветом.
  const m = /^(\s*)([✓✗!?])(\s.*)$/s.exec(text)
  const color = m ? (m[2] === '✓' ? 'var(--series-3)' : m[2] === '✗' ? 'var(--series-8)' : 'var(--series-4)') : undefined
  const loud = !!m && m[2] !== '✓'
  return (
    <>
      {m ? (
        <>
          {m[1]}
          <span style={{ color, fontWeight: 600 }}>{m[2]}</span>
          <span style={loud ? { color, fontWeight: 600 } : undefined}>{blurText(m[3])}</span>
        </>
      ) : (
        blurText(text)
      )}
      {last ? '' : '\n'}
    </>
  )
}

export function JobLogModal({
  job,
  onClose,
  scope = '',
  onDone,
}: {
  job: Job
  onClose: () => void
  /** Приставка пути к API — например «/hosts/local» для заданий самого
   * хаба, открытых из списка хостов, где область запросов не выбрана. */
  scope?: string
  /** Вызывается один раз, когда открытое задание завершилось (любым
   * исходом) — список хостов по этому сигналу обновляет строки. */
  onDone?: (job: Job) => void
}) {
  const { t } = useTranslation()
  // «Попробовать снова» открывает в этом же окне новое задание —
  // openJob сменяется, журнал начинается заново.
  const [openJob, setOpenJob] = useState<Job>(job)
  const [lines, setLines] = useState<JobLogLine[]>([])
  const [current, setCurrent] = useState<Job>(job)
  const [live, setLive] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [retryError, setRetryError] = useState<string | null>(null)
  const lastSeq = useRef(0)
  const bodyRef = useRef<HTMLPreElement | null>(null)

  // Дочитываний может идти несколько разом: сообщение из сокета, запасной
  // опрос и переоткрытие окна. Пока предыдущее не вернулось, номер
  // последней строки не сдвинут — и второй запрос приносит тот же хвост.
  // Отсюда и брались повторы вроде трёх «Готово» подряд.
  const fetching = useRef(false)

  const fetchTail = useCallback(async () => {
    if (fetching.current) return null
    fetching.current = true
    try {
      const res = await api<{ job: Job; lines: JobLogLine[] }>(
        `${scope}/jobs/${openJob.id}/log${qs({ after: lastSeq.current })}`,
      )
      setCurrent(res.job)
      // Отбор по номеру строки, а не по «пришло что-то»: сверяемся с тем,
      // что уже показано, — это единственное, что знает правду о
      // показанном, даже если запросов было несколько.
      setLines((prev) => {
        const seen = prev.length > 0 ? prev[prev.length - 1].seq : 0
        const fresh = res.lines.filter((l) => l.seq > seen)
        if (fresh.length === 0) return prev
        lastSeq.current = fresh[fresh.length - 1].seq
        return [...prev, ...fresh]
      })
      return res.job
    } catch {
      // Сеть моргнула — следующий заход дочитает то же самое: номер
      // последней строки не сдвинулся.
      return null
    } finally {
      fetching.current = false
    }
  }, [openJob.id, scope])

  // Первый заход всегда через базу — до всякого сокета.
  useEffect(() => {
    void fetchTail()
  }, [fetchTail])

  // Живой поток. Строки из сокета не складываются в состояние напрямую:
  // он может пропустить событие под нагрузкой, поэтому по каждому сигналу
  // дочитывается хвост из базы — единственный источник правды.
  useEffect(() => {
    if (isJobDone(openJob)) return
    let closed = false
    let ws: WebSocket | null = null
    try {
      ws = new WebSocket(wsURL(`${scope}/jobs/${openJob.id}/ws`))
    } catch {
      return
    }
    ws.onopen = () => !closed && setLive(true)
    ws.onmessage = () => void fetchTail()
    ws.onclose = () => {
      if (!closed) setLive(false)
    }
    ws.onerror = () => {
      if (!closed) setLive(false)
    }
    return () => {
      closed = true
      ws?.close()
    }
  }, [openJob, scope, fetchTail])

  // Запасной опрос: работает, пока задание идёт и живого потока нет.
  useEffect(() => {
    if (live || isJobDone(current)) return
    const timer = setInterval(() => void fetchTail(), LOG_POLL_MS)
    return () => clearInterval(timer)
  }, [live, current, fetchTail])

  useEffect(() => {
    const el = bodyRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines])

  const doneNotified = useRef<number | null>(null)
  useEffect(() => {
    if (!onDone || !isJobDone(current) || doneNotified.current === current.id) return
    doneNotified.current = current.id
    onDone(current)
  }, [current, onDone])

  async function retry() {
    setRetrying(true)
    setRetryError(null)
    try {
      const res = await api<{ job_id: number }>(`${scope}/jobs/${openJob.id}/retry`, { method: 'POST' })
      const next = await api<Job>(`${scope}/jobs/${res.job_id}`)
      lastSeq.current = 0
      setLines([])
      setCurrent(next)
      setOpenJob(next)
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : String(err))
    } finally {
      setRetrying(false)
    }
  }
  const canRetry = isJobDone(current) && current.status !== 'succeeded' && current.resumable

  return (
    <Modal title={blurText(current.title || current.kind)} onClose={onClose} maskClosable={false} width={860} sizeKey="job">
      <div className="row" style={{ marginBottom: '0.5rem' }}>
        <Tag color={STATUS_COLOR[current.status] ?? 'default'}>{t(`jobs.status.${current.status}`)}</Tag>
        {current.steps > 0 && current.steps !== 100 && (
          <span className="small muted">
            {t('jobs.stepOf', { step: current.step, steps: current.steps })}
            {current.step_name ? ` · ${current.step_name}` : ''}
          </span>
        )}
        {!isJobDone(current) && !live && <span className="small muted">{t('jobs.polling')}</span>}
        {canRetry && (
          <Tooltip title={t('jobs.retryHint')}>
            <Button size="small" type="primary" icon={<RedoOutlined />} loading={retrying} onClick={() => void retry()} style={{ marginLeft: 'auto' }}>
              {t('jobs.retry')}
            </Button>
          </Tooltip>
        )}
      </div>
      {/* Прогресс долгой операции: у заданий с шагами — доля шагов, у
          сообщающих проценты (steps = 100) — сами проценты; шаг без
          процентов (сохранение образа, virsh define) — индикатор занятости
          с названием. */}
      {!isJobDone(current) && current.steps > 0 && (
        <div style={{ marginBottom: '0.5rem' }}>
          <Progress percent={Math.round((current.step / current.steps) * 100)} status="active" />
          {current.steps === 100 && current.step_name && <div className="small muted">{current.step_name}</div>}
        </div>
      )}
      {!isJobDone(current) && current.steps === 0 && current.step_name && (
        <div className="small muted" style={{ marginBottom: '0.5rem' }}>
          <Spin size="small" /> {current.step_name}
        </div>
      )}

      {current.status === 'interrupted' && <Banner kind="warn">{t('jobs.interruptedHint')}</Banner>}
      {current.error && (
        <Banner kind="error">
          <span className="row row-nowrap" style={{ gap: '0.4rem', alignItems: 'flex-start' }}>
            <span>{current.error}</span>
            {/* Ошибка задания — тот же случай, что находка: объяснить и
                подсказать, что делать. */}
            {!NO_AI_KINDS.has(current.kind) && (
              <AIExplain ctx={{ kind: 'job-error', title: current.title || current.kind, detail: current.error, service: current.kind }} />
            )}
          </span>
        </Banner>
      )}
      <ErrorNote error={retryError} />

      <pre ref={bodyRef} className="diff mono modal-fill" style={{ maxHeight: '26rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
        {lines.length === 0 ? t('jobs.noOutput') : lines.map((l, i) => <LogLine key={l.seq ?? i} text={l.text} last={i === lines.length - 1} />)}
      </pre>
    </Modal>
  )
}
