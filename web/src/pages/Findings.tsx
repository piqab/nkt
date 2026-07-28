import { useMemo, useState } from 'react'
import { qs, useApi } from '../api'
import type { Finding, Severity } from '../types'
import { Card, ErrorNote, Loading, SeverityBadge, SEVERITIES, SEVERITY_LABEL } from '../components/ui'
import { formatNumber } from '../components/charts'

interface FindingsResponse {
  findings: Finding[]
  counts: Partial<Record<Severity, number>>
  total: number
}

export default function Findings() {
  const [severity, setSeverity] = useState('')
  const [service, setService] = useState('')
  const [query, setQuery] = useState('')

  const { data, error, loading } = useApi<FindingsResponse>(
    `/findings${qs({ severity, service })}`,
    120_000,
  )

  const services = useMemo(() => {
    const set = new Set<string>()
    data?.findings.forEach((f) => set.add(f.service))
    return [...set].sort()
  }, [data])

  const visible = useMemo(() => {
    if (!data) return []
    const needle = query.trim().toLowerCase()
    if (!needle) return data.findings
    return data.findings.filter((f) =>
      [f.title, f.detail, f.object, f.rule, f.file].some((v) => v?.toLowerCase().includes(needle)),
    )
  }, [data, query])

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Проблемы конфигурации</h1>
          <p>
            Результат сопоставления конфигов nginx, haproxy и docker с реальными слушателями хоста
            и правилами firewall. Каждая запись содержит объяснение и конкретное действие.
          </p>
        </div>
      </div>

      <ErrorNote error={error} />

      <Card>
        <div className="filters">
          <label>
            Серьёзность
            <select value={severity} onChange={(e) => setSeverity(e.target.value)}>
              <option value="">все</option>
              {SEVERITIES.map((s) => (
                <option key={s} value={s}>
                  {SEVERITY_LABEL[s]} ({data?.counts[s] ?? 0})
                </option>
              ))}
            </select>
          </label>
          <label>
            Сервис
            <select value={service} onChange={(e) => setService(e.target.value)}>
              <option value="">все</option>
              {services.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label style={{ flex: 1, minWidth: '14rem' }}>
            Поиск
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="порт, файл, имя правила…"
            />
          </label>
          <span className="small muted" style={{ paddingBottom: '0.4rem' }}>
            показано {formatNumber(visible.length)} из {formatNumber(data?.total ?? 0)}
          </span>
        </div>
      </Card>

      {loading && !data ? (
        <Loading what="список проблем" />
      ) : visible.length === 0 ? (
        <Card>
          <div className="chart-empty">Ничего не найдено под заданные условия.</div>
        </Card>
      ) : (
        <div className="col">
          {visible.map((f) => (
            <Card key={f.id}>
              <div className="spread" style={{ alignItems: 'flex-start' }}>
                <div style={{ minWidth: 0 }}>
                  <div className="row" style={{ marginBottom: '0.25rem' }}>
                    <SeverityBadge severity={f.severity} />
                    <span className="tag">{f.rule}</span>
                    <span className="tag">{f.service}</span>
                    {f.object && <span className="tag">{f.object}</span>}
                  </div>
                  <h3>{f.title}</h3>
                  <p className="secondary" style={{ margin: '0.3rem 0 0' }}>
                    {f.detail}
                  </p>
                  {f.suggestion && (
                    <p style={{ margin: '0.45rem 0 0' }}>
                      <strong>Что сделать: </strong>
                      <span className="secondary">{f.suggestion}</span>
                    </p>
                  )}
                </div>
                {f.file && (
                  <div className="small muted nowrap mono">
                    {f.file}
                    {f.line ? `:${f.line}` : ''}
                  </div>
                )}
              </div>
            </Card>
          ))}
        </div>
      )}
    </>
  )
}
