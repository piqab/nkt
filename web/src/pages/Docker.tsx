import { useState } from 'react'
import { Button, Table, type TableColumnsType } from 'antd'
import { api, qs, useApi } from '../api'
import type { Container, DockerNetwork, FileContent, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import BlockTree from '../components/BlockTree'
import PathPicker, { ownerFromPath } from '../components/PathPicker'

export default function Docker({ me }: { me: Me }) {
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
      setNotice({ kind: 'info', text: 'Хост пересканирован.' })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  const containerColumns: TableColumnsType<Container> = [
    {
      title: 'Контейнер',
      key: 'name',
      render: (_, c) => (
        <>
          <strong>{c.name}</strong>
          <div className="small muted">
            {c.project ? `${c.project}/${c.service_name}` : 'вне compose'}
            {c.restart ? ` · restart: ${c.restart}` : ''}
          </div>
        </>
      ),
    },
    {
      title: 'Пользователь',
      key: 'owner',
      render: (_, c) => <span className="small">{(c.compose_file && ownerFromPath(c.compose_file)) || '—'}</span>,
    },
    {
      title: 'Образ',
      key: 'image',
      render: (_, c) => (
        <span className="small mono" style={{ wordBreak: 'break-all' }}>
          {c.image}
        </span>
      ),
    },
    {
      title: 'Состояние',
      key: 'state',
      render: (_, c) => (
        <>
          <StateBadge state={c.state} />
          <div className="small muted">{c.status}</div>
          {c.declared && !c.running && <div className="small muted">описан, но не запущен</div>}
          {!c.declared && c.running && <div className="small muted">запущен вне compose</div>}
        </>
      ),
    },
    {
      title: 'Порты',
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
                    : `${p.container_port}/${p.protocol} (не опубликован)`}
                </div>
              ))}
        </span>
      ),
    },
    {
      title: 'Сети',
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
      title: 'Действия',
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
              редактировать конфиг
            </Button>
          )}
        </div>
      ),
    },
  ]

  const networkColumns: TableColumnsType<DockerNetwork> = [
    {
      title: 'Сеть',
      key: 'name',
      render: (_, n) => (
        <>
          <strong>{n.name}</strong>
          {n.internal && <div className="small muted">internal</div>}
        </>
      ),
    },
    { title: 'Драйвер', key: 'driver', render: (_, n) => <span className="small">{n.driver}</span> },
    { title: 'Подсети', key: 'subnets', render: (_, n) => <span className="small mono">{(n.subnets ?? []).join(', ') || '—'}</span> },
    { title: 'Шлюз', key: 'gateway', render: (_, n) => <span className="small mono">{n.gateway || '—'}</span> },
    { title: 'Интерфейс', key: 'bridge', render: (_, n) => <span className="small mono">{n.bridge || '—'}</span> },
  ]

  async function containerAct(name: string, action: string) {
    if (!window.confirm(`Выполнить «${action}» для контейнера ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/containers/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${action} выполнено.` })
      // Awaited, not fire-and-forget: clearing `busy` (below, in finally)
      // before the refreshed list has actually landed made the spinner
      // disappear while the table still showed the pre-action state for a
      // moment — reads as "did that even do anything?".
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
            <InfoHint>Сопоставление того, что описано в compose, с тем, что реально работает.</InfoHint>
          </h1>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button onClick={rescan} loading={rescanning}>
              {rescanning ? 'Сканирую…' : 'Пересканировать'}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={docker.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && (
        <Banner kind="info">Действия недоступны: нужна роль admin и включённые изменения.</Banner>
      )}

      <Card
        title="Контейнеры docker"
        actions={
          canControl && (
            <Button type="link" onClick={() => setPickingPath(true)}>
              + новый контейнер
            </Button>
          )
        }
      >
        {docker.loading && !docker.data ? (
          <Loading what="контейнеры" />
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
                  <div>{c.project ? `${c.project}/${c.service_name}` : 'вне compose'}</div>
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

      <Card title="Сети docker">
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
        <Modal title="Новый контейнер — выберите расположение" onClose={() => setPickingPath(false)}>
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
  const file = useApi<FileContent>(`/configs/file${qs({ path })}`)

  return (
    <Modal title={path} onClose={onClose}>
      {file.loading && !file.data ? (
        <Loading what="файл" />
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
