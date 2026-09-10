import { useState } from 'react'
import { Button, Form, Input } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import { api } from '../api'
import { Banner } from './ui'

/** Matches the minimum the API enforces, counted in characters. */
const MIN_LENGTH = 10

/**
 * Changing one's own password requires the current one, so a forgotten session
 * left open on someone else's screen cannot be used to take the account over.
 *
 * На antd Form, а не на рукописной разметке: правила проверки живут рядом с
 * полем, ошибка показывается под ним и до отправки, а кнопка сама знает,
 * можно ли уже отправлять. Раньше то же самое делалось тремя состояниями и
 * условными надписями, и «слишком короткий» появлялся под одним полем,
 * «не совпадает» — под другим, каждый своим способом.
 */
export default function PasswordForm({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation()
  const [form] = Form.useForm<{ old: string; next: string; repeat: string }>()
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(values: { old: string; next: string }) {
    setBusy(true)
    setError(null)
    try {
      await api('/auth/password', {
        method: 'POST',
        body: { old_password: values.old, new_password: values.next },
      })
      // The server drops every session on success, so a fresh login is required.
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Form form={form} layout="vertical" onFinish={submit} requiredMark={false}>
      <p className="small secondary" style={{ marginTop: 0 }}>
        {t('passwordForm.sessionsEnd')}
      </p>

      {error && <Banner kind="error">{error}</Banner>}

      <Form.Item name="old" label={t('passwordForm.currentPassword')} rules={[{ required: true }]}>
        <Input.Password autoComplete="current-password" autoFocus />
      </Form.Item>

      <Form.Item
        name="next"
        label={t('passwordForm.newPassword')}
        rules={[
          { required: true },
          // Длина считается в символах, а не в байтах: тот же счёт, что и на
          // сервере, иначе кириллический пароль из десяти букв выглядел бы
          // достаточно длинным здесь и коротким там.
          {
            validator: (_, value: string) =>
              !value || [...value].length >= MIN_LENGTH
                ? Promise.resolve()
                : Promise.reject(new Error(t('passwordForm.tooShort', { count: MIN_LENGTH }))),
          },
        ]}
      >
        <Input.Password autoComplete="new-password" />
      </Form.Item>

      <Form.Item
        name="repeat"
        label={t('passwordForm.repeatPassword')}
        dependencies={['next']}
        rules={[
          { required: true },
          ({ getFieldValue }) => ({
            validator: (_, value: string) =>
              !value || value === getFieldValue('next')
                ? Promise.resolve()
                : Promise.reject(new Error(t('passwordForm.mismatch'))),
          }),
        ]}
      >
        <Input.Password autoComplete="new-password" />
      </Form.Item>

      <Button type="primary" htmlType="submit" loading={busy}>
        {busy ? t('passwordForm.changing') : t('passwordForm.submit')}
      </Button>

      <p className="small muted" style={{ marginBottom: 0, marginTop: '0.6rem' }}>
        <Trans i18nKey="passwordForm.forgot" components={{ code: <code className="mono" /> }} />
      </p>
    </Form>
  )
}
