import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, useApi } from '../api'
import type { Me, Overview } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import {
  Banner,
  Card,
  ErrorNote,
  Loading,
  SeverityBadge,
  StateBadge,
  formatDateTime,
  formatRelative,
} from '../components/ui'

export default function OverviewPage({ me }: { me: Me }) {
  const { data, error, loading, reload } = useApi<Overview>('/overview', 60_000)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  async function rescan() {
    setBusy(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      reload()
      setNotice('Хост пересканирован.')
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (loading && !data) return <Loading what="обзор хоста" />
  if (error && !data) return <ErrorNote error={error} />
  if (!data) return null

  const findings = data.findings
  const worst = (findings.critical ?? 0) + (findings.high ?? 0)
  const av = data.availability

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>{data.host.hostname}</h1>
          <p>
            {data.host.os} · ядро {data.host.kernel || '—'} · последний скан{' '}
            {formatRelative(data.scanned)} ({data.scan_ms} мс)
          </p>
        </div>
        <div className="row">
          {me.is_admin && (
            <button onClick={rescan} disabled={busy}>
              {busy ? 'Сканирую…' : 'Пересканировать'}
            </button>
          )}
        </div>
      </div>

      {notice && <Banner kind="info">{notice}</Banner>}
      {data.host.notes?.map((note) => (
        <Banner key={note} kind="info">
          {note}
        </Banner>
      ))}

      <div className="grid grid-4">
        <StatTile
          label="Проблемы требуют внимания"
          value={formatNumber(worst)}
          note={`критичных ${findings.critical ?? 0}, высоких ${findings.high ?? 0}`}
          tone={worst > 0 ? 'critical' : 'good'}
        />
        <StatTile
          label="Слушателей объявлено"
          value={formatNumber(data.counts.endpoints ?? 0)}
          note={`из них публичных ${data.counts.endpoints_public ?? 0}, с TLS ${data.counts.endpoints_tls ?? 0}`}
        />
        <StatTile
          label="Контейнеры"
          value={`${data.counts.containers_running ?? 0} / ${data.counts.containers ?? 0}`}
          note={`описано в compose: ${data.counts.containers_declared ?? 0}`}
          tone={
            (data.counts.containers ?? 0) > (data.counts.containers_running ?? 0) ? 'warning' : undefined
          }
        />
        <StatTile
          label="Доступность за 24 ч"
          value={`${av.avg_uptime.toFixed(1)}%`}
          note={`целей ${av.targets}: сейчас доступно ${av.up}, недоступно ${av.down}`}
          tone={av.down > 0 ? 'warning' : 'good'}
        />
      </div>

      <div className="grid grid-2">
        <Card
          title="Что сломано"
          subtitle="Отсортировано по серьёзности. Полный список — на вкладке «Проблемы»."
          actions={<Link to="/findings">все проблемы →</Link>}
        >
          {data.top_findings.length === 0 ? (
            <div className="chart-empty">Проблем не найдено.</div>
          ) : (
            <div className="col">
              {data.top_findings.map((f) => (
                <div key={f.id} style={{ borderBottom: '1px solid var(--gridline)', paddingBottom: '0.5rem' }}>
                  <div className="row" style={{ gap: '0.5rem' }}>
                    <SeverityBadge severity={f.severity} />
                    <strong>{f.title}</strong>
                  </div>
                  <div className="small secondary">{f.detail}</div>
                  {f.object && <span className="tag">{f.object}</span>}
                </div>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card title="Сервисы" actions={<Link to="/services">управление →</Link>}>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Сервис</th>
                    <th>Состояние</th>
                    <th>Автозапуск</th>
                    <th className="num">PID</th>
                  </tr>
                </thead>
                <tbody>
                  {data.services.map((s) => (
                    <tr key={s.name}>
                      <td>
                        <strong>{s.name}</strong>
                        <div className="small muted">{s.description}</div>
                      </td>
                      <td>
                        <StateBadge state={s.active_state} />
                      </td>
                      <td className="small">{s.enabled || '—'}</td>
                      <td className="num small">{s.main_pid || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>

          <Card title="Firewall" actions={<Link to="/firewall">правила →</Link>}>
            <div className="row">
              <StateBadge state={data.firewall.ufw_active ? 'active' : 'inactive'} />
              <span className="small secondary">ufw · {data.firewall.ufw_policy || 'политика не прочитана'}</span>
            </div>
            <div className="table-wrap" style={{ marginTop: '0.6rem' }}>
              <table>
                <thead>
                  <tr>
                    <th>Цепочка</th>
                    <th>Политика</th>
                    <th className="num">Пакетов</th>
                  </tr>
                </thead>
                <tbody>
                  {data.firewall.policies
                    .filter((p) => p.table === 'filter')
                    .map((p) => (
                      <tr key={`${p.backend}/${p.chain}`}>
                        <td className="mono">
                          {p.backend}/{p.chain}
                        </td>
                        <td>
                          <StateBadge state={p.policy === 'DROP' || p.policy === 'REJECT' ? 'active' : 'inactive'} />
                          <span className="mono small" style={{ marginLeft: 6 }}>
                            {p.policy}
                          </span>
                        </td>
                        <td className="num">{formatNumber(p.packets)}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </Card>
        </div>
      </div>

      <div className="grid grid-2">
        <Card title="Последние простои" actions={<Link to="/availability">расписание доступности →</Link>}>
          {av.outages.length === 0 ? (
            <div className="chart-empty">За сутки простоев не зафиксировано.</div>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Ресурс</th>
                    <th>Начало</th>
                    <th className="num">Проверок</th>
                    <th>Ошибка</th>
                  </tr>
                </thead>
                <tbody>
                  {av.outages.map((o, i) => (
                    <tr key={i}>
                      <td>{o.label}</td>
                      <td className="small nowrap">{formatDateTime(o.start)}</td>
                      <td className="num">{o.checks}</td>
                      <td className="small mono">{o.error}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        <Card title="Источники данных" subtitle="Что удалось прочитать при последнем скане">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Источник</th>
                  <th>Статус</th>
                  <th>Версия</th>
                  <th className="num">мс</th>
                </tr>
              </thead>
              <tbody>
                {data.sources.map((s) => (
                  <tr key={s.name}>
                    <td>{s.name}</td>
                    <td>
                      <StateBadge state={s.error ? 'failed' : s.available ? 'active' : 'inactive'} />
                      {s.error && <div className="small muted">{s.error}</div>}
                      {s.warnings?.length ? (
                        <div className="small muted">предупреждений: {s.warnings.length}</div>
                      ) : null}
                    </td>
                    <td className="small mono">{s.version || '—'}</td>
                    <td className="num">{s.duration_ms}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      </div>
    </>
  )
}
