import { useMemo, useState } from 'react'
import { Badge, Button, Form, Input, Select, Table, Tag, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { FirewallPolicy, FirewallRule, Listener, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, StateBadge } from '../components/ui'
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

/** ufw's own text format for one `ufw status numbered` line — e.g.
 * "80/tcp                     ALLOW IN    Anywhere" or
 * "22/tcp (OpenSSH)           ALLOW IN    Anywhere (v6)" — the leading
 * token is the port (optionally "/tcp" or "/udp"), and ALLOW/DENY/REJECT/
 * LIMIT tells what it actually does once matched. */
function parseNumberedRule(text: string): { port: number; protocol?: string; action?: string } | null {
  const portMatch = text.match(/^(\d+)(?:\/(tcp|udp))?/)
  if (!portMatch) return null
  const actionMatch = text.match(/\b(ALLOW|DENY|REJECT|LIMIT)\b/)
  return { port: Number(portMatch[1]), protocol: portMatch[2], action: actionMatch?.[1] }
}

const UFW_ACTION_LABEL: Record<string, string> = {
  ALLOW: 'разрешено',
  DENY: 'запрещено',
  REJECT: 'отклонено',
  LIMIT: 'ограничено (limit)',
}
const UFW_ACTION_COLOR: Record<string, string> = {
  ALLOW: 'success',
  DENY: 'error',
  REJECT: 'error',
  LIMIT: 'warning',
}

type AddRuleValues = {
  action: string
  port: string
  protocol: string
  from: string
  comment: string
}

export default function Firewall({ me }: { me: Me }) {
  const fw = useApi<FirewallResponse>('/firewall', 60_000)
  const numbered = useApi<{ rules: NumberedRule[] }>('/firewall/rules', 60_000)
  const [backend, setBackend] = useState('')
  const [chain, setChain] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [addForm] = Form.useForm<AddRuleValues>()

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

  async function addRule(values: AddRuleValues) {
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<{ output?: string; simulated?: boolean }>('/firewall/rules', {
        method: 'POST',
        body: {
          action: values.action,
          port: Number(values.port),
          protocol: values.protocol,
          from: values.from,
          comment: values.comment,
        },
      })
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}`,
      })
      addForm.setFieldsValue({ port: '', comment: '' })
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

  /** "Открытые сокеты хоста" → allow — the same one-off, no-source-restriction
   * shape as the form above, just triggered straight from the socket's own
   * row instead of typing the port in by hand. */
  async function quickAllowListener(l: Listener) {
    if (!window.confirm(`Разрешить в ufw ${l.port}/${l.protocol} от любого источника?`)) return
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<{ output?: string; simulated?: boolean }>('/firewall/rules', {
        method: 'POST',
        body: { action: 'allow', port: l.port, protocol: l.protocol, from: '', comment: '' },
      })
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}`,
      })
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

  /** Which numbered ufw rule (if any) actually governs a given socket —
   * matched on port and, when the rule names one, protocol. ufw's own
   * status text is the only place this pairing exists; the structured
   * FirewallRule view (from iptables-save) knows a rule came from ufw
   * (ManagedBy) but not which ufw index deleting it needs. */
  function ufwRuleForListener(l: Listener): { rule: NumberedRule; parsed: NonNullable<ReturnType<typeof parseNumberedRule>> } | null {
    for (const rule of numbered.data?.rules ?? []) {
      const parsed = parseNumberedRule(rule.text)
      if (parsed && parsed.port === l.port && (!parsed.protocol || parsed.protocol === l.protocol)) {
        return { rule, parsed }
      }
    }
    return null
  }

  const policyColumns: TableColumnsType<FirewallPolicy> = [
    { title: 'Цепочка', key: 'chain', render: (_, p) => <span className="mono small">{p.backend}/{p.table}/{p.chain}</span> },
    { title: 'Политика', dataIndex: 'policy', key: 'policy', className: 'mono small' },
    { title: 'Пакетов', key: 'packets', align: 'right', render: (_, p) => <span className="num small">{formatNumber(p.packets)}</span> },
  ]

  const numberedColumns: TableColumnsType<NumberedRule> = [
    { title: '№', dataIndex: 'number', key: 'number', align: 'right' },
    { title: 'Правило', dataIndex: 'text', key: 'text', className: 'mono small' },
    {
      title: '',
      key: 'actions',
      render: (_, r) => (
        <Button danger type="link" size="small" loading={busy} onClick={() => deleteRule(r)}>
          удалить
        </Button>
      ),
    },
  ]

  const ruleColumns: TableColumnsType<FirewallRule> = [
    {
      title: 'Цепочка',
      key: 'chain',
      render: (_, r) => (
        <span className="mono small nowrap">
          {r.backend}/{r.table ? `${r.table}/` : ''}
          {r.chain}
        </span>
      ),
    },
    {
      title: 'Действие',
      key: 'action',
      render: (_, r) => (
        <span className="mono small">
          {r.action}
          {r.dnat_to && <div className="small muted">→ {r.dnat_to}</div>}
        </span>
      ),
    },
    {
      title: 'Порт',
      key: 'port',
      render: (_, r) => (
        <span className="mono small">
          {r.port_spec || '—'}
          {r.protocol ? `/${r.protocol}` : ''}
          {r.ports?.length === 1 && !listeningPorts.has(r.ports[0]) && (
            <div className="small" style={{ color: 'var(--status-warning)' }}>
              никто не слушает
            </div>
          )}
        </span>
      ),
    },
    { title: 'Источник', key: 'source', render: (_, r) => <span className="mono small">{r.source || 'any'}</span> },
    { title: 'Кем создано', key: 'managed_by', render: (_, r) => <span className="small">{r.managed_by || '—'}</span> },
    { title: 'Пакетов', key: 'packets', align: 'right', render: (_, r) => <span className="num small">{formatNumber(r.packets)}</span> },
    { title: 'Байт', key: 'bytes', align: 'right', render: (_, r) => <span className="num small">{formatBytes(r.bytes)}</span> },
  ]

  const listenerColumns: TableColumnsType<Listener> = [
    { title: 'Протокол', key: 'protocol', render: (_, l) => <span className="small mono">{l.protocol}</span> },
    { title: 'Адрес', key: 'address', render: (_, l) => <span className="small mono">{l.address}</span> },
    { title: 'Порт', key: 'port', align: 'right', render: (_, l) => <span className="num small">{l.port}</span> },
    {
      title: 'Процесс',
      key: 'process',
      render: (_, l) => (
        <span className="small">
          {l.process || '—'}
          {l.pid ? ` (${l.pid})` : ''}
        </span>
      ),
    },
    {
      title: 'Доступность',
      key: 'exposure',
      render: (_, l) =>
        l.address === '0.0.0.0' || l.address === '::' ? (
          <Badge color="var(--status-warning)" text="все интерфейсы" />
        ) : (
          <Badge color="var(--status-good)" text="локально" />
        ),
    },
    {
      title: 'Правило ufw',
      key: 'ufw_rule',
      render: (_, l) => {
        const found = ufwRuleForListener(l)
        if (!found) return <Tag>нет правила</Tag>
        const { action } = found.parsed
        return (
          <>
            <Tag color={(action && UFW_ACTION_COLOR[action]) || 'default'}>
              {(action && UFW_ACTION_LABEL[action]) || action || 'правило есть'}
            </Tag>
            {!fw.data?.ufw_active && <div className="small muted">ufw выключен — не действует</div>}
          </>
        )
      },
    },
    ...(canControl
      ? [
          {
            title: '',
            key: 'ufw_actions',
            render: (_: unknown, l: Listener) => {
              const found = ufwRuleForListener(l)
              return found ? (
                <Button danger type="link" size="small" loading={busy} onClick={() => deleteRule(found.rule)}>
                  удалить
                </Button>
              ) : (
                <Button type="link" size="small" loading={busy} onClick={() => quickAllowListener(l)}>
                  разрешить
                </Button>
              )
            },
          } satisfies TableColumnsType<Listener>[number],
        ]
      : []),
  ]

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
              <Button
                style={{ marginTop: '0.6rem' }}
                loading={busy}
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
                Перезагрузить ufw
              </Button>
            )}
          </Card>

          <Card title="Политики цепочек">
            <div className="table-wrap">
              <Table<FirewallPolicy>
                dataSource={fw.data.policies.filter((p) => p.policy !== '-')}
                columns={policyColumns}
                rowKey={(p) => `${p.backend}/${p.table}/${p.chain}`}
                pagination={false}
                size="small"
              />
            </div>
          </Card>

          {canControl && (
            <Card title="Добавить правило" subtitle="Через ufw, с записью в журнал">
              <Form<AddRuleValues>
                form={addForm}
                layout="vertical"
                onFinish={addRule}
                initialValues={{ action: 'allow', port: '', protocol: 'tcp', from: '', comment: '' }}
              >
                <div className="row">
                  <Form.Item name="action" label="Действие" style={{ flex: 1 }}>
                    <Select
                      options={['allow', 'deny', 'reject', 'limit'].map((v) => ({ value: v, label: v }))}
                    />
                  </Form.Item>
                  <Form.Item name="port" label="Порт" rules={[{ required: true }]} style={{ flex: 1 }}>
                    <Input inputMode="numeric" />
                  </Form.Item>
                  <Form.Item name="protocol" label="Протокол" style={{ flex: 1 }}>
                    <Select options={['tcp', 'udp'].map((v) => ({ value: v, label: v }))} />
                  </Form.Item>
                </div>
                <Form.Item name="from" label="Источник (IP или CIDR, пусто = отовсюду)">
                  <Input placeholder="10.10.0.0/24" />
                </Form.Item>
                <Form.Item name="comment" label="Комментарий">
                  <Input />
                </Form.Item>
                <Form.Item style={{ marginBottom: 0 }}>
                  <Button type="primary" htmlType="submit" loading={busy}>
                    Добавить правило
                  </Button>
                </Form.Item>
              </Form>
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
            <Table<NumberedRule>
              dataSource={numbered.data.rules}
              columns={numberedColumns}
              rowKey="number"
              pagination={false}
              size="small"
            />
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
              <Select
                value={backend}
                onChange={setBackend}
                style={{ minWidth: '8rem' }}
                options={[{ value: '', label: 'все' }, ...(fw.data?.backends ?? []).map((b) => ({ value: b, label: b }))]}
              />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              цепочка
              <Select
                value={chain}
                onChange={setChain}
                style={{ minWidth: '8rem' }}
                options={[{ value: '', label: 'все' }, ...chains.map((c) => ({ value: c, label: c }))]}
              />
            </label>
          </>
        }
      >
        {fw.loading && !fw.data ? (
          <Loading what="правила" />
        ) : (
          <div className="table-wrap">
            <Table<FirewallRule> dataSource={rules} columns={ruleColumns} rowKey="id" pagination={false} size="small" />
          </div>
        )}
      </Card>

      <Card title="Открытые сокеты хоста" subtitle="Вывод ss: то, что действительно слушает порты">
        <div className="table-wrap">
          <Table<Listener>
            dataSource={fw.data?.listeners ?? []}
            columns={listenerColumns}
            rowKey={(_, i) => i ?? 0}
            pagination={false}
            size="small"
          />
        </div>
      </Card>
    </>
  )
}
