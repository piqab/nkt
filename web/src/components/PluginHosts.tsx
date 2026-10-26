import { useState } from 'react'
import { Button, Input, Select, Tabs, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, CodeEditor, DiffView, Loading, Modal, formatBytesShort, formatDateTime, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { confirmAction } from './confirm'
import { unifiedDiff } from './textDiff'
import type { PluginAction, PluginField, PluginView } from '../plugins'
import type { HubHost } from '../types'

/** Хосты плагинов (MikroTik, Windows…): устройства без агента nkt, которые
 * читает и которыми управляет плагин хаба. Учётки хранит хаб; пароли
 * наружу не отдаются — только «задан» и «заменить». */

export interface PluginHostType {
  id: string
  title: string
  hint?: string
  fields: PluginField[]
  sections: string[]
  actions?: PluginAction[]
  exec?: string
}

interface TypeRow {
  plugin: string
  plugin_title: string
  type: PluginHostType
}

interface HostInfo {
  name?: string
  os?: string
  version?: string
  model?: string
  serial?: string
  arch?: string
  uptime_s?: number
  firmware?: string
  fingerprint?: string
}

function infoLine(i?: HostInfo) {
  if (!i) return ''
  return [i.name, [i.os, i.version].filter(Boolean).join(' '), i.model, i.firmware && `fw ${i.firmware}`].filter(Boolean).join(' · ')
}

function ConnFields({
  type,
  values,
  set,
  secretSet,
}: {
  type: PluginHostType
  values: Record<string, string>
  set: (k: string, v: string) => void
  secretSet?: Record<string, boolean>
}) {
  const { t } = useTranslation()
  return (
    <>
      {type.fields.map((f) => (
        <label key={f.name} className="col" style={{ gap: '0.2rem' }}>
          <span>
            {f.title}
            {f.required && !(f.type === 'password' && secretSet?.[f.name]) && ' *'}
            {f.type === 'password' && secretSet && (
              <>
                {' '}
                {secretSet[f.name] ? <Tag color="success">{t('plugins.settingsSecretSet')}</Tag> : <Tag color="warning">{t('plugins.settingsSecretUnset')}</Tag>}
              </>
            )}
          </span>
          {f.type === 'password' ? (
            <Input.Password
              autoComplete="new-password"
              value={values[f.name] ?? ''}
              placeholder={secretSet?.[f.name] ? t('plugins.settingsReplace') : ''}
              onChange={(e) => set(f.name, e.target.value)}
            />
          ) : f.type === 'select' ? (
            <Select
              value={values[f.name] ?? (f.default as string | undefined)}
              onChange={(v: string) => set(f.name, v)}
              options={(f.options ?? []).map((o) => ({ value: o.value, label: o.title || o.value }))}
            />
          ) : (
            <Input value={values[f.name] ?? ''} placeholder={f.placeholder ?? (f.default as string | undefined)} onChange={(e) => set(f.name, e.target.value)} />
          )}
          {f.hint && <span className="muted">{f.hint}</span>}
        </label>
      ))}
    </>
  )
}

/** «Добавить устройство»: тип плагина → поля → «Проверить» → «Добавить». */
export function PluginHostAddModal({ groups, onClose, onAdded }: { groups: string[]; onClose: () => void; onAdded: () => void }) {
  const { t } = useTranslation()
  const types = useApi<{ types: TypeRow[] }>('/hub/plugin-hosts/types')
  const list = types.data?.types ?? []
  const [key, setKey] = useState<string | undefined>()
  const row = list.find((r) => `${r.plugin}/${r.type.id}` === key) ?? list[0]
  const [values, setValues] = useState<Record<string, string>>({})
  const [name, setName] = useState('')
  const [group, setGroup] = useState('')
  const [checked, setChecked] = useState<{ info: HostInfo; fingerprint?: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = (k: string, v: string) => {
    setValues({ ...values, [k]: v })
    setChecked(null)
  }
  const missing = (row?.type.fields ?? []).filter((f) => f.required && !(values[f.name] ?? (f.default as string | undefined) ?? '').trim())
  const body = () => ({ plugin: row!.plugin, type: row!.type.id, name: name.trim(), group: group.trim(), conn: values })

  async function check() {
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ info: HostInfo; fingerprint?: string }>('/hub/plugin-hosts/check', { method: 'POST', body: body(), timeoutMs: 120_000 })
      setChecked(r)
      if (r.fingerprint && !values.fingerprint && row?.type.fields.some((f) => f.name === 'fingerprint')) setValues({ ...values, fingerprint: r.fingerprint })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  async function add() {
    setBusy(true)
    setError(null)
    try {
      await api('/hub/plugin-hosts', { method: 'POST', body: body(), timeoutMs: 120_000 })
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('devices.addTitle')} onClose={onClose} width={720} maskClosable={false}>
      <div className="col small" style={{ gap: '0.6rem' }}>
        {types.loading && <Loading />}
        {!types.loading && list.length === 0 && <Banner kind="info">{t('devices.noTypes')}</Banner>}
        {list.length > 0 && row && (
          <>
            <label className="col" style={{ gap: '0.2rem' }}>
              {t('devices.type')}
              <Select
                value={`${row.plugin}/${row.type.id}`}
                onChange={(v: string) => {
                  setKey(v)
                  setValues({})
                  setChecked(null)
                }}
                options={list.map((r) => ({ value: `${r.plugin}/${r.type.id}`, label: `${r.type.title} — ${r.plugin_title} (${r.plugin})` }))}
              />
            </label>
            {row.type.hint && <span className="muted">{row.type.hint}</span>}
            <ConnFields type={row.type} values={values} set={set} />
            <label className="col" style={{ gap: '0.2rem' }}>
              {t('devices.name')}
              <Input value={name} placeholder={checked?.info.name?.toLowerCase() ?? ''} onChange={(e) => setName(e.target.value.trim().toLowerCase())} />
              <span className="muted">{t('devices.nameHint')}</span>
            </label>
            <label className="col" style={{ gap: '0.2rem' }}>
              {t('devices.group')}
              <Select allowClear showSearch value={group || undefined} onChange={(v: string | undefined) => setGroup(v ?? '')} options={groups.map((g) => ({ value: g, label: g }))} />
            </label>
            {checked && (
              <Banner kind="info">
                {t('devices.checked', { info: infoLine(checked.info) })}
                {checked.fingerprint && (
                  <>
                    <br />
                    {t('devices.fingerprint')}: <code className="small">{checked.fingerprint}</code>
                  </>
                )}
              </Banner>
            )}
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
          <Button loading={busy && !checked} disabled={!row || missing.length > 0} onClick={() => void check()}>
            {t('devices.check')}
          </Button>
          <Button type="primary" loading={busy && !!checked} disabled={!checked} onClick={() => void add()}>
            {t('devices.add')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          {missing.length > 0 && <span className="muted">{t('plugins.fieldsMissing', { list: missing.map((f) => f.title).join(', ') })}</span>}
        </div>
      </div>
    </Modal>
  )
}

interface HostCard {
  host: HubHost & { plugin?: string; plugin_type?: string }
  type?: PluginHostType
  conn?: Record<string, string>
  secret_set?: Record<string, boolean>
  state?: { reachable: boolean; error?: string; polled_at?: string; info?: HostInfo; findings?: Record<string, number> }
  error?: string
}

/** Таблица из вида плагина (дополнительные таблицы состава). */
function ViewTable({ view }: { view: PluginView }) {
  return (
    <div className="col" style={{ gap: '0.3rem' }}>
      <strong>{view.title}</strong>
      <div className="table-wrap">
        <DataTable
          rowKey={(_, i) => String(i)}
          dataSource={(view.rows ?? []).map((r, i) => ({ ...r, _i: i }))}
          columns={(view.columns ?? []).map((c) => ({ title: c.title, key: c.key, render: (_: unknown, r: { cells: Record<string, unknown> }) => String(r.cells[c.key] ?? '') }))}
        />
      </div>
    </div>
  )
}

function simpleTable<T extends object>(rows: T[] | undefined, cols: [string, string, (r: T) => React.ReactNode][]) {
  if (!rows?.length) return null
  return (
    <div className="table-wrap">
      <DataTable
        rowKey={(_, i) => String(i)}
        dataSource={rows.map((r, i) => ({ ...r, _i: i }))}
        columns={cols.map(([key, title, render]) => ({ key, title, render: (_: unknown, r: T) => render(r) }))}
      />
    </div>
  )
}

interface Snapshot {
  info: HostInfo
  cpu?: { model?: string; cores?: number }
  mem_total?: number
  disks?: { name: string; size?: number; used?: number }[]
  interfaces?: { name: string; type?: string; mac?: string; addrs?: string[]; up: boolean; disabled?: boolean; comment?: string }[]
  services?: { name: string; state?: string; enabled: boolean; detail?: string }[]
  packages?: { name: string; version?: string }[]
  ports?: { proto: string; port: number; service?: string; allowed?: string }[]
  users?: { name: string; group?: string }[]
  neighbors?: { name?: string; address?: string; mac?: string; interface?: string; platform?: string }[]
  extra?: PluginView[]
}

function OverviewTab({ id }: { id: number }) {
  const { t } = useTranslation()
  const st = useApi<Snapshot>(`/hub/plugin-hosts/${id}/snapshot`)
  const s = st.data
  if (st.error) return <Banner kind="error">{st.error}</Banner>
  if (!s) return <Loading />
  return (
    <div className="col small" style={{ gap: '0.8rem' }}>
      <div>
        <strong>{infoLine(s.info)}</strong>
        <div className="muted">
          {[s.cpu?.model && `${s.cpu.model}${s.cpu.cores ? ` × ${s.cpu.cores}` : ''}`, s.mem_total && `${t('devices.mem')} ${formatBytesShort(s.mem_total)}`,
            s.info.serial && `S/N ${s.info.serial}`, s.info.uptime_s && `${t('devices.uptime')} ${Math.round(s.info.uptime_s / 3600)} ${t('devices.hours')}`]
            .filter(Boolean)
            .join(' · ')}
        </div>
      </div>
      {simpleTable(s.interfaces, [
        ['name', t('devices.iface'), (r) => <code>{r.name}</code>],
        ['type', t('devices.ifType'), (r) => r.type],
        ['addrs', t('devices.addrs'), (r) => (r.addrs ?? []).join(', ')],
        ['mac', 'MAC', (r) => <span className="mono">{r.mac}</span>],
        ['up', t('devices.state'), (r) => (r.disabled ? <Tag>{t('devices.disabled')}</Tag> : r.up ? <Tag color="success">up</Tag> : <Tag color="warning">down</Tag>)],
        ['comment', t('devices.comment'), (r) => r.comment],
      ])}
      {simpleTable(s.ports, [
        ['port', t('devices.port'), (r) => `${r.port}/${r.proto}`],
        ['service', t('devices.service'), (r) => r.service],
        ['allowed', t('devices.allowed'), (r) => r.allowed || <Tag color="warning">{t('devices.allowedAll')}</Tag>],
      ])}
      {simpleTable(s.neighbors, [
        ['name', t('devices.neighbor'), (r) => r.name],
        ['address', t('devices.addrs'), (r) => r.address],
        ['interface', t('devices.iface'), (r) => r.interface],
        ['platform', t('devices.platform'), (r) => r.platform],
      ])}
      {(s.extra ?? []).map((v, i) => (
        <ViewTable key={i} view={v} />
      ))}
      {simpleTable(s.services, [
        ['name', t('devices.service'), (r) => r.name],
        ['state', t('devices.state'), (r) => r.state],
        ['detail', t('devices.comment'), (r) => r.detail],
      ])}
      {simpleTable(s.disks, [
        ['name', t('devices.disk'), (r) => r.name],
        ['used', t('devices.used'), (r) => (r.size ? `${formatBytesShort(r.used ?? 0)} / ${formatBytesShort(r.size)}` : '')],
      ])}
      {simpleTable(s.users, [
        ['name', t('devices.user'), (r) => r.name],
        ['group', t('devices.userGroup'), (r) => r.group],
      ])}
      {simpleTable(s.packages, [
        ['name', t('devices.package'), (r) => r.name],
        ['version', t('devices.version'), (r) => r.version],
      ])}
    </div>
  )
}

function MetricsTab({ id }: { id: number }) {
  const { t } = useTranslation()
  const st = useApi<{ cpu_pct?: number; mem_used?: number; mem_total?: number; disk_used?: number; disk_size?: number; temp_c?: number; uptime_s?: number }>(
    `/hub/plugin-hosts/${id}/metrics`,
    30_000,
  )
  const m = st.data
  if (st.error) return <Banner kind="error">{st.error}</Banner>
  if (!m) return <Loading />
  const rows: [string, string][] = []
  if (m.cpu_pct != null) rows.push([t('devices.cpu'), `${m.cpu_pct}%`])
  if (m.mem_used != null && m.mem_total) rows.push([t('devices.mem'), `${formatBytesShort(m.mem_used)} / ${formatBytesShort(m.mem_total)}`])
  if (m.disk_used != null && m.disk_size) rows.push([t('devices.disk'), `${formatBytesShort(m.disk_used)} / ${formatBytesShort(m.disk_size)}`])
  if (m.temp_c != null) rows.push([t('devices.temp'), `${m.temp_c} °C`])
  if (m.uptime_s) rows.push([t('devices.uptime'), `${Math.round(m.uptime_s / 3600)} ${t('devices.hours')}`])
  return (
    <div className="col small" style={{ gap: '0.5rem' }}>
      {rows.map(([k, v]) => (
        <div key={k} className="row" style={{ gap: '0.5rem' }}>
          <span className="muted" style={{ minWidth: '8rem' }}>
            {k}
          </span>
          <strong>{v}</strong>
        </div>
      ))}
      <span className="muted">{t('devices.metricsHint')}</span>
    </div>
  )
}

const SEV_COLOR: Record<string, string> = { critical: 'red', warning: 'orange', info: 'blue' }

function FindingsTab({ id }: { id: number }) {
  const { t } = useTranslation()
  const st = useApi<{ findings: { id: string; severity: string; title: string; detail?: string; fix?: string }[] }>(`/hub/plugin-hosts/${id}/findings`)
  if (st.error) return <Banner kind="error">{st.error}</Banner>
  if (!st.data) return <Loading />
  if (st.data.findings.length === 0) return <span className="small muted">{t('devices.noFindings')}</span>
  return (
    <div className="col small" style={{ gap: '0.5rem' }}>
      {st.data.findings.map((f) => (
        <div key={f.id} className="col" style={{ gap: '0.15rem', borderBottom: '1px solid var(--border)', paddingBottom: '0.4rem' }}>
          <span>
            <Tag color={SEV_COLOR[f.severity] ?? 'default'}>{t(`devices.sev.${f.severity}`, { defaultValue: f.severity })}</Tag> <strong>{f.title}</strong>
          </span>
          {f.detail && <span className="muted">{f.detail}</span>}
          {f.fix && (
            <span>
              {t('devices.fix')}: <code className="small">{f.fix}</code>
            </span>
          )}
        </div>
      ))}
    </div>
  )
}

function ConfigTab({ id }: { id: number }) {
  const { t } = useTranslation()
  const [fresh, setFresh] = useState(0)
  const st = useApi<{ versions: { at: string; sha: string; text: string; by?: string }[] }>(`/hub/plugin-hosts/${id}/config${fresh ? `?fresh=1&n=${fresh}` : ''}`)
  const [pick, setPick] = useState(0)
  const list = st.data?.versions ?? []
  const cur = list[pick]
  const prev = list[pick + 1]
  return (
    <div className="col small" style={{ gap: '0.5rem' }}>
      <span className="muted">{t('devices.configHint')}</span>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
        <Select
          style={{ minWidth: 320 }}
          value={pick}
          onChange={setPick}
          options={list.map((v, i) => ({ value: i, label: `${formatDateTime(v.at)} · ${v.by || t('devices.byPoll')} · ${v.sha}` }))}
        />
        <Button loading={st.loading} onClick={() => setFresh((n) => n + 1)}>
          {t('devices.readNow')}
        </Button>
      </div>
      {st.error && <Banner kind="error">{st.error}</Banner>}
      {cur && prev && <DiffView text={unifiedDiff(prev.text, cur.text, formatDateTime(prev.at), formatDateTime(cur.at)) || t('devices.noChange')} />}
      {cur && <CodeEditor value={cur.text} readOnly rows={18} />}
    </div>
  )
}

function ActionsTab({ id, type }: { id: number; type: PluginHostType }) {
  const { t } = useTranslation()
  const [out, setOut] = useState<{ text?: string; error?: string } | null>(null)
  const [form, setForm] = useState<{ action: PluginAction; values: Record<string, string> } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  async function run(a: PluginAction, args: Record<string, string>, dry: boolean) {
    if (!dry && a.confirm && !(await confirmAction(a.confirm, { danger: a.danger }))) return
    setBusy(a.op!)
    setOut(null)
    try {
      const r = await api<{ text: string }>(`/hub/plugin-hosts/${id}/action`, { method: 'POST', body: { action: a.op, args, dry_run: dry }, timeoutMs: 300_000 })
      setOut({ text: r.text })
      setForm(null)
    } catch (err) {
      setOut({ error: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }
  return (
    <div className="col small" style={{ gap: '0.6rem' }}>
      <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap' }}>
        {(type.actions ?? []).map((a) => (
          <Button key={a.op} danger={a.danger} loading={busy === a.op} onClick={() => (a.fields?.length ? setForm({ action: a, values: {} }) : void run(a, {}, false))}>
            {a.title}
          </Button>
        ))}
      </div>
      {form && (
        <div className="col" style={{ gap: '0.4rem', border: '1px solid var(--border)', borderRadius: 6, padding: '0.6rem' }}>
          <strong>{form.action.title}</strong>
          {(form.action.fields ?? []).map((f) => (
            <label key={f.name} className="col" style={{ gap: '0.2rem' }}>
              {f.title}
              <Input value={form.values[f.name] ?? ''} placeholder={f.placeholder} onChange={(e) => setForm({ ...form, values: { ...form.values, [f.name]: e.target.value } })} />
              {f.hint && <span className="muted">{f.hint}</span>}
            </label>
          ))}
          <div className="row" style={{ gap: '0.5rem' }}>
            <Button onClick={() => void run(form.action, form.values, true)}>{t('devices.dryRun')}</Button>
            <Button type="primary" onClick={() => void run(form.action, form.values, false)}>
              {t('devices.run')}
            </Button>
            <Button onClick={() => setForm(null)}>{t('common.cancel')}</Button>
          </div>
        </div>
      )}
      {out?.error && <Banner kind="error">{out.error}</Banner>}
      {out?.text && <pre className="small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{out.text}</pre>}
    </div>
  )
}

function ExecTab({ id, type, name }: { id: number; type: PluginHostType; name: string }) {
  const { t } = useTranslation()
  const [cmd, setCmd] = useState('')
  const [out, setOut] = useState<{ text?: string; error?: string; dry: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  async function run(dry: boolean) {
    if (!dry && !(await confirmAction(t('devices.execConfirm', { name }), { danger: true }))) return
    setBusy(true)
    try {
      const r = await api<{ text: string }>(`/hub/plugin-hosts/${id}/exec`, { method: 'POST', body: { command: cmd, dry_run: dry }, timeoutMs: 300_000 })
      setOut({ text: r.text, dry })
    } catch (err) {
      setOut({ error: err instanceof Error ? err.message : String(err), dry })
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="col small" style={{ gap: '0.5rem' }}>
      <span className="muted">{t('devices.execHint', { lang: type.exec, name })}</span>
      <CodeEditor value={cmd} onChange={(e) => setCmd(e.target.value)} rows={6} />
      <div className="row" style={{ gap: '0.5rem' }}>
        <Button loading={busy} disabled={!cmd.trim()} onClick={() => void run(true)}>
          {t('devices.dryRun')}
        </Button>
        <Button type="primary" danger loading={busy} disabled={!cmd.trim()} onClick={() => void run(false)}>
          {t('devices.run')}
        </Button>
      </div>
      {out?.error && <Banner kind="error">{out.error}</Banner>}
      {out?.text != null && <pre className="small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{out.text || t('devices.noOutput')}</pre>}
    </div>
  )
}

function ConnTab({ card, onSaved }: { card: HostCard; onSaved: () => void }) {
  const { t } = useTranslation()
  const [values, setValues] = useState<Record<string, string>>(card.conn ?? {})
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok?: string; error?: string } | null>(null)
  if (!card.type) return null
  const diff = unifiedDiff(
    Object.entries(card.conn ?? {}).map(([k, v]) => `${k}=${v}`).join('\n'),
    Object.entries(card.conn ?? {}).map(([k]) => `${k}=${values[k] ?? ''}`).join('\n'),
    t('devices.saved'),
    t('editModal.draft'),
  )
  async function save() {
    setBusy(true)
    setMsg(null)
    try {
      const r = await api<{ info: HostInfo }>(`/hub/plugin-hosts/${card.host.id}/conn`, { method: 'PUT', body: { conn: values }, timeoutMs: 120_000 })
      setMsg({ ok: t('devices.connSaved', { info: infoLine(r.info) }) })
      onSaved()
    } catch (err) {
      setMsg({ error: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="col small" style={{ gap: '0.5rem' }}>
      <span className="muted">{t('devices.connHint')}</span>
      <ConnFields type={card.type} values={values} set={(k, v) => setValues({ ...values, [k]: v })} secretSet={card.secret_set} />
      {diff && <DiffView text={diff} />}
      {msg?.ok && <Banner kind="info">{msg.ok}</Banner>}
      {msg?.error && <Banner kind="error">{msg.error}</Banner>}
      <div className="row">
        <Button type="primary" loading={busy} onClick={() => void save()}>
          {t('devices.checkSave')}
        </Button>
      </div>
    </div>
  )
}

/** Хост плагина: разделы, которые есть у его типа. */
export function PluginHostPanel({ host, admin, onClose, onChanged }: { host: HubHost; admin: boolean; onClose: () => void; onChanged: () => void }) {
  const { t } = useTranslation()
  const card = useApi<HostCard>(`/hub/plugin-hosts/${host.id}`)
  const c = card.data
  const sections = c?.type?.sections ?? []
  const has = (s: string) => sections.includes(s)
  async function remove() {
    if (!(await confirmAction(t('devices.deleteConfirm', { name: host.name })))) return
    await api(`/hub/plugin-hosts/${host.id}`, { method: 'DELETE' })
    onChanged()
    onClose()
  }
  const items = [
    has('overview') && { key: 'overview', label: t('devices.tabOverview'), children: <OverviewTab id={host.id} /> },
    has('metrics') && { key: 'metrics', label: t('devices.tabMetrics'), children: <MetricsTab id={host.id} /> },
    has('findings') && { key: 'findings', label: t('devices.tabFindings'), children: <FindingsTab id={host.id} /> },
    has('config') && { key: 'config', label: t('devices.tabConfig'), children: <ConfigTab id={host.id} /> },
    admin && has('actions') && c?.type && { key: 'actions', label: t('devices.tabActions'), children: <ActionsTab id={host.id} type={c.type} /> },
    admin && has('exec') && c?.type && { key: 'exec', label: t('devices.tabExec'), children: <ExecTab id={host.id} type={c.type} name={host.name} /> },
    admin && c?.type && { key: 'conn', label: t('devices.tabConn'), children: <ConnTab card={c} onSaved={() => card.reload()} /> },
  ].filter(Boolean) as { key: string; label: string; children: React.ReactNode }[]
  return (
    <Modal title={`${host.name} — ${c?.type?.title ?? host.plugin_type ?? ''}`} onClose={onClose} width={1100} sizeKey="plugin-host">
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span className="muted">
          {t('devices.via', { plugin: host.plugin })} · {host.addr}
          {c?.state?.polled_at && ` · ${t('devices.polled')} ${formatRelative(c.state.polled_at)}`}
          {admin && (
            <Button size="small" danger type="link" onClick={() => void remove()}>
              {t('devices.delete')}
            </Button>
          )}
        </span>
        {c?.error && <Banner kind="error">{c.error}</Banner>}
        {c?.state && !c.state.reachable && c.state.error && <Banner kind="warn">{c.state.error}</Banner>}
        {card.loading && !c ? <Loading /> : <Tabs items={items} destroyInactiveTabPane />}
      </div>
    </Modal>
  )
}
