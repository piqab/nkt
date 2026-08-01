import { useMemo, useState } from 'react'
import { api, useApi } from '../api'
import type { FirewallPolicy, FirewallRule, Listener, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, Spinner, StateBadge } from '../components/ui'
import { formatBytes, formatNumber } from '../components/charts'

interface FirewallResponse {
  ufw_active: boolean
  ufw_policy: string
  backends: string[]
  policies: FirewallPolicy[]
  rules: FirewallRule[]
  listeners: Listener[]
}

interface NumberedRule {
  number: number
  text: string
}

export default function Firewall({ me }: { me: Me }) {
  const fw = useApi<FirewallResponse>('/firewall', 60_000)
  const numbered = useApi<{ rules: NumberedRule[] }>('/firewall/rules', 60_000)
  const [backend, setBackend] = useState('')
  const [chain, setChain] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [form, setForm] = useState({ action: 'allow', port: '', protocol: 'tcp', from: '', comment: '' })

  const canControl = me.is_admin && me.allow_mutations

  const chains = useMemo(() => {
    const set = new Set<string>()
    fw.data?.rules.forEach((r) => set.add(r.chain))
    return [...set].sort()
  }, [fw.data])

  const rules = useMemo(() => {
    let list = fw.data?.rules ?? []
    if (backend) list = list.filter((r) => r.backend === backend)
    if (chain) list = list.filter((r) => r.chain === chain)
    return list
  }, [fw.data, backend, chain])

  async function addRule(event: React.FormEvent) {
    event.preventDefault()
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<{ output?: string; simulated?: boolean }>('/firewall/rules', {
        method: 'POST',
        body: {
          action: form.action,
          port: Number(form.port),
          protocol: form.protocol,
          from: form.from,
          comment: form.comment,
        },
      })
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}`,
      })
      setForm({ ...form, port: '', comment: '' })
      fw.reload()
      numbered.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function deleteRule(rule: NumberedRule) {
    if (!window.confirm(`Удалить правило ufw №${rule.number}?\n\n${rule.text}`)) return
    setBusy(true)
    setNotice(null)
    try {
      await api(`/firewall/rules/${rule.number}`, { method: 'DELETE', body: { expected: rule.text } })
      setNotice({ kind: 'info', text: `Правило №${rule.number} удалено.` })
      fw.reload()
      numbered.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  const listeningPorts = useMemo(() => {
    const set = new Set<number>()
    fw.data?.listeners.forEach((l) => set.add(l.port))
    return set
  }, [fw.data])

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Firewall</h1>
          <p>
            Полный набор правил iptables и ufw со счётчиками. Правила меняются только через ufw:
            прямая правка iptables из веб-интерфейса — верный способ потерять доступ к серверу.
          </p>
        </div>
      </div>

      <ErrorNote error={fw.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      {fw.data && (
        <div className="grid grid-3">
          <Card title="ufw">
            <div className="row">
              <StateBadge state={fw.data.ufw_active ? 'active' : 'inactive'} />
              <span className="small secondary">{fw.data.ufw_policy || 'политика не прочитана'}</span>
            </div>
            {canControl && (
              <button
                style={{ marginTop: '0.6rem' }}
                disabled={busy}
                onClick={async () => {
                  setBusy(true)
                  try {
                    await api('/firewall/reload', { method: 'POST' })
                    setNotice({ kind: 'info', text: 'ufw перезагружен.' })
                    fw.reload()
                  } catch (err) {
                    setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
                  } finally {
                    setBusy(false)
                  }
                }}
              >
                {busy && <Spinner />}
                Перезагрузить ufw
              </button>
            )}
          </Card>

          <Card title="Политики цепочек">
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Цепочка</th>
                    <th>Политика</th>
                    <th className="num">Пакетов</th>
                  </tr>
                </thead>
                <tbody>
                  {fw.data.policies
                    .filter((p) => p.policy !== '-')
                    .map((p) => (
                      <tr key={`${p.backend}/${p.table}/${p.chain}`}>
                        <td className="mono small">
                          {p.backend}/{p.table}/{p.chain}
                        </td>
                        <td className="mono small">{p.policy}</td>
                        <td className="num small">{formatNumber(p.packets)}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </Card>

          {canControl && (
            <Card title="Добавить правило" subtitle="Через ufw, с записью в журнал">
              <form className="col" onSubmit={addRule}>
                <div className="row">
                  <label style={{ flex: 1 }}>
                    Действие
                    <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })}>
                      <option value="allow">allow</option>
                      <option value="deny">deny</option>
                      <option value="reject">reject</option>
                      <option value="limit">limit</option>
                    </select>
                  </label>
                  <label style={{ flex: 1 }}>
                    Порт
                    <input
                      value={form.port}
                      onChange={(e) => setForm({ ...form, port: e.target.value })}
                      inputMode="numeric"
                      required
                    />
                  </label>
                  <label style={{ flex: 1 }}>
                    Протокол
                    <select value={form.protocol} onChange={(e) => setForm({ ...form, protocol: e.target.value })}>
                      <option value="tcp">tcp</option>
                      <option value="udp">udp</option>
                    </select>
                  </label>
                </div>
                <label>
                  Источник (IP или CIDR, пусто = отовсюду)
                  <input
                    value={form.from}
                    onChange={(e) => setForm({ ...form, from: e.target.value })}
                    placeholder="10.10.0.0/24"
                  />
                </label>
                <label>
                  Комментарий
                  <input value={form.comment} onChange={(e) => setForm({ ...form, comment: e.target.value })} />
                </label>
                <button className="primary" type="submit" disabled={busy}>
                  {busy && <Spinner />}
                  Добавить правило
                </button>
              </form>
            </Card>
          )}
        </div>
      )}

      {canControl && numbered.data?.rules.length ? (
        <Card
          title="Правила ufw"
          subtitle="Номера сдвигаются после каждого изменения, поэтому удаление сверяется с тем текстом, который вы видите."
        >
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th className="num">№</th>
                  <th>Правило</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {numbered.data.rules.map((r) => (
                  <tr key={r.number}>
                    <td className="num">{r.number}</td>
                    <td className="mono small">{r.text}</td>
                    <td>
                      <button className="danger ghost" disabled={busy} onClick={() => deleteRule(r)}>
                        {busy && <Spinner />}
                        удалить
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ) : null}

      <Card
        title="Все правила пакетного фильтра"
        subtitle="Счётчики берутся из iptables-save -c: нулевой счётчик означает, что правило ни разу не сработало."
        actions={
          <>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              backend
              <select value={backend} onChange={(e) => setBackend(e.target.value)}>
                <option value="">все</option>
                {(fw.data?.backends ?? []).map((b) => (
                  <option key={b} value={b}>
                    {b}
                  </option>
                ))}
              </select>
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              цепочка
              <select value={chain} onChange={(e) => setChain(e.target.value)}>
                <option value="">все</option>
                {chains.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </label>
          </>
        }
      >
        {fw.loading && !fw.data ? (
          <Loading what="правила" />
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Цепочка</th>
                  <th>Действие</th>
                  <th>Порт</th>
                  <th>Источник</th>
                  <th>Кем создано</th>
                  <th className="num">Пакетов</th>
                  <th className="num">Байт</th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r) => (
                  <tr key={r.id}>
                    <td className="mono small nowrap">
                      {r.backend}/{r.table ? `${r.table}/` : ''}
                      {r.chain}
                    </td>
                    <td className="mono small">
                      {r.action}
                      {r.dnat_to && <div className="small muted">→ {r.dnat_to}</div>}
                    </td>
                    <td className="mono small">
                      {r.port_spec || '—'}
                      {r.protocol ? `/${r.protocol}` : ''}
                      {r.ports?.length === 1 && !listeningPorts.has(r.ports[0]) && (
                        <div className="small" style={{ color: 'var(--status-warning)' }}>
                          никто не слушает
                        </div>
                      )}
                    </td>
                    <td className="mono small">{r.source || 'any'}</td>
                    <td className="small">{r.managed_by || '—'}</td>
                    <td className="num small">{formatNumber(r.packets)}</td>
                    <td className="num small">{formatBytes(r.bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="Открытые сокеты хоста" subtitle="Вывод ss: то, что действительно слушает порты">
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Протокол</th>
                <th>Адрес</th>
                <th className="num">Порт</th>
                <th>Процесс</th>
                <th>Доступность</th>
              </tr>
            </thead>
            <tbody>
              {(fw.data?.listeners ?? []).map((l, i) => (
                <tr key={i}>
                  <td className="small mono">{l.protocol}</td>
                  <td className="small mono">{l.address}</td>
                  <td className="num small">{l.port}</td>
                  <td className="small">
                    {l.process || '—'}
                    {l.pid ? ` (${l.pid})` : ''}
                  </td>
                  <td className="small">
                    {l.address === '0.0.0.0' || l.address === '::' ? (
                      <span className="badge sev-medium">
                        <span className="badge-dot" />
                        все интерфейсы
                      </span>
                    ) : (
                      <span className="badge sev-ok">
                        <span className="badge-dot" />
                        локально
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  )
}
