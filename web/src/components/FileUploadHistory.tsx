import { useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Progress, Segmented, Select, Tabs, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { ConfigVersion, Me } from '../types'
import { Banner, DiffView, Modal, Spinner, formatDateTime } from './ui'
import { unifiedDiff } from './textDiff'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { VersionHistory } from './VersionHistory'
import { msg, tx, type Msg } from '../msg'

/**
 * История загрузок «Дисков → Файлов»: план загрузки с диффом и выбором
 * файлов, список загрузок с откатом, защищённые файлы папки, хранилище
 * истории с лимитами и ручной чисткой, история одного файла.
 */

/** Файл из очереди загрузки (см. FileBrowser). */
export interface PendingFile {
  rel: string
  file: File
}

export interface PlanItem {
  rel: string
  path?: string
  status: 'new' | 'same' | 'changed' | 'conflict' | 'invalid'
  protected?: boolean
  text?: boolean
  existing_size?: number
  error?: string
}

export interface HistoryUsage {
  used: number
  limit: number
  percent: number
  limits: { per_upload_mb: number; total_mb: number; days: number }
}

export interface UploadPlan {
  items: PlanItem[]
  protect: string[]
  history?: HistoryUsage
}

/** Больше — сумму в браузере не считаем (память): сервер сочтёт такой
 * файл изменённым, если он уже есть. */
const HASH_LIMIT = 64 << 20
/** Дифф текста — для файлов не больше, чем редактор. */
const DIFF_LIMIT = 2 << 20

export async function sha256Of(file: File): Promise<string> {
  if (file.size > HASH_LIMIT || !crypto?.subtle) return ''
  const buf = await file.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buf)
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('')
}

type Filter = 'all' | 'new' | 'changed' | 'same' | 'protected'

/**
 * План загрузки: что ляжет новым, что заменит, что совпадает, что
 * защищено. Галочки — какие файлы грузить; у изменённого текста — дифф
 * «на хосте → загружаемый». Записывается только по «Загрузить».
 */
export function UploadPlanModal({
  dir,
  pending,
  plan,
  onClose,
  onStart,
  onEditProtect,
}: {
  dir: string
  pending: PendingFile[]
  plan: UploadPlan
  onClose: () => void
  onStart: (files: PendingFile[], force: Set<string>, note: string) => void
  onEditProtect: () => void
}) {
  const { t } = useTranslation()
  const byRel = useMemo(() => new Map(pending.map((p) => [p.rel, p])), [pending])
  const selectable = (it: PlanItem) => it.status === 'new' || it.status === 'changed'
  const [checked, setChecked] = useState<Set<string>>(
    () => new Set(plan.items.filter((it) => selectable(it) && !(it.protected && it.status === 'changed')).map((it) => it.rel)),
  )
  const [filter, setFilter] = useState<Filter>('all')
  const [note, setNote] = useState('')
  const [diff, setDiff] = useState<{ rel: string; text: string } | null>(null)
  const [diffBusy, setDiffBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const count = (f: (it: PlanItem) => boolean) => plan.items.filter(f).length
  const counts = {
    new: count((it) => it.status === 'new'),
    changed: count((it) => it.status === 'changed'),
    same: count((it) => it.status === 'same'),
    protected: count((it) => !!it.protected && it.status === 'changed'),
    bad: count((it) => it.status === 'conflict' || it.status === 'invalid'),
  }
  const rows = plan.items.filter((it) =>
    filter === 'all' ? true : filter === 'protected' ? it.protected && it.status === 'changed' : it.status === filter,
  )
  // Большие перезаписываемые файлы сверх лимита загрузки не войдут в историю.
  const perUpload = (plan.history?.limits.per_upload_mb ?? 200) * 1024 * 1024
  let budget = 0
  let overBudget = 0
  for (const it of plan.items) {
    if (it.status !== 'changed' || !checked.has(it.rel) || (it.existing_size ?? 0) <= DIFF_LIMIT) continue
    budget += it.existing_size ?? 0
    if (budget > perUpload) overBudget++
  }

  async function showDiff(it: PlanItem) {
    const p = byRel.get(it.rel)
    if (!p || !it.path) return
    setDiffBusy(it.rel)
    setError(null)
    try {
      const cur = await api<{ content: string }>(`/files/read${qs({ path: it.path })}`)
      const next = await p.file.text()
      setDiff({ rel: it.rel, text: unifiedDiff(cur.content, next, it.path, it.path) || t('configs.noDiff') })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setDiffBusy(null)
    }
  }

  const statusTag = (it: PlanItem) => {
    const color = { new: 'success', changed: 'processing', same: 'default', conflict: 'error', invalid: 'error' }[it.status]
    return <Tag color={color}>{t(`files.plan.${it.status}`)}</Tag>
  }
  const selected = plan.items.filter((it) => checked.has(it.rel))
  const force = new Set(selected.filter((it) => it.protected && it.status === 'changed').map((it) => it.rel))

  return (
    <Modal title={t('files.plan.title', { dir })} onClose={onClose} closeLabel={t('common.cancel')} width="min(96vw, 1100px)" maskClosable={false}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap' }}>
          <Tag color="success">{t('files.plan.countNew', { count: counts.new })}</Tag>
          <Tag color="processing">{t('files.plan.countChanged', { count: counts.changed })}</Tag>
          <Tag>{t('files.plan.countSame', { count: counts.same })}</Tag>
          {counts.protected > 0 && <Tag color="warning">{t('files.plan.countProtected', { count: counts.protected })}</Tag>}
          {counts.bad > 0 && <Tag color="error">{t('files.plan.countBad', { count: counts.bad })}</Tag>}
        </div>
        <div className="muted">{t('files.plan.hint')}</div>
        <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <span className="muted">{t('files.plan.protectList')}:</span>
          <span className="mono">{plan.protect.join('  ')}</span>
          <Button size="small" type="link" onClick={onEditProtect}>
            {t('files.protect.edit')}
          </Button>
        </div>
        {plan.history && plan.history.percent >= 80 && (
          <Banner kind="warn">{t('files.history.nearFull', { percent: plan.history.percent })}</Banner>
        )}
        {overBudget > 0 && <Banner kind="warn">{t('files.plan.overBudget', { count: overBudget, mb: plan.history?.limits.per_upload_mb ?? 200 })}</Banner>}
        {error && <Banner kind="error">{error}</Banner>}
        <Segmented<Filter>
          size="small"
          value={filter}
          onChange={setFilter}
          options={[
            { value: 'all', label: t('files.plan.filterAll', { count: plan.items.length }) },
            { value: 'new', label: t('files.plan.new') },
            { value: 'changed', label: t('files.plan.changed') },
            { value: 'same', label: t('files.plan.same') },
            { value: 'protected', label: t('files.plan.protected') },
          ]}
        />
        <div className="table-wrap">
          <DataTable<PlanItem>
            dataSource={rows}
            rowKey="rel"
            size="small"
            pagination={rows.length > 200 ? { pageSize: 200 } : false}
            columns={[
              {
                title: '',
                key: 'check',
                width: 36,
                render: (_, it) => (
                  <Checkbox
                    checked={checked.has(it.rel)}
                    disabled={!selectable(it)}
                    onChange={(e) => {
                      const next = new Set(checked)
                      if (e.target.checked) next.add(it.rel)
                      else next.delete(it.rel)
                      setChecked(next)
                    }}
                  />
                ),
              },
              {
                title: t('files.colName'),
                key: 'rel',
                render: (_, it) => {
                  const i = it.rel.lastIndexOf('/')
                  return (
                    <span className="mono">
                      {i >= 0 && <span className="muted">{it.rel.slice(0, i + 1)}</span>}
                      {it.rel.slice(i + 1)}
                    </span>
                  )
                },
              },
              {
                title: t('files.plan.colStatus'),
                key: 'status',
                render: (_, it) => (
                  <>
                    {statusTag(it)}
                    {it.protected && it.status === 'changed' && <Tag color="warning">{t('files.plan.protectedTag')}</Tag>}
                    {it.error && <div className="small muted">{it.error}</div>}
                  </>
                ),
              },
              {
                title: t('files.colSize'),
                key: 'size',
                align: 'right',
                render: (_, it) => {
                  const size = byRel.get(it.rel)?.file.size ?? 0
                  return (
                    <span className="small nowrap">
                      {it.status === 'changed' ? `${formatBytes(it.existing_size ?? 0)} → ${formatBytes(size)}` : formatBytes(size)}
                    </span>
                  )
                },
              },
              {
                title: '',
                key: 'diff',
                render: (_, it) =>
                  it.status === 'changed' && it.text && (byRel.get(it.rel)?.file.size ?? 0) <= DIFF_LIMIT ? (
                    <RowAction action="details" label={t('configs.diff')} loading={diffBusy === it.rel} onClick={() => void showDiff(it)} />
                  ) : null,
              },
            ]}
          />
        </div>
        <Input
          size="small"
          addonBefore={t('configs.editNote')}
          placeholder={t('files.plan.notePlaceholder')}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
        <div className="row" style={{ justifyContent: 'flex-end', gap: '0.5rem' }}>
          <Button
            type="primary"
            disabled={selected.length === 0}
            onClick={() => onStart(selected.map((it) => byRel.get(it.rel)).filter((p): p is PendingFile => !!p), force, note.trim())}
          >
            {t('files.plan.start', { count: selected.length })}
          </Button>
        </div>
      </div>
      {diff && (
        <Modal title={t('files.plan.diffTitle', { name: diff.rel })} onClose={() => setDiff(null)} width={900}>
          <DiffView text={diff.text} />
        </Modal>
      )}
    </Modal>
  )
}

interface FileUpload {
  id: number
  ts: string
  author: string
  dir: string
  note: string
  status: 'open' | 'done' | 'rolled_back'
  new: number
  changed: number
}

interface UploadItem {
  id: number
  path: string
  existed: boolean
  version_id: number
}

/** История загрузок в каталог: состав, откат заданием, удаление из истории. */
export function UploadsModal({ dir, onClose, onJob }: { dir: string; onClose: () => void; onJob: (jobID: number) => void }) {
  const { t } = useTranslation()
  const list = useApi<{ uploads: FileUpload[] }>(`/files/uploads${qs({ dir })}`)
  const [open, setOpen] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  async function rollback(u: FileUpload) {
    setError(null)
    try {
      const res = await api<{ items: UploadItem[] }>(`/files/uploads/${u.id}`)
      const restore = res.items.filter((it) => it.existed && it.version_id > 0).length
      const removed = res.items.filter((it) => !it.existed).length
      const noHistory = res.items.filter((it) => it.existed && it.version_id === 0).length
      if (!(await confirmAction(t('files.uploads.confirmRollback', { id: u.id, restore, removed, noHistory }), { okText: t('files.uploads.rollback') }))) return
      setBusy(u.id)
      const job = await api<{ job_id: number }>(`/files/uploads/${u.id}/rollback`, { method: 'POST' })
      onJob(job.job_id)
      void list.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  async function forget(u: FileUpload) {
    if (!(await confirmAction(t('files.uploads.confirmForget', { id: u.id })))) return
    setError(null)
    try {
      await api('/files/history/delete', { method: 'POST', body: { upload_id: u.id } })
      void list.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const rows = list.data?.uploads ?? []
  return (
    <Modal title={t('files.uploads.title', { dir })} onClose={onClose} width="min(96vw, 1000px)">
      {error && <Banner kind="error">{error}</Banner>}
      {list.error && <Banner kind="error">{list.error}</Banner>}
      {!list.data ? (
        <Spinner />
      ) : rows.length === 0 ? (
        <div className="chart-empty">{t('files.uploads.empty')}</div>
      ) : (
        <div className="table-wrap">
          <DataTable<FileUpload>
            dataSource={rows}
            rowKey="id"
            size="small"
            expandable={{
              expandedRowKeys: open ? [open] : [],
              onExpand: (exp, u) => setOpen(exp ? u.id : null),
              expandedRowRender: (u) => <UploadItems id={u.id} />,
            }}
            columns={[
              { title: '#', dataIndex: 'id', key: 'id', align: 'right' },
              { title: t('configs.colTs'), key: 'ts', render: (_, u) => <span className="small nowrap">{formatDateTime(u.ts)}</span> },
              { title: t('configs.colAuthor'), dataIndex: 'author', key: 'author', className: 'small' },
              { title: t('files.uploads.colDir'), key: 'dir', render: (_, u) => <span className="small mono">{u.dir}</span> },
              { title: t('configs.colNote'), dataIndex: 'note', key: 'note', className: 'small secondary' },
              {
                title: t('files.uploads.colFiles'),
                key: 'files',
                render: (_, u) => (
                  <span className="small nowrap">
                    {t('files.uploads.files', { new: u.new, changed: u.changed })}
                    {u.status === 'rolled_back' && <Tag style={{ marginLeft: 6 }}>{t('files.uploads.rolledBack')}</Tag>}
                  </span>
                ),
              },
              {
                title: '',
                key: 'actions',
                render: (_, u) => (
                  <div className="row row-nowrap">
                    {u.status === 'done' && (
                      <RowAction action="history" label={t('files.uploads.rollback')} loading={busy === u.id} onClick={() => void rollback(u)} />
                    )}
                    <RowAction action="delete" danger label={t('files.uploads.forget')} onClick={() => void forget(u)} />
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
    </Modal>
  )
}

function UploadItems({ id }: { id: number }) {
  const { t } = useTranslation()
  const res = useApi<{ items: UploadItem[] }>(`/files/uploads/${id}`)
  if (!res.data) return <Spinner />
  return (
    <div className="col small" style={{ gap: '0.15rem' }}>
      {res.data.items.map((it) => (
        <div key={it.id} className="row" style={{ gap: '0.5rem' }}>
          <Tag color={it.existed ? 'processing' : 'success'}>{t(it.existed ? 'files.plan.changed' : 'files.plan.new')}</Tag>
          <span className="mono">{it.path}</span>
          {it.existed && it.version_id === 0 && <span className="muted">{t('files.uploads.noHistory')}</span>}
        </div>
      ))}
    </div>
  )
}

/** Защищённые файлы папки: шаблоны правятся с диффом, у них своя история. */
export function ProtectModal({ dir, me, onClose, onSaved }: { dir: string; me: Me; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const cur = useApi<{ patterns: string[]; defaults: string[]; path: string }>(`/files/protect${qs({ dir })}`)
  const [text, setText] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [preview, setPreview] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const original = cur.data ? cur.data.patterns.join('\n') + '\n' : ''
  const value = text ?? original

  async function save() {
    setBusy(true)
    setError(null)
    try {
      await api('/files/protect', { method: 'PUT', body: { dir, patterns: value, note: note.trim() } })
      setPreview(null)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const editor = (
    <div className="col small" style={{ gap: '0.5rem' }}>
      <div className="muted">{t('files.protect.hint')}</div>
      <Input.TextArea className="mono" rows={10} value={value} onChange={(e) => setText(e.target.value)} />
      <Input size="small" addonBefore={t('configs.editNote')} value={note} onChange={(e) => setNote(e.target.value)} />
      <div className="row" style={{ gap: '0.5rem' }}>
        <Button
          type="primary"
          disabled={!cur.data || value.trim() === original.trim()}
          onClick={() => setPreview(unifiedDiff(original, value.endsWith('\n') ? value : value + '\n', dir, dir))}
        >
          {t('common.save')}
        </Button>
        <Button onClick={() => cur.data && setText(cur.data.defaults.join('\n') + '\n')}>{t('files.protect.defaults')}</Button>
      </div>
    </div>
  )
  return (
    <Modal title={t('files.protect.title', { dir })} onClose={onClose} width={760}>
      {error && <Banner kind="error">{error}</Banner>}
      {!cur.data ? (
        <Spinner />
      ) : (
        <Tabs
          items={[
            { key: 'edit', label: t('configs.editTab'), children: editor },
            {
              key: 'history',
              label: t('configs.versionHistoryTitle'),
              children: <VersionHistory path={cur.data.path} me={me} base="/files" onChanged={() => { setText(null); void cur.reload() }} />,
            },
          ]}
        />
      )}
      {preview !== null && (
        <Modal title={t('blocks.previewTitle')} onClose={() => setPreview(null)} width={760} maskClosable={false}>
          <DiffView text={preview} />
          <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
            <Button type="primary" loading={busy} onClick={() => void save()}>
              {t('virt.applyChanges')}
            </Button>
            <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
          </div>
        </Modal>
      )}
    </Modal>
  )
}

/** Хранилище истории: занятость, лимиты, крупнейшие версии, чистка. */
export function HistoryStorageModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const data = useApi<{ usage: HistoryUsage; biggest: ConfigVersion[] }>('/files/history')
  const [limits, setLimits] = useState<HistoryUsage['limits'] | null>(null)
  const [olderDays, setOlderDays] = useState(14)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<Msg | null>(null)
  const usage = data.data?.usage
  const current = limits ?? usage?.limits

  async function run(body: Record<string, unknown>, confirmText: string) {
    if (!(await confirmAction(confirmText))) return
    setError(null)
    try {
      const res = await api<{ deleted: number }>('/files/history/delete', { method: 'POST', body })
      setNotice(tx('files.history.deleted', { count: res.deleted }))
      void data.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function saveLimits() {
    if (!current) return
    setError(null)
    try {
      const res = await api<{ pruned: number }>('/files/history/settings', { method: 'PUT', body: current })
      setNotice(tx('files.history.saved', { count: res.pruned }))
      setLimits(null)
      void data.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Modal title={t('files.history.title')} onClose={onClose} width="min(96vw, 900px)">
      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="info">{msg(notice)}</Banner>}
      {!usage || !current ? (
        <Spinner />
      ) : (
        <div className="col small" style={{ gap: '0.75rem' }}>
          <div>
            {t('files.history.used', { used: formatBytes(usage.used), limit: formatBytes(usage.limit) })}
            <Progress percent={Math.min(100, usage.percent)} status={usage.percent >= 80 ? 'exception' : 'normal'} size="small" />
            {usage.percent >= 80 && <Banner kind="warn">{t('files.history.nearFull', { percent: usage.percent })}</Banner>}
          </div>
          <div className="row" style={{ gap: '0.75rem', flexWrap: 'wrap', alignItems: 'center' }}>
            <label>
              {t('files.history.perUpload')}{' '}
              <InputNumber size="small" min={0} max={100000} value={current.per_upload_mb} onChange={(v) => setLimits({ ...current, per_upload_mb: v ?? 0 })} /> {t('files.history.mb')}
            </label>
            <label>
              {t('files.history.total')}{' '}
              <InputNumber size="small" min={1} max={1000000} value={current.total_mb} onChange={(v) => setLimits({ ...current, total_mb: v ?? 1 })} /> {t('files.history.mb')}
            </label>
            <label>
              {t('files.history.days')} <InputNumber size="small" min={1} max={3650} value={current.days} onChange={(v) => setLimits({ ...current, days: v ?? 1 })} />
            </label>
            <Button size="small" type="primary" disabled={!limits} onClick={() => void saveLimits()}>
              {t('common.save')}
            </Button>
          </div>
          <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
            {t('files.history.deleteOlder')}
            <Select
              size="small"
              value={olderDays}
              onChange={setOlderDays}
              options={[1, 7, 14, 30, 90].map((d) => ({ value: d, label: t('files.history.daysN', { count: d }) }))}
            />
            <Button
              size="small"
              danger
              onClick={() =>
                void run(
                  { before: new Date(Date.now() - olderDays * 86400_000).toISOString().replace(/\.\d+Z$/, 'Z') },
                  t('files.history.confirmOlder', { count: olderDays }),
                )
              }
            >
              {t('files.history.delete')}
            </Button>
          </div>
          <div className="muted">{t('files.history.biggest')}</div>
          <div className="table-wrap">
            <DataTable<ConfigVersion>
              dataSource={data.data?.biggest ?? []}
              rowKey="id"
              size="small"
              columns={[
                { title: t('files.colName'), key: 'path', render: (_, v) => <span className="mono small">{v.path}</span> },
                { title: t('configs.colTs'), key: 'ts', render: (_, v) => <span className="small nowrap">{formatDateTime(v.ts)}</span> },
                { title: t('configs.colNote'), dataIndex: 'note', key: 'note', className: 'small secondary' },
                { title: t('configs.colSize'), key: 'size', align: 'right', render: (_, v) => <span className="small">{formatBytes(v.size)}</span> },
                {
                  title: '',
                  key: 'actions',
                  render: (_, v) => (
                    <div className="row row-nowrap">
                      <RowAction action="delete" danger label={t('files.history.deleteVersion')} onClick={() => void run({ version_ids: [v.id] }, t('files.history.confirmVersion', { id: v.id }))} />
                      <RowAction action="history" label={t('files.history.deletePath')} onClick={() => void run({ path: v.path }, t('files.history.confirmPath', { path: v.path }))} />
                    </div>
                  ),
                },
              ]}
            />
          </div>
        </div>
      )}
    </Modal>
  )
}

/** История одного файла (любого — и двоичного): версии, дифф, откат. */
export function FileHistoryModal({ path, me, onClose, onChanged }: { path: string; me: Me; onClose: () => void; onChanged: () => void }) {
  const { t } = useTranslation()
  return (
    <Modal title={t('files.historyOf', { path })} onClose={onClose} width="min(96vw, 1000px)">
      <VersionHistory path={path} me={me} base="/files" onChanged={onChanged} />
    </Modal>
  )
}
