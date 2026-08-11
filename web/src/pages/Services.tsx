import { useState } from 'react'
import { api, useApi } from '../api'
import type { Me, ServiceUnit } from '../types'
import { Banner, Card, ErrorNote, Loading, Spinner, StateBadge, formatBytesShort } from '../components/ui'

const ACTION_LABEL: Record<string, string> = {
  start: 'запустить',
  stop: 'остановить',
  restart: 'перезапустить',
  reload: 'перечитать конфиг',
  validate: 'проверить конфиг',
}

export default function Services({ me }: { me: Me }) {
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
            <button onClick={rescan} disabled={rescanning} title="Список ниже — снапшот, обновляется раз в несколько минут; эта кнопка пересканирует хост сейчас">
              {rescanning && <Spinner />}
              {rescanning ? 'Сканирую…' : 'Пересканировать'}
            </button>
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
            <table>
              <thead>
                <tr>
                  <th>Сервис</th>
                  <th>Состояние</th>
                  <th>Автозапуск</th>
                  <th className="num">PID</th>
                  <th className="num">Память</th>
                  <th className="num">Перезапусков</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {(services.data?.services ?? []).map((s) => (
                  <tr key={s.name}>
                    <td>
                      <strong>{s.name}</strong>
                      <div className="small muted">{s.description || s.unit}</div>
                      {!s.installed && <div className="small muted">не установлен на хосте</div>}
                    </td>
                    <td>
                      <StateBadge state={s.active_state} />
                      {s.sub_state && <div className="small muted">{s.sub_state}</div>}
                    </td>
                    <td className="small">{s.enabled || '—'}</td>
                    <td className="num small">{s.main_pid || '—'}</td>
                    <td className="num small">{s.memory_bytes ? formatBytesShort(s.memory_bytes) : '—'}</td>
                    <td className="num small">{s.restarts ?? 0}</td>
                    <td className="nowrap">
                      {(s.actions ?? []).map((a) => (
                        <button
                          key={a}
                          className="ghost"
                          disabled={!canControl || busy === `${s.name}:${a}`}
                          onClick={() => act(s.name, a)}
                        >
                          {busy === `${s.name}:${a}` && <Spinner />}
                          {ACTION_LABEL[a] ?? a}
                        </button>
                      ))}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  )
}
