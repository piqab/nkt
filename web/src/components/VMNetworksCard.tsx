import { useState } from 'react'
import { Button, Checkbox, Input, Select, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me, VMNetwork } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal } from './ui'
import { DataTable } from './DataTable'
import { confirmAction } from './confirm'

/**
 * Сети libvirt, в которые включаются машины.
 *
 * Отдельно от сетевых настроек хоста: там речь про интерфейсы самого
 * сервера, здесь — про то, куда смотрят его машины. Без такой сети
 * машина не стартует вовсе, и самая частая встреча с libvirt на свежем
 * хосте — «нет сети с совпадающим именем default».
 */
export default function VMNetworksCard({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  const nets = useApi<{ networks: VMNetwork[] }>('/vm/networks', 30_000)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function act(name: string, action: string, confirm?: string) {
    if (confirm && !(await confirmAction(confirm, { danger: action === 'delete' || action === 'stop' }))) return
    setBusy(name)
    setError(null)
    try {
      await api(`/vm/networks/${encodeURIComponent(name)}/${action}`, { method: 'POST' })
      nets.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<VMNetwork> = [
    {
      title: t('vmnet.colName'),
      key: 'name',
      render: (_, n) => (
        <div style={{ minWidth: '9rem' }}>
          <strong>{n.name}</strong>
          <div className="small muted">{t(`vmnet.mode.${n.mode || 'isolated'}`, { defaultValue: n.mode || '—' })}</div>
        </div>
      ),
    },
    {
      title: t('vmnet.colState'),
      key: 'state',
      render: (_, n) => (
        <span className="nowrap">
          {n.active ? <Tag color="success">{t('vmnet.active')}</Tag> : <Tag>{t('vmnet.inactive')}</Tag>}
          {n.autostart && <Tag>{t('vmnet.autostart')}</Tag>}
        </span>
      ),
    },
    {
      title: t('vmnet.colBridge'),
      key: 'bridge',
      render: (_, n) => <span className="small mono">{n.bridge || '—'}</span>,
    },
    {
      title: t('vmnet.colSubnet'),
      key: 'subnet',
      render: (_, n) => (
        <span className="small nowrap">
          {n.address ? `${n.address}/${n.netmask}` : '—'}
          {n.address && <div className="small muted">{n.dhcp ? t('vmnet.withDHCP') : t('vmnet.noDHCP')}</div>}
        </span>
      ),
    },
    {
      title: '',
      key: 'actions',
      className: 'nowrap',
      render: (_, n) =>
        canEdit && (
          <div className="row row-nowrap">
            {n.active ? (
              <Button
                type="link"
                size="small"
                danger
                loading={busy === n.name}
                onClick={() => void act(n.name, 'stop', t('vmnet.confirmStop', { name: n.name }))}
              >
                {t('vmnet.stop')}
              </Button>
            ) : (
              <Button type="link" size="small" loading={busy === n.name} onClick={() => void act(n.name, 'start')}>
                {t('vmnet.start')}
              </Button>
            )}
            <Button
              type="link"
              size="small"
              loading={busy === n.name}
              onClick={() => void act(n.name, n.autostart ? 'autostart-off' : 'autostart-on')}
            >
              {n.autostart ? t('vmnet.autostartOff') : t('vmnet.autostartOn')}
            </Button>
            <Button
              type="link"
              size="small"
              danger
              loading={busy === n.name}
              onClick={() => void act(n.name, 'delete', t('vmnet.confirmDelete', { name: n.name }))}
            >
              {t('common.delete')}
            </Button>
          </div>
        ),
    },
  ]

  const list = nets.data?.networks ?? []

  return (
    <>
      <Card
        title={
          <>
            {t('vmnet.title')}
            <InfoHint>{t('vmnet.hint')}</InfoHint>
          </>
        }
        actions={
          canEdit && (
            <Button size="small" onClick={() => setCreating(true)}>
              {t('vmnet.create')}
            </Button>
          )
        }
      >
        <ErrorNote error={error} />
        <ErrorNote error={nets.error} />
        {nets.loading && !nets.data ? (
          <Loading what={t('vmnet.title')} />
        ) : list.length === 0 ? (
          <Banner kind="warn">{t('vmnet.empty')}</Banner>
        ) : (
          <div className="table-wrap">
            <DataTable<VMNetwork> dataSource={list} columns={columns} rowKey="name" tableLayout="auto" />
          </div>
        )}
      </Card>

      {creating && (
        <CreateNetworkModal
          onClose={() => setCreating(false)}
          onDone={() => {
            setCreating(false)
            nets.reload()
          }}
        />
      )}
    </>
  )
}

/** Создание сети: NAT с подсетью, мост на интерфейс хоста или
 * изолированная. */
function CreateNetworkModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [mode, setMode] = useState<'nat' | 'bridge' | 'isolated'>('nat')
  const [subnet, setSubnet] = useState('192.168.100.0/24')
  const [bridge, setBridge] = useState('')
  const [dhcp, setDHCP] = useState(true)
  const [autostart, setAutostart] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function create() {
    setBusy(true)
    setError(null)
    try {
      await api('/vm/networks', {
        method: 'POST',
        body: {
          name: name.trim(),
          mode,
          subnet: mode === 'bridge' ? '' : subnet.trim(),
          bridge: bridge.trim(),
          dhcp: mode === 'bridge' ? false : dhcp,
          autostart,
        },
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('vmnet.createTitle')} onClose={onClose} width={640}>
      <ErrorNote error={error} />
      <div className="col" style={{ gap: '0.6rem' }}>
        <label>
          {t('vmnet.name')}
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="vmnet" />
        </label>
        <label>
          {t('vmnet.mode.label')}
          <Select
            value={mode}
            onChange={(v: 'nat' | 'bridge' | 'isolated') => setMode(v)}
            options={[
              { value: 'nat', label: t('vmnet.mode.nat') },
              { value: 'bridge', label: t('vmnet.mode.bridge') },
              { value: 'isolated', label: t('vmnet.mode.isolated') },
            ]}
          />
          <span className="small muted">{t(`vmnet.modeHint.${mode}`)}</span>
        </label>

        {mode === 'bridge' ? (
          <label>
            {t('vmnet.bridgeExisting')}
            <Input value={bridge} onChange={(e) => setBridge(e.target.value)} placeholder="br0" />
          </label>
        ) : (
          <>
            <label>
              {t('vmnet.subnet')}
              <Input value={subnet} onChange={(e) => setSubnet(e.target.value)} placeholder="192.168.100.0/24" />
              <span className="small muted">{t('vmnet.subnetHint')}</span>
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
              <Checkbox checked={dhcp} onChange={(e) => setDHCP(e.target.checked)} />
              {t('vmnet.dhcp')}
            </label>
          </>
        )}

        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
          <Checkbox checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
          {t('vmnet.autostartLabel')}
        </label>

        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" loading={busy} disabled={!name.trim()} onClick={() => void create()}>
            {t('vmnet.create')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}
