import { useMemo, useState } from 'react'
import { Button, Checkbox, Input, Select, Space, Tag, Tooltip, type TableColumnsType } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { TitleHelp } from '../components/Docs'
import { RowAction } from '../components/RowAction'
import { confirmWithOption } from '../components/confirm'
import { unifiedDiff } from '../components/textDiff'
import { msg, tx, type Msg } from '../msg'

interface OSUserKey {
  type: string
  comment?: string
  fingerprint?: string
  id?: string
}

interface OSUser {
  name: string
  uid: number
  home: string
  shell: string
  sudo: boolean
  keys?: OSUserKey[]
  groups: string[]
  nkt_sudo?: boolean
  hub_user?: boolean
}

interface OSGroup {
  name: string
  gid: number
  system: boolean
}

// Часто нужные группы — сверху окна, если такие есть на хосте.
const COMMON_GROUPS = ['docker', 'sudo', 'wheel', 'adm', 'systemd-journal', 'lxd', 'libvirt', 'www-data']
const SUDO_GROUPS = ['sudo', 'wheel', 'admin']
const NAME_RE = /^[a-z_][a-z0-9_-]{0,31}$/
const KEY_RE = /^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-[a-z0-9@.-]+)\s+[A-Za-z0-9+/=]+(\s+.*)?$/

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/**
 * Учётные записи самой операционной системы — не те, что заводятся в
 * разделе «Пользователи»: там учётки веб-интерфейса nkt, здесь люди,
 * которые входят на хост по SSH.
 *
 * Заведение и правка — в одном окне: ключи, sudo, оболочка, группы
 * галочками; перед записью — дифф. Пользователя, под которым входит хаб,
 * удалить нельзя, а отрезать ему ключ или sudo — только с подтверждением.
 */
export default function OSUsers({ me }: { me: Me }) {
  const { t } = useTranslation()
  const users = useApi<{ users: OSUser[] }>('/os-users', 60_000)
  const [editing, setEditing] = useState<OSUser | 'new' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<Msg | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)

  const canUse = me.is_admin && me.allow_mutations

  async function remove(u: OSUser) {
    const res = await confirmWithOption(t('osUsers.deleteConfirm', { name: u.name }), t('osUsers.deleteHome', { home: u.home }), {
      title: t('osUsers.deleteTitle'),
      okText: t('osUsers.delete'),
      optionHint: t('osUsers.deleteHomeHint'),
    })
    if (!res) return
    setDeleting(u.name)
    setError(null)
    setDone(null)
    try {
      await api(`/os-users/${encodeURIComponent(u.name)}${res.checked ? '?home=1' : ''}`, { method: 'DELETE' })
      setDone(tx('osUsers.deleted', { name: u.name }))
      await users.reload()
    } catch (err) {
      setError(errText(err))
    } finally {
      setDeleting(null)
    }
  }

  const columns: TableColumnsType<OSUser> = [
    {
      title: t('osUsers.colName'),
      key: 'name',
      render: (_, u) => (
        <div className="col">
          <span>
            <code className="mono">{u.name}</code>
            {u.hub_user && (
              <Tooltip title={t('osUsers.hubUserHint')}>
                <Tag color="blue" style={{ marginLeft: '0.4rem' }}>
                  {t('osUsers.hubUser')}
                </Tag>
              </Tooltip>
            )}
          </span>
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
      title: t('osUsers.colGroups'),
      key: 'groups',
      render: (_, u) =>
        u.groups.length > 0 ? (
          <div className="tag-row">
            {u.groups.map((g) => (
              <Tag key={g} color={g === 'docker' ? 'orange' : SUDO_GROUPS.includes(g) ? 'red' : undefined}>
                {g}
              </Tag>
            ))}
          </div>
        ) : (
          <span className="small muted">—</span>
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
  if (canUse) {
    columns.push({
      title: '',
      key: 'actions',
      width: '6rem',
      render: (_, u) => (
        <Space size={0}>
          <RowAction action="edit" label={t('osUsers.edit')} onClick={() => setEditing(u)} />
          <RowAction
            action="delete"
            danger
            label={u.uid === 0 ? t('osUsers.noDeleteRoot') : u.hub_user ? t('osUsers.noDeleteHub') : t('osUsers.delete')}
            disabled={u.uid === 0 || !!u.hub_user}
            loading={deleting === u.name}
            onClick={() => void remove(u)}
          />
        </Space>
      ),
    })
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('osUsers.title')}
            <TitleHelp>{t('osUsers.hint')}</TitleHelp>
          </h1>
        </div>
        {canUse && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing('new')}>
            {t('osUsers.addTitle')}
          </Button>
        )}
      </div>

      <ErrorNote error={users.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {done && <Banner kind="info">{msg(done)}</Banner>}

      <Card title={t('osUsers.listTitle')} subtitle={t('osUsers.count', { count: users.data?.users.length ?? 0 })}>
        {users.loading && !users.data ? (
          <Loading what={t('osUsers.title')} />
        ) : (
          <div className="table-wrap">
            <DataTable<OSUser> dataSource={users.data?.users ?? []} rowKey="name" columns={columns} />
          </div>
        )}
      </Card>

      {editing && (
        <OSUserModal
          user={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={async (msg) => {
            setEditing(null)
            setError(null)
            setDone(msg)
            await users.reload()
          }}
        />
      )}
    </>
  )
}

interface Draft {
  name: string
  shell: string
  sudo: boolean
  groups: string[]
  removeKeys: string[]
  addKeys: string
}

/** Окно заведения и правки: всё, что меняется, — в диффе перед записью. */
function OSUserModal({ user, onClose, onSaved }: { user: OSUser | null; onClose: () => void; onSaved: (message: Msg) => Promise<void> }) {
  const { t } = useTranslation()
  const catalog = useApi<{ groups: OSGroup[]; shells: string[] }>('/os-users/groups')
  const initial: Draft = useMemo(
    () => ({
      name: user?.name ?? '',
      shell: user?.shell ?? '/bin/bash',
      sudo: !!user?.nkt_sudo,
      groups: [...(user?.groups ?? [])].sort(),
      removeKeys: [],
      addKeys: '',
    }),
    [user],
  )
  const [d, setD] = useState<Draft>(initial)
  const [filter, setFilter] = useState('')
  const [confirmHub, setConfirmHub] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const groups = catalog.data?.groups ?? []
  const shells = catalog.data?.shells ?? []
  const has = (g: string) => groups.some((x) => x.name === g)
  const common = COMMON_GROUPS.filter(has)
  // Своя основная группа пользователя дополнительной не бывает.
  const others = groups.filter((g) => !COMMON_GROUPS.includes(g.name) && g.name !== d.name && (filter === '' || g.name.includes(filter.trim())))

  const newKeys = d.addKeys
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
  const badKey = newKeys.find((k) => !KEY_RE.test(k))
  const nameBad = !user && !NAME_RE.test(d.name)
  const keepKeys = (user?.keys ?? []).filter((k) => !k.id || !d.removeKeys.includes(k.id))

  const describe = (x: Draft, keys: string[]) =>
    [
      `${t('osUsers.colName')}: ${x.name}`,
      `${t('osUsers.colShell')}: ${x.shell}`,
      `${t('osUsers.fieldSudo')}: ${x.sudo ? t('osUsers.yes') : t('osUsers.no')}`,
      `${t('osUsers.colGroups')}: ${[...x.groups].sort().join(', ') || '—'}`,
      ...keys.map((k) => `${t('osUsers.keyLine')}: ${k}`),
    ].join('\n') + '\n'
  const keyText = (k: OSUserKey) => `${k.type} ${k.fingerprint ?? ''} ${k.comment ?? ''}`.trim()
  const before = user ? describe(initial, (user.keys ?? []).map(keyText)) : ''
  const after = describe(d, [...keepKeys.map(keyText), ...newKeys.map((k) => k.replace(/^(\S+)\s+(\S{8})\S*(\S{8})(.*)$/, '$1 $2…$3$4'))])
  const diff = unifiedDiff(before, after, t('tokens.saved'), t('tokens.draft'))
  const changed = user ? before !== after : true

  // Правка задевает вход хаба: его ключ, sudo nkt, группа sudo.
  const hubTouched =
    !!user?.hub_user &&
    (d.removeKeys.length > 0 ||
      (user.nkt_sudo && !d.sudo) ||
      SUDO_GROUPS.some((g) => user.groups.includes(g) && !d.groups.includes(g)))

  const toggleGroup = (g: string, on: boolean) =>
    setD({ ...d, groups: on ? [...d.groups, g].sort() : d.groups.filter((x) => x !== g) })

  async function save() {
    setBusy(true)
    setError(null)
    try {
      if (!user) {
        await api('/os-users', {
          method: 'POST',
          body: { name: d.name.trim(), key: newKeys[0] ?? '', sudo: d.sudo, groups: d.groups, shell: d.shell },
        })
        if (newKeys.length > 1) {
          await api(`/os-users/${encodeURIComponent(d.name.trim())}`, { method: 'PATCH', body: { add_keys: newKeys.slice(1) } })
        }
        await onSaved(tx('osUsers.created', { name: d.name.trim() }))
        return
      }
      const body: Record<string, unknown> = { confirm_hub: confirmHub }
      if (initial.groups.join(',') !== [...d.groups].sort().join(',')) body.groups = d.groups
      if (initial.shell !== d.shell) body.shell = d.shell
      if (initial.sudo !== d.sudo) body.sudo = d.sudo
      if (newKeys.length > 0) body.add_keys = newKeys
      if (d.removeKeys.length > 0) body.remove_keys = d.removeKeys
      await api(`/os-users/${encodeURIComponent(user.name)}`, { method: 'PATCH', body })
      await onSaved(tx('osUsers.saved', { name: user.name }))
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  const groupBox = (g: string, system?: boolean) => (
    <Checkbox key={g} checked={d.groups.includes(g)} onChange={(e) => toggleGroup(g, e.target.checked)}>
      <span className="mono">{g}</span>
      {system === false && <span className="small muted"> · {t('osUsers.userGroup')}</span>}
    </Checkbox>
  )

  return (
    <Modal title={user ? t('osUsers.editTitle', { name: user.name }) : t('osUsers.addTitle')} onClose={onClose} width={680} maskClosable={false}>
      {!user && <p className="small muted">{t('osUsers.addHint')}</p>}
      {catalog.error && <ErrorNote error={catalog.error} />}
      <div className="col" style={{ gap: '0.8rem' }}>
        {!user && (
          <label className="col">
            <span>{t('osUsers.fieldName')}</span>
            <Input value={d.name} placeholder="deploy" onChange={(e) => setD({ ...d, name: e.target.value.trim() })} status={d.name && nameBad ? 'error' : undefined} />
            {d.name && nameBad && <span className="small error-text">{t('osUsers.nameRule')}</span>}
          </label>
        )}
        <div className="filters">
          <label className="col" style={{ flex: 1, minWidth: '12rem' }}>
            <span>{t('osUsers.colShell')}</span>
            <Select
              value={d.shell}
              onChange={(v) => setD({ ...d, shell: v })}
              options={[...new Set([d.shell, ...shells])].map((s) => ({ value: s, label: <span className="mono">{s}</span> }))}
            />
          </label>
          <label className="col" style={{ justifyContent: 'flex-end' }}>
            <Checkbox checked={d.sudo} onChange={(e) => setD({ ...d, sudo: e.target.checked })}>
              {t('osUsers.fieldSudo')}
            </Checkbox>
          </label>
        </div>
        {user && user.sudo && !user.nkt_sudo && <span className="small muted">{t('osUsers.sudoElsewhere')}</span>}

        <div>
          <div style={{ marginBottom: '0.3rem' }}>
            <strong>{t('osUsers.colGroups')}</strong>
          </div>
          {catalog.loading && !catalog.data ? (
            <Loading />
          ) : (
            <>
              {common.length > 0 && (
                <div className="os-groups common">{common.map((g) => groupBox(g))}</div>
              )}
              {d.groups.includes('docker') && <Banner kind="warn">{t('osUsers.dockerWarn')}</Banner>}
              {SUDO_GROUPS.some((g) => d.groups.includes(g)) && <p className="small muted">{t('osUsers.sudoGroupHint')}</p>}
              <Input.Search
                allowClear
                size="small"
                placeholder={t('osUsers.groupSearch')}
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                style={{ margin: '0.5rem 0 0.3rem', maxWidth: '18rem' }}
              />
              <div className="os-groups all">{others.map((g) => groupBox(g.name, g.system))}</div>
            </>
          )}
        </div>

        {user && (user.keys ?? []).length > 0 && (
          <div>
            <div style={{ marginBottom: '0.3rem' }}>
              <strong>{t('osUsers.colKeys')}</strong>
            </div>
            <div className="col" style={{ gap: '0.2rem' }}>
              {(user.keys ?? []).map((k, i) => {
                const removed = !!k.id && d.removeKeys.includes(k.id)
                return (
                  <Checkbox
                    key={k.id ?? i}
                    checked={!removed}
                    disabled={!k.id}
                    onChange={(e) =>
                      k.id && setD({ ...d, removeKeys: e.target.checked ? d.removeKeys.filter((x) => x !== k.id) : [...d.removeKeys, k.id] })
                    }
                  >
                    <span className="small mono" style={removed ? { textDecoration: 'line-through' } : undefined}>
                      {keyText(k)}
                    </span>
                  </Checkbox>
                )
              })}
            </div>
          </div>
        )}
        <label className="col">
          <span>{user ? t('osUsers.addKeys') : t('osUsers.fieldKey')}</span>
          <Input.TextArea
            className="mono"
            autoSize={{ minRows: 2, maxRows: 6 }}
            placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5… user@laptop"
            spellCheck={false}
            value={d.addKeys}
            onChange={(e) => setD({ ...d, addKeys: e.target.value })}
            status={badKey ? 'error' : undefined}
          />
          <span className={'small ' + (badKey ? 'error-text' : 'muted')}>{badKey ? t('osUsers.keyRule') : t('osUsers.keysHint')}</span>
        </label>

        {hubTouched && (
          <Banner kind="warn">
            <div>{t('osUsers.hubWarn')}</div>
            <Checkbox checked={confirmHub} onChange={(e) => setConfirmHub(e.target.checked)} style={{ marginTop: '0.4rem' }}>
              {t('osUsers.hubConfirm')}
            </Checkbox>
          </Banner>
        )}

        {changed && (d.name || user) && (
          <div>
            <p className="small muted">{t('tokens.diffHint')}</p>
            <DiffView text={diff} />
          </div>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <Space wrap>
          <Button
            type="primary"
            loading={busy}
            disabled={!changed || nameBad || !!badKey || (hubTouched && !confirmHub)}
            onClick={() => void save()}
          >
            {user ? t('tokens.save') : t('osUsers.add')}
          </Button>
        </Space>
      </div>
    </Modal>
  )
}
