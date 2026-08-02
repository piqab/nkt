import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, qs, useApi } from '../api'
import type { Container, DockerNetwork, Me, ServiceUnit } from '../types'
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
  const docker = useApi<{ containers: Container[]; networks: DockerNetwork[] }>('/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  const canControl = me.is_admin && me.allow_mutations
  // Any container already knows the compose file that declares it; used as
  // the target for "+ новый контейнер" since there is no other way to infer
  // which compose file a brand-new service belongs in.
  const composeFile = docker.data?.containers.find((c) => c.compose_file)?.compose_file

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      services.reload()
      docker.reload()
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
      <div className="page-head spread">
        <div>
          <h1>Сервисы и контейнеры</h1>
          <p>
            Управление systemd-юнитами и контейнерами docker. Все действия записываются в журнал
            с указанием пользователя.
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
                {services.data?.services.map((s) => (
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

      <Card
        title="Контейнеры docker"
        subtitle="Сопоставление того, что описано в compose, с тем, что реально работает"
        actions={
          canControl &&
          composeFile && (
            <Link to={`/configs${qs({ path: composeFile, view: 'blocks', create: '1' })}`}>
              + новый контейнер
            </Link>
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
                  <th>Образ</th>
                  <th>Состояние</th>
                  <th>Порты</th>
                  <th>Сети</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {docker.data?.containers.map((c) => (
                  <tr key={c.name}>
                    <td>
                      <strong>{c.name}</strong>
                      <div className="small muted">
                        {c.project ? `${c.project}/${c.service_name}` : 'вне compose'}
                        {c.restart ? ` · restart: ${c.restart}` : ''}
                      </div>
                    </td>
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
                        <Link to={`/configs${qs({ path: c.compose_file, view: 'blocks', focus: c.service_name })}`}>
                          редактировать конфиг
                        </Link>
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
              {docker.data?.networks.map((n) => (
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
    </>
  )
}
