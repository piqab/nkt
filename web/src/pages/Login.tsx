import { useState } from 'react'
import { Button, Form, Input } from 'antd'
import { api } from '../api'
import { Banner } from '../components/ui'

type LoginValues = { username: string; password: string }

export default function Login({ onSuccess }: { onSuccess: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(values: LoginValues) {
    setBusy(true)
    setError(null)
    try {
      await api('/auth/login', { method: 'POST', body: values })
      onSuccess()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-wrap">
      <Form<LoginValues> className="login-card" layout="vertical" onFinish={submit}>
        <div>
          <h1>NetKnownsThat</h1>
          <p className="secondary small" style={{ margin: '0.25rem 0 0' }}>
            Карта сетевых ресурсов, проверка конфигураций и управление сервисами хоста.
          </p>
        </div>

        {error && <Banner kind="error">{error}</Banner>}

        <Form.Item name="username" label="Логин" rules={[{ required: true }]}>
          <Input autoComplete="username" autoFocus />
        </Form.Item>
        <Form.Item name="password" label="Пароль" rules={[{ required: true }]}>
          <Input.Password autoComplete="current-password" />
        </Form.Item>

        <Form.Item style={{ marginBottom: '0.75rem' }}>
          <Button type="primary" htmlType="submit" loading={busy} block>
            {busy ? 'Проверяю…' : 'Войти'}
          </Button>
        </Form.Item>

        <p className="muted small" style={{ margin: 0 }}>
          Пароль администратора печатается в журнал сервера при первом запуске.
        </p>
      </Form>
    </div>
  )
}
