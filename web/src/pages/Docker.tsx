import { useState } from 'react'
import { api, qs, useApi } from '../api'
import type { Container, DockerNetwork, FileContent, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, Modal, Spinner, StateBadge } from '../components/ui'
import BlockTree from '../components/BlockTree'
import PathPicker, { ownerFromPath } from '../components/PathPicker'

export default function Docker({ me }: { me: Me }) {
  const docker = useApi<{ containers: Container[]; networks: DockerNetwork[] }>('/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [configModal, setConfigModal] = useState<{ path: string; focusName?: string; autoCreate?: boolean } | null>(null)
  const [pickingPath, setPickingPath] = useState(false)

  const canControl = me.is_admin && me.allow_mutations

  async function containerAct(name: string, action: string) {
    if (!window.confirm(`Выполнить «${action}» для контейнера ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/containers/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${action} выполнено.` })
      docker.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Docker</h1>
          <p>Сопоставление того, что описано в compose, с тем, что реально работает.</p>
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
            <button className="ghost" onClick={() => setPickingPath(true)}>
              + новый контейнер
            </button>
          )
        }
      >
        {docker.loading && !docker.data ? (
          <Loading what="контейнеры" />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Контейнер</th>
                  <th>Пользователь</th>
                  <th>Образ</th>
                  <th>Состояние</th>
                  <th>Порты</th>
                  <th>Сети</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {(docker.data?.containers ?? []).map((c) => (
                  <tr key={c.name}>
                    <td>
                      <strong>{c.name}</strong>
                      <div className="small muted">
                        {c.project ? `${c.project}/${c.service_name}` : 'вне compose'}
                        {c.restart ? ` · restart: ${c.restart}` : ''}
                      </div>
                    </td>
                    <td className="small">{(c.compose_file && ownerFromPath(c.compose_file)) || '—'}</td>
                    <td className="small mono" style={{ wordBreak: 'break-all' }}>
                      {c.image}
                    </td>
                    <td>
                      <StateBadge state={c.state} />
                      <div className="small muted">{c.status}</div>
                      {c.declared && !c.running && <div className="small muted">описан, но не запущен</div>}
                      {!c.declared && c.running && <div className="small muted">запущен вне compose</div>}
                    </td>
                    <td className="small mono">
                      {(c.ports ?? []).length === 0
                        ? '—'
                        : (c.ports ?? []).map((p, i) => (
                            <div
                              key={i}
                              style={{
                                color:
                                  p.host_port && (!p.host_ip || p.host_ip === '0.0.0.0')
                                    ? 'var(--status-critical)'
                                    : undefined,
                              }}
                            >
                              {p.host_port
                                ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}/${p.protocol}`
                                : `${p.container_port}/${p.protocol} (не опубликован)`}
                            </div>
                          ))}
                    </td>
                    <td className="small">
                      {(c.networks ?? []).map((n) => (
                        <div key={n.name}>
                          {n.name}
                          {n.ip_address ? ` · ${n.ip_address}` : ''}
                        </div>
                      ))}
                    </td>
                    <td className="nowrap">
                      {['start', 'restart', 'stop'].map((a) => (
                        <button
                          key={a}
                          className="ghost"
                          disabled={!canControl || busy === `${c.name}:${a}`}
                          onClick={() => containerAct(c.name, a)}
                        >
                          {busy === `${c.name}:${a}` && <Spinner />}
                          {a}
                        </button>
                      ))}
                      {canControl && c.compose_file && c.service_name && (
                        <button
                          className="ghost"
                          onClick={() => setConfigModal({ path: c.compose_file!, focusName: c.service_name })}
                        >
                          редактировать конфиг
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="Сети docker">
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Сеть</th>
                <th>Драйвер</th>
                <th>Подсети</th>
                <th>Шлюз</th>
                <th>Интерфейс</th>
              </tr>
            </thead>
            <tbody>
              {(docker.data?.networks ?? []).map((n) => (
                <tr key={n.id}>
                  <td>
                    <strong>{n.name}</strong>
                    {n.internal && <div className="small muted">internal</div>}
                  </td>
                  <td className="small">{n.driver}</td>
                  <td className="small mono">{(n.subnets ?? []).join(', ') || '—'}</td>
                  <td className="small mono">{n.gateway || '—'}</td>
                  <td className="small mono">{n.bridge || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
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
