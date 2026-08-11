import { FormEvent, useState } from 'react'
import { api, useApi } from '../api'
import type { LXDInstance, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, Spinner, StateBadge } from '../components/ui'

export default function LXD({ me }: { me: Me }) {
  const instances = useApi<{ instances: LXDInstance[] }>('/lxd/instances', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState(false)

  const canControl = me.is_admin && me.allow_mutations

  async function act(name: string, action: string) {
    if (!window.confirm(`Выполнить «${action}» для инстанса ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${action} выполнено.` })
      instances.reload()
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
      instances.reload()
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
          <h1>LXD</h1>
          <p>Контейнеры и виртуальные машины LXD — один инструмент управляет обоими типами инстансов.</p>
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
            <button className="ghost" onClick={() => setCreating(true)}>
              + новый инстанс
            </button>
          )
        }
      >
        {instances.loading && !instances.data ? (
          <Loading what="инстансы LXD" />
        ) : (instances.data?.instances ?? []).length === 0 ? (
          <p className="small muted">LXD не обнаружен или инстансов нет.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Имя</th>
                  <th>Тип</th>
                  <th>Состояние</th>
                  <th>Архитектура</th>
                  <th>IPv4</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {(instances.data?.instances ?? []).map((i) => (
                  <tr key={i.name}>
                    <td>
                      <strong>{i.name}</strong>
                    </td>
                    <td className="small">{i.type === 'virtual-machine' ? 'VM' : 'контейнер'}</td>
                    <td>
                      <StateBadge state={i.status} />
                    </td>
                    <td className="small mono">{i.architecture || '—'}</td>
                    <td className="small mono">{(i.ipv4 ?? []).join(', ') || '—'}</td>
                    <td className="nowrap">
                      {['start', 'restart', 'stop', 'pause'].map((a) => (
                        <button
                          key={a}
                          className="ghost"
                          disabled={!canControl || busy === `${i.name}:${a}`}
                          onClick={() => act(i.name, a)}
                        >
                          {busy === `${i.name}:${a}` && <Spinner />}
                          {a}
                        </button>
                      ))}
                      {canControl && (
                        <button
                          className="ghost"
                          disabled={busy === `${i.name}:delete`}
                          onClick={() => del(i.name)}
                        >
                          {busy === `${i.name}:delete` && <Spinner />}
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

function CreateInstanceForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [image, setImage] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api('/lxd/instances', { method: 'POST', body: { image, name } })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title="Новый инстанс LXD"
      subtitle="lxc launch — образ скачивается автоматически, инстанс сразу запускается."
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
            <input value={image} onChange={(e) => setImage(e.target.value)} placeholder="ubuntu:24.04" required />
          </label>
          <label style={{ flex: 1, minWidth: '12rem' }}>
            Имя инстанса
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-instance" required />
          </label>
        </div>
        <div>
          <button className="primary" type="submit" disabled={busy}>
            {busy && <Spinner />}
            {busy ? 'Запускаю…' : 'Создать и запустить'}
          </button>
        </div>
      </form>
    </Card>
  )
}
