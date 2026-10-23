import { useEffect, useRef, useState } from 'react'
import { Button, Checkbox, Input, Select, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, apiURL, useApi } from '../api'
import type { HubHost, Job, Me } from '../types'
import { Banner, Card, ErrorNote, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { EditTextModal } from '../components/EditTextModal'
import { confirmAction } from '../components/confirm'
import { TitleHelp } from '../components/Docs'
import { JobLogModal } from './Jobs'

/**
 * «Профили» → «Сервисы»: описания сервисов (YAML, internal/svcdesc) хаба
 * с историей правок; встроенные — для просмотра и перекрытия. На хосты
 * описания уходят уровнем «профиль» заданием «Доставить на хосты».
 * Журнал разбора и конструктор путей работают на выбранном хосте.
 */

interface Check {
  name: string
  title?: string
  engine?: string
  disabled?: boolean
  sources: number
  endpoints: number
  upstreams: number
  rules: number
  root_commands?: string[]
  error?: string
}

interface OwnRow {
  id: number
  name: string
  note?: string
  author?: string
  updated_at: string
  check: Check
}

interface BuiltinRow extends Check {
  file: string
  content: string
}

interface Version {
  id: number
  ts: string
  author?: string
  note?: string
  content?: string
}

const NEW_TEMPLATE = `name: myservice
title: My service
detect:
  any:
    - file: /etc/myservice/config.yml
sources:
  - id: config
    file: /etc/myservice/config.yml
extract:
  endpoints:
    - from: config
      each: "$.listeners[*]"
      fields:
        address: "@.address | host"
        port: "@.address | port"
rules: []
`

function hostPath(id: number) {
  return `/hosts/${id === -1 ? 'local' : id}`
}

function summary(t: (k: string, o?: Record<string, unknown>) => string, c: Check) {
  if (c.error) return <span className="small" style={{ color: 'var(--status-error, #d4380d)' }}>{c.error}</span>
  if (c.disabled) return <Tag>{t('svcdesc.disabled')}</Tag>
  if (c.engine) return <span className="small muted">{t('svcdesc.engine')}</span>
  return (
    <span className="small muted">
      {t('svcdesc.summary', { sources: c.sources, endpoints: c.endpoints, upstreams: c.upstreams, rules: c.rules })}
      {c.root_commands && c.root_commands.length > 0 && (
        <Tooltip title={c.root_commands.join('\n')}>
          <Tag color="orange" style={{ marginLeft: 6 }}>
            {t('svcdesc.asRoot', { count: c.root_commands.length })}
          </Tag>
        </Tooltip>
      )}
    </span>
  )
}

export default function ServiceDescriptions({ me }: { me: Me }) {
  const { t } = useTranslation()
  const list = useApi<{ own: OwnRow[]; builtin: BuiltinRow[] }>('/hub/svcdescs')
  const [editing, setEditing] = useState<{ id: number | null; saved: string } | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [check, setCheck] = useState<Check | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'error' | 'success'; text: string } | null>(null)
  const [history, setHistory] = useState<number | null>(null)
  const [viewing, setViewing] = useState<BuiltinRow | null>(null)
  const [deliver, setDeliver] = useState(false)
  const [job, setJob] = useState<Job | null>(null)
  const [testing, setTesting] = useState(false)
  const [builder, setBuilder] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const admin = me.is_admin

  const open = async (id: number | null, content?: string) => {
    let saved = ''
    if (id !== null) {
      const d = await api<{ content: string }>(`/hub/svcdescs/${id}`)
      saved = d.content
    }
    setEditing({ id, saved })
    setDraft(content ?? (id === null ? NEW_TEMPLATE : saved))
    setNote('')
    setCheck(null)
  }

  const runCheck = async (text: string) => {
    const c = await api<Check>('/hub/svcdescs/check', { method: 'POST', body: { content: text } })
    setCheck(c)
    return c
  }

  const save = async (): Promise<boolean> => {
    if (!editing) return false
    setBusy(true)
    try {
      const c = await runCheck(draft)
      if (c.error) return false
      const body = { content: draft, note }
      if (editing.id === null) await api('/hub/svcdescs', { method: 'POST', body })
      else await api(`/hub/svcdescs/${editing.id}`, { method: 'PUT', body })
      setNotice({ kind: 'success', text: t('svcdesc.saved', { name: c.name }) })
      void list.reload()
      return true
    } catch (err) {
      setCheck({ name: '', sources: 0, endpoints: 0, upstreams: 0, rules: 0, error: err instanceof Error ? err.message : String(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  const importFile = async (file: File) => {
    const text = await file.text()
    await open(null, text)
  }

  if (list.error) return <ErrorNote error={list.error} />
  if (!list.data) return <Loading what={t('svcdesc.title')} />
  const own = list.data.own
  const ownNames = new Set(own.map((o) => o.name))

  return (
    <div className="col" style={{ gap: '0.8rem' }}>
      <div className="page-head spread">
        <h2 style={{ margin: 0 }}>
          {t('svcdesc.title')}
          <TitleHelp>{t('svcdesc.hint')}</TitleHelp>
        </h2>
        {admin && (
          <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
            <Button type="primary" onClick={() => void open(null)}>
              {t('svcdesc.new')}
            </Button>
            <Button onClick={() => fileInput.current?.click()}>{t('svcdesc.import')}</Button>
            <Tooltip title={t('svcdesc.deliverHint')}>
              <Button onClick={() => setDeliver(true)}>{t('svcdesc.deliver')}</Button>
            </Tooltip>
            <input
              ref={fileInput}
              type="file"
              accept=".yaml,.yml"
              style={{ display: 'none' }}
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f) void importFile(f)
              }}
            />
          </div>
        )}
      </div>
      {notice && (
        <Banner kind={notice.kind} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}

      <Card title={t('svcdesc.own')} subtitle={t('svcdesc.ownHint')}>
        <DataTable<OwnRow>
          rowKey="id"
          dataSource={own}
          locale={{ emptyText: t('svcdesc.ownEmpty') }}
          columns={[
            {
              title: t('svcdesc.colName'),
              key: 'name',
              render: (_, r) => (
                <span>
                  <strong className="mono">{r.name}</strong>
                  {r.check.title && <span className="muted"> · {r.check.title}</span>}
                  {list.data!.builtin.some((b) => b.name === r.name) && (
                    <Tag color="blue" style={{ marginLeft: 6 }}>
                      {t('svcdesc.overrides')}
                    </Tag>
                  )}
                </span>
              ),
            },
            { title: t('svcdesc.colWhat'), key: 'what', render: (_, r) => summary(t, r.check) },
            {
              title: t('svcdesc.colUpdated'),
              key: 'updated',
              render: (_, r) => (
                <span className="small muted">
                  {formatDateTime(r.updated_at)} · {r.author || '—'}
                </span>
              ),
            },
            {
              title: '',
              key: 'actions',
              render: (_, r) => (
                <div className="row" style={{ gap: '0.3rem' }}>
                  <Button size="small" onClick={() => void open(r.id)}>
                    {admin ? t('common.edit') : t('svcdesc.view')}
                  </Button>
                  <Button size="small" onClick={() => setHistory(r.id)}>
                    {t('svcdesc.history')}
                  </Button>
                  <Button size="small" href={apiURL(`/hub/svcdescs/${r.id}/export`)}>
                    {t('svcdesc.export')}
                  </Button>
                  {admin && (
                    <Button
                      size="small"
                      danger
                      onClick={async () => {
                        if (!(await confirmAction(t('svcdesc.deleteConfirm', { name: r.name })))) return
                        await api(`/hub/svcdescs/${r.id}`, { method: 'DELETE' })
                        void list.reload()
                      }}
                    >
                      {t('common.delete')}
                    </Button>
                  )}
                </div>
              ),
            },
          ]}
        />
      </Card>

      <Card title={t('svcdesc.builtin')} subtitle={t('svcdesc.builtinHint')}>
        <DataTable<BuiltinRow>
          rowKey="file"
          dataSource={list.data.builtin}
          columns={[
            {
              title: t('svcdesc.colName'),
              key: 'name',
              render: (_, r) => (
                <span>
                  <strong className="mono">{r.name}</strong>
                  {r.title && <span className="muted"> · {r.title}</span>}
                  {ownNames.has(r.name) && (
                    <Tag color="orange" style={{ marginLeft: 6 }}>
                      {t('svcdesc.overridden')}
                    </Tag>
                  )}
                </span>
              ),
            },
            { title: t('svcdesc.colWhat'), key: 'what', render: (_, r) => summary(t, r) },
            {
              title: '',
              key: 'actions',
              render: (_, r) => (
                <div className="row" style={{ gap: '0.3rem' }}>
                  <Button size="small" onClick={() => setViewing(r)}>
                    {t('svcdesc.view')}
                  </Button>
                  {admin && !ownNames.has(r.name) && (
                    <Tooltip title={t('svcdesc.overrideHint')}>
                      <Button size="small" onClick={() => void open(null, r.content)}>
                        {t('svcdesc.override')}
                      </Button>
                    </Tooltip>
                  )}
                </div>
              ),
            },
          ]}
        />
      </Card>

      {editing && (
        <EditTextModal
          title={editing.id === null ? t('svcdesc.new') : t('svcdesc.editTitle')}
          saved={editing.saved}
          draft={draft}
          onDraft={(v) => {
            setDraft(v)
            setCheck(null)
          }}
          busy={busy}
          saveBlocked={!check ? t('svcdesc.needCheck') : check.error ? t('svcdesc.needFix') : undefined}
          onSave={save}
          onHistory={editing.id !== null ? () => setHistory(editing.id) : undefined}
          onClose={() => setEditing(null)}
          fields={
            <div className="filters" style={{ marginBottom: '0.5rem', alignItems: 'flex-end' }}>
              <label style={{ flex: 1, minWidth: '14rem' }}>
                {t('svcdesc.note')}
                <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('svcdesc.notePlaceholder')} />
              </label>
              <Button onClick={() => void runCheck(draft)}>{t('svcdesc.check')}</Button>
              <Button onClick={() => setTesting(true)}>{t('svcdesc.test')}</Button>
              <Button onClick={() => setBuilder(true)}>{t('svcdesc.builder')}</Button>
            </div>
          }
          below={
            check &&
            (check.error ? (
              <Banner kind="error">{check.error}</Banner>
            ) : (
              <Banner kind="success">
                {t('svcdesc.checkOK', { name: check.name })} {summary(t, check)}
              </Banner>
            ))
          }
        />
      )}

      {history !== null && (
        <Modal title={t('svcdesc.history')} onClose={() => setHistory(null)} width={720}>
          <Versions
            id={history}
            onRestore={async (content) => {
              setHistory(null)
              if (editing) setDraft(content)
              else await open(history, content)
            }}
          />
        </Modal>
      )}

      {viewing && (
        <Modal title={`${viewing.name} · ${t('svcdesc.builtinOne')}`} onClose={() => setViewing(null)} width={820}>
          <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', maxHeight: '65vh', overflow: 'auto' }}>
            {viewing.content}
          </pre>
        </Modal>
      )}

      {deliver && (
        <DeliverModal
          onClose={() => setDeliver(false)}
          onStarted={(j) => {
            setDeliver(false)
            setJob(j)
          }}
        />
      )}
      {job && <JobLogModal job={job} onClose={() => setJob(null)} />}
      {testing && <TestModal content={draft} onClose={() => setTesting(false)} />}
      {builder && <PathBuilder onClose={() => setBuilder(false)} />}
    </div>
  )
}

function Versions({ id, onRestore }: { id: number; onRestore: (content: string) => void }) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: Version[] }>(`/hub/svcdescs/${id}/versions`)
  if (versions.loading && !versions.data) return <Loading what={t('svcdesc.history')} />
  return (
    <div className="col" style={{ gap: '0.35rem' }}>
      {(versions.data?.versions ?? []).map((v) => (
        <div key={v.id} className="row spread">
          <span className="small">
            {formatDateTime(v.ts)} · {v.author || '—'}
            {v.note ? ` · ${v.note}` : ''}
          </span>
          <Button
            size="small"
            onClick={async () => {
              const full = await api<Version>(`/hub/svcdescs/versions/${v.id}`)
              onRestore(full.content ?? '')
            }}
          >
            {t('profiles.restoreVersion')}
          </Button>
        </div>
      ))}
    </div>
  )
}

/** Выбор хоста хаба (машина хаба — первой). */
function HostSelect({ value, onChange }: { value: number | null; onChange: (id: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  return (
    <Select
      style={{ minWidth: '14rem' }}
      placeholder={t('svcdesc.pickHost')}
      value={value ?? undefined}
      onChange={onChange}
      loading={hosts.loading}
      showSearch
      optionFilterProp="label"
      options={(hosts.data ?? []).map((h) => ({ value: h.id, label: h.name }))}
    />
  )
}

function DeliverModal({ onClose, onStarted }: { onClose: () => void; onStarted: (j: Job) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [picked, setPicked] = useState<number[]>([])
  const [error, setError] = useState<string | null>(null)
  const all = (hosts.data ?? []).map((h) => h.id)
  return (
    <Modal title={t('svcdesc.deliver')} onClose={onClose} width={560} closeLabel={t('common.cancel')}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <span className="small muted">{t('svcdesc.deliverBody')}</span>
        <Checkbox
          checked={picked.length > 0 && picked.length === all.length}
          indeterminate={picked.length > 0 && picked.length < all.length}
          onChange={(e) => setPicked(e.target.checked ? all : [])}
        >
          {t('svcdesc.allHosts')}
        </Checkbox>
        <div className="col" style={{ gap: '0.2rem', maxHeight: '45vh', overflow: 'auto' }}>
          {(hosts.data ?? []).map((h) => (
            <Checkbox
              key={h.id}
              checked={picked.includes(h.id)}
              onChange={(e) => setPicked(e.target.checked ? [...picked, h.id] : picked.filter((x) => x !== h.id))}
            >
              {h.name}
            </Checkbox>
          ))}
        </div>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ justifyContent: 'flex-end' }}>
          <Button
            type="primary"
            disabled={picked.length === 0}
            onClick={async () => {
              try {
                const res = await api<{ job_id: number }>('/hub/svcdescs/deliver', { method: 'POST', body: { host_ids: picked } })
                onStarted(await api<Job>(`/jobs/${res.job_id}`))
              } catch (err) {
                setError(err instanceof Error ? err.message : String(err))
              }
            }}
          >
            {t('svcdesc.deliverStart', { count: picked.length })}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

interface TestResult {
  error?: string
  log: string[]
  endpoints: { service: string; address: string; port: number; protocol: string; label: string; tls: boolean }[]
  upstreams: { name: string; servers: { host: string; port: number }[] }[]
  findings: { rule: string; severity: string; title: string; object?: string }[]
  warnings: string[]
  version?: string
}

/** Журнал разбора: черновик описания на выбранном хосте (ничего не
 * сохраняется ни на хабе, ни на хосте). */
function TestModal({ content, onClose }: { content: string; onClose: () => void }) {
  const { t } = useTranslation()
  const [host, setHost] = useState<number | null>(null)
  const [res, setRes] = useState<TestResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const run = async (id: number) => {
    setBusy(true)
    setError(null)
    try {
      setRes(await api<TestResult>(`${hostPath(id)}/svcdesc/test`, { method: 'POST', body: { content }, timeoutMs: 120_000 }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('svcdesc.test')} onClose={onClose} width={900}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <div className="row" style={{ gap: '0.4rem' }}>
          <HostSelect value={host} onChange={setHost} />
          <Button type="primary" disabled={host === null} loading={busy} onClick={() => host !== null && void run(host)}>
            {t('svcdesc.testRun')}
          </Button>
        </div>
        <span className="small muted">{t('svcdesc.testHint')}</span>
        {error && <Banner kind="error">{error}</Banner>}
        {res?.error && <Banner kind="error">{res.error}</Banner>}
        {res && !res.error && (
          <>
            <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', maxHeight: '40vh', overflow: 'auto', margin: 0 }}>
              {res.log.join('\n')}
            </pre>
            {res.version && <span className="small">{t('svcdesc.version', { version: res.version })}</span>}
            {res.endpoints.length > 0 && (
              <div className="small">
                <strong>{t('svcdesc.endpoints')}</strong>{' '}
                {res.endpoints.map((e, i) => (
                  <Tag key={i} className="mono">
                    {e.address || '*'}:{e.port}/{e.protocol}
                    {e.tls ? ' TLS' : ''} · {e.label}
                  </Tag>
                ))}
              </div>
            )}
            {res.upstreams.length > 0 && (
              <div className="small">
                <strong>{t('svcdesc.upstreams')}</strong>{' '}
                {res.upstreams.map((u, i) => (
                  <Tag key={i} className="mono">
                    {u.name} → {u.servers.map((s) => `${s.host}:${s.port}`).join(', ')}
                  </Tag>
                ))}
              </div>
            )}
            {res.findings.map((f, i) => (
              <Banner key={i} kind={f.severity === 'critical' || f.severity === 'high' ? 'error' : 'warn'}>
                [{f.severity}] {f.title} {f.object ? <span className="muted">· {f.object}</span> : null}
              </Banner>
            ))}
            {res.warnings.map((w, i) => (
              <Banner key={i} kind="warn">
                {w}
              </Banner>
            ))}
          </>
        )}
      </div>
    </Modal>
  )
}

type Seg = string | number

function segText(s: Seg): string {
  if (typeof s === 'number') return `[${s}]`
  if (s === '*') return '.*'
  if (/^[A-Za-z_][A-Za-z0-9_-]*$/.test(s)) return `.${s}`
  return `['${s.replace(/\\/g, '\\\\').replace(/'/g, "\\'")}']`
}

function pathText(root: '$' | '@', segs: Seg[]): string {
  return root + segs.map(segText).join('')
}

/** Узел дерева образца: щелчок — путь к нему. */
function TreeNode({ k, value, segs, depth, onPick, picked }: { k: Seg | null; value: unknown; segs: Seg[]; depth: number; onPick: (s: Seg[]) => void; picked: string }) {
  const [open, setOpen] = useState(depth < 2)
  const isObj = value !== null && typeof value === 'object'
  const entries: [Seg, unknown][] = Array.isArray(value)
    ? value.map((v, i) => [i, v])
    : isObj
      ? Object.entries(value as Record<string, unknown>)
      : []
  const me = pathText('$', segs)
  const label = k === null ? '$' : typeof k === 'number' ? `[${k}]` : k
  return (
    <div style={{ paddingLeft: depth === 0 ? 0 : 14 }}>
      <span className="mono small" style={{ cursor: 'pointer' }}>
        {isObj && (
          <span onClick={() => setOpen(!open)} style={{ display: 'inline-block', width: 14 }}>
            {open ? '▾' : '▸'}
          </span>
        )}
        <span onClick={() => onPick(segs)} style={{ background: picked === me ? 'var(--wash)' : undefined, fontWeight: picked === me ? 600 : undefined }}>
          {label}
        </span>
        {!isObj && <span className="muted">: {JSON.stringify(value)}</span>}
        {isObj && !open && <span className="muted"> {Array.isArray(value) ? `[${entries.length}]` : `{${entries.length}}`}</span>}
      </span>
      {isObj && open && entries.slice(0, 300).map(([ck, cv]) => <TreeNode key={String(ck)} k={ck} value={cv} segs={[...segs, ck]} depth={depth + 1} onPick={onPick} picked={picked} />)}
    </div>
  )
}

interface EvalResult {
  items: { key: Seg; value: unknown; when?: boolean; values?: unknown[] }[]
  error?: string
}

/** Конструктор путей: образец источника с хоста деревом; щелчок по узлу —
 * путь; each, when и поле считаются над образцом тут же. */
function PathBuilder({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const [host, setHost] = useState<number | null>(null)
  const [kind, setKind] = useState<'file' | 'http' | 'command'>('file')
  const [target, setTarget] = useState('')
  const [format, setFormat] = useState('')
  const [docs, setDocs] = useState<{ file?: string; tree: unknown }[] | null>(null)
  const [docIdx, setDocIdx] = useState(0)
  const [warnings, setWarnings] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [picked, setPicked] = useState<Seg[] | null>(null)
  const [each, setEach] = useState('$')
  const [when, setWhen] = useState('')
  const [expr, setExpr] = useState('')
  const [result, setResult] = useState<EvalResult | null>(null)

  const load = async () => {
    if (host === null || !target.trim()) return
    setBusy(true)
    setError(null)
    try {
      const source: Record<string, unknown> = { format: format || undefined }
      if (kind === 'file') source.file = target.trim()
      else if (kind === 'http') source.http = target.trim()
      else source.command = target.trim().split(/\s+/)
      const res = await api<{ docs: { file?: string; tree: unknown }[]; warnings: string[] }>(`${hostPath(host)}/svcdesc/sample`, {
        method: 'POST',
        body: { source },
        timeoutMs: 90_000,
      })
      setDocs(res.docs)
      setWarnings(res.warnings)
      setDocIdx(0)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const doc = docs?.[docIdx]
  useEffect(() => {
    if (!doc) return
    const timer = setTimeout(async () => {
      try {
        setResult(await api<EvalResult>('/hub/svcdescs/eval', { method: 'POST', body: { tree: doc.tree, each, when, expr } }))
      } catch (err) {
        setResult({ items: [], error: err instanceof Error ? err.message : String(err) })
      }
    }, 350)
    return () => clearTimeout(timer)
  }, [doc, each, when, expr])

  const pickedText = picked ? pathText('$', picked) : ''
  const generalize = (segs: Seg[]): Seg[] => (segs.length === 0 ? segs : [...segs.slice(0, -1), '*'])
  const eachSegs = (): number => {
    // Сколько сегментов у each (по тексту: $ и . или [ — грубо, но для
    // путей, собранных здесь, точно).
    const m = each.match(/(\.[^.[]+|\[[^\]]*\])/g)
    return m ? m.length : 0
  }

  return (
    <Modal title={t('svcdesc.builder')} onClose={onClose} width={1100}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <span className="small muted">{t('svcdesc.builderHint')}</span>
        <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
          <HostSelect value={host} onChange={setHost} />
          <Select
            value={kind}
            onChange={setKind}
            style={{ width: 120 }}
            options={[
              { value: 'file', label: t('svcdesc.kindFile') },
              { value: 'http', label: 'HTTP' },
              { value: 'command', label: t('svcdesc.kindCommand') },
            ]}
          />
          <Input
            style={{ flex: 1, minWidth: '16rem' }}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder={kind === 'file' ? '/etc/traefik/traefik.yml' : kind === 'http' ? 'http://127.0.0.1:8080/api/rawdata' : 'rabbitmq-diagnostics -q listeners --formatter json'}
            onPressEnter={() => void load()}
          />
          <Select
            value={format}
            onChange={setFormat}
            style={{ width: 130 }}
            options={[{ value: '', label: t('svcdesc.formatAuto') }, ...['yaml', 'json', 'toml', 'ini', 'env', 'properties', 'lines', 'xml', 'text'].map((f) => ({ value: f, label: f }))]}
          />
          <Button type="primary" loading={busy} disabled={host === null || !target.trim()} onClick={() => void load()}>
            {t('svcdesc.loadSample')}
          </Button>
        </div>
        {error && <Banner kind="error">{error}</Banner>}
        {warnings.map((w, i) => (
          <Banner key={i} kind="warn">
            {w}
          </Banner>
        ))}
        {docs && docs.length === 0 && !error && <Banner kind="warn">{t('svcdesc.noDocs')}</Banner>}
        {docs && docs.length > 1 && (
          <Select value={docIdx} onChange={setDocIdx} options={docs.map((d, i) => ({ value: i, label: d.file || `#${i + 1}` }))} style={{ maxWidth: 520 }} />
        )}
        {doc && (
          <div className="row" style={{ gap: '0.8rem', alignItems: 'flex-start' }}>
            <div style={{ flex: 1, minWidth: 0, maxHeight: '55vh', overflow: 'auto', border: '1px solid var(--border)', borderRadius: 6, padding: '0.4rem' }}>
              <TreeNode k={null} value={doc.tree} segs={[]} depth={0} onPick={setPicked} picked={pickedText} />
            </div>
            <div className="col" style={{ flex: 1, minWidth: 0, gap: '0.45rem' }}>
              <label className="col small">
                {t('svcdesc.pickedPath')}
                <Input className="mono" value={pickedText} readOnly />
              </label>
              <div className="row" style={{ gap: '0.3rem', flexWrap: 'wrap' }}>
                <Button size="small" disabled={!picked} onClick={() => picked && setEach(pathText('$', generalize(picked)))}>
                  {t('svcdesc.asEach')}
                </Button>
                <Button size="small" disabled={!picked} onClick={() => picked && setEach(pathText('$', picked))}>
                  {t('svcdesc.asEachExact')}
                </Button>
                <Button size="small" disabled={!picked} onClick={() => picked && setExpr(pathText('@', picked.slice(eachSegs())))}>
                  {t('svcdesc.asField')}
                </Button>
              </div>
              <label className="col small">
                each
                <Input className="mono" value={each} onChange={(e) => setEach(e.target.value)} />
              </label>
              <label className="col small">
                when
                <Input className="mono" value={when} onChange={(e) => setWhen(e.target.value)} placeholder="@.enabled == true" />
              </label>
              <label className="col small">
                {t('svcdesc.field')}
                <Input className="mono" value={expr} onChange={(e) => setExpr(e.target.value)} placeholder="@.address | port" />
              </label>
              {result?.error && <Banner kind="error">{result.error}</Banner>}
              {result && !result.error && (
                <div style={{ maxHeight: '30vh', overflow: 'auto' }}>
                  <span className="small muted">{t('svcdesc.items', { count: result.items.length })}</span>
                  <table className="small mono" style={{ width: '100%' }}>
                    <tbody>
                      {result.items.map((it, i) => (
                        <tr key={i} style={{ opacity: it.when === false ? 0.45 : 1 }}>
                          <td style={{ paddingRight: 8 }}>@key={String(it.key ?? '')}</td>
                          <td>{expr ? JSON.stringify(it.values ?? []) : JSON.stringify(it.value).slice(0, 120)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
