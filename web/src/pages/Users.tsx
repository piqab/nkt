import { useState } from 'react'
import { Button, Form, Input, Select, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Account, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge, formatDateTime, formatRelative } from '../components/ui'

/** Matches the minimum the API enforces, counted in characters. */
const MIN_LENGTH = 10

export default function Users({ me }: { me: Me }) {
  const { t } = useTranslation()
  const { data, error, loading, reload } = useApi<{ users: Account[] }>('/users', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  async function toggleDisabled(u: Account) {
    if (!window.confirm(t(u.disabled ? 'users.confirmEnable' : 'users.confirmDisable', { username: u.username }))) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'PATCH', body: { disabled: !u.disabled } })
      setNotice({
        kind: 'info',
        text: t(u.disabled ? 'users.enabledNotice' : 'users.disabledNotice', { username: u.username }),
      })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function toggleRole(u: Account) {
    const nextRole = u.role === 'admin' ? 'viewer' : 'admin'
    if (!window.confirm(t('users.confirmRoleChange', { username: u.username, role: nextRole }))) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'PATCH', body: { role: nextRole } })
      setNotice({ kind: 'info', text: t('users.roleChanged', { username: u.username, role: nextRole }) })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function remove(u: Account) {
    if (!window.confirm(t('users.confirmDelete', { username: u.username }))) return
    setBusy(u.username)
    setNotice(null)
    try {
      await api(`/users/${u.username}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('users.deletedNotice', { username: u.username }) })
      reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<Account> = [
    {
      title: t('users.colLogin'),
      key: 'username',
      render: (_, u) => (
        <>
          <strong>{u.username}</strong>
          {u.username === me.username && <span className="small muted">{t('users.you')}</span>}
        </>
      ),
    },
    { title: t('users.colRole'), dataIndex: 'role', key: 'role' },
    { title: t('users.colState'), key: 'state', render: (_, u) => <StateBadge state={u.disabled ? 'inactive' : 'active'} /> },
    { title: t('users.colCreated'), key: 'created_at', render: (_, u) => <span className="small nowrap">{formatDateTime(u.created_at)}</span> },
    {
      title: t('users.colLastLogin'),
      key: 'last_login_at',
      render: (_, u) => <span className="small nowrap">{u.last_login_at ? formatRelative(u.last_login_at) : t('users.never')}</span>,
    },
    {
      title: t('users.colActions'),
      key: 'actions',
      render: (_, u) => {
        const self = u.username === me.username
        return (
          <div className="row">
            <Button
              type="link"
              size="small"
              loading={busy === u.username}
              disabled={self}
              title={self ? t('users.cannotChangeOwnRole') : undefined}
              onClick={() => toggleRole(u)}
            >
              {u.role === 'admin' ? t('users.makeViewer') : t('users.makeAdmin')}
            </Button>
            <Button
              type="link"
              size="small"
              loading={busy === u.username}
              disabled={self && !u.disabled}
              title={self && !u.disabled ? t('users.cannotDisableSelf') : undefined}
              onClick={() => toggleDisabled(u)}
            >
              {u.disabled ? t('users.enable') : t('users.disable')}
            </Button>
            <Button
              danger
              type="link"
              size="small"
              loading={busy === u.username}
              disabled={self}
              title={self ? t('users.cannotDeleteSelf') : undefined}
              onClick={() => remove(u)}
            >
              {t('users.delete')}
            </Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('users.title')}
            <InfoHint>{t('users.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      <Card title={t('users.existingAccounts')}>
        {loading && !data ? (
          <Loading what={t('users.loadingAccounts')} />
        ) : (
          <div className="table-wrap">
            <Table<Account> dataSource={data?.users ?? []} columns={columns} rowKey="id" pagination={false} size="small" />
          </div>
        )}
      </Card>

      <CreateUserForm onCreated={reload} />
    </>
  )
}

type CreateUserValues = { username: string; password: string; role: 'admin' | 'viewer' }

function CreateUserForm({ onCreated }: { onCreated: () => void }) {
  const { t } = useTranslation()
  const [form] = Form.useForm<CreateUserValues>()
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const tooShort = password.length > 0 && [...password].length < MIN_LENGTH

  async function submit(values: CreateUserValues) {
    if (tooShort) return
    setBusy(true)
    setError(null)
    try {
      await api('/users', { method: 'POST', body: values })
      form.resetFields()
      setPassword('')
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title={
        <>
          {t('users.newAccount')}
          <InfoHint>{t('users.roleHint')}</InfoHint>
        </>
      }
    >
      <Form<CreateUserValues> form={form} layout="vertical" onFinish={submit} initialValues={{ role: 'viewer' }}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="username" label={t('users.login')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label={t('users.password')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '14rem' }}>
            <Input.Password autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
          </Form.Item>
          <Form.Item name="role" label={t('users.role')}>
            <Select
              options={[
                { value: 'viewer', label: 'viewer' },
                { value: 'admin', label: 'admin' },
              ]}
            />
          </Form.Item>
        </div>
        {tooShort && (
          <p className="small" style={{ color: 'var(--status-warning)', marginTop: '-0.5rem' }}>
            {t('users.passwordTooShort', { count: MIN_LENGTH })}
          </p>
        )}
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy} disabled={tooShort}>
            {t('users.create')}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
