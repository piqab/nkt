import { FormEvent, useState } from 'react'
import { api, useApi } from '../api'
import type { Me, PodmanContainer } from '../types'
import { Banner, Card, ErrorNote, Loading, Spinner, StateBadge } from '../components/ui'

export default function Podman({ me }: { me: Me }) {
  const containers = useApi<{ containers: PodmanContainer[] }>('/podman/containers', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState(false)

  const canControl = me.is_admin && me.allow_mutations

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

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Podman</h1>
          <p>Контейнеры отдельного от docker движка Podman — те же операции: запуск, остановка, удаление.</p>
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
            <button className="ghost" onClick={() => setCreating(true)}>
              + новый контейнер
            </button>
          )
        }
      >
        {containers.loading && !containers.data ? (
          <Loading what="контейнеры Podman" />
        ) : (containers.data?.containers ?? []).length === 0 ? (
          <p className="small muted">Podman не обнаружен или контейнеров нет.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Контейнер</th>
                  <th>Под</th>
                  <th>Образ</th>
                  <th>Состояние</th>
                  <th>Порты</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {(containers.data?.containers ?? []).map((c) => (
                  <tr key={c.id}>
                    <td>
                      <strong>{c.name}</strong>
                    </td>
                    <td className="small">{c.pod || '—'}</td>
                    <td className="small mono" style={{ wordBreak: 'break-all' }}>
                      {c.image}
                    </td>
                    <td>
                      <StateBadge state={c.state} />
                      <div className="small muted">{c.status}</div>
                    </td>
                    <td className="small mono">
                      {(c.ports ?? []).length === 0
                        ? '—'
                        : (c.ports ?? []).map((p, i) => (
                            <div key={i}>
                              {p.host_port
                                ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}/${p.protocol}`
                                : `${p.container_port}/${p.protocol} (не опубликован)`}
                            </div>
                          ))}
                    </td>
                    <td className="nowrap">
                      {['start', 'restart', 'stop'].map((a) => (
                        <button
                          key={a}
                          className="ghost"
                          disabled={!canControl || busy === `${c.name}:${a}`}
                          onClick={() => act(c.name, a)}
                        >
                          {busy === `${c.name}:${a}` && <Spinner />}
                          {a}
                        </button>
                      ))}
                      {canControl && (
                        <button
                          className="ghost"
                          disabled={busy === `${c.name}:delete`}
                          onClick={() => del(c.name)}
                        >
                          {busy === `${c.name}:delete` && <Spinner />}
                          удалить
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

function CreateContainerForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [image, setImage] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api('/podman/containers', { method: 'POST', body: { image, name } })
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
        <button className="ghost" onClick={onClose}>
          закрыть
        </button>
      }
    >
      <form className="col" onSubmit={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <label style={{ flex: 1, minWidth: '16rem' }}>
            Образ
            <input value={image} onChange={(e) => setImage(e.target.value)} placeholder="docker.io/library/nginx:1.27" required />
          </label>
          <label style={{ flex: 1, minWidth: '12rem' }}>
            Имя контейнера
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-container" required />
          </label>
        </div>
        <div>
          <button className="primary" type="submit" disabled={busy}>
            {busy && <Spinner />}
            {busy ? 'Создаю…' : 'Создать и запустить'}
          </button>
        </div>
      </form>
    </Card>
  )
}
