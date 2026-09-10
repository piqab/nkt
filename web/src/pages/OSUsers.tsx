import { useState } from 'react'
import { Button, Checkbox, Form, Input, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import { DataTable } from '../components/DataTable'

interface OSUserKey {
  type: string
  comment?: string
  fingerprint?: string
}

interface OSUser {
  name: string
  uid: number
  home: string
  shell: string
  sudo: boolean
  keys?: OSUserKey[]
}

/**
 * Учётные записи самой операционной системы — не те, что заводятся в
 * разделе «Пользователи»: там учётки веб-интерфейса nkt, здесь люди,
 * которые входят на хост по SSH.
 *
 * Раздел решает одну задачу: дать человеку доступ, не выдавая пароль root
 * и не заходя на сервер вручную. Поэтому здесь только список с ключами и
 * форма «имя плюс ключ» — всё остальное (смена оболочки, группы, пароли)
 * делается в терминале теми, кому это действительно нужно.
 */
export default function OSUsers({ me }: { me: Me }) {
  const { t } = useTranslation()
  const users = useApi<{ users: OSUser[] }>('/os-users', 60_000)
  const [form] = Form.useForm<{ name: string; key: string; sudo: boolean }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)

  const canUse = me.is_admin && me.allow_mutations

  async function submit(values: { name: string; key: string; sudo?: boolean }) {
    setBusy(true)
    setError(null)
    setDone(null)
    try {
      await api('/os-users', {
        method: 'POST',
        body: { name: values.name.trim(), key: values.key.trim(), sudo: values.sudo ?? false },
      })
      setDone(t('osUsers.created', { name: values.name.trim() }))
      form.resetFields()
      await users.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const columns: TableColumnsType<OSUser> = [
    {
      title: t('osUsers.colName'),
      key: 'name',
      render: (_, u) => (
        <div className="col">
          <code className="mono">{u.name}</code>
          <span className="small muted">uid {u.uid}</span>
        </div>
      ),
    },
    {
      title: t('osUsers.colKeys'),
      key: 'keys',
      render: (_, u) =>
        u.keys && u.keys.length > 0 ? (
          <div className="col">
            {u.keys.map((k, i) => (
              <span key={i} className="small mono">
                {k.type} {k.fingerprint} {k.comment}
              </span>
            ))}
          </div>
        ) : (
          <span className="small muted">{t('osUsers.noKeys')}</span>
        ),
    },
    {
      title: t('osUsers.colSudo'),
      key: 'sudo',
      width: '8rem',
      render: (_, u) => (u.sudo ? <Tag color="orange">sudo</Tag> : <span className="small muted">—</span>),
    },
    {
      title: t('osUsers.colShell'),
      key: 'shell',
      render: (_, u) => <span className="small mono">{u.shell}</span>,
    },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('osUsers.title')}
            <InfoHint>{t('osUsers.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={users.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {done && <Banner kind="info">{done}</Banner>}

      {canUse && (
        <Card title={t('osUsers.addTitle')} subtitle={t('osUsers.addHint')}>
          {/* Проверки повторяют серверные (internal/control/osusers.go):
              имя по правилам useradd, ключ — строка authorized_keys. Смысл
              не в замене серверной проверки, а в том, чтобы не узнавать об
              опечатке после круговой поездки на хост. */}
          <Form form={form} layout="vertical" onFinish={submit} requiredMark={false}>
            <div className="filters">
              <Form.Item
                name="name"
                label={t('osUsers.fieldName')}
                rules={[
                  { required: true },
                  {
                    pattern: /^[a-z_][a-z0-9_-]{0,31}$/,
                    message: t('osUsers.nameRule'),
                  },
                ]}
                style={{ flex: 1, minWidth: '12rem' }}
              >
                <Input placeholder="deploy" />
              </Form.Item>
              <Form.Item name="sudo" valuePropName="checked" label=" ">
                <Checkbox>{t('osUsers.fieldSudo')}</Checkbox>
              </Form.Item>
            </div>
            <Form.Item
              name="key"
              label={t('osUsers.fieldKey')}
              rules={[
                { required: true },
                {
                  pattern: /^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-[a-z0-9@.-]+)\s+[A-Za-z0-9+/=]+(\s+.*)?$/,
                  message: t('osUsers.keyRule'),
                },
              ]}
            >
              <Input.TextArea
                className="mono"
                autoSize={{ minRows: 2, maxRows: 5 }}
                placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5… user@laptop"
                spellCheck={false}
              />
            </Form.Item>
            <Button type="primary" htmlType="submit" loading={busy}>
              {t('osUsers.add')}
            </Button>
          </Form>
        </Card>
      )}

      <Card title={t('osUsers.listTitle')} subtitle={t('osUsers.count', { count: users.data?.users.length ?? 0 })}>
        {users.loading && !users.data ? (
          <Loading what={t('osUsers.title')} />
        ) : (
          <div className="table-wrap">
            <DataTable<OSUser>               dataSource={users.data?.users ?? []}
              rowKey="name"
              columns={columns}
            />
          </div>
        )}
      </Card>
    </>
  )
}
