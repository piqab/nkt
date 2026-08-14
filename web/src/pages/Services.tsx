import { useState } from 'react'
import { Button, Table, Tabs, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Listener, Me, ServiceUnit } from '../types'
import { Banner, Card, ErrorNote, Loading, StateBadge, formatBytesShort } from '../components/ui'
import Misc from './Misc'

const ACTION_LABEL: Record<string, string> = {
  start: 'запустить',
  stop: 'остановить',
  restart: 'перезапустить',
  reload: 'перечитать конфиг',
  validate: 'проверить конфиг',
}

/**
 * "Сервисы" now has two tabs: systemd-юниты (below) and "Разное" — moved
 * here from "Контейнеры и ВМ". After ps/cgroup enrichment, most of what
 * shows up in "Разное" turns out to be a systemd unit nobody described in
 * any parsed config, not a container — so it belongs next to the rest of
 * the service inventory, not next to Docker/Podman/LXD/VMs.
 */
export default function Services({ me }: { me: Me }) {
  const misc = useApi<{ listeners: Listener[] }>('/misc', 60_000)

  return (
    <Tabs
      defaultActiveKey="systemd"
      items={[
        { key: 'systemd', label: 'systemd', children: <SystemdTab me={me} /> },
        {
          key: 'misc',
          label: misc.data ? `Разное (${misc.data.listeners.length})` : 'Разное',
          children: <Misc />,
        },
      ]}
    />
  )
}

function SystemdTab({ me }: { me: Me }) {
  const services = useApi<{ services: ServiceUnit[]; allow_mutations: boolean }>('/services', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  const canControl = me.is_admin && me.allow_mutations

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      services.reload()
      setNotice({ kind: 'info', text: 'Хост пересканирован.' })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(service: string, action: string) {
    if (action !== 'validate' && action !== 'reload') {
      if (!window.confirm(`Выполнить «${ACTION_LABEL[action]}» для сервиса ${service}?`)) return
    }
    setBusy(`${service}:${action}`)
    setNotice(null)
    try {
      const path = action === 'validate' ? `/services/${service}/validate` : `/services/${service}/${action}`
      const res = await api<{ output?: string; valid?: boolean; simulated?: boolean }>(path, {
        method: 'POST',
      })
      const suffix = res.simulated ? ' (симуляция, режим снапшота)' : ''
      setNotice({
        kind: res.valid === false ? 'error' : 'info',
        text: `${service}: ${ACTION_LABEL[action]} — ${res.output?.trim() || 'выполнено'}${suffix}`,
      })
      services.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<ServiceUnit> = [
    {
      title: 'Сервис',
      key: 'name',
      render: (_, s) => (
        <>
          <strong>{s.name}</strong>
          <div className="small muted">{s.description || s.unit}</div>
          {!s.installed && <div className="small muted">не установлен на хосте</div>}
        </>
      ),
    },
    {
      title: 'Состояние',
      key: 'state',
      render: (_, s) => (
        <>
          <StateBadge state={s.active_state} />
          {s.sub_state && <div className="small muted">{s.sub_state}</div>}
        </>
      ),
    },
    { title: 'Автозапуск', key: 'enabled', render: (_, s) => <span className="small">{s.enabled || '—'}</span> },
    { title: 'PID', key: 'main_pid', align: 'right', render: (_, s) => <span className="small">{s.main_pid || '—'}</span> },
    {
      title: 'Память',
      key: 'memory_bytes',
      align: 'right',
      render: (_, s) => <span className="small">{s.memory_bytes ? formatBytesShort(s.memory_bytes) : '—'}</span>,
    },
    { title: 'Перезапусков', key: 'restarts', align: 'right', render: (_, s) => <span className="small">{s.restarts ?? 0}</span> },
    {
      title: 'Действия',
      key: 'actions',
      render: (_, s) => (
        <div className="row">
          {(s.actions ?? []).map((a) => (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={!canControl}
              loading={busy === `${s.name}:${a}`}
              onClick={() => act(s.name, a)}
            >
              {ACTION_LABEL[a] ?? a}
            </Button>
          ))}
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Сервисы</h1>
          <p>
            Управление systemd-юнитами. Все действия записываются в журнал с указанием
            пользователя.
          </p>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button
              onClick={rescan}
              loading={rescanning}
              title="Список ниже — снапшот, обновляется раз в несколько минут; эта кнопка пересканирует хост сейчас"
            >
              {rescanning ? 'Сканирую…' : 'Пересканировать'}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={services.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && (
        <Banner kind="info">
          Действия недоступны: нужна роль admin и включённые изменения.
        </Banner>
      )}

      <Card title="systemd">
        {services.loading && !services.data ? (
          <Loading what="состояние сервисов" />
        ) : (
          <div className="table-wrap">
            <Table<ServiceUnit>
              dataSource={services.data?.services ?? []}
              columns={columns}
              rowKey="name"
              pagination={false}
              size="small"
            />
          </div>
        )}
      </Card>
    </>
  )
}
