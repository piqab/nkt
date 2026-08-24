import { useState } from 'react'
import { Button, Form, Input, Select, Table, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Account, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge, formatDateTime, formatRelative } from '../components/ui'

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

  const columns: TableColumnsType<Account> = [
    {
      title: 'Логин',
      key: 'username',
      render: (_, u) => (
        <>
          <strong>{u.username}</strong>
          {u.username === me.username && <span className="small muted"> (это вы)</span>}
        </>
      ),
    },
    { title: 'Роль', dataIndex: 'role', key: 'role' },
    { title: 'Состояние', key: 'state', render: (_, u) => <StateBadge state={u.disabled ? 'inactive' : 'active'} /> },
    { title: 'Создана', key: 'created_at', render: (_, u) => <span className="small nowrap">{formatDateTime(u.created_at)}</span> },
    {
      title: 'Последний вход',
      key: 'last_login_at',
      render: (_, u) => <span className="small nowrap">{u.last_login_at ? formatRelative(u.last_login_at) : 'ни разу'}</span>,
    },
    {
      title: 'Действия',
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
              title={self ? 'Нельзя изменить роль себе' : undefined}
              onClick={() => toggleRole(u)}
            >
              {u.role === 'admin' ? 'сделать viewer' : 'сделать admin'}
            </Button>
            <Button
              type="link"
              size="small"
              loading={busy === u.username}
              disabled={self && !u.disabled}
              title={self && !u.disabled ? 'Нельзя отключить себя' : undefined}
              onClick={() => toggleDisabled(u)}
            >
              {u.disabled ? 'включить' : 'отключить'}
            </Button>
            <Button
              danger
              type="link"
              size="small"
              loading={busy === u.username}
              disabled={self}
              title={self ? 'Нельзя удалить себя' : undefined}
              onClick={() => remove(u)}
            >
              удалить
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
            Учётные записи
            <InfoHint>
              Кто может входить в веб-интерфейс и с какой ролью. Пароли хранятся необратимо
              (argon2id) — сбросить забытый пароль можно только выпуском нового, командой
              sudo nkt passwd &lt;логин&gt; на самом хосте.
            </InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      <Card title="Существующие учётные записи">
        {loading && !data ? (
          <Loading what="учётные записи" />
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
          Новая учётная запись
          <InfoHint>Роль viewer — только просмотр, admin — полное управление хостом</InfoHint>
        </>
      }
    >
      <Form<CreateUserValues> form={form} layout="vertical" onFinish={submit} initialValues={{ role: 'viewer' }}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="username" label="Логин" rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label="Пароль" rules={[{ required: true }]} style={{ flex: 1, minWidth: '14rem' }}>
            <Input.Password autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
          </Form.Item>
          <Form.Item name="role" label="Роль">
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
            Пароль не короче {MIN_LENGTH} символов
          </p>
        )}
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy} disabled={tooShort}>
            Создать
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
