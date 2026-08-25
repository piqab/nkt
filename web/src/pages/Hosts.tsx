import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Badge, Button, Checkbox, Form, Input, InputNumber, Select, Switch, Table, Tooltip, type TableColumnsType } from 'antd'
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  InfoCircleFilled,
  QuestionCircleOutlined,
  WarningFilled,
} from '@ant-design/icons'
import { api, ApiError, LOCAL_HOST_ID, useApi } from '../api'
import type { HubHost, RenewEvent, RenewJobStatus, Severity } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, SEVERITIES, SEVERITY_LABEL, formatRelative } from '../components/ui'
import { checkForNewProblems, notificationsEnabled, requestNotificationPermission, setNotificationsEnabled, type NotifyState } from '../notifications'
import { decryptWithPassword, encryptWithPassword, isPasswordEncrypted } from '../exportCrypto'

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

const STATUS_LABEL: Record<HubHost['status'], string> = {
  new: 'новый',
  installing: 'установка…',
  online: 'подключён',
  error: 'ошибка',
}

const STATUS_COLOR: Record<HubHost['status'], string> = {
  new: 'var(--text-muted)',
  installing: 'var(--text-muted)',
  online: 'var(--status-good)',
  error: 'var(--status-critical)',
}

const SUDO_LABEL: Record<NonNullable<HubHost['sudo_status']>, string> = {
  '': 'неизвестно',
  root: 'root',
  nopasswd: 'без пароля',
  password_required: 'нужен пароль',
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
  const s = status ?? ''
  return <Badge color={SUDO_COLOR[s]} text={SUDO_LABEL[s]} />
}

function HostStatusBadge({ status }: { status: HubHost['status'] }) {
  return <Badge color={STATUS_COLOR[status]} text={STATUS_LABEL[status]} />
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
  if (!host.tunnel_enabled) return <span className="small muted">—</span>
  if (host.channel === 'tunnel') {
    return (
      <Tooltip title="SSH сейчас недоступен — дашборд, терминал и переустановка/обновление идут через резервный канал">
        <Badge color="var(--status-warning)" text="через резервный канал" />
      </Tooltip>
    )
  }
  if (host.tunnel_connected) {
    return (
      <Tooltip title="Резервный канал подключён и готов, но сейчас не используется — SSH работает">
        <Badge color="var(--status-good)" text="резервный канал: подключён" />
      </Tooltip>
    )
  }
  return (
    <Tooltip title="Резервный канал включён в настройках, но хаб ещё не подключился к хосту">
      <Badge color="var(--text-muted)" text="резервный канал: не подключён" />
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
  if (host.reachable === undefined) {
    return (
      <span className="small muted row" style={{ gap: '0.3rem', flexWrap: 'nowrap' }}>
        <QuestionCircleOutlined /> неизвестно
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
            <CheckCircleFilled /> нет проблем
          </span>
        ) : (
          present.map((s) => (
            <Tooltip key={s} title={SEVERITY_LABEL[s]}>
              <span className="row small" style={{ gap: '0.25rem', flexWrap: 'nowrap' }}>
                {SEVERITY_ICON[s]} {findings[s]}
              </span>
            </Tooltip>
          ))
        )}
      </div>
      {host.reachable === false && (
        <span className="small" style={{ color: 'var(--status-critical)' }}>
          недоступен{host.last_polled_at ? ` (данные от ${formatRelative(host.last_polled_at)})` : ''}
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
  const { data: hosts, error, loading, reload } = useApi<HubHost[]>('/hub/hosts', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [installHostId, setInstallHostId] = useState<number | null>(null)
  const [job, setJob] = useState<string | null>(null)
  const [jobStatus, setJobStatus] = useState<RenewJobStatus | null>(null)
  const [editingHost, setEditingHost] = useState<HubHost | null>(null)
  const [pubKeyInfo, setPubKeyInfo] = useState<{ hostName: string; key: string } | null>(null)
  const [notifyOn, setNotifyOn] = useState(() => notificationsEnabled())
  const [busyServiceIds, setBusyServiceIds] = useState<Set<number>>(new Set())
  const [bulkBusy, setBulkBusy] = useState<'stop' | 'start' | null>(null)
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

  // The previous poll tick's per-host snapshot — comparing against it is
  // the dedup mechanism itself (see notifications.ts): a ref, not state,
  // since updating it must never itself trigger a render.
  const notifyStateRef = useRef<NotifyState>(new Map())
  useEffect(() => {
    if (hosts) checkForNewProblems(hosts, notifyStateRef.current)
  }, [hosts])

  async function toggleNotify(checked: boolean) {
    if (checked) {
      const granted = await requestNotificationPermission()
      if (!granted) {
        setNotice({ kind: 'error', text: 'Браузер не дал разрешение на уведомления.' })
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
        finishAutoOpen('не удалось получить статус установки')
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
  async function startInstall(host: HubHost, force = false): Promise<boolean> {
    setNotice(null)
    try {
      const res = await api<{ job: string }>(
        `/hub/hosts/${host.id}/install${force ? '?force=true' : ''}`,
        { method: 'POST' },
      )
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
        if (
          window.confirm(
            `На хосте уже есть nkt, установленный не этим хабом: ${detail}.\n\n` +
              'Продолжить всё равно? Бинарник, systemd-юнит и env будут перезаписаны, ' +
              'а пароль администратора синхронизирован с тем, что хранит хаб — ' +
              'если это чужая установка, доступ к ней текущим паролем будет потерян.',
          )
        ) {
          return startInstall(host, true)
        }
        return false
      }
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return false
    }
  }

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
    if (
      !window.confirm(
        `Убрать passwordless sudo у «${host.ssh_user}» на «${host.name}»?\n\n` +
          'Хаб не сможет заливать обновления или менять что-либо на хосте, требующее ' +
          'root, пока доступ не будет выдан заново (или SSH-пользователь не станет root).',
      )
    )
      return
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
    if (!window.confirm(`Остановить nkt на «${host.name}»? Хост станет недоступен через хаб, пока вы не запустите его снова.`)) return
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

  /** Runs setServiceRunning across every installed host in parallel and
   * reports one combined summary — a host mid-install ('new'/'installing')
   * has nothing to stop/start yet and is silently skipped rather than
   * counted as a failure. */
  async function bulkSetServiceRunning(running: boolean) {
    const targets = (hosts ?? []).filter((h) => h.status !== 'new' && h.status !== 'installing')
    if (targets.length === 0) return
    if (
      !window.confirm(
        `${running ? 'Запустить' : 'Остановить'} nkt сразу на всех хостах (${targets.length} шт.)?` +
          (running ? '' : ' Все они станут недоступны через хаб, пока вы не запустите их снова.'),
      )
    ) {
      return
    }
    setNotice(null)
    setBulkBusy(running ? 'start' : 'stop')
    try {
      const results = await Promise.all(targets.map((h) => setServiceRunning(h, running)))
      const failed = results.filter((e): e is string => e !== null).length
      if (failed > 0) {
        setNotice({ kind: 'error', text: `Не удалось на ${failed} из ${targets.length} хостов.` })
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
        throw new Error(payload?.error ?? `Ошибка ${res.status}`)
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
    if (
      !window.confirm(
        'Импортировать хосты из файла? Хосты добавятся к уже существующим (файл не заменяет и не ' +
          'сверяет дубликаты по имени/адресу). Секреты в файле расшифруются только если он экспортирован ' +
          'с тем же NKT_HUB_MASTER_KEY, что и у этого хаба.',
      )
    ) {
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
      if (!res.ok) throw new Error(payload?.error ?? `Ошибка ${res.status}`)
      const { imported, errors } = payload as { imported: number; errors?: string[] }
      setNotice({
        kind: errors?.length ? 'error' : 'info',
        text: `Импортировано хостов: ${imported}.${errors?.length ? ` Ошибки: ${errors.join('; ')}` : ''}`,
      })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setImporting(false)
    }
  }

  async function remove(host: HubHost) {
    if (!window.confirm(`Удалить хост «${host.name}» из хаба? Сам nkt на нём не удаляется.`)) return
    try {
      await api(`/hub/hosts/${host.id}`, { method: 'DELETE' })
      reload()
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
        <div className="row">
          <Button type="link" onClick={() => onSelect({ id: h.id, name: h.name })}>
            открыть
          </Button>
        </div>
      )
    }
    const outdated = isOutdated(h, hubVersion)
    return (
      <div className="row">
        {h.status === 'online' && (
          <Button type="link" loading={autoOpenHost?.id === h.id} onClick={() => openHost(h)}>
            {autoOpenHost?.id === h.id ? 'обновляю перед открытием…' : 'открыть'}
          </Button>
        )}
        <Button
          type={outdated ? 'primary' : 'default'}
          loading={h.status === 'installing'}
          onClick={() => startInstall(h)}
        >
          {h.status === 'new' ? 'установить' : outdated ? 'обновить' : 'переустановить'}
        </Button>
        {h.status === 'installing' && (
          <Button danger type="link" onClick={() => cancelInstall(h)}>
            отменить
          </Button>
        )}
        {h.status !== 'new' && (
          <Button type="link" onClick={() => openInstallLog(h)}>
            журнал установки
          </Button>
        )}
        {h.status !== 'new' && h.status !== 'installing' && (
          <>
            <Button type="link" loading={busyServiceIds.has(h.id)} onClick={() => startHost(h)}>
              запустить
            </Button>
            <Button danger type="link" loading={busyServiceIds.has(h.id)} onClick={() => stopHost(h)}>
              остановить
            </Button>
          </>
        )}
        <Button type="link" disabled={h.status === 'installing'} onClick={() => setEditingHost(h)}>
          изменить
        </Button>
        {h.ssh_auth_kind === 'key' && (
          <Button type="link" onClick={() => showPubKey(h)}>
            публичный ключ
          </Button>
        )}
        {h.sudo_status === 'nopasswd' && (
          <Button danger type="link" onClick={() => removeSudoAccess(h)}>
            снять NOPASSWD
          </Button>
        )}
        <Button danger type="link" onClick={() => remove(h)}>
          удалить
        </Button>
      </div>
    )
  }

  const columns: TableColumnsType<HubHost> = [
    { title: 'Имя', dataIndex: 'name', key: 'name', render: (name: string) => <strong>{name}</strong> },
    {
      title: 'Адрес',
      key: 'addr',
      render: (_, h) =>
        h.id === LOCAL_HOST_ID ? (
          <span className="small muted">эта машина — хаб сканирует сам себя, без SSH</span>
        ) : (
          <span className="mono small">
            {h.ssh_user}@{h.addr}:{h.ssh_port}
          </span>
        ),
    },
    { title: 'Проблемы', key: 'problems', render: (_, h) => <ProblemsCell host={h} /> },
    {
      title: 'Архитектура',
      key: 'arch',
      render: (_, h) => <span className="small">{h.id === LOCAL_HOST_ID ? '—' : h.arch || '—'}</span>,
    },
    {
      title: 'Состояние',
      key: 'status',
      render: (_, h) => (
        <>
          <HostStatusBadge status={h.status} />
          {h.status === 'error' && h.error_msg && (
            <div className="small" style={{ color: 'var(--status-critical)' }}>
              {h.error_msg}
            </div>
          )}
        </>
      ),
    },
    {
      title: 'Sudo',
      key: 'sudo',
      render: (_, h) =>
        h.ssh_user === 'root' || h.id === LOCAL_HOST_ID ? (
          <span className="small muted">—</span>
        ) : (
          <SudoBadge status={h.sudo_status} />
        ),
    },
    {
      title: 'Канал',
      key: 'channel',
      render: (_, h) => (h.id === LOCAL_HOST_ID ? <span className="small muted">—</span> : <TunnelChannelBadge host={h} />),
    },
    {
      title: 'Версия nkt',
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
                установлено {h.nkt_version} — обновление не применилось
              </div>
            )}
            {!stale && outdated && (
              <div className="small" style={{ color: 'var(--status-warning)' }}>
                на хабе: {hubVersion}
              </div>
            )}
          </span>
        )
      },
    },
    {
      title: 'Виден в сети',
      key: 'last_seen',
      render: (_, h) => (
        <span className="small nowrap">
          {h.id === LOCAL_HOST_ID ? '—' : h.last_seen_at ? formatRelative(h.last_seen_at) : 'ни разу'}
        </span>
      ),
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            Хосты
            <InfoHint>
              Каждый хост — отдельная VPS с собственным nkt: хаб заливает на неё бинарник по SSH,
              поднимает как systemd-сервис и дальше проксирует к нему запросы, так что дашборд и
              управление ничем не отличаются от обычного nkt на одном хосте.
            </InfoHint>
          </h1>
        </div>
        <div className="row" style={{ gap: '1rem' }}>
          <Button loading={bulkBusy === 'start'} disabled={bulkBusy === 'stop'} onClick={() => bulkSetServiceRunning(true)}>
            запустить все
          </Button>
          <Button
            danger
            loading={bulkBusy === 'stop'}
            disabled={bulkBusy === 'start'}
            onClick={() => bulkSetServiceRunning(false)}
          >
            остановить все
          </Button>
          <Button onClick={() => exportHosts(false)}>экспорт</Button>
          <Tooltip title="Для переноса на новый хаб без ручного копирования NKT_HUB_MASTER_KEY — файл станет самодостаточным для расшифровки, храните его так же осторожно, как пароли">
            <Button onClick={() => exportHosts(true)}>экспорт с ключом</Button>
          </Tooltip>
          <Button loading={importing} onClick={() => importInputRef.current?.click()}>
            импорт
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
          <Tooltip title="Уведомит браузером о новой critical/high-проблеме или о потере связи с хостом — пока эта вкладка открыта">
            <span className="row" style={{ gap: '0.4rem' }}>
              <Switch checked={notifyOn} onChange={toggleNotify} />
              Уведомлять о проблемах
            </span>
          </Tooltip>
        </div>
      </div>

      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      <ErrorNote error={error} />

      <Card title="Зарегистрированные хосты">
        {loading && !hosts ? (
          <Loading what="хосты" />
        ) : !hosts?.length ? (
          <p className="small muted">Хостов ещё нет — добавьте первый ниже.</p>
        ) : (
          <div className="table-wrap">
            <Table<HubHost>
              dataSource={hosts}
              columns={columns}
              rowKey="id"
              pagination={false}
              size="small"
              expandable={{
                expandedRowKeys: hosts.map((h) => h.id),
                expandIcon: () => null,
                rowExpandable: () => true,
                expandedRowRender: renderActions,
              }}
            />
          </div>
        )}
      </Card>

      <HostForm
        onDone={(name, authorizedKey) => {
          reload()
          if (authorizedKey) setPubKeyInfo({ hostName: name, key: authorizedKey })
        }}
      />

      {editingHost && (
        <Modal title={`Изменить хост «${editingHost.name}»`} onClose={() => setEditingHost(null)}>
          <HostForm
            initial={editingHost}
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

      {pubKeyInfo && (
        <PublicKeyModal
          hostName={pubKeyInfo.hostName}
          authorizedKey={pubKeyInfo.key}
          onClose={() => setPubKeyInfo(null)}
        />
      )}

      {job && (
        <Modal title={`Установка на ${installingHost?.name ?? 'хост'}`} onClose={closeJobModal} maskClosable={false}>
          <InstallLog events={jobStatus?.events ?? []} />
          {jobStatus?.done ? (
            <Banner kind={jobStatus.error ? 'error' : 'info'}>
              {jobStatus.error ? `Ошибка: ${jobStatus.error}` : 'Готово.'}
            </Banner>
          ) : (
            <p className="small muted row" style={{ alignItems: 'center', marginBottom: 0 }}>
              выполняется — можно закрыть окно, установка продолжится в фоне.
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
    <Modal title={`Публичный ключ для «${hostName}»`} onClose={onClose}>
      <p className="small muted">
        Добавьте эту строку в <code className="mono">~/.ssh/authorized_keys</code> на самом
        хосте (пользователю, указанному при добавлении), затем нажмите «установить». Приватная
        половина ключа хранится только на хабе и никуда больше не передаётся.
      </p>
      <pre className="diff mono" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
        {authorizedKey}
      </pre>
      <div>
        <Button onClick={copy}>{copied ? 'скопировано' : 'скопировать'}</Button>
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
  const [password, setPassword] = useState('')

  function download() {
    if (!password) {
      if (
        !window.confirm(
          'Скачать БЕЗ шифрования?\n\n' +
            'Файл экспорта — открытый JSON с ключом шифрования хаба и SSH-/admin-секретами всех ' +
            'хостов внутри: у кого файл — у того и они. Без пароля любой, кто его получит, читает ' +
            'всё напрямую.',
        )
      ) {
        return
      }
      onDownload(undefined)
      return
    }
    onDownload(password)
  }

  return (
    <Modal title="Экспорт с ключом" onClose={onClose}>
      <p className="small muted">
        Этот файл несёт ключ шифрования хаба и SSH-/admin-секреты всех хостов открытым текстом —
        того, кто им завладеет, достаточно, чтобы расшифровать всё. <strong>Настоятельно
        рекомендуется</strong> зашифровать файл паролем прямо сейчас, а не хранить его как есть.
        Пароль нигде не сохраняется — потеряете его, файл станет бесполезен.
      </p>
      <Form layout="vertical" onFinish={download}>
        <Form.Item label="Пароль для шифрования файла (необязательно, но рекомендуется)">
          <Input.Password
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            autoComplete="new-password"
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {password ? 'Скачать зашифрованным' : 'Скачать'}
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
    <Modal title="Файл зашифрован паролем" onClose={onClose}>
      <p className="small muted">
        «{fileName}» зашифрован паролем — введите его, чтобы расшифровать и импортировать хосты.
      </p>
      {error && <Banner kind="error">{error}</Banner>}
      <Form layout="vertical" onFinish={submit}>
        <Form.Item label="Пароль">
          <Input.Password
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            autoComplete="current-password"
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy} disabled={!password}>
            Расшифровать и импортировать
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}

/** Auto-scrolling live log of an install job's progress — same shape as
 * Certificates.tsx's RenewLog. */
function InstallLog({ events }: { events: RenewEvent[] }) {
  const preRef = useRef<HTMLPreElement>(null)

  useEffect(() => {
    const el = preRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [events.length])

  if (events.length === 0) {
    return <p className="small muted">Начинаю…</p>
  }

  return (
    <pre ref={preRef} className="diff" style={{ maxHeight: '22rem' }}>
      {events.map((e) => `[${new Date(e.time).toLocaleTimeString('ru-RU')}] ${e.text}`).join('\n')}
    </pre>
  )
}

type AuthKind = 'generated' | 'password' | 'key'

const AUTH_KIND_OPTIONS: { value: AuthKind; label: string }[] = [
  { value: 'generated', label: 'хаб сгенерирует ключ (рекомендуется)' },
  { value: 'key', label: 'свой приватный ключ' },
  { value: 'password', label: 'пароль' },
]

type HostFormValues = {
  name: string
  addr: string
  ssh_port: number
  ssh_user: string
  secret?: string
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
  onDone,
}: {
  initial?: HubHost
  onDone: (
    name: string,
    generatedAuthorizedKey?: string,
    terminalEnabledChanged?: boolean,
    tunnelEnabledChanged?: boolean,
  ) => void
}) {
  const editing = initial !== undefined
  const [form] = Form.useForm<HostFormValues>()
  // New hosts default to a hub-generated key — the operator's own private
  // key never has to be pasted anywhere for the common case. Editing
  // defaults to whatever the host already uses, since switching it is an
  // explicit choice, not the default action of opening the form.
  const [authKind, setAuthKind] = useState<AuthKind>(initial?.ssh_auth_kind ?? 'generated')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

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
        auth_kind: authKind,
        secret: values.secret ?? '',
        terminal_enabled: terminalEnabled,
        tunnel_enabled: tunnelEnabled,
      }
      let authorizedKey: string | undefined
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
        form.setFieldsValue({ name: '', addr: '' })
      }
      form.setFieldsValue({ secret: '' })
      const terminalEnabledChanged = editing && terminalEnabled !== (initial.terminal_enabled ?? false)
      const tunnelEnabledChanged = editing && tunnelEnabled !== (initial.tunnel_enabled ?? false)
      onDone(values.name, authorizedKey, terminalEnabledChanged, tunnelEnabledChanged)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const secretLabel =
    authKind === 'key'
      ? editing
        ? 'Новый приватный ключ (PEM) — оставьте пустым, чтобы не менять'
        : 'Приватный ключ (PEM)'
      : editing
        ? 'Новый пароль — оставьте пустым, чтобы не менять'
        : 'Пароль'

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
        terminal_enabled: initial?.terminal_enabled ?? false,
        // On by default for a new host (unlike terminal_enabled): unlike a
        // root shell in the browser, the fallback channel only ever kicks
        // in once SSH itself is already unreachable, so there is no extra
        // exposure from having it ready. Editing an existing host still
        // defaults to whatever it already has.
        tunnel_enabled: initial?.tunnel_enabled ?? true,
      }}
    >
      {error && <Banner kind="error">{error}</Banner>}
      <div className="filters">
        <Form.Item name="name" label="Имя" rules={[{ required: true }]} style={{ flex: 1, minWidth: '10rem' }}>
          <Input />
        </Form.Item>
        <Form.Item
          name="addr"
          label="IP-адрес или имя хоста"
          rules={[{ required: true }]}
          style={{ flex: 1, minWidth: '10rem' }}
        >
          <Input />
        </Form.Item>
        <Form.Item name="ssh_port" label="SSH-порт" rules={[{ required: true }]} style={{ minWidth: '6rem' }}>
          <InputNumber min={1} max={65535} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="ssh_user" label="SSH-пользователь" rules={[{ required: true }]} style={{ minWidth: '8rem' }}>
          <Input />
        </Form.Item>
        <Form.Item label="Способ входа" style={{ minWidth: '14rem' }}>
          <Select<AuthKind> value={authKind} onChange={setAuthKind} options={AUTH_KIND_OPTIONS} />
        </Form.Item>
      </div>
      {authKind === 'generated' ? (
        <p className="small muted">
          Приватный ключ никуда вставлять не нужно — хаб сгенерирует пару сам и после
          сохранения покажет публичную часть, которую нужно добавить в{' '}
          <code className="mono">~/.ssh/authorized_keys</code> на хосте.
          {editing && ' Старый способ входа этого хоста будет заменён на новый ключ.'}
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
      <Form.Item name="terminal_enabled" valuePropName="checked" style={{ marginBottom: '0.4rem' }}>
        <Checkbox>
          включить веб-терминал на хосте
          <div className="small muted" style={{ fontWeight: 400 }}>
            Полноценный root-shell прямо в браузере (раздел «Терминал» на самом хосте) —
            выключено по умолчанию.{' '}
            {editing
              ? 'Изменение этой настройки сразу переустановит nkt на хосте (если он уже установлен), чтобы применить её.'
              : 'Применится при первой установке этого хоста.'}
          </div>
        </Checkbox>
      </Form.Item>
      <Form.Item name="tunnel_enabled" valuePropName="checked" style={{ marginBottom: '0.4rem' }}>
        <Checkbox>
          резервный канал связи на случай недоступности SSH
          <div className="small muted" style={{ fontWeight: 400 }}>
            Хаб держит отдельное защищённое соединение к хосту (порт 8078 по умолчанию —
            NKT_HUB_TUNNEL_PORT на хабе), чтобы дашборд, терминал и «переустановить»/«обновить»
            продолжали работать, даже если SSH к нему перестанет отвечать. Настраивать адрес
            самого хаба не нужно — хаб дозванивается так же, как и по SSH, тем же адресом хоста.
            Включено по умолчанию для новых хостов; не подменяет SSH для самой первой установки
            (тогда ещё нечему было раньше подключиться).{' '}
            {editing
              ? 'Изменение этой настройки сразу переустановит nkt на хосте (если он уже установлен), чтобы применить её.'
              : 'Применится при первой установке этого хоста.'}
          </div>
        </Checkbox>
      </Form.Item>
      <Form.Item style={{ marginBottom: 0 }}>
        <Button type="primary" htmlType="submit" loading={busy}>
          {editing ? 'Сохранить' : 'Добавить хост'}
        </Button>
      </Form.Item>
    </Form>
  )

  if (editing) return formEl

  return (
    <Card
      title={
        <>
          Добавить хост
          <InfoHint>
            Хаб подключится по SSH, определит архитектуру, соберёт или возьмёт из кэша бинарник nkt и
            установит его как systemd-сервис
          </InfoHint>
        </>
      }
    >
      {formEl}
    </Card>
  )
}
