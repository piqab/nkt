import { useMemo } from 'react'
import { useApi } from '../api'
import type { Certificate, CertificatesResponse } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Card, ErrorNote, Loading, formatDateTime } from '../components/ui'

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
  if (cert.renewal.automatic) return 'автоматическое'
  if (cert.renewal.managed) return 'настроено, но не запускается'
  if (cert.renewal.tool === 'certbot') return 'запись certbot потеряна'
  return 'вручную'
}

function renewalTone(cert: Certificate): Tone | 'muted' {
  if (cert.renewal.automatic) return 'good'
  if (cert.renewal.managed) return 'warning'
  if (cert.renewal.tool === 'certbot') return 'critical'
  return 'muted'
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

export default function Certificates() {
  const { data, error, loading } = useApi<CertificatesResponse>('/certificates', 300_000)

  const certs = useMemo(() => data?.certificates ?? [], [data])
  const summary = data?.summary

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
            стойкость ключа и то, запустится ли автообновление на самом деле.
          </p>
        </div>
      </div>

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
                <th>Обслуживает</th>
              </tr>
            </thead>
            <tbody>
              {certs.map((cert) => {
                const tone = expiryTone(cert)
                const rTone = renewalTone(cert)
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
                    <td className="small mono">{(cert.endpoints ?? []).join(', ') || '—'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  )
}
