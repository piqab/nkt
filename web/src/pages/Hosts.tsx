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
import { api, useApi } from '../api'
import type { HubHost, RenewEvent, RenewJobStatus, Severity } from '../types'
import { Banner, Card, ErrorNote, Loading, Modal, SEVERITIES, SEVERITY_LABEL, formatRelative } from '../components/ui'
import { checkForNewProblems, notificationsEnabled, requestNotificationPermission, setNotificationsEnabled, type NotifyState } from '../notifications'

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
        }
      } catch (err) {
        if (cancelled) return
        setJobStatus({ events: [], done: true, error: err instanceof Error ? err.message : String(err) })
        window.clearInterval(timer)
      }
    }

    void poll()
    timer = window.setInterval(poll, INSTALL_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [job, installHostId, reload])

  async function startInstall(host: HubHost) {
    setNotice(null)
    try {
      const res = await api<{ job: string }>(`/hub/hosts/${host.id}/install`, { method: 'POST' })
      setInstallHostId(host.id)
      setJobStatus(null)
      setJob(res.job)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
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

  /** GET /hub/export returns a file, not JSON-for-the-UI — bypasses the
   * api() helper (which always parses the body as JSON) and drives a
   * regular browser download instead, using the filename the server
   * suggested via Content-Disposition. */
  async function exportHosts(includeKey: boolean) {
    if (
      includeKey &&
      !window.confirm(
        'Включить ключ шифрования в файл экспорта?\n\n' +
          'Так секреты на новом хабе расшифруются сами при импорте (перешифруются его собственным ' +
          'ключом) — не нужно вручную переносить NKT_HUB_MASTER_KEY. Но пока ключ в файле, этого файла ' +
          'одного достаточно, чтобы расшифровать все секреты всех хостов — храните и передавайте его ' +
          'так же осторожно, как сами пароли, и удалите после того, как импортируете.',
      )
    ) {
      return
    }
    setNotice(null)
    try {
      const res = await fetch(`/api/hub/export${includeKey ? '?include_key=1' : ''}`, { credentials: 'same-origin' })
      if (!res.ok) {
        const payload = await res.json().catch(() => null)
        throw new Error(payload?.error ?? `Ошибка ${res.status}`)
      }
      const blob = await res.blob()
      const filename = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'nkt-hub-export.json'
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  async function importHosts(file: File) {
    if (
      !window.confirm(
        'Импортировать хосты из файла? Хосты добавятся к уже существующим (файл не заменяет и не ' +
          'сверяет дубликаты по имени/адресу). Секреты в файле расшифруются только если он экспортирован ' +
          'с тем же NKT_HUB_MASTER_KEY, что и у этого хаба.',
      )
    ) {
      return
    }
    setNotice(null)
    setImporting(true)
    try {
      const text = await file.text()
      const res = await fetch('/api/hub/import', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: text,
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
    const outdated = isOutdated(h, hubVersion)
    return (
      <div className="row">
        {h.status === 'online' && (
          <Button type="link" onClick={() => onSelect({ id: h.id, name: h.name })}>
            открыть
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
      render: (_, h) => (
        <span className="mono small">
          {h.ssh_user}@{h.addr}:{h.ssh_port}
        </span>
      ),
    },
    { title: 'Проблемы', key: 'problems', render: (_, h) => <ProblemsCell host={h} /> },
    { title: 'Архитектура', key: 'arch', render: (_, h) => <span className="small">{h.arch || '—'}</span> },
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
        h.ssh_user === 'root' ? <span className="small muted">—</span> : <SudoBadge status={h.sudo_status} />,
    },
    {
      title: 'Версия nkt',
      key: 'version',
      render: (_, h) => {
        const outdated = isOutdated(h, hubVersion)
        return (
          <span className="small mono">
            {h.nkt_version || '—'}
            {outdated && (
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
      render: (_, h) => <span className="small nowrap">{h.last_seen_at ? formatRelative(h.last_seen_at) : 'ни разу'}</span>,
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Хосты</h1>
          <p>
            Каждый хост — отдельная VPS с собственным nkt: хаб заливает на неё бинарник по SSH,
            поднимает как systemd-сервис и дальше проксирует к нему запросы, так что дашборд и
            управление ничем не отличаются от обычного nkt на одном хосте.
          </p>
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
            onDone={(name, authorizedKey, terminalEnabledChanged) => {
              const host = editingHost
              setEditingHost(null)
              reload()
              if (authorizedKey) setPubKeyInfo({ hostName: name, key: authorizedKey })
              // Saving the checkbox alone only updates the hub's own
              // record — nothing changes on the host itself until
              // nkt.env is rewritten and the service restarted, which is
              // exactly what a reinstall does. Only for a host already
              // past its first install: a brand new one goes through
              // that install for the first time via its own separate
              // flow, and one already installing must not get a second,
              // concurrent job racing the first.
              if (terminalEnabledChanged && host.status !== 'new' && host.status !== 'installing') {
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
        <Modal title={`Установка на ${installingHost?.name ?? 'хост'}`} onClose={closeJobModal}>
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
}

/**
 * Add-host form, or (with `initial` set) an edit form for an existing one.
 * Editing never requires re-entering the SSH secret — an empty secret field
 * leaves whatever is already stored untouched (see Manager.UpdateHost).
 * onDone receives the host's name, the freshly generated public key when
 * auth_kind is "generated", and whether terminal_enabled actually changed
 * — the caller uses that last one to auto-trigger a reinstall, since
 * saving the checkbox alone only updates the hub's own record and does
 * nothing on the host itself until nkt.env is rewritten and the service
 * restarted (see Manager.UpdateHost / install).
 */
function HostForm({
  initial,
  onDone,
}: {
  initial?: HubHost
  onDone: (name: string, generatedAuthorizedKey?: string, terminalEnabledChanged?: boolean) => void
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
      const body = {
        name: values.name,
        addr: values.addr,
        ssh_port: values.ssh_port,
        ssh_user: values.ssh_user,
        auth_kind: authKind,
        secret: values.secret ?? '',
        terminal_enabled: terminalEnabled,
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
      onDone(values.name, authorizedKey, terminalEnabledChanged)
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
      title="Добавить хост"
      subtitle="Хаб подключится по SSH, определит архитектуру, соберёт или возьмёт из кэша бинарник nkt и установит его как systemd-сервис"
    >
      {formEl}
    </Card>
  )
}
