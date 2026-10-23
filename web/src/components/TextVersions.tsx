import { useRef, useState } from 'react'
import { Button, Segmented } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, CodeEditor, DiffView, Loading, formatDateTime } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { unifiedDiff } from './textDiff'

export interface TextVersion {
  id: number
  ts: string
  author?: string
  note?: string
  size?: number
  content?: string
}

/**
 * История редакций текста, который хранит хаб (сценарии, профили,
 * описания сервисов): таблица как в «Конфигурациях» — №, время, автор,
 * заметка, размер, метка «текущая»; у каждой редакции — «Содержимое»,
 * «Разница» (с текущей или с предыдущей) и «Вернуть в редактор». Возврат
 * ничего не пишет: текст попадает в окно правки, дальше — обычное
 * сохранение с диффом.
 */
export function TextVersions({
  listPath,
  itemPath,
  onRestore,
  canRestore = true,
}: {
  /** GET → { versions: [...] }, новые сверху. */
  listPath: string
  /** GET одной редакции целиком (с content). */
  itemPath: (id: number) => string
  onRestore?: (content: string) => void
  canRestore?: boolean
}) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: TextVersion[] }>(listPath)
  const cache = useRef(new Map<number, string>())
  const [open, setOpen] = useState<{ id: number; mode: 'content' | 'diff' } | null>(null)
  const [against, setAgainst] = useState<'current' | 'previous'>('current')
  const [view, setView] = useState<{ content: string; diff: string } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const list = versions.data?.versions ?? []
  const currentId = list[0]?.id
  // № по порядку, как в таблице: первая редакция — 1.
  const num = (id: number) => list.length - list.findIndex((v) => v.id === id)

  async function content(id: number): Promise<string> {
    const hit = cache.current.get(id)
    if (hit !== undefined) return hit
    const full = await api<TextVersion>(itemPath(id))
    const text = full.content ?? ''
    cache.current.set(id, text)
    return text
  }

  async function show(id: number, mode: 'content' | 'diff', with_: 'current' | 'previous' = against) {
    setError(null)
    try {
      const text = await content(id)
      let diff = ''
      if (mode === 'diff') {
        const idx = list.findIndex((v) => v.id === id)
        if (with_ === 'current') {
          diff = unifiedDiff(text, await content(currentId!), t('versions.thisOne', { n: num(id) }), t('versions.currentOne', { n: num(currentId!) }))
        } else if (idx + 1 < list.length) {
          const prev = list[idx + 1].id
          diff = unifiedDiff(await content(prev), text, t('versions.thisOne', { n: num(prev) }), t('versions.thisOne', { n: num(id) }))
        } else {
          diff = unifiedDiff('', text, t('versions.nothing'), t('versions.thisOne', { n: num(id) }))
        }
      }
      setOpen({ id, mode })
      setView({ content: text, diff })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  function toggle(id: number, mode: 'content' | 'diff') {
    if (open?.id === id && open.mode === mode) {
      setOpen(null)
      setView(null)
      return
    }
    // У текущей «с текущей» — пусто: показываем с предыдущей.
    const w = mode === 'diff' && id === currentId ? 'previous' : against
    if (w !== against) setAgainst(w)
    void show(id, mode, w)
  }

  if (versions.loading && !versions.data) return <Loading what={t('versions.title')} />
  if (versions.error) return <Banner kind="error">{versions.error}</Banner>
  if (list.length === 0) return <div className="chart-empty">{t('versions.empty')}</div>
  return (
    <div className="col" style={{ gap: '0.5rem' }}>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      <div className="table-wrap">
        <DataTable<TextVersion>
          dataSource={list}
          rowKey="id"
          columns={[
            { title: '#', key: 'n', align: 'right', render: (_, v) => <span className="num small">{num(v.id)}</span> },
            { title: t('configs.colTs'), key: 'ts', render: (_, v) => <span className="small nowrap">{formatDateTime(v.ts)}</span> },
            { title: t('configs.colAuthor'), key: 'author', render: (_, v) => <span className="small">{v.author || '—'}</span> },
            { title: t('configs.colNote'), key: 'note', render: (_, v) => <span className="small secondary">{v.note}</span> },
            { title: t('configs.colSize'), key: 'size', align: 'right', render: (_, v) => <span className="num small nowrap">{formatBytes(v.size ?? 0)}</span> },
            {
              title: '',
              key: 'actions',
              render: (_, v) => (
                <div className="row row-nowrap" style={{ gap: '0.25rem', justifyContent: 'flex-end' }}>
                  {v.id === currentId && <span className="small secondary nowrap">{t('configs.isCurrent')}</span>}
                  <Button size="small" type={open?.id === v.id && open.mode === 'content' ? 'primary' : 'default'} onClick={() => toggle(v.id, 'content')}>
                    {t('versions.content')}
                  </Button>
                  <Button size="small" type={open?.id === v.id && open.mode === 'diff' ? 'primary' : 'default'} onClick={() => toggle(v.id, 'diff')}>
                    {t('versions.diff')}
                  </Button>
                  {canRestore && onRestore && v.id !== currentId && (
                    <Button size="small" onClick={() => void content(v.id).then(onRestore)}>
                      {t('versions.restore')}
                    </Button>
                  )}
                </div>
              ),
            },
          ]}
        />
      </div>
      {open && view && (
        <div className="col" style={{ gap: '0.4rem' }}>
          <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <span className="small secondary">
              {t(open.mode === 'content' ? 'versions.contentCaption' : 'versions.diffCaption', { n: num(open.id) })}
            </span>
            {open.mode === 'diff' && (
              <Segmented
                size="small"
                value={against}
                onChange={(v) => {
                  setAgainst(v as 'current' | 'previous')
                  void show(open.id, 'diff', v as 'current' | 'previous')
                }}
                options={[
                  { value: 'current', label: t('versions.withCurrent'), disabled: open.id === currentId },
                  { value: 'previous', label: t('versions.withPrevious') },
                ]}
              />
            )}
          </div>
          {open.mode === 'content' ? (
            <CodeEditor value={view.content} onChange={() => undefined} rows={16} readOnly />
          ) : view.diff === '' ? (
            <p className="small muted">{t('editModal.noChanges')}</p>
          ) : (
            <DiffView text={view.diff} />
          )}
        </div>
      )}
    </div>
  )
}
