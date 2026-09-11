import { useState } from 'react'
import { Button, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { useHostRescan } from '../rescan'
import { api, qs, useApi } from '../api'
import type { Container, DockerNetwork, FileContent, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge, shortImageRef } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import BlockTree from '../components/BlockTree'
import PathPicker, { ownerFromPath } from '../components/PathPicker'
import { confirmAction } from '../components/confirm'
import { DataTable } from '../components/DataTable'

export default function Docker({ me }: { me: Me }) {
  const { t } = useTranslation()
  const docker = useApi<{ containers: Container[]; networks: DockerNetwork[] }>('/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [configModal, setConfigModal] = useState<{ path: string; focusName?: string; autoCreate?: boolean } | null>(null)
  const [pickingPath, setPickingPath] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  // Раздел показывает снимок инвентаря: при входе он пересобирается сам,
  // иначе только что поднятого контейнера в списке не окажется.
  const { rescanning, rescan } = useHostRescan({
    reload: () => docker.reload(),
    canScan: canControl,
    onNotice: (kind, text) => setNotice({ kind, text }),
  })
  const allContainers = docker.data?.containers ?? []
  const activeContainers = allContainers.filter((c) => c.running)
  const inactiveContainers = allContainers.filter((c) => !c.running)


  const containerColumns: TableColumnsType<Container> = [
    {
      title: t('docker.colContainer'),
      key: 'name',
      render: (_, c) => (
        <>
          <strong>{c.name}</strong>
          <div className="small muted nowrap">
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
        <Tooltip title={c.image !== shortImageRef(c.image) ? c.image : undefined}>
          <span className="small mono nowrap">{shortImageRef(c.image)}</span>
        </Tooltip>
      ),
    },
    {
      title: t('docker.colState'),
      key: 'state',
      // Одной строкой: «Up 8 days (healthy)» и пометки про compose
      // растили строку втрое, а читают их редко — подробности уезжают в
      // подсказку, на виду остаётся само состояние и с какого времени.
      render: (_, c) => {
        const notes = [
          c.status,
          c.declared && !c.running ? t('docker.declaredNotRunning') : '',
          !c.declared && c.running ? t('docker.runningOutsideCompose') : '',
        ].filter(Boolean)
        return (
          <Tooltip title={notes.join(' · ')}>
            <span className="nowrap">
              <StateBadge state={c.state} />
              {c.status && <span className="small muted"> · {c.status}</span>}
            </span>
          </Tooltip>
        )
      },
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
      // Одной строкой: пять кнопок столбиком растили строку впятеро, а
      // места им нужно немного. Не влезли — таблица прокручивается вбок,
      // это дешевле высоких строк.
      render: (_, c) => (
        <div className="row row-nowrap">
          {['start', 'restart', 'stop'].map((a) => (
            <Button
              key={a}
              type="link"
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
          <Button
            danger
            type="link"
            disabled={!canControl}
            loading={busy === `${c.name}:delete`}
            onClick={() => void del(c)}
          >
            {t('common.delete')}
          </Button>
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

  /**
   * Удаление контейнера.
   *
   * Запущенный docker удалять отказывается, и это правильно — остановка
   * должна быть видимым шагом. Но заставлять нажимать «stop», а потом
   * «удалить» того, кто уже сказал «удалить», незачем: спрашиваем прямо,
   * что контейнер будет остановлен, и просим docker сделать это самому.
   */
  async function del(c: Container) {
    const running = c.running
    const question = running
      ? t('docker.confirmDeleteRunning', { name: c.name })
      : t('common.confirmDelete', { what: t('docker.container'), name: c.name })
    if (!(await confirmAction(question))) return
    setBusy(`${c.name}:delete`)
    setNotice(null)
    try {
      await api(`/containers/${encodeURIComponent(c.name)}${qs({ force: running ? 'true' : '' })}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('common.deleted', { name: c.name }) })
      await api('/inventory/refresh', { method: 'POST' })
      await docker.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function containerAct(name: string, action: string) {
    if (!(await confirmAction(t('docker.confirmAction', { action, name })))) return
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
            <Button onClick={() => void rescan(t('common.hostRescanned'))} loading={rescanning}>
              {rescanning ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={docker.error} />
      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
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
              <DataTable<Container>                 dataSource={activeContainers}
                columns={containerColumns}
                rowKey="name"
              />
            </div>
          </>
        )}
      </Card>

      <Card title={t('docker.networks')}>
        <div className="table-wrap">
          <DataTable<DockerNetwork>             dataSource={docker.data?.networks ?? []}
            columns={networkColumns}
            rowKey="id"
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
