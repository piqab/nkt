import { useState, type FormEvent } from 'react'
import { api, useApi } from '../api'
import type { Account, Me } from '../types'
import {
  Banner,
  Card,
  ErrorNote,
  Loading,
  Spinner,
  StateBadge,
  formatDateTime,
  formatRelative,
} from '../components/ui'

/** Matches the minimum the API enforces, counted in characters. */
const MIN_LENGTH = 10

export default function Users({ me }: { me: Me }) {
  const { data, error, loading, reload } = useApi<{ users: Account[] }>('/users', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  async function toggleDisabled(u: Account) {
    const verb = u.disabled ? 'включить' : 'отключить'
    if (!window.confirm(`${verb === 'включить' ? 'Включить' : 'Отключить'} учётную запись ${u.username}?`)) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'PATCH', body: { disabled: !u.disabled } })
      setNotice({ kind: 'info', text: `${u.username}: учётная запись ${u.disabled ? 'включена' : 'отключена'}.` })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function toggleRole(u: Account) {
    const nextRole = u.role === 'admin' ? 'viewer' : 'admin'
    if (!window.confirm(`Сменить роль ${u.username} на ${nextRole}?`)) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'PATCH', body: { role: nextRole } })
      setNotice({ kind: 'info', text: `${u.username}: роль изменена на ${nextRole}.` })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function remove(u: Account) {
    if (!window.confirm(`Удалить учётную запись ${u.username}? Это необратимо.`)) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: `${u.username}: учётная запись удалена.` })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Учётные записи</h1>
          <p>
            Кто может входить в веб-интерфейс и с какой ролью. Пароли хранятся необратимо
            (argon2id) — сбросить забытый пароль можно только выпуском нового, командой{' '}
            <code className="mono">sudo nkt passwd &lt;логин&gt;</code> на самом хосте.
          </p>
        </div>
      </div>

      <ErrorNote error={error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      <Card title="Существующие учётные записи">
        {loading && !data ? (
          <Loading what="учётные записи" />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Логин</th>
                  <th>Роль</th>
                  <th>Состояние</th>
                  <th>Создана</th>
                  <th>Последний вход</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {data?.users.map((u) => {
                  const self = u.username === me.username
                  return (
                    <tr key={u.id}>
                      <td>
                        <strong>{u.username}</strong>
                        {self && <span className="small muted"> (это вы)</span>}
                      </td>
                      <td>{u.role}</td>
                      <td>
                        <StateBadge state={u.disabled ? 'inactive' : 'active'} />
                      </td>
                      <td className="small nowrap">{formatDateTime(u.created_at)}</td>
                      <td className="small nowrap">
                        {u.last_login_at ? formatRelative(u.last_login_at) : 'ни разу'}
                      </td>
                      <td className="nowrap">
                        <button
                          className="ghost"
                          disabled={busy === u.username || self}
                          title={self ? 'Нельзя изменить роль себе' : undefined}
                          onClick={() => toggleRole(u)}
                        >
                          {busy === u.username && <Spinner />}
                          {u.role === 'admin' ? 'сделать viewer' : 'сделать admin'}
                        </button>
                        <button
                          className="ghost"
                          disabled={busy === u.username || (self && !u.disabled)}
                          title={self && !u.disabled ? 'Нельзя отключить себя' : undefined}
                          onClick={() => toggleDisabled(u)}
                        >
                          {busy === u.username && <Spinner />}
                          {u.disabled ? 'включить' : 'отключить'}
                        </button>
                        <button
                          className="danger ghost"
                          disabled={busy === u.username || self}
                          title={self ? 'Нельзя удалить себя' : undefined}
                          onClick={() => remove(u)}
                        >
                          {busy === u.username && <Spinner />}
                          удалить
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <CreateUserForm onCreated={reload} />
    </>
  )
}

function CreateUserForm({ onCreated }: { onCreated: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<'admin' | 'viewer'>('viewer')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const tooShort = password.length > 0 && [...password].length < MIN_LENGTH

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (tooShort) return
    setBusy(true)
    setError(null)
    try {
      await api('/users', { method: 'POST', body: { username, password, role } })
      setUsername('')
      setPassword('')
      setRole('viewer')
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title="Новая учётная запись"
      subtitle="Роль viewer — только просмотр, admin — полное управление хостом"
    >
      <form className="col" onSubmit={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <label style={{ flex: 1, minWidth: '12rem' }}>
            Логин
            <input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="off"
              required
            />
          </label>
          <label style={{ flex: 1, minWidth: '14rem' }}>
            Пароль
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              required
            />
            {tooShort && (
              <span className="small" style={{ color: 'var(--status-warning)' }}>
                не короче {MIN_LENGTH} символов
              </span>
            )}
          </label>
          <label>
            Роль
            <select value={role} onChange={(e) => setRole(e.target.value as 'admin' | 'viewer')}>
              <option value="viewer">viewer</option>
              <option value="admin">admin</option>
            </select>
          </label>
        </div>
        <div>
          <button className="primary" type="submit" disabled={busy || tooShort}>
            {busy && <Spinner />}
            {busy ? 'Создаю…' : 'Создать'}
          </button>
        </div>
      </form>
    </Card>
  )
}
