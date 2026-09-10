import { useState } from 'react'
import { Button, Checkbox, Input, Table, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'

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
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [sudo, setSudo] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)

  const canUse = me.is_admin && me.allow_mutations

  async function submit() {
    setBusy(true)
    setError(null)
    setDone(null)
    try {
      await api('/os-users', { method: 'POST', body: { name: name.trim(), key: key.trim(), sudo } })
      setDone(t('osUsers.created', { name: name.trim() }))
      setName('')
      setKey('')
      setSudo(false)
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
          <div className="filters">
            <label style={{ flex: 1, minWidth: '12rem' }}>
              {t('osUsers.fieldName')}
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="deploy" />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              <Checkbox checked={sudo} onChange={(e) => setSudo(e.target.checked)} />
              {t('osUsers.fieldSudo')}
            </label>
          </div>
          <label className="small">
            {t('osUsers.fieldKey')}
            <Input.TextArea
              className="mono"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              autoSize={{ minRows: 2, maxRows: 5 }}
              placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5… user@laptop"
              spellCheck={false}
            />
          </label>
          <Button
            type="primary"
            style={{ marginTop: '0.6rem' }}
            loading={busy}
            disabled={!name.trim() || !key.trim()}
            onClick={submit}
          >
            {t('osUsers.add')}
          </Button>
        </Card>
      )}

      <Card title={t('osUsers.listTitle')} subtitle={t('osUsers.count', { count: users.data?.users.length ?? 0 })}>
        {users.loading && !users.data ? (
          <Loading what={t('osUsers.title')} />
        ) : (
          <div className="table-wrap">
            <Table<OSUser>
              dataSource={users.data?.users ?? []}
              rowKey="name"
              size="small"
              pagination={false}
              columns={columns}
            />
          </div>
        )}
      </Card>
    </>
  )
}
