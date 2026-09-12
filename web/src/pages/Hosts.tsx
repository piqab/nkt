import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { AutoComplete, Badge, Button, Checkbox, Form, Input, InputNumber, Select, Switch, Tabs, Tag, Tooltip, type TableColumnsType } from 'antd'
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  InfoCircleFilled,
  MinusCircleOutlined,
  QuestionCircleOutlined,
  SyncOutlined,
  WarningFilled,
} from '@ant-design/icons'
import { Trans, useTranslation } from 'react-i18next'
import { api, ApiError, LOCAL_HOST_ID, useApi } from '../api'
import type { HostEvent, HubHost, Job, RenewEvent, RenewJobStatus, Severity } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, SEVERITIES, formatRelative, severityLabel } from '../components/ui'
import { notificationsEnabled, notifyNewEvents, requestNotificationPermission, setNotificationsEnabled } from '../notifications'
import { decryptWithPassword, encryptWithPassword, isPasswordEncrypted } from '../exportCrypto'
import i18n from '../i18n'
import { confirmAction } from '../components/confirm'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { JobLogModal } from './Jobs'

/** How often to poll a running install job for new progress lines — same
 * cadence Certificates.tsx uses for certbot jobs. */
const INSTALL_POLL_MS = 800

/** Parses a leading MAJOR.MINOR.PATCH off a version string, ignoring any
 * suffix (so "1.2.3-dirty" still parses as [1, 2, 3]). Returns null for
 * anything that doesn't start with that shape — a "dev" build, or a bare
 * git hash from before the VERSION file existed. */
function parseSemver(v: string): [number, number, number] | null {
  const m = /^(\d+)\.(\d+)\.(\d+)/.exec(v)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3])]
}

/** True when `current` is an older release than `latest`. Falls back to a
 * plain "not equal" when either side doesn't parse as semver — the best
 * that can be said about an opaque string like "dev" — rather than
 * guessing which of two incomparable strings is "newer". Never reports a
 * host as outdated when it is actually newer than the hub (e.g. the hub
 * itself hasn't been rebuilt yet) — "переустановить" would only downgrade
 * it in that case, not update it. */
function isOlderVersion(current: string, latest: string): boolean {
  const a = parseSemver(current)
  const b = parseSemver(latest)
  if (!a || !b) return current !== latest
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] < b[i]
  }
  return false
}

function isOutdated(h: HubHost, hubVersion?: string): boolean {
  return h.status !== 'new' && !!hubVersion && !!h.nkt_version && isOlderVersion(h.nkt_version, hubVersion)
}

const STATUS_LABEL_KEY: Record<HubHost['status'], string> = {
  new: 'hosts.statusNew',
  installing: 'hosts.statusInstalling',
  online: 'hosts.statusOnline',
  error: 'hosts.statusError',
}

const STATUS_COLOR: Record<HubHost['status'], string> = {
  new: 'var(--text-muted)',
  installing: 'var(--text-muted)',
  online: 'var(--status-good)',
  error: 'var(--status-critical)',
}

const SUDO_LABEL_KEY: Record<NonNullable<HubHost['sudo_status']>, string> = {
  '': 'hosts.sudoUnknown',
  root: 'hosts.sudoRoot',
  nopasswd: 'hosts.sudoNopasswd',
  password_required: 'hosts.sudoPasswordRequired',
}

const SUDO_COLOR: Record<NonNullable<HubHost['sudo_status']>, string> = {
  '': 'var(--text-muted)',
  root: 'var(--text-muted)',
  nopasswd: 'var(--status-good)',
  password_required: 'var(--status-critical)',
}

/** What the last install/update on this host actually observed about sudo
 * — set as a side effect (Manager.recordSudoOutcome), never probed on its
 * own, so this can be stale until the next install/update touches the host. */
function SudoBadge({ status }: { status: HubHost['sudo_status'] }) {
  const { t } = useTranslation()
  const s = status ?? ''
  // Галочка или крестик, слово — в подсказке: колонка отвечает на один
  // вопрос «есть ли sudo без пароля», и двух знаков ей достаточно.
  // Всё, что не NOPASSWD, — красный крестик: колонка отвечает на один
  // вопрос, и «ещё не проверялось» для него — тоже «нет».
  const icon =
    s === 'nopasswd' ? (
      <CheckCircleFilled style={{ color: SUDO_COLOR[s] }} />
    ) : (
      <CloseCircleFilled style={{ color: 'var(--status-critical)' }} />
    )
  return (
    <Tooltip title={t(SUDO_LABEL_KEY[s])}>
      <span aria-label={t(SUDO_LABEL_KEY[s])}>{icon}</span>
    </Tooltip>
  )
}

/** Состояние хоста одной иконкой; слово и текст ошибки — в подсказке. */
function HostStatusIcon({ host }: { host: HubHost }) {
  const { t } = useTranslation()
  const label = t(STATUS_LABEL_KEY[host.status]) + (host.status === 'error' && host.error_msg ? `: ${host.error_msg}` : '')
  const icon =
    host.status === 'online' ? (
      <CheckCircleFilled style={{ color: STATUS_COLOR.online }} />
    ) : host.status === 'installing' ? (
      <SyncOutlined spin style={{ color: 'var(--seq-300)' }} />
    ) : host.status === 'error' ? (
      <CloseCircleFilled style={{ color: STATUS_COLOR.error }} />
    ) : (
      <MinusCircleOutlined style={{ color: STATUS_COLOR.new }} />
    )
  return (
    <Tooltip title={label}>
      <span aria-label={label}>{icon}</span>
    </Tooltip>
  )
}

function HostStatusBadge({ status }: { status: HubHost['status'] }) {
  const { t } = useTranslation()
  return <Badge color={STATUS_COLOR[status]} text={t(STATUS_LABEL_KEY[status])} />
}

/**
 * Reverse-tunnel fallback channel state for one host (see internal/tunnel,
 * internal/hub's Manager.recordChannel/TunnelConnected). Three states, most
 * urgent first: `channel === 'tunnel'` means SSH is *right now* unreachable
 * and traffic is actually flowing over the fallback — worth a loud color,
 * since it is standing in for a broken primary path, not just a healthy
 * standby. `tunnel_connected` alone (channel still "ssh", or not dialed
 * yet) means the standby connection is up and ready but not needed. Neither
 * set, with the feature enabled, means the hub hasn't connected to the
 * host's tunnel listener yet (freshly installed, or the host itself
 * offline/unreachable on that port).
 */
function TunnelChannelBadge({ host }: { host: HubHost }) {
  const { t } = useTranslation()
  if (!host.tunnel_enabled) return <span className="small muted">—</span>
  // Галочка или крестик, слова — в подсказке. Канал, по которому прямо
  // сейчас идёт трафик вместо сломанного SSH, — тоже галочка, но
  // тревожного цвета: он работает, но подменяет собой основной путь.
  if (host.channel === 'tunnel') {
    return (
      <Tooltip title={`${t('hosts.tunnelActive')} — ${t('hosts.tunnelActiveTooltip')}`}>
        <span aria-label={t('hosts.tunnelActive')}>
          <CheckCircleFilled style={{ color: 'var(--status-warning)' }} />
        </span>
      </Tooltip>
    )
  }
  if (host.tunnel_connected) {
    return (
      <Tooltip title={`${t('hosts.tunnelConnected')} — ${t('hosts.tunnelConnectedTooltip')}`}>
        <span aria-label={t('hosts.tunnelConnected')}>
          <CheckCircleFilled style={{ color: 'var(--status-good)' }} />
        </span>
      </Tooltip>
    )
  }
  return (
    <Tooltip title={`${t('hosts.tunnelDisconnected')} — ${t('hosts.tunnelDisconnectedTooltip')}`}>
      <span aria-label={t('hosts.tunnelDisconnected')}>
        <CloseCircleFilled style={{ color: 'var(--text-muted)' }} />
      </span>
    </Tooltip>
  )
}

const SEVERITY_ICON: Record<Severity, ReactNode> = {
  critical: <CloseCircleFilled style={{ color: 'var(--status-critical)' }} />,
  high: <ExclamationCircleFilled style={{ color: 'var(--status-serious)' }} />,
  medium: <WarningFilled style={{ color: 'var(--status-warning)' }} />,
  low: <InfoCircleFilled style={{ color: 'var(--seq-300)' }} />,
  info: <InfoCircleFilled style={{ color: 'var(--text-muted)' }} />,
}

/**
 * The "Проблемы" column: icons + counts sourced from the hub's own
 * background poll (see internal/hub's pollOverviews), not fetched on open —
 * `host.reachable === undefined` is the tri-state "never polled" signal
 * (see types.ts's own doc comment on HubHost), distinct from a real
 * zero-findings reading.
 */
function ProblemsCell({ host }: { host: HubHost }) {
  const { t } = useTranslation()
  if (host.reachable === undefined) {
    return (
      <span className="small muted row" style={{ gap: '0.3rem', flexWrap: 'nowrap' }}>
        <QuestionCircleOutlined /> {t('hosts.problemsUnknown')}
      </span>
    )
  }
  const findings = host.findings ?? {}
  const present = SEVERITIES.filter((s) => (findings[s] ?? 0) > 0)
  return (
    <div className="col" style={{ gap: '0.25rem' }}>
      <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
        {present.length === 0 ? (
          <span className="row small" style={{ gap: '0.3rem', color: 'var(--status-good)', flexWrap: 'nowrap' }}>
            <CheckCircleFilled /> {t('hosts.noProblems')}
          </span>
        ) : (
          present.map((s) => (
            <Tooltip key={s} title={severityLabel(s)}>
              <span className="row small" style={{ gap: '0.25rem', flexWrap: 'nowrap' }}>
                {SEVERITY_ICON[s]} {findings[s]}
              </span>
            </Tooltip>
          ))
        )}
      </div>
      {host.reachable === false && (
        <span className="small" style={{ color: 'var(--status-critical)' }}>
          {t('hosts.unreachable', {
            stale: host.last_polled_at ? t('hosts.unreachableStale', { time: formatRelative(host.last_polled_at) }) : '',
          })}
        </span>
      )}
    </div>
  )
}

/**
 * Host registry and per-host dashboard entry point for a hub. Selecting an
 * online host hands its id up to the shell (App.tsx), which scopes every
 * other page's API calls to it — those pages are otherwise unmodified.
 */
export default function Hosts({
  onSelect,
  hubVersion,
}: {
  onSelect: (host: { id: number; name: string }) => void
  hubVersion?: string
}) {
  const { t } = useTranslation()
  const { data: hosts, error, loading, reload } = useApi<HubHost[]>('/hub/hosts', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [installHostId, setInstallHostId] = useState<number | null>(null)
  const [job, setJob] = useState<string | null>(null)
  const [jobStatus, setJobStatus] = useState<RenewJobStatus | null>(null)
  const [editingHost, setEditingHost] = useState<HubHost | null>(null)
  const [creatingHost, setCreatingHost] = useState(false)
  const [pubKeyInfo, setPubKeyInfo] = useState<{ hostName: string; key: string } | null>(null)
  const [notifyOn, setNotifyOn] = useState(() => notificationsEnabled())
  const [busyServiceIds, setBusyServiceIds] = useState<Set<number>>(new Set())
  const [bulkBusy, setBulkBusy] = useState<'stop' | 'start' | null>(null)
  // Drives "Обновить всё": the hosts still waiting their turn (shrinks by
  // one every time the shared job/jobStatus state above clears — see the
  // two effects below), and the outcome of each host already processed,
  // for the combined summary notice once the queue empties. null means no
  // batch update is running; an empty array (not null) means the last host
  // is still being recorded/closed, distinct from "never started" — see
  // the completion effect's own guard.
  const [updateAllQueue, setUpdateAllQueue] = useState<HubHost[] | null>(null)
  const [updateAllResults, setUpdateAllResults] = useState<{ name: string; ok: boolean }[]>([])
  const [updateAllTotal, setUpdateAllTotal] = useState(0)
  const [importing, setImporting] = useState(false)
  const importInputRef = useRef<HTMLInputElement>(null)
  // Set when "экспорт с ключом" is clicked — opens ExportPasswordModal
  // instead of downloading immediately, since the file about to be
  // generated carries the hub's own master key plus every host's secret.
  const [exportPrompt, setExportPrompt] = useState(false)
  const [exportBusy, setExportBusy] = useState(false)
  // A file the operator just picked for "импорт" that turned out to be
  // password-encrypted (see exportCrypto.ts) — decryptImportFile below
  // needs the password before store.DecodeHubExport has anything to parse.
  const [pendingImportFile, setPendingImportFile] = useState<File | null>(null)
  // Set by openHost when "открыть" is clicked on a host whose nkt_version
  // trails the hub's own — the update this same click kicked off has to
  // actually finish before there's anything current to look at. Cleared
  // once the matching job settles, whether or not that navigation happens.
  const [autoOpenHost, setAutoOpenHost] = useState<{ id: number; name: string } | null>(null)

  // Уведомления берутся из журнала оповещений хаба: переходы находит он
  // сам в фоновом опросе, а вкладка только показывает то, чего оператор
  // ещё не видел. Так во всплывающем есть и адрес, и подробности, а
  // закрытая вкладка не значит «событие потеряно».
  const events = useApi<{ events: HostEvent[]; notify?: Record<string, boolean> }>('/hub/events?limit=50', 30_000)
  useEffect(() => {
    if (events.data?.events) notifyNewEvents(events.data.events, events.data.notify)
  }, [events.data])

  async function toggleNotify(checked: boolean) {
    if (checked) {
      const granted = await requestNotificationPermission()
      if (!granted) {
        setNotice({ kind: 'error', text: t('hosts.notificationDenied') })
        return
      }
    }
    setNotificationsEnabled(checked)
    setNotifyOn(checked)
  }

  useEffect(() => {
    if (!job) return
    let cancelled = false
    let timer: number | undefined

    async function poll() {
      try {
        const status = await api<RenewJobStatus>(`/hub/hosts/${installHostId}/install/${job}`)
        if (cancelled) return
        setJobStatus(status)
        if (status.done) {
          window.clearInterval(timer)
          reload()
          finishAutoOpen(status.error)
        }
      } catch (err) {
        if (cancelled) return
        setJobStatus({ events: [], done: true, error: err instanceof Error ? err.message : String(err) })
        window.clearInterval(timer)
        finishAutoOpen(t('hosts.installStatusFailed'))
      }
    }

    // Navigates into the host that "открыть" auto-triggered this very job
    // for (see openHost) once it settles — but only on success: opening an
    // outdated host anyway, silently, right after its update just failed,
    // would hide the failure behind a dashboard that still isn't current.
    function finishAutoOpen(jobError: string | undefined) {
      setAutoOpenHost((pending) => {
        if (pending && pending.id === installHostId) {
          if (!jobError) {
            closeJobModal()
            onSelect(pending)
          }
          return null
        }
        return pending
      })
    }

    void poll()
    timer = window.setInterval(poll, INSTALL_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [job, installHostId, reload])

  /** force is set on retry, after the operator confirms the 409 prompt
   * below — a first call is always unforced, so an existing "foreign"
   * install (one this hub has no record of putting there itself) always
   * gets a chance to be seen before anything on the host is touched.
   * Returns whether a job actually started — openHost needs that to know
   * whether autoOpenHost has anything left to wait for. */
  // Ключ — id хоста: форма добавления уже знает его из ответа, а установка
  // запускается отдельным вызовом чуть позже.
  const pendingBootstrap = useRef(new Map<number, BootstrapOptions>())
  const [removingHost, setRemovingHost] = useState<HubHost | null>(null)
  // Группы приходят с сервера, а не выводятся из хостов: пустую группу
  // иначе неоткуда взять, а её и создают первой — чтобы потом перетащить
  // в неё хосты.
  const groups = useApi<{ groups: string[] }>('/hub/groups', 60_000)
  // Профили хаба — те же, что правятся в разделе «Профили» его
  // собственной машины: раскатывать по группе можно любой из них.
  const profiles = useApi<{ profiles: { id: number; name: string }[] }>('/hosts/local/profiles', 120_000)
  const [applyTo, setApplyTo] = useState<{ group: string; hosts: number } | null>(null)
  const [provisionOn, setProvisionOn] = useState<HubHost | null>(null)
  // Раскрытые списки машин — по идентификатору хоста. По умолчанию
  // свёрнуто: у хоста с десятком машин список иначе оттеснил бы сами
  // хосты.
  const [openVMs, setOpenVMs] = useState<Set<number>>(new Set())
  const [detectingAddr, setDetectingAddr] = useState<number | null>(null)
  // Журнал задания хаба (создание машины, раскатка профиля) — открывается
  // прямо здесь: в списке хостов раздела «Задания» под рукой нет, и
  // отсылать к нему значило бы отправлять оператора искать несуществующую
  // кнопку.
  const [hubJob, setHubJob] = useState<Job | null>(null)

  async function openHubJob(jobID: number) {
    try {
      setHubJob(await api<Job>(`/hosts/local/jobs/${jobID}`))
    } catch {
      // Журнал не открылся — не повод считать задание неудачным: оно
      // идёт своим ходом, и его всегда видно в разделе «Задания».
    }
  }

  // Адрес машина получает не сразу: сначала грузится, потом ждёт DHCP.
  // Кнопка спрашивает его у хоста, на котором машина работает.
  async function detectAddress(h: HubHost) {
    setDetectingAddr(h.id)
    setNotice(null)
    try {
      const res = await api<{
        address: string
        found: boolean
        state?: string
        reason?: string
        detail?: string
      }>(`/hub/hosts/${h.id}/detect-address`, { method: 'POST' })
      if (res.found) {
        setNotice({ kind: 'info', text: t('hosts.detectAddressFound', { name: h.name, addr: res.address }) })
        reload()
        return
      }
      // «Не знаю» без причины — тупик: оператору некуда идти дальше.
      // Причина приходит кодом, а сырой ответ virsh идёт следом.
      const reason = t(`hosts.detectAddressReason.${res.reason ?? 'no-lease'}`, {
        defaultValue: t('hosts.detectAddressReason.no-lease'),
        state: res.state ?? '—',
      })
      setNotice({
        kind: 'info',
        text: t('hosts.detectAddressNone', { name: h.name, reason }) + (res.detail ? ` (${res.detail})` : ''),
      })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setDetectingAddr(null)
    }
  }

  // Задания самого хаба: создание машины идёт заданием, а не установкой,
  // и по статусу записи этого не видно. Список тот же, что в разделе
  // «Задания» (он живёт на встроенной машине хаба).
  const hubJobs = useApi<{ jobs: Job[] }>('/hosts/local/jobs?limit=50', 10_000)

  // Машины по хосту, на котором они созданы.
  const vmsByHost = useMemo(() => {
    const out = new Map<number, HubHost[]>()
    for (const h of hosts ?? []) {
      if (!h.parent_id) continue
      out.set(h.parent_id, [...(out.get(h.parent_id) ?? []), h])
    }
    return out
  }, [hosts])

  /**
   * Машины, по которым прямо сейчас идёт задание хаба.
   *
   * Пока машину создают или доводят до готовности, её кнопки нажимать
   * нечего: установка уже идёт заданием, адрес ещё определяется, а
   * удаление посреди создания оставит на хосте половину машины. Статуса
   * записи для этого мало — он меняется только на шаге установки.
   */
  const busyVMs = useMemo(() => {
    const out = new Set<number>()
    for (const j of hubJobs.data?.jobs ?? []) {
      if (j.status !== 'running' && j.status !== 'queued') continue
      const queued = /^vm:(\d+)$/.exec(j.queue ?? '')
      if (!queued) continue
      let name = ''
      try {
        name = (JSON.parse(j.params || '{}') as { spec?: { name?: string } }).spec?.name ?? ''
      } catch {
        // Параметры задания — не то, ради чего стоит ронять список.
      }
      for (const vm of vmsByHost.get(Number(queued[1])) ?? []) {
        // Без имени в параметрах отметить можно только все машины хоста —
        // это вернее, чем не отметить ту, которую действительно строят.
        if (!name || vm.name === name) out.add(vm.id)
      }
    }
    return out
  }, [hubJobs.data, vmsByHost])

  function toggleVMs(hostID: number) {
    setOpenVMs((prev) => {
      const next = new Set(prev)
      if (next.has(hostID)) next.delete(hostID)
      else next.add(hostID)
      return next
    })
  }
  const [groupDialog, setGroupDialog] = useState<{ mode: 'create' | 'rename'; from?: string } | null>(null)
  const [groupName, setGroupName] = useState('')

  // Хосты по разделам. Порядок групп — алфавитный, «Без группы» всегда
  // последней: это не группа, а её отсутствие, и держать её среди
  // названных значило бы прятать хосты, до которых руки не дошли.
  const groupedHosts = useMemo<{ group: string; items: HubHost[] }[]>(() => {
    const byGroup = new Map<string, HubHost[]>()
    // Сначала пустые заготовки для всех известных групп — иначе только что
    // созданная группа не показалась бы вовсе, и перетаскивать было бы
    // некуда.
    for (const name of groups.data?.groups ?? []) byGroup.set(name, [])
    for (const host of hosts ?? []) {
      // Машины в разделах не показываются: они живут внутри своего
      // хоста и раскрываются по «+».
      if (host.parent_id) continue
      const key = (host.group ?? '').trim()
      byGroup.set(key, [...(byGroup.get(key) ?? []), host])
    }
    const named = [...byGroup.keys()].filter((g) => g !== '').sort((a, b) => a.localeCompare(b))
    // «Без группы» показывается, только если такие хосты есть: пустой
    // раздел без названия объяснить нечем.
    const order = byGroup.has('') ? [...named, ''] : named
    return order.map((group) => ({ group, items: byGroup.get(group) ?? [] }))
  }, [hosts, groups.data])

  // Названия существующих групп — для автодополнения в форме хоста.
  const knownGroups = useMemo(
    () =>
      [
        ...new Set([
          ...(groups.data?.groups ?? []),
          ...(hosts ?? []).map((h) => (h.group ?? '').trim()).filter(Boolean),
        ]),
      ].sort(),
    [hosts, groups.data],
  )

  // Действия над самой группой. Создание и переименование идут через одно
  // окно: разница только в том, есть ли исходное название.
  async function submitGroupDialog() {
    if (!groupDialog) return
    const name = groupName.trim()
    if (!name) return
    try {
      if (groupDialog.mode === 'create') {
        await api('/hub/groups', { method: 'POST', body: { name } })
      } else {
        await api('/hub/groups/rename', { method: 'POST', body: { name: groupDialog.from, to: name } })
      }
      setGroupDialog(null)
      setGroupName('')
      groups.reload()
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  async function deleteGroup(name: string, hostCount: number) {
    if (!(await confirmAction(t('hosts.confirmDeleteGroup', { name, count: hostCount })))) return
    try {
      await api('/hub/groups/delete', { method: 'POST', body: { name } })
      groups.reload()
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }
  // Свёрнутые группы и перетаскиваемая строка. Свёрнутость — состояние
  // человека, а не данных: запоминается между заходами.
  const [collapsedGroups, setCollapsedGroups] = useState<string[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(HOST_GROUPS_COLLAPSED_KEY) ?? '[]') as string[]
    } catch {
      return []
    }
  })
  const [draggingHost, setDraggingHost] = useState<number | null>(null)
  const [dropGroup, setDropGroup] = useState<string | null>(null)

  function toggleGroup(group: string) {
    setCollapsedGroups((prev) => {
      const next = prev.includes(group) ? prev.filter((g) => g !== group) : [...prev, group]
      try {
        localStorage.setItem(HOST_GROUPS_COLLAPSED_KEY, JSON.stringify(next))
      } catch {
        // Приватный режим — состояние просто не запомнится.
      }
      return next
    })
  }

  // Перенос хоста в другую группу. Отдельный запрос, а не общая правка
  // хоста: перетаскивание меняет ровно группу, и пересылать вместе с ней
  // адрес и пользователя значило бы однажды перезаписать их устаревшими
  // значениями.
  async function moveToGroup(hostID: number, group: string) {
    try {
      await api(`/hub/hosts/${hostID}/group`, { method: 'POST', body: { group } })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  // Принимает не весь хост, а только его идентификатор: сразу после
  // добавления полной записи ещё нет, а установке кроме id ничего и не
  // нужно.
  async function startInstall(host: Pick<HubHost, 'id'>, force = false): Promise<boolean> {
    setNotice(null)
    try {
      // Подготовка относится к одной конкретной установке — к той, что
      // идёт сразу после добавления хоста. Повторная установка и
      // обновление её не повторяют: хост уже подготовлен.
      const bootstrap = pendingBootstrap.current.get(host.id)
      const res = await api<{ job: string }>(
        `/hub/hosts/${host.id}/install${force ? '?force=true' : ''}`,
        { method: 'POST', body: bootstrap ?? {} },
      )
      pendingBootstrap.current.delete(host.id)
      setInstallHostId(host.id)
      setJobStatus(null)
      setJob(res.job)
      return true
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        err.payload &&
        typeof err.payload === 'object' &&
        (err.payload as { foreign_install?: boolean }).foreign_install
      ) {
        const detail = (err.payload as { detail?: string }).detail ?? ''
        if (await confirmAction(t('hosts.confirmForeignInstall', { detail }))) {
          return startInstall(host, true)
        }
        return false
      }
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return false
    }
  }

  /** Kicks off "Обновить всё": every non-local host that isOutdated says is
   * behind the hub's own build. Deliberately sequential (see the two
   * effects below), not Promise.all like bulkSetServiceRunning — updating
   * several hosts' running nkt binary at once is riskier to watch/debug
   * than starting/stopping a service, so this walks the queue one host's
   * install-log modal at a time instead. */
  async function updateAllOutdated() {
    const targets = (hosts ?? []).filter((h) => h.id !== LOCAL_HOST_ID && isOutdated(h, hubVersion))
    if (targets.length === 0) return
    if (!(await confirmAction(t('hosts.confirmUpdateAll', { count: targets.length })))) return
    setNotice(null)
    setUpdateAllResults([])
    setUpdateAllTotal(targets.length)
    setUpdateAllQueue(targets)
  }

  // Advances the queue: fires whenever it changes, or whenever `job`
  // clears (the completion effect below calls closeJobModal() once a
  // host's install finishes, which is what actually unblocks this). Popping
  // the next host *before* startInstall resolves means a host whose
  // startInstall call itself fails synchronously (network error, or the
  // foreign-install confirm being declined) still advances the queue —
  // recorded as a failure in the .then() below, not left stuck forever.
  useEffect(() => {
    if (updateAllQueue === null || job) return
    if (updateAllQueue.length === 0) {
      const failed = updateAllResults.filter((r) => !r.ok).length
      setNotice({
        kind: failed > 0 ? 'error' : 'info',
        text: t('hosts.bulkUpdateFinished', { total: updateAllResults.length, failed }),
      })
      setUpdateAllQueue(null)
      setUpdateAllTotal(0)
      return
    }
    const [next, ...rest] = updateAllQueue
    setUpdateAllQueue(rest)
    void startInstall(next).then((started) => {
      if (!started) setUpdateAllResults((prev) => [...prev, { name: next.name, ok: false }])
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [updateAllQueue, job])

  // Records the just-finished host's outcome and closes its modal — which
  // clears `job`, letting the effect above pick the next one. Only active
  // while a batch is actually running (updateAllQueue !== null, including
  // the empty-array "last host still settling" state — see its own
  // declaration comment), so a manual single-host update never trips this.
  useEffect(() => {
    if (updateAllQueue === null || !jobStatus?.done) return
    const finishedHost = hosts?.find((h) => h.id === installHostId)
    setUpdateAllResults((prev) => [...prev, { name: finishedHost?.name ?? '', ok: !jobStatus.error }])
    closeJobModal()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobStatus])

  /** "открыть" on a host whose nkt_version trails the hub's own: the
   * dashboard it would open into is for a build that's already known to be
   * behind, so update it first and open once that actually lands (see the
   * install-poll effect's finishAutoOpen) instead of showing a stale
   * version and making the operator notice and fix it by hand. */
  async function openHost(host: HubHost) {
    if (!isOutdated(host, hubVersion)) {
      onSelect({ id: host.id, name: host.name })
      return
    }
    setAutoOpenHost({ id: host.id, name: host.name })
    if (!(await startInstall(host))) {
      setAutoOpenHost(null)
    }
  }

  async function cancelInstall(host: HubHost) {
    setNotice(null)
    try {
      await api(`/hub/hosts/${host.id}/install/cancel`, { method: 'POST' })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  async function showPubKey(host: HubHost) {
    setNotice(null)
    try {
      const res = await api<{ authorized_key: string }>(`/hub/hosts/${host.id}/pubkey`)
      setPubKeyInfo({ hostName: host.name, key: res.authorized_key })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  async function removeSudoAccess(host: HubHost) {
    if (!(await confirmAction(t('hosts.confirmRemoveSudo', { user: host.ssh_user, name: host.name })))) return
    setNotice(null)
    try {
      await api(`/hub/hosts/${host.id}/sudo/remove`, { method: 'POST' })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  /** Shared by the per-host buttons and "остановить все"/"запустить все" —
   * confirm is skipped for the bulk path (one confirm covers the whole
   * batch, see stopAll/startAll below), and errors are collected by the
   * caller instead of shown immediately, so one failing host doesn't hide
   * what happened to the rest. */
  async function setServiceRunning(host: HubHost, running: boolean): Promise<string | null> {
    setBusyServiceIds((s) => new Set(s).add(host.id))
    try {
      await api(`/hub/hosts/${host.id}/${running ? 'start' : 'stop'}`, { method: 'POST' })
      return null
    } catch (err) {
      return err instanceof Error ? err.message : String(err)
    } finally {
      setBusyServiceIds((s) => {
        const next = new Set(s)
        next.delete(host.id)
        return next
      })
    }
  }

  async function stopHost(host: HubHost) {
    if (!(await confirmAction(t('hosts.confirmStopHost', { name: host.name })))) return
    setNotice(null)
    const err = await setServiceRunning(host, false)
    if (err) setNotice({ kind: 'error', text: err })
    else reload()
  }

  async function startHost(host: HubHost) {
    setNotice(null)
    const err = await setServiceRunning(host, true)
    if (err) setNotice({ kind: 'error', text: err })
    else reload()
  }

  // Сама машина — через хост, на котором она создана: старт службы nkt по
  // SSH внутрь выключенной машины упирался бы в отсутствующий адрес.
  const [vmActing, setVmActing] = useState<number | null>(null)
  async function vmDomainAction(host: HubHost, action: 'start' | 'shutdown' | 'destroy') {
    if (action !== 'start' && !(await confirmAction(t(`hosts.confirmVM.${action}`, { name: host.name })))) return
    setVmActing(host.id)
    setNotice(null)
    try {
      await api(`/hub/hosts/${host.id}/vm/${action}`, { method: 'POST' })
      if (action === 'start') setNotice({ kind: 'info', text: t('hosts.vmStarted', { name: host.name }) })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setVmActing(null)
    }
  }

  /** Runs setServiceRunning across every installed host in parallel and
   * reports one combined summary — a host mid-install ('new'/'installing')
   * has nothing to stop/start yet and is silently skipped rather than
   * counted as a failure. */
  async function bulkSetServiceRunning(running: boolean) {
    const targets = (hosts ?? []).filter((h) => h.status !== 'new' && h.status !== 'installing')
    if (targets.length === 0) return
    if (!(await confirmAction(t(running ? 'hosts.confirmBulkStart' : 'hosts.confirmBulkStop', { count: targets.length })))) {
      return
    }
    setNotice(null)
    setBulkBusy(running ? 'start' : 'stop')
    try {
      const results = await Promise.all(targets.map((h) => setServiceRunning(h, running)))
      const failed = results.filter((e): e is string => e !== null).length
      if (failed > 0) {
        setNotice({ kind: 'error', text: t('hosts.bulkFailed', { failed, total: targets.length }) })
      }
      reload()
    } finally {
      setBulkBusy(null)
    }
  }

  /** "экспорт" (no key) downloads straight away — nothing in that file
   * decrypts without the hub's own master key anyway. "экспорт с ключом"
   * opens ExportPasswordModal instead: that file IS enough on its own to
   * decrypt every host's secrets, so it always gets a chance to be
   * password-protected before it touches disk. */
  function exportHosts(includeKey: boolean) {
    if (!includeKey) {
      void downloadExport(false)
      return
    }
    setExportPrompt(true)
  }

  /** GET /hub/export returns a file, not JSON-for-the-UI — bypasses the
   * api() helper (which always parses the body as JSON). password, when
   * given, encrypts the downloaded bytes in-browser (see exportCrypto.ts)
   * before the save-as dialog ever sees them — the plaintext export never
   * touches disk itself. */
  async function downloadExport(includeKey: boolean, password?: string) {
    setNotice(null)
    setExportBusy(true)
    try {
      const res = await fetch(`/api/hub/export${includeKey ? '?include_key=1' : ''}`, { credentials: 'same-origin' })
      if (!res.ok) {
        const payload = await res.json().catch(() => null)
        throw new Error(payload?.error ?? t('common.httpError', { status: res.status }))
      }
      let blob = await res.blob()
      const filename = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'nkt-hub-export.json'
      if (password) {
        const plaintext = new Uint8Array(await blob.arrayBuffer())
        const encrypted = await encryptWithPassword(password, plaintext)
        blob = new Blob([encrypted.buffer as ArrayBuffer], { type: 'application/octet-stream' })
      }
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
      setExportPrompt(false)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setExportBusy(false)
    }
  }

  /** Reads the picked file far enough to tell whether exportCrypto.ts's
   * password envelope wraps it (see isPasswordEncrypted) — an encrypted
   * file needs ImportPasswordModal before there's any JSON to send
   * anywhere; a plain one goes straight to doImport, same as before this
   * feature existed. */
  async function importHosts(file: File) {
    setNotice(null)
    let buf: Uint8Array
    try {
      buf = new Uint8Array(await file.arrayBuffer())
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return
    }
    if (isPasswordEncrypted(buf)) {
      setPendingImportFile(file)
      return
    }
    await doImport(new TextDecoder().decode(buf))
  }

  async function decryptImportFile(password: string) {
    if (!pendingImportFile) return
    setNotice(null)
    try {
      const buf = new Uint8Array(await pendingImportFile.arrayBuffer())
      const plaintext = await decryptWithPassword(password, buf)
      setPendingImportFile(null)
      await doImport(new TextDecoder().decode(plaintext))
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  async function doImport(jsonText: string) {
    if (!(await confirmAction(t('hosts.confirmImport')))) {
      return
    }
    setImporting(true)
    try {
      const res = await fetch('/api/hub/import', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: jsonText,
      })
      const payload = await res.json()
      if (!res.ok) throw new Error(payload?.error ?? t('common.httpError', { status: res.status }))
      const { imported, errors } = payload as { imported: number; errors?: string[] }
      setNotice({
        kind: errors?.length ? 'error' : 'info',
        text: t('hosts.imported', {
          count: imported,
          errors: errors?.length ? t('hosts.importedErrors', { errors: errors.join('; ') }) : '',
        }),
      })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setImporting(false)
    }
  }

  // Удаление открывает окно с выбором того, что убрать с самого хоста:
  // цена у пунктов разная, и решать за оператора нельзя ни в ту, ни в
  // другую сторону.
  async function remove(host: HubHost, purge: PurgeOptions) {
    try {
      const res = await api<{ purge?: PurgeResult }>(`/hub/hosts/${host.id}`, {
        method: 'DELETE',
        body: purge,
      })
      setRemovingHost(null)
      reload()
      const p = res.purge
      if (p?.attempted && !p.ok) {
        // Запись всё равно удалена: хост мог быть уже погашен. Молчать об
        // этом нельзя — на сервере остался работающий nkt.
        setNotice({ kind: 'error', text: t('hosts.purgeFailed', { name: host.name, error: p.error ?? '' }) })
      } else if (p?.attempted && p.steps?.length) {
        setNotice({ kind: 'info', text: `${host.name}: ${p.steps.join('; ')}` })
      }
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  function closeJobModal() {
    setJob(null)
    setInstallHostId(null)
    setJobStatus(null)
    // The table's status/version/last-seen columns only otherwise refresh
    // on done (see the poll effect above) or the next 30s tick — closing
    // right after a job finishes, or while it's still running, must not
    // leave the buttons showing stale state until then.
    reload()
  }

  /** Reopens the progress/log for a host's current or most recent install —
   * the only way back in once the modal has been closed, since the job id
   * itself is otherwise only ever known transiently (see startInstall). */
  async function openInstallLog(host: HubHost) {
    setNotice(null)
    try {
      const res = await api<{ job: string }>(`/hub/hosts/${host.id}/install/latest`)
      setInstallHostId(host.id)
      setJobStatus(null)
      setJob(res.job)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  const installingHost = hosts?.find((h) => h.id === installHostId)

  function renderActions(h: HubHost) {
    // "localhost" (the hub's own machine, see internal/hub/handlers.go's
    // localHostEntry) has no SSH install to manage — it's just a link into
    // its own dashboard, same as any other online host's "открыть".
    if (h.id === LOCAL_HOST_ID) {
      return (
        <div className="row row-nowrap">
          <RowAction action="open" label={t('hosts.open')} onClick={() => onSelect({ id: h.id, name: h.name })} />
        </div>
      )
    }
    const outdated = isOutdated(h, hubVersion)
    // Занята: по машине идёт задание хаба или на хост ставится nkt.
    // Журнал и отмена остаются живыми — иначе следить за работой и
    // прерывать её было бы нечем.
    const busy = busyVMs.has(h.id) || h.status === 'installing'
    // Машина с выключенным доменом: внутри неё ничего не ответит, и все
    // действия по SSH бессмысленны — только запустить саму машину.
    const vmOff = !!h.parent_id && !!h.vm_state && h.vm_state !== 'running'
    if (vmOff) {
      return (
        <div className="row row-nowrap">
          <RowAction action="start" label={t('hosts.vmStart')} loading={vmActing === h.id} disabled={busy} onClick={() => void vmDomainAction(h, 'start')} />
          <RowAction action="edit" label={t('hosts.edit')} disabled={busy} onClick={() => setEditingHost(h)} />
          <RowAction action="delete" label={t('hosts.delete')} danger loading={busy} disabled={busy} onClick={() => setRemovingHost(h)} />
        </div>
      )
    }
    return (
      <div className="row row-nowrap">
        {h.parent_id && h.vm_state === 'running' ? (
          <>
            <RowAction action="shutdown" label={t('hosts.vmShutdown')} danger loading={vmActing === h.id} disabled={busy} onClick={() => void vmDomainAction(h, 'shutdown')} />
            <RowAction action="destroy" label={t('hosts.vmDestroy')} danger disabled={busy} onClick={() => void vmDomainAction(h, 'destroy')} />
          </>
        ) : null}
        {h.status === 'online' && (
          <RowAction
            action="open"
            label={autoOpenHost?.id === h.id ? t('hosts.updatingBeforeOpen') : t('hosts.open')}
            loading={autoOpenHost?.id === h.id}
            disabled={busy}
            onClick={() => openHost(h)}
          />
        )}
        {isAddrUnknown(h) ? (
          // Установка по заглушке всё равно провалится рукопожатием с
          // 0.0.0.0, поэтому вместо неё предлагается то, чего не хватает.
          <RowAction
            action="address"
            label={`${t('hosts.detectAddress')} — ${t('hosts.detectAddressHint')}`}
            loading={detectingAddr === h.id || busy}
            disabled={busy}
            onClick={() => void detectAddress(h)}
          />
        ) : (
          <RowAction
            action={h.status === 'new' ? 'install' : outdated ? 'update' : 'install'}
            label={h.status === 'new' ? t('hosts.install') : outdated ? t('hosts.update') : t('hosts.reinstall')}
            loading={busy}
            disabled={busy}
            onClick={() => startInstall(h)}
          />
        )}
        {h.status === 'installing' && (
          <RowAction action="destroy" label={t('hosts.cancel')} danger onClick={() => cancelInstall(h)} />
        )}
        {h.status !== 'new' && (
          <RowAction action="logs" label={t('hosts.installLog')} onClick={() => openInstallLog(h)} />
        )}
        {h.status !== 'new' && h.status !== 'installing' && (
          <>
            <RowAction
              action="start"
              label={t('hosts.start')}
              loading={busyServiceIds.has(h.id) || busy}
              disabled={busy}
              onClick={() => startHost(h)}
            />
            <RowAction
              action="stop"
              label={t('hosts.stop')}
              danger
              loading={busyServiceIds.has(h.id) || busy}
              disabled={busy}
              onClick={() => stopHost(h)}
            />
          </>
        )}
        {/* Машину создаём только на хосте, где уже стоит nkt: команду
            создания выполняет он сам, а хаб лишь просит и ждёт. */}
        {h.status === 'online' && !h.parent_id && (
          <RowAction action="create" label={t('hosts.newVM')} disabled={busy} onClick={() => setProvisionOn(h)} />
        )}
        <RowAction action="edit" label={t('hosts.edit')} disabled={busy} onClick={() => setEditingHost(h)} />
        {h.ssh_auth_kind === 'key' && (
          <RowAction action="key" label={t('hosts.publicKey')} disabled={busy} onClick={() => showPubKey(h)} />
        )}
        <RowAction
          action="delete"
          label={t('hosts.delete')}
          danger
          loading={busy}
          disabled={busy}
          onClick={() => setRemovingHost(h)}
        />
      </div>
    )
  }

  /**
   * Машины хоста: создать новую и раскрыть список.
   *
   * Отдельной строкой под «открыть», а не среди остальных действий: это
   * не действие над самим хостом, а вход в то, что внутри него. В общем
   * ряду обе кнопки терялись в хвосте, и «+ 1 машина» оказывалась дальше
   * всего от машин, которые она показывает.
   */
  function renderVMActions(h: HubHost) {
    if (h.id === LOCAL_HOST_ID) return null
    const count = vmsByHost.get(h.id)?.length ?? 0
    // Машину создаём только на хосте, где уже стоит nkt: команду создания
    // выполняет он сам, а хаб лишь просит и ждёт.
    if (count === 0) return null
    return (
      <div className="row">
        {/* Сначала то, что уже есть, потом создание нового: список машин
            относится к кнопке «открыть» над ним, а «новая машина» —
            действие, и ему место с краю. Машин нет — нет и кнопки:
            «+ 0 машин» открывает пустоту. */}
        {count > 0 && (
          <Tooltip title={t('hosts.vmCount', { count })}>
            <Button type="link" size="small" aria-label={t('hosts.vmCount', { count })} onClick={() => toggleVMs(h.id)}>
              {openVMs.has(h.id) ? '−' : '+'} {count}
            </Button>
          </Tooltip>
        )}
      </div>
    )
  }

  /**
   * Строка хоста в раскрытом виде: его действия, а под ними — машины,
   * если список раскрыт «плюсом».
   *
   * Машина показывается здесь, а не отдельной строкой в разделе: она
   * привязана к своему хосту, переезжает между группами вместе с ним и
   * сама по себе никуда не перетаскивается.
   */
  function renderRowBody(h: HubHost) {
    const vms = vmsByHost.get(h.id) ?? []
    return (
      <>
        {renderVMActions(h)}
        {vms.length > 0 && openVMs.has(h.id) && (
          <div className="col host-vms">
            {vms.map((vm) => (
              <div key={vm.id} className="host-vm">
                <div className="row spread">
                  <span className="small">
                    <strong>{vm.name}</strong>{' '}
                    {isAddrUnknown(vm) ? (
                      <span className="muted">{t('hosts.addrUnknown')}</span>
                    ) : (
                      <span className="mono muted">
                        {vm.ssh_user}@{vm.addr}
                      </span>
                    )}
                  </span>
                  <span className="row" style={{ gap: '0.5rem' }}>
                    {vm.vm_state && (
                      <Tag color={vm.vm_state === 'running' ? 'success' : 'default'}>
                        {vm.vm_state === 'running' ? t('hosts.vmRunning') : t('hosts.vmOff', { state: vm.vm_state })}
                      </Tag>
                    )}
                    <HostStatusBadge status={vm.status} />
                  </span>
                </div>
                {renderActions(vm)}
              </div>
            ))}
          </div>
        )}
      </>
    )
  }

  const columns: TableColumnsType<HubHost> = [
    {
      // Состояние — первой колонкой и одной иконкой: слово в заголовке и
      // в ячейке занимало место, а сказать ему нечего сверх цвета. Текст
      // ошибки остаётся в подсказке.
      title: '',
      key: 'status',
      width: '2rem',
      className: 'nowrap',
      render: (_, h) => <HostStatusIcon host={h} />,
    },
    {
      title: t('hosts.colName'),
      key: 'name',
      // Действия — сразу за именем, в той же строке: отдельная строка
      // под каждым хостом удваивала высоту списка ради семи иконок.
      // Имя — не длиннее десяти знаков: длинное растягивало колонку и
      // сдвигало остальные; целиком оно в подсказке.
      render: (_, h) => (
        <div className="row row-nowrap" style={{ gap: '0.5rem' }}>
          <strong className="host-name" title={h.name}>
            {h.name}
          </strong>
          {renderActions(h)}
        </div>
      ),
    },
    {
      title: t('hosts.colAddr'),
      key: 'addr',
      render: (_, h) =>
        h.id === LOCAL_HOST_ID ? (
          <span className="small muted">—</span>
        ) : isAddrUnknown(h) ? (
          // Машина ещё не получила адрес: показывать «0.0.0.0» значило бы
          // выдавать заглушку за настоящий адрес.
          <span className="small muted">{t('hosts.addrUnknown')}</span>
        ) : (
          <span className="mono small">
            {h.ssh_user}@{h.addr}:{h.ssh_port}
          </span>
        ),
    },
    { title: t('hosts.colProblems'), key: 'problems', render: (_, h) => <ProblemsCell host={h} /> },
    {
      title: t('hosts.colArch'),
      key: 'arch',
      render: (_, h) => <span className="small">{h.id === LOCAL_HOST_ID ? '—' : h.arch || '—'}</span>,
    },
    {
      title: t('hosts.colSudo'),
      key: 'sudo',
      // «Снять NOPASSWD» — здесь, рядом с галочкой, а не среди общих
      // действий: кнопка относится ровно к тому, что показывает колонка.
      render: (_, h) =>
        h.ssh_user === 'root' || h.id === LOCAL_HOST_ID ? (
          <span className="small muted">—</span>
        ) : (
          <span className="row row-nowrap" style={{ gap: '0.15rem', alignItems: 'center' }}>
            <SudoBadge status={h.sudo_status} />
            {h.sudo_status === 'nopasswd' ? (
              <RowAction
                action="disable"
                label={t('hosts.removeNopasswd')}
                danger
                disabled={h.status === 'installing'}
                onClick={() => removeSudoAccess(h)}
              />
            ) : (
              // Второй знак той же ширины, что кнопка «снять»: без него
              // строки без NOPASSWD были бы короче, и колонка прыгала.
              <Tooltip title={t(SUDO_LABEL_KEY[h.sudo_status ?? ''])}>
                <span className="sudo-placeholder" aria-hidden="true">
                  <CloseCircleFilled style={{ color: 'var(--status-critical)' }} />
                </span>
              </Tooltip>
            )}
          </span>
        ),
    },
    {
      title: t('hosts.colChannel'),
      key: 'channel',
      render: (_, h) => (h.id === LOCAL_HOST_ID ? <span className="small muted">—</span> : <TunnelChannelBadge host={h} />),
    },
    {
      title: t('hosts.colVersion'),
      key: 'version',
      render: (_, h) => {
        const outdated = isOutdated(h, hubVersion)
        // running_version comes from the host's own binary (hub poll);
        // nkt_version is what the hub recorded installing. A mismatch is
        // the only visible sign that an update silently did not take
        // effect — the host keeps working, just as the older version.
        // localhost has no separate "installed" version to compare against
        // — it always runs whatever build the hub itself was built from
        // (see internal/hub/handlers.go's localHostEntry), so a mismatch
        // here can never mean "an update didn't take effect".
        const stale = h.id !== LOCAL_HOST_ID && !!h.running_version && h.running_version !== h.nkt_version
        return (
          <span className="small mono">
            {h.running_version || h.nkt_version || '—'}
            {stale && (
              <div className="small" style={{ color: 'var(--status-warning)' }}>
                {t('hosts.staleVersion', { version: h.nkt_version })}
              </div>
            )}
            {!stale && outdated && (
              <div className="small" style={{ color: 'var(--status-warning)' }}>
                {t('hosts.hubVersion', { version: hubVersion })}
              </div>
            )}
          </span>
        )
      },
    },
    {
      title: t('hosts.colLastSeen'),
      key: 'last_seen',
      render: (_, h) => (
        <span className="small nowrap">
          {h.id === LOCAL_HOST_ID ? '—' : h.last_seen_at ? formatRelative(h.last_seen_at) : t('hosts.never')}
        </span>
      ),
    },
  ]

  const outdatedCount = (hosts ?? []).filter((h) => h.id !== LOCAL_HOST_ID && isOutdated(h, hubVersion)).length

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('hosts.title')}
            <InfoHint>{t('hosts.hint')}</InfoHint>
          </h1>
        </div>
        <div className="row" style={{ gap: '1rem' }}>
          <Button type="primary" onClick={() => setCreatingHost(true)}>
            {t('hosts.addHost')}
          </Button>
          <Button
            type={outdatedCount > 0 ? 'primary' : 'default'}
            loading={updateAllQueue !== null}
            disabled={outdatedCount === 0 || bulkBusy !== null}
            onClick={updateAllOutdated}
          >
            {outdatedCount > 0 ? t('hosts.updateAllCount', { count: outdatedCount }) : t('hosts.updateAllNone')}
          </Button>
          <Button
            loading={bulkBusy === 'start'}
            disabled={bulkBusy === 'stop' || updateAllQueue !== null}
            onClick={() => bulkSetServiceRunning(true)}
          >
            {t('hosts.startAll')}
          </Button>
          <Button
            danger
            loading={bulkBusy === 'stop'}
            disabled={bulkBusy === 'start' || updateAllQueue !== null}
            onClick={() => bulkSetServiceRunning(false)}
          >
            {t('hosts.stopAll')}
          </Button>
          <Button onClick={() => exportHosts(false)}>{t('hosts.export')}</Button>
          <Tooltip title={t('hosts.exportWithKeyTooltip')}>
            <Button onClick={() => exportHosts(true)}>{t('hosts.exportWithKey')}</Button>
          </Tooltip>
          <Button loading={importing} onClick={() => importInputRef.current?.click()}>
            {t('hosts.import')}
          </Button>
          <input
            ref={importInputRef}
            type="file"
            accept="application/json"
            style={{ display: 'none' }}
            onChange={(e) => {
              const file = e.target.files?.[0]
              e.target.value = ''
              if (file) void importHosts(file)
            }}
          />
          <Tooltip title={t('hosts.notifyTooltip')}>
            <span className="row" style={{ gap: '0.4rem' }}>
              <Switch checked={notifyOn} onChange={toggleNotify} />
              {t('hosts.notifyLabel')}
            </span>
          </Tooltip>
        </div>
      </div>

      {provisionOn && (
        <ProvisionVMModal
          host={provisionOn}
          hubVersion={hubVersion}
          onClose={() => setProvisionOn(null)}
          onStarted={(text, jobID) => {
            setProvisionOn(null)
            setNotice({ kind: 'info', text })
            void openHubJob(jobID)
            reload()
          }}
        />
      )}

      {hubJob && <JobLogModal job={hubJob} scope="/hosts/local" onClose={() => setHubJob(null)} />}

      {applyTo && (
        <ApplyProfileModal
          group={applyTo.group}
          hostCount={applyTo.hosts}
          profiles={profiles.data?.profiles ?? []}
          onClose={() => setApplyTo(null)}
          onStarted={(text, jobID) => {
            setApplyTo(null)
            setNotice({ kind: 'info', text })
            void openHubJob(jobID)
          }}
        />
      )}

      {groupDialog && (
      <Modal
        onClose={() => setGroupDialog(null)}
        title={groupDialog.mode === 'rename' ? t('hosts.renameGroup') : t('hosts.createGroup')}
      >
        <Form layout="vertical" onFinish={() => void submitGroupDialog()}>
          <Form.Item label={t('hosts.group')}>
            <Input value={groupName} onChange={(e) => setGroupName(e.target.value)} autoFocus maxLength={64} />
          </Form.Item>
          <div className="row" style={{ gap: '0.5rem' }}>
            <Button type="primary" htmlType="submit" disabled={!groupName.trim()}>
              {t('common.save')}
            </Button>
            <Button onClick={() => setGroupDialog(null)}>{t('common.cancel')}</Button>
          </div>
        </Form>
      </Modal>
      )}

      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
      <ErrorNote error={error} />

      <Card
        title={t('hosts.registeredHosts')}
        actions={
          <Button
            size="small"
            onClick={() => {
              setGroupName('')
              setGroupDialog({ mode: 'create' })
            }}
          >
            {t('hosts.createGroup')}
          </Button>
        }
      >
        {loading && !hosts ? (
          <Loading what={t('hosts.loading')} />
        ) : !hosts?.length && !groupedHosts.length ? (
          <p className="small muted">{t('hosts.noHosts')}</p>
        ) : (
          <div className="col" style={{ gap: '0.6rem' }}>
            {groupedHosts.map(({ group, items }) => {
              const collapsed = collapsedGroups.includes(group)
              return (
                <div
                  key={group || '\u0000none'}
                  className={dropGroup === group ? 'host-group host-group-drop' : 'host-group'}
                  // Бросить строку можно в любое место раздела, а не только
                  // в его заголовок: попасть мышью в тонкую полоску
                  // заголовка тяжело, особенно на большом списке.
                  onDragOver={(e) => {
                    if (draggingHost === null) return
                    e.preventDefault()
                    setDropGroup(group)
                  }}
                  onDragLeave={() => setDropGroup((cur) => (cur === group ? null : cur))}
                  onDrop={() => {
                    if (draggingHost !== null) void moveToGroup(draggingHost, group)
                    setDraggingHost(null)
                    setDropGroup(null)
                  }}
                >
                  <div className="host-group-head">
                    <button className="host-group-toggle" onClick={() => toggleGroup(group)}>
                      <span className="host-group-caret">{collapsed ? '▸' : '▾'}</span>
                      <span className="host-group-name">{group || t('hosts.groupNone')}</span>
                      <span className="small muted">{t('hosts.groupCount', { count: items.length })}</span>
                    </button>
                    {/* Профиль раскатывается по группе целиком — хосты
                        обходятся по одному, чтобы ошибка в описании не
                        досталась сразу всем. */}
                    {/* Считаются только настоящие хосты: localhost — своя
                        машина хаба, к ней профиль применяют в её же
                        разделе «Профили», а не через SSH. */}
                    {items.some((h) => h.id !== LOCAL_HOST_ID) && (
                      // Видна всегда, а не по наведению, как переименование
                      // с удалением: раскатка профиля — то, ради чего в
                      // этот заголовок и смотрят, и прятать её незачем.
                      // Без единого профиля кнопка выключена и говорит,
                      // где его завести, — иначе её отсутствие выглядело
                      // бы поломкой.
                      <span className="row" style={{ gap: '0.25rem' }}>
                        <Tooltip title={(profiles.data?.profiles?.length ?? 0) === 0 ? t('hosts.applyProfileNone') : ''}>
                          <Button
                            size="small"
                            type="text"
                            disabled={(profiles.data?.profiles?.length ?? 0) === 0}
                            onClick={() =>
                              setApplyTo({ group, hosts: items.filter((h) => h.id !== LOCAL_HOST_ID).length })
                            }
                          >
                            {t('hosts.applyProfile')}
                          </Button>
                        </Tooltip>
                      </span>
                    )}
                    {/* «Без группы» — не группа, а остаток: переименовать
                        или удалить его нечего. */}
                    {group && (
                      <span className="row host-group-actions" style={{ gap: '0.25rem' }}>
                        <Button
                          size="small"
                          type="text"
                          onClick={() => {
                            setGroupName(group)
                            setGroupDialog({ mode: 'rename', from: group })
                          }}
                        >
                          {t('hosts.renameGroup')}
                        </Button>
                        <Button size="small" type="text" danger onClick={() => void deleteGroup(group, items.length)}>
                          {t('common.delete')}
                        </Button>
                      </span>
                    )}
                  </div>
                  {!collapsed && !items.length && (
                    <p className="small muted host-group-empty">{t('hosts.groupEmpty')}</p>
                  )}
                  {!collapsed && items.length > 0 && (
                    <div className="table-wrap">
                      <DataTable<HubHost>
                        dataSource={items}
                        columns={columns}
                        rowKey="id"
                        onRow={(host) => ({
                          draggable: true,
                          onDragStart: () => setDraggingHost(host.id),
                          onDragEnd: () => {
                            setDraggingHost(null)
                            setDropGroup(null)
                          },
                        })}
                        expandable={{
                          expandedRowKeys: items.map((h) => h.id),
                          expandIcon: () => null,
                          rowExpandable: (h) => (vmsByHost.get(h.id)?.length ?? 0) > 0,
                          expandedRowRender: renderRowBody,
                        }}
                      />
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Card>

      {creatingHost && (
        <Modal title={t('hosts.addHostTitle')} onClose={() => setCreatingHost(false)} width={860}>
          <HostForm
            knownGroups={knownGroups}
            onDone={(name, authorizedKey, _t, _tu, created) => {
              setCreatingHost(false)
              reload()
              if (created?.bootstrap) pendingBootstrap.current.set(created.id, created.bootstrap)
              if (authorizedKey) {
                // Ручной режим: пока ключ не окажется в authorized_keys на
                // хосте, установке подключаться нечем — сначала показываем
                // ключ, установку оператор запускает сам, скопировав его.
                setPubKeyInfo({ hostName: name, key: authorizedKey })
                return
              }
              // Автонастройка: реквизиты для подключения уже есть, ждать
              // нечего — форма закрывается и сразу открывается журнал
              // установки, вместо того чтобы искать кнопку в таблице.
              if (created) void startInstall({ id: created.id })
            }}
          />
        </Modal>
      )}

      {editingHost && (
        <Modal
          title={t('hosts.editHostTitle', { name: editingHost.name })}
          onClose={() => setEditingHost(null)}
          width={860}
        >
          <HostForm
            initial={editingHost}
            knownGroups={knownGroups}
            onDone={(name, authorizedKey, terminalEnabledChanged, tunnelEnabledChanged) => {
              const host = editingHost
              setEditingHost(null)
              reload()
              if (authorizedKey) setPubKeyInfo({ hostName: name, key: authorizedKey })
              // Saving either checkbox alone only updates the hub's own
              // record — nothing changes on the host itself until
              // nkt.env is rewritten and the service restarted, which is
              // exactly what a reinstall does. Only for a host already
              // past its first install: a brand new one goes through
              // that install for the first time via its own separate
              // flow, and one already installing must not get a second,
              // concurrent job racing the first.
              if (
                (terminalEnabledChanged || tunnelEnabledChanged) &&
                host.status !== 'new' &&
                host.status !== 'installing'
              ) {
                void startInstall(host)
              }
            }}
          />
        </Modal>
      )}

      {removingHost && (
        <RemoveHostModal
          host={removingHost}
          onCancel={() => setRemovingHost(null)}
          onConfirm={(purge) => remove(removingHost, purge)}
        />
      )}

      {pubKeyInfo && (
        <PublicKeyModal
          hostName={pubKeyInfo.hostName}
          authorizedKey={pubKeyInfo.key}
          onClose={() => setPubKeyInfo(null)}
        />
      )}

      {job && (
        <Modal
          title={
            updateAllQueue !== null
              ? t('hosts.installTitleBatch', {
                  name: installingHost?.name ?? t('hosts.installTitleFallback'),
                  current: updateAllResults.length + 1,
                  total: updateAllTotal,
                })
              : t('hosts.installTitle', { name: installingHost?.name ?? t('hosts.installTitleFallback') })
          }
          onClose={closeJobModal}
          maskClosable={false}
        >
          <InstallLog events={jobStatus?.events ?? []} />
          {jobStatus?.done ? (
            jobStatus.error ? (
              <Banner kind="error">
                <div>{t('hosts.jobErrorLabel')}</div>
                {/* Plain text through Alert's own message prop collapses
                    newlines like any other inline content — losing exactly
                    the line breaks diagnoseInstallError's sudoersHint
                    depends on to be readable/copyable (the two commands an
                    operator needs to grant passwordless sudo). A <pre>
                    block, same styling PublicKeyModal already uses for its
                    own copyable multi-line text, preserves them. */}
                <pre className="diff mono" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', marginTop: '0.4rem' }}>
                  {jobStatus.error}
                </pre>
              </Banner>
            ) : (
              <Banner kind="info">{t('hosts.jobDone')}</Banner>
            )
          ) : (
            <p className="small muted row" style={{ alignItems: 'center', marginBottom: 0 }}>
              {t('hosts.jobRunning')}
            </p>
          )}
        </Modal>
      )}

      {exportPrompt && (
        <ExportPasswordModal
          busy={exportBusy}
          onDownload={(password) => downloadExport(true, password)}
          onClose={() => setExportPrompt(false)}
        />
      )}

      {pendingImportFile && (
        <ImportPasswordModal
          fileName={pendingImportFile.name}
          onDecrypt={decryptImportFile}
          onClose={() => setPendingImportFile(null)}
        />
      )}
    </>
  )
}

/**
 * Shows a hub-generated (or re-fetched) public key for the operator to
 * copy onto the target host's own ~/.ssh/authorized_keys — the private
 * half that matches it never leaves the hub.
 */
function PublicKeyModal({
  hostName,
  authorizedKey,
  onClose,
}: {
  hostName: string
  authorizedKey: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  async function copy() {
    try {
      await navigator.clipboard.writeText(authorizedKey)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard API can be unavailable (e.g. no HTTPS) — the text below
      // is still selectable and copyable by hand either way.
    }
  }

  return (
    <Modal title={t('hosts.publicKeyTitle', { name: hostName })} onClose={onClose}>
      <p className="small muted">
        <Trans i18nKey="hosts.publicKeyBody" components={{ code: <code className="mono" /> }} />
      </p>
      <pre className="diff mono" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
        {authorizedKey}
      </pre>
      <div>
        <Button onClick={copy}>{copied ? t('hosts.copied') : t('hosts.copy')}</Button>
      </div>
    </Modal>
  )
}

/**
 * Opened by "экспорт с ключом" instead of downloading straight away — that
 * file carries the hub's own master key plus every host's secret, so it
 * always gets a chance to be password-protected (AES-256-GCM via
 * exportCrypto.ts, decryptable by `nkt hub import` or this same modal's
 * counterpart on another hub) before it ever touches disk. Leaving the
 * password field empty and downloading anyway is still possible — this is
 * a reminder, not a hard requirement — but needs an explicit second
 * confirmation, the same way the CLI's own `nkt hub delete` treats it.
 */
function ExportPasswordModal({
  busy,
  onDownload,
  onClose,
}: {
  busy: boolean
  onDownload: (password?: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')

  async function download() {
    if (!password) {
      if (!(await confirmAction(t('hosts.confirmExportUnencrypted')))) {
        return
      }
      onDownload(undefined)
      return
    }
    onDownload(password)
  }

  return (
    <Modal title={t('hosts.exportWithKeyTitle')} onClose={onClose}>
      <p className="small muted">
        <Trans i18nKey="hosts.exportWithKeyBody" components={{ strong: <strong /> }} />
      </p>
      <Form layout="vertical" onFinish={download}>
        <Form.Item label={t('hosts.encryptPasswordLabel')}>
          <Input.Password
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            autoComplete="new-password"
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {password ? t('hosts.downloadEncrypted') : t('hosts.download')}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}

/** Opened by "импорт" when the picked file turns out to be password-
 * encrypted (see exportCrypto.ts's isPasswordEncrypted) — decryption
 * happens entirely in the browser before anything is sent to the hub;
 * the plaintext export never touches disk. */
function ImportPasswordModal({
  fileName,
  onDecrypt,
  onClose,
}: {
  fileName: string
  onDecrypt: (password: string) => Promise<void>
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit() {
    setBusy(true)
    setError(null)
    try {
      await onDecrypt(password)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('hosts.encryptedFileTitle')} onClose={onClose}>
      <p className="small muted">{t('hosts.encryptedFileBody', { fileName })}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <Form layout="vertical" onFinish={submit}>
        <Form.Item label={t('hosts.password')}>
          <Input.Password
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            autoComplete="current-password"
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy} disabled={!password}>
            {t('hosts.decryptAndImport')}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}

/** Auto-scrolling live log of an install job's progress — same shape as
 * Certificates.tsx's RenewLog. */
function InstallLog({ events }: { events: RenewEvent[] }) {
  const { t } = useTranslation()
  const preRef = useRef<HTMLPreElement>(null)

  useEffect(() => {
    const el = preRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [events.length])

  if (events.length === 0) {
    return <p className="small muted">{t('hosts.startingLog')}</p>
  }

  return (
    <pre ref={preRef} className="diff" style={{ maxHeight: '22rem' }}>
      {events
        .map((e) => `[${new Date(e.time).toLocaleTimeString(i18n.language === 'en' ? 'en-US' : 'ru-RU')}] ${e.text}`)
        .join('\n')}
    </pre>
  )
}

/** Что убрать с самого хоста при удалении — internal/hub/purge.go. */
export interface PurgeOptions {
  service: boolean
  data: boolean
  access: boolean
  user: boolean
  restore_password: boolean
  /** Удалить саму машину на хосте. Только для машин. */
  vm: boolean
  /** Вместе с дисками этой машины. */
  vm_disks: boolean
}

interface PurgeResult {
  attempted: boolean
  ok: boolean
  steps?: string[]
  error?: string
}

/** Разовая подготовка нового хоста — internal/hub/bootstrap.go. */
export interface BootstrapOptions {
  enabled: boolean
  user: string
  /** Публичный ключ оператора для создаваемой учётной записи. */
  user_key: string
  packages: string[]
  disable_password_auth: boolean
}

/** Набор по умолчанию повторяет BootstrapPackagesDefault на стороне хаба:
 * первые шесть нужны самому nkt, tmux и btop включают режим tmux в
 * терминале и живой просмотр нагрузки. */
/** Где запоминается, какие группы хостов свёрнуты. */
const HOST_GROUPS_COLLAPSED_KEY = 'nkt-host-groups-collapsed'

export const BOOTSTRAP_PACKAGES_DEFAULT =
  'dbus sudo iproute2 procps ca-certificates curl tmux btop neovim git gh mc'

type AuthKind = 'generated' | 'password' | 'key'

// Два сценария: «Автонастройка» — хаб заходит по паролю и настраивает
// хост сам; «Ручная» — хаб генерирует ключ, оператор сам кладёт его в
// authorized_keys. Вставка собственного приватного ключа убрана из
// интерфейса: тип 'key' остаётся в модели ради уже добавленных так
// хостов, но заводить новые этим способом больше нельзя.
const AUTH_KIND_OPTIONS: { value: AuthKind; labelKey: string }[] = [
  { value: 'password', labelKey: 'hosts.authPassword' },
  { value: 'generated', labelKey: 'hosts.authGenerated' },
]

type HostFormValues = {
  name: string
  addr: string
  ssh_port: number
  ssh_user: string
  secret?: string
  group?: string
  terminal_enabled: boolean
  tunnel_enabled: boolean
}

/**
 * Add-host form, or (with `initial` set) an edit form for an existing one.
 * Editing never requires re-entering the SSH secret — an empty secret field
 * leaves whatever is already stored untouched (see Manager.UpdateHost).
 * onDone receives the host's name, the freshly generated public key when
 * auth_kind is "generated", and whether terminal_enabled/tunnel_enabled
 * actually changed — the caller uses those to auto-trigger a reinstall,
 * since saving either checkbox alone only updates the hub's own record and
 * does nothing on the host itself until nkt.env is rewritten and the
 * service restarted (see Manager.UpdateHost / install).
 */
function HostForm({
  initial,
  knownGroups,
  onDone,
}: {
  initial?: HubHost
  knownGroups: string[]
  onDone: (
    name: string,
    generatedAuthorizedKey?: string,
    terminalEnabledChanged?: boolean,
    tunnelEnabledChanged?: boolean,
    created?: { id: number; bootstrap?: BootstrapOptions },
  ) => void
}) {
  const { t } = useTranslation()
  const editing = initial !== undefined
  const [form] = Form.useForm<HostFormValues>()
  // New hosts default to a hub-generated key — the operator's own private
  // key never has to be pasted anywhere for the common case. Editing
  // defaults to whatever the host already uses, since switching it is an
  // explicit choice, not the default action of opening the form.
  // Новый хост открывается на сценарии «root и пароль»: это состояние
  // свежего сервера, и только в нём хаб способен подготовить хост сам.
  // Правка существующего открывается на том способе, который у него уже
  // есть — менять его при открытии формы никто не просил.
  const [authKind, setAuthKind] = useState<AuthKind>(initial?.ssh_auth_kind ?? 'password')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Подготовка имеет смысл ровно один раз — при добавлении нового хоста,
  // на который пока есть только root с паролем.
  // Включена сразу: в сценарии с паролем подготовка — это и есть то, ради
  // чего сценарий выбран. Снять её осмысленно, только если хост уже
  // подготовлен, а пароль просто удобнее.
  const [bootstrapEnabled, setBootstrapEnabled] = useState(!initial)
  const [bootstrapUser, setBootstrapUser] = useState('')
  const [bootstrapUserKey, setBootstrapUserKey] = useState('')
  const [bootstrapPackages, setBootstrapPackages] = useState(BOOTSTRAP_PACKAGES_DEFAULT)
  const [bootstrapDisablePassword, setBootstrapDisablePassword] = useState(false)
  // Набор хранится на хабе, а не в коде страницы: правка при добавлении
  // одного хоста должна быть видна и при добавлении следующего.
  const bootstrapDefaults = useApi<BootstrapOptions>(editing ? null : '/hub/bootstrap/defaults')
  useEffect(() => {
    const d = bootstrapDefaults.data
    if (!d) return
    setBootstrapUser(d.user)
    setBootstrapPackages(d.packages.join(' '))
    setBootstrapDisablePassword(d.disable_password_auth)
  }, [bootstrapDefaults.data])

  async function submit(values: HostFormValues) {
    setBusy(true)
    setError(null)
    try {
      const terminalEnabled = values.terminal_enabled ?? false
      const tunnelEnabled = values.tunnel_enabled ?? false
      const body = {
        name: values.name,
        addr: values.addr,
        ssh_port: values.ssh_port,
        ssh_user: values.ssh_user,
        group: (values.group ?? '').trim(),
        auth_kind: authKind,
        secret: values.secret ?? '',
        terminal_enabled: terminalEnabled,
        tunnel_enabled: tunnelEnabled,
      }
      // Правка становится умолчанием для следующих хостов — ровно то, чего
      // ждёшь от поля, которое каждый раз показывает прошлое значение.
      // Ошибка сохранения не должна ронять добавление хоста: настройка
      // вторична по отношению к тому, ради чего форму открыли.
      const bootstrapValues: BootstrapOptions = {
        enabled: bootstrapEnabled,
        user: bootstrapUser.trim(),
        user_key: bootstrapUserKey.trim(),
        packages: bootstrapPackages.split(/[\s,]+/).filter(Boolean),
        disable_password_auth: bootstrapDisablePassword,
      }
      if (!editing && bootstrapEnabled) {
        await api('/hub/bootstrap/defaults', { method: 'PUT', body: bootstrapValues }).catch(() => {})
      }

      let authorizedKey: string | undefined
      let createdId: number | undefined
      if (editing) {
        const res = await api<{ authorized_key?: string }>(`/hub/hosts/${initial.id}`, {
          method: 'PATCH',
          body,
        })
        authorizedKey = res.authorized_key
      } else {
        const res = await api<{ id: number; authorized_key?: string }>('/hub/hosts', {
          method: 'POST',
          body,
        })
        authorizedKey = res.authorized_key
        createdId = res.id
        form.setFieldsValue({ name: '', addr: '' })
      }
      form.setFieldsValue({ secret: '' })
      const terminalEnabledChanged = editing && terminalEnabled !== (initial.terminal_enabled ?? false)
      const tunnelEnabledChanged = editing && tunnelEnabled !== (initial.tunnel_enabled ?? false)
      onDone(
        values.name,
        authorizedKey,
        terminalEnabledChanged,
        tunnelEnabledChanged,
        createdId === undefined
          ? undefined
          : {
              id: createdId,
              bootstrap: bootstrapEnabled ? bootstrapValues : undefined,
            },
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const secretLabel = t(
    authKind === 'key'
      ? editing
        ? 'hosts.secretKeyEditing'
        : 'hosts.secretKeyNew'
      : editing
        ? 'hosts.secretPasswordEditing'
        : 'hosts.secretPasswordNew',
  )

  const formEl = (
    <Form<HostFormValues>
      form={form}
      layout="vertical"
      onFinish={submit}
      initialValues={{
        name: initial?.name ?? '',
        addr: initial?.addr ?? '',
        ssh_port: initial?.ssh_port ?? 22,
        ssh_user: initial?.ssh_user ?? 'root',
        group: initial?.group ?? '',
        terminal_enabled: initial?.terminal_enabled ?? false,
        // On by default for a new host (unlike terminal_enabled): unlike a
        // root shell in the browser, the fallback channel only ever kicks
        // in once SSH itself is already unreachable, so there is no extra
        // exposure from having it ready. Editing an existing host still
        // defaults to whatever it already has.
        tunnel_enabled: initial?.tunnel_enabled ?? true,
      }}
    >
      {!editing && <p className="small muted">{t('hosts.addHostHint')}</p>}
      {error && <Banner kind="error">{error}</Banner>}
      <div className="filters" style={{ flexWrap: 'nowrap' }}>
        <Form.Item name="name" label={t('hosts.name')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '9rem' }}>
          <Input />
        </Form.Item>
        <Form.Item
          name="addr"
          label={t('hosts.addr')}
          rules={[{ required: true }]}
          style={{ flex: 1, minWidth: '9rem' }}
        >
          <Input />
        </Form.Item>
        <Form.Item name="ssh_port" label={t('hosts.sshPort')} rules={[{ required: true }]} style={{ width: '6rem' }}>
          <InputNumber min={1} max={65535} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="ssh_user" label={t('hosts.sshUser')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '8rem' }}>
          <Input />
        </Form.Item>
        {/* Группа — свободный текст с подсказкой уже существующих: новые
            заводятся на ходу, а справочник, который надо заполнять заранее,
            мешал бы ровно тому, ради чего группы и нужны.

            У машины поля нет вовсе: её группа — это группа её хоста, и
            пустое поле в форме правки сбрасывало бы машину в «Без
            группы» каждый раз, когда у неё правят адрес. */}
        {!initial?.parent_id && (
        <Form.Item name="group" label={t('hosts.group')} style={{ flex: 1, minWidth: '9rem' }}>
          <AutoComplete
            options={knownGroups.map((g) => ({ value: g }))}
            filterOption={(input, option) =>
              (option?.value ?? '').toString().toLowerCase().includes(input.toLowerCase())
            }
            allowClear
          />
        </Form.Item>
        )}
      </div>

      {/* Режимы установки — табами, а не выпадающим списком: каждый таб
          законченный сценарий, и внутри видно только то, что к нему
          относится. Прежняя связка «список + галочка подготовки»
          позволяла собрать заведомо нерабочее сочетание — подготовку при
          входе по ключу, которого на свежем хосте ещё нет, из-за чего
          установка падала на подключении, так и не дойдя до неё. */}
      <Tabs
        activeKey={authKind}
        onChange={(key) => setAuthKind(key as AuthKind)}
        items={AUTH_KIND_OPTIONS.map((o) => ({ key: o.value, label: t(o.labelKey) }))}
        style={{ marginBottom: '0.4rem' }}
      />
      {authKind === 'generated' ? (
        <p className="small muted">
          <Trans i18nKey="hosts.generatedKeyHint" components={{ code: <code className="mono" /> }} />
          {editing && t('hosts.generatedKeyHintReplace')}
        </p>
      ) : (
        <Form.Item name="secret" label={secretLabel} rules={[{ required: !editing }]}>
          {authKind === 'key' ? (
            <Input.TextArea
              className="mono"
              rows={6}
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
              autoComplete="off"
            />
          ) : (
            <Input.Password autoComplete="new-password" />
          )}
        </Form.Item>
      )}
      {/* Только в сценарии с паролем: подготовке нужен рабочий доступ, а
          на свежем сервере он ровно один — root с паролем. Для правки
          существующего хоста блока нет: подготовка делается один раз. */}
      {!editing && authKind === 'password' && (
        <div
          style={{
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            padding: '0.6rem 0.75rem',
            marginBottom: '0.6rem',
          }}
        >
          <Checkbox checked={bootstrapEnabled} onChange={(e) => setBootstrapEnabled(e.target.checked)}>
            {t('hosts.bootstrapEnable')}
          </Checkbox>
          <div className="small muted" style={{ marginTop: '0.25rem' }}>
            {t('hosts.bootstrapHint')}
          </div>
          {bootstrapEnabled && (
            <div style={{ marginTop: '0.5rem' }}>
              <label className="small">
                {t('hosts.bootstrapUser')}
                <Input
                  value={bootstrapUser}
                  onChange={(e) => setBootstrapUser(e.target.value)}
                  placeholder="nkt"
                />
              </label>
              {/* Ключ оператора кладётся той же учётной записи, под которой
                  дальше работают терминал, tmux и всё остальное: хаб ходит
                  своим ключом, человек — своим, а пользователь один. */}
              {bootstrapUser.trim() !== '' && (
                <label className="small" style={{ display: 'block', marginTop: '0.4rem' }}>
                  {t('hosts.bootstrapUserKey')}
                  <Input.TextArea
                    className="mono"
                    value={bootstrapUserKey}
                    onChange={(e) => setBootstrapUserKey(e.target.value)}
                    autoSize={{ minRows: 2, maxRows: 4 }}
                    placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5… user@laptop"
                    spellCheck={false}
                  />
                  <span className="small muted">{t('hosts.bootstrapUserKeyHint')}</span>
                </label>
              )}
              <label className="small" style={{ display: 'block', marginTop: '0.4rem' }}>
                {t('hosts.bootstrapPackages')}
                <Input.TextArea
                  value={bootstrapPackages}
                  onChange={(e) => setBootstrapPackages(e.target.value)}
                  autoSize={{ minRows: 2, maxRows: 4 }}
                />
              </label>
              <Button
                style={{ marginTop: '0.4rem' }}
                onClick={() => {
                  setBootstrapUser('')
                  setBootstrapUserKey('')
                  setBootstrapPackages(BOOTSTRAP_PACKAGES_DEFAULT)
                }}
              >
                {t('hosts.bootstrapReset')}
              </Button>
              <br />
              <Checkbox
                checked={bootstrapDisablePassword}
                onChange={(e) => setBootstrapDisablePassword(e.target.checked)}
                style={{ marginTop: '0.4rem' }}
              >
                {t('hosts.bootstrapDisablePassword')}
              </Checkbox>
              <div className="small muted">{t('hosts.bootstrapDisablePasswordHint')}</div>
            </div>
          )}
        </div>
      )}

      <Form.Item name="terminal_enabled" valuePropName="checked" style={{ marginBottom: '0.4rem' }}>
        <Checkbox>
          {t('hosts.terminalEnabled')}
          <div className="small muted" style={{ fontWeight: 400 }}>
            {t('hosts.terminalEnabledHint')}
            {t(editing ? 'hosts.reinstallsOnChange' : 'hosts.appliesOnFirstInstall')}
          </div>
        </Checkbox>
      </Form.Item>
      <Form.Item name="tunnel_enabled" valuePropName="checked" style={{ marginBottom: '0.4rem' }}>
        <Checkbox>
          {t('hosts.tunnelEnabled')}
          <div className="small muted" style={{ fontWeight: 400 }}>
            {t('hosts.tunnelEnabledHint')}
            {t(editing ? 'hosts.reinstallsOnChange' : 'hosts.appliesOnFirstInstall')}
          </div>
        </Checkbox>
      </Form.Item>
      <Form.Item style={{ marginBottom: 0 }}>
        <Button type="primary" htmlType="submit" loading={busy}>
          {editing ? t('hosts.save') : t('hosts.addHost')}
        </Button>
      </Form.Item>
    </Form>
  )

  return formEl
}

/**
 * Окно удаления хоста. Галочки выключены по умолчанию: удаление из хаба и
 * очистка сервера — разные действия, и второе делается только по прямому
 * указанию. Порядок пунктов — по возрастанию необратимости.
 */
function RemoveHostModal({
  host,
  onCancel,
  onConfirm,
}: {
  host: HubHost
  onCancel: () => void
  onConfirm: (purge: PurgeOptions) => void
}) {
  const { t } = useTranslation()
  // Возврат пароля включён сразу: вместе с хабом с хоста уезжает и его
  // ключ, и без пароля хост остался бы вообще без способа входа.
  // Машина — это не сервер, с которого убирают следы nkt: удаляя её
  // запись, обычно хотят удалить и саму машину. Оставить её работать без
  // хаба можно, но это отдельное решение, поэтому пункт виден и снимается.
  const isVM = !!host.parent_id
  const [purge, setPurge] = useState<PurgeOptions>({
    service: false,
    data: false,
    access: false,
    user: false,
    restore_password: !isVM,
    vm: isVM,
    // Диски — отдельный вопрос и отдельная галочка: описание машины
    // заводится заново за минуту, а диск с её данными — нет.
    vm_disks: false,
  })
  const [busy, setBusy] = useState(false)

  const item = (key: keyof PurgeOptions, label: string, hint: string, disabled = false) => (
    <label style={{ display: 'block', marginBottom: '0.5rem', opacity: disabled ? 0.5 : 1 }}>
      <Checkbox
        checked={purge[key]}
        disabled={disabled}
        onChange={(e) => setPurge((prev) => ({ ...prev, [key]: e.target.checked }))}
      >
        {label}
      </Checkbox>
      <div className="small muted" style={{ marginLeft: '1.5rem' }}>
        {hint}
      </div>
    </label>
  )

  return (
    <Modal title={t(isVM ? 'hosts.removeTitleVM' : 'hosts.removeTitle', { name: host.name })} onClose={onCancel} width={620}>
      <p className="small">{t(isVM ? 'hosts.removeIntroVM' : 'hosts.removeIntro')}</p>
      {isVM && item('vm', t('hosts.purgeVM'), t('hosts.purgeVMHint'))}
      {isVM && item('vm_disks', t('hosts.purgeVMDisks'), t('hosts.purgeVMDisksHint'), !purge.vm)}
      {item('restore_password', t('hosts.purgeRestorePassword'), t('hosts.purgeRestorePasswordHint'), isVM && purge.vm)}
      {item('service', t('hosts.purgeService'), t('hosts.purgeServiceHint'), isVM && purge.vm)}
      {item('data', t('hosts.purgeData'), t('hosts.purgeDataHint'), isVM && purge.vm)}
      {item('access', t('hosts.purgeAccess'), t('hosts.purgeAccessHint'), isVM && purge.vm)}
      {item(
        'user',
        t('hosts.purgeUser', { user: host.ssh_user }),
        host.ssh_user === 'root' ? t('hosts.purgeUserRoot') : t('hosts.purgeUserHint'),
        host.ssh_user === 'root' || (isVM && purge.vm),
      )}
      {isVM && purge.vm && <p className="small muted">{t('hosts.purgeVMOnly')}</p>}
      <p className="small muted">{t('hosts.removeUnreachable')}</p>
      <div className="row" style={{ gap: '0.5rem', marginTop: '0.75rem' }}>
        <Button
          danger
          type="primary"
          loading={busy}
          onClick={() => {
            setBusy(true)
            onConfirm(purge)
          }}
        >
          {t('hosts.delete')}
        </Button>
        <Button onClick={onCancel}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}

/**
 * Выбор профиля для раскатки по группе.
 *
 * Запуск отвечает номером задания, а не ждёт конца работы: обход группы
 * идёт минутами и переживает закрытую вкладку — смотреть за ним нужно в
 * «Заданиях», а не здесь.
 */
function ApplyProfileModal({
  group,
  hostCount,
  profiles,
  onClose,
  onStarted,
}: {
  group: string
  hostCount: number
  profiles: { id: number; name: string }[]
  onClose: () => void
  onStarted: (text: string, jobID: number) => void
}) {
  const { t } = useTranslation()
  const [profileID, setProfileID] = useState<number | null>(profiles[0]?.id ?? null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function start() {
    if (!profileID) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number; hosts: number }>('/hub/groups/apply-profile', {
        method: 'POST',
        body: { profile_id: profileID, group },
      })
      onStarted(t('hosts.applyProfileStarted', { count: res.hosts, job: res.job_id }), res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('hosts.applyProfileTitle', { group: group || t('hosts.groupNone'), count: hostCount })}
      onClose={onClose}
    >
      <p className="small muted">{t('hosts.applyProfileBody')}</p>
      <ErrorNote error={error} />
      <div className="col" style={{ gap: '0.4rem', marginBottom: '0.6rem' }}>
        {profiles.map((p) => (
          <label key={p.id} style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
            <input
              type="radio"
              name="profile"
              checked={profileID === p.id}
              onChange={() => setProfileID(p.id)}
            />
            {p.name}
          </label>
        ))}
      </div>
      <div className="row" style={{ gap: '0.5rem' }}>
        <Button type="primary" loading={busy} disabled={!profileID} onClick={() => void start()}>
          {t('hosts.applyProfileStart')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}

/**
 * Создание виртуальной машины на управляемом хосте.
 *
 * Хаб заранее выдаёт себе ключ и просит хост положить его в машину при
 * первом запуске — иначе новую машину пришлось бы открывать хабу руками.
 * Установка nkt на неё остаётся отдельным шагом: это обычный хост, и
 * решение «ставить ли» остаётся за оператором.
 */
function ProvisionVMModal({
  host,
  hubVersion,
  onClose,
  onStarted,
}: {
  host: HubHost
  // Версия хаба, с которой сравнивается версия на хосте. Может быть не
  // известна (старый ответ /auth/me) — тогда сравнивать нечего и
  // предупреждать не о чем.
  hubVersion?: string
  onClose: () => void
  onStarted: (text: string, jobID: number) => void
}) {
  const { t } = useTranslation()
  // Тот же ответ, что и на странице образов самого хоста: заодно
  // говорит, чем на нём машины вообще создавать. Проверять это здесь
  // важнее, чем там: отсюда оператор не видит того хоста и узнал бы о
  // нехватке только из провалившегося задания.
  const images = useApi<{
    catalog: { id: string; name: string }[]
    local: { id: string; downloaded: boolean }[]
    missing: { command: string; package: string; why: string }[] | null
  }>(`/hosts/${host.id}/vm/images`, 5_000)
  const [installingTools, setInstallingTools] = useState(false)

  const missingTools = images.data?.missing ?? []

  async function installTools() {
    setInstallingTools(true)
    setError(null)
    try {
      await api(`/hosts/${host.id}/vm/tools/install`, { method: 'POST' })
      // Задание идёт своим ходом; список инструментов перечитается сам
      // — опрос здесь для того и частый.
      images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setInstallingTools(false)
    }
  }
  const [name, setName] = useState('')
  const [imageID, setImageID] = useState('')
  const [user, setUser] = useState('deploy')
  const [sshKey, setSSHKey] = useState('')
  const [diskGB, setDiskGB] = useState(20)
  const [memoryMB, setMemoryMB] = useState(2048)
  const [vcpus, setVCPUs] = useState(2)
  // Пусто, пока не выбрали: по умолчанию берётся первая существующая
  // сеть хоста, а «default» вслепую ставить нельзя — на минимальной
  // установке libvirt её нет.
  const [network, setNetwork] = useState('')
  const [installNKT, setInstallNKT] = useState(true)
  // Сети того хоста, где создаётся машина: их список виден только ему.
  const nets = useApi<{ networks: { name: string; active: boolean }[] }>(`/hosts/${host.id}/vm/networks`, 60_000)
  const knownNets = nets.data?.networks ?? []
  const chosenNetwork = network || knownNets[0]?.name || 'default' 
  const [profileID, setProfileID] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Профили хаба — те же, что правятся в разделе «Профили» его машины.
  const profiles = useApi<{ profiles: { id: number; name: string }[] }>('/hosts/local/profiles')

  const downloaded = new Set((images.data?.local ?? []).filter((l) => l.downloaded).map((l) => l.id))

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/vm/provision', {
        method: 'POST',
        body: {
          host_id: host.id,
          install_nkt: installNKT,
          profile_id: profileID ?? 0,
          spec: {
            name,
            image_id: imageID,
            network: chosenNetwork,
            disk_gb: diskGB,
            memory_mb: memoryMB,
            vcpus,
            user,
            ssh_key: sshKey.trim(),
          },
        },
      })
      onStarted(t('hosts.newVMStarted', { name, job: res.job_id }), res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('hosts.newVMTitle', { host: host.name })} onClose={onClose} width={720}>
      <p className="small muted">{t('hosts.newVMBody')}</p>
      <ErrorNote error={error} />
      <ErrorNote error={images.error} />

      {/* Создание опирается на API самого хоста: на старой версии этих
          запросов там просто нет. Задание обновит его само, но сказать
          об этом заранее честнее, чем удивить лишними пятью минутами. */}
      {hubVersion !== undefined && isOutdated(host, hubVersion) && (
        <Banner kind="info">{t('hosts.newVMWillUpdate', { host: host.name })}</Banner>
      )}

      {missingTools.length > 0 && (
        <Banner kind="warn">
          <div className="col" style={{ gap: '0.4rem' }}>
            <span>{t('hosts.newVMToolsMissing', { host: host.name })}</span>
            <ul style={{ margin: 0, paddingLeft: '1.1rem' }}>
              {missingTools.map((tool) => (
                <li key={tool.command} className="small">
                  <span className="mono">{tool.command}</span> ({t('vmimages.toolPackage')}{' '}
                  <span className="mono">{tool.package}</span>)
                </li>
              ))}
            </ul>
            {/* Создание поставит их само нулевым шагом; кнопка — чтобы
                сделать это заранее и не ждать потом. */}
            <span>
              <Button size="small" loading={installingTools} onClick={() => void installTools()}>
                {t('vmimages.installToolsNow')}
              </Button>
            </span>
          </div>
        </Banner>
      )}

      <label style={{ marginBottom: '0.6rem' }}>
        {t('hosts.newVMImage')}
        <div className="col" style={{ gap: '0.25rem', marginTop: '0.25rem' }}>
          {(images.data?.catalog ?? []).map((img) => (
            <label key={img.id} style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
              <input type="radio" name="image" checked={imageID === img.id} onChange={() => setImageID(img.id)} />
              {img.name}
              {!downloaded.has(img.id) && <span className="small muted">{t('hosts.newVMWillDownload')}</span>}
            </label>
          ))}
        </div>
      </label>

      <div className="grid grid-2" style={{ marginBottom: '0.6rem' }}>
        <label>
          {t('hosts.newVMName')}
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-02" />
        </label>
        <label>
          {t('hosts.newVMUser')}
          <Input value={user} onChange={(e) => setUser(e.target.value)} />
        </label>
        <label>
          {t('hosts.newVMDisk')}
          <InputNumber value={diskGB} min={1} max={4096} onChange={(v) => setDiskGB(v ?? 20)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('hosts.newVMMemory')}
          <InputNumber value={memoryMB} min={256} step={256} onChange={(v) => setMemoryMB(v ?? 2048)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('hosts.newVMVcpus')}
          <InputNumber value={vcpus} min={1} max={256} onChange={(v) => setVCPUs(v ?? 2)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('hosts.newVMNetwork')}
          <Select
            value={chosenNetwork}
            onChange={(v: string) => setNetwork(v)}
            options={
              knownNets.length > 0
                ? knownNets.map((n) => ({
                    value: n.name,
                    label: n.active ? n.name : `${n.name} (${t('vmnet.inactive')})`,
                  }))
                : [{ value: 'default', label: t('vmimages.networkWillCreate') }]
            }
          />
        </label>
      </div>

      {/* Ключ хаба кладётся в машину всегда — им хаб и заходит. Этот
          нужен только тому, кто хочет подключаться к машине напрямую,
          мимо хаба. */}
      <label style={{ marginBottom: '0.6rem' }}>
        {t('hosts.newVMKey')}
        <Input.TextArea rows={3} value={sshKey} onChange={(e) => setSSHKey(e.target.value)} placeholder="ssh-ed25519 AAAA..." />
        <span className="small muted">{t('hosts.newVMKeyOptional')}</span>
      </label>

      {/* Что делать с машиной после её появления. Профиль применяет сам
          nkt на этой машине, поэтому без установки его выбрать нельзя —
          обещать применение было бы нечестно. */}
      <div className="col" style={{ gap: '0.3rem', marginBottom: '0.6rem' }}>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox
            checked={installNKT}
            onChange={(e) => {
              setInstallNKT(e.target.checked)
              if (!e.target.checked) setProfileID(null)
            }}
          />
          {t('hosts.newVMInstall')}
        </label>
        {(profiles.data?.profiles?.length ?? 0) > 0 && (
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
            {t('hosts.newVMProfile')}
            <Select
              size="small"
              style={{ minWidth: '12rem' }}
              disabled={!installNKT}
              value={profileID ?? 0}
              onChange={(v: number) => setProfileID(v || null)}
              options={[
                { value: 0, label: t('hosts.newVMProfileNone') },
                ...(profiles.data?.profiles ?? []).map((p) => ({ value: p.id, label: p.name })),
              ]}
            />
          </label>
        )}
      </div>

      <div className="row" style={{ gap: '0.5rem' }}>
        <Button type="primary" loading={busy} disabled={!name.trim() || !imageID} onClick={() => void start()}>
          {t('hosts.newVMStart')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}

/** Адрес ещё не известен: у только что созданной машины стоит заглушка,
 * пока она не получит настоящий у DHCP. */
function isAddrUnknown(h: HubHost): boolean {
  return h.id !== LOCAL_HOST_ID && (!h.addr || h.addr === '0.0.0.0')
}
