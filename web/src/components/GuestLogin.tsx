import { useState } from 'react'
import { Button, Checkbox, Form, Input, Tooltip } from 'antd'
import { CopyOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Modal, formatDateTime } from './ui'
import { useJobLauncher } from './useJobLauncher'

type GuestInfo = { user?: string; set: boolean; set_at?: string; set_by?: string }

/**
 * Строка «Вход» в окнах консоли и экрана гостя: какой логин, задан ли
 * через nkt пароль, «показать» (администратору, с записью в аудит) и
 * «задать пароль» (фоновым заданием внутри гостя).
 */
export function GuestLoginBar({ kind, name, canControl }: { kind: 'lxd' | 'vm'; name: string; canControl: boolean }) {
  const { t } = useTranslation()
  const info = useApi<GuestInfo>(`/guests/${kind}/${encodeURIComponent(name)}/credentials`)
  const [shown, setShown] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [setting, setSetting] = useState(false)
  const launcher = useJobLauncher(() => void info.reload())

  async function reveal() {
    setError(null)
    try {
      const r = await api<{ password: string }>(`/guests/${kind}/${encodeURIComponent(name)}/credentials/reveal`, { method: 'POST' })
      setShown(r.password)
      window.setTimeout(() => setShown(null), 60_000)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const d = info.data
  return (
    <div className="small" style={{ marginBottom: '0.5rem' }}>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        {d?.set ? (
          <>
            <span>
              {t('guestLogin.login')}: <strong className="mono">{d.user}</strong>
            </span>
            {shown ? (
              <span className="mono">
                {shown}{' '}
                <Tooltip title={t('guestLogin.copy')}>
                  <Button size="small" type="text" icon={<CopyOutlined />} onClick={() => void navigator.clipboard?.writeText(shown)} />
                </Tooltip>
              </span>
            ) : (
              <span className="muted">{t('guestLogin.passwordSet', { at: formatDateTime(d.set_at), by: d.set_by })}</span>
            )}
            {canControl && !shown && (
              <Button size="small" onClick={() => void reveal()}>
                {t('guestLogin.show')}
              </Button>
            )}
          </>
        ) : (
          <span className="muted">{t(`guestLogin.notSet.${kind}`)}</span>
        )}
        {canControl && (
          <Button size="small" onClick={() => setSetting(true)}>
            {d?.set ? t('guestLogin.change') : t('guestLogin.set')}
          </Button>
        )}
      </div>
      {error && <Banner kind="error">{error}</Banner>}
      {setting && (
        <SetPasswordModal
          kind={kind}
          name={name}
          defaultUser={d?.user ?? (kind === 'lxd' ? 'root' : '')}
          onClose={() => setSetting(false)}
          onSubmit={async (body) => {
            await launcher.start(`/guests/${kind}/${encodeURIComponent(name)}/password`, body)
            setSetting(false)
          }}
        />
      )}
      {launcher.modal}
    </div>
  )
}

/** Поля логина и пароля — общие для окна смены и форм создания. */
export function GuestPasswordFields({
  user,
  onUser,
  password,
  onPassword,
  generate,
  onGenerate,
  userPlaceholder,
}: {
  user: string
  onUser: (v: string) => void
  password: string
  onPassword: (v: string) => void
  generate: boolean
  onGenerate: (v: boolean) => void
  userPlaceholder?: string
}) {
  const { t } = useTranslation()
  return (
    <div className="row" style={{ gap: '0.75rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
      <Form.Item label={t('guestLogin.user')} style={{ marginBottom: 0 }}>
        <Input value={user} onChange={(e) => onUser(e.target.value.trim())} placeholder={userPlaceholder} style={{ width: '10rem' }} />
      </Form.Item>
      <Form.Item label={t('guestLogin.password')} style={{ marginBottom: 0 }}>
        <Input.Password value={password} onChange={(e) => onPassword(e.target.value)} disabled={generate} style={{ width: '14rem' }} autoComplete="new-password" />
      </Form.Item>
      <Checkbox checked={generate} onChange={(e) => onGenerate(e.target.checked)}>
        {t('guestLogin.generate')}
      </Checkbox>
    </div>
  )
}

function SetPasswordModal({
  kind,
  name,
  defaultUser,
  onClose,
  onSubmit,
}: {
  kind: 'lxd' | 'vm'
  name: string
  defaultUser: string
  onClose: () => void
  onSubmit: (body: { user: string; password: string; generate: boolean }) => Promise<void>
}) {
  const { t } = useTranslation()
  const [user, setUser] = useState(defaultUser)
  const [password, setPassword] = useState('')
  const [generate, setGenerate] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const ok = user !== '' && (generate || password.length >= 8)

  async function submit() {
    setBusy(true)
    setError(null)
    try {
      await onSubmit({ user, password, generate })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('guestLogin.setTitle', { name })} onClose={onClose}>
      <p className="small muted">{t(`guestLogin.setHint.${kind}`)}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <Form layout="vertical" onFinish={() => void submit()}>
        <GuestPasswordFields user={user} onUser={setUser} password={password} onPassword={setPassword} generate={generate} onGenerate={setGenerate} />
        <Button type="primary" htmlType="submit" disabled={!ok} loading={busy} style={{ marginTop: '0.75rem' }}>
          {t('guestLogin.apply')}
        </Button>
      </Form>
    </Modal>
  )
}
