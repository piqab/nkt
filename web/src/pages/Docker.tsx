import { useState } from 'react'
import { Button, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Container, DockerNetwork, FileContent, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import BlockTree from '../components/BlockTree'
import PathPicker, { ownerFromPath } from '../components/PathPicker'

export default function Docker({ me }: { me: Me }) {
  const { t } = useTranslation()
  const docker = useApi<{ containers: Container[]; networks: DockerNetwork[] }>('/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [configModal, setConfigModal] = useState<{ path: string; focusName?: string; autoCreate?: boolean } | null>(null)
  const [pickingPath, setPickingPath] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  const allContainers = docker.data?.containers ?? []
  const activeContainers = allContainers.filter((c) => c.running)
  const inactiveContainers = allContainers.filter((c) => !c.running)

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      await docker.reload()
      setNotice({ kind: 'info', text: t('common.hostRescanned') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  const containerColumns: TableColumnsType<Container> = [
    {
      title: t('docker.colContainer'),
      key: 'name',
      render: (_, c) => (
        <>
          <strong>{c.name}</strong>
          <div className="small muted">
            {c.project ? `${c.project}/${c.service_name}` : t('docker.outsideCompose')}
            {c.restart ? t('docker.restart', { policy: c.restart }) : ''}
          </div>
        </>
      ),
    },
    {
      title: t('docker.colOwner'),
      key: 'owner',
      render: (_, c) => <span className="small">{(c.compose_file && ownerFromPath(c.compose_file)) || '—'}</span>,
    },
    {
      title: t('docker.colImage'),
      key: 'image',
      render: (_, c) => (
        <span className="small mono" style={{ wordBreak: 'break-all' }}>
          {c.image}
        </span>
      ),
    },
    {
      title: t('docker.colState'),
      key: 'state',
      render: (_, c) => (
        <>
          <StateBadge state={c.state} />
          <div className="small muted">{c.status}</div>
          {c.declared && !c.running && <div className="small muted">{t('docker.declaredNotRunning')}</div>}
          {!c.declared && c.running && <div className="small muted">{t('docker.runningOutsideCompose')}</div>}
        </>
      ),
    },
    {
      title: t('docker.colPorts'),
      key: 'ports',
      render: (_, c) => (
        <span className="small mono">
          {(c.ports ?? []).length === 0
            ? '—'
            : (c.ports ?? []).map((p, i) => (
                <div
                  key={i}
                  style={{
                    color: p.host_port && (!p.host_ip || p.host_ip === '0.0.0.0') ? 'var(--status-critical)' : undefined,
                  }}
                >
                  {p.host_port
                    ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}/${p.protocol}`
                    : t('common.notPublished', { port: `${p.container_port}/${p.protocol}` })}
                </div>
              ))}
        </span>
      ),
    },
    {
      title: t('docker.colNetworks'),
      key: 'networks',
      render: (_, c) => (
        <span className="small">
          {(c.networks ?? []).map((n) => (
            <div key={n.name}>
              {n.name}
              {n.ip_address ? ` · ${n.ip_address}` : ''}
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
              onClick={() => containerAct(c.name, a)}
            >
              {a}
            </Button>
          ))}
          {canControl && c.compose_file && c.service_name && (
            <Button type="link" size="small" onClick={() => setConfigModal({ path: c.compose_file!, focusName: c.service_name })}>
              {t('docker.editConfig')}
            </Button>
          )}
        </div>
      ),
    },
  ]

  const networkColumns: TableColumnsType<DockerNetwork> = [
    {
      title: t('docker.colNetwork'),
      key: 'name',
      render: (_, n) => (
        <>
          <strong>{n.name}</strong>
          {n.internal && <div className="small muted">internal</div>}
        </>
      ),
    },
    { title: t('docker.colDriver'), key: 'driver', render: (_, n) => <span className="small">{n.driver}</span> },
    { title: t('docker.colSubnets'), key: 'subnets', render: (_, n) => <span className="small mono">{(n.subnets ?? []).join(', ') || '—'}</span> },
    { title: t('docker.colGateway'), key: 'gateway', render: (_, n) => <span className="small mono">{n.gateway || '—'}</span> },
    { title: t('docker.colInterface'), key: 'bridge', render: (_, n) => <span className="small mono">{n.bridge || '—'}</span> },
  ]

  async function containerAct(name: string, action: string) {
    if (!window.confirm(t('docker.confirmAction', { action, name }))) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/containers/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: t('docker.actionDone', { name, action }) })
      // handleContainerAction itself only calls rescanLater() — a fire-
      // and-forget *background* full rescan, deliberately not blocking
      // that response — so a bare docker.reload() right after would just
      // reread the still-stale cached snapshot from before the action.
      // /inventory/refresh runs the same rescan synchronously (the same
      // one "Пересканировать" below uses); awaiting it first is what
      // makes the reload right after it actually fresh, at the cost of
      // the spinner honestly staying up for a full rescan instead of
      // clearing early and then quietly going stale for a few seconds.
      await api('/inventory/refresh', { method: 'POST' })
      await docker.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            Docker
            <InfoHint>{t('docker.hint')}</InfoHint>
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

      <ErrorNote error={docker.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card
        title={t('docker.containers')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setPickingPath(true)}>
              {t('docker.newContainer')}
            </Button>
          )
        }
      >
        {docker.loading && !docker.data ? (
          <Loading what={t('docker.loading')} />
        ) : (
          <>
            <InactiveSummary
              items={inactiveContainers}
              getKey={(c) => c.name}
              getLabel={(c) => c.name}
              getTooltip={(c) => (
                <>
                  <div>{c.image}</div>
                  <div>
                    {c.state} · {c.status}
                  </div>
                  <div>{c.project ? `${c.project}/${c.service_name}` : t('docker.outsideCompose')}</div>
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
            />
            <div className="table-wrap">
              <Table<Container>
                dataSource={activeContainers}
                columns={containerColumns}
                rowKey="name"
                pagination={false}
                size="small"
              />
            </div>
          </>
        )}
      </Card>

      <Card title={t('docker.networks')}>
        <div className="table-wrap">
          <Table<DockerNetwork>
            dataSource={docker.data?.networks ?? []}
            columns={networkColumns}
            rowKey="id"
            pagination={false}
            size="small"
          />
        </div>
      </Card>

      {pickingPath && (
        <Modal title={t('docker.newContainerLocation')} onClose={() => setPickingPath(false)}>
          <PathPicker
            onPick={(path) => {
              setPickingPath(false)
              setConfigModal({ path, autoCreate: true })
            }}
            onCancel={() => setPickingPath(false)}
          />
        </Modal>
      )}

      {configModal && (
        <ContainerConfigModal
          path={configModal.path}
          focusName={configModal.focusName}
          autoCreate={configModal.autoCreate}
          me={me}
          onClose={() => setConfigModal(null)}
          onSaved={() => docker.reload()}
        />
      )}
    </>
  )
}

/** Edits a container's compose service in place — no navigation to
 * «Конфигурации», since jumping away and back just to change one image tag
 * or port was the whole complaint that led here. */
function ContainerConfigModal({
  path,
  focusName,
  autoCreate,
  me,
  onClose,
  onSaved,
}: {
  path: string
  focusName?: string
  autoCreate?: boolean
  me: Me
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const file = useApi<FileContent>(`/configs/file${qs({ path })}`)

  return (
    <Modal title={path} onClose={onClose}>
      {file.loading && !file.data ? (
        <Loading what={t('docker.loadingFile')} />
      ) : file.error ? (
        <ErrorNote error={file.error} />
      ) : file.data ? (
        <BlockTree
          path={file.data.path}
          service={file.data.service}
          sha256={file.data.sha256}
          me={me}
          focusName={focusName}
          autoCreate={autoCreate}
          onSaved={() => {
            file.reload()
            onSaved()
          }}
        />
      ) : null}
    </Modal>
  )
}
