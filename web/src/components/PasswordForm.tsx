import { useState, type FormEvent } from 'react'
import { api } from '../api'
import { Banner, Spinner } from './ui'

/** Matches the minimum the API enforces, counted in characters. */
const MIN_LENGTH = 10

/**
 * Changing one's own password requires the current one, so a forgotten session
 * left open on someone else's screen cannot be used to take the account over.
 */
export default function PasswordForm({ onDone }: { onDone: () => void }) {
  const [oldPassword, setOld] = useState('')
  const [newPassword, setNew] = useState('')
  const [repeat, setRepeat] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const tooShort = newPassword.length > 0 && [...newPassword].length < MIN_LENGTH
  const mismatch = repeat.length > 0 && newPassword !== repeat

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (tooShort || mismatch) return
    setBusy(true)
    setError(null)
    try {
      await api('/auth/password', {
        method: 'POST',
        body: { old_password: oldPassword, new_password: newPassword },
      })
      // The server drops every session on success, so a fresh login is required.
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <form className="col" onSubmit={submit}>
      <p className="small secondary" style={{ margin: 0 }}>
        После смены пароля все сессии завершаются — потребуется войти заново.
      </p>

      {error && <Banner kind="error">{error}</Banner>}

      <label>
        Текущий пароль
        <input
          type="password"
          value={oldPassword}
          onChange={(e) => setOld(e.target.value)}
          autoComplete="current-password"
          required
          autoFocus
        />
      </label>
      <label>
        Новый пароль
        <input
          type="password"
          value={newPassword}
          onChange={(e) => setNew(e.target.value)}
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
        Повторите новый пароль
        <input
          type="password"
          value={repeat}
          onChange={(e) => setRepeat(e.target.value)}
          autoComplete="new-password"
          required
        />
        {mismatch && (
          <span className="small" style={{ color: 'var(--status-warning)' }}>
            пароли не совпадают
          </span>
        )}
      </label>

      <div className="row">
        <button className="primary" type="submit" disabled={busy || tooShort || mismatch}>
          {busy && <Spinner />}
          {busy ? 'Меняю…' : 'Сменить пароль'}
        </button>
      </div>

      <p className="small muted" style={{ margin: 0 }}>
        Забыли пароль и не можете войти? На хосте выполните{' '}
        <code className="mono">sudo nkt passwd</code> — это задаст новый пароль администратора,
        не трогая накопленную историю.
      </p>
    </form>
  )
}
