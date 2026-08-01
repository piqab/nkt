import { useState, type FormEvent } from 'react'
import { api } from '../api'
import { Banner, Spinner } from '../components/ui'

export default function Login({ onSuccess }: { onSuccess: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api('/auth/login', { method: 'POST', body: { username, password } })
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <div>
          <h1>NetKnownsThat</h1>
          <p className="secondary small" style={{ margin: '0.25rem 0 0' }}>
            Карта сетевых ресурсов, проверка конфигураций и управление сервисами хоста.
          </p>
        </div>

        {error && <Banner kind="error">{error}</Banner>}

        <label>
          Логин
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
          />
        </label>
        <label>
          Пароль
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        <button className="primary" type="submit" disabled={busy}>
          {busy && <Spinner />}
          {busy ? 'Проверяю…' : 'Войти'}
        </button>

        <p className="muted small" style={{ margin: 0 }}>
          Пароль администратора печатается в журнал сервера при первом запуске.
        </p>
      </form>
    </div>
  )
}
