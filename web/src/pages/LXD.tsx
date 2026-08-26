import { useState } from 'react'
import { Button, Form, Input, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { LXDInstance, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'

export default function LXD({ me }: { me: Me }) {
  const { t } = useTranslation()
  const instances = useApi<{ instances: LXDInstance[] }>('/lxd/instances', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  const allInstances = instances.data?.instances ?? []
  const activeInstances = allInstances.filter((i) => i.status.toLowerCase() === 'running')
  const inactiveInstances = allInstances.filter((i) => i.status.toLowerCase() !== 'running')

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
      setNotice({ kind: 'info', text: t('common.hostRescanned') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(name: string, action: string) {
    if (!window.confirm(t('lxd.confirmAction', { action, name }))) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: t('lxd.actionDone', { name, action }) })
      // The backend only kicks off a fire-and-forget background rescan
      // (rescanLater) — a bare reload() right after would just reread the
      // still-stale cached snapshot. /inventory/refresh runs the same
      // rescan synchronously, same as "Пересканировать" below.
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    if (!window.confirm(t('common.confirmDelete', { what: t('lxd.instance'), name }))) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('common.deleted', { name }) })
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<LXDInstance> = [
    { title: t('lxd.colName'), key: 'name', render: (_, i) => <strong>{i.name}</strong> },
    { title: t('lxd.colType'), key: 'type', render: (_, i) => <span className="small">{i.type === 'virtual-machine' ? t('lxd.vm') : t('lxd.container')}</span> },
    { title: t('lxd.colState'), key: 'status', render: (_, i) => <StateBadge state={i.status} /> },
    { title: t('lxd.colArch'), key: 'architecture', render: (_, i) => <span className="small mono">{i.architecture || '—'}</span> },
    { title: 'IPv4', key: 'ipv4', render: (_, i) => <span className="small mono">{(i.ipv4 ?? []).join(', ') || '—'}</span> },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_, i) => (
        <div className="row">
          {['start', 'restart', 'stop', 'pause'].map((a) => (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={!canControl}
              loading={busy === `${i.name}:${a}`}
              onClick={() => act(i.name, a)}
            >
              {a}
            </Button>
          ))}
          {canControl && (
            <Button danger type="link" size="small" loading={busy === `${i.name}:delete`} onClick={() => del(i.name)}>
              {t('common.delete')}
            </Button>
          )}
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            LXD
            <InfoHint>{t('lxd.hint')}</InfoHint>
          </h1>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button onClick={rescan} loading={rescanning}>
              {rescanning ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={instances.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card
        title={t('lxd.instances')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreating(true)}>
              {t('lxd.newInstance')}
            </Button>
          )
        }
      >
        {instances.loading && !instances.data ? (
          <Loading what={t('lxd.loading')} />
        ) : allInstances.length === 0 ? (
          <p className="small muted">{t('lxd.none')}</p>
        ) : (
          <>
            <InactiveSummary
              items={inactiveInstances}
              getKey={(i) => i.name}
              getLabel={(i) => i.name}
              getTooltip={(i) => (
                <>
                  <div>{i.type === 'virtual-machine' ? t('lxd.vm') : t('lxd.container')}</div>
                  <div>{t('lxd.state', { state: i.status })}</div>
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
            />
            <div className="table-wrap">
              <Table<LXDInstance>
                dataSource={activeInstances}
                columns={columns}
                rowKey="name"
                pagination={false}
                size="small"
              />
            </div>
          </>
        )}
      </Card>

      {creating && (
        <CreateInstanceForm
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            instances.reload()
          }}
        />
      )}
    </>
  )
}

type CreateInstanceValues = { image: string; name: string }

function CreateInstanceForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(values: CreateInstanceValues) {
    setBusy(true)
    setError(null)
    try {
      await api('/lxd/instances', { method: 'POST', body: values })
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
          {t('lxd.newInstanceTitle')}
          <InfoHint>{t('lxd.newInstanceHint')}</InfoHint>
        </>
      }
      actions={
        <Button type="link" onClick={onClose}>
          {t('common.close')}
        </Button>
      }
    >
      <Form<CreateInstanceValues> layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="image" label={t('lxd.image')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '16rem' }}>
            <Input placeholder="ubuntu:24.04" />
          </Form.Item>
          <Form.Item name="name" label={t('lxd.instanceName')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input placeholder="my-instance" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? t('lxd.launching') : t('lxd.createAndStart')}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
