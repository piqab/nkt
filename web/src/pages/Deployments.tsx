import { useEffect, useState } from 'react'
import { Button, Checkbox, Dropdown, Input, Select, Space, Switch, Tabs, Tag, Tooltip } from 'antd'
import { CopyOutlined, DownOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost, Job, Me } from '../types'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { EditTextModal } from '../components/EditTextModal'
import { confirmAction, confirmWithOption } from '../components/confirm'
import { unifiedDiff } from '../components/textDiff'
import { JobLogModal } from './Jobs'
import { EdgeCard } from '../components/EdgeCard'
import { SitesPanel } from '../components/SitesPanel'
import { ComposeEngineStatus } from '../components/ComposeEngineStatus'
import { HelpButton, TitleHelp } from '../components/Docs'
import { PIPELINE_EXAMPLES, type PipelineExample } from '../pipelineExamples'

interface Deployment {
  id: number
  ref?: string
  commit?: string
  tag?: string
  trigger: string
  author?: string
  job_id?: number
  env_version?: number
  status: string
  error?: string
  created_at: string
  finished_at?: string
}
interface Pipeline {
  id: number
  name: string
  content?: string
  hook_id: string
  enabled: boolean
  last_commit?: string
  last_tag?: string
  failed_commit?: string
  failed_tag?: string
  has_git_cred: boolean
  has_registry_cred: boolean
  has_env?: boolean
  updated_at: string
  last?: Deployment
  action?: string
  /** JSON: что удалять и ошибка, если удаление с хостов не завершилось. */
  removal?: string
  /** JSON: проверки сухого прогона, с которых сняли галочки. */
  dry_skip?: string
}
interface PipelineVersion {
  id: number
  ts: string
  author?: string
  note?: string
  content?: string
}
interface EnvVersion {
  id: number
  ts: string
  author?: string
  note?: string
  cleared?: boolean
  names?: string[]
  added?: string[]
  removed?: string[]
  changed?: string[]
  current?: boolean
}
interface EdgeStatus {
  configured: boolean
  domain?: string
}

const STATUS_COLOR: Record<string, string> = { succeeded: 'success', failed: 'error', running: 'processing', queued: 'default' }
const short = (sha?: string) => (sha ? sha.slice(0, 10) : '')
const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/**
 * Выкладки хаба: конвейер берёт репозиторий Git и выкладывает его
 * манифестом, Helm-релизом или сценарием — по кнопке, вебхуку, опросу
 * репозитория или новому тегу образа в registry. Сборку образов делает
 * внешний CI; здесь — вторая половина: «готово → выложить».
 */
export default function Deployments({ me }: { me: Me }) {
  const { t } = useTranslation()
  const list = useApi<{ pipelines: Pipeline[] }>('/hub/pipelines', 15_000)
  const [edit, setEdit] = useState<{ pipeline?: Pipeline } | null>(null)
  const [dialog, setDialog] = useState<{ type: 'deploy' | 'access' | 'hook' | 'history'; p: Pipeline } | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [remove, setRemove] = useState<Pipeline | null>(null)

  async function openJob(id?: number) {
    if (!id) return
    try {
      setJob(await api<Job>(`/hosts/local/jobs/${id}`))
    } catch {
      // задание откроется из «Заданий»
    }
  }

  const columns = [
    {
      title: t('deploy.name'),
      key: 'name',
      render: (_: unknown, p: Pipeline) => (
        <Space size={4}>
          <strong>{p.name}</strong>
          {!p.enabled && (
            <Tooltip title={t('deploy.enabledHint')}>
              <Tag>{t('deploy.manualOnly')}</Tag>
            </Tooltip>
          )}
          {removalOf(p)?.error ? (
            <Tooltip title={removalOf(p)?.error}>
              <Tag color="error">{t('deploy.removeUnfinished')}</Tag>
            </Tooltip>
          ) : removalOf(p) ? (
            <Tag color="processing">{t('deploy.removing')}</Tag>
          ) : null}
        </Space>
      ),
    },
    {
      title: t('deploy.last'),
      key: 'last',
      render: (_: unknown, p: Pipeline) =>
        p.last ? (
          <Space size={4} wrap>
            <Tag color={STATUS_COLOR[p.last.status]}>{t(`deploy.status.${p.last.status}`)}</Tag>
            <span className="mono small">{p.last.tag || short(p.last.commit) || p.last.ref}</span>
            <span className="small muted">
              {t(`deploy.trigger.${p.last.trigger}`)} · {formatRelative(p.last.created_at)}
            </span>
            {p.last.job_id ? (
              <Button size="small" type="link" onClick={() => void openJob(p.last?.job_id)}>
                {t('deploy.log')}
              </Button>
            ) : null}
            {p.failed_commit && p.last.status === 'failed' && (
              <Tooltip title={t('deploy.waitsNewHint', { what: [p.failed_tag, short(p.failed_commit)].filter(Boolean).join(' ') })}>
                <Tag>{t('deploy.waitsNew')}</Tag>
              </Tooltip>
            )}
          </Space>
        ) : (
          <span className="small muted">{t('deploy.never')}</span>
        ),
    },
    {
      title: (
        <Tooltip title={t('deploy.enabledHint')}>
          <span>{t('deploy.enabled')}</span>
        </Tooltip>
      ),
      key: 'enabled',
      render: (_: unknown, p: Pipeline) => (
        <Switch
          title={t('deploy.enabledHint')}
          size="small"
          checked={p.enabled}
          onChange={async (v) => {
            try {
              await api(`/hub/pipelines/${p.id}/enabled`, { method: 'POST', body: { enabled: v } })
              void list.reload()
            } catch (err) {
              setError(errText(err))
            }
          }}
        />
      ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, p: Pipeline) => (
        <Space size={4} wrap>
          <Button size="small" type="primary" onClick={() => setDialog({ type: 'deploy', p })}>
            {t('deploy.deployNow')}
          </Button>
          <Button size="small" onClick={() => setDialog({ type: 'history', p })}>
            {t('deploy.history')}
          </Button>
          <Button size="small" onClick={() => void api<Pipeline>(`/hub/pipelines/${p.id}`).then((full) => setEdit({ pipeline: full }))}>
            {t('deploy.description')}
          </Button>
          <Button size="small" onClick={() => setDialog({ type: 'access', p })}>
            {t('deploy.access')}
          </Button>
          <Button size="small" onClick={() => setDialog({ type: 'hook', p })}>
            {t('deploy.hook')}
          </Button>
          {p.action === 'compose' ? (
            <Button size="small" danger onClick={() => setRemove(p)}>
              {removalOf(p) ? t('deploy.deleteRetry') : t('deploy.delete')}
            </Button>
          ) : (
            <Button
              size="small"
              danger
              onClick={async () => {
                if (!(await confirmAction(t('deploy.deleteConfirm', { name: p.name })))) return
                try {
                  await api(`/hub/pipelines/${p.id}`, { method: 'DELETE' })
                  void list.reload()
                } catch (err) {
                  setError(errText(err))
                }
              }}
            >
              {t('deploy.delete')}
            </Button>
          )}
        </Space>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('deploy.title')}
          <TitleHelp>{t('deploy.pageHint')}</TitleHelp>
        </h1>
        {me.is_admin && (
          <Button type="primary" onClick={() => setEdit({})}>
            {t('deploy.new')}
          </Button>
        )}
      </div>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      <Tabs
        items={[
          {
            key: 'pipelines',
            label: t('deploy.tabPipelines'),
            children: (
              <>
                <HubGitBanner admin={me.is_admin} />
                <Card title={t('deploy.pipelines')} subtitle={t('deploy.subtitle')}>
                  <ErrorNote error={list.error} />
                  {list.loading && !list.data ? (
                    <Loading what={t('deploy.pipelines')} />
                  ) : (list.data?.pipelines ?? []).length === 0 ? (
                    <p className="small muted">{t('deploy.empty')}</p>
                  ) : (
                    <div className="table-wrap">
                      <DataTable<Pipeline> dataSource={list.data?.pipelines ?? []} rowKey={(p) => String(p.id)} size="small" columns={columns} />
                    </div>
                  )}
                </Card>
                {me.is_admin && <EdgeCard onOpenJob={(id) => void openJob(id)} />}
              </>
            ),
          },
          { key: 'sites', label: t('deploy.tabSites'), children: <SitesPanel me={me} /> },
        ]}
      />
      {edit && <PipelineEditor pipeline={edit.pipeline} onClose={() => setEdit(null)} onSaved={() => void list.reload()} />}
      {dialog?.type === 'deploy' && (
        <DeployModal
          p={dialog.p}
          onClose={() => setDialog(null)}
          onStarted={(jobID) => {
            setDialog(null)
            void list.reload()
            void openJob(jobID)
          }}
        />
      )}
      {dialog?.type === 'history' && <HistoryModal p={dialog.p} onClose={() => setDialog(null)} onOpenJob={(id) => void openJob(id)} onChanged={() => void list.reload()} />}
      {dialog?.type === 'access' && <AccessModal p={dialog.p} onClose={() => setDialog(null)} onSaved={() => void list.reload()} />}
      {dialog?.type === 'hook' && <HookModal p={dialog.p} onClose={() => setDialog(null)} />}
      {remove && (
        <RemoveModal
          p={remove}
          onClose={() => setRemove(null)}
          onStarted={(jobID) => {
            setRemove(null)
            void list.reload()
            void openJob(jobID)
          }}
        />
      )}
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} onDone={() => void list.reload()} />}
    </>
  )
}

/** Состояние удаления конвейера с хостов (pipelines.removal). */
interface Removal {
  volumes?: boolean
  images?: boolean
  cert?: boolean
  job_id?: number
  error?: string
  running?: boolean
}
function removalOf(p: Pipeline): Removal | null {
  if (!p.removal) return null
  try {
    const r = JSON.parse(p.removal) as Removal
    return { ...r, running: !r.error }
  } catch {
    return null
  }
}

/** Удаление конвейера compose: задание хаба — сайт, стек на хостах
 * (compose down, каталог стека), запись. Тома, образы и сертификат — по
 * галочкам, по умолчанию нет. Повтор — с прежними галочками. */
function RemoveModal({ p, onClose, onStarted }: { p: Pipeline; onClose: () => void; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const prev = removalOf(p)
  const [volumes, setVolumes] = useState(prev?.volumes ?? false)
  const [images, setImages] = useState(prev?.images ?? false)
  const [cert, setCert] = useState(prev?.cert ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <Modal title={t('deploy.removeTitle', { name: p.name })} onClose={onClose} width={640}>
      <p className="small">{t('deploy.removeHint')}</p>
      {prev?.error && <Banner kind="error">{t('deploy.removeUnfinishedText', { error: prev.error })}</Banner>}
      {error && <Banner kind="error">{error}</Banner>}
      <div className="col" style={{ gap: '0.4rem', margin: '0.5rem 0' }}>
        <Checkbox checked={volumes} onChange={(e) => setVolumes(e.target.checked)}>
          <span style={{ color: volumes ? 'var(--status-error)' : undefined }}>{t('deploy.removeVolumes')}</span>
          <div className="small muted">{t('deploy.removeVolumesHint')}</div>
        </Checkbox>
        <Checkbox checked={images} onChange={(e) => setImages(e.target.checked)}>
          {t('deploy.removeImages')}
        </Checkbox>
        <Checkbox checked={cert} onChange={(e) => setCert(e.target.checked)}>
          {t('deploy.removeCert')}
        </Checkbox>
      </div>
      <Space>
        <Button
          danger
          type="primary"
          loading={busy}
          onClick={async () => {
            setBusy(true)
            setError(null)
            try {
              const r = await api<{ job_id: number }>(`/hub/pipelines/${p.id}/remove`, { method: 'POST', body: { volumes, images, cert } })
              onStarted(r.job_id)
            } catch (err) {
              setError(errText(err))
            } finally {
              setBusy(false)
            }
          }}
        >
          {prev?.error ? t('deploy.deleteRetry') : t('deploy.removeStart')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </Space>
    </Modal>
  )
}

/** На хабе нет git — выкладки и сухой прогон не работают: плашка и
 * установка пакета git на машине хаба (фоновое задание, стандартное окно). */
function HubGitBanner({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const git = useApi<{ installed: boolean; version?: string; installable: boolean }>('/hub/deploy/git')
  const [job, setJob] = useState<Job | null>(null)
  const [error, setError] = useState<string | null>(null)
  async function install() {
    setError(null)
    try {
      const res = await api<{ job_id?: number }>('/hosts/local/system/apt/packages/git/install/ws?job=1', { method: 'POST' })
      if (typeof res.job_id === 'number') setJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
      else void git.reload()
    } catch (err) {
      setError(errText(err))
    }
  }
  return (
    <>
      {git.data && !git.data.installed && (
        <Banner kind="error">
          <Space wrap>
            <span>{t('deploy.noGit')}</span>
            {admin && git.data.installable ? (
              <Button size="small" type="primary" onClick={() => void install()}>
                {t('deploy.installGit')}
              </Button>
            ) : (
              <span className="small">{t('deploy.noGitManual')}</span>
            )}
            {error && <span className="small">{error}</span>}
          </Space>
        </Banner>
      )}
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} onDone={() => void git.reload()} />}
    </>
  )
}

/** Описание конвейера (YAML): правка с диффом, история редакций. */
function PipelineEditor({ pipeline: initial, onClose, onSaved }: { pipeline?: Pipeline; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  // Новый конвейер, сохранённый кнопкой «Доступ», — дальше окно правит его.
  const [created, setCreated] = useState<Pipeline | null>(null)
  const pipeline = initial ?? created ?? undefined
  const [access, setAccess] = useState<Pipeline | null>(null)
  const tpl = useApi<{ content: string }>(pipeline ? null : '/hub/pipelines/template')
  const [name, setName] = useState('')
  const [note, setNote] = useState('')
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const saved = pipeline?.content ?? ''
  const text = draft ?? (pipeline ? saved : (tpl.data?.content ?? ''))
  const [dryJob, setDryJob] = useState<Job | null>(null)
  const [dryOpen, setDryOpen] = useState(false)
  const [drySkip, setDrySkip] = useState(() => parseDrySkip(pipeline?.dry_skip))

  // «Доступ»: новый конвейер сначала сохраняется (нужно имя), затем —
  // то же окно, что в списке, с проверкой доступа.
  async function openAccess() {
    setError(null)
    try {
      let id = pipeline?.id
      if (!id) {
        setBusy(true)
        const res = await api<{ id: number }>('/hub/pipelines', { method: 'POST', body: { name, content: text } })
        id = res.id
        onSaved()
      }
      const fresh = await api<Pipeline>(`/hub/pipelines/${id}`)
      if (!initial) {
        setCreated(fresh)
        setDraft(null)
      }
      setAccess(fresh)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  async function save(): Promise<boolean> {
    setBusy(true)
    setError(null)
    try {
      if (pipeline) await api(`/hub/pipelines/${pipeline.id}`, { method: 'PUT', body: { content: text, note } })
      else await api('/hub/pipelines', { method: 'POST', body: { name, content: text } })
      onSaved()
      return true
    } catch (err) {
      setError(errText(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <EditTextModal
        title={pipeline ? t('deploy.editTitle', { name: pipeline.name }) : t('deploy.newTitle')}
        saved={saved}
        draft={text}
        onDraft={setDraft}
        busy={busy}
        onSave={save}
        onClose={onClose}
        onHistory={pipeline ? () => setHistory(true) : undefined}
        fields={
          <>
            <Space size={4} wrap style={{ marginBottom: '0.3rem' }}>
              <span className="small muted">{t('deploy.editHint')}</span>
              <HelpButton docKey="deploy:compose" isHub admin label={t('docs.labelCompose')} />
              <HelpButton docKey="deploy:site" isHub admin label={t('docs.labelSite')} />
            </Space>
            {!initial && (
              <ComposeFromLink
                pipelineId={pipeline?.id}
                onFill={(yaml, suggested) => {
                  setDraft(yaml)
                  if (!name) setName(suggested)
                }}
                onName={(n) => {
                  if (!name) setName(n)
                }}
              />
            )}
            <Space wrap style={{ marginBottom: '0.5rem' }}>
              {!pipeline && <Input size="small" style={{ width: '16rem' }} value={name} placeholder={t('deploy.name')} onChange={(e) => setName(e.target.value)} />}
              {pipeline && <Input size="small" style={{ width: '20rem' }} value={note} placeholder={t('deploy.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />}
              <Tooltip title={!pipeline && !name.trim() ? t('deploy.accessNeedsName') : t('deploy.accessFromEditor')}>
                <Button size="small" disabled={busy || (!pipeline && !name.trim())} onClick={() => void openAccess()}>
                  {t('deploy.access')}
                </Button>
              </Tooltip>
              {/action:\s*compose/.test(text) && (
                <Tooltip title={t('deploy.dryRunHint')}>
                  <Button size="small" onClick={() => setDryOpen(true)}>
                    {t('deploy.dryRun')}
                  </Button>
                </Tooltip>
              )}
            </Space>
          </>
        }
        below={error ? <Banner kind="error" onClose={() => setError(null)}>{error}</Banner> : null}
      />
      {dryOpen && (
        <DryRunModal
          name={pipeline?.name ?? name}
          skip={drySkip}
          saved={!!pipeline}
          onClose={() => setDryOpen(false)}
          onRun={async (skip) => {
            // Сухой прогон текущего текста — и несохранённого: доступ и
            // .env — сохранённого конвейера.
            const res = await api<{ job_id: number }>('/hub/pipelines/dryrun', { method: 'POST', body: { pipeline_id: pipeline?.id ?? 0, content: text, skip } })
            setDrySkip(skip)
            setDryOpen(false)
            setDryJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
          }}
        />
      )}
      {access && <AccessModal p={access} onClose={() => setAccess(null)} onSaved={onSaved} />}
      {dryJob && <JobLogModal job={dryJob} scope="/hosts/local" onClose={() => setDryJob(null)} />}
      {history && pipeline && (
        <VersionsModal
          p={pipeline}
          onClose={() => setHistory(false)}
          onLoad={(content) => {
            setDraft(content)
            setHistory(false)
          }}
        />
      )}
    </>
  )
}

/** Ссылка на compose-файл в веб-интерфейсе Git → репозиторий, ветка,
 * путь. GitHub (blob/raw), GitLab (-/blob, -/raw), Codeberg/Gitea/Forgejo
 * (src/branch, raw/branch, src/tag). Ветка со слешем в имени по ссылке
 * неотличима от каталога — берётся первый сегмент, поправить можно в
 * описании. */
export function parseComposeLink(link: string): { repo: string; ref: string; file: string; name: string } | null {
  let u: URL
  try {
    u = new URL(link.trim())
  } catch {
    return null
  }
  const parts = u.pathname.split('/').filter(Boolean)
  const make = (owner: string[], ref: string, rest: string[], host = u.host) => {
    const repoName = owner[owner.length - 1].replace(/\.git$/, '')
    return { repo: `https://${host}/${owner.join('/')}.git`.replace(/\.git\.git$/, '.git'), ref, file: rest.join('/'), name: repoName }
  }
  if (u.host === 'raw.githubusercontent.com' && parts.length >= 4) {
    return make(parts.slice(0, 2), parts[2], parts.slice(3), 'github.com')
  }
  const dash = parts.indexOf('-')
  if (dash > 0 && (parts[dash + 1] === 'blob' || parts[dash + 1] === 'raw') && parts.length > dash + 3) {
    return make(parts.slice(0, dash), parts[dash + 2], parts.slice(dash + 3))
  }
  const blob = parts.findIndex((p, i) => i >= 2 && (p === 'blob' || p === 'raw'))
  if (u.host === 'github.com' && blob === 2 && parts.length > 4) {
    return make(parts.slice(0, 2), parts[3], parts.slice(4))
  }
  const src = parts.findIndex((p, i) => i >= 2 && (p === 'src' || p === 'raw'))
  if (src === 2 && ['branch', 'tag', 'commit'].includes(parts[3]) && parts.length > 5) {
    return make(parts.slice(0, 2), parts[4], parts.slice(5))
  }
  return null
}

/** «Compose по ссылке»: ссылка на compose-файл, хосты, имя стека →
 * описание конвейера action: compose (дальше — обычная правка с диффом). */
function ComposeFromLink({ pipelineId, onFill, onName }: { pipelineId?: number; onFill: (yaml: string, name: string) => void; onName: (name: string) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [link, setLink] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [project, setProject] = useState('')
  const [bad, setBad] = useState(false)
  // Выбранный пример: и до хостов (описание заполнится по «Заполнить
  // описание»), и после — повторное заполнение тоже по примеру, пока
  // ссылку не сменили.
  const [pending, setPending] = useState<PipelineExample | null>(null)
  // Разбор самого файла (своя ссылка): кэш по ссылке и выбор сервиса сайта.
  const [scan, setScan] = useState<{ link: string; scan: ComposeScan } | null>(null)
  const [scanNote, setScanNote] = useState<string | null>(null)
  const [scanning, setScanning] = useState(false)
  const [siteChoice, setSiteChoice] = useState<string>('auto')
  async function fill(link: string, project: string, ex?: PipelineExample, choice = siteChoice) {
    const p = parseComposeLink(link)
    if (!p) {
      setBad(true)
      return
    }
    setBad(false)
    const proj = (project || p.name).toLowerCase().replace(/[^a-z0-9_-]/g, '-').replace(/^[-_]+/, '').slice(0, 63) || 'app'
    if (!ex) {
      let sc = scan?.link === link ? scan.scan : null
      if (!sc) {
        setScanning(true)
        setScanNote(null)
        try {
          const res = await api<{ scan?: ComposeScan; error?: string; closed?: boolean; has_access?: boolean }>('/hub/pipelines/scan', {
            method: 'POST',
            body: { repo: p.repo, ref: p.ref, file: p.file, pipeline_id: pipelineId ?? 0 },
          })
          if (res.scan) {
            sc = res.scan
            setScan({ link, scan: res.scan })
          } else {
            setScanNote(res.closed && !res.has_access ? t('deploy.scanClosed') : t('deploy.scanFailed', { error: res.error ?? '' }))
          }
        } catch (err) {
          setScanNote(t('deploy.scanFailed', { error: errText(err) }))
        } finally {
          setScanning(false)
        }
      }
      if (sc) {
        onFill(scannedYaml(sc, p, proj, picked, choice, t), proj)
        return
      }
    }
    // Сервисов чужого compose хаб не знает — заглушка явная.
    const site = ex?.site ?? { service: t('deploy.fromLinkSiteService'), port: t('deploy.fromLinkSitePort') }
    const block = (name: string, rec: Record<string, string | string[]> | undefined, comment: string) =>
      rec
        ? `  ${name}:${comment ? '                          '.slice(name.length) + '# ' + comment : ''}\n` +
          Object.entries(rec)
            .map(([svc, v]) => `    ${svc}: ${Array.isArray(v) ? `[${v.map((x) => (x.includes(':') ? `"${x}"` : x)).join(', ')}]` : v}\n`)
            .join('')
        : ''
    const files = ex?.files ? `  files: [${ex.files.join(', ')}]              # ${t('deploy.fromLinkFilesComment')}\n` : ''
    const envKeys = ex?.envKeys
      ? `  env_keys:                        # ${t('deploy.envKeysComment')}\n` +
        Object.entries(ex.envKeys)
          .map(([svc, keys]) => `    ${svc}: [${keys.join(', ')}]\n`)
          .join('')
      : ''
    const envTemplate = ex?.envTemplate
      ? `\n# ${t('deploy.envTemplateComment')}\n` + ex.envTemplate.map((l) => `#   ${l}\n`).join('')
      : ''
    const yaml =
      (ex ? `# ${t('deploy.exampleComment')}: ${ex.readme}\n` : '') +
      `repo: ${p.repo}\nref: ${p.ref}\n\naction: compose\ncompose:\n  file: ${p.file}\n  project: ${proj}\n` +
      `  hosts: [${picked.join(', ')}]\n` +
      (ex ? '' : `  # files: [${p.file.includes('/') ? p.file.slice(0, p.file.lastIndexOf('/') + 1) : ''}nginx.conf]  # ${t('deploy.fromLinkFilesComment')}\n`) +
      files +
      `  wait_timeout: ${ex?.waitTimeout ?? '5m'}\n` +
      block('images', ex?.images, t('deploy.imagesComment')) +
      block('ports', ex?.ports, t('deploy.portsComment')) +
      envKeys +
      `  # site:                          # ${t('deploy.fromLinkSiteComment')}\n` +
      `  #   domains: [${ex ? ex.project : proj}.example.com]\n` +
      `  #   service: ${site.service}\n` +
      `  #   port: ${site.port}\n` +
      `\n# poll: 5m   # ${t('deploy.fromLinkPollComment')}\n` +
      envTemplate
    onFill(yaml, proj)
  }
  function example(ex: PipelineExample) {
    setLink(ex.link)
    setProject(ex.project)
    onName(ex.project)
    setPending(ex)
    if (picked.length > 0) fill(ex.link, ex.project, ex)
  }
  return (
    <div className="col" style={{ gap: '0.3rem', marginBottom: '0.6rem', padding: '0.5rem', border: '1px solid var(--border)', borderRadius: 6 }}>
      <strong className="small">{t('deploy.fromLink')}</strong>
      <span className="small muted">{t('deploy.fromLinkHint')}</span>
      <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
        <Input size="small" style={{ flex: 1, minWidth: 280 }} value={link} onChange={(e) => setLink(e.target.value)} placeholder="https://github.com/org/app/blob/main/deploy/docker-compose.yml" className="mono" />
        <Select
          size="small"
          mode="multiple"
          style={{ minWidth: 200 }}
          placeholder={t('deploy.fromLinkHosts')}
          value={picked}
          onChange={setPicked}
          options={(hosts.data ?? []).map((h) => ({ value: h.name, label: h.name }))}
        />
        <Input size="small" style={{ width: 140 }} value={project} onChange={(e) => setProject(e.target.value)} placeholder={t('deploy.fromLinkProject')} />
        <Button size="small" loading={scanning} disabled={!link || picked.length === 0} onClick={() => void fill(link, project, pending?.link === link ? pending : undefined)}>
          {t('deploy.fromLinkFill')}
        </Button>
        {scan?.link === link && pending?.link !== link && (
          <Tooltip title={t('deploy.siteChoiceHint')}>
            <Select
              size="small"
              style={{ minWidth: 190 }}
              value={siteChoice}
              onChange={(v: string) => {
                setSiteChoice(v)
                void fill(link, project, undefined, v)
              }}
              options={[
                { value: 'auto', label: scan.scan.web ? t('deploy.siteAuto', { name: scan.scan.web }) : t('deploy.siteAutoNone') },
                ...scan.scan.services.filter((s) => !s.build_only).map((s) => ({ value: s.name, label: `${t('deploy.siteService')}: ${s.name}` })),
                { value: 'none', label: t('deploy.siteNone') },
              ]}
            />
          </Tooltip>
        )}
        <Dropdown
          trigger={['click']}
          menu={{
            items: PIPELINE_EXAMPLES.map((ex) => ({
              key: ex.key,
              label: (
                <div style={{ maxWidth: 360 }}>
                  <strong>{t(`deploy.examples.${ex.key}.title`)}</strong>
                  <div className="small muted" style={{ whiteSpace: 'normal' }}>
                    {t(`deploy.examples.${ex.key}.hint`)}
                  </div>
                </div>
              ),
            })),
            onClick: ({ key }) => {
              const ex = PIPELINE_EXAMPLES.find((e) => e.key === key)
              if (ex) example(ex)
            },
          }}
        >
          <Button size="small" type="dashed">
            {t('deploy.examplesButton')} <DownOutlined />
          </Button>
        </Dropdown>
      </div>
      {picked.length > 0 && (
        <div className="row" style={{ gap: '0.9rem', flexWrap: 'wrap' }}>
          {picked.map((n) => {
            const h = (hosts.data ?? []).find((x) => x.name === n)
            return h ? <ComposeEngineStatus key={n} hostId={h.id} name={n} admin /> : null
          })}
        </div>
      )}
      {bad && <span className="small" style={{ color: 'var(--status-error)' }}>{t('deploy.fromLinkBad')}</span>}
      {scanNote && <span className="small" style={{ color: 'var(--status-warning)', fontWeight: 600 }}>{scanNote}</span>}
      {scan?.link === link && <span className="small muted">{t('deploy.scanDone', { services: scan.scan.services.length, vars: scan.scan.vars.length })}</span>}
    </div>
  )
}

function VersionsModal({ p, onClose, onLoad }: { p: Pipeline; onClose: () => void; onLoad: (content: string) => void }) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: PipelineVersion[] }>(`/hub/pipelines/${p.id}/versions`)
  const [diff, setDiff] = useState<{ id: number; text: string } | null>(null)
  const full = (id: number) => api<PipelineVersion>(`/hub/pipelines/versions/${id}`)
  return (
    <Modal title={t('deploy.versionsTitle', { name: p.name })} onClose={onClose} width={900}>
      {!versions.data ? (
        <Loading what={t('deploy.history')} />
      ) : (
        versions.data.versions.map((v, i) => (
          <div key={v.id} style={{ borderBottom: '1px solid var(--border)', padding: '0.4rem 0' }}>
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <strong className="small">#{v.id}</strong>
              <span className="small">{formatRelative(v.ts)}</span>
              <span className="small">{v.author}</span>
              <span className="small muted">{v.note}</span>
              <span style={{ flex: 1 }} />
              {i > 0 && (
                <Button
                  size="small"
                  onClick={async () => {
                    if (diff?.id === v.id) return setDiff(null)
                    const old = await full(v.id)
                    setDiff({ id: v.id, text: unifiedDiff(old.content ?? '', p.content ?? '', `#${v.id}`, t('manifests.current')) })
                  }}
                >
                  {t('manifests.diffWithCurrent')}
                </Button>
              )}
              <Button size="small" onClick={async () => onLoad((await full(v.id)).content ?? '')}>
                {t('manifests.loadVersion')}
              </Button>
            </div>
            {diff?.id === v.id && <DiffView text={diff.text} />}
          </div>
        ))
      )}
    </Modal>
  )
}

function DeployModal({ p, onClose, onStarted }: { p: Pipeline; onClose: () => void; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const [ref, setRef] = useState('')
  const [tag, setTag] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [dryOpen, setDryOpen] = useState(false)
  return (
    <Modal title={t('deploy.deployTitle', { name: p.name })} onClose={onClose} width={560}>
      <p className="small muted">{t('deploy.deployHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <Space wrap>
        <Input size="small" style={{ width: '12rem' }} value={ref} placeholder={t('deploy.refPlaceholder')} onChange={(e) => setRef(e.target.value.trim())} />
        <Input size="small" style={{ width: '12rem' }} value={tag} placeholder={t('deploy.tagPlaceholder')} onChange={(e) => setTag(e.target.value.trim())} />
        <Button
          type="primary"
          loading={busy}
          onClick={async () => {
            setBusy(true)
            setError(null)
            try {
              const res = await api<{ job_id: number }>(`/hub/pipelines/${p.id}/deploy`, { method: 'POST', body: { ref, tag } })
              onStarted(res.job_id)
            } catch (err) {
              setError(errText(err))
            } finally {
              setBusy(false)
            }
          }}
        >
          {t('deploy.deployNow')}
        </Button>
        <Tooltip title={t('deploy.dryRunHint')}>
          <Button disabled={busy} onClick={() => setDryOpen(true)}>
            {t('deploy.dryRun')}
          </Button>
        </Tooltip>
      </Space>
      {dryOpen && (
        <DryRunModal
          name={p.name}
          skip={parseDrySkip(p.dry_skip)}
          saved
          onClose={() => setDryOpen(false)}
          onRun={async (skip) => {
            const res = await api<{ job_id: number }>('/hub/pipelines/dryrun', { method: 'POST', body: { pipeline_id: p.id, ref, tag, skip } })
            onStarted(res.job_id)
          }}
        />
      )}
    </Modal>
  )
}

/** Проверки сухого прогона (ключи — как на хабе, DryChecks). */
const DRY_CHECKS = ['engine', 'config', 'images', 'ports', 'resources', 'health', 'stack', 'site_dns', 'site_outside', 'site_host', 'site_cert', 'version']

function parseDrySkip(raw?: string): string[] {
  try {
    const v: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(v) ? v.filter((k): k is string => DRY_CHECKS.includes(k as string)) : []
  } catch {
    return []
  }
}

/** Сухой прогон: какие проверки делать. Хранятся снятые галочки — новая
 *  проверка в следующей версии будет включена сама. */
function DryRunModal({ name, skip, saved, onClose, onRun }: { name: string; skip: string[]; saved: boolean; onClose: () => void; onRun: (skip: string[]) => Promise<void> }) {
  const { t } = useTranslation()
  const [off, setOff] = useState<string[]>(skip)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <Modal title={t('deploy.dryTitle', { name })} onClose={onClose} width={560}>
      <p className="small muted">{t('deploy.dryChecksHint')}</p>
      {!saved && <p className="small muted">{t('deploy.dryUnsaved')}</p>}
      <Space size={4} style={{ marginBottom: '0.5rem' }}>
        <Button
          type="primary"
          loading={busy}
          onClick={async () => {
            setBusy(true)
            setError(null)
            try {
              await onRun(DRY_CHECKS.filter((k) => off.includes(k)))
            } catch (err) {
              setError(errText(err))
            } finally {
              setBusy(false)
            }
          }}
        >
          {t('deploy.dryRunStart')}
        </Button>
        <Button size="small" type="link" onClick={() => setOff([])}>
          {t('deploy.dryAll')}
        </Button>
        <Button size="small" type="link" onClick={() => setOff(DRY_CHECKS)}>
          {t('deploy.dryNone')}
        </Button>
      </Space>
      {error && <Banner kind="error">{error}</Banner>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        {DRY_CHECKS.map((k) => (
          <Checkbox key={k} checked={!off.includes(k)} onChange={(e) => setOff(e.target.checked ? off.filter((x) => x !== k) : [...off, k])}>
            {t(`deploy.dryCheck.${k}`)} <span className="small muted">— {t(`deploy.dryCheck.${k}Hint`)}</span>
          </Checkbox>
        ))}
      </div>
    </Modal>
  )
}

function HistoryModal({ p, onClose, onOpenJob, onChanged }: { p: Pipeline; onClose: () => void; onOpenJob: (id: number) => void; onChanged: () => void }) {
  const { t } = useTranslation()
  const res = useApi<{ deployments: Deployment[] }>(`/hub/pipelines/${p.id}/deployments`, 10_000)
  const [error, setError] = useState<string | null>(null)
  const columns = [
    { title: '#', key: 'id', render: (_: unknown, d: Deployment) => <span className="small">{d.id}</span> },
    { title: t('deploy.status.title'), key: 'status', render: (_: unknown, d: Deployment) => <Tooltip title={d.error}><Tag color={STATUS_COLOR[d.status]}>{t(`deploy.status.${d.status}`)}</Tag></Tooltip> },
    { title: t('deploy.what'), key: 'what', render: (_: unknown, d: Deployment) => <span className="mono small">{[d.tag, short(d.commit), d.ref].filter(Boolean).join(' · ')}</span> },
    { title: t('deploy.triggerTitle'), key: 'trigger', render: (_: unknown, d: Deployment) => <span className="small">{t(`deploy.trigger.${d.trigger}`)}{d.author ? ` · ${d.author}` : ''}</span> },
    { title: t('deploy.when'), key: 'when', render: (_: unknown, d: Deployment) => <span className="small nowrap">{formatRelative(d.created_at)}</span> },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, d: Deployment) => (
        <Space size={4}>
          {d.job_id ? (
            <Button size="small" type="link" onClick={() => onOpenJob(d.job_id!)}>
              {t('deploy.log')}
            </Button>
          ) : null}
          {d.status === 'succeeded' && d.commit && d.commit !== p.last_commit && (
            <Button
              size="small"
              danger
              onClick={async () => {
                const text = t('deploy.rollbackConfirm', { what: d.tag || short(d.commit) })
                let withEnv = false
                if (d.env_version) {
                  const ok = await confirmWithOption(text, t('deploy.rollbackWithEnv', { version: d.env_version }), { optionHint: t('deploy.rollbackWithEnvHint') })
                  if (!ok) return
                  withEnv = ok.checked
                } else if (!(await confirmAction(text))) return
                try {
                  const r = await api<{ job_id: number }>(`/hub/pipelines/${p.id}/rollback`, { method: 'POST', body: { deployment_id: d.id, with_env: withEnv } })
                  onChanged()
                  void res.reload()
                  onOpenJob(r.job_id)
                } catch (err) {
                  setError(errText(err))
                }
              }}
            >
              {t('deploy.rollback')}
            </Button>
          )}
        </Space>
      ),
    },
  ]
  return (
    <Modal title={t('deploy.historyTitle', { name: p.name })} onClose={onClose} width={1000}>
      {error && <Banner kind="error">{error}</Banner>}
      {!res.data ? <Loading what={t('deploy.history')} /> : <DataTable<Deployment> dataSource={res.data.deployments} rowKey={(d) => String(d.id)} size="small" columns={columns} />}
    </Modal>
  )
}

/** Доступ к репозиторию и registry — значения не показываются, только
 * задаются или убираются. */
function AccessModal({ p, onClose, onSaved }: { p: Pipeline; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [token, setToken] = useState('')
  const [key, setKey] = useState('')
  const [registry, setRegistry] = useState('')
  const [env, setEnv] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [envHistory, setEnvHistory] = useState(false)
  // Проверка доступа с сохранёнными ключами: при открытии и после записи.
  const [check, setCheck] = useState<AccessCheck | null>(null)
  const [checking, setChecking] = useState(false)
  const [saved, setSaved] = useState(false)
  async function runCheck() {
    setChecking(true)
    try {
      setCheck(await api<AccessCheck>(`/hub/pipelines/${p.id}/access/check`, { method: 'POST' }))
    } catch (err) {
      setCheck({ repo: '', ref: '', repo_ok: false, ref_found: false, repo_error: errText(err) })
    } finally {
      setChecking(false)
    }
  }
  useEffect(() => {
    void runCheck()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- при открытии окна
  }, [p.id])
  // Новый .env — сначала разница по именам с текущим (значения — секреты).
  async function save() {
    if (env) {
      let current: string[] = []
      try {
        const vs = await api<{ versions: EnvVersion[] }>(`/hub/pipelines/${p.id}/env/versions`)
        current = vs.versions.find((v) => v.current && !v.cleared)?.names ?? []
      } catch {
        // без истории — сравнивать не с чем
      }
      const next = envNames(env)
      const appear = next.filter((n) => !current.includes(n))
      const vanish = current.filter((n) => !next.includes(n))
      const text = t('deploy.envSaveConfirm', { appear: appear.join(', ') || '—', vanish: vanish.join(', ') || '—', count: next.length })
      if (!(await confirmAction(text))) return
    }
    await send({ git_token: token, ssh_key: key, registry, env })
  }
  async function send(body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    try {
      await api(`/hub/pipelines/${p.id}/credentials`, { method: 'POST', body })
      onSaved()
      // Окно остаётся открытым: сразу проверка с новыми ключами.
      setToken('')
      setKey('')
      setRegistry('')
      setEnv('')
      setSaved(true)
      await runCheck()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('deploy.accessTitle', { name: p.name })} onClose={onClose} width={700}>
      <Space size={4} wrap>
        <span className="small muted">{t('deploy.accessHint')}</span>
        <HelpButton docKey="deploy:access" isHub admin />
      </Space>
      {error && <Banner kind="error">{error}</Banner>}
      {saved && <Banner kind="info">{t('deploy.accessSaved')}</Banner>}
      <AccessCheckView check={check} checking={checking} onRecheck={() => void runCheck()} />
      <p className="small">
        {t('deploy.gitCred')}: <Tag color={p.has_git_cred ? 'success' : 'default'}>{p.has_git_cred ? t('deploy.set') : t('deploy.notSet')}</Tag>
        {' · '}
        {t('deploy.registryCred')}: <Tag color={p.has_registry_cred ? 'success' : 'default'}>{p.has_registry_cred ? t('deploy.set') : t('deploy.notSet')}</Tag>
        {' · '}
        {t('deploy.envCred')}: <Tag color={p.has_env ? 'success' : 'default'}>{p.has_env ? t('deploy.set') : t('deploy.notSet')}</Tag>
      </p>
      <div className="col" style={{ gap: '0.5rem' }}>
        <Input.Password size="small" value={token} placeholder={t('deploy.tokenPlaceholder')} onChange={(e) => setToken(e.target.value)} autoComplete="new-password" />
        <Input.TextArea rows={4} className="mono sensitive-area" value={key} placeholder={t('deploy.keyPlaceholder')} onChange={(e) => setKey(e.target.value)} />
        <Input.Password size="small" value={registry} placeholder={t('deploy.registryPlaceholder')} onChange={(e) => setRegistry(e.target.value)} autoComplete="new-password" />
        <Input.TextArea rows={4} className="mono sensitive-area" value={env} placeholder={t('deploy.envPlaceholder')} onChange={(e) => setEnv(e.target.value)} />
        <Space wrap>
          <Button type="primary" loading={busy} disabled={!token && !key && !registry && !env} onClick={() => void save()}>
            {t('deploy.saveAccess')}
          </Button>
          {p.has_git_cred && (
            <Button danger size="small" onClick={() => void send({ clear_git: true })}>
              {t('deploy.clearGit')}
            </Button>
          )}
          {p.has_registry_cred && (
            <Button danger size="small" onClick={() => void send({ clear_registry: true })}>
              {t('deploy.clearRegistry')}
            </Button>
          )}
          {p.has_env && (
            <Button danger size="small" onClick={() => void send({ clear_env: true })}>
              {t('deploy.clearEnv')}
            </Button>
          )}
          <Button size="small" onClick={() => setEnvHistory(true)}>
            {t('deploy.envHistory')}
          </Button>
        </Space>
      </div>
      {envHistory && <EnvHistoryModal p={p} onClose={() => setEnvHistory(false)} onChanged={onSaved} />}
    </Modal>
  )
}

/** Имена переменных .env (как их читает docker compose). */
function envNames(text: string): string[] {
  const out = new Set<string>()
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim().replace(/^export\s+/, '')
    if (!line || line.startsWith('#')) continue
    const k = line.split('=')[0].trim()
    if (k) out.add(k)
  }
  return [...out].sort()
}

/** История .env: разница — только по именам переменных (значения —
 * секреты); значения — по кнопке, с записью в журнал действий; возврат
 * версии — новой версией, на хосты — со следующей выкладкой. */
function EnvHistoryModal({ p, onClose, onChanged }: { p: Pipeline; onClose: () => void; onChanged: () => void }) {
  const { t } = useTranslation()
  const list = useApi<{ versions: EnvVersion[] }>(`/hub/pipelines/${p.id}/env/versions`)
  const [shown, setShown] = useState<{ id: number; content: string; cleared: boolean } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const current = list.data?.versions.find((v) => v.current)
  const names = (items: string[] | undefined, color: string, sign: string) =>
    (items ?? []).map((n) => (
      <Tag key={sign + n} color={color} className="mono">
        {sign}
        {n}
      </Tag>
    ))
  async function reveal(v: EnvVersion) {
    if (!(await confirmAction(t('deploy.envRevealConfirm', { id: v.id })))) return
    setError(null)
    try {
      const r = await api<{ content: string; cleared: boolean }>(`/hub/pipelines/${p.id}/env/versions/${v.id}/reveal`, { method: 'POST' })
      setShown({ id: v.id, ...r })
    } catch (err) {
      setError(errText(err))
    }
  }
  async function restore(v: EnvVersion) {
    const now = new Set(current?.names ?? [])
    const then = new Set(v.names ?? [])
    const appear = [...then].filter((n) => !now.has(n))
    const vanish = [...now].filter((n) => !then.has(n))
    const text = v.cleared
      ? t('deploy.envRestoreClearedConfirm', { id: v.id })
      : t('deploy.envRestoreConfirm', {
          id: v.id,
          appear: appear.join(', ') || '—',
          vanish: vanish.join(', ') || '—',
        })
    if (!(await confirmAction(text))) return
    setError(null)
    try {
      await api(`/hub/pipelines/${p.id}/env/versions/${v.id}/restore`, { method: 'POST' })
      setShown(null)
      void list.reload()
      onChanged()
    } catch (err) {
      setError(errText(err))
    }
  }
  return (
    <Modal title={t('deploy.envHistoryTitle', { name: p.name })} onClose={onClose} width={860}>
      <p className="small muted">{t('deploy.envHistoryHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <ErrorNote error={list.error} />
      {!list.data ? (
        <Loading what={t('deploy.envHistory')} />
      ) : list.data.versions.length === 0 ? (
        <p className="small muted">{t('deploy.envHistoryEmpty')}</p>
      ) : (
        list.data.versions.map((v) => (
          <div key={v.id} style={{ borderBottom: '1px solid var(--border)', padding: '0.4rem 0' }}>
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <strong className="small">#{v.id}</strong>
              {v.current && <Tag color="blue">{t('deploy.envCurrent')}</Tag>}
              <span className="small">{formatRelative(v.ts)}</span>
              <span className="small">{v.author}</span>
              <span className="small muted">{v.note}</span>
              <span style={{ flex: 1 }} />
              <Button size="small" onClick={() => void reveal(v)}>
                {t('deploy.envReveal')}
              </Button>
              {!v.current && (
                <Button size="small" onClick={() => void restore(v)}>
                  {t('deploy.envRestore')}
                </Button>
              )}
            </div>
            <div style={{ marginTop: '0.25rem' }}>
              {v.cleared ? (
                <Tag>{t('deploy.envCleared')}</Tag>
              ) : (
                <>
                  {names(v.added, 'success', '+')}
                  {names(v.removed, 'error', '−')}
                  {names(v.changed, 'warning', '~')}
                  {!v.added?.length && !v.removed?.length && !v.changed?.length && (
                    <span className="small muted">{t('deploy.envNoChange', { names: (v.names ?? []).join(', ') || '—' })}</span>
                  )}
                </>
              )}
            </div>
            {shown?.id === v.id && (
              <div style={{ marginTop: '0.35rem' }}>
                {shown.cleared ? (
                  <span className="small muted">{t('deploy.envCleared')}</span>
                ) : (
                  <pre className="mono small sensitive-area" style={{ margin: 0, padding: '0.4rem', background: 'var(--bg-subtle, #f6f6f6)', borderRadius: 4, whiteSpace: 'pre-wrap' }}>
                    {shown.content}
                  </pre>
                )}
                <Space style={{ marginTop: '0.25rem' }}>
                  {!shown.cleared && (
                    <Button size="small" icon={<CopyOutlined />} onClick={() => void navigator.clipboard?.writeText(shown.content)}>
                      {t('deploy.envCopy')}
                    </Button>
                  )}
                  <Button size="small" onClick={() => setShown(null)}>
                    {t('deploy.envHide')}
                  </Button>
                </Space>
              </div>
            )}
          </div>
        ))
      )}
    </Modal>
  )
}

/** Вебхук: адрес(а) и секрет подписи (показ — в аудит), примеры. */
function HookModal({ p, onClose }: { p: Pipeline; onClose: () => void }) {
  const { t } = useTranslation()
  const edge = useApi<EdgeStatus>('/hub/edge')
  const [secret, setSecret] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const direct = `${location.origin}/api/hub/hooks/${p.hook_id}`
  const viaEdge = edge.data?.configured && edge.data.domain ? `https://${edge.data.domain}/hooks/${p.hook_id}` : null
  const url = viaEdge ?? direct
  async function reveal(rotate: boolean) {
    if (rotate && !(await confirmAction(t('deploy.rotateConfirm')))) return
    try {
      setSecret((await api<{ secret: string }>(`/hub/pipelines/${p.id}/hook-secret`, { method: 'POST', body: { rotate } })).secret)
    } catch (err) {
      setError(errText(err))
    }
  }
  const copy = (v: string) => (
    <Tooltip title={t('k8s.copy')}>
      <Button size="small" type="text" icon={<CopyOutlined />} onClick={() => void navigator.clipboard?.writeText(v)} />
    </Tooltip>
  )
  const curl = `BODY='{"ref":"refs/heads/main","tag":"'"$TAG"'"}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$NKT_HOOK_SECRET" -hex | sed 's/^.* //')
curl -fsS -X POST ${url} -H "X-NKT-Timestamp: $TS" -H "X-NKT-Signature: $SIG" -d "$BODY"`
  return (
    <Modal title={t('deploy.hookTitle', { name: p.name })} onClose={onClose} width={900}>
      <p className="small muted">{t('deploy.hookHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      {viaEdge && (
        <p className="small">
          {t('deploy.hookViaEdge')}: <span className="mono">{viaEdge}</span> {copy(viaEdge)}
        </p>
      )}
      <p className="small">
        {t('deploy.hookDirect')}: <span className="mono">{direct}</span> {copy(direct)}
      </p>
      {!viaEdge && <Banner kind="info">{t('deploy.hookNoEdge')}</Banner>}
      <Space wrap style={{ margin: '0.5rem 0' }}>
        <Button size="small" onClick={() => void reveal(false)}>
          {t('deploy.showSecret')}
        </Button>
        <Button size="small" danger onClick={() => void reveal(true)}>
          {t('deploy.rotateSecret')}
        </Button>
        {secret && (
          <>
            <span className="mono small sensitive-area">{secret}</span> {copy(secret)}
          </>
        )}
      </Space>
      <p className="small">
        <strong>GitHub / Gitea:</strong> {t('deploy.hookGithub')}
      </p>
      <p className="small">
        <strong>GitLab:</strong> {t('deploy.hookGitlab')}
      </p>
      <p className="small">
        <strong>{t('deploy.hookWorkflow')}</strong>
      </p>
      <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap' }}>
        {curl}
      </pre>
    </Modal>
  )
}

interface AccessCheck {
  repo: string
  ref: string
  repo_ok: boolean
  ref_found: boolean
  repo_error?: string
  registry?: string
  registry_ok?: boolean
  registry_tags?: number
  registry_error?: string
}

/** Итог проверки доступа: репозиторий и ветка, registry. */
function AccessCheckView({ check, checking, onRecheck }: { check: AccessCheck | null; checking: boolean; onRecheck: () => void }) {
  const { t } = useTranslation()
  const ok = (good: boolean, text: string) => (
    <div className="small" style={{ color: good ? 'var(--status-good)' : 'var(--status-critical)', fontWeight: good ? undefined : 600 }}>
      {good ? '✓' : '✗'} {text}
    </div>
  )
  return (
    <div className="col" style={{ gap: '0.15rem', margin: '0.4rem 0 0.6rem' }}>
      <Space size={6}>
        <strong className="small">{t('deploy.accessCheckTitle')}</strong>
        <Button size="small" type="link" loading={checking} onClick={onRecheck}>
          {t('deploy.accessRecheck')}
        </Button>
      </Space>
      {checking && !check ? (
        <span className="small muted">{t('deploy.accessChecking')}</span>
      ) : check ? (
        <>
          {check.repo_ok
            ? check.ref_found
              ? ok(true, t('deploy.accessRepoOK', { repo: check.repo, ref: check.ref }))
              : ok(false, t('deploy.accessRefMissing', { repo: check.repo, ref: check.ref }))
            : ok(false, t('deploy.accessRepoFail', { repo: check.repo, error: check.repo_error ?? '' }))}
          {check.registry &&
            (check.registry_ok
              ? ok(true, t('deploy.accessRegistryOK', { image: check.registry, count: check.registry_tags ?? 0 }))
              : ok(false, t('deploy.accessRegistryFail', { image: check.registry, error: check.registry_error ?? '' })))}
        </>
      ) : null}
    </div>
  )
}

interface ComposeScan {
  services: { name: string; image?: string; build_only?: boolean; ports?: number[]; image_ports?: boolean; published?: string[]; db?: boolean; unpinned?: boolean }[]
  vars: { name: string; default?: string; has_default?: boolean; required?: boolean; secret?: boolean }[]
  files?: string[]
  web?: string
  web_port?: number
}

const WEB_PORTS = [80, 8080, 3000, 8000, 5000, 8081, 8888, 9000, 4000, 5173, 8443, 443]

/** Описание конвейера по разобранному compose-файлу: сайт (выбранный или
 * «авто»), файлы рядом, образы для сервисов со сборкой, наружные
 * публикации и шаблон .env — комментариями, где решать человеку. */
function scannedYaml(
  sc: ComposeScan,
  p: { repo: string; ref: string; file: string },
  proj: string,
  hosts: string[],
  choice: string,
  t: (k: string, o?: Record<string, unknown>) => string,
): string {
  const svcPort = (name: string) => {
    const s = sc.services.find((x) => x.name === name)
    const ports = s?.ports ?? []
    return WEB_PORTS.find((w) => ports.includes(w)) ?? ports[0] ?? 0
  }
  const siteService = choice === 'none' ? '' : choice === 'auto' ? (sc.web ?? '') : choice
  const sitePort = siteService ? (choice === 'auto' ? (sc.web_port ?? svcPort(siteService)) : svcPort(siteService)) : 0
  const lines: string[] = [`# ${t('deploy.scanComment')}`, `repo: ${p.repo}`, `ref: ${p.ref}`, '', 'action: compose', 'compose:', `  file: ${p.file}`, `  project: ${proj}`, `  hosts: [${hosts.join(', ')}]`]
  if (sc.files?.length) lines.push(`  files: [${sc.files.join(', ')}]  # ${t('deploy.fromLinkFilesComment')}`)
  lines.push('  wait_timeout: 5m')
  const builds = sc.services.filter((s) => s.build_only)
  if (builds.length) {
    lines.push(`  images:  # ${t('deploy.scanBuildComment')}`)
    for (const b of builds) lines.push(`    ${b.name}: <${t('deploy.scanImagePlaceholder')}>`)
  }
  // Публикации не на loopback — то, что автор хотел наружу.
  const pubOut = (s: ComposeScan['services'][number]) => (s.published ?? []).filter((x) => !x.startsWith('127.') && !x.startsWith('localhost:'))
  const outside = sc.services.filter((s) => s.name !== siteService && pubOut(s).length > 0)
  if (outside.length) {
    lines.push(`  # ports:  # ${t('deploy.scanPortsComment')}`)
    for (const s of outside) lines.push(`  #   ${s.name}: [${pubOut(s).map((x) => `"${x.split(':').length === 2 ? '0.0.0.0:' + x : x}"`).join(', ')}]`)
  }
  for (const s of sc.services.filter((x) => x.unpinned && x.image)) lines.push(`  # ${t('deploy.scanUnpinned', { image: s.image, name: s.name })}`)
  if (siteService) {
    lines.push(`  # site:  # ${t('deploy.fromLinkSiteComment')}`, `  #   domains: [${proj}.example.com]`, `  #   service: ${siteService}`, `  #   port: ${sitePort || t('deploy.fromLinkSitePort')}`)
  } else if (choice !== 'none') {
    lines.push(`  # ${t('deploy.scanNoWeb')}`)
  }
  lines.push('', `# poll: 5m   # ${t('deploy.fromLinkPollComment')}`)
  if (sc.vars.length) {
    lines.push('', `# ${t('deploy.envTemplateComment')}`)
    for (const v of sc.vars) {
      const val = v.has_default ? v.default : v.secret ? `<${t('deploy.scanSecret')}>` : `<${t('deploy.scanValue')}>`
      lines.push(`#   ${v.name}=${val}${v.required ? `  # ${t('deploy.scanRequired')}` : ''}`)
    }
  }
  return lines.join('\n') + '\n'
}
