import { useState } from 'react'
import { Sensitive } from '../privacy'
import { Button, Checkbox, Input, InputNumber, Select, Tag, Tooltip } from 'antd'
import { ClusterOutlined, CodeOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, apiURL, useApi } from '../api'
import type { HubHost, Job } from '../types'
import { JobLogModal } from '../pages/Jobs'
import { Banner, Card, ErrorNote, Modal, formatBytesShort, formatRelative } from '../components/ui'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'

/**
 * Кластеры Kubernetes на виртуалках хоста (internal/hub/cluster.go):
 * диалог создания с выбором топологии и карточка со списком — узлы,
 * состояние, kubeconfig, добавить worker'ов, удалить.
 */

export interface ClusterNode {
  host_id: number
  name: string
  role: string
  addr: string
  status: string
  reachable?: boolean
}

export interface Cluster {
  id: number
  name: string
  host_id: number
  host_name: string
  flavor: string
  topology: string
  workers: number
  expose: boolean
  status: 'creating' | 'ready' | 'failed' | 'deleting'
  error_msg?: string
  server_addr?: string
  has_kubeconfig: boolean
  nodes: ClusterNode[]
  created_at: string
}

export function NewClusterModal({
  host,
  onClose,
  onStarted,
}: {
  host: HubHost
  onClose: () => void
  onStarted: (text: string, jobID: number) => void
}) {
  const { t } = useTranslation()
  const images = useApi<{ catalog: { id: string; name: string }[]; local: { id: string; downloaded: boolean }[] }>(`/hosts/${host.id}/vm/images`, 10_000)
  const nets = useApi<{ networks: { name: string; active: boolean }[] }>(`/hosts/${host.id}/vm/networks`, 60_000)
  const hubImages = useApi<{ images: { name: string; size: number }[] }>('/hub/cluster-images', 60_000)
  const knownNets = nets.data?.networks ?? []
  const [name, setName] = useState('k8s')
  const [flavor, setFlavor] = useState<'k3s' | 'kubeadm'>('k3s')
  const [k8sVersion, setK8sVersion] = useState('')
  const versions = useApi<{ stable: string; versions: string[] }>('/hub/k8s-versions', 600_000)
  const [topology, setTopology] = useState<'single' | 'cp1' | 'cp3'>('cp1')
  const [workers, setWorkers] = useState(1)
  const [expose, setExpose] = useState(true)
  const [ports, setPorts] = useState<{ api: number | null; http: number | null; https: number | null }>({ api: 6443, http: 80, https: 443 })
  const [cilium, setCilium] = useState(true)
  const [kpr, setKPR] = useState(false)
  const [imageID, setImageID] = useState('')
  const [network, setNetwork] = useState('')
  const [cp, setCP] = useState({ vcpus: 2, mem: 4096, disk: 30 })
  const [w, setW] = useState({ vcpus: 2, mem: 4096, disk: 30 })
  const [busy, setBusy] = useState<'create' | 'dry' | null>(null)
  const [prepare, setPrepare] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // Журнал сухого прогона открывается поверх формы: настройки остаются,
  // после проверок можно поправить и запустить создание.
  const [dryJob, setDryJob] = useState<Job | null>(null)
  const downloaded = new Set((images.data?.local ?? []).filter((l) => l.downloaded).map((l) => l.id))
  const catalog = images.data?.catalog ?? []
  // Ubuntu 24.04 — умолчание из плана; иначе первый образ каталога.
  const effectiveImage = imageID || catalog.find((i) => /ubuntu.*24/i.test(i.id + i.name))?.id || catalog[0]?.id || ''
  const machines = (topology === 'single' ? 1 : topology === 'cp3' ? 3 : 1) + (topology === 'single' ? 0 : workers)

  const body = () => ({
          name,
          host_id: host.id,
          flavor,
          k8s_version: k8sVersion,
          topology,
          workers: topology === 'single' ? 0 : workers,
          expose,
          expose_api: expose ? ports.api ?? 0 : 0,
          expose_http: expose ? ports.http ?? 0 : 0,
          expose_https: expose ? ports.https ?? 0 : 0,
          cni: cilium ? 'cilium' : '',
          kube_proxy_replacement: cilium && kpr,
          image_id: effectiveImage,
          network: network || knownNets[0]?.name || '',
          cp_vcpus: cp.vcpus,
          cp_memory_mb: cp.mem,
          cp_disk_gb: cp.disk,
          w_vcpus: w.vcpus,
          w_memory_mb: w.mem,
          w_disk_gb: w.disk,
  })

  async function start(dry: boolean) {
    setBusy(dry ? 'dry' : 'create')
    setError(null)
    try {
      const res = await api<{ id?: number; job_id: number }>(dry ? '/hub/clusters/dry-run' : '/hub/clusters', {
        method: 'POST',
        body: dry ? { ...body(), prepare } : body(),
      })
      if (dry) {
        setDryJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
        return
      }
      onStarted(t('clusters.started', { name }), res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const sizes = (label: string, v: { vcpus: number; mem: number; disk: number }, set: (v: { vcpus: number; mem: number; disk: number }) => void) => (
    <div className="col" style={{ gap: '0.2rem' }}>
      <span className="small muted">{label}</span>
      <div className="row" style={{ gap: '0.4rem' }}>
        <InputNumber min={1} max={64} value={v.vcpus} onChange={(n) => set({ ...v, vcpus: n ?? 2 })} addonBefore="CPU" style={{ width: '8rem' }} />
        <InputNumber min={1024} step={1024} value={v.mem} onChange={(n) => set({ ...v, mem: n ?? 4096 })} addonBefore="MB" style={{ width: '9rem' }} />
        <InputNumber min={10} max={2000} value={v.disk} onChange={(n) => set({ ...v, disk: n ?? 30 })} addonBefore="GB" style={{ width: '8rem' }} />
      </div>
    </div>
  )

  return (
    <Modal title={t('clusters.newTitle', { host: host.name })} onClose={onClose} width={760}>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      <p className="small muted">{t('clusters.newBody')}</p>
      <ErrorNote error={error} />
      <ErrorNote error={images.error} />
      <div className="grid grid-2" style={{ marginBottom: '0.6rem' }}>
        <label>
          {t('clusters.name')}
          <Input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="k8s" />
        </label>
        <label>
          {t('clusters.flavor')}
          <Select
            value={flavor}
            onChange={(v: 'k3s' | 'kubeadm') => {
              setFlavor(v)
              if (v === 'kubeadm' && topology === 'cp3') setTopology('cp1')
            }}
            options={[
              { value: 'k3s', label: t('clusters.flavorK3s') },
              { value: 'kubeadm', label: t('clusters.flavorKubeadm') },
            ]}
          />
        </label>
        <label>
          {t('clusters.k8sVersion')}
          <Select
            value={k8sVersion}
            onChange={(v: string) => setK8sVersion(v)}
            options={[
              { value: '', label: t('clusters.k8sVersionStable', { v: versions.data?.stable ?? '…' }) },
              ...(versions.data?.versions ?? []).map((v) => ({ value: v, label: v })),
            ]}
          />
          <span className="small muted">{t('clusters.k8sVersionHint')}</span>
        </label>
        <label>
          {t('clusters.topology')}
          <Select
            value={topology}
            onChange={(v: 'single' | 'cp1' | 'cp3') => setTopology(v)}
            options={[
              { value: 'single', label: t('clusters.topoSingle') },
              { value: 'cp1', label: t('clusters.topoCP1') },
              { value: 'cp3', label: t('clusters.topoCP3'), disabled: flavor !== 'k3s' },
            ]}
          />
        </label>
        <label>
          {t('clusters.workers')}
          <InputNumber min={1} max={20} value={workers} disabled={topology === 'single'} onChange={(v) => setWorkers(v ?? 1)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('hosts.newVMImage')}
          <Select
            value={effectiveImage}
            onChange={(v: string) => setImageID(v)}
            options={[
              ...catalog.map((img) => ({ value: img.id, label: img.name + (downloaded.has(img.id) ? '' : ` ${t('hosts.newVMWillDownload')}`) })),
              ...(hubImages.data?.images ?? []).map((img) => ({ value: `hub:${img.name}`, label: `${t('clusters.imageFromHub')}: ${img.name} (${formatBytesShort(img.size)})` })),
            ]}
          />
        </label>
        <label>
          {t('hosts.newVMNetwork')}
          <Select
            value={network || knownNets[0]?.name || 'default'}
            onChange={(v: string) => setNetwork(v)}
            options={knownNets.length > 0 ? knownNets.map((n) => ({ value: n.name, label: n.name })) : [{ value: 'default', label: 'default' }]}
          />
        </label>
      </div>
      <div className="col" style={{ gap: '0.6rem', marginBottom: '0.6rem' }}>
        {sizes(t('clusters.cpSizes'), cp, setCP)}
        {topology !== 'single' && sizes(t('clusters.wSizes'), w, setW)}
      </div>
      <div className="col" style={{ gap: '0.4rem', marginBottom: '0.6rem' }}>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={cilium} onChange={(e) => setCilium(e.target.checked)} />
          <span>
            {t('clusters.cilium')} <span className="small muted">{t('clusters.ciliumHint')}</span>
          </span>
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', paddingLeft: '1.6rem' }}>
          <Checkbox checked={cilium && kpr} disabled={!cilium} onChange={(e) => setKPR(e.target.checked)} />
          <span>
            {t('clusters.kpr')} <span className="small muted">{t('clusters.kprHint')}</span>
          </span>
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={expose} onChange={(e) => setExpose(e.target.checked)} />
          <span>
            {t('clusters.expose')} <span className="small muted">{t('clusters.exposeHint', { host: host.addr })}</span>
          </span>
        </label>
        {expose && (
          <div className="row" style={{ gap: '0.5rem', paddingLeft: '1.6rem', flexWrap: 'wrap' }}>
            <InputNumber min={0} max={65535} value={ports.api} onChange={(v) => setPorts({ ...ports, api: v })} addonBefore="API" style={{ width: '10rem' }} />
            <InputNumber min={0} max={65535} value={ports.http} onChange={(v) => setPorts({ ...ports, http: v })} addonBefore="HTTP" style={{ width: '10rem' }} />
            <InputNumber min={0} max={65535} value={ports.https} onChange={(v) => setPorts({ ...ports, https: v })} addonBefore="HTTPS" style={{ width: '10rem' }} />
            <span className="small muted">{t('clusters.portsHint')}</span>
          </div>
        )}
      </div>
      <p className="small muted">{t('clusters.summary', { machines, flavor, host: host.name })}</p>
      <div className="row" style={{ justifyContent: 'flex-end', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={prepare} onChange={(e) => setPrepare(e.target.checked)} />
          <span className="small">{t('clusters.dryPrepare')}</span>
        </label>
        <Tooltip title={t('clusters.dryHint')}>
          <Button loading={busy === 'dry'} disabled={!name || !effectiveImage || busy === 'create'} onClick={() => void start(true)}>
            {t('clusters.dryRun')}
          </Button>
        </Tooltip>
        <Button type="primary" loading={busy === 'create'} disabled={!name || !effectiveImage || busy === 'dry'} onClick={() => void start(false)}>
          {t('clusters.create')}
        </Button>
      </div>
      {dryJob && <JobLogModal job={dryJob} scope="/hosts/local" onClose={() => setDryJob(null)} />}
    </Modal>
  )
}

const STATUS_COLOR: Record<Cluster['status'], string> = { creating: 'processing', ready: 'success', failed: 'error', deleting: 'default' }

export function ClustersCard({ onOpenJob, onChanged, showEmpty }: { onOpenJob: (jobID: number) => void; onChanged: () => void; showEmpty?: boolean }) {
  const { t } = useTranslation()
  const [pollMs, setPollMs] = useState(15_000)
  const list = useApi<Cluster[]>('/hub/clusters', pollMs)
  const clusters = list.data ?? []
  const anyBusy = clusters.some((c) => c.status === 'creating' || c.status === 'deleting')
  if (anyBusy && pollMs !== 4_000) setPollMs(4_000)
  if (!anyBusy && pollMs !== 15_000) setPollMs(15_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  async function call(key: string, path: string, method: 'POST' | 'DELETE', body?: unknown) {
    setBusy(key)
    try {
      const res = await api<{ job_id?: number }>(path, { method, body })
      if (res.job_id) onOpenJob(res.job_id)
      await list.reload()
      onChanged()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  // Терминал с kubectl — откреплённое окно на первом control plane
  // (?kubectl=1: KUBECONFIG на admin-конфиг, автодополнение).
  function openKubectl(c: Cluster) {
    const cp = c.nodes.find((n) => n.role === 'control-plane')
    if (!cp) return
    const params = new URLSearchParams({ host: String(cp.host_id), name: `${c.name} · ${cp.name}`, kubectl: '1' })
    window.open(`/terminal/popout?${params.toString()}`, `nkt-kubectl-${c.id}`, 'width=980,height=640,resizable=yes')
  }

  if (clusters.length === 0 && !showEmpty) return null
  return (
    <Card title={t('clusters.title')} subtitle={t('clusters.subtitle', { count: clusters.length })}>
      {clusters.length === 0 && <p className="small muted">{t('clusters.none')}</p>}
      <ErrorNote error={list.error} />
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
      <div className="col" style={{ gap: '0.8rem' }}>
        {clusters.map((c) => (
          <div key={c.id} className="col" style={{ gap: '0.3rem' }}>
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <ClusterOutlined />
              <strong>{c.name}</strong>
              <Tag color={STATUS_COLOR[c.status]}>{t(`clusters.status.${c.status}`)}</Tag>
              <Tag>{c.flavor}</Tag>
              <Tag>{t(`clusters.topo.${c.topology}`)}</Tag>
              <span className="small muted">
                {t('clusters.onHost', { host: c.host_name })} · {formatRelative(c.created_at)}
              </span>
              {c.server_addr && (
                <span className="small mono muted">
                  https://<Sensitive>{c.server_addr}</Sensitive>:6443{c.expose ? '' : ` (${t('clusters.internalOnly')})`}
                </span>
              )}
              <span style={{ flex: 1 }} />
              {c.has_kubeconfig && (
                <Tooltip title={t('clusters.kubeconfigHint')}>
                  <a href={apiURL(`/hub/clusters/${c.id}/kubeconfig`)} download>
                    <Button size="small">{t('clusters.kubeconfig')}</Button>
                  </a>
                </Tooltip>
              )}
              {c.status === 'ready' && c.nodes.some((n) => n.role === 'control-plane') && (
                <Tooltip title={t('clusters.kubectlHint')}>
                  <Button size="small" icon={<CodeOutlined />} onClick={() => openKubectl(c)}>
                    kubectl
                  </Button>
                </Tooltip>
              )}
              {c.status === 'failed' && (
                <Tooltip title={t('clusters.retryHint')}>
                  <Button size="small" type="primary" loading={busy === `retry:${c.id}`} onClick={() => void call(`retry:${c.id}`, `/hub/clusters/${c.id}/retry`, 'POST')}>
                    {t('clusters.retry')}
                  </Button>
                </Tooltip>
              )}
              {c.status === 'ready' && c.topology !== 'single' && (
                <Button size="small" loading={busy === `add:${c.id}`} onClick={() => void call(`add:${c.id}`, `/hub/clusters/${c.id}/workers`, 'POST', { count: 1 })}>
                  {t('clusters.addWorker')}
                </Button>
              )}
              <RowAction
                action="delete"
                label={t('clusters.delete')}
                danger
                loading={busy === `del:${c.id}`}
                disabled={c.status === 'deleting'}
                onClick={async () => {
                  if (!(await confirmAction(t('clusters.confirmDelete', { name: c.name, count: c.nodes.length })))) return
                  await call(`del:${c.id}`, `/hub/clusters/${c.id}`, 'DELETE')
                }}
              />
            </div>
            {c.error_msg && <Banner kind="error">{c.error_msg}</Banner>}
            <div className="table-wrap">
              <DataTable<ClusterNode>
                dataSource={c.nodes}
                rowKey="host_id"
                size="small"
                pagination={false}
                columns={[
                  { title: t('clusters.colNode'), dataIndex: 'name', key: 'name', render: (v: string) => <span className="mono"><Sensitive>{v}</Sensitive></span> },
                  { title: t('clusters.colRole'), dataIndex: 'role', key: 'role', render: (v: string) => <Tag color={v === 'control-plane' ? 'blue' : 'default'}>{v}</Tag> },
                  { title: t('hosts.colAddr'), dataIndex: 'addr', key: 'addr', render: (v: string) => <span className="mono small"><Sensitive>{v}</Sensitive></span> },
                  {
                    title: t('clusters.colState'),
                    key: 'state',
                    render: (_: unknown, n: ClusterNode) =>
                      n.reachable === undefined ? <span className="muted">—</span> : n.reachable ? <Tag color="success">{t('clusters.reachable')}</Tag> : <Tag color="error">{t('clusters.unreachable')}</Tag>,
                  },
                ]}
              />
            </div>
          </div>
        ))}
      </div>
    </Card>
  )
}
