import { useContext, useState, type ReactNode } from 'react'
import { Button, Segmented, Space, Tooltip } from 'antd'
import { ExportOutlined, QuestionCircleOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { DEFAULT_DOCS_URL, DOCS, DocsContext, type DocsTarget, docsSettingsPath, docsURL, setDocsBase, useDocsBase } from '../docs'
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
        <DocsFrame url={url} />
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

/**
 * Вкладка «Справка» у редактора (сценарии, профили): по умолчанию —
 * страница сайта справки, переключатель «Сайт / Из nkt» — встроенная
 * справка (работает без сайта, из неё вставляются готовые тексты). Выбор
 * помнится в браузере (storageKey).
 */
export function SiteHelpTab({
  docKey,
  isHub,
  storageKey,
  siteHint,
  nktHint,
  fallback,
}: {
  docKey: string
  isHub: boolean
  storageKey: string
  siteHint: string
  nktHint: string
  fallback: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const base = useDocsBase(isHub)
  const target = DOCS[docKey]
  const url = target ? docsURL(base, i18n.language, target) : ''
  const [source, setSource] = useState<'site' | 'nkt'>(() => {
    try {
      return localStorage.getItem(storageKey) === 'nkt' ? 'nkt' : 'site'
    } catch {
      return 'site'
    }
  })
  function choose(v: 'site' | 'nkt') {
    setSource(v)
    try {
      localStorage.setItem(storageKey, v)
    } catch {
      /* без хранилища — выбор до перезагрузки */
    }
  }
  const site = source === 'site' && url !== ''
  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      <Space wrap>
        <Segmented
          size="small"
          value={site ? 'site' : 'nkt'}
          onChange={(v) => choose(v as 'site' | 'nkt')}
          options={[
            { value: 'site', label: t('docs.fromSite'), disabled: url === '' },
            { value: 'nkt', label: t('docs.fromNkt') },
          ]}
        />
        {site && (
          <a href={url} target="_blank" rel="noreferrer">
            <Button size="small" icon={<ExportOutlined />}>
              {t('docs.newTab')}
            </Button>
          </a>
        )}
        <span className="small muted">{site ? siteHint : nktHint}</span>
      </Space>
      {site ? <DocsFrame url={url} height="70vh" /> : fallback}
    </div>
  )
}

/** Страница сайта справки внутри интерфейса (окно справки, вкладки). */
export function DocsFrame({ url, height }: { url: string; height?: string }) {
  const { t } = useTranslation()
  return (
    <iframe
      key={url}
      src={url}
      title={t('docs.title')}
      referrerPolicy="no-referrer"
      sandbox="allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox"
      style={{ flex: height ? undefined : 1, height, width: '100%', border: '1px solid var(--border)', borderRadius: 6, background: '#fff' }}
    />
  )
}

/**
 * «?» в названии вкладки: своя страница справки у каждой вкладки
 * раздела. Щелчок открывает справку и не переключает вкладку.
 */
export function TabHelp({ docKey }: { docKey: string }) {
  const { t } = useTranslation()
  const place = useContext(DocsContext)
  const [open, setOpen] = useState(false)
  const target = DOCS[docKey]
  if (!target || !place) return null
  const show = (e: { stopPropagation: () => void; preventDefault: () => void }) => {
    e.stopPropagation()
    e.preventDefault()
    setOpen(true)
  }
  return (
    <>
      <Tooltip title={t('docs.button')}>
        <QuestionCircleOutlined
          className="muted"
          role="button"
          aria-label={t('docs.button')}
          style={{ marginLeft: '0.35rem', fontSize: '0.85em', cursor: 'pointer' }}
          tabIndex={0}
          onClick={show}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') show(e)
          }}
        />
      </Tooltip>
      {open && <DocsModal target={target} isHub={place.isHub} admin={place.admin} version={place.version} onClose={() => setOpen(false)} />}
    </>
  )
}

/** Кнопка «Справка» для места интерфейса (ключ docsMap.json). */
export function HelpButton({ docKey, isHub, admin, version, compact, label }: { docKey: string; isHub: boolean; admin: boolean; version?: string; compact?: boolean; label?: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const target = DOCS[docKey]
  if (!target) return null
  return (
    <>
      <Button size="small" type={compact ? 'text' : 'default'} icon={<QuestionCircleOutlined />} aria-label={t('docs.button')} onClick={() => setOpen(true)}>
        {compact ? null : label ? `${t('docs.button')}: ${label}` : t('docs.button')}
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

/**
 * Значок у названия раздела вместо «ⓘ»: при наведении — та же подсказка и
 * «нажмите — справка», по щелчку — окно справки для этого раздела.
 * Вне оболочки (нет контекста) — просто подсказка.
 */
export function TitleHelp({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const place = useContext(DocsContext)
  const [open, setOpen] = useState(false)
  const target = place ? DOCS[place.docKey] : undefined
  return (
    <>
      <Tooltip
        title={
          <>
            {children}
            {target && <div style={{ marginTop: '0.3rem', opacity: 0.8 }}>{t('docs.clickHint')}</div>}
          </>
        }
      >
        <QuestionCircleOutlined
          className="muted"
          role={target ? 'button' : undefined}
          aria-label={t('docs.button')}
          style={{ marginLeft: '0.4rem', fontSize: '0.8em', cursor: target ? 'pointer' : 'help', verticalAlign: 'middle' }}
          tabIndex={0}
          onClick={() => target && setOpen(true)}
          onKeyDown={(e) => {
            if (target && (e.key === 'Enter' || e.key === ' ')) setOpen(true)
          }}
        />
      </Tooltip>
      {open && target && place && <DocsModal target={target} isHub={place.isHub} admin={place.admin} version={place.version} onClose={() => setOpen(false)} />}
    </>
  )
}
