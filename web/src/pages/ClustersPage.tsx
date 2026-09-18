import { useEffect, useState, type CSSProperties } from 'react'
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

/** Что известно о хосте для строки размещения: каталог образов, мосты. */
interface HostInfo {
  images: { id: string; name: string; downloaded: boolean }[]
  bridges: string[]
  networks: string[]
  loading: boolean
  error?: string
}

const newRow = (role: Row['role']): Row => ({ host_id: null, role, kind: 'vm', count: 1, vcpus: 2, memory_mb: 4096, disk_gb: 30, image_id: '', network: '', bridge: '' })

const cell: CSSProperties = { display: 'flex', flexDirection: 'column', gap: '0.15rem' }

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
  const [endpoints, setEndpoints] = useState<Record<number, string>>({})
  const [info, setInfo] = useState<Record<number, HostInfo>>({})
  const [expose, setExpose] = useState(true)
  const [ports, setPorts] = useState<{ api: number | null; http: number | null; https: number | null }>({ api: 6443, http: 80, https: 443 })
  const [prepare, setPrepare] = useState(true)
  const [busy, setBusy] = useState<'create' | 'dry' | null>(null)
  const [error, setError] = useState<string | null>(null)

  const hostIDs = [...new Set(rows.map((r) => r.host_id).filter((h): h is number => h !== null))]
  const distinctHosts = hostIDs.length
  const effectiveNetwork = distinctHosts <= 1 && network === 'bridge' && rows.every((r) => !r.bridge) ? 'nat' : network
  const cpCount = rows.filter((r) => r.role === 'control-plane').reduce((n, r) => n + (r.kind === 'host' ? 1 : r.count), 0)
  const update = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  // Образы и мосты — с хоста строки: у каждого хоста свой каталог.
  useEffect(() => {
    for (const id of hostIDs) {
      if (info[id]) continue
      setInfo((m) => ({ ...m, [id]: { images: [], bridges: [], networks: [], loading: true } }))
      void (async () => {
        try {
          const [img, pf] = await Promise.all([
            api<{ catalog: { id: string; name: string }[]; local: { id: string; downloaded: boolean }[] }>(`/hosts/${id}/vm/images`),
            api<{ bridges?: string[]; networks?: { name: string }[] }>(`/hosts/${id}/vm/preflight`),
          ])
          const downloaded = new Set((img.local ?? []).filter((l) => l.downloaded).map((l) => l.id))
          setInfo((m) => ({
            ...m,
            [id]: {
              images: (img.catalog ?? []).map((i) => ({ id: i.id, name: i.name, downloaded: downloaded.has(i.id) })),
              bridges: pf.bridges ?? [],
              networks: (pf.networks ?? []).map((n) => n.name),
              loading: false,
            },
          }))
        } catch (err) {
          setInfo((m) => ({ ...m, [id]: { images: [], bridges: [], networks: [], loading: false, error: err instanceof Error ? err.message : String(err) } }))
        }
      })()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostIDs.join(',')])

  const defaultImage = (id: number | null) => {
    const imgs = id !== null ? info[id]?.images ?? [] : []
    return imgs.find((i) => /ubuntu.*24/i.test(i.id + i.name))?.id || imgs[0]?.id || ''
  }
  const imageOf = (r: Row) => r.image_id || defaultImage(r.host_id)

  const problems: string[] = []
  if (cpCount !== 1 && cpCount !== 3) problems.push(t('clusters.cpCountHint', { n: cpCount }))
  if (cpCount === 3 && flavor === 'kubeadm') problems.push(t('clusters.cp3K3sOnly'))
  if (effectiveNetwork === 'bridge') {
    for (const r of rows) if (r.kind === 'vm' && r.host_id !== null && !r.bridge) problems.push(t('clusters.bridgeMissingRow'))
  }
  if (effectiveNetwork === 'nat' && distinctHosts > 1) problems.push(t('clusters.natSingle'))
  const valid = !!name && rows.length > 0 && rows.every((r) => r.host_id !== null && (r.kind === 'host' || (r.count > 0 && imageOf(r)))) && problems.length === 0

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
          image_id: r.kind === 'vm' ? imageOf(r) : '',
          network: effectiveNetwork === 'nat' ? r.network : '',
          bridge: effectiveNetwork === 'bridge' ? r.bridge : '',
          endpoint: effectiveNetwork === 'wireguard' && r.host_id !== null ? (endpoints[r.host_id] ?? '').trim() : '',
        })),
        expose,
        expose_api: expose ? ports.api ?? 0 : 0,
        expose_http: expose ? ports.http ?? 0 : 0,
        expose_https: expose ? ports.https ?? 0 : 0,
        image_id: imageOf(rows[0]) || 'ubuntu-24.04',
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

  const hostName = (id: number) => online.find((h) => h.id === id)?.name ?? `#${id}`
  const hostAddr = (id: number) => online.find((h) => h.id === id)?.addr ?? ''

  return (
    <Modal title={t('clusters.newMultiTitle')} onClose={onClose} width={1180}>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      <p className="small muted">{t('clusters.newMultiBody')}</p>
      <ErrorNote error={error} />
      <ErrorNote error={hosts.error} />
      <div className="grid grid-3" style={{ marginBottom: '0.6rem' }}>
        <label>
          {t('clusters.name')}
          <Input value={name} onChange={(e) => setName(e.target.value.trim())} />
        </label>
        <label>
          {t('clusters.flavor')}
          <Select value={flavor} onChange={(v: 'k3s' | 'kubeadm') => setFlavor(v)} options={[{ value: 'k3s', label: t('clusters.flavorK3s') }, { value: 'kubeadm', label: t('clusters.flavorKubeadm') }]} />
          <span className="small muted">{t('clusters.flavorHint')}</span>
        </label>
        <label>
          {t('clusters.network')}
          <Select
            value={network}
            onChange={(v: 'nat' | 'bridge' | 'wireguard') => setNetwork(v)}
            options={[
              { value: 'nat', label: t('clusters.netNAT'), disabled: distinctHosts > 1 },
              { value: 'bridge', label: t('clusters.netBridge') },
              { value: 'wireguard', label: t('clusters.netWireGuard') },
            ]}
          />
          <span className="small muted">{network === 'wireguard' ? t('clusters.netWireGuardHint') : network === 'bridge' ? t('clusters.netBridgeHint') : t('clusters.netNATHint')}</span>
        </label>
      </div>

      <h3 style={{ margin: '0.4rem 0' }}>{t('clusters.placement')}</h3>
      <p className="small muted" style={{ marginTop: 0 }}>{t('clusters.placementHint')}</p>
      <div className="col" style={{ gap: '0.5rem' }}>
        {rows.map((r, i) => {
          const hi = r.host_id !== null ? info[r.host_id] : undefined
          return (
            <div key={i} style={{ border: '1px solid var(--border)', borderRadius: 6, padding: '0.5rem 0.6rem' }}>
              <div className="row" style={{ gap: '0.6rem', alignItems: 'flex-end' }}>
                <div style={{ ...cell, minWidth: '14rem', flex: 1 }}>
                  <span className="small muted">{t('clusters.colHost')}</span>
                  <Select
                    size="small"
                    value={r.host_id ?? undefined}
                    placeholder={t('clusters.pickHost')}
                    onChange={(v: number) => update(i, { host_id: v, image_id: '', bridge: '' })}
                    options={online.map((h) => ({ value: h.id, label: h.name + (h.parent_id ? ' (VM)' : '') + (h.addr ? ` — ${h.addr}` : '') }))}
                  />
                </div>
                <div style={{ ...cell, minWidth: '11rem' }}>
                  <span className="small muted">{t('clusters.colKind')}</span>
                  <Select size="small" value={r.kind} onChange={(v: Row['kind']) => update(i, { kind: v })} options={[{ value: 'vm', label: t('clusters.kindVM') }, { value: 'host', label: t('clusters.kindHost') }]} />
                </div>
                <div style={{ ...cell, minWidth: '9rem' }}>
                  <span className="small muted">{t('clusters.colRole')}</span>
                  <Select size="small" value={r.role} onChange={(v: Row['role']) => update(i, { role: v })} options={[{ value: 'control-plane', label: 'control plane' }, { value: 'worker', label: 'worker' }]} />
                </div>
                {r.kind === 'vm' && (
                  <div style={cell}>
                    <span className="small muted">{t('clusters.colCount')}</span>
                    <InputNumber size="small" min={1} max={20} value={r.count} onChange={(v) => update(i, { count: v ?? 1 })} style={{ width: '4.5rem' }} />
                  </div>
                )}
                <Button size="small" type="text" icon={<DeleteOutlined />} disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, j) => j !== i))} />
              </div>
              {r.kind === 'vm' && (
                <div className="row" style={{ gap: '0.6rem', alignItems: 'flex-end', marginTop: '0.4rem' }}>
                  <div style={cell}>
                    <span className="small muted">CPU</span>
                    <InputNumber size="small" min={1} value={r.vcpus} onChange={(v) => update(i, { vcpus: v ?? 2 })} style={{ width: '4.5rem' }} />
                  </div>
                  <div style={cell}>
                    <span className="small muted">{t('clusters.colMem')}</span>
                    <InputNumber size="small" min={1024} step={1024} value={r.memory_mb} onChange={(v) => update(i, { memory_mb: v ?? 4096 })} style={{ width: '6rem' }} />
                  </div>
                  <div style={cell}>
                    <span className="small muted">{t('clusters.colDisk')}</span>
                    <InputNumber size="small" min={10} value={r.disk_gb} onChange={(v) => update(i, { disk_gb: v ?? 30 })} style={{ width: '5rem' }} />
                  </div>
                  <div style={{ ...cell, minWidth: '16rem', flex: 1 }}>
                    <span className="small muted">{t('hosts.newVMImage')}</span>
                    <Select
                      size="small"
                      value={imageOf(r) || undefined}
                      loading={hi?.loading}
                      placeholder={r.host_id === null ? t('clusters.pickHostFirst') : t('clusters.pickImage')}
                      disabled={r.host_id === null}
                      onChange={(v: string) => update(i, { image_id: v })}
                      options={(hi?.images ?? []).map((img) => ({ value: img.id, label: img.name + (img.downloaded ? '' : ` ${t('hosts.newVMWillDownload')}`) }))}
                    />
                  </div>
                  {effectiveNetwork === 'bridge' && (
                    <div style={{ ...cell, minWidth: '10rem' }}>
                      <span className="small muted">{t('clusters.colBridge')}</span>
                      {hi && !hi.loading && hi.bridges.length === 0 ? (
                        <span className="small" style={{ color: 'var(--series-8)' }}>{t('clusters.noBridges')}</span>
                      ) : (
                        <Select
                          size="small"
                          value={r.bridge || undefined}
                          loading={hi?.loading}
                          placeholder={r.host_id === null ? t('clusters.pickHostFirst') : t('clusters.pickBridge')}
                          disabled={r.host_id === null}
                          onChange={(v: string) => update(i, { bridge: v })}
                          options={(hi?.bridges ?? []).map((b) => ({ value: b, label: b }))}
                        />
                      )}
                    </div>
                  )}
                  {effectiveNetwork === 'nat' && (
                    <div style={{ ...cell, minWidth: '10rem' }}>
                      <span className="small muted">{t('hosts.newVMNetwork')}</span>
                      <Select
                        size="small"
                        value={r.network || hi?.networks[0] || 'default'}
                        loading={hi?.loading}
                        disabled={r.host_id === null}
                        onChange={(v: string) => update(i, { network: v })}
                        options={(hi?.networks.length ? hi.networks : ['default']).map((n) => ({ value: n, label: n }))}
                      />
                    </div>
                  )}
                </div>
              )}
              {hi?.error && <ErrorNote error={hi.error} />}
            </div>
          )
        })}
      </div>
      <Button size="small" icon={<PlusOutlined />} style={{ margin: '0.5rem 0 0.8rem' }} onClick={() => setRows([...rows, newRow('worker')])}>
        {t('clusters.addRow')}
      </Button>

      {effectiveNetwork === 'wireguard' && hostIDs.length > 0 && (
        <div style={{ marginBottom: '0.8rem' }}>
          <h3 style={{ margin: '0.2rem 0' }}>{t('clusters.endpoints')}</h3>
          <p className="small muted" style={{ marginTop: 0 }}>{t('clusters.endpointsHint')}</p>
          <div className="row" style={{ gap: '0.6rem' }}>
            {hostIDs.map((id) => (
              <label key={id} style={{ minWidth: '16rem' }}>
                <span className="small">{hostName(id)}</span>
                <Input size="small" value={endpoints[id] ?? ''} placeholder={hostAddr(id)} onChange={(e) => setEndpoints({ ...endpoints, [id]: e.target.value })} />
              </label>
            ))}
          </div>
        </div>
      )}

      {problems.length > 0 && (
        <ul className="small" style={{ color: 'var(--series-8)', margin: '0 0 0.6rem', paddingLeft: '1.2rem' }}>
          {[...new Set(problems)].map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}

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
