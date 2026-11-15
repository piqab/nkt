import { useMemo } from 'react'
import { Button, Select, Space } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import type { Finding, HubHost, Severity } from '../types'
import { Banner, Card, ErrorNote, Loading, SeverityBadge } from './ui'
import { AIExplain } from './AIExplain'

interface ClusterRow {
  id: number
  name: string
  status: string
  host_id: number
  nodes: unknown[]
}

/**
 * Выбор кластеров: список готовых и быстрый выбор — все или по группе
 * хоста, на котором стоит кластер.
 */
export function ClusterPicker({ value, onChange }: { value: number[]; onChange: (v: number[]) => void }) {
  const { t } = useTranslation()
  const clusters = useApi<ClusterRow[]>('/hub/clusters')
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const ready = useMemo(() => (clusters.data ?? []).filter((c) => c.status === 'ready' && c.nodes.length > 0), [clusters.data])
  const groupOf = useMemo(() => {
    const m = new Map<number, string>()
    for (const h of hosts.data ?? []) if (h.group) m.set(h.id, h.group)
    return m
  }, [hosts.data])
  const groups = [...new Set(ready.map((c) => groupOf.get(c.host_id)).filter((g): g is string => !!g))].sort()
  return (
    <div className="col" style={{ gap: '0.3rem' }}>
      <Select
        mode="multiple"
        size="small"
        style={{ minWidth: '22rem' }}
        placeholder={t('manifests.clusters')}
        value={value}
        onChange={onChange}
        loading={clusters.loading}
        options={ready.map((c) => ({ value: c.id, label: groupOf.get(c.host_id) ? `${c.name} · ${groupOf.get(c.host_id)}` : c.name }))}
      />
      {ready.length > 0 && (
        <Space size={4} wrap>
          <span className="small muted">{t('manifests.pick')}</span>
          <Button size="small" type="link" onClick={() => onChange(ready.map((c) => c.id))}>
            {t('manifests.pickAll')}
          </Button>
          {groups.map((g) => (
            <Button key={g} size="small" type="link" onClick={() => onChange(ready.filter((c) => groupOf.get(c.host_id) === g).map((c) => c.id))}>
              {g}
            </Button>
          ))}
        </Space>
      )}
      {clusters.data && ready.length === 0 && <Banner kind="warn">{t('manifests.noClusters')}</Banner>}
    </div>
  )
}

interface ClusterFindings {
  cluster_id: number
  cluster: string
  findings: Finding[]
  error?: string
}

const SEV_ORDER: Severity[] = ['critical', 'high', 'medium', 'low', 'info']

/** Находки Kubernetes со всех кластеров хаба — с лампочкой ИИ. */
export function ClustersFindingsCard() {
  const { t } = useTranslation()
  const res = useApi<{ clusters: ClusterFindings[] }>('/hub/k8s/findings', 120_000)
  const list = res.data?.clusters ?? []
  if (res.data && list.length === 0) return null
  return (
    <Card title={t('clustersFindings.title')} subtitle={t('clustersFindings.subtitle')}>
      <ErrorNote error={res.error} />
      {res.loading && !res.data ? (
        <Loading what={t('clustersFindings.title')} />
      ) : (
        list.map((c) => (
          <div key={c.cluster_id} style={{ marginBottom: '0.75rem' }}>
            <strong className="small">{c.cluster}</strong>{' '}
            <span className="small muted">{c.error ? '' : t('clustersFindings.count', { count: c.findings.length })}</span>
            {c.error && <Banner kind="error">{c.error}</Banner>}
            {[...c.findings]
              .sort((a, b) => SEV_ORDER.indexOf(a.severity as Severity) - SEV_ORDER.indexOf(b.severity as Severity))
              .map((f) => (
                <div key={f.id} className="row small" style={{ gap: '0.4rem', alignItems: 'center', marginTop: '0.2rem' }}>
                  <SeverityBadge severity={f.severity as Severity} />
                  <span>{f.title}</span>
                  <AIExplain ctx={{ kind: 'finding', title: f.title, detail: f.detail, suggestion: f.suggestion, severity: f.severity, service: f.service, object: f.object }} />
                </div>
              ))}
          </div>
        ))
      )}
    </Card>
  )
}
