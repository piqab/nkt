import { useState } from 'react'
import { Button, Form, Input } from 'antd'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import { Banner } from '../components/ui'
import { useLang } from '../hooks/useLang'
import type { Lang } from '../i18n'

type LoginValues = { username: string; password: string }

export default function Login({ onSuccess }: { onSuccess: () => void }) {
  const { t } = useTranslation()
  const [lang, setLang] = useLang()
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
        <div className="row spread" style={{ alignItems: 'flex-start' }}>
          <div>
            <h1>NetKnownsThat</h1>
            <p className="secondary small" style={{ margin: '0.25rem 0 0' }}>
              {t('login.subtitle')}
            </p>
          </div>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
            <select
              value={lang}
              onChange={(e) => setLang(e.target.value as Lang)}
              className="small"
              aria-label={t('app.language')}
            >
              <option value="ru">Русский</option>
              <option value="en">English</option>
            </select>
          </label>
        </div>

        {error && <Banner kind="error">{error}</Banner>}

        <Form.Item name="username" label={t('login.username')} rules={[{ required: true }]}>
          <Input autoComplete="username" autoFocus />
        </Form.Item>
        <Form.Item name="password" label={t('login.password')} rules={[{ required: true }]}>
          <Input.Password autoComplete="current-password" />
        </Form.Item>

        <Form.Item style={{ marginBottom: '0.75rem' }}>
          <Button type="primary" htmlType="submit" loading={busy} block>
            {busy ? t('login.checking') : t('login.signIn')}
          </Button>
        </Form.Item>

        <p className="muted small" style={{ margin: 0 }}>
          {t('login.adminPasswordNote')}
        </p>
      </Form>
    </div>
  )
}
