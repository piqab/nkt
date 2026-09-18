import { useMemo, useState } from 'react'
import { Checkbox, Input, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs, useApi } from '../api'
import type { Finding, Severity } from '../types'
import { Card, ErrorNote, InfoHint, Loading, SeverityBadge, SEVERITIES, severityLabel } from '../components/ui'
import { formatNumber } from '../components/charts'

interface FindingsResponse {
  findings: Finding[]
  counts: Partial<Record<Severity, number>>
  total: number
}

export default function Findings() {
  const { t } = useTranslation()
  const [severity, setSeverity] = useState('')
  const [service, setService] = useState('')
  const [query, setQuery] = useState('')

  const { data, error, loading } = useApi<FindingsResponse>(
    `/findings${qs({ severity, service })}`,
    120_000,
  )
  // Что появилось с прошлого просмотра — из того же сравнения снимков,
  // что и карточка «Что изменилось» на обзоре: помечается тегом, и
  // список можно сузить до новых.
  const changes = useApi<{ changes: { kind: string; key: string; action: string }[] }>('/changes', 120_000)
  const [onlyNew, setOnlyNew] = useState(false)
  const newIDs = useMemo(() => {
    const set = new Set<string>()
    for (const c of changes.data?.changes ?? []) if (c.kind === 'finding' && c.action === 'appeared') set.add(c.key)
    return set
  }, [changes.data])

  const services = useMemo(() => {
    const set = new Set<string>()
    data?.findings.forEach((f) => set.add(f.service))
    return [...set].sort()
  }, [data])

  const visible = useMemo(() => {
    if (!data) return []
    const needle = query.trim().toLowerCase()
    let list = data.findings
    if (onlyNew) list = list.filter((f) => newIDs.has(f.id))
    if (!needle) return list
    return list.filter((f) =>
      [f.title, f.detail, f.object, f.rule, f.file].some((v) => v?.toLowerCase().includes(needle)),
    )
  }, [data, query, onlyNew, newIDs])

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('findings.title')}
            <InfoHint>{t('findings.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={error} />

      <Card>
        <div className="filters">
          <label>
            {t('findings.severity')}
            <Select
              value={severity}
              onChange={setSeverity}
              style={{ minWidth: '11rem' }}
              options={[
                { value: '', label: t('common.all') },
                ...SEVERITIES.map((s) => ({ value: s, label: `${severityLabel(s)} (${data?.counts[s] ?? 0})` })),
              ]}
            />
          </label>
          <label>
            {t('findings.service')}
            <Select
              value={service}
              onChange={setService}
              style={{ minWidth: '9rem' }}
              options={[{ value: '', label: t('common.all') }, ...services.map((s) => ({ value: s, label: s }))]}
            />
          </label>
          <label style={{ flex: 1, minWidth: '14rem' }}>
            {t('common.search')}
            <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('findings.searchPlaceholder')} />
          </label>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', paddingBottom: '0.4rem' }}>
            <Checkbox checked={onlyNew} disabled={newIDs.size === 0} onChange={(e) => setOnlyNew(e.target.checked)} />
            {t('findings.onlyNew', { count: newIDs.size })}
          </label>
          <span className="small muted" style={{ paddingBottom: '0.4rem' }}>
            {t('common.shown', { shown: formatNumber(visible.length), total: formatNumber(data?.total ?? 0) })}
          </span>
        </div>
      </Card>

      {loading && !data ? (
        <Loading what={t('findings.loadingList')} />
      ) : visible.length === 0 ? (
        <Card>
          <div className="chart-empty">{t('common.noMatch')}</div>
        </Card>
      ) : (
        <div className="col">
          {visible.map((f) => (
            <Card key={f.id}>
              <div className="spread" style={{ alignItems: 'flex-start' }}>
                <div style={{ minWidth: 0 }}>
                  <div className="row" style={{ marginBottom: '0.25rem' }}>
                    <SeverityBadge severity={f.severity} />
                    {newIDs.has(f.id) && <Tag color="gold">{t('findings.newTag')}</Tag>}
                    <Tag>{f.rule}</Tag>
                    <Tag>{f.service}</Tag>
                    {f.object && <Tag>{f.object}</Tag>}
                  </div>
                  <h3>{f.title}</h3>
                  <p className="secondary" style={{ margin: '0.3rem 0 0' }}>
                    {f.detail}
                  </p>
                  {f.suggestion && (
                    <p style={{ margin: '0.45rem 0 0' }}>
                      <strong>{t('findings.whatToDo')}</strong>
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
