import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Tooltip } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost, Job, Me } from '../types'
import { Banner, ErrorNote, InfoHint, Modal } from '../components/ui'
import { ClustersCard } from '../components/Clusters'
import { JobLogModal } from './Jobs'

/**
 * Раздел «Кластеры» на хабе: все кластеры Kubernetes и создание кластера
 * на нескольких хостах — таблица размещения (хост → роль → машины или
 * сам хост → размеры), сеть между хостами, Cilium, проброс. Задание и
 * проверки те же, что у диалога «новый кластер» у хоста.
 */

interface Row {
  host_id: number | null
  role: 'control-plane' | 'worker'
  kind: 'vm' | 'host'
  count: number
  vcpus: number
  memory_mb: number
  disk_gb: number
  image_id: string
  network: string
  bridge: string
}

const newRow = (role: Row['role']): Row => ({ host_id: null, role, kind: 'vm', count: role === 'control-plane' ? 1 : 1, vcpus: 2, memory_mb: 4096, disk_gb: 30, image_id: 'ubuntu-24.04', network: '', bridge: '' })

export default function ClustersPage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [creating, setCreating] = useState(false)
  const [hubJob, setHubJob] = useState<Job | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [tick, setTick] = useState(0)

  async function openHubJob(id: number) {
    try {
      const job = await api<Job>(`/hosts/local/jobs/${id}`)
      setHubJob(job)
    } catch {
      // Журнал откроется из «Заданий», если сразу не нашёлся.
    }
  }

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('clusters.title')}
          <InfoHint>{t('clusters.pageHint')}</InfoHint>
        </h1>
        {me.is_admin && (
          <Button type="primary" onClick={() => setCreating(true)}>
            {t('clusters.newMulti')}
          </Button>
        )}
      </div>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
      <ClustersCard key={tick} onOpenJob={(id) => void openHubJob(id)} onChanged={() => setTick((n) => n + 1)} showEmpty />
      {creating && (
        <MultiClusterModal
          onClose={() => setCreating(false)}
          onStarted={(text, jobID) => {
            setCreating(false)
            setNotice({ kind: 'info', text })
            void openHubJob(jobID)
            setTick((n) => n + 1)
          }}
        />
      )}
      {hubJob && <JobLogModal job={hubJob} scope="/hosts/local" onClose={() => setHubJob(null)} />}
    </>
  )
}

function MultiClusterModal({ onClose, onStarted }: { onClose: () => void; onStarted: (text: string, jobID: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const online = (hosts.data ?? []).filter((h) => h.status === 'online' && h.id > 0)
  const [name, setName] = useState('k8s')
  const [flavor, setFlavor] = useState<'k3s' | 'kubeadm'>('k3s')
  const [cilium, setCilium] = useState(true)
  const [kpr, setKPR] = useState(false)
  const [network, setNetwork] = useState<'nat' | 'bridge' | 'wireguard'>('bridge')
  const [rows, setRows] = useState<Row[]>([newRow('control-plane'), newRow('worker')])
  const [expose, setExpose] = useState(true)
  const [ports, setPorts] = useState<{ api: number | null; http: number | null; https: number | null }>({ api: 6443, http: 80, https: 443 })
  const [prepare, setPrepare] = useState(true)
  const [busy, setBusy] = useState<'create' | 'dry' | null>(null)
  const [error, setError] = useState<string | null>(null)

  const distinctHosts = new Set(rows.map((r) => r.host_id).filter((h) => h !== null)).size
  const effectiveNetwork = distinctHosts <= 1 && network === 'bridge' && rows.every((r) => !r.bridge) ? 'nat' : network
  const update = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const valid = name && rows.length > 0 && rows.every((r) => r.host_id !== null && (r.kind === 'host' || r.count > 0))

  async function start(dry: boolean) {
    setBusy(dry ? 'dry' : 'create')
    setError(null)
    try {
      const body = {
        name,
        flavor,
        cni: cilium ? 'cilium' : '',
        kube_proxy_replacement: cilium && kpr,
        network_mode: effectiveNetwork,
        placements: rows.map((r) => ({
          host_id: r.host_id,
          role: r.role,
          kind: r.kind,
          count: r.kind === 'host' ? 1 : r.count,
          vcpus: r.vcpus,
          memory_mb: r.memory_mb,
          disk_gb: r.disk_gb,
          image_id: r.image_id,
          network: effectiveNetwork === 'nat' ? r.network : '',
          bridge: effectiveNetwork === 'bridge' ? r.bridge : '',
        })),
        expose,
        expose_api: expose ? ports.api ?? 0 : 0,
        expose_http: expose ? ports.http ?? 0 : 0,
        expose_https: expose ? ports.https ?? 0 : 0,
        image_id: rows[0]?.image_id ?? 'ubuntu-24.04',
        ...(dry ? { prepare } : {}),
      }
      const res = await api<{ job_id: number }>(dry ? '/hub/clusters/dry-run' : '/hub/clusters', { method: 'POST', body })
      onStarted(t(dry ? 'clusters.dryStarted' : 'clusters.started', { name }), res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal title={t('clusters.newMultiTitle')} onClose={onClose} width={1000}>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      <p className="small muted">{t('clusters.newMultiBody')}</p>
      <ErrorNote error={error} />
      <ErrorNote error={hosts.error} />
      <div className="grid grid-2" style={{ marginBottom: '0.6rem' }}>
        <label>
          {t('clusters.name')}
          <Input value={name} onChange={(e) => setName(e.target.value.trim())} />
        </label>
        <label>
          {t('clusters.flavor')}
          <Select value={flavor} onChange={(v: 'k3s' | 'kubeadm') => setFlavor(v)} options={[{ value: 'k3s', label: t('clusters.flavorK3s') }, { value: 'kubeadm', label: t('clusters.flavorKubeadm') }]} />
        </label>
        <label>
          {t('clusters.network')}
          <Select
            value={network}
            onChange={(v: 'nat' | 'bridge' | 'wireguard') => setNetwork(v)}
            options={[
              { value: 'nat', label: t('clusters.netNAT'), disabled: distinctHosts > 1 },
              { value: 'bridge', label: t('clusters.netBridge') },
              { value: 'wireguard', label: t('clusters.netWireGuard'), disabled: true },
            ]}
          />
        </label>
      </div>

      <h3 style={{ margin: '0.4rem 0' }}>{t('clusters.placement')}</h3>
      <div className="table-wrap">
        <table className="ant-table" style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr>
              {[t('clusters.colHost'), t('clusters.colRole'), t('clusters.colKind'), t('clusters.colCount'), 'CPU', 'MB', 'GB', t('hosts.newVMImage'), network === 'bridge' ? t('clusters.colBridge') : t('hosts.newVMNetwork'), ''].map((h, i) => (
                <th key={i} style={{ textAlign: 'left', padding: '0.2rem 0.4rem' }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                <td style={{ padding: '0.2rem 0.4rem', minWidth: '10rem' }}>
                  <Select
                    size="small"
                    style={{ width: '100%' }}
                    value={r.host_id ?? undefined}
                    placeholder={t('clusters.pickHost')}
                    onChange={(v: number) => update(i, { host_id: v })}
                    options={online.map((h) => ({ value: h.id, label: h.name + (h.parent_id ? ' (VM)' : '') }))}
                  />
                </td>
                <td style={{ padding: '0.2rem 0.4rem' }}>
                  <Select size="small" value={r.role} onChange={(v: Row['role']) => update(i, { role: v })} options={[{ value: 'control-plane', label: 'control plane' }, { value: 'worker', label: 'worker' }]} />
                </td>
                <td style={{ padding: '0.2rem 0.4rem' }}>
                  <Select size="small" value={r.kind} onChange={(v: Row['kind']) => update(i, { kind: v })} options={[{ value: 'vm', label: t('clusters.kindVM') }, { value: 'host', label: t('clusters.kindHost') }]} />
                </td>
                <td style={{ padding: '0.2rem 0.4rem' }}><InputNumber size="small" min={1} max={20} value={r.kind === 'host' ? 1 : r.count} disabled={r.kind === 'host'} onChange={(v) => update(i, { count: v ?? 1 })} style={{ width: '4.5rem' }} /></td>
                <td style={{ padding: '0.2rem 0.4rem' }}><InputNumber size="small" min={1} value={r.vcpus} disabled={r.kind === 'host'} onChange={(v) => update(i, { vcpus: v ?? 2 })} style={{ width: '4.5rem' }} /></td>
                <td style={{ padding: '0.2rem 0.4rem' }}><InputNumber size="small" min={1024} step={1024} value={r.memory_mb} disabled={r.kind === 'host'} onChange={(v) => update(i, { memory_mb: v ?? 4096 })} style={{ width: '6rem' }} /></td>
                <td style={{ padding: '0.2rem 0.4rem' }}><InputNumber size="small" min={10} value={r.disk_gb} disabled={r.kind === 'host'} onChange={(v) => update(i, { disk_gb: v ?? 30 })} style={{ width: '5rem' }} /></td>
                <td style={{ padding: '0.2rem 0.4rem' }}><Input size="small" value={r.image_id} disabled={r.kind === 'host'} onChange={(e) => update(i, { image_id: e.target.value })} style={{ width: '9rem' }} /></td>
                <td style={{ padding: '0.2rem 0.4rem' }}>
                  {network === 'bridge' ? (
                    <Input size="small" value={r.bridge} disabled={r.kind === 'host'} placeholder="br0" onChange={(e) => update(i, { bridge: e.target.value.trim() })} style={{ width: '6rem' }} />
                  ) : (
                    <Input size="small" value={r.network} disabled={r.kind === 'host'} placeholder="default" onChange={(e) => update(i, { network: e.target.value.trim() })} style={{ width: '6rem' }} />
                  )}
                </td>
                <td style={{ padding: '0.2rem 0.4rem' }}>
                  <Button size="small" type="text" icon={<DeleteOutlined />} disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, j) => j !== i))} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Button size="small" icon={<PlusOutlined />} style={{ margin: '0.4rem 0 0.8rem' }} onClick={() => setRows([...rows, newRow('worker')])}>
        {t('clusters.addRow')}
      </Button>

      <div className="col" style={{ gap: '0.4rem', marginBottom: '0.6rem' }}>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={cilium} onChange={(e) => setCilium(e.target.checked)} />
          <span>{t('clusters.cilium')} <span className="small muted">{t('clusters.ciliumHint')}</span></span>
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', paddingLeft: '1.6rem' }}>
          <Checkbox checked={cilium && kpr} disabled={!cilium} onChange={(e) => setKPR(e.target.checked)} />
          <span>{t('clusters.kpr')} <span className="small muted">{t('clusters.kprHint')}</span></span>
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={expose} onChange={(e) => setExpose(e.target.checked)} />
          <span>{t('clusters.expose')} <span className="small muted">{t('clusters.exposeMultiHint')}</span></span>
        </label>
        {expose && (
          <div className="row" style={{ gap: '0.5rem', paddingLeft: '1.6rem', flexWrap: 'wrap' }}>
            <InputNumber min={0} max={65535} value={ports.api} onChange={(v) => setPorts({ ...ports, api: v })} addonBefore="API" style={{ width: '10rem' }} />
            <InputNumber min={0} max={65535} value={ports.http} onChange={(v) => setPorts({ ...ports, http: v })} addonBefore="HTTP" style={{ width: '10rem' }} />
            <InputNumber min={0} max={65535} value={ports.https} onChange={(v) => setPorts({ ...ports, https: v })} addonBefore="HTTPS" style={{ width: '10rem' }} />
          </div>
        )}
      </div>
      <div className="row" style={{ justifyContent: 'flex-end', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={prepare} onChange={(e) => setPrepare(e.target.checked)} />
          <span className="small">{t('clusters.dryPrepare')}</span>
        </label>
        <Tooltip title={t('clusters.dryHint')}>
          <Button loading={busy === 'dry'} disabled={!valid || busy === 'create'} onClick={() => void start(true)}>
            {t('clusters.dryRun')}
          </Button>
        </Tooltip>
        <Button type="primary" loading={busy === 'create'} disabled={!valid || busy === 'dry'} onClick={() => void start(false)}>
          {t('clusters.create')}
        </Button>
      </div>
    </Modal>
  )
}
