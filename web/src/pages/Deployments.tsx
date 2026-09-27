import { useState } from 'react'
import { Button, Input, Space, Switch, Tag, Tooltip } from 'antd'
import { CopyOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, Me } from '../types'
import { Banner, Card, DiffView, ErrorNote, InfoHint, Loading, Modal, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { EditTextModal } from '../components/EditTextModal'
import { confirmAction } from '../components/confirm'
import { unifiedDiff } from '../components/textDiff'
import { JobLogModal } from './Jobs'

interface Deployment {
  id: number
  ref?: string
  commit?: string
  tag?: string
  trigger: string
  author?: string
  job_id?: number
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
  has_git_cred: boolean
  has_registry_cred: boolean
  updated_at: string
  last?: Deployment
}
interface PipelineVersion {
  id: number
  ts: string
  author?: string
  note?: string
  content?: string
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
          {!p.enabled && <Tag>{t('deploy.disabled')}</Tag>}
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
          </Space>
        ) : (
          <span className="small muted">{t('deploy.never')}</span>
        ),
    },
    {
      title: t('deploy.enabled'),
      key: 'enabled',
      render: (_: unknown, p: Pipeline) => (
        <Switch
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
        </Space>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('deploy.title')}
          <InfoHint>{t('deploy.pageHint')}</InfoHint>
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
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} onDone={() => void list.reload()} />}
    </>
  )
}

/** Описание конвейера (YAML): правка с диффом, история редакций. */
function PipelineEditor({ pipeline, onClose, onSaved }: { pipeline?: Pipeline; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const tpl = useApi<{ content: string }>(pipeline ? null : '/hub/pipelines/template')
  const [name, setName] = useState('')
  const [note, setNote] = useState('')
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const saved = pipeline?.content ?? ''
  const text = draft ?? (pipeline ? saved : (tpl.data?.content ?? ''))

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
            <p className="small muted">{t('deploy.editHint')}</p>
            <Space wrap style={{ marginBottom: '0.5rem' }}>
              {!pipeline && <Input size="small" style={{ width: '16rem' }} value={name} placeholder={t('deploy.name')} onChange={(e) => setName(e.target.value)} />}
              {pipeline && <Input size="small" style={{ width: '20rem' }} value={note} placeholder={t('deploy.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />}
            </Space>
          </>
        }
        below={error ? <Banner kind="error" onClose={() => setError(null)}>{error}</Banner> : null}
      />
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
      </Space>
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
                if (!(await confirmAction(t('deploy.rollbackConfirm', { what: d.tag || short(d.commit) })))) return
                try {
                  const r = await api<{ job_id: number }>(`/hub/pipelines/${p.id}/rollback`, { method: 'POST', body: { deployment_id: d.id } })
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
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function send(body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    try {
      await api(`/hub/pipelines/${p.id}/credentials`, { method: 'POST', body })
      onSaved()
      onClose()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('deploy.accessTitle', { name: p.name })} onClose={onClose} width={700}>
      <p className="small muted">{t('deploy.accessHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <p className="small">
        {t('deploy.gitCred')}: <Tag color={p.has_git_cred ? 'success' : 'default'}>{p.has_git_cred ? t('deploy.set') : t('deploy.notSet')}</Tag>
        {' · '}
        {t('deploy.registryCred')}: <Tag color={p.has_registry_cred ? 'success' : 'default'}>{p.has_registry_cred ? t('deploy.set') : t('deploy.notSet')}</Tag>
      </p>
      <div className="col" style={{ gap: '0.5rem' }}>
        <Input.Password size="small" value={token} placeholder={t('deploy.tokenPlaceholder')} onChange={(e) => setToken(e.target.value)} autoComplete="new-password" />
        <Input.TextArea rows={4} className="mono sensitive-area" value={key} placeholder={t('deploy.keyPlaceholder')} onChange={(e) => setKey(e.target.value)} />
        <Input.Password size="small" value={registry} placeholder={t('deploy.registryPlaceholder')} onChange={(e) => setRegistry(e.target.value)} autoComplete="new-password" />
        <Space wrap>
          <Button type="primary" loading={busy} disabled={!token && !key && !registry} onClick={() => void send({ git_token: token, ssh_key: key, registry })}>
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
        </Space>
      </div>
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
