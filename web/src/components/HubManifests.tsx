import { useState } from 'react'
import { Button, Input, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { EditTextModal } from './EditTextModal'
import { confirmAction } from './confirm'
import { unifiedDiff } from './textDiff'
import { ClusterPicker } from './HubClustersMulti'

interface Manifest {
  id: number
  name: string
  content?: string
  note?: string
  author?: string
  updated_at: string
}
interface ManifestVersion {
  id: number
  ts: string
  author?: string
  note?: string
  content?: string
  results?: string
}
interface Result {
  cluster_id: number
  cluster: string
  diff?: string
  output?: string
  error?: string
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

const SAMPLE = `apiVersion: v1
kind: Namespace
metadata:
  name: platform
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-info
  namespace: platform
data:
  owner: ops
`

/**
 * Манифесты хаба: один YAML — сразу в несколько кластеров. Перед записью
 * — текстовый дифф с прошлой редакцией и kubectl diff в каждом кластере;
 * каждое применение — редакция с итогом по кластерам.
 */
export function HubManifestsCard() {
  const { t } = useTranslation()
  const list = useApi<{ manifests: Manifest[] }>('/hub/k8s/manifests')
  const [edit, setEdit] = useState<{ manifest?: Manifest; content?: string } | null>(null)
  const [history, setHistory] = useState<Manifest | null>(null)
  const [results, setResults] = useState<{ name: string; results: Result[] } | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function open(m: Manifest, content?: string) {
    try {
      const full = await api<Manifest>(`/hub/k8s/manifests/${m.id}`)
      setEdit({ manifest: full, content })
    } catch (err) {
      setError(errText(err))
    }
  }

  const columns = [
    { title: t('manifests.name'), key: 'name', render: (_: unknown, m: Manifest) => <strong className="small">{m.name}</strong> },
    { title: t('manifests.note'), key: 'note', render: (_: unknown, m: Manifest) => <span className="small">{m.note || '—'}</span> },
    { title: t('manifests.author'), key: 'author', render: (_: unknown, m: Manifest) => <span className="small">{m.author || '—'}</span> },
    { title: t('manifests.updated'), key: 'updated', render: (_: unknown, m: Manifest) => <span className="small nowrap">{formatRelative(m.updated_at)}</span> },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, m: Manifest) => (
        <Space size={4}>
          <Button size="small" onClick={() => void open(m)}>
            {t('manifests.open')}
          </Button>
          <Button size="small" onClick={() => setHistory(m)}>
            {t('manifests.history')}
          </Button>
          <Button
            size="small"
            danger
            onClick={async () => {
              if (!(await confirmAction(t('manifests.deleteConfirm', { name: m.name })))) return
              try {
                await api(`/hub/k8s/manifests/${m.id}`, { method: 'DELETE' })
                void list.reload()
              } catch (err) {
                setError(errText(err))
              }
            }}
          >
            {t('manifests.delete')}
          </Button>
        </Space>
      ),
    },
  ]

  return (
    <Card
      title={t('manifests.title')}
      subtitle={t('manifests.subtitle')}
      actions={
        <Button size="small" type="primary" onClick={() => setEdit({})}>
          {t('manifests.new')}
        </Button>
      }
    >
      <ErrorNote error={list.error} />
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {results && <ResultsBanner name={results.name} results={results.results} onClose={() => setResults(null)} />}
      {list.loading && !list.data ? (
        <Loading what={t('manifests.title')} />
      ) : (list.data?.manifests ?? []).length === 0 ? (
        <p className="small muted">{t('manifests.empty')}</p>
      ) : (
        <div className="table-wrap">
          <DataTable<Manifest> dataSource={list.data?.manifests ?? []} rowKey={(m) => String(m.id)} size="small" columns={columns} />
        </div>
      )}
      {edit && (
        <ApplyModal
          manifest={edit.manifest}
          initial={edit.content}
          onClose={() => setEdit(null)}
          onApplied={(name, res) => {
            setResults({ name, results: res })
            void list.reload()
          }}
        />
      )}
      {history && (
        <HistoryModal
          manifest={history}
          onClose={() => setHistory(null)}
          onLoad={(content) => {
            const m = history
            setHistory(null)
            void open(m, content)
          }}
        />
      )}
    </Card>
  )
}

function ResultsBanner({ name, results, onClose }: { name: string; results: Result[]; onClose?: () => void }) {
  const { t } = useTranslation()
  const failed = results.filter((r) => r.error)
  return (
    <Banner kind={failed.length ? 'error' : 'info'} onClose={onClose}>
      <div>{t(failed.length ? 'manifests.appliedPartly' : 'manifests.applied', { name, ok: results.length - failed.length, total: results.length })}</div>
      {results.map((r) => (
        <div key={r.cluster_id} className="small">
          <Tag color={r.error ? 'error' : 'success'}>{r.cluster}</Tag>
          <span className="mono">{r.error || r.output || 'ok'}</span>
        </div>
      ))}
    </Banner>
  )
}

function ApplyModal({ manifest, initial, onClose, onApplied }: { manifest?: Manifest; initial?: string; onClose: () => void; onApplied: (name: string, res: Result[]) => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState(manifest?.name ?? '')
  const [note, setNote] = useState('')
  const [selected, setSelected] = useState<number[]>([])
  const saved = manifest?.content ?? ''
  const [draft, setDraft] = useState(initial ?? (manifest ? saved : SAMPLE))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function clusterDiff(): Promise<string> {
    if (selected.length === 0) throw new Error(t('manifests.pickClusters'))
    const res = await api<{ results: Result[] }>('/hub/k8s/manifests/diff', { method: 'POST', body: { content: draft, clusters: selected } })
    // Один дифф на кластер, с заголовком: так его читает DiffView.
    return res.results.map((r) => `### ${r.cluster}\n${r.error ? `! ${r.error}\n` : r.diff || `${t('editModal.noChanges')}\n`}`).join('\n')
  }

  async function apply(): Promise<boolean> {
    setError(null)
    if (selected.length === 0) {
      setError(t('manifests.pickClusters'))
      return false
    }
    setBusy(true)
    try {
      const res = await api<{ results: Result[] }>('/hub/k8s/manifests/apply', { method: 'POST', body: { name, note, content: draft, clusters: selected } })
      onApplied(name, res.results)
      return true
    } catch (err) {
      setError(errText(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <EditTextModal
      title={manifest ? t('manifests.applyTitle', { name: manifest.name }) : t('manifests.newTitle')}
      saved={saved}
      draft={draft}
      onDraft={setDraft}
      busy={busy}
      onSave={apply}
      onClose={onClose}
      blocksEndpoint="/hub/k8s/manifests/blocks"
      serverDiff={{ title: t('manifests.clusterDiff'), load: clusterDiff }}
      fields={
        <>
          <p className="small muted">{t('manifests.hint')}</p>
          <Space wrap style={{ marginBottom: '0.5rem' }}>
            <Input size="small" style={{ width: '16rem' }} value={name} disabled={!!manifest} placeholder={t('manifests.name')} onChange={(e) => setName(e.target.value)} />
            <Input size="small" style={{ width: '18rem' }} value={note} placeholder={t('manifests.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />
          </Space>
          <div style={{ marginBottom: '0.5rem' }}>
            <ClusterPicker value={selected} onChange={setSelected} />
          </div>
        </>
      }
      below={
        error ? (
          <Banner kind="error" onClose={() => setError(null)}>
            {error}
          </Banner>
        ) : null
      }
    />
  )
}

function HistoryModal({ manifest, onClose, onLoad }: { manifest: Manifest; onClose: () => void; onLoad: (content: string) => void }) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: ManifestVersion[] }>(`/hub/k8s/manifests/${manifest.id}/versions`)
  const current = useApi<Manifest>(`/hub/k8s/manifests/${manifest.id}`)
  const [diff, setDiff] = useState<{ id: number; text: string } | null>(null)
  const list = versions.data?.versions ?? []

  async function full(id: number) {
    return api<ManifestVersion>(`/hub/k8s/manifests/versions/${id}`)
  }

  return (
    <Modal title={t('manifests.historyTitle', { name: manifest.name })} onClose={onClose} width={960}>
      {versions.loading && !versions.data ? (
        <Loading what={t('manifests.history')} />
      ) : (
        list.map((v, i) => {
          let res: Result[] = []
          try {
            res = v.results ? (JSON.parse(v.results) as Result[]) : []
          } catch {
            res = []
          }
          return (
            <div key={v.id} style={{ borderBottom: '1px solid var(--border)', padding: '0.5rem 0' }}>
              <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                <strong className="small">#{v.id}</strong>
                <span className="small nowrap">{formatRelative(v.ts)}</span>
                <span className="small">{v.author}</span>
                <span className="small muted">{v.note}</span>
                {res.map((r) => (
                  <Tag key={r.cluster_id} color={r.error ? 'error' : 'success'} title={r.error || r.output}>
                    {r.cluster}
                  </Tag>
                ))}
                <span style={{ flex: 1 }} />
                {i > 0 && (
                  <Button
                    size="small"
                    onClick={async () => {
                      if (diff?.id === v.id) return setDiff(null)
                      const old = await full(v.id)
                      setDiff({ id: v.id, text: unifiedDiff(old.content ?? '', current.data?.content ?? '', `#${v.id}`, t('manifests.current')) })
                    }}
                  >
                    {t('manifests.diffWithCurrent')}
                  </Button>
                )}
                <Button size="small" onClick={async () => onLoad((await full(v.id)).content ?? '')}>
                  {t('manifests.loadVersion')}
                </Button>
              </div>
              {diff?.id === v.id && (diff.text ? <DiffView text={diff.text} /> : <p className="small muted">{t('editModal.noChanges')}</p>)}
            </div>
          )
        })
      )}
    </Modal>
  )
}
