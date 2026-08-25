import { useState } from 'react'
import { Button, Form, Input, Table, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { LXDInstance, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, StateBadge } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'

export default function LXD({ me }: { me: Me }) {
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
      setNotice({ kind: 'info', text: 'Хост пересканирован.' })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(name: string, action: string) {
    if (!window.confirm(`Выполнить «${action}» для инстанса ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${action} выполнено.` })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    if (!window.confirm(`Удалить инстанс ${name}? Это необратимо.`)) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: `${name}: удалён.` })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<LXDInstance> = [
    { title: 'Имя', key: 'name', render: (_, i) => <strong>{i.name}</strong> },
    { title: 'Тип', key: 'type', render: (_, i) => <span className="small">{i.type === 'virtual-machine' ? 'VM' : 'контейнер'}</span> },
    { title: 'Состояние', key: 'status', render: (_, i) => <StateBadge state={i.status} /> },
    { title: 'Архитектура', key: 'architecture', render: (_, i) => <span className="small mono">{i.architecture || '—'}</span> },
    { title: 'IPv4', key: 'ipv4', render: (_, i) => <span className="small mono">{(i.ipv4 ?? []).join(', ') || '—'}</span> },
    {
      title: 'Действия',
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
          <h1>
            LXD
            <InfoHint>Контейнеры и виртуальные машины LXD — один инструмент управляет обоими типами инстансов.</InfoHint>
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

      <ErrorNote error={instances.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && (
        <Banner kind="info">Действия недоступны: нужна роль admin и включённые изменения.</Banner>
      )}

      <Card
        title="Инстансы LXD"
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreating(true)}>
              + новый инстанс
            </Button>
          )
        }
      >
        {instances.loading && !instances.data ? (
          <Loading what="инстансы LXD" />
        ) : allInstances.length === 0 ? (
          <p className="small muted">LXD не обнаружен или инстансов нет.</p>
        ) : (
          <>
            <InactiveSummary
              items={inactiveInstances}
              getKey={(i) => i.name}
              getLabel={(i) => i.name}
              getTooltip={(i) => (
                <>
                  <div>{i.type === 'virtual-machine' ? 'VM' : 'контейнер'}</div>
                  <div>состояние: {i.status}</div>
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
          Новый инстанс LXD
          <InfoHint>lxc launch — образ скачивается автоматически, инстанс сразу запускается.</InfoHint>
        </>
      }
      actions={
        <Button type="link" onClick={onClose}>
          закрыть
        </Button>
      }
    >
      <Form<CreateInstanceValues> layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="image" label="Образ" rules={[{ required: true }]} style={{ flex: 1, minWidth: '16rem' }}>
            <Input placeholder="ubuntu:24.04" />
          </Form.Item>
          <Form.Item name="name" label="Имя инстанса" rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input placeholder="my-instance" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? 'Запускаю…' : 'Создать и запустить'}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  )
}
