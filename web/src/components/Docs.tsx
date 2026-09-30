import { useState } from 'react'
import { Button, Space } from 'antd'
import { ExportOutlined, QuestionCircleOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { DEFAULT_DOCS_URL, DOCS, type DocsTarget, docsSettingsPath, docsURL, setDocsBase, useDocsBase } from '../docs'
import { Banner, Card, Modal, formatRelative } from './ui'
import { EditTextModal } from './EditTextModal'

interface DocsState {
  url: string
  default: string
  custom: boolean
  history: { ts: string; author?: string; url: string }[]
}

/**
 * Окно справки: страница сайта документации внутри (iframe), «Открепить»
 * — в отдельное окно браузера (как терминал), «В новой вкладке». Сайт
 * может запрещать показ внутри чужих страниц (X-Frame-Options) — тогда
 * окно пустое, и про это строка внизу.
 */
export function DocsModal({ target, isHub, admin, version, onClose }: { target: DocsTarget; isHub: boolean; admin: boolean; version?: string; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const base = useDocsBase(isHub)
  const url = docsURL(base, i18n.language, target)
  const [settings, setSettings] = useState(false)
  function detach() {
    window.open(url, 'nkt-docs', 'width=1100,height=820,resizable=yes')
    onClose()
  }
  return (
    <Modal title={t('docs.title')} onClose={onClose} width={1100} sizeKey="docs">
      <div className="col modal-fill" style={{ gap: '0.4rem', height: '72vh' }}>
        <Space wrap>
          <Button size="small" onClick={detach}>
            {t('docs.detach')}
          </Button>
          <a href={url} target="_blank" rel="noreferrer">
            <Button size="small" icon={<ExportOutlined />}>
              {t('docs.newTab')}
            </Button>
          </a>
          <span className="small muted mono">{url}</span>
        </Space>
        <iframe
          key={url}
          src={url}
          title={t('docs.title')}
          referrerPolicy="no-referrer"
          sandbox="allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox"
          style={{ flex: 1, width: '100%', border: '1px solid var(--border)', borderRadius: 6, background: '#fff' }}
        />
        <div className="small muted">
          {t('docs.hint', { version: version || '—' })}{' '}
          {admin && (
            <Button size="small" type="link" style={{ padding: 0 }} onClick={() => setSettings(true)}>
              {t('docs.settingsLink')}
            </Button>
          )}
        </div>
      </div>
      {settings && <DocsSettingsModal isHub={isHub} onClose={() => setSettings(false)} />}
    </Modal>
  )
}

/** Кнопка «Справка» для места интерфейса (ключ docsMap.json). */
export function HelpButton({ docKey, isHub, admin, version, compact }: { docKey: string; isHub: boolean; admin: boolean; version?: string; compact?: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const target = DOCS[docKey]
  if (!target) return null
  return (
    <>
      <Button size="small" type={compact ? 'text' : 'default'} icon={<QuestionCircleOutlined />} aria-label={t('docs.button')} onClick={() => setOpen(true)}>
        {compact ? null : t('docs.button')}
      </Button>
      {open && <DocsModal target={target} isHub={isHub} admin={admin} version={version} onClose={() => setOpen(false)} />}
    </>
  )
}

/** Адрес сайта справки: правка с диффом и историей. */
export function DocsSettingsModal({ isHub, onClose }: { isHub: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const state = useApi<DocsState>(docsSettingsPath(isHub))
  const [draft, setDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const saved = state.data ? state.data.url + '\n' : ''
  const text = draft ?? saved
  async function save(): Promise<boolean> {
    setBusy(true)
    setError(null)
    try {
      const res = await api<DocsState>(docsSettingsPath(isHub), { method: 'PUT', body: { url: text.trim() } })
      setDocsBase(res.url)
      void state.reload()
      setDraft(null)
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <EditTextModal
        title={t('docs.settingsTitle')}
        saved={saved}
        draft={text}
        onDraft={setDraft}
        busy={busy}
        onSave={save}
        onClose={onClose}
        onHistory={() => setHistory(true)}
        rows={2}
        fields={
          <>
            <p className="small muted">{t('docs.settingsHint', { def: DEFAULT_DOCS_URL })}</p>
            <Space wrap style={{ marginBottom: '0.4rem' }}>
              <Button size="small" onClick={() => setDraft(DEFAULT_DOCS_URL + '\n')}>
                {t('docs.useDefault')}
              </Button>
              <a href={text.trim()} target="_blank" rel="noreferrer">
                <Button size="small" icon={<ExportOutlined />}>
                  {t('docs.check')}
                </Button>
              </a>
            </Space>
          </>
        }
        below={error ? <Banner kind="error" onClose={() => setError(null)}>{error}</Banner> : null}
      />
      {history && (
        <Modal title={t('docs.historyTitle')} onClose={() => setHistory(false)} width={700}>
          {(state.data?.history ?? []).length === 0 ? (
            <p className="small muted">{t('docs.historyEmpty')}</p>
          ) : (
            state.data!.history.map((h, i) => (
              <div key={i} className="row" style={{ gap: '0.5rem', alignItems: 'center', borderBottom: '1px solid var(--border)', padding: '0.3rem 0' }}>
                <span className="small">{formatRelative(h.ts)}</span>
                <span className="small">{h.author}</span>
                <span className="mono small" style={{ flex: 1 }}>
                  {h.url}
                </span>
                <Button
                  size="small"
                  onClick={() => {
                    setDraft(h.url + '\n')
                    setHistory(false)
                  }}
                >
                  {t('docs.load')}
                </Button>
              </div>
            ))
          )}
        </Modal>
      )}
    </>
  )
}

/** Карточка «Справка» в «О системе». */
export function DocsSettingsCard({ isHub, admin }: { isHub: boolean; admin: boolean }) {
  const { t } = useTranslation()
  const base = useDocsBase(isHub)
  const [edit, setEdit] = useState(false)
  return (
    <Card
      title={t('docs.cardTitle')}
      subtitle={t('docs.cardHint')}
      actions={
        admin && (
          <Button size="small" onClick={() => setEdit(true)}>
            {t('docs.change')}
          </Button>
        )
      }
    >
      <p className="small">
        {t('docs.current')}:{' '}
        <a href={base} target="_blank" rel="noreferrer" className="mono">
          {base}
        </a>
        {base === DEFAULT_DOCS_URL ? <span className="muted"> ({t('docs.isDefault')})</span> : null}
      </p>
      {edit && <DocsSettingsModal isHub={isHub} onClose={() => setEdit(false)} />}
    </Card>
  )
}
