import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Form, Input, InputNumber, Select, Table, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type {
  Certificate,
  CertificatesResponse,
  CombineResult,
  LineageInfo,
  Me,
  RenewEvent,
  RenewJobStatus,
  SelfSignedRequest,
  SelfSignedResult,
} from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, Spinner, formatDateTime } from '../components/ui'

/** How often to poll a running renew job for new progress lines. */
const RENEW_POLL_MS = 800

/** Expiry bands, matching the thresholds the analyzer uses. */
const WARN_DAYS = 30
const CRITICAL_DAYS = 7
/** Horizon of the runway bar: a year of validity fills it. */
const SCALE_DAYS = 365

type Tone = 'good' | 'warning' | 'serious' | 'critical'

const TONE_COLOR: Record<Tone, string> = {
  good: 'var(--status-good)',
  warning: 'var(--status-warning)',
  serious: 'var(--status-serious)',
  critical: 'var(--status-critical)',
}

function expiryTone(cert: Certificate): Tone {
  if (cert.error) return 'critical'
  if (cert.days_left < 0) return 'critical'
  if (cert.days_left <= CRITICAL_DAYS) return 'serious'
  if (cert.days_left <= WARN_DAYS) return 'warning'
  return 'good'
}

/** Status is never colour alone: every bar and badge carries this wording. */
function expiryWord(cert: Certificate): string {
  if (cert.error) return 'не читается'
  if (cert.days_left < 0) return `просрочен на ${-cert.days_left} дн.`
  if (cert.days_left === 0) return 'истекает сегодня'
  return `${cert.days_left} дн.`
}

function renewalWord(cert: Certificate): string {
  const prefix = cert.renewal.derived ? 'копия certbot, ' : ''
  if (cert.renewal.automatic) return prefix + 'автоматическое'
  if (cert.renewal.managed) return prefix + 'настроено, но не запускается'
  if (cert.renewal.tool === 'certbot') return prefix + 'запись certbot потеряна'
  return 'вручную'
}

/** A "Продлить" button only makes sense when certbot itself can act on this
 * lineage — an orphan lineage or a manually managed path has nothing for
 * `certbot renew --cert-name` to find. */
function canRenew(cert: Certificate): boolean {
  return cert.renewal.tool === 'certbot' && cert.renewal.managed && !!cert.renewal.lineage
}

function renewalTone(cert: Certificate): Tone | 'muted' {
  if (cert.renewal.automatic) return 'good'
  if (cert.renewal.managed) return 'warning'
  if (cert.renewal.tool === 'certbot') return 'critical'
  return 'muted'
}

/** What the socket actually hands back, checked by dialing it directly. This
 * is the only signal in the app that does not trust the file on disk. */
function servingWord(cert: Certificate): string {
  if (!cert.serving.checked) return 'не проверялось'
  if (cert.serving.error) return 'не отвечает'
  return cert.serving.match ? 'совпадает' : 'отличается от файла'
}

function servingTone(cert: Certificate): Tone | 'muted' {
  if (!cert.serving.checked || cert.serving.error) return 'muted'
  return cert.serving.match ? 'good' : 'critical'
}

function certName(cert: Certificate): string {
  if (cert.names?.length) return cert.names.join(', ')
  if (cert.sites?.length) return cert.sites.join(', ')
  return cert.path
}

/** Pull CN out of an RFC 2253 distinguished name for compact display. */
function commonName(dn?: string): string {
  if (!dn) return '—'
  for (const part of dn.split(',')) {
    const trimmed = part.trim()
    if (trimmed.startsWith('CN=')) return trimmed.slice(3)
  }
  return dn
}

function certColumns(
  canControl: boolean,
  busy: string | null,
  renew: (cert: Certificate) => void,
): TableColumnsType<Certificate> {
  const columns: TableColumnsType<Certificate> = [
    {
      title: 'Сайты',
      key: 'name',
      render: (_, cert) => (
        <>
          <strong>{certName(cert)}</strong>
          {cert.self_signed && <div className="small muted">самоподписанный</div>}
          {cert.error && (
            <div className="small" style={{ color: TONE_COLOR.critical }}>
              {cert.error}
            </div>
          )}
        </>
      ),
    },
    {
      title: 'Файл',
      key: 'path',
      render: (_, cert) => (
        <span className="small mono" style={{ wordBreak: 'break-all' }}>
          {cert.path}
        </span>
      ),
    },
    {
      title: 'Действителен до',
      key: 'not_after',
      render: (_, cert) => <span className="small nowrap">{cert.error ? '—' : formatDateTime(cert.not_after)}</span>,
    },
    {
      title: 'Осталось',
      key: 'days_left',
      render: (_, cert) => (
        <span className="small nowrap" style={{ color: TONE_COLOR[expiryTone(cert)] }}>
          ● {expiryWord(cert)}
        </span>
      ),
    },
    {
      title: 'Ключ',
      key: 'key',
      render: (_, cert) => (
        <span className="small nowrap">
          {cert.key_algorithm ? `${cert.key_algorithm} ${cert.key_bits}` : '—'}
          {cert.sig_algorithm && <div className="small muted">{cert.sig_algorithm}</div>}
        </span>
      ),
    },
    { title: 'Издатель', key: 'issuer', render: (_, cert) => <span className="small">{commonName(cert.issuer)}</span> },
    {
      title: 'Обновление',
      key: 'renewal',
      render: (_, cert) => {
        const rTone = renewalTone(cert)
        return (
          <>
            <span style={{ color: rTone === 'muted' ? 'var(--text-muted)' : TONE_COLOR[rTone] }}>
              ● {renewalWord(cert)}
            </span>
            {cert.renewal.detail && <div className="small muted">{cert.renewal.detail}</div>}
          </>
        )
      },
    },
    {
      title: 'На сокете',
      key: 'serving',
      render: (_, cert) => {
        const sTone = servingTone(cert)
        return (
          <>
            <span style={{ color: sTone === 'muted' ? 'var(--text-muted)' : TONE_COLOR[sTone] }}>
              ● {servingWord(cert)}
            </span>
            {cert.serving.checked && !cert.serving.error && !cert.serving.match && (
              <div className="small muted">
                на сокете действителен до{' '}
                {cert.serving.served_not_after ? formatDateTime(cert.serving.served_not_after) : '—'}
              </div>
            )}
            {cert.serving.endpoint && <div className="small muted mono">{cert.serving.endpoint}</div>}
          </>
        )
      },
    },
  ]
  if (canControl) {
    columns.push({
      title: 'Действия',
      key: 'actions',
      render: (_, cert) =>
        canRenew(cert) && (
          <Button type="link" size="small" loading={busy === cert.id} onClick={() => renew(cert)}>
            {busy === cert.id ? 'продлеваю…' : 'продлить'}
          </Button>
        ),
    })
  }
  return columns
}

export default function Certificates({ me }: { me: Me }) {
  const { data, error, loading, reload } = useApi<CertificatesResponse>('/certificates', 300_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  // Shared by both certbot flows — renewing an existing lineage and issuing
  // a brand-new certificate both run in the background and report progress
  // through the same job registry (RenewJobStatus), so one poller/Modal
  // serves either kind; label is what tells them apart on screen.
  const [job, setJob] = useState<{ id: string; label: string } | null>(null)
  const [jobStatus, setJobStatus] = useState<RenewJobStatus | null>(null)

  const certs = useMemo(() => data?.certificates ?? [], [data])
  const summary = data?.summary
  const canControl = me.is_admin && me.allow_mutations

  // Shared with both the "неподключённые сертификаты" card and CombineForm's
  // own dropdown, rather than each fetching it separately — a lineage on
  // disk doesn't change when something gets wired to it or reloaded, only
  // its "attached" status (derived below from `certs`) does.
  const lineages = useApi<{ lineages: LineageInfo[] }>('/certificates/lineages', 60_000)
  // A lineage counts as attached once some parsed endpoint's certificate
  // resolves back to it — Certificate.renewal.lineage is already how the
  // "продлить" button finds the right `certbot renew --cert-name`, so the
  // same field is the correct join key here too.
  const attachedLineages = useMemo(
    () => new Set(certs.map((c) => c.renewal.lineage).filter((l): l is string => !!l)),
    [certs],
  )
  const unattachedLineages = useMemo(
    () => (lineages.data?.lineages ?? []).filter((l) => !attachedLineages.has(l.name)),
    [lineages.data, attachedLineages],
  )

  // Polls the running job — stopping services, certbot's own output,
  // recombining any haproxy copy, restarting services can together take
  // minutes, so this shows it happening instead of one long spinner.
  useEffect(() => {
    if (!job) return
    const jobId = job.id
    let cancelled = false
    let timer: number | undefined

    async function poll() {
      try {
        const status = await api<RenewJobStatus>(`/certificates/renew/${jobId}`)
        if (cancelled) return
        setJobStatus(status)
        if (status.done) {
          window.clearInterval(timer)
          reload()
        }
      } catch (err) {
        if (cancelled) return
        setJobStatus({ events: [], done: true, error: err instanceof Error ? err.message : String(err) })
        window.clearInterval(timer)
      }
    }

    void poll()
    timer = window.setInterval(poll, RENEW_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [job, reload])

  function startJob(id: string, label: string) {
    setJobStatus(null)
    setJob({ id, label })
  }

  async function renew(cert: Certificate) {
    const lineage = cert.renewal.lineage
    if (!lineage) return
    const caveat = cert.renewal.derived
      ? `\n\nЭто копия сертификата из ${cert.renewal.source_path} — продлится оригинал, а эта копия ` +
        'будет автоматически пересобрана из нового сертификата и ключа, после чего перечитается сервис.'
      : ''
    if (!window.confirm(`Запустить certbot renew --cert-name ${lineage}?${caveat}`)) return
    setBusy(cert.id)
    setNotice(null)
    try {
      const res = await api<{ job: string }>('/certificates/renew', {
        method: 'POST',
        body: { lineage },
      })
      startJob(res.job, `Продление ${lineage}`)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  /** Same endpoint "продлить" uses, just keyed by a bare lineage name
   * instead of a Certificate — an unattached lineage has no Certificate
   * object (nothing parsed points at it), but certbot itself doesn't care
   * either way: `certbot renew --cert-name X` works on any lineage it
   * manages, attached or not. */
  async function renewLineage(lineageName: string) {
    if (!window.confirm(`Запустить certbot renew --cert-name ${lineageName}?`)) return
    setBusy(`lineage:${lineageName}`)
    setNotice(null)
    try {
      const res = await api<{ job: string }>('/certificates/renew', {
        method: 'POST',
        body: { lineage: lineageName },
      })
      startJob(res.job, `Продление ${lineageName}`)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  function closeJobModal() {
    setJob(null)
    setJobStatus(null)
  }

  /** Called once CombineForm has written the combined PEM. A plain reload()
   * would only re-fetch /certificates' last cached scan (handleCertificates
   * serves LatestOrScan, not a live parse) — the combined file's own new
   * expiry/coverage would stay invisible until *something* rescans the
   * host, same reasoning as Overview's own post-update rescan. Runs the
   * same /inventory/refresh the rest of the app already uses for this,
   * so CombineForm's result banner can show the shared "Пересканирую…"
   * spinner instead of a bespoke one. */
  async function refreshAfterCombine() {
    setRescanning(true)
    try {
      await api('/inventory/refresh', { method: 'POST' })
    } catch {
      // Best-effort: the combine itself already succeeded and reported its
      // own result — a failed rescan just means the certificate list stays
      // one scan stale, not that anything about the combine failed.
    } finally {
      reload()
      setRescanning(false)
    }
  }

  if (loading && !data) return <Loading what="сертификаты" />
  if (error && !data) return <ErrorNote error={error} />

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            TLS-сертификаты
            <InfoHint>
              Читаются файлы, на которые ссылаются директивы ssl_certificate в nginx и crt в haproxy.
              Проверяются сроки, покрытие имён, стойкость ключа, то, запустится ли автообновление на
              самом деле, и — отдельным TLS-подключением к сокету — совпадает ли файл на диске с тем,
              что реально видят клиенты.
            </InfoHint>
          </h1>
        </div>
      </div>

      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      <div className="grid grid-4">
        <StatTile label="Сертификатов" value={formatNumber(summary?.total ?? 0)} />
        <StatTile
          label="Просрочено"
          value={formatNumber(summary?.expired ?? 0)}
          tone={(summary?.expired ?? 0) > 0 ? 'critical' : 'good'}
        />
        <StatTile
          label="Истекают в течение месяца"
          value={formatNumber(summary?.expiring ?? 0)}
          tone={(summary?.expiring ?? 0) > 0 ? 'warning' : 'good'}
        />
        <StatTile
          label="Без автообновления"
          value={formatNumber(summary?.unmanaged ?? 0)}
          note={
            (summary?.unreadable ?? 0) > 0 ? `не читается файлов: ${summary?.unreadable}` : undefined
          }
          tone={(summary?.unmanaged ?? 0) > 0 ? 'warning' : 'good'}
        />
      </div>

      <Card
        title={
          <>
            Расписание истечения
            <InfoHint>
              Запас до истечения на общей шкале в год — порядок, в котором сертификаты перестанут
              работать
            </InfoHint>
          </>
        }
      >
        {certs.length === 0 ? (
          <div className="chart-empty">
            В разобранных конфигурациях нет ни одной директивы ssl_certificate.
          </div>
        ) : (
          <div className="col" style={{ gap: '0.45rem' }}>
            {certs.map((cert) => {
              const tone = expiryTone(cert)
              const fraction = cert.error
                ? 0
                : Math.max(0, Math.min(cert.days_left / SCALE_DAYS, 1))
              return (
                <div key={cert.id} className="row" style={{ gap: '0.75rem', flexWrap: 'nowrap' }}>
                  <span
                    className="small"
                    style={{ width: '15rem', flex: '0 0 15rem', overflow: 'hidden', textOverflow: 'ellipsis' }}
                    title={certName(cert)}
                  >
                    {certName(cert)}
                  </span>
                  <span
                    style={{
                      flex: 1,
                      height: 12,
                      background: 'var(--wash)',
                      borderRadius: 4,
                      overflow: 'hidden',
                      minWidth: '6rem',
                    }}
                  >
                    <span
                      style={{
                        display: 'block',
                        width: `${fraction * 100}%`,
                        height: '100%',
                        background: TONE_COLOR[tone],
                        borderRadius: 4,
                      }}
                    />
                  </span>
                  <span
                    className="small nowrap"
                    style={{ width: '11rem', flex: '0 0 11rem', color: TONE_COLOR[tone] }}
                  >
                    ● {expiryWord(cert)}
                  </span>
                </div>
              )
            })}
          </div>
        )}
        <p className="small muted" style={{ marginBottom: 0 }}>
          Пороги: {WARN_DAYS} дн. — предупреждение, {CRITICAL_DAYS} дн. — срочно. Let's Encrypt
          продлевает за 30 дней до конца, поэтому меньший запас означает, что автоматика уже
          не сработала.
        </p>
      </Card>

      <Card title="Подробности">
        <div className="table-wrap">
          <Table<Certificate>
            dataSource={certs}
            rowKey="id"
            pagination={false}
            size="small"
            columns={certColumns(canControl, busy, renew)}
          />
        </div>
      </Card>

      {unattachedLineages.length > 0 && (
        <UnattachedCard lineages={unattachedLineages} canControl={canControl} busy={busy} onRenew={renewLineage} />
      )}

      {me.is_admin && me.allow_mutations && (
        <>
          <IssueForm onStarted={startJob} />
          <CombineForm
            onCombined={refreshAfterCombine}
            rescanning={rescanning}
            lineages={lineages.data?.lineages ?? []}
            lineagesLoading={lineages.loading}
            lineagesError={lineages.error}
          />
          <SelfSignedForm onIssued={reload} />
        </>
      )}

      {job && (
        <Modal title={job.label} onClose={closeJobModal} maskClosable={false}>
          <RenewLog events={jobStatus?.events ?? []} />
          {jobStatus?.done ? (
            <Banner kind={jobStatus.error ? 'error' : 'info'}>
              {jobStatus.error ? `Ошибка: ${jobStatus.error}` : 'Готово.'}
            </Banner>
          ) : (
            <p className="small muted row" style={{ alignItems: 'center', marginBottom: 0 }}>
              <Spinner />
              Выполняется — можно закрыть окно, процесс на хосте продолжится в фоне.
            </p>
          )}
        </Modal>
      )}
    </>
  )
}

/** Lineages certbot manages on disk (/etc/letsencrypt/live) that no parsed
 * endpoint references at all — `certbot certonly` (whether run by hand or
 * through "Выпустить новый сертификат" above) only writes the certificate
 * files, it never wires them into nginx/haproxy/Caddy itself, so a
 * freshly issued certificate is invisible in "Подробности" above until
 * something actually points at it. This is that "something exists but
 * nothing uses it yet" list — read-only detail plus the one action that
 * still makes sense without an endpoint (certbot renew doesn't care
 * whether anything serves the cert it's renewing). */
function UnattachedCard({
  lineages,
  canControl,
  busy,
  onRenew,
}: {
  lineages: LineageInfo[]
  canControl: boolean
  busy: string | null
  onRenew: (name: string) => void
}) {
  const columns: TableColumnsType<LineageInfo> = [
    {
      title: 'Домен',
      key: 'name',
      render: (_, info) => (
        <>
          <strong>{info.name}</strong>
          {info.name_unicode && <div className="small muted">{info.name_unicode}</div>}
        </>
      ),
    },
    {
      title: 'Осталось',
      key: 'days_left',
      render: (_, info) => {
        if (!info.known) return <span className="small muted">срок неизвестен</span>
        const tone: Tone =
          info.days_left < 0
            ? 'critical'
            : info.days_left <= CRITICAL_DAYS
              ? 'serious'
              : info.days_left <= WARN_DAYS
                ? 'warning'
                : 'good'
        const word =
          info.days_left < 0
            ? `просрочен на ${-info.days_left} дн.`
            : info.days_left === 0
              ? 'истекает сегодня'
              : `${info.days_left} дн.`
        return (
          <span className="small nowrap" style={{ color: TONE_COLOR[tone] }}>
            ● {word}
          </span>
        )
      },
    },
  ]
  if (canControl) {
    columns.push({
      title: '',
      key: 'actions',
      render: (_, info) => (
        <Button
          type="link"
          size="small"
          loading={busy === `lineage:${info.name}`}
          onClick={() => onRenew(info.name)}
        >
          продлить
        </Button>
      ),
    })
  }

  return (
    <Card
      title={
        <>
          Неподключённые сертификаты
          <InfoHint>
            Есть в /etc/letsencrypt/live, но ни один разобранный конфиг на них не ссылается — сюда
            попадает всё, что certbot certonly выпустил (вручную или через форму выше), но никто ещё
            не подключил к сервису
          </InfoHint>
        </>
      }
    >
      <div className="table-wrap">
        <Table<LineageInfo> dataSource={lineages} rowKey="name" pagination={false} size="small" columns={columns} />
      </div>
      <p className="small muted" style={{ marginBottom: 0, marginTop: '0.6rem' }}>
        Чтобы сертификат появился в «Подробности» выше — подключите его в конфиг сервиса (страница
        «Конфигурации») или соберите PEM для haproxy формой ниже.
      </p>
    </Card>
  )
}

/** Auto-scrolling live log of a renew job's progress, one line per step. */
function RenewLog({ events }: { events: RenewEvent[] }) {
  const preRef = useRef<HTMLPreElement>(null)

  useEffect(() => {
    const el = preRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [events.length])

  if (events.length === 0) {
    return <p className="small muted">Начинаю…</p>
  }

  return (
    <pre ref={preRef} className="diff" style={{ maxHeight: '22rem' }}>
      {events.map((e) => `[${new Date(e.time).toLocaleTimeString('ru-RU')}] ${e.text}`).join('\n')}
    </pre>
  )
}

/** Shows expiry inline so picking a lineage doesn't require checking the
 * certificates table first. */
function lineageLabel(info: LineageInfo): string {
  const name = info.name_unicode ? `${info.name} (${info.name_unicode})` : info.name
  if (!info.known) return `${name} — срок неизвестен`
  if (info.days_left < 0) return `${name} — просрочен ${-info.days_left} дн. назад`
  if (info.days_left === 0) return `${name} — истекает сегодня`
  return `${name} — ${info.days_left} дн.`
}

/** Requests a brand-new Let's Encrypt certificate for domain(s) certbot
 * doesn't manage yet — unlike "продлить" (an existing lineage) and unlike
 * the self-signed form below (no real CA involved at all). Runs in the
 * background through the same job/progress Modal "продлить" already uses. */
function IssueForm({ onStarted }: { onStarted: (jobId: string, label: string) => void }) {
  const [form] = Form.useForm<{ domains: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(values: { domains: string }) {
    const domainList = values.domains
      .split(',')
      .map((d) => d.trim())
      .filter(Boolean)
    if (domainList.length === 0) {
      setError('Укажите хотя бы одно доменное имя')
      return
    }
    if (
      !window.confirm(
        `Выпустить сертификат для ${domainList.join(', ')}?\n\n` +
          'certbot certonly --standalone требует свободный порт 80/443 — nginx и haproxy будут ' +
          'ненадолго остановлены и автоматически запущены обратно после завершения.',
      )
    ) {
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job: string }>('/certificates/issue', {
        method: 'POST',
        body: { domains: domainList },
      })
      form.resetFields()
      onStarted(res.job, `Выпуск сертификата: ${domainList.join(', ')}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title={
        <>
          Выпустить новый сертификат Let's Encrypt
          <InfoHint>
            Для домена, у которого ещё нет сертификата — certbot certonly --standalone.
            Wildcard-имена не поддерживаются: standalone доказывает владение только одним точным
            именем за раз.
          </InfoHint>
        </>
      }
    >
      <Form form={form} layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="domains" label="Доменные имена через запятую" rules={[{ required: true }]} style={{ flex: 1, minWidth: '18rem' }}>
            <Input placeholder="new.example.com, www.new.example.com" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? 'Запускаю…' : 'Выпустить'}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}

const NEW_FILE = ''

function CombineForm({
  onCombined,
  rescanning,
  lineages: lineageOptions,
  lineagesLoading,
  lineagesError,
}: {
  onCombined: () => void
  /** Whether the host is being rescanned right now to pick up the combined
   * file's new expiry/coverage — see Certificates' own refreshAfterCombine. */
  rescanning: boolean
  lineages: LineageInfo[]
  lineagesLoading: boolean
  lineagesError: string | null
}) {
  const haproxyPaths = useApi<{ paths: string[] }>('/certificates/haproxy-paths', 0)
  const [lineage, setLineage] = useState('')
  const [targetPath, setTargetPath] = useState(NEW_FILE)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<CombineResult | null>(null)

  const pathOptions = haproxyPaths.data?.paths ?? []

  async function submit() {
    if (!lineage) {
      setError('Выберите lineage')
      return
    }
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await api<CombineResult>('/certificates/combine', {
        method: 'POST',
        body: { lineage, target_path: targetPath },
      })
      setResult(res)
      onCombined()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title={
        <>
          Собрать PEM для haproxy из certbot
          <InfoHint>
            Берёт уже выпущенный certbot-сертификат из /etc/letsencrypt/live и склеивает его с ключом
            в один файл — то, что требует haproxy crt. certbot renew не вызывается.
          </InfoHint>
        </>
      }
    >
      <Form layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <ErrorNote error={lineagesError} />
        <div className="filters">
          <Form.Item label="Lineage (/etc/letsencrypt/live/…)" style={{ flex: 1, minWidth: '18rem' }}>
            <Select
              value={lineage || undefined}
              onChange={setLineage}
              placeholder="— выберите —"
              options={lineageOptions.map((info) => ({ value: info.name, label: lineageLabel(info) }))}
            />
          </Form.Item>
          <Form.Item label="Куда записать" style={{ flex: 1, minWidth: '18rem' }}>
            <Select
              value={targetPath}
              onChange={setTargetPath}
              options={[{ value: NEW_FILE, label: '— новый файл —' }, ...pathOptions.map((path) => ({ value: path, label: path }))]}
            />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy} disabled={lineageOptions.length === 0}>
            {busy ? 'Собираю…' : 'Собрать'}
          </Button>
        </Form.Item>
        {!lineagesLoading && lineageOptions.length === 0 && !lineagesError && (
          <p className="small muted" style={{ marginBottom: 0 }}>
            В /etc/letsencrypt/live не найдено ни одной lineage.
          </p>
        )}
      </Form>

      {result &&
        (result.snippet ? (
          <div className="col" style={{ marginTop: '0.85rem' }}>
            <Banner kind="info">
              {result.lineage}: PEM собран, действителен до {formatDateTime(result.not_after)}. Он ещё не
              подключён ни к одному сервису — вставьте директиву ниже в нужный файл через страницу
              «Конфигурации».
            </Banner>
            <pre className="diff">{result.snippet}</pre>
          </div>
        ) : (
          <div style={{ marginTop: '0.85rem' }}>
            <Banner kind="info">
              {result.lineage}: файл <code className="mono">{result.combined_path}</code> записан,
              действителен до {formatDateTime(result.not_after)}. haproxy перечитал конфигурацию — вставлять
              ничего не нужно.{' '}
              {rescanning ? (
                <span className="row" style={{ display: 'inline-flex', alignItems: 'center', gap: '0.4rem' }}>
                  <Spinner /> обновляю список сертификатов…
                </span>
              ) : (
                'Список сертификатов уже обновлён.'
              )}
            </Banner>
          </div>
        ))}
    </Card>
  )
}

const SERVICE_OPTIONS: { value: SelfSignedRequest['service']; label: string }[] = [
  { value: 'nginx', label: 'nginx' },
  { value: 'haproxy', label: 'haproxy' },
]

type SelfSignedFormValues = { names: string; service: SelfSignedRequest['service']; bits: number; days: number }

function SelfSignedForm({ onIssued }: { onIssued: () => void }) {
  const [form] = Form.useForm<SelfSignedFormValues>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<SelfSignedResult | null>(null)

  async function submit(values: SelfSignedFormValues) {
    const nameList = values.names
      .split(',')
      .map((n) => n.trim())
      .filter(Boolean)
    if (nameList.length === 0) {
      setError('Укажите хотя бы одно имя')
      return
    }
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await api<SelfSignedResult>('/certificates/self-signed', {
        method: 'POST',
        body: { names: nameList, service: values.service, bits: values.bits, days: values.days } satisfies SelfSignedRequest,
      })
      setResult(res)
      onIssued()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title={
        <>
          Выпустить самоподписанный сертификат
          <InfoHint>
            Для внутренних сервисов или как временная мера, пока не готов сертификат от доверенного
            центра — браузер всё равно покажет предупреждение
          </InfoHint>
        </>
      }
    >
      <Form<SelfSignedFormValues>
        form={form}
        layout="vertical"
        onFinish={submit}
        initialValues={{ service: 'nginx', bits: 2048, days: 397 }}
      >
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="names" label="Имена через запятую" rules={[{ required: true }]} style={{ flex: 2, minWidth: '16rem' }}>
            <Input placeholder="internal.example.com, *.internal.example.com" />
          </Form.Item>
          <Form.Item name="service" label="Сервис">
            <Select options={SERVICE_OPTIONS} />
          </Form.Item>
          <Form.Item name="bits" label="Длина ключа">
            <Select options={[2048, 3072, 4096].map((n) => ({ value: n, label: String(n) }))} />
          </Form.Item>
          <Form.Item name="days" label="Срок действия, дней">
            <InputNumber min={1} max={825} />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? 'Генерирую…' : 'Создать'}
          </Button>
        </Form.Item>
      </Form>

      {result && (
        <div className="col" style={{ marginTop: '0.85rem' }}>
          <Banner kind="info">
            Сертификат для {result.names.join(', ')} создан, действителен до{' '}
            {formatDateTime(result.not_after)}. Он ещё не подключён ни к одному сервису — вставьте
            директивы ниже в нужный файл через страницу «Конфигурации».
          </Banner>
          {result.unicode_names && (
            <p className="small muted">
              {Object.entries(result.unicode_names)
                .map(([ascii, unicode]) => `${unicode} → ${ascii}`)
                .join('; ')}
            </p>
          )}
          <pre className="diff">{result.snippet}</pre>
        </div>
      )}
    </Card>
  )
}
