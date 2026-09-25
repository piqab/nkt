import { useState } from 'react'
import { Button, Form, Input, Table } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Loading, Modal, formatBytesShort } from './ui'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { LXDImagePicker, type LXDImage } from './LXDImagePicker'
import CommandModal from './CommandModal'

type Network = { name: string; type: string; managed: boolean; status: string; ipv4: string; ipv6: string; nat: boolean; used_by: string[] }
type Pool = { name: string; driver: string; status: string; source: string; size: string; used_by: string[] }

const usedBy = (list: string[]) => (list.length ? list.map((u) => <div key={u} className="small mono">{u}</div>) : <span className="small muted">—</span>)

/** Сети, образы и пулы хранения LXD под списком инстансов. */
export function LXDResources({ canControl }: { canControl: boolean }) {
  const { t } = useTranslation()
  const nets = useApi<{ networks: Network[] }>('/lxd/networks')
  const images = useApi<{ images: LXDImage[] }>('/lxd/images?remote=local')
  const pools = useApi<{ pools: Pool[] }>('/lxd/storage')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [creatingNet, setCreatingNet] = useState(false)
  const [picking, setPicking] = useState(false)
  const [copying, setCopying] = useState<{ ref: string; vm: boolean } | null>(null)

  async function run(key: string, fn: () => Promise<unknown>, reload: () => Promise<void>) {
    setBusy(key)
    setError(null)
    try {
      await fn()
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function delNet(n: Network) {
    if (!(await confirmAction(t('lxdRes.netDeleteConfirm', { name: n.name }), { okText: t('common.delete') }))) return
    await run(`net:${n.name}`, () => api(`/lxd/networks/${encodeURIComponent(n.name)}`, { method: 'DELETE' }), nets.reload)
  }

  async function delImage(im: LXDImage) {
    if (!im.fingerprint) return
    if (!(await confirmAction(t('lxdRes.imageDeleteConfirm', { name: im.alias || im.fingerprint.slice(0, 12) }), { okText: t('common.delete') }))) return
    await run(`img:${im.fingerprint}`, () => api(`/lxd/images/${im.fingerprint}`, { method: 'DELETE' }), images.reload)
  }

  return (
    <>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      <Card
        title={t('lxdRes.networks')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreatingNet(true)}>
              {t('lxdRes.newNetwork')}
            </Button>
          )
        }
      >
        {nets.error ? (
          <p className="small muted">{nets.error}</p>
        ) : !nets.data ? (
          <Loading what={t('lxdRes.loading')} />
        ) : (
          <div className="table-wrap">
            <Table<Network>
              size="small"
              pagination={false}
              rowKey="name"
              dataSource={nets.data.networks}
              columns={[
                { title: t('lxdRes.colName'), key: 'name', render: (_, n) => <span className="mono">{n.name}</span> },
                { title: t('lxdRes.colType'), key: 'type', render: (_, n) => `${n.type}${n.managed ? '' : ` (${t('lxdRes.unmanaged')})`}` },
                { title: 'IPv4', key: 'ipv4', render: (_, n) => <span className="mono small">{n.ipv4 || '—'}{n.nat ? ' NAT' : ''}</span> },
                { title: 'IPv6', key: 'ipv6', render: (_, n) => <span className="mono small">{n.ipv6 || '—'}</span> },
                { title: t('lxdRes.colUsedBy'), key: 'used', render: (_, n) => usedBy(n.used_by) },
                {
                  title: t('common.actions'),
                  key: 'actions',
                  render: (_, n) =>
                    canControl &&
                    n.managed && (
                      <RowAction action="delete" danger label={t('common.delete')} loading={busy === `net:${n.name}`} onClick={() => void delNet(n)} />
                    ),
                },
              ]}
            />
          </div>
        )}
      </Card>

      <Card
        title={t('lxdRes.images')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setPicking(true)}>
              {t('lxdRes.downloadImage')}
            </Button>
          )
        }
      >
        {images.error ? (
          <p className="small muted">{images.error}</p>
        ) : !images.data ? (
          <Loading what={t('lxdRes.loading')} />
        ) : images.data.images.length === 0 ? (
          <p className="small muted">{t('lxdRes.noImages')}</p>
        ) : (
          <div className="table-wrap">
            <Table<LXDImage>
              size="small"
              pagination={false}
              rowKey={(i) => i.fingerprint ?? i.ref}
              dataSource={images.data.images}
              columns={[
                { title: t('lxdRes.colAlias'), key: 'alias', render: (_, i) => <span className="mono">{i.alias || '—'}</span> },
                { title: t('lxdRes.colDescription'), key: 'desc', render: (_, i) => <span className="small">{i.description}</span> },
                { title: t('lxdRes.colType'), key: 'type', render: (_, i) => (i.type === 'virtual-machine' ? t('lxd.vm') : t('lxd.container')) },
                { title: t('lxdRes.colSize'), key: 'size', render: (_, i) => <span className="small">{i.size ? formatBytesShort(i.size) : '—'}</span> },
                { title: t('lxdRes.colFingerprint'), key: 'fp', render: (_, i) => <span className="mono small">{i.fingerprint?.slice(0, 12)}</span> },
                {
                  title: t('common.actions'),
                  key: 'actions',
                  render: (_, i) =>
                    canControl &&
                    i.fingerprint && (
                      <RowAction action="delete" danger label={t('common.delete')} loading={busy === `img:${i.fingerprint}`} onClick={() => void delImage(i)} />
                    ),
                },
              ]}
            />
          </div>
        )}
      </Card>

      <Card title={t('lxdRes.storage')}>
        {pools.error ? (
          <p className="small muted">{pools.error}</p>
        ) : !pools.data ? (
          <Loading what={t('lxdRes.loading')} />
        ) : (
          <div className="table-wrap">
            <Table<Pool>
              size="small"
              pagination={false}
              rowKey="name"
              dataSource={pools.data.pools}
              columns={[
                { title: t('lxdRes.colName'), key: 'name', render: (_, p) => <span className="mono">{p.name}</span> },
                { title: t('lxdRes.colDriver'), key: 'driver', dataIndex: 'driver' },
                { title: t('lxdRes.colSource'), key: 'source', render: (_, p) => <span className="mono small">{p.source || '—'}</span> },
                { title: t('lxdRes.colSize'), key: 'size', render: (_, p) => p.size || '—' },
                { title: t('lxdRes.colUsedBy'), key: 'used', render: (_, p) => usedBy(p.used_by) },
              ]}
            />
          </div>
        )}
      </Card>

      {creatingNet && (
        <NetworkForm
          onClose={() => setCreatingNet(false)}
          onCreated={() => {
            setCreatingNet(false)
            void nets.reload()
          }}
        />
      )}
      {picking && (
        <ImageDownloadForm
          onClose={() => setPicking(false)}
          onPick={(ref, vm) => {
            setPicking(false)
            setCopying({ ref, vm })
          }}
        />
      )}
      {copying && (
        <CommandModal
          title={t('lxdRes.downloadTitle', { ref: copying.ref })}
          asJob
          wsPath={`/lxd/images/copy/ws?ref=${encodeURIComponent(copying.ref)}${copying.vm ? '&vm=1' : ''}`}
          onClose={() => setCopying(null)}
          onFinished={() => void images.reload()}
        />
      )}
    </>
  )
}

function NetworkForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [ipv4, setIPv4] = useState('auto')
  const [ipv6, setIPv6] = useState('none')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const nameOK = /^[a-z][a-z0-9-]{0,14}$/.test(name)

  async function create() {
    setBusy(true)
    setError(null)
    try {
      await api('/lxd/networks', { method: 'POST', body: { name, ipv4, ipv6 } })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('lxdRes.newNetwork')} onClose={onClose}>
      <p className="small muted">{t('lxdRes.netHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <Form layout="vertical" onFinish={() => void create()}>
        <Form.Item label={t('lxdRes.colName')} validateStatus={name && !nameOK ? 'error' : undefined}>
          <Input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="lxdbr1" />
        </Form.Item>
        <Form.Item label="ipv4.address" extra={t('lxdRes.addrHint')}>
          <Input value={ipv4} onChange={(e) => setIPv4(e.target.value.trim())} />
        </Form.Item>
        <Form.Item label="ipv6.address">
          <Input value={ipv6} onChange={(e) => setIPv6(e.target.value.trim())} />
        </Form.Item>
        <Button type="primary" htmlType="submit" disabled={!nameOK} loading={busy}>
          {t('lxdRes.create')}
        </Button>
      </Form>
    </Modal>
  )
}

function ImageDownloadForm({ onClose, onPick }: { onClose: () => void; onPick: (ref: string, vm: boolean) => void }) {
  const { t } = useTranslation()
  const [ref, setRef] = useState('')
  const [vm, setVM] = useState(false)
  const remote = /^(images|ubuntu):/.test(ref)
  return (
    <Modal title={t('lxdRes.downloadImage')} onClose={onClose} width={760}>
      <p className="small muted">{t('lxdRes.downloadHint')}</p>
      <LXDImagePicker
        value={ref}
        onChange={(r, isVM) => {
          setRef(r)
          setVM(isVM)
        }}
      />
      <div className="row" style={{ marginTop: '0.75rem' }}>
        <Button type="primary" disabled={!remote} onClick={() => onPick(ref, vm)}>
          {t('lxdRes.download')}
        </Button>
      </div>
    </Modal>
  )
}
