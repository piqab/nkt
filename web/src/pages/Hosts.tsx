import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, useApi } from '../api'
import type { HubHost, RenewEvent, RenewJobStatus } from '../types'
import { Banner, Card, ErrorNote, Loading, Modal, Spinner, formatRelative } from '../components/ui'

/** How often to poll a running install job for new progress lines — same
 * cadence Certificates.tsx uses for certbot jobs. */
const INSTALL_POLL_MS = 800

const STATUS_LABEL: Record<HubHost['status'], string> = {
  new: 'новый',
  installing: 'установка…',
  online: 'подключён',
  error: 'ошибка',
}

const STATUS_TONE: Record<HubHost['status'], string> = {
  new: 'info',
  installing: 'info',
  online: 'ok',
  error: 'critical',
}

function HostStatusBadge({ status }: { status: HubHost['status'] }) {
  return (
    <span className={`badge sev-${STATUS_TONE[status]}`}>
      <span className="badge-dot" />
      {STATUS_LABEL[status]}
    </span>
  )
}

/**
 * Host registry and per-host dashboard entry point for a hub. Selecting an
 * online host hands its id up to the shell (App.tsx), which scopes every
 * other page's API calls to it — those pages are otherwise unmodified.
 */
export default function Hosts({ onSelect }: { onSelect: (host: { id: number; name: string }) => void }) {
  const { data: hosts, error, loading, reload } = useApi<HubHost[]>('/hub/hosts', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [installHostId, setInstallHostId] = useState<number | null>(null)
  const [job, setJob] = useState<string | null>(null)
  const [jobStatus, setJobStatus] = useState<RenewJobStatus | null>(null)
  const [editingHost, setEditingHost] = useState<HubHost | null>(null)

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
  }

  const installingHost = hosts?.find((h) => h.id === installHostId)

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
            <table>
              <thead>
                <tr>
                  <th>Имя</th>
                  <th>Адрес</th>
                  <th>Архитектура</th>
                  <th>Состояние</th>
                  <th>Версия nkt</th>
                  <th>Виден в сети</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {hosts.map((h) => (
                  <tr key={h.id}>
                    <td>
                      <strong>{h.name}</strong>
                    </td>
                    <td className="mono small">
                      {h.ssh_user}@{h.addr}:{h.ssh_port}
                    </td>
                    <td className="small">{h.arch || '—'}</td>
                    <td>
                      <HostStatusBadge status={h.status} />
                      {h.status === 'error' && h.error_msg && (
                        <div className="small" style={{ color: 'var(--status-critical)' }}>
                          {h.error_msg}
                        </div>
                      )}
                    </td>
                    <td className="small mono">{h.nkt_version || '—'}</td>
                    <td className="small nowrap">
                      {h.last_seen_at ? formatRelative(h.last_seen_at) : 'ни разу'}
                    </td>
                    <td className="nowrap">
                      {h.status === 'online' && (
                        <button className="ghost" onClick={() => onSelect({ id: h.id, name: h.name })}>
                          открыть
                        </button>
                      )}
                      <button
                        className="ghost"
                        disabled={h.status === 'installing'}
                        onClick={() => startInstall(h)}
                      >
                        {h.status === 'installing' && <Spinner />}
                        {h.status === 'new' ? 'установить' : 'переустановить'}
                      </button>
                      <button
                        className="ghost"
                        disabled={h.status === 'installing'}
                        onClick={() => setEditingHost(h)}
                      >
                        изменить
                      </button>
                      <button className="danger ghost" onClick={() => remove(h)}>
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <HostForm onDone={reload} />

      {editingHost && (
        <Modal title={`Изменить хост «${editingHost.name}»`} onClose={() => setEditingHost(null)}>
          <HostForm
            initial={editingHost}
            onDone={() => {
              setEditingHost(null)
              reload()
            }}
          />
        </Modal>
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
              <Spinner />
              Выполняется — можно закрыть окно, установка продолжится в фоне.
            </p>
          )}
        </Modal>
      )}
    </>
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

/**
 * Add-host form, or (with `initial` set) an edit form for an existing one.
 * Editing never requires re-entering the SSH secret — an empty secret field
 * leaves whatever is already stored untouched (see Manager.UpdateHost).
 */
function HostForm({ initial, onDone }: { initial?: HubHost; onDone: () => void }) {
  const editing = initial !== undefined
  const [name, setName] = useState(initial?.name ?? '')
  const [addr, setAddr] = useState(initial?.addr ?? '')
  const [sshPort, setSshPort] = useState(initial?.ssh_port ?? 22)
  const [sshUser, setSshUser] = useState(initial?.ssh_user ?? 'root')
  const [authKind, setAuthKind] = useState<'password' | 'key'>(initial?.ssh_auth_kind ?? 'key')
  const [secret, setSecret] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body = { name, addr, ssh_port: sshPort, ssh_user: sshUser, auth_kind: authKind, secret }
      if (editing) {
        await api(`/hub/hosts/${initial.id}`, { method: 'PATCH', body })
      } else {
        await api('/hub/hosts', { method: 'POST', body })
        setName('')
        setAddr('')
      }
      setSecret('')
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const secretLabel = editing
    ? authKind === 'key'
      ? 'Новый приватный ключ (PEM) — оставьте пустым, чтобы не менять'
      : 'Новый пароль — оставьте пустым, чтобы не менять'
    : authKind === 'key'
      ? 'Приватный ключ (PEM)'
      : 'Пароль'

  const form = (
    <form className="col" onSubmit={submit}>
      {error && <Banner kind="error">{error}</Banner>}
      <div className="filters">
        <label style={{ flex: 1, minWidth: '10rem' }}>
          Имя
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </label>
        <label style={{ flex: 1, minWidth: '10rem' }}>
          IP-адрес или имя хоста
          <input value={addr} onChange={(e) => setAddr(e.target.value)} required />
        </label>
        <label style={{ minWidth: '6rem' }}>
          SSH-порт
          <input
            type="number"
            min={1}
            max={65535}
            value={sshPort}
            onChange={(e) => setSshPort(Number(e.target.value))}
            required
          />
        </label>
        <label style={{ minWidth: '8rem' }}>
          SSH-пользователь
          <input value={sshUser} onChange={(e) => setSshUser(e.target.value)} required />
        </label>
        <label style={{ minWidth: '10rem' }}>
          Способ входа
          <select value={authKind} onChange={(e) => setAuthKind(e.target.value as 'password' | 'key')}>
            <option value="key">приватный ключ</option>
            <option value="password">пароль</option>
          </select>
        </label>
      </div>
      <label>
        {secretLabel}
        {authKind === 'key' ? (
          <textarea
            className="mono"
            rows={6}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            required={!editing}
          />
        ) : (
          <input
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            autoComplete="new-password"
            required={!editing}
          />
        )}
      </label>
      <div>
        <button className="primary" type="submit" disabled={busy}>
          {busy && <Spinner />}
          {busy ? 'Сохраняю…' : editing ? 'Сохранить' : 'Добавить хост'}
        </button>
      </div>
    </form>
  )

  if (editing) return form

  return (
    <Card
      title="Добавить хост"
      subtitle="Хаб подключится по SSH, определит архитектуру, соберёт или возьмёт из кэша бинарник nkt и установит его как systemd-сервис"
    >
      {form}
    </Card>
  )
}
