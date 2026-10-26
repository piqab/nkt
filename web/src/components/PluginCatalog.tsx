import { useEffect, useRef, useState } from 'react'
import { Button, Input, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, apiURL, fetchHubJob, useApi } from '../api'
import type { Job } from '../types'
import { Banner, DiffView, Modal, formatRelative } from './ui'
import { unifiedDiff } from './textDiff'

/** Ответ /hub/catalog. */
interface CatalogData {
  url: string
  default_url: string
  custom: boolean
  source?: string
  fetched_at?: string
  commit?: string
  generated?: string
  error?: string
  last_attempt?: string
  plugins: CatalogPlugin[]
}

interface CatalogPlugin {
  key: string
  name: string
  version: string
  min_nkt?: string
  compatible: boolean
  title: string
  description: string
  docker?: boolean
  readme: boolean
  screens: string[]
  tests?: string
  installed: { name: string; host: string; outdated?: boolean }[]
}

const fileURL = (key: string, rel: string) => apiURL(`/hub/catalog/files/${key}/${rel}`)

/**
 * «Плагины» → «Каталог»: плагины из подписанного каталога репозитория
 * (копия на хабе — работает и без интернета). Справка и скриншоты — из
 * копии через хаб; «Установить» открывает обычное окно установки;
 * «Обновить каталог» — заданием; без интернета — «Загрузить архив»
 * (catalog.tar.gz с той же страницы, где каталог).
 */
export function CatalogModal({
  admin,
  onClose,
  onInstall,
  onJob,
}: {
  admin: boolean
  onClose: () => void
  onInstall: (key: string) => void
  onJob: (job: Job) => void
}) {
  const { t, i18n } = useTranslation()
  const cat = useApi<CatalogData>('/hub/catalog')
  const [readme, setReadme] = useState<CatalogPlugin | null>(null)
  const [source, setSource] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [uploading, setUploading] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const d = cat.data

  async function refresh() {
    setError(null)
    try {
      const r = await api<{ job_id: number }>('/hub/catalog/refresh', { method: 'POST' })
      onJob(await fetchHubJob<Job>(r.job_id))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function upload(file: File) {
    setError(null)
    setUploading(true)
    try {
      const res = await fetch(apiURL('/hub/catalog/upload'), { method: 'POST', credentials: 'same-origin', headers: { 'X-NKT-Lang': i18n.language }, body: file })
      if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.error || res.statusText)
      }
      cat.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setUploading(false)
    }
  }

  return (
    <Modal title={t('catalog.title')} onClose={onClose} width={860}>
      <div className="col small" style={{ gap: '0.6rem' }}>
        <span className="muted">{t('catalog.hint')}</span>
        {cat.error && <Banner kind="error">{cat.error}</Banner>}
        {d && (
          <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <span className="muted">
              {d.source
                ? t('catalog.state', {
                    source: d.source === 'upload' ? t('catalog.fromUpload') : d.source,
                    time: d.fetched_at ? formatRelative(d.fetched_at) : '—',
                    commit: (d.commit ?? '').slice(0, 8) || '—',
                  })
                : t('catalog.notLoaded')}
            </span>
            {d.custom && <Tag color="processing">{t('catalog.customSource')}</Tag>}
            {admin && (
              <>
                <Button size="small" type={d.source ? 'default' : 'primary'} onClick={() => void refresh()}>
                  {t('catalog.refresh')}
                </Button>
                <Button size="small" loading={uploading} onClick={() => fileInput.current?.click()}>
                  {t('catalog.upload')}
                </Button>
                <input
                  ref={fileInput}
                  type="file"
                  accept=".tar.gz,.tgz,application/gzip"
                  style={{ display: 'none' }}
                  onChange={(e) => {
                    const f = e.target.files?.[0]
                    e.target.value = ''
                    if (f) void upload(f)
                  }}
                />
                <Button size="small" onClick={() => setSource(true)}>
                  {t('catalog.source')}
                </Button>
              </>
            )}
          </div>
        )}
        {d?.error && <Banner kind="warn">{t('catalog.lastError', { error: d.error, time: d.last_attempt ? formatRelative(d.last_attempt) : '' })}</Banner>}
        {error && <Banner kind="error">{error}</Banner>}
        {d && d.plugins.length === 0 && d.source && <span className="muted">{t('catalog.empty')}</span>}
        {(d?.plugins ?? []).map((p) => (
          <div key={p.key} className="col" style={{ gap: '0.25rem', padding: '0.5rem 0', borderTop: '1px solid var(--border)' }}>
            <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <strong>{p.title}</strong>
              <Tag>{p.version}</Tag>
              {p.tests === 'passed' && <Tag color="success">{t('catalog.testsPassed')}</Tag>}
              {!p.compatible && <Tag color="warning">{t('catalog.needsNKT', { version: p.min_nkt })}</Tag>}
              {p.docker && <Tag color="warning">{t('catalog.docker')}</Tag>}
              {p.installed.map((i) => (
                <Tag key={`${i.name}@${i.host}`} color={i.outdated ? 'processing' : 'default'}>
                  {i.name}@{i.host}
                  {i.outdated ? ` · ${t('plugins.outdated')}` : ''}
                </Tag>
              ))}
            </div>
            <span className="muted">{p.description}</span>
            <div className="row" style={{ gap: '0.4rem' }}>
              {(p.readme || p.screens.length > 0) && (
                <Button size="small" onClick={() => setReadme(p)}>
                  {t('catalog.readme')}
                </Button>
              )}
              {admin && (
                <Button size="small" type="primary" disabled={!p.compatible} onClick={() => onInstall(p.key)}>
                  {t('plugins.install')}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      {readme && <ReadmeModal plugin={readme} onClose={() => setReadme(null)} />}
      {source && (
        <SourceModal
          onClose={() => setSource(false)}
          onSaved={() => {
            setSource(false)
            cat.reload()
          }}
        />
      )}
    </Modal>
  )
}

/** Справка плагина (README на языке интерфейса) и скриншоты — из копии
 * каталога на хабе. README — обычным текстом, как «Что нового»: разметку
 * чужого файла интерфейс не интерпретирует. */
function ReadmeModal({ plugin, onClose }: { plugin: CatalogPlugin; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const [text, setText] = useState<string | null>(null)
  useEffect(() => {
    if (!plugin.readme) return
    const name = i18n.language === 'en' ? 'README.en.md' : 'README.md'
    let alive = true
    fetch(fileURL(plugin.key, name), { credentials: 'same-origin' })
      .then((r) => (r.ok ? r.text() : ''))
      .then((s) => alive && setText(s))
      .catch(() => alive && setText(''))
    return () => {
      alive = false
    }
  }, [plugin, i18n.language])
  return (
    <Modal title={t('catalog.readmeTitle', { name: plugin.title })} onClose={onClose} width={900}>
      <div className="col small" style={{ gap: '0.75rem' }}>
        {plugin.screens.map((s) => (
          <img key={s} src={fileURL(plugin.key, s)} alt={plugin.title} style={{ maxWidth: '100%', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)' }} />
        ))}
        {plugin.readme && (
          <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'inherit' }}>{text ?? t('catalog.loadingReadme')}</pre>
        )}
      </div>
    </Modal>
  )
}

interface SourceVersion {
  at: string
  user?: string
  url?: string
  keys?: string
}

/** Источник каталога: свой адрес (форк, внутренний сервер) и свои открытые
 * ключи — «Сверить» показывает дифф, история — с возвратом версии. */
function SourceModal({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const st = useApi<{ url: string; keys: string; default_url: string; history: SourceVersion[] }>('/hub/catalog/settings')
  const [url, setURL] = useState<string | null>(null)
  const [keys, setKeys] = useState<string | null>(null)
  const [diff, setDiff] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const cur = { url: st.data?.url ?? '', keys: st.data?.keys ?? '' }
  const next = { url: url ?? cur.url, keys: keys ?? cur.keys }
  const text = (v: { url: string; keys: string }) => `url: ${v.url || st.data?.default_url || ''}\nkeys:\n${v.keys || '—'}\n`

  async function save(dry: boolean) {
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ changed?: boolean }>('/hub/catalog/settings', { method: 'POST', body: { ...next, dry_run: dry } })
      if (dry) setDiff(r.changed ? unifiedDiff(text(cur), text(next), t('catalog.sourceCurrent'), t('editModal.draft')) : '')
      else onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('catalog.source')} onClose={onClose} width={720} maskClosable={false}>
      <div className="col small" style={{ gap: '0.6rem' }}>
        <span className="muted">{t('catalog.sourceHint')}</span>
        {st.error && <Banner kind="error">{st.error}</Banner>}
        {(st.data?.history.length ?? 0) > 0 && (
          <label className="col" style={{ gap: '0.2rem' }}>
            {t('plugins.settingsHistory')}
            <Select
              placeholder={t('plugins.settingsVersionPick')}
              value={null}
              onChange={(i: number) => {
                const h = st.data!.history[i]
                setURL(h.url ?? '')
                setKeys(h.keys ?? '')
                setDiff(null)
              }}
              options={st.data!.history.map((h, i) => ({ value: i, label: `${formatRelative(h.at)} · ${h.user || '—'} · ${h.url || st.data!.default_url}` }))}
            />
          </label>
        )}
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('catalog.sourceURL')}
          <Input
            value={next.url}
            placeholder={st.data?.default_url}
            onChange={(e) => {
              setURL(e.target.value)
              setDiff(null)
            }}
          />
        </label>
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('catalog.sourceKeys')}
          <Input.TextArea
            rows={4}
            className="mono"
            value={next.keys}
            placeholder={'-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----'}
            onChange={(e) => {
              setKeys(e.target.value)
              setDiff(null)
            }}
          />
          <span className="muted">{t('catalog.sourceKeysHint')}</span>
        </label>
        {diff === '' && <span className="muted">{t('plugins.settingsNoChanges')}</span>}
        {diff && <DiffView text={diff} />}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button loading={busy && diff === null} disabled={!st.data} onClick={() => void save(true)}>
            {t('plugins.settingsCheck')}
          </Button>
          <Button type="primary" loading={busy && !!diff} disabled={!diff} onClick={() => void save(false)}>
            {t('plugins.settingsSave')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}
