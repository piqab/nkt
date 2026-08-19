import { useMemo, useState } from 'react'
import { Badge, Button, Checkbox, Form, Input, Radio, Select, Table, Tag, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { FirewallManagerState, FirewallPolicy, FirewallRule, Listener, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, StateBadge } from '../components/ui'
import { formatBytes, formatNumber } from '../components/charts'
import PackageInstallModal from '../components/PackageInstallModal'

interface FirewallResponse {
  managers: FirewallManagerState[]
  backends: string[]
  policies: FirewallPolicy[]
  rules: FirewallRule[]
  listeners: Listener[]
}

/** Everything that differs between the two managers' install/reload
 * endpoints — the rest of the page logic is shared. */
const MANAGER_META: Record<string, { label: string; reloadPath: string; installWsPath: string; installStatusPath: string }> = {
  ufw: {
    label: 'ufw',
    reloadPath: '/firewall/reload',
    installWsPath: '/firewall/ufw-install/ws',
    installStatusPath: '/firewall/ufw-install/status',
  },
  firewalld: {
    label: 'firewalld',
    reloadPath: '/firewall/firewalld/reload',
    installWsPath: '/firewall/firewalld-install/ws',
    installStatusPath: '/firewall/firewalld-install/status',
  },
}

interface NumberedRule {
  number: number
  text: string
}

/** One line of `ufw show added` — what ufw has stored regardless of
 * whether it's currently active. Unlike NumberedRule (`ufw status
 * numbered`, which prints nothing but "Status: inactive" while ufw is
 * off), this is the only way to tell "no rule" apart from "a rule exists
 * but ufw can't currently report it" on a host where ufw isn't running. */
interface AddedRule {
  spec: string
  action?: string
  port?: number
  protocol?: string
}

/** What ufwRuleForListener found, and where — numbered (ufw active, has a
 * real index to delete by) or added-only (ufw inactive, only removable by
 * re-specifying the rule). */
type ListenerRuleMatch =
  | { source: 'numbered'; rule: NumberedRule; action?: string }
  | { source: 'added'; added: AddedRule; action?: string }

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

// Ports where getting this wrong can lock every future connection out —
// not just this one. An already-open SSH session usually survives a bad
// rule change (ufw's default chain accepts ESTABLISHED,RELATED traffic
// ahead of user rules), but the moment that session drops — a network
// blip, a laptop going to sleep — the NEXT connection attempt is exactly
// what the new rule blocks, and recovering from that needs a hosting
// provider's console or physical access: precisely the channel that just
// got cut. 22 is the one port worth hardcoding a warning for; nkt's own
// web port has no fixed number to check against the same way.
const CRITICAL_PORTS = new Set([22])

function confirmCriticalPort(port: number, action: string): boolean {
  if (!CRITICAL_PORTS.has(port)) return true
  return window.confirm(
    `ВНИМАНИЕ: порт ${port} — стандартный порт SSH.\n\n` +
      `${action} может отрезать доступ по SSH к этому хосту. Уже открытая сессия обычно не ` +
      `оборвётся сразу, но следующее подключение — не пройдёт. Восстановить доступ тогда можно ` +
      `будет только через консоль хостинг-провайдера или физически.\n\n` +
      `Точно продолжить?`,
  )
}

type AddRuleValues = {
  action: string
  port: string
  protocol: string
  from: string
  comment: string
}

// firewalld zones aren't discoverable from a fixed list the way ufw's four
// actions are — these are just the common defaults every install ships
// with, merged with whatever zones this host's own rules already mention,
// so a host with custom zones still offers them without hardcoding more.
const COMMON_FIREWALLD_ZONES = ['public', 'trusted', 'home', 'work', 'internal', 'external', 'dmz', 'block', 'drop']

type AddFirewalldValues = {
  zone: string
  targetType: 'port' | 'service'
  port: string
  protocol: string
  service: string
  runtime: boolean
  permanent: boolean
}

export default function Firewall({ me }: { me: Me }) {
  const fw = useApi<FirewallResponse>('/firewall', 60_000)
  const numbered = useApi<{ rules: NumberedRule[]; added: AddedRule[] }>('/firewall/rules', 60_000)
  const [backend, setBackend] = useState('')
  const [chain, setChain] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [addForm] = Form.useForm<AddRuleValues>()
  const [addFirewalldForm] = Form.useForm<AddFirewalldValues>()
  const [installTarget, setInstallTarget] = useState<'ufw' | 'firewalld' | null>(null)
  const [installOutcome, setInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  const [rescanningAfterInstall, setRescanningAfterInstall] = useState(false)

  const canControl = me.is_admin && me.allow_mutations

  // Polled independently of whether the install dialog is open, same reason
  // as Overview's /updates/status: without it there's no way to tell, from
  // the button alone, whether opening the dialog would reattach to an
  // install already running (started earlier, or by someone else) or start
  // a fresh one. Two backends, two independent sessions — a hook can't be
  // called conditionally/in a loop, so this is unrolled rather than looped.
  const { data: ufwInstallStatus, reload: reloadUfwInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>(MANAGER_META.ufw.installStatusPath, 5_000)
  const { data: firewalldInstallStatus, reload: reloadFirewalldInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>(MANAGER_META.firewalld.installStatusPath, 5_000)

  function installActiveFor(name: string): boolean {
    return name === 'ufw' ? (ufwInstallStatus?.active ?? false) : (firewalldInstallStatus?.active ?? false)
  }

  /** Fires the moment the install session's socket closes (apt actually
   * exited) — a plain fw.reload() alone would still show installed: false
   * until this runs, since /firewall serves the last scan. */
  async function handleInstallFinished() {
    if (!installTarget) return
    const meta = MANAGER_META[installTarget]
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(meta.installStatusPath).catch(() => null)
    if (installTarget === 'ufw') reloadUfwInstallStatus()
    else reloadFirewalldInstallStatus()
    if (fresh?.succeeded) {
      setInstallOutcome({ ok: true })
      setRescanningAfterInstall(true)
      try {
        await api('/inventory/refresh', { method: 'POST' })
        fw.reload()
        numbered.reload()
      } finally {
        setRescanningAfterInstall(false)
      }
    } else {
      setInstallOutcome({ ok: false, exitCode: fresh?.exit_code })
    }
  }

  async function reloadManager(name: string) {
    setBusy(true)
    setNotice(null)
    try {
      await api(MANAGER_META[name].reloadPath, { method: 'POST' })
      setNotice({ kind: 'info', text: `${MANAGER_META[name].label} перезагружен.` })
      fw.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

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

  const ufwManager = fw.data?.managers.find((m) => m.name === 'ufw')
  const firewalldManager = fw.data?.managers.find((m) => m.name === 'firewalld')

  const knownZones = useMemo(() => {
    const set = new Set(COMMON_FIREWALLD_ZONES)
    fw.data?.rules.forEach((r) => {
      if (r.zone) set.add(r.zone)
    })
    return [...set].sort()
  }, [fw.data])

  async function addRule(values: AddRuleValues) {
    const port = Number(values.port)
    if (values.action !== 'allow' && !confirmCriticalPort(port, `«${values.action}» для порта ${port}`)) return
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
      const ufwOffNote = ufwManager && !ufwManager.active ? ' ufw сейчас выключен — правило сохранено, но не действует, пока вы его не включите.' : ''
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}.${ufwOffNote}`,
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

  async function addFirewalldRule(values: AddFirewalldValues) {
    if (!values.runtime && !values.permanent) {
      setNotice({ kind: 'error', text: 'Выберите хотя бы один вариант: применить сейчас или сохранить постоянно.' })
      return
    }
    const isService = values.targetType === 'service'
    if (!isService && !confirmCriticalPort(Number(values.port), 'Добавление правила firewalld')) return
    setBusy(true)
    setNotice(null)
    try {
      const body: Record<string, unknown> = { zone: values.zone, runtime: values.runtime, permanent: values.permanent }
      if (isService) body.service = values.service
      else {
        body.port = Number(values.port)
        body.protocol = values.protocol
      }
      const res = await api<{ output?: string; simulated?: boolean }>('/firewall/firewalld/rules', {
        method: 'POST',
        body,
      })
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}.`,
      })
      addFirewalldForm.setFieldsValue({ port: '', service: '' })
      fw.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  /** Deletes a firewalld port/service rule straight from its own row in the
   * "Все правила пакетного фильтра" table — rich rules have no delete
   * action here (see the actions column below): firewall-cmd removes them
   * by re-specifying the exact rule text via --remove-rich-rule, which is
   * fragile enough (whitespace/quoting) to leave for a follow-up rather
   * than risk deleting the wrong thing from a reconstructed string. */
  async function deleteFirewalldRule(r: FirewallRule) {
    const isPort = (r.ports?.length ?? 0) > 0
    if (isPort && r.ports && !confirmCriticalPort(r.ports[0], 'Удаление этого правила firewalld')) return
    const label = isPort ? `${r.port_spec}/${r.protocol}` : r.port_spec
    if (!window.confirm(`Удалить правило firewalld: зона ${r.zone}, ${label}?`)) return
    setBusy(true)
    setNotice(null)
    try {
      const body: Record<string, unknown> = { zone: r.zone, runtime: r.runtime, permanent: r.permanent }
      if (isPort) {
        body.port = r.ports![0]
        body.protocol = r.protocol
      } else {
        body.service = r.port_spec
      }
      await api('/firewall/firewalld/rules', { method: 'DELETE', body })
      setNotice({ kind: 'info', text: `Правило firewalld удалено: зона ${r.zone}, ${label}.` })
      fw.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function deleteRule(rule: NumberedRule) {
    const parsed = parseNumberedRule(rule.text)
    if (parsed && !confirmCriticalPort(parsed.port, 'Удаление этого правила')) return
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

  /** Removes a rule by the specification that created it, not by ufw's
   * positional index — the only way to remove a rule while ufw is
   * inactive, since NumberedRule (what deleteRule's number refers to) is
   * empty then. Action defaults to "allow": every quick-add from a socket
   * row creates a plain allow, so that's what a quick-remove from the same
   * row is undoing. */
  async function deleteAddedRule(added: AddedRule) {
    if (added.port && !confirmCriticalPort(added.port, 'Удаление этого правила')) return
    if (!window.confirm(`Удалить правило ufw: ${added.spec}?`)) return
    setBusy(true)
    setNotice(null)
    try {
      await api('/firewall/rules', {
        method: 'DELETE',
        body: { action: added.action || 'allow', port: added.port, protocol: added.protocol || 'tcp', from: '', comment: '' },
      })
      setNotice({ kind: 'info', text: `Правило удалено: ${added.spec}` })
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
      const ufwOffNote = ufwManager && !ufwManager.active ? ' ufw сейчас выключен — правило сохранено, но не действует, пока вы его не включите.' : ''
      setNotice({
        kind: 'info',
        text: `Правило добавлено${res.simulated ? ' (симуляция)' : ''}: ${res.output?.trim() || 'ok'}.${ufwOffNote}`,
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
  function ufwRuleForListener(l: Listener): ListenerRuleMatch | null {
    for (const rule of numbered.data?.rules ?? []) {
      const parsed = parseNumberedRule(rule.text)
      if (parsed && parsed.port === l.port && (!parsed.protocol || parsed.protocol === l.protocol)) {
        return { source: 'numbered', rule, action: parsed.action }
      }
    }
    // ufw status numbered — what the loop above reads — prints nothing at
    // all while ufw is inactive, so a rule that's genuinely stored just
    // never shows up there until ufw is turned on. "ufw show added" is
    // the only place that still reports it either way.
    for (const added of numbered.data?.added ?? []) {
      if (added.port === l.port && (!added.protocol || added.protocol === l.protocol)) {
        return { source: 'added', added, action: added.action?.toUpperCase() }
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
    {
      title: 'Зона',
      key: 'zone',
      render: (_, r) => (r.zone ? <span className="small mono">{r.zone}</span> : <span className="small muted">—</span>),
    },
    {
      title: 'Хранилище',
      key: 'store',
      render: (_, r) => {
        if (r.backend !== 'firewalld') return <span className="small muted">—</span>
        return (
          <>
            {r.runtime && <Tag color="success">сейчас</Tag>}
            {r.permanent && <Tag>постоянно</Tag>}
          </>
        )
      },
    },
    { title: 'Источник', key: 'source', render: (_, r) => <span className="mono small">{r.source || 'any'}</span> },
    { title: 'Кем создано', key: 'managed_by', render: (_, r) => <span className="small">{r.managed_by || '—'}</span> },
    { title: 'Пакетов', key: 'packets', align: 'right', render: (_, r) => <span className="num small">{formatNumber(r.packets)}</span> },
    { title: 'Байт', key: 'bytes', align: 'right', render: (_, r) => <span className="num small">{formatBytes(r.bytes)}</span> },
    ...(canControl
      ? [
          {
            title: '',
            key: 'firewalld_actions',
            render: (_: unknown, r: FirewallRule) => {
              // Rich rules (r.raw set, no port_spec) have no delete action
              // here yet — see deleteFirewalldRule's own doc comment.
              if (r.backend !== 'firewalld' || r.raw) return null
              return (
                <Button danger type="link" size="small" loading={busy} onClick={() => deleteFirewalldRule(r)}>
                  удалить
                </Button>
              )
            },
          } satisfies TableColumnsType<FirewallRule>[number],
        ]
      : []),
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
        const { action } = found
        return (
          <>
            <Tag color={(action && UFW_ACTION_COLOR[action]) || 'default'}>
              {(action && UFW_ACTION_LABEL[action]) || action || 'правило есть'}
            </Tag>
            {!ufwManager?.active && <div className="small muted">ufw выключен — не действует</div>}
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
              if (!found) {
                return (
                  <Button type="link" size="small" loading={busy} onClick={() => quickAllowListener(l)}>
                    разрешить
                  </Button>
                )
              }
              const onDelete = found.source === 'numbered' ? () => deleteRule(found.rule) : () => deleteAddedRule(found.added)
              return (
                <Button danger type="link" size="small" loading={busy} onClick={onDelete}>
                  удалить
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
            Полный набор правил iptables, ufw и firewalld со счётчиками. Правила меняются только
            через ufw/firewalld: прямая правка iptables из веб-интерфейса — верный способ потерять
            доступ к серверу.
          </p>
        </div>
      </div>

      <ErrorNote error={fw.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      {fw.data && (
        <div className="grid grid-3">
          <Card title="Менеджеры firewall">
            {fw.data.managers.map((m, i) => (
              <div key={m.name} style={i > 0 ? { marginTop: '0.8rem', paddingTop: '0.8rem', borderTop: '1px solid var(--border)' } : undefined}>
                <div className="small muted" style={{ marginBottom: '0.2rem' }}>
                  {MANAGER_META[m.name]?.label ?? m.name}
                </div>
                {m.installed ? (
                  <>
                    <div className="row">
                      <StateBadge state={m.active ? 'active' : 'inactive'} />
                      <span className="small secondary">{m.policy || 'политика не прочитана'}</span>
                    </div>
                    {canControl && (
                      <Button style={{ marginTop: '0.6rem' }} loading={busy} onClick={() => reloadManager(m.name)}>
                        Перезагрузить {MANAGER_META[m.name]?.label ?? m.name}
                      </Button>
                    )}
                  </>
                ) : (
                  <>
                    <Banner kind="warn">{MANAGER_META[m.name]?.label ?? m.name} не установлен на этом хосте.</Banner>
                    {canControl &&
                      (installActiveFor(m.name) ? (
                        <Button
                          style={{ marginTop: '0.6rem' }}
                          type="primary"
                          onClick={() => {
                            setInstallOutcome(null)
                            setInstallTarget(m.name as 'ufw' | 'firewalld')
                          }}
                        >
                          установка выполняется — открыть
                        </Button>
                      ) : (
                        <Button
                          style={{ marginTop: '0.6rem' }}
                          type="primary"
                          onClick={() => {
                            const label = MANAGER_META[m.name]?.label ?? m.name
                            if (window.confirm(`Установить ${label} (apt-get install -y ${label}) на этом хосте?`)) {
                              setInstallOutcome(null)
                              setInstallTarget(m.name as 'ufw' | 'firewalld')
                            }
                          }}
                        >
                          Установить {MANAGER_META[m.name]?.label ?? m.name}
                        </Button>
                      ))}
                  </>
                )}
              </div>
            ))}
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

          {canControl && ufwManager?.installed && (
            <Card title="Добавить правило (ufw)" subtitle="Через ufw, с записью в журнал">
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

          {canControl && firewalldManager?.installed && (
            <Card title="Добавить правило (firewalld)" subtitle="Порт или сервис в зоне, с записью в журнал">
              <Form<AddFirewalldValues>
                form={addFirewalldForm}
                layout="vertical"
                onFinish={addFirewalldRule}
                initialValues={{
                  zone: firewalldManager.policy || 'public',
                  targetType: 'port',
                  port: '',
                  protocol: 'tcp',
                  service: '',
                  runtime: true,
                  permanent: true,
                }}
              >
                <div className="row">
                  <Form.Item name="zone" label="Зона" rules={[{ required: true }]} style={{ flex: 1 }}>
                    <Select options={knownZones.map((z) => ({ value: z, label: z }))} />
                  </Form.Item>
                  <Form.Item name="targetType" label="Что разрешить" style={{ flex: 1 }}>
                    <Radio.Group
                      options={[
                        { value: 'port', label: 'порт' },
                        { value: 'service', label: 'сервис' },
                      ]}
                      optionType="button"
                    />
                  </Form.Item>
                </div>
                <Form.Item shouldUpdate={(prev, next) => prev.targetType !== next.targetType} noStyle>
                  {({ getFieldValue }) =>
                    getFieldValue('targetType') === 'service' ? (
                      <Form.Item name="service" label="Сервис" rules={[{ required: true }]}>
                        <Input placeholder="ssh" />
                      </Form.Item>
                    ) : (
                      <div className="row">
                        <Form.Item name="port" label="Порт" rules={[{ required: true }]} style={{ flex: 1 }}>
                          <Input inputMode="numeric" />
                        </Form.Item>
                        <Form.Item name="protocol" label="Протокол" style={{ flex: 1 }}>
                          <Select options={['tcp', 'udp'].map((v) => ({ value: v, label: v }))} />
                        </Form.Item>
                      </div>
                    )
                  }
                </Form.Item>
                <div className="row">
                  <Form.Item name="runtime" valuePropName="checked">
                    <Checkbox>применить сейчас</Checkbox>
                  </Form.Item>
                  <Form.Item name="permanent" valuePropName="checked">
                    <Checkbox>сохранить постоянно</Checkbox>
                  </Form.Item>
                </div>
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

      {installTarget && (
        <PackageInstallModal
          packageName={MANAGER_META[installTarget].label}
          wsPath={MANAGER_META[installTarget].installWsPath}
          onClose={() => setInstallTarget(null)}
          onFinished={handleInstallFinished}
          outcome={installOutcome}
          rescanning={rescanningAfterInstall}
        />
      )}
    </>
  )
}
