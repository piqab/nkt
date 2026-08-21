import { useState } from 'react'
import { Button, Form, Input, Table, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Me, PodmanContainer } from '../types'
import { Banner, Card, ErrorNote, Loading, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'

export default function Podman({ me }: { me: Me }) {
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
      containers.reload()
      setNotice({ kind: 'info', text: 'Хост пересканирован.' })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(name: string, action: string) {
    if (!window.confirm(`Выполнить «${action}» для контейнера ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/podman/containers/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${action} выполнено.` })
      containers.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    if (!window.confirm(`Удалить контейнер ${name}? Это необратимо.`)) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/podman/containers/${name}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: `${name}: удалён.` })
      containers.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<PodmanContainer> = [
    { title: 'Контейнер', key: 'name', render: (_, c) => <strong>{c.name}</strong> },
    { title: 'Под', key: 'pod', render: (_, c) => <span className="small">{c.pod || '—'}</span> },
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
                <div key={i}>
                  {p.host_port
                    ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}/${p.protocol}`
                    : `${p.container_port}/${p.protocol} (не опубликован)`}
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
              onClick={() => act(c.name, a)}
            >
              {a}
            </Button>
          ))}
          {canControl && (
            <Button danger type="link" size="small" loading={busy === `${c.name}:delete`} onClick={() => del(c.name)}>
              удалить
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
          <h1>Podman</h1>
          <p>Контейнеры отдельного от docker движка Podman — те же операции: запуск, остановка, удаление.</p>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button onClick={rescan} loading={rescanning}>
              {rescanning ? 'Сканирую…' : 'Пересканировать'}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={containers.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && (
        <Banner kind="info">Действия недоступны: нужна роль admin и включённые изменения.</Banner>
      )}

      <Card
        title="Контейнеры Podman"
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreating(true)}>
              + новый контейнер
            </Button>
          )
        }
      >
        {containers.loading && !containers.data ? (
          <Loading what="контейнеры Podman" />
        ) : allContainers.length === 0 ? (
          <p className="small muted">Podman не обнаружен или контейнеров нет.</p>
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
      title="Новый контейнер Podman"
      subtitle="Образ будет скачан (если нужно), контейнер создан и сразу запущен."
      actions={
        <Button type="link" onClick={onClose}>
          закрыть
        </Button>
      }
    >
      <Form<CreateContainerValues> layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="image" label="Образ" rules={[{ required: true }]} style={{ flex: 1, minWidth: '16rem' }}>
            <Input placeholder="docker.io/library/nginx:1.27" />
          </Form.Item>
          <Form.Item name="name" label="Имя контейнера" rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input placeholder="my-container" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? 'Создаю…' : 'Создать и запустить'}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
