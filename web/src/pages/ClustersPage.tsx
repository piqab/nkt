import { useEffect, useRef, useState, type CSSProperties } from 'react'
import * as yaml from 'js-yaml'
import { LonghornField, addonsBody, type LonghornChoice } from '../components/ClusterAddons'
import { OsIcon, OsIconOf } from '../components/OsIcon'
import { guardUnload } from '../components/uploadJob'
import { Sensitive } from '../privacy'
import { Button, Checkbox, Input, InputNumber, Progress, Select, Tooltip } from 'antd'
import { DeleteOutlined, PlusOutlined, UploadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { HubManifestsCard } from '../components/HubManifests'
import { ClustersFindingsCard, HelmMultiModal } from '../components/HubClustersMulti'
import type { HubHost, Job, Me, NetworkInterface } from '../types'
import { nicLabel, nicService, nicSuitable, nicV4 } from '../nics'
import { Banner, Card, ErrorNote, Modal, formatBytesShort, formatRelative } from '../components/ui'
import { confirmAction } from '../components/confirm'
import { ClustersCard, ListenerFields, listenerBody, type ClusterGateway } from '../components/Clusters'
import { StepsChecklist, stepConflicts, stepsChosen, type ClusterStep, type StepSelection } from '../components/ClusterSteps'
import { EditTextModal } from '../components/EditTextModal'
import { JobLogModal } from './Jobs'
import { TitleHelp } from '../components/Docs'
import { msg, tx, type Msg } from '../msg'

/**
 * Раздел «Кластеры» на хабе: все кластеры Kubernetes и создание кластера
 * на нескольких хостах — таблица размещения (хост → роль → машины или
 * сам хост → размеры), сеть между хостами, Cilium, проброс. Задание и
 * проверки те же, что у диалога «новый кластер» у хоста.
 */

// Дефолтные Helm values для Cilium: cni.binPath=/usr/lib/cni решает проблему
// с поиском плагина CNI на Debian/Ubuntu (kubelet ищет в /usr/lib/cni,
// Cilium chart по умолчанию монтирует в /opt/cni/bin).
const CILIUM_DEFAULT_VALUES = `cni:
  binPath: /usr/lib/cni
kubeProxyReplacement: true
ipv4:
  enabled: true
ipv6:
  enabled: false
bpf:
  masquerade: true

gatewayAPI:
  enabled: true
  hostNetwork:
    enabled: true

l7Proxy: true

nodePort:
  enabled: true
externalIPs:
  enabled: true
hostServices:
  enabled: true
`

/** Образ из библиотеки хаба: загружен один раз, заливается на хосты. */
export interface ClusterImage {
  name: string
  size: number
  mod_time: string
}

interface Row {
  host_id: number | null
  role: 'control-plane' | 'worker' | 'load-balancer'
  kind: 'vm' | 'host'
  count: number
  vcpus: number
  memory_mb: number
  disk_gb: number
  image_id: string
  network: string
  bridge: string
  // nic — устройство хоста, в которое машина включается напрямую (macvtap):
  // на каждом хосте своё, поэтому оно в строке, а не в режиме целиком.
  nic: string
}

/** Что известно о хосте для строки размещения: каталог образов, мосты. */
interface HostInfo {
  images: { id: string; name: string; downloaded: boolean }[]
  bridges: string[]
  networks: string[]
  // nics — устройства, годные под macvtap; nicsUnknown — хост про пригодность
  // устройств не ответил (агент старше той проверки), и список собран по
  // снимку интерфейсов: выбирать можно, ручается хост при создании машины.
  nics: { name: string; addr: string }[]
  nicsUnknown: boolean
  loading: boolean
  error?: string
}

const newRow = (role: Row['role']): Row => ({ host_id: null, role, kind: 'vm', count: 1, vcpus: role === 'load-balancer' ? 1 : 2, memory_mb: role === 'load-balancer' ? 1024 : 4096, disk_gb: role === 'load-balancer' ? 10 : 30, image_id: '', network: '', bridge: '', nic: '' })

const cell: CSSProperties = { display: 'flex', flexDirection: 'column', gap: '0.15rem' }

export default function ClustersPage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [creating, setCreating] = useState(false)
  const [helmMulti, setHelmMulti] = useState(false)
  const [hubJob, setHubJob] = useState<Job | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: Msg } | null>(null)
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
          <TitleHelp>{t('clusters.pageHint')}</TitleHelp>
        </h1>
        {me.is_admin && (
          <div className="row" style={{ gap: '0.5rem' }}>
            <Button onClick={() => setHelmMulti(true)}>{t('helmMulti.button')}</Button>
            <Button type="primary" onClick={() => setCreating(true)}>
              {t('clusters.newMulti')}
            </Button>
          </div>
        )}
      </div>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {msg(notice.text)}
        </Banner>
      )}
      <ClustersCard key={tick} onOpenJob={(id) => void openHubJob(id)} onChanged={() => setTick((n) => n + 1)} showEmpty />
      <ClusterImagesCard canEdit={me.is_admin} />
      {me.is_admin && <ClustersFindingsCard />}
      {me.is_admin && <HubManifestsCard />}
      {helmMulti && (
        <HelmMultiModal
          onClose={() => setHelmMulti(false)}
          onStarted={(id) => {
            setHelmMulti(false)
            void openHubJob(id)
          }}
        />
      )}
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

/** Библиотека образов хаба: загрузить qcow2/img/raw один раз — и любой
 * кластер на любом хосте создаётся из него (хаб заливает образ на хост
 * сам, если его там ещё нет). */
function ClusterImagesCard({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation()
  const images = useApi<{ images: ClusterImage[] }>('/hub/cluster-images')
  const [uploading, setUploading] = useState<number | null>(null)
  const [error, setError] = useState<Msg | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  function upload(file: File) {
    setError(null)
    setUploading(0)
    // Образ — гигабайты: пока он идёт, закрытие вкладки переспросит.
    const release = guardUnload()
    const xhr = new XMLHttpRequest()
    xhr.onloadend = release
    xhr.open('POST', `/api/hub/cluster-images/upload?name=${encodeURIComponent(file.name)}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setUploading(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      setUploading(null)
      if (xhr.status === 200) {
        images.reload()
        return
      }
      try {
        setError((JSON.parse(xhr.responseText) as { error?: string }).error ?? t('common.httpCode', { code: xhr.status }))
      } catch {
        setError(tx('common.httpCode', { code: xhr.status }))
      }
    }
    xhr.onerror = () => {
      setUploading(null)
      setError(tx('vmimages.uploadFailed'))
    }
    xhr.send(file)
  }

  async function remove(name: string) {
    if (!(await confirmAction(t('clusters.imageDeleteConfirm', { name })))) return
    try {
      await api(`/hub/cluster-images/${encodeURIComponent(name)}`, { method: 'DELETE' })
      images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const list = images.data?.images ?? []
  return (
    <Card
      title={t('clusters.imagesTitle')}
      subtitle={t('clusters.imagesHint')}
      actions={
        canEdit ? (
          <>
            <input ref={fileRef} type="file" accept=".qcow2,.img,.raw" hidden onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
            <Button size="small" icon={<UploadOutlined />} disabled={uploading !== null} onClick={() => fileRef.current?.click()}>
              {t('clusters.imageUpload')}
            </Button>
          </>
        ) : undefined
      }
    >
      <ErrorNote error={msg(error)} />
      <ErrorNote error={images.error} />
      {uploading !== null && <Progress percent={uploading} size="small" style={{ marginBottom: '0.5rem' }} />}
      {list.length === 0 ? (
        <p className="small muted" style={{ margin: 0 }}>{t('clusters.imagesNone')}</p>
      ) : (
        <table className="ant-table" style={{ width: '100%', borderCollapse: 'collapse' }}>
          <tbody>
            {list.map((img) => (
              <tr key={img.name}>
                <td style={{ padding: '0.2rem 0.4rem' }}><code>hub:{img.name}</code></td>
                <td style={{ padding: '0.2rem 0.4rem' }} className="small muted">{formatBytesShort(img.size)}</td>
                <td style={{ padding: '0.2rem 0.4rem' }} className="small muted">{formatRelative(img.mod_time)}</td>
                <td style={{ padding: '0.2rem 0.4rem', textAlign: 'right' }}>
                  {canEdit && <Button size="small" type="text" icon={<DeleteOutlined />} onClick={() => void remove(img.name)} />}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Card>
  )
}

function MultiClusterModal({ onClose, onStarted }: { onClose: () => void; onStarted: (text: Msg, jobID: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const online = (hosts.data ?? []).filter((h) => h.status === 'online' && h.id > 0)
  const hubImages = useApi<{ images: ClusterImage[] }>('/hub/cluster-images')
  const [name, setName] = useState('k8s')
  const [flavor, setFlavor] = useState<'k3s' | 'kubeadm'>('k3s')
  const [k8sVersion, setK8sVersion] = useState('')
  const versions = useApi<{ stable: string; versions: string[] }>('/hub/k8s-versions', 600_000)
  const [cilium, setCilium] = useState(true)
  const [kpr, setKPR] = useState(false)
  const [longhorn, setLonghorn] = useState<LonghornChoice>({ on: false, version: '' })
  const [network, setNetwork] = useState<'nat' | 'bridge' | 'macvtap' | 'wireguard'>('bridge')
  // Общий адрес control plane и всё, что вокруг него (см. ListenerFields).
  const gws = useApi<{ gateways: ClusterGateway[] }>('/hub/cluster/gateways', 60_000)
  const [endpoint, setEndpoint] = useState('')
  const [gateway, setGateway] = useState(0)
  const [allow, setAllow] = useState('')
  const [rows, setRows] = useState<Row[]>([newRow('control-plane'), newRow('worker')])
  const [endpoints, setEndpoints] = useState<Record<number, string>>({})
  const [info, setInfo] = useState<Record<number, HostInfo>>({})
  const [expose, setExpose] = useState(true)
  const [ports, setPorts] = useState<{ api: number | null; http: number | null; https: number | null }>({ api: 6443, http: 80, https: 443 })
  const [prepare, setPrepare] = useState(true)
  const [busy, setBusy] = useState<'create' | 'dry' | 'steps' | null>(null)
  const [error, setError] = useState<Msg | null>(null)
  // Встроенный балансировщик: 1–2 машины с HAProxy (+ keepalived при HA).
  const [lbEnabled, setLbEnabled] = useState(false)
  const [lbCount, setLbCount] = useState(1)
  const [lbVIP, setLbVIP] = useState('')
  const [lbRows, setLbRows] = useState<Row[]>([newRow('load-balancer')])
  const [lbHaproxyOpen, setLbHaproxyOpen] = useState(false)
  const [lbHaproxy, setLbHaproxy] = useState({ connect: 5000, client: 50000, server: 50000, maxConn: 2000, statsPort: 8404, algorithm: 'source' as 'source' | 'roundrobin' })
  // Helm values для Cilium (редактор YAML).
  const [ciliumValuesOpen, setCiliumValuesOpen] = useState(false)
  const [ciliumValuesDraft, setCiliumValuesDraft] = useState('')
  const [ciliumValuesError, setCiliumValuesError] = useState<string | null>(null)
  // Журнал сухого прогона — поверх формы, настройки не теряются.
  const [dryJob, setDryJob] = useState<Job | null>(null)
  // Второй шаг формы — чек-лист действий задания (тот же, что у диалога
  // «новый кластер» у хоста): что именно создаст «Создать кластер».
  const [stage, setStage] = useState<'form' | 'steps'>('form')
  const [stepsPreview, setStepsPreview] = useState<ClusterStep[] | null>(null)
  const [stepsSel, setStepsSel] = useState<StepSelection>({})

  // Сохранённые наборы: вся форма одним JSON на хабе; хосты в наборе —
  // по имени, чтобы набор пережил пересоздание хоста.
  interface PresetForm {
    name: string
    flavor: 'k3s' | 'kubeadm'
    k8s_version: string
    cilium: boolean
    kpr: boolean
    longhorn?: LonghornChoice
    network: 'nat' | 'bridge' | 'macvtap' | 'wireguard'
    rows: (Omit<Row, 'host_id'> & { host: string })[]
    // endpoints — адреса хостов для туннеля (по хосту), endpoint — общий
    // адрес control plane всего кластера.
    endpoints: Record<string, string>
    expose: boolean
    ports: { api: number | null; http: number | null; https: number | null }
    // Шлюз — адресом, как хосты в строках размещения: id шлюза живёт на
    // хабе и меняется, когда шлюз пересоздают, адрес же остаётся тем, что
    // человек видит в списке.
    endpoint?: string
    gateway_addr?: string
    allow?: string
    // Встроенный балансировщик.
    lb_enabled?: boolean
    lb_count?: number
    lb_vip?: string
    lb_rows?: (Omit<Row, 'host_id'> & { host: string })[]
    lb_haproxy?: { connect: number; client: number; server: number; maxConn: number; statsPort: number; algorithm: 'source' | 'roundrobin' }
  }
  const presets = useApi<{ presets: { id: number; name: string; form: string }[] }>('/hub/cluster-presets')
  const [presetName, setPresetName] = useState('')
  const [presetBusy, setPresetBusy] = useState(false)
  const [presetNote, setPresetNote] = useState<Msg | null>(null)
  const hostNameOf = (id: number | null) => (id === null ? '' : (hosts.data ?? []).find((h) => h.id === id)?.name ?? '')

  async function savePreset() {
    const pname = presetName.trim()
    if (!pname) return
    if ((presets.data?.presets ?? []).some((p) => p.name === pname) && !(await confirmAction(t('clusters.presetOverwrite', { name: pname })))) return
    setPresetBusy(true)
    try {
      const form: PresetForm = {
        name, flavor, k8s_version: k8sVersion, cilium, kpr, longhorn, network, expose, ports,
        rows: rows.map(({ host_id, ...r }) => ({ ...r, host: hostNameOf(host_id) })),
        endpoints: Object.fromEntries(Object.entries(endpoints).map(([id, v]) => [hostNameOf(Number(id)), v]).filter(([h]) => h)),
        endpoint,
        gateway_addr: (gws.data?.gateways ?? []).find((g) => g.edge_id === gateway)?.address ?? '',
        allow,
        lb_enabled: lbEnabled,
        lb_count: lbEnabled ? lbCount : undefined,
        lb_vip: lbEnabled ? lbVIP.trim() : undefined,
        lb_rows: lbEnabled ? lbRows.map(({ host_id, ...r }) => ({ ...r, host: hostNameOf(host_id) })) : undefined,
        lb_haproxy: lbEnabled ? lbHaproxy : undefined,
      }
      await api('/hub/cluster-presets', { method: 'POST', body: { name: pname, form } })
      await presets.reload()
      setPresetNote(tx('clusters.presetSaved', { name: pname }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPresetBusy(false)
    }
  }

  function applyPreset(id: number) {
    const p = (presets.data?.presets ?? []).find((x) => x.id === id)
    if (!p) return
    let form: PresetForm
    try {
      form = JSON.parse(p.form) as PresetForm
    } catch {
      setError(tx('clusters.presetBroken', { name: p.name }))
      return
    }
    const byName = new Map((hosts.data ?? []).map((h) => [h.name, h.id]))
    const missing: string[] = []
    setName(form.name ?? 'k8s')
    setFlavor(form.flavor ?? 'k3s')
    setK8sVersion(form.k8s_version ?? '')
    setCilium(form.cilium ?? true)
    setKPR(form.kpr ?? false)
    setLonghorn(form.longhorn ?? { on: false, version: '' })
    setNetwork(form.network ?? 'bridge')
    setExpose(form.expose ?? true)
    setPorts(form.ports ?? { api: 6443, http: 80, https: 443 })
    setRows(
      (form.rows ?? []).map(({ host, ...r }) => {
        const hid = byName.get(host)
        if (host && hid === undefined) missing.push(host)
        return { ...newRow(r.role), ...r, host_id: hid ?? null }
      }),
    )
    setEndpoints(Object.fromEntries(Object.entries(form.endpoints ?? {}).flatMap(([h, v]) => (byName.has(h) ? [[String(byName.get(h)), v]] : []))))
    setEndpoint(form.endpoint ?? '')
    setAllow(form.allow ?? '')
    const gwAddr = form.gateway_addr ?? ''
    const gw = (gws.data?.gateways ?? []).find((g) => g.address === gwAddr)
    setGateway(gw?.edge_id ?? 0)
    // Встроенный балансировщик.
    setLbEnabled(form.lb_enabled ?? false)
    setLbCount(form.lb_count ?? 1)
    setLbVIP(form.lb_vip ?? '')
    if (form.lb_rows) {
      setLbRows(form.lb_rows.map(({ host, ...r }) => {
        const hid = byName.get(host)
        if (host && hid === undefined) missing.push(host)
        return { ...newRow(r.role), ...r, host_id: hid ?? null }
      }))
    }
    if (form.lb_haproxy) {
      setLbHaproxy(form.lb_haproxy)
    }
    setPresetName(p.name)
    const notes: string[] = []
    if (missing.length) notes.push(t('clusters.presetHostsMissing', { hosts: [...new Set(missing)].join(', ') }))
    // Шлюз из набора есть, а на хабе его нет: слушатель поедет туда, куда
    // скажет адрес, — промолчать значит обещать выбранный шлюз.
    if (gwAddr && !gw) notes.push(t('clusters.presetGatewayMissing', { addr: gwAddr }))
    setPresetNote(notes.length ? notes.join(' ') : t('clusters.presetApplied', { name: p.name }))
  }

  async function deletePreset(id: number) {
    const p = (presets.data?.presets ?? []).find((x) => x.id === id)
    if (!p || !(await confirmAction(t('clusters.presetDeleteConfirm', { name: p.name })))) return
    try {
      await api(`/hub/cluster-presets/${id}`, { method: 'DELETE' })
      await presets.reload()
      setPresetNote(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const hostIDs = [...new Set(rows.map((r) => r.host_id).filter((h): h is number => h !== null))]
  const distinctHosts = hostIDs.length
  const effectiveNetwork = distinctHosts <= 1 && network === 'bridge' && rows.every((r) => !r.bridge) ? 'nat' : network
  const cpCount = rows.filter((r) => r.role === 'control-plane').reduce((n, r) => n + (r.kind === 'host' ? 1 : r.count), 0)
  const update = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const updateLb = (i: number, patch: Partial<Row>) => setLbRows(lbRows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  // Образы, мосты и устройства — с хоста строки: у каждого хоста свой
  // каталог, свои мосты и свои интерфейсы.
  useEffect(() => {
    for (const id of hostIDs) {
      if (info[id]) continue
      setInfo((m) => ({ ...m, [id]: { images: [], bridges: [], networks: [], nics: [], nicsUnknown: false, loading: true } }))
      void (async () => {
        try {
          const [img, pf, ifs] = await Promise.all([
            api<{ catalog: { id: string; name: string }[]; local: { id: string; downloaded: boolean }[] }>(`/hosts/${id}/vm/images`),
            api<{ bridges?: string[]; networks?: { name: string }[]; direct?: Record<string, string> | null }>(`/hosts/${id}/vm/preflight`),
            // Адреса устройств — из снимка: за пригодность отвечает хост, а
            // подпись «eth0 — 192.168.1.5» он не даёт, выбирать же надо вслепую
            // без неё. Снимок недоступен — список останется без адресов.
            api<{ interfaces?: NetworkInterface[] }>(`/hosts/${id}/interfaces`).catch(() => ({ interfaces: [] as NetworkInterface[] })),
          ])
          const downloaded = new Set((img.local ?? []).filter((l) => l.downloaded).map((l) => l.id))
          const addrOf = new Map((ifs.interfaces ?? []).map((i) => [i.name, nicV4(i)]))
          const fit = (name: string) => ({ name, addr: addrOf.get(name) ?? '' })
          // Пустая причина в ответе хоста — «годится»: тот же ответ, по которому
          // создание машины откажет, поэтому ручаться на него можно. Но годность
          // хост смотрит по состоянию устройства, а по имени — нет: поднятый
          // virbr0 или wg0 годится и вернёт пустую причину, а машина в них
          // окажется там, откуда ей не выйти. Имя фильтруем здесь — и в обеих
          // ветках, чтобы список не зависел от того, ответил ли хост.
          const known = pf.direct != null
          const nics = (known
            ? Object.entries(pf.direct!).filter(([name, reason]) => reason === '' && !nicService(name)).map(([name]) => name)
            : (ifs.interfaces ?? []).filter(nicSuitable).map((i) => i.name)
          ).map(fit).sort((a, b) => a.name.localeCompare(b.name))
          setInfo((m) => ({
            ...m,
            [id]: {
              images: (img.catalog ?? []).map((i) => ({ id: i.id, name: i.name, downloaded: downloaded.has(i.id) })),
              bridges: pf.bridges ?? [],
              networks: (pf.networks ?? []).map((n) => n.name),
              nics,
              nicsUnknown: !known,
              loading: false,
            },
          }))
        } catch (err) {
          setInfo((m) => ({ ...m, [id]: { images: [], bridges: [], networks: [], nics: [], nicsUnknown: false, loading: false, error: err instanceof Error ? err.message : String(err) } }))
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
  // Три control plane по kubeadm живут по общему адресу: без него вход
  // второго и третьего узла идёт по адресу первого, и кластер висит на нём.
  // При включённом LB endpoint устанавливается автоматически — не требуем.
  if (cpCount === 3 && flavor === 'kubeadm' && !endpoint.trim() && !lbEnabled) problems.push(t('clusters.endpointNeeded'))
  if (effectiveNetwork === 'bridge') {
    for (const r of rows) if (r.kind === 'vm' && r.host_id !== null && !r.bridge) problems.push(t('clusters.bridgeMissingRow'))
  }
  // Валидация LB: при HA-паре VIP обязателен.
  if (lbEnabled) {
    if (lbCount === 2 && !lbVIP.trim()) problems.push(t('clusters.lbVIPNeeded'))
    if (lbRows.length === 0) problems.push(t('clusters.lbNeedsPlacements'))
    for (const r of lbRows) {
      if (r.kind === 'vm' && r.host_id !== null && !r.image_id) problems.push(t('clusters.lbNeedsImage'))
      if (effectiveNetwork === 'macvtap' && r.kind === 'vm' && r.host_id !== null && !r.nic) problems.push(t('clusters.nicMissingRow'))
    }
  }
  if (effectiveNetwork === 'nat' && distinctHosts > 1) problems.push(t('clusters.natSingle'))
  // macvtap: устройство выбирается на каждом хосте своё, и без него строка
  // с машинами не значит ничего — хаб не угадывает, куда включать машину.
  if (effectiveNetwork === 'macvtap') {
    for (const r of rows) if (r.kind === 'vm' && r.host_id !== null && !r.nic) problems.push(t('clusters.nicMissingRow'))
    // Проброс с хоста на его же macvtap-машину не работает: хост с ними не
    // говорит. Общий адрес кластера при этом остаётся — его обслуживает шлюз.
    if (expose) problems.push(t('clusters.exposeMacvtap'))
  }
  const valid = !!name && rows.length > 0 && rows.every((r) => r.host_id !== null && (r.kind === 'host' || (r.count > 0 && imageOf(r)))) && problems.length === 0 && (!lbEnabled || lbRows.every((r) => r.host_id !== null && r.image_id))

  const buildBody = () => ({
    name,
    flavor,
    k8s_version: k8sVersion,
    cni: cilium ? 'cilium' : '',
    kube_proxy_replacement: cilium && kpr,
    cilium_values: cilium ? ciliumValuesDraft : '',
    addons: addonsBody(longhorn),
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
      nic: effectiveNetwork === 'macvtap' ? r.nic : '',
      endpoint: effectiveNetwork === 'wireguard' && r.host_id !== null ? (endpoints[r.host_id] ?? '').trim() : '',
    })),
    expose,
    expose_api: expose ? ports.api ?? 0 : 0,
    expose_http: expose ? ports.http ?? 0 : 0,
    expose_https: expose ? ports.https ?? 0 : 0,
    image_id: imageOf(rows[0]) || 'ubuntu-24.04',
    ...listenerBody(flavor, endpoint, gateway, allow),
    // Встроенный балансировщик.
    lb_enabled: lbEnabled,
    lb_count: lbEnabled ? lbCount : 0,
    lb_image: '',
    vip: lbEnabled ? lbVIP.trim() : '',
    lb_placements: lbEnabled ? lbRows.map((r) => ({
      host_id: r.host_id,
      role: 'load-balancer' as const,
      kind: r.kind,
      count: r.count,
      vcpus: r.vcpus,
      memory_mb: r.memory_mb,
      disk_gb: r.disk_gb,
      image_id: r.kind === 'vm' ? r.image_id : '',
      network: effectiveNetwork === 'nat' ? r.network : '',
      bridge: effectiveNetwork === 'bridge' ? r.bridge : '',
      nic: effectiveNetwork === 'macvtap' ? r.nic : '',
    })) : [],
    lb_haproxy: lbEnabled ? {
      connect_timeout: lbHaproxy.connect,
      client_timeout: lbHaproxy.client,
      server_timeout: lbHaproxy.server,
      max_conn: lbHaproxy.maxConn,
      stats_port: lbHaproxy.statsPort,
      algorithm: lbHaproxy.algorithm,
    } : {},
  })

  // Чек-лист создания приходит с хаба: имена узлов и порядок шагов знает
  // исполнитель (internal/hub/cluster_steps.go), фронт их не угадывает.
  async function toSteps() {
    setBusy('steps')
    setError(null)
    try {
      const res = await api<{ steps: ClusterStep[] }>('/hub/clusters/steps-preview', { method: 'POST', body: buildBody() })
      setStepsPreview(res.steps)
      setStepsSel(Object.fromEntries(res.steps.map((s) => [s.id, true])))
      setStage('steps')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function start(dry: boolean) {
    setBusy(dry ? 'dry' : 'create')
    setError(null)
    try {
      const res = await api<{ job_id: number }>(dry ? '/hub/clusters/dry-run' : '/hub/clusters', {
        method: 'POST',
        body: dry ? { ...buildBody(), prepare } : { ...buildBody(), steps: stepsChosen(stepsPreview ?? [], stepsSel) },
      })
      if (dry) {
        setDryJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
        return
      }
      onStarted(tx('clusters.started', { name }), res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const hostName = (id: number) => online.find((h) => h.id === id)?.name ?? `#${id}`
  const hostAddr = (id: number) => online.find((h) => h.id === id)?.addr ?? ''

  const chosenMulti = stepsChosen(stepsPreview ?? [], stepsSel)
  const multiConflicts = stepConflicts(stepsPreview ?? [], stepsSel)

  return (
    <Modal title={t('clusters.newMultiTitle')} onClose={onClose} width={1180}>
      {stage === 'steps' && stepsPreview ? (
        <>
          <Banner kind="warn">{t('clusters.experimental')}</Banner>
          <p className="small muted">{t('clusters.steps.about')}</p>
          <ErrorNote error={msg(error)} />
          <StepsChecklist steps={stepsPreview} sel={stepsSel} onToggle={(id, v) => setStepsSel({ ...stepsSel, [id]: v })} />
          <div className="row" style={{ justifyContent: 'flex-end', gap: '0.5rem', alignItems: 'center', marginTop: '0.6rem', flexWrap: 'wrap' }}>
            <span className="small muted">{t('clusters.steps.chosen', { n: chosenMulti.length })}</span>
            <Button onClick={() => setStage('form')} disabled={busy === 'create'}>
              {t('clusters.steps.back')}
            </Button>
            <Button
              type="primary"
              loading={busy === 'create'}
              disabled={chosenMulti.length === 0 || multiConflicts.size > 0}
              onClick={() => void start(false)}
            >
              {t('clusters.create')}
            </Button>
          </div>
        </>
      ) : (
        <>
      <Banner kind="warn">{t('clusters.experimental')}</Banner>
      <p className="small muted">{t('clusters.newMultiBody')}</p>
      <ErrorNote error={msg(error)} />
      <ErrorNote error={hosts.error} />
      <div className="row" style={{ gap: '0.5rem', alignItems: 'flex-end', marginBottom: '0.6rem', flexWrap: 'wrap' }}>
        <label style={{ minWidth: '16rem', margin: 0 }}>
          {t('clusters.presets')}
          <Select
            placeholder={t('clusters.presetPick')}
            value={undefined}
            onChange={(v: number) => applyPreset(v)}
            options={(presets.data?.presets ?? []).map((p) => ({ value: p.id, label: p.name }))}
            notFoundContent={t('clusters.presetsNone')}
          />
        </label>
        <label style={{ minWidth: '12rem', margin: 0 }}>
          {t('clusters.presetName')}
          <Input value={presetName} onChange={(e) => setPresetName(e.target.value)} placeholder={t('clusters.presetNamePlaceholder')} />
        </label>
        <Button loading={presetBusy} disabled={!presetName.trim()} onClick={() => void savePreset()}>
          {t('clusters.presetSave')}
        </Button>
        {(presets.data?.presets ?? []).some((p) => p.name === presetName.trim()) && (
          <Button danger onClick={() => { const p = (presets.data?.presets ?? []).find((x) => x.name === presetName.trim()); if (p) void deletePreset(p.id) }}>
            {t('clusters.presetDelete')}
          </Button>
        )}
        {presetNote && <span className="small muted">{msg(presetNote)}</span>}
      </div>
      <div className="grid grid-4" style={{ marginBottom: '0.6rem' }}>
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
          {t('clusters.network')}
          <Select
            value={network}
            onChange={(v: 'nat' | 'bridge' | 'macvtap' | 'wireguard') => setNetwork(v)}
            options={[
              { value: 'nat', label: t('clusters.netNAT'), disabled: distinctHosts > 1 },
              { value: 'bridge', label: t('clusters.netBridge') },
              { value: 'macvtap', label: t('clusters.netMacvtap') },
              { value: 'wireguard', label: t('clusters.netWireGuard') },
            ]}
          />
          <span className="small muted">
            {network === 'wireguard'
              ? t('clusters.netWireGuardHint')
              : network === 'macvtap'
                ? t('clusters.netMacvtapHint')
                : network === 'bridge'
                  ? t('clusters.netBridgeHint')
                  : t('clusters.netNATHint')}
          </span>
        </label>
      </div>

      {flavor === 'kubeadm' && (
        <ListenerFields
          gateways={gws.data?.gateways ?? []}
          cp3={cpCount === 3}
          endpoint={endpoint}
          setEndpoint={setEndpoint}
          gateway={gateway}
          setGateway={setGateway}
          allow={allow}
          setAllow={setAllow}
        />
      )}

      {/* Встроенный балансировщик */}
      <div style={{ margin: '0.6rem 0', padding: '0.6rem', border: '1px solid var(--border)', borderRadius: 6, background: lbEnabled ? 'var(--bg-2)' : 'transparent' }}>
        <div className="row" style={{ alignItems: 'center', gap: '0.5rem' }}>
          <Checkbox checked={lbEnabled} onChange={(e) => setLbEnabled(e.target.checked)} />
          <strong>{t('clusters.lbTitle')}</strong>
          <span className="small muted">{t('clusters.lbHint')}</span>
        </div>
        {lbEnabled && (
          <div style={{ marginTop: '0.6rem', display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: '0.6rem' }}>
            <label>
              {t('clusters.lbCount')}
              <Select
                value={lbCount}
                onChange={(v: number) => {
                  setLbCount(v)
                  // При переключении на HA добавляем вторую строку размещения, если её нет.
                  if (v === 2 && lbRows.length < 2) {
                    setLbRows([...lbRows, newRow('load-balancer')])
                  } else if (v === 1 && lbRows.length > 1) {
                    setLbRows([lbRows[0]])
                  }
                }}
                options={[
                  { value: 1, label: t('clusters.lbCount1') },
                  { value: 2, label: t('clusters.lbCount2') },
                ]}
              />
            </label>
            <label>
              {t('clusters.lbVIP')}
              <Input value={lbVIP} onChange={(e) => setLbVIP(e.target.value)} placeholder="10.77.1.10" />
              <span className="small muted">{t('clusters.lbVIPHint')}</span>
            </label>
          </div>
        )}
        {lbEnabled && network === 'macvtap' && (
          <p className="small muted" style={{ margin: '0.4rem 0 0', color: 'var(--series-8)' }}>
            ⚠ {t('clusters.lbDHCPHint')}
          </p>
        )}
        {lbEnabled && (
          <div style={{ marginTop: '0.6rem' }}>
            <h4 style={{ margin: '0 0 0.4rem' }}>{t('clusters.lbPlacement', { n: 1 })}</h4>
            {lbRows.map((r, i) => {
              const hi = r.host_id !== null ? info[r.host_id] : undefined
              return (
                <div key={i} style={{ border: '1px solid var(--border)', borderRadius: 6, padding: '0.5rem 0.6rem', marginBottom: '0.4rem' }}>
                  <div className="row" style={{ gap: '0.6rem', alignItems: 'flex-end' }}>
                    <div style={{ ...cell, minWidth: '14rem', flex: 1 }}>
                      <span className="small muted">{t('clusters.colHost')}</span>
                      <Select
                        size="small"
                        value={r.host_id ?? undefined}
                        placeholder={t('clusters.pickHost')}
                        onChange={(v: number) => updateLb(i, { host_id: v, image_id: '', bridge: '', nic: '' })}
                        options={online.map((h) => ({
                          value: h.id,
                          label: (
                            <>
                              <OsIcon os={h.os_info} size={12} />
                              <Sensitive>{h.name + (h.parent_id ? ' (VM)' : '') + (h.addr ? ` — ${h.addr}` : '')}</Sensitive>
                            </>
                          ),
                        }))}
                      />
                    </div>
                    <div style={{ ...cell, minWidth: '11rem' }}>
                      <span className="small muted">{t('clusters.colKind')}</span>
                      <Select size="small" value={r.kind} onChange={(v: Row['kind']) => updateLb(i, { kind: v })} options={[{ value: 'vm', label: t('clusters.kindVM') }]} disabled />
                    </div>
                    <div style={cell}>
                      <span className="small muted">CPU</span>
                      <InputNumber size="small" min={1} max={4} value={r.vcpus} onChange={(v) => updateLb(i, { vcpus: v ?? 1 })} style={{ width: '4.5rem' }} />
                    </div>
                    <div style={cell}>
                      <span className="small muted">{t('clusters.colMem')}</span>
                      <InputNumber size="small" min={512} step={512} value={r.memory_mb} onChange={(v) => updateLb(i, { memory_mb: v ?? 1024 })} style={{ width: '6rem' }} />
                    </div>
                    <div style={cell}>
                      <span className="small muted">{t('clusters.colDisk')}</span>
                      <InputNumber size="small" min={5} value={r.disk_gb} onChange={(v) => updateLb(i, { disk_gb: v ?? 10 })} style={{ width: '5rem' }} />
                    </div>
                    <div style={{ ...cell, minWidth: '16rem', flex: 1 }}>
                      <span className="small muted">{t('hosts.newVMImage')}</span>
                      <Select
                        size="small"
                        value={r.image_id || undefined}
                        loading={hi?.loading}
                        placeholder={r.host_id === null ? t('clusters.pickHostFirst') : t('clusters.pickImage')}
                        disabled={r.host_id === null}
                        onChange={(v: string) => updateLb(i, { image_id: v })}
                        options={[
                          ...(hi?.images ?? []).map((img) => ({ value: img.id, label: img.name + (img.downloaded ? '' : ` ${t('hosts.newVMWillDownload')}`) })),
                          ...(hubImages.data?.images ?? []).map((img) => ({ value: `hub:${img.name}`, label: `${t('clusters.imageFromHub')}: ${img.name} (${formatBytesShort(img.size)})` })),
                        ]}
                      />
                    </div>
                    {effectiveNetwork === 'macvtap' && (
                      <div style={{ ...cell, minWidth: '10rem' }}>
                        <span className="small muted">{t('clusters.colNic')}</span>
                        <Select
                          size="small"
                          value={r.nic || undefined}
                          loading={hi?.loading}
                          placeholder={r.host_id === null ? t('clusters.pickHostFirst') : t('clusters.pickNic')}
                          disabled={r.host_id === null}
                          onChange={(v: string) => updateLb(i, { nic: v })}
                          options={(hi?.nics ?? []).map((n) => ({ value: n.name, label: n.name + (n.addr ? ` — ${n.addr}` : '') }))}
                        />
                      </div>
                    )}
                    {lbCount === 2 && (
                      <Button size="small" type="text" icon={<DeleteOutlined />} disabled={lbRows.length <= 1} onClick={() => setLbRows(lbRows.filter((_, j) => j !== i))} />
                    )}
                  </div>
                </div>
              )
            })}
            {lbCount === 2 && lbRows.length < 2 && (
              <Button size="small" icon={<PlusOutlined />} onClick={() => setLbRows([...lbRows, newRow('load-balancer')])}>
                {t('clusters.addRow')} lb-{lbRows.length + 1}
              </Button>
            )}
          </div>
        )}
        {lbEnabled && (
          <div style={{ marginTop: '0.6rem' }}>
            <Button type="link" size="small" onClick={() => setLbHaproxyOpen(!lbHaproxyOpen)}>
              {lbHaproxyOpen ? '▼' : '▶'} {t('clusters.lbHaproxy')}
            </Button>
            {lbHaproxyOpen && (
              <div style={{ marginTop: '0.4rem', padding: '0.6rem', background: 'var(--bg-2)', borderRadius: 4, display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: '0.6rem' }}>
                <label>
                  {t('clusters.lbHaproxyConnect')}
                  <InputNumber size="small" min={1000} max={60000} step={1000} value={lbHaproxy.connect} onChange={(v) => setLbHaproxy({ ...lbHaproxy, connect: v ?? 5000 })} style={{ width: '100%' }} />
                </label>
                <label>
                  {t('clusters.lbHaproxyClient')}
                  <InputNumber size="small" min={1000} max={300000} step={1000} value={lbHaproxy.client} onChange={(v) => setLbHaproxy({ ...lbHaproxy, client: v ?? 50000 })} style={{ width: '100%' }} />
                </label>
                <label>
                  {t('clusters.lbHaproxyServer')}
                  <InputNumber size="small" min={1000} max={300000} step={1000} value={lbHaproxy.server} onChange={(v) => setLbHaproxy({ ...lbHaproxy, server: v ?? 50000 })} style={{ width: '100%' }} />
                </label>
                <label>
                  {t('clusters.lbHaproxyMaxConn')}
                  <InputNumber size="small" min={100} max={100000} step={100} value={lbHaproxy.maxConn} onChange={(v) => setLbHaproxy({ ...lbHaproxy, maxConn: v ?? 2000 })} style={{ width: '100%' }} />
                </label>
                <label>
                  {t('clusters.lbHaproxyStatsPort')}
                  <InputNumber size="small" min={0} max={65535} value={lbHaproxy.statsPort} onChange={(v) => setLbHaproxy({ ...lbHaproxy, statsPort: v ?? 8404 })} style={{ width: '100%' }} />
                </label>
                <label>
                  {t('clusters.lbHaproxyAlgorithm')}
                  <Select
                    size="small"
                    value={lbHaproxy.algorithm}
                    onChange={(v: 'source' | 'roundrobin') => setLbHaproxy({ ...lbHaproxy, algorithm: v })}
                    options={[
                      { value: 'source', label: t('clusters.lbHaproxyAlgorithmSource') },
                      { value: 'roundrobin', label: t('clusters.lbHaproxyAlgorithmRR') },
                    ]}
                    style={{ width: '100%' }}
                  />
                </label>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Отображение endpoint при включённом LB */}
      {lbEnabled && (
        <div style={{ margin: '0.4rem 0', padding: '0.4rem 0.6rem', background: 'var(--bg-2)', borderRadius: 4, border: '1px solid var(--border)' }}>
          <span className="small muted">{t('clusters.endpoint')}: </span>
          <code>{lbVIP ? `${lbVIP}:6443` : t('clusters.lbEndpointAuto')}</code>
        </div>
      )}

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
                    onChange={(v: number) => update(i, { host_id: v, image_id: '', bridge: '', nic: '' })}
                    options={online.map((h) => ({
                      value: h.id,
                      label: (
                        <>
                          <OsIcon os={h.os_info} size={12} />
                          <Sensitive>{h.name + (h.parent_id ? ' (VM)' : '') + (h.addr ? ` — ${h.addr}` : '')}</Sensitive>
                        </>
                      ),
                    }))}
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
                      options={[
                        ...(hi?.images ?? []).map((img) => ({ value: img.id, label: img.name + (img.downloaded ? '' : ` ${t('hosts.newVMWillDownload')}`) })),
                        ...(hubImages.data?.images ?? []).map((img) => ({ value: `hub:${img.name}`, label: `${t('clusters.imageFromHub')}: ${img.name} (${formatBytesShort(img.size)})` })),
                      ]}
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
                  {effectiveNetwork === 'macvtap' && (
                    <div style={{ ...cell, minWidth: '13rem' }}>
                      <span className="small muted">{t('clusters.colNic')}</span>
                      {hi && !hi.loading && hi.nics.length === 0 ? (
                        // Списка нет — имя вписывают руками: хост проверит его
                        // при создании машины и откажет по имени устройства.
                        <Input
                          size="small"
                          value={r.nic}
                          disabled={r.host_id === null}
                          placeholder={t('clusters.nicPlaceholder')}
                          onChange={(e) => update(i, { nic: e.target.value.trim() })}
                        />
                      ) : (
                        <Select
                          size="small"
                          showSearch
                          value={r.nic || undefined}
                          loading={hi?.loading}
                          placeholder={r.host_id === null ? t('clusters.pickHostFirst') : t('clusters.pickNic')}
                          disabled={r.host_id === null}
                          onChange={(v: string) => update(i, { nic: v })}
                          options={(hi?.nics ?? []).map((n) => ({ value: n.name, label: nicLabel(n.name, n.addr) }))}
                        />
                      )}
                      {hi && !hi.loading && hi.nicsUnknown && (
                        <span className="small muted">{t('clusters.nicUnknown')}</span>
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
                <span className="small">
                  <OsIconOf kind="host" hostID={id} />
                  <Sensitive>{hostName(id)}</Sensitive>
                </span>
                <Input size="small" className="sensitive" value={endpoints[id] ?? ''} placeholder={hostAddr(id)} onChange={(e) => setEndpoints({ ...endpoints, [id]: e.target.value })} />
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
        {cilium && (
          <Button size="small" style={{ marginLeft: '1.6rem' }} onClick={() => {
            setCiliumValuesDraft(CILIUM_DEFAULT_VALUES)
            setCiliumValuesError(null)
            setCiliumValuesOpen(true)
          }}>
            {t('clusters.ciliumValues')}
          </Button>
        )}
        <LonghornField value={longhorn} onChange={setLonghorn} k8s={k8sVersion || versions.data?.stable || ''} />
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
        <Button type="primary" loading={busy === 'steps'} disabled={!valid || busy === 'dry' || busy === 'create'} onClick={() => void toSteps()}>
          {t('clusters.steps.next')}
        </Button>
      </div>
      {dryJob && <JobLogModal job={dryJob} scope="/hosts/local" onClose={() => setDryJob(null)} />}
      {ciliumValuesOpen && (
        <EditTextModal
          title={t('clusters.ciliumValuesTitle')}
          saved=""
          draft={ciliumValuesDraft}
          onDraft={(v) => {
            setCiliumValuesDraft(v)
            // Валидация YAML.
            try {
              yaml.load(v)
              setCiliumValuesError(null)
            } catch (e) {
              setCiliumValuesError(e instanceof Error ? e.message : String(e))
            }
          }}
          onSave={async () => {
            setCiliumValuesOpen(false)
            return true
          }}
          onClose={() => setCiliumValuesOpen(false)}
          rows={20}
          below={ciliumValuesError ? <Banner kind="error">{t('yamlBlocks.parseError', { error: ciliumValuesError })}</Banner> : <span className="small muted">{t('clusters.ciliumValuesHint')}</span>}
          saveBlocked={ciliumValuesError ? t('yamlBlocks.parseErrorShort') : undefined}
        />
      )}
        </>
      )}
    </Modal>
  )
}
