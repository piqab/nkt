import { useMemo, useState, type FormEvent } from 'react'
import { api, useApi } from '../api'
import type { Certificate, CertificatesResponse, Me, SelfSignedRequest, SelfSignedResult } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, Loading, formatDateTime } from '../components/ui'

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

export default function Certificates({ me }: { me: Me }) {
  const { data, error, loading, reload } = useApi<CertificatesResponse>('/certificates', 300_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  const certs = useMemo(() => data?.certificates ?? [], [data])
  const summary = data?.summary
  const canControl = me.is_admin && me.allow_mutations

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
      const res = await api<{ output: string; simulated: boolean }>('/certificates/renew', {
        method: 'POST',
        body: { lineage },
      })
      const suffix = res.simulated ? ' (симуляция, режим снапшота)' : ''
      setNotice({ kind: 'info', text: `${lineage}: ${res.output || 'продлено'}${suffix}` })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  if (loading && !data) return <Loading what="сертификаты" />
  if (error && !data) return <ErrorNote error={error} />

  return (
    <>
      <div className="page-head">
        <div>
          <h1>TLS-сертификаты</h1>
          <p>
            Читаются файлы, на которые ссылаются директивы <code className="mono">ssl_certificate</code>{' '}
            в nginx и <code className="mono">crt</code> в haproxy. Проверяются сроки, покрытие имён,
            стойкость ключа, то, запустится ли автообновление на самом деле, и — отдельным
            TLS-подключением к сокету — совпадает ли файл на диске с тем, что реально видят клиенты.
          </p>
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
        title="Расписание истечения"
        subtitle="Запас до истечения на общей шкале в год — порядок, в котором сертификаты перестанут работать"
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
          <table>
            <thead>
              <tr>
                <th>Сайты</th>
                <th>Файл</th>
                <th>Действителен до</th>
                <th>Осталось</th>
                <th>Ключ</th>
                <th>Издатель</th>
                <th>Обновление</th>
                <th>На сокете</th>
                {canControl && <th>Действия</th>}
              </tr>
            </thead>
            <tbody>
              {certs.map((cert) => {
                const tone = expiryTone(cert)
                const rTone = renewalTone(cert)
                const sTone = servingTone(cert)
                return (
                  <tr key={cert.id}>
                    <td>
                      <strong>{certName(cert)}</strong>
                      {cert.self_signed && <div className="small muted">самоподписанный</div>}
                      {cert.error && (
                        <div className="small" style={{ color: TONE_COLOR.critical }}>
                          {cert.error}
                        </div>
                      )}
                    </td>
                    <td className="small mono" style={{ wordBreak: 'break-all' }}>
                      {cert.path}
                    </td>
                    <td className="small nowrap">
                      {cert.error ? '—' : formatDateTime(cert.not_after)}
                    </td>
                    <td className="small nowrap" style={{ color: TONE_COLOR[tone] }}>
                      ● {expiryWord(cert)}
                    </td>
                    <td className="small nowrap">
                      {cert.key_algorithm ? `${cert.key_algorithm} ${cert.key_bits}` : '—'}
                      {cert.sig_algorithm && <div className="small muted">{cert.sig_algorithm}</div>}
                    </td>
                    <td className="small">{commonName(cert.issuer)}</td>
                    <td className="small">
                      <span style={{ color: rTone === 'muted' ? 'var(--text-muted)' : TONE_COLOR[rTone] }}>
                        ● {renewalWord(cert)}
                      </span>
                      {cert.renewal.detail && (
                        <div className="small muted">{cert.renewal.detail}</div>
                      )}
                    </td>
                    <td className="small">
                      <span style={{ color: sTone === 'muted' ? 'var(--text-muted)' : TONE_COLOR[sTone] }}>
                        ● {servingWord(cert)}
                      </span>
                      {cert.serving.checked && !cert.serving.error && !cert.serving.match && (
                        <div className="small muted">
                          на сокете действителен до{' '}
                          {cert.serving.served_not_after ? formatDateTime(cert.serving.served_not_after) : '—'}
                        </div>
                      )}
                      {cert.serving.endpoint && (
                        <div className="small muted mono">{cert.serving.endpoint}</div>
                      )}
                    </td>
                    {canControl && (
                      <td className="nowrap">
                        {canRenew(cert) && (
                          <button
                            className="ghost"
                            disabled={busy === cert.id}
                            onClick={() => renew(cert)}
                          >
                            {busy === cert.id ? 'продлеваю…' : 'продлить'}
                          </button>
                        )}
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </Card>

      {me.is_admin && me.allow_mutations && (
        <SelfSignedForm onIssued={reload} />
      )}
    </>
  )
}

const SERVICE_OPTIONS: { value: SelfSignedRequest['service']; label: string }[] = [
  { value: 'nginx', label: 'nginx' },
  { value: 'haproxy', label: 'haproxy' },
]

function SelfSignedForm({ onIssued }: { onIssued: () => void }) {
  const [names, setNames] = useState('')
  const [service, setService] = useState<SelfSignedRequest['service']>('nginx')
  const [bits, setBits] = useState(2048)
  const [days, setDays] = useState(397)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<SelfSignedResult | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    const nameList = names
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
        body: { names: nameList, service, bits, days } satisfies SelfSignedRequest,
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
      title="Выпустить самоподписанный сертификат"
      subtitle="Для внутренних сервисов или как временная мера, пока не готов сертификат от доверенного центра — браузер всё равно покажет предупреждение"
    >
      <form className="col" onSubmit={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <label style={{ flex: 2, minWidth: '16rem' }}>
            Имена через запятую
            <input
              value={names}
              onChange={(e) => setNames(e.target.value)}
              placeholder="internal.example.com, *.internal.example.com"
              required
            />
          </label>
          <label>
            Сервис
            <select value={service} onChange={(e) => setService(e.target.value as SelfSignedRequest['service'])}>
              {SERVICE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Длина ключа
            <select value={bits} onChange={(e) => setBits(Number(e.target.value))}>
              <option value={2048}>2048</option>
              <option value={3072}>3072</option>
              <option value={4096}>4096</option>
            </select>
          </label>
          <label>
            Срок действия, дней
            <input
              type="number"
              min={1}
              max={825}
              value={days}
              onChange={(e) => setDays(Number(e.target.value))}
            />
          </label>
        </div>
        <div>
          <button className="primary" type="submit" disabled={busy}>
            {busy ? 'Генерирую…' : 'Создать'}
          </button>
        </div>
      </form>

      {result && (
        <div className="col" style={{ marginTop: '0.85rem' }}>
          <Banner kind="info">
            Сертификат для {result.names.join(', ')} создан, действителен до{' '}
            {formatDateTime(result.not_after)}. Он ещё не подключён ни к одному сервису — вставьте
            директивы ниже в нужный файл через страницу «Конфигурации».
          </Banner>
          <pre className="diff">{result.snippet}</pre>
        </div>
      )}
    </Card>
  )
}
