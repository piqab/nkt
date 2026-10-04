import { useMemo, useState } from 'react'
import { Input, Select, Space, Tag, Tooltip } from 'antd'
import { CheckCircleFilled, CloseCircleFilled, ExclamationCircleFilled, MinusCircleFilled, RightOutlined, DownOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { Card } from './ui'
import { formatBytes } from './charts'
import { Sensitive } from '../privacy'

/**
 * Блок «Хосты» «Мониторинга» — как список хостов хаба: группы хаба
 * сворачиваемыми разделами, у хоста значок состояния, метки Kubernetes,
 * процессор, память и диски полосками (цвет — по порогам), щелчок —
 * графики хоста. Узлы кластеров, которых нет среди хостов хаба, — своими
 * разделами «кластер · узлы без хоста».
 */

export interface MonHostLite {
  id: number
  name: string
  group?: string
  reachable?: boolean
  old?: boolean
  error?: string
  has_data: boolean
  cpu_avg: number
  cpu_max: number
  mem_used: number
  mem_total: number
  mem_avg_pct: number
  mem_max_pct: number
  load_avg: number
  load_max: number
  disks: { mount: string; pct: number; eta_days?: number }[]
  workloads: number
  k8s_cluster?: string
  k8s_role?: string
  k8s_node?: string
}

export interface MonClusterLite {
  name: string
  host_id: number
  nodes: { name: string; ip?: string; ready: boolean; control_plane?: boolean; host_id?: number }[]
}

export interface MonNodeLoad {
  host_id: number
  subject: string
  cpu_avg: number
  cpu_max: number
  mem_avg: number
  mem_max: number
  cpu_share?: number
  mem_share?: number
}

const COLLAPSED_KEY = 'nkt.monitoring.collapsed'

function readCollapsed(): string[] {
  try {
    return JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? '[]') as string[]
  } catch {
    return []
  }
}

/** Полоска с подписью: цвет — от порога (≥ порога — красный, ≥ 80% порога — жёлтый). */
export function MiniBar({ pct, peak, limit, label, title }: { pct: number; peak?: number; limit: number; label: string; title?: string }) {
  const v = Math.max(0, Math.min(100, pct))
  const worst = Math.max(pct, peak ?? 0)
  const color = worst >= limit ? 'var(--status-critical)' : worst >= limit * 0.8 ? 'var(--status-warning)' : 'var(--status-good)'
  return (
    <Tooltip title={title}>
      <div className="mon-bar">
        <div className="mon-bar-track">
          <div className="mon-bar-fill" style={{ width: `${v}%`, background: color }} />
          {peak !== undefined && peak > pct && <div className="mon-bar-peak" style={{ left: `${Math.min(100, peak)}%`, background: color }} />}
        </div>
        <span className="small nowrap mon-bar-label">{label}</span>
      </div>
    </Tooltip>
  )
}

function K8sTags({ cluster, role, node }: { cluster?: string; role?: string; node?: string }) {
  const { t } = useTranslation()
  if (!role && !cluster) return null
  return (
    <span className="row" style={{ gap: '0.2rem', flexWrap: 'wrap' }}>
      {role && <Tag color={role === 'control-plane' ? 'geekblue' : 'cyan'}>k8s · {t(`monitoring.k8sRole.${role}`, { defaultValue: role })}</Tag>}
      {cluster && <Tag>{t('monitoring.k8sCluster', { name: cluster })}</Tag>}
      {node && <span className="small muted mono">{node}</span>}
    </span>
  )
}

export function MonHostsBlock({
  hosts,
  clusters,
  nodeLoads,
  cpuLimit,
  memLimit,
  diskWarnDays,
  diskCritDays,
  onOpen,
  onOpenNode,
}: {
  hosts: MonHostLite[]
  clusters: MonClusterLite[]
  nodeLoads: MonNodeLoad[]
  cpuLimit: number
  memLimit: number
  diskWarnDays: number
  diskCritDays: number
  onOpen: (h: MonHostLite) => void
  onOpenNode: (n: MonNodeLoad, cluster: string) => void
}) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const [cluster, setCluster] = useState('')
  const [collapsed, setCollapsedState] = useState<string[]>(readCollapsed)
  const toggle = (g: string) =>
    setCollapsedState((prev) => {
      const next = prev.includes(g) ? prev.filter((x) => x !== g) : [...prev, g]
      try {
        localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next))
      } catch {
        // не запомнится — не страшно
      }
      return next
    })

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    return hosts.filter((h) => (!cluster || h.k8s_cluster === cluster) && (!s || `${h.name} ${h.group ?? ''} ${h.k8s_node ?? ''}`.toLowerCase().includes(s)))
  }, [hosts, q, cluster])

  const groups = useMemo(() => {
    const by = new Map<string, MonHostLite[]>()
    for (const h of filtered) {
      const g = (h.group ?? '').trim()
      by.set(g, [...(by.get(g) ?? []), h])
    }
    return [...by.entries()].sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
  }, [filtered])

  // Узлы без хоста на хабе — по кластерам, с нагрузкой из рядов k8s_node.
  const orphans = useMemo(() => {
    const s = q.trim().toLowerCase()
    return clusters
      .filter((c) => !cluster || c.name === cluster)
      .map((c) => ({
        cluster: c,
        nodes: c.nodes
          .filter((n) => !n.host_id && (!s || n.name.toLowerCase().includes(s)))
          .map((n) => ({ node: n, load: nodeLoads.find((l) => l.host_id === c.host_id && l.subject === n.name) })),
      }))
      .filter((x) => x.nodes.length > 0)
  }, [clusters, nodeLoads, cluster, q])

  const status = (h: MonHostLite) => {
    if (h.reachable === false) return <Tooltip title={t('monitoring.unreachable')}><CloseCircleFilled style={{ color: 'var(--status-critical)' }} /></Tooltip>
    if (h.old) return <Tooltip title={t('monitoring.oldNkt')}><ExclamationCircleFilled style={{ color: 'var(--status-warning)' }} /></Tooltip>
    if (!h.has_data) return <Tooltip title={h.error ? t('monitoring.collectError', { error: h.error }) : t('monitoring.noData')}><MinusCircleFilled style={{ color: 'var(--text-muted)' }} /></Tooltip>
    return <CheckCircleFilled style={{ color: 'var(--status-good)' }} />
  }

  const hostRow = (h: MonHostLite) => (
    <div key={h.id} className={`mon-host-row${h.has_data ? ' clickable' : ''}`} onClick={() => h.has_data && onOpen(h)}>
      <span className="mon-col-status">{status(h)}</span>
      <span className="mon-col-name">
        <strong><Sensitive>{h.name}</Sensitive></strong>
        <K8sTags cluster={h.k8s_cluster} role={h.k8s_role} node={h.k8s_node && h.k8s_node !== h.name ? h.k8s_node : undefined} />
      </span>
      <span className="mon-col-bar">
        {h.has_data ? <MiniBar pct={h.cpu_avg} peak={h.cpu_max} limit={cpuLimit} label={`${h.cpu_avg} / ${h.cpu_max}%`} title={t('monitoring.barHint')} /> : <span className="muted">—</span>}
      </span>
      <span className="mon-col-bar">
        {h.mem_total > 0 ? (
          <MiniBar pct={h.mem_avg_pct} peak={h.mem_max_pct} limit={memLimit} label={`${formatBytes(h.mem_used)} / ${formatBytes(h.mem_total)}`} title={t('monitoring.memBarHint', { avg: h.mem_avg_pct, max: h.mem_max_pct })} />
        ) : (
          <span className="muted">—</span>
        )}
      </span>
      <span className="mon-col-load small">{h.has_data ? `${h.load_avg} / ${h.load_max}` : '—'}</span>
      <span className="mon-col-disks">
        {h.disks.map((dk) => (
          <span key={dk.mount} className="mon-disk">
            <MiniBar pct={dk.pct} limit={90} label={`${dk.mount} ${dk.pct}%`} title={`${dk.mount} — ${dk.pct}%`} />
            {dk.eta_days !== undefined && (
              <Tag color={dk.eta_days <= diskCritDays ? 'red' : dk.eta_days <= diskWarnDays ? 'orange' : 'blue'}>{t('monitoring.diskEta', { days: dk.eta_days })}</Tag>
            )}
          </span>
        ))}
      </span>
      <span className="mon-col-wl small">{h.workloads || '—'}</span>
    </div>
  )

  const header = (
    <div className="mon-host-row mon-host-head small muted">
      <span className="mon-col-status" />
      <span className="mon-col-name">{t('monitoring.colHost')}</span>
      <span className="mon-col-bar">{t('monitoring.colCPU')}</span>
      <span className="mon-col-bar">{t('monitoring.colMem')}</span>
      <span className="mon-col-load">{t('monitoring.colLoad')}</span>
      <span className="mon-col-disks">{t('monitoring.colDisks')}</span>
      <span className="mon-col-wl">{t('monitoring.colWorkloads')}</span>
    </div>
  )

  const section = (key: string, title: React.ReactNode, count: number, body: React.ReactNode, countKey = 'monitoring.hostsCount') => {
    const isCollapsed = collapsed.includes(key)
    return (
      <div key={key} className="mon-group">
        <div className="mon-group-head" onClick={() => toggle(key)}>
          {isCollapsed ? <RightOutlined /> : <DownOutlined />} <strong>{title}</strong> <span className="small muted">{t(countKey, { count })}</span>
        </div>
        {!isCollapsed && body}
      </div>
    )
  }

  return (
    <Card
      title={t('monitoring.hostsTitle')}
      subtitle={t('monitoring.hostsHint')}
      actions={
        <Space wrap>
          {clusters.length > 0 && (
            <Select
              value={cluster}
              onChange={setCluster}
              style={{ minWidth: '11rem' }}
              options={[{ value: '', label: t('monitoring.allClusters') }, ...clusters.map((c) => ({ value: c.name, label: t('monitoring.k8sCluster', { name: c.name }) }))]}
            />
          )}
          <Input.Search allowClear placeholder={t('monitoring.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ width: '14rem' }} />
        </Space>
      }
    >
      <div className="table-wrap">
        <div className="mon-hosts">
          {groups.map(([g, list]) => section(`g:${g}`, g || t('monitoring.noGroup'), list.length, <>{header}{list.map(hostRow)}</>))}
          {orphans.map(({ cluster: c, nodes }) =>
            section(
              `k8s:${c.name}`,
              <>{t('monitoring.k8sCluster', { name: c.name })} · {t('monitoring.nodesWithoutHost')}</>,
              nodes.length,
              <>
                {header}
                {nodes.map(({ node: n, load }) => (
                  <div key={n.name} className={`mon-host-row${load ? ' clickable' : ''}`} onClick={() => load && onOpenNode(load, c.name)}>
                    <span className="mon-col-status">
                      {n.ready ? <CheckCircleFilled style={{ color: 'var(--status-good)' }} /> : <Tooltip title={t('monitoring.nodeNotReady')}><CloseCircleFilled style={{ color: 'var(--status-critical)' }} /></Tooltip>}
                    </span>
                    <span className="mon-col-name">
                      <strong className="mono">{n.name}</strong>
                      <K8sTags role={n.control_plane ? 'control-plane' : 'worker'} />
                      {n.ip && <span className="small muted mono">{n.ip}</span>}
                    </span>
                    <span className="mon-col-bar">
                      {load ? <MiniBar pct={load.cpu_share ?? 0} limit={cpuLimit} label={`${load.cpu_share ?? '—'}% · ${(load.cpu_avg / 100).toFixed(2)} ${t('monitoring.cores')}`} /> : '—'}
                    </span>
                    <span className="mon-col-bar">
                      {load ? <MiniBar pct={load.mem_share ?? 0} limit={memLimit} label={`${load.mem_share ?? '—'}% · ${formatBytes(load.mem_avg)}`} /> : '—'}
                    </span>
                    <span className="mon-col-load small">—</span>
                    <span className="mon-col-disks" />
                    <span className="mon-col-wl" />
                  </div>
                ))}
              </>,
              'monitoring.nodesCount',
            ),
          )}
        </div>
      </div>
    </Card>
  )
}
