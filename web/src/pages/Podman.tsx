import { useState } from 'react'
import { Button, Form, Input, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me, PodmanContainer } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'

export default function Podman({ me }: { me: Me }) {
  const { t } = useTranslation()
  const containers = useApi<{ containers: PodmanContainer[] }>('/podman/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  const allContainers = containers.data?.containers ?? []
  const activeContainers = allContainers.filter((c) => c.state === 'running')
  const inactiveContainers = allContainers.filter((c) => c.state !== 'running')

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      await containers.reload()
      setNotice({ kind: 'info', text: t('common.hostRescanned') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(name: string, action: string) {
    if (!window.confirm(t('podman.confirmAction', { action, name }))) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/podman/containers/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: t('podman.actionDone', { name, action }) })
      // The backend only kicks off a fire-and-forget background rescan
      // (rescanLater) — a bare reload() right after would just reread the
      // still-stale cached snapshot. /inventory/refresh runs the same
      // rescan synchronously, same as "Пересканировать" below.
      await api('/inventory/refresh', { method: 'POST' })
      await containers.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    if (!window.confirm(t('common.confirmDelete', { what: t('podman.container'), name }))) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/podman/containers/${name}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('common.deleted', { name }) })
      await api('/inventory/refresh', { method: 'POST' })
      await containers.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<PodmanContainer> = [
    { title: t('podman.colContainer'), key: 'name', render: (_, c) => <strong>{c.name}</strong> },
    { title: t('podman.colPod'), key: 'pod', render: (_, c) => <span className="small">{c.pod || '—'}</span> },
    {
      title: t('podman.colImage'),
      key: 'image',
      render: (_, c) => (
        <span className="small mono" style={{ wordBreak: 'break-all' }}>
          {c.image}
        </span>
      ),
    },
    {
      title: t('podman.colState'),
      key: 'state',
      render: (_, c) => (
        <>
          <StateBadge state={c.state} />
          <div className="small muted">{c.status}</div>
        </>
      ),
    },
    {
      title: t('podman.colPorts'),
      key: 'ports',
      render: (_, c) => (
        <span className="small mono">
          {(c.ports ?? []).length === 0
            ? '—'
            : (c.ports ?? []).map((p, i) => (
                <div key={i}>
                  {p.host_port
                    ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}/${p.protocol}`
                    : t('common.notPublished', { port: `${p.container_port}/${p.protocol}` })}
                </div>
              ))}
        </span>
      ),
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_, c) => (
        <div className="row">
          {['start', 'restart', 'stop'].map((a) => (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={!canControl}
              loading={busy === `${c.name}:${a}`}
              onClick={() => act(c.name, a)}
            >
              {a}
            </Button>
          ))}
          {canControl && (
            <Button danger type="link" size="small" loading={busy === `${c.name}:delete`} onClick={() => del(c.name)}>
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
            Podman
            <InfoHint>{t('podman.hint')}</InfoHint>
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

      <ErrorNote error={containers.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card
        title={t('podman.containers')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreating(true)}>
              {t('podman.newContainer')}
            </Button>
          )
        }
      >
        {containers.loading && !containers.data ? (
          <Loading what={t('podman.loading')} />
        ) : allContainers.length === 0 ? (
          <p className="small muted">{t('podman.none')}</p>
        ) : (
          <>
            <InactiveSummary
              items={inactiveContainers}
              getKey={(c) => c.id}
              getLabel={(c) => c.name}
              getTooltip={(c) => (
                <>
                  <div>{c.image}</div>
                  <div>
                    {c.state} · {c.status}
                  </div>
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
            />
            <div className="table-wrap">
              <Table<PodmanContainer>
                dataSource={activeContainers}
                columns={columns}
                rowKey="id"
                pagination={false}
                size="small"
              />
            </div>
          </>
        )}
      </Card>

      {creating && (
        <CreateContainerForm
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false)
            containers.reload()
          }}
        />
      )}
    </>
  )
}

type CreateContainerValues = { image: string; name: string }

function CreateContainerForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(values: CreateContainerValues) {
    setBusy(true)
    setError(null)
    try {
      await api('/podman/containers', { method: 'POST', body: values })
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
          {t('podman.newContainerTitle')}
          <InfoHint>{t('podman.newContainerHint')}</InfoHint>
        </>
      }
      actions={
        <Button type="link" onClick={onClose}>
          {t('common.close')}
        </Button>
      }
    >
      <Form<CreateContainerValues> layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="image" label={t('podman.colImage')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '16rem' }}>
            <Input placeholder="docker.io/library/nginx:1.27" />
          </Form.Item>
          <Form.Item name="name" label={t('podman.containerName')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input placeholder="my-container" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? t('podman.creating') : t('podman.createAndStart')}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
