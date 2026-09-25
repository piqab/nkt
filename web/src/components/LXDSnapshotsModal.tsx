import { useState } from 'react'
import { Button, Checkbox, Input, Table } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Loading, Modal, formatBytesShort, formatDateTime } from './ui'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'

type Snapshot = { name: string; created_at: string; expires_at?: string; stateful: boolean; size: number }

/** Снимки инстанса LXD: создать, откатить (lxc restore), удалить. */
export default function LXDSnapshotsModal({
  name,
  isVM,
  canControl,
  onClose,
  onChanged,
}: {
  name: string
  isVM: boolean
  canControl: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const base = `/lxd/instances/${encodeURIComponent(name)}/snapshots`
  const list = useApi<{ snapshots: Snapshot[] }>(base)
  const [snapName, setSnapName] = useState(() => defaultSnapName())
  const [stateful, setStateful] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const nameOK = /^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(snapName)

  async function run(key: string, fn: () => Promise<unknown>) {
    setBusy(key)
    setError(null)
    try {
      await fn()
      await list.reload()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const create = () =>
    run('create', async () => {
      await api(base, { method: 'POST', body: { name: snapName, stateful } })
      setSnapName(defaultSnapName())
    })

  async function restore(s: string) {
    if (!(await confirmAction(t('lxdSnap.restoreConfirm', { name, snap: s }), { okText: t('lxdSnap.restore') }))) return
    await run(`${s}:restore`, () => api(`${base}/${encodeURIComponent(s)}/restore`, { method: 'POST' }))
  }

  async function del(s: string) {
    if (!(await confirmAction(t('lxdSnap.deleteConfirm', { name, snap: s }), { okText: t('common.delete') }))) return
    await run(`${s}:delete`, () => api(`${base}/${encodeURIComponent(s)}`, { method: 'DELETE' }))
  }

  const snaps = list.data?.snapshots ?? []
  return (
    <Modal title={t('lxdSnap.title', { name })} onClose={onClose} width={720}>
      <p className="small muted">{t('lxdSnap.hint')}</p>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {list.error && <Banner kind="error">{list.error}</Banner>}
      {canControl && (
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap', marginBottom: '0.75rem' }}>
          <Input
            size="small"
            value={snapName}
            onChange={(e) => setSnapName(e.target.value.trim())}
            status={nameOK ? undefined : 'error'}
            placeholder={t('lxdSnap.namePlaceholder')}
            style={{ width: '16rem' }}
            onPressEnter={() => nameOK && void create()}
          />
          <Checkbox checked={stateful} onChange={(e) => setStateful(e.target.checked)}>
            {t('lxdSnap.stateful')}
          </Checkbox>
          <Button size="small" type="primary" disabled={!nameOK} loading={busy === 'create'} onClick={() => void create()}>
            {t('lxdSnap.create')}
          </Button>
        </div>
      )}
      {stateful && <p className="small muted">{isVM ? t('lxdSnap.statefulVM') : t('lxdSnap.statefulCT')}</p>}
      {list.loading && !list.data ? (
        <Loading what={t('lxdSnap.loading')} />
      ) : snaps.length === 0 ? (
        <p className="small muted">{t('lxdSnap.none')}</p>
      ) : (
        <Table<Snapshot>
          size="small"
          pagination={false}
          rowKey="name"
          dataSource={snaps}
          columns={[
            { title: t('lxdSnap.colName'), key: 'name', render: (_, s) => <span className="mono">{s.name}</span> },
            { title: t('lxdSnap.colCreated'), key: 'created', render: (_, s) => <span className="small nowrap">{formatDateTime(s.created_at)}</span> },
            {
              title: t('lxdSnap.colExpires'),
              key: 'expires',
              render: (_, s) => <span className="small nowrap">{s.expires_at ? formatDateTime(s.expires_at) : '—'}</span>,
            },
            { title: t('lxdSnap.colSize'), key: 'size', render: (_, s) => <span className="small">{s.size > 0 ? formatBytesShort(s.size) : '—'}</span> },
            { title: t('lxdSnap.colStateful'), key: 'stateful', render: (_, s) => (s.stateful ? '✓' : '—') },
            {
              title: t('common.actions'),
              key: 'actions',
              render: (_, s) =>
                canControl && (
                  <div className="row">
                    <RowAction action="restore" label={t('lxdSnap.restore')} loading={busy === `${s.name}:restore`} onClick={() => void restore(s.name)} />
                    <RowAction action="delete" danger label={t('common.delete')} loading={busy === `${s.name}:delete`} onClick={() => void del(s.name)} />
                  </div>
                ),
            },
          ]}
        />
      )}
    </Modal>
  )
}

function defaultSnapName() {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `snap-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}`
}
