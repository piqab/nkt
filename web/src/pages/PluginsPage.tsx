import { useEffect, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Tabs } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, CodeEditor, DiffView, Empty, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { EditTextModal } from '../components/EditTextModal'
import { confirmAction } from '../components/confirm'
import { unifiedDiff } from '../components/textDiff'
import type { PluginAction, PluginField, PluginInfo, PluginResponse, PluginView } from '../plugins'

/**
 * Раздел «Плагины»: у каждого подключённого плагина — его разделы
 * (манифест → pages). Раздел — операция плагина, которая отдаёт вид:
 * таблицу, текст или несколько видов подряд. Кнопки вида вызывают
 * операции (с формой и подтверждением), открывают другой раздел с
 * аргументами или правят документ — окном с диффом и историей версий в
 * хабе, как всё, что правится в nkt.
 */
export function PluginsPage({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const list = useApi<{ plugins: PluginInfo[] }>('/hub/plugins')
  const plugins = list.data?.plugins ?? []
  const [active, setActive] = useState<string | undefined>(undefined)
  if (list.loading && !list.data) return <Loading what={t('plugins.title')} />
  if (plugins.length === 0) return <Empty>{t('plugins.none')}</Empty>
  return (
    <div className="page">
      <h1>{t('plugins.title')}</h1>
      {list.error && <Banner kind="error">{list.error}</Banner>}
      <Tabs
        activeKey={active ?? plugins[0].name}
        onChange={setActive}
        items={plugins.map((p) => ({
          key: p.name,
          label: p.title || p.name,
          children: <PluginPanel plugin={p} admin={admin} />,
        }))}
      />
    </div>
  )
}

function PluginPanel({ plugin, admin }: { plugin: PluginInfo; admin: boolean }) {
  const { t } = useTranslation()
  const pages = plugin.manifest?.pages ?? []
  // Открытый раздел и его аргументы («открыть зону» из строки таблицы).
  const [page, setPage] = useState<{ id: string; args?: Record<string, unknown>; title?: string } | null>(null)
  if (plugin.error) {
    return <Banner kind="error">{t('plugins.unreachable', { error: plugin.error })}</Banner>
  }
  if (pages.length === 0) {
    return <PluginAbout plugin={plugin} />
  }
  const cur = page ?? { id: (pages.find((p) => !p.hidden) ?? pages[0]).id }
  const def = pages.find((p) => p.id === cur.id) ?? pages[0]
  return (
    <div className="col" style={{ gap: '0.75rem' }}>
      <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
        {pages
          .filter((p) => !p.hidden)
          .map((p) => (
            <Button key={p.id} size="small" type={p.id === def.id && !cur.args ? 'primary' : 'default'} onClick={() => setPage({ id: p.id })}>
              {p.title}
            </Button>
          ))}
        {cur.args && (
          <span className="small muted">
            → {def.title}
            {cur.title ? ` — ${cur.title}` : ''}
          </span>
        )}
      </div>
      <PluginPageView
        key={def.id + JSON.stringify(cur.args ?? {})}
        plugin={plugin}
        op={def.op}
        args={cur.args}
        admin={admin}
        onOpen={(id, args, title) => setPage({ id, args, title })}
      />
      <PluginAbout plugin={plugin} compact />
    </div>
  )
}

/** Что умеет плагин: версия, хост, команды сценариев, инструменты MCP. */
function PluginAbout({ plugin, compact }: { plugin: PluginInfo; compact?: boolean }) {
  const { t } = useTranslation()
  const m = plugin.manifest
  if (!m) return null
  const tools = m.ops.filter((o) => o.mcp)
  return (
    <div className="small muted" style={{ marginTop: compact ? '0.5rem' : 0 }}>
      {m.title} {m.version} · {plugin.host}:{plugin.port}
      {plugin.pipeline ? ` · ${t('plugins.pipeline')}: ${plugin.pipeline}` : ''}
      {m.description && !compact && <p>{m.description}</p>}
      {(m.commands?.length ?? 0) > 0 && (
        <div>
          {t('plugins.commands')}: {m.commands!.map((c) => c.usage).join('; ')}
        </div>
      )}
      {tools.length > 0 && (
        <div>
          {t('plugins.tools')}: {tools.map((o) => `${plugin.name.replace(/-/g, '_')}_${o.name}`).join(', ')}
        </div>
      )}
    </div>
  )
}

async function callPlugin(plugin: string, op: string, args?: Record<string, unknown>): Promise<PluginResponse> {
  return api<PluginResponse>(`/hub/plugins/${plugin}/call`, { method: 'POST', body: { op, args: args ?? {} }, timeoutMs: 120_000 })
}

function PluginPageView({
  plugin,
  op,
  args,
  admin,
  onOpen,
}: {
  plugin: PluginInfo
  op: string
  args?: Record<string, unknown>
  admin: boolean
  onOpen: (page: string, args?: Record<string, unknown>, title?: string) => void
}) {
  const [state, setState] = useState<{ view?: PluginView; error?: string } | null>(null)
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    let cancelled = false
    setState(null)
    callPlugin(plugin.name, op, args)
      .then((r) => !cancelled && setState({ view: r.view ?? { type: 'text', text: r.text ?? '' } }))
      .catch((err) => !cancelled && setState({ error: err instanceof Error ? err.message : String(err) }))
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- args сравниваются ключом компонента
  }, [plugin.name, op, nonce])
  if (!state) return <Loading what={plugin.title} />
  if (state.error) return <Banner kind="error">{state.error}</Banner>
  return <ViewBlock plugin={plugin} view={state.view!} admin={admin} onOpen={onOpen} onChanged={() => setNonce((n) => n + 1)} />
}

function ViewBlock({
  plugin,
  view,
  admin,
  onOpen,
  onChanged,
}: {
  plugin: PluginInfo
  view: PluginView
  admin: boolean
  onOpen: (page: string, args?: Record<string, unknown>, title?: string) => void
  onChanged: () => void
}) {
  const actions = (list?: PluginAction[], rowTitle?: string) =>
    (list ?? []).map((a, i) => <ActionButton key={i} plugin={plugin} action={a} admin={admin} onOpen={onOpen} onChanged={onChanged} context={rowTitle} />)
  if (view.type === 'sections') {
    return (
      <div className="col" style={{ gap: '0.75rem' }}>
        {(view.items ?? []).map((v, i) => (
          <ViewBlock key={i} plugin={plugin} view={v} admin={admin} onOpen={onOpen} onChanged={onChanged} />
        ))}
      </div>
    )
  }
  const body =
    view.type === 'table' ? (
      <div className="table-wrap">
        <DataTable
          rowKey={(_, i) => String(i)}
          dataSource={(view.rows ?? []).map((r, i) => ({ ...r, _i: i }))}
          columns={[
            ...(view.columns ?? []).map((c) => ({
              title: c.title,
              key: c.key,
              render: (_: unknown, r: { cells: Record<string, unknown> }) => cellText(r.cells[c.key]),
            })),
            ...((view.rows ?? []).some((r) => r.actions?.length)
              ? [
                  {
                    title: '',
                    key: '_actions',
                    render: (_: unknown, r: { cells: Record<string, unknown>; actions?: PluginAction[] }) => (
                      <div className="row" style={{ gap: '0.3rem', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                        {actions(r.actions, cellText(r.cells[view.columns?.[0]?.key ?? '']))}
                      </div>
                    ),
                  },
                ]
              : []),
          ]}
        />
      </div>
    ) : (
      <pre className="small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
        {view.text}
      </pre>
    )
  return (
    <Card title={view.title} subtitle={view.note} actions={(view.actions?.length ?? 0) > 0 && actions(view.actions)}>
      {body}
    </Card>
  )
}

function cellText(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (Array.isArray(v)) return v.map(cellText).join(', ')
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

/** Кнопка вида: открыть раздел, править документ или вызвать операцию. */
function ActionButton({
  plugin,
  action,
  admin,
  onOpen,
  onChanged,
  context,
}: {
  plugin: PluginInfo
  action: PluginAction
  admin: boolean
  onOpen: (page: string, args?: Record<string, unknown>, title?: string) => void
  onChanged: () => void
  context?: string
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState(false)
  const [edit, setEdit] = useState(false)
  const [result, setResult] = useState<{ text?: string; error?: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const op = action.op ? plugin.manifest?.ops.find((o) => o.name === action.op) : undefined
  const writes = (action.op && !op?.read_only) || !!action.edit
  // Что меняет — администратору (сервер проверит и сам).
  if (writes && !admin) return null

  async function run(values: Record<string, unknown>) {
    if (action.confirm && !(await confirmAction(action.confirm, { danger: action.danger }))) return false
    setBusy(true)
    try {
      const r = await callPlugin(plugin.name, action.op!, { ...(action.args ?? {}), ...values })
      setResult({ text: r.text || t('plugins.done') })
      onChanged()
      return true
    } catch (err) {
      setResult({ error: err instanceof Error ? err.message : String(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  function click() {
    if (action.open) {
      onOpen(action.open, action.args, context)
      return
    }
    if (action.edit) {
      setEdit(true)
      return
    }
    if (action.fields?.length) {
      setForm(true)
      return
    }
    void run({})
  }

  return (
    <>
      <Button size="small" danger={action.danger} loading={busy} onClick={click}>
        {action.title}
      </Button>
      {form && <FormModal action={action} busy={busy} onClose={() => setForm(false)} onSubmit={async (v) => (await run(v)) && setForm(false)} />}
      {edit && action.edit && <DocModal plugin={plugin} action={action} title={context} onClose={() => setEdit(false)} onSaved={onChanged} />}
      {result && (
        <Modal title={action.title} onClose={() => setResult(null)}>
          {result.error ? <Banner kind="error">{result.error}</Banner> : <pre className="small" style={{ whiteSpace: 'pre-wrap' }}>{result.text}</pre>}
        </Modal>
      )}
    </>
  )
}

function FormModal({
  action,
  busy,
  onClose,
  onSubmit,
}: {
  action: PluginAction
  busy: boolean
  onClose: () => void
  onSubmit: (values: Record<string, unknown>) => void
}) {
  const { t } = useTranslation()
  const [values, setValues] = useState<Record<string, unknown>>(() =>
    Object.fromEntries((action.fields ?? []).filter((f) => f.default !== undefined).map((f) => [f.name, f.default])),
  )
  const missing = (action.fields ?? []).filter((f) => f.required && (values[f.name] === undefined || values[f.name] === ''))
  const set = (f: PluginField, v: unknown) => setValues((cur) => ({ ...cur, [f.name]: v }))
  return (
    <Modal title={action.title} onClose={onClose} width={560} maskClosable={false}>
      <div className="col" style={{ gap: '0.6rem' }}>
        {(action.fields ?? []).map((f) => (
          <label key={f.name} className="small col" style={{ gap: '0.2rem' }}>
            <span>
              {f.title}
              {f.required && ' *'}
            </span>
            {f.type === 'textarea' ? (
              <CodeEditor value={String(values[f.name] ?? '')} onChange={(e) => set(f, e.target.value)} rows={8} />
            ) : f.type === 'number' ? (
              <InputNumber style={{ width: '100%' }} value={values[f.name] as number | undefined} onChange={(v) => set(f, v)} placeholder={f.placeholder} />
            ) : f.type === 'select' ? (
              <Select
                value={values[f.name] as string | undefined}
                onChange={(v) => set(f, v)}
                options={(f.options ?? []).map((o) => ({ value: o.value, label: o.title || o.value }))}
                placeholder={f.placeholder}
              />
            ) : f.type === 'bool' ? (
              <Checkbox checked={!!values[f.name]} onChange={(e) => set(f, e.target.checked)} />
            ) : (
              <Input value={String(values[f.name] ?? '')} onChange={(e) => set(f, e.target.value)} placeholder={f.placeholder} />
            )}
            {f.hint && <span className="muted">{f.hint}</span>}
          </label>
        ))}
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
          <Button type="primary" danger={action.danger} loading={busy} disabled={missing.length > 0} onClick={() => onSubmit(values)}>
            {action.title}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          {missing.length > 0 && <span className="small muted">{t('plugins.fieldsMissing', { list: missing.map((f) => f.title).join(', ') })}</span>}
        </div>
      </div>
    </Modal>
  )
}

interface DocVersion {
  ts: string
  author?: string
  note?: string
  text: string
}

/** Документ плагина (файл зоны и т. п.): правка с диффом перед записью,
 * история версий в хабе, «Вернуть в редактор». */
function DocModal({
  plugin,
  action,
  title,
  onClose,
  onSaved,
}: {
  plugin: PluginInfo
  action: PluginAction
  title?: string
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const e = action.edit!
  const query = `get=${encodeURIComponent(e.get)}&doc=${encodeURIComponent(e.doc)}&args=${encodeURIComponent(JSON.stringify(action.args ?? {}))}`
  const [doc, setDoc] = useState<{ text: string; versions: DocVersion[] } | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [history, setHistory] = useState(false)
  const load = async () => {
    try {
      const d = await api<{ text: string; versions: DocVersion[] }>(`/hub/plugins/${plugin.name}/doc?${query}`)
      setDoc(d)
      setDraft(d.text)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }
  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- один раз на открытие окна
  }, [])
  if (!doc) {
    return (
      <Modal title={action.title} onClose={onClose}>
        {error ? <Banner kind="error">{error}</Banner> : <Loading what={action.title} />}
      </Modal>
    )
  }
  return (
    <>
      <EditTextModal
        title={`${action.title}${title ? ` — ${title}` : ''}`}
        saved={doc.text}
        draft={draft}
        onDraft={setDraft}
        busy={busy}
        fields={
          <div className="col" style={{ gap: '0.4rem', marginBottom: '0.5rem' }}>
            {error && <Banner kind="error">{error}</Banner>}
            <Input placeholder={t('plugins.note')} value={note} onChange={(ev) => setNote(ev.target.value)} />
          </div>
        }
        onHistory={() => setHistory(true)}
        onClose={onClose}
        onSave={async () => {
          setBusy(true)
          setError(null)
          try {
            await api(`/hub/plugins/${plugin.name}/doc`, {
              method: 'PUT',
              body: { get: e.get, put: e.put, doc: e.doc, args: action.args ?? {}, text: draft, base: doc.text, note },
              timeoutMs: 120_000,
            })
            onSaved()
            return true
          } catch (err) {
            setError(err instanceof Error ? err.message : String(err))
            return false
          } finally {
            setBusy(false)
          }
        }}
      />
      {history && (
        <DocHistory
          versions={doc.versions}
          current={doc.text}
          onRestore={(text) => {
            setDraft(text)
            setHistory(false)
          }}
          onClose={() => setHistory(false)}
        />
      )}
    </>
  )
}

function DocHistory({
  versions,
  current,
  onRestore,
  onClose,
}: {
  versions: DocVersion[]
  current: string
  onRestore: (text: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState<number | null>(null)
  const list = [...versions].reverse()
  return (
    <Modal title={t('configs.versionHistoryTitle')} onClose={onClose} width={1000} sizeKey="versions">
      {list.length === 0 ? (
        <p className="small muted">{t('plugins.noHistory')}</p>
      ) : (
        <div className="table-wrap">
          <DataTable<DocVersion & { n: number }>
            rowKey={(v) => String(v.n)}
            dataSource={list.map((v, i) => ({ ...v, n: list.length - i }))}
            columns={[
              { title: '№', dataIndex: 'n', key: 'n' },
              { title: t('plugins.when'), key: 'ts', render: (_: unknown, v: DocVersion) => formatDateTime(v.ts) },
              { title: t('plugins.author'), dataIndex: 'author', key: 'author' },
              { title: t('plugins.note'), dataIndex: 'note', key: 'note' },
              {
                title: '',
                key: 'a',
                render: (_: unknown, v: DocVersion & { n: number }) => (
                  <div className="row" style={{ gap: '0.3rem' }}>
                    <Button size="small" onClick={() => setOpen(v.n)}>
                      {t('plugins.diffWithCurrent')}
                    </Button>
                    <Button size="small" onClick={() => onRestore(v.text)}>
                      {t('plugins.restore')}
                    </Button>
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
      {open !== null && (
        <Modal title={t('plugins.diffWithCurrent')} onClose={() => setOpen(null)} width={900} sizeKey="diff">
          {(() => {
            const v = list[list.length - open]
            const d = unifiedDiff(v.text, current, `№${open}`, t('plugins.current'))
            return d ? <DiffView text={d} /> : <p className="small muted">{t('editModal.noChanges')}</p>
          })()}
        </Modal>
      )}
    </Modal>
  )
}
