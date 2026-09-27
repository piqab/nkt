import { useEffect, useState, type ReactNode } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { Banner, CodeEditor, DiffView, Loading, Modal } from './ui'
import { unifiedDiff } from './textDiff'

/**
 * Окно правки текста — общий шаблон для всего, что правится в nkt:
 * редактор, свои поля (заметка, цвет, имя), «показать изменения» и
 * «Сохранить» через окно диффа «сохранено → черновик»; запись — только
 * по «Записать». История версий — кнопкой рядом (onHistory).
 */
export function EditTextModal({
  title,
  saved,
  draft,
  onDraft,
  fields,
  busy,
  onSave,
  onHistory,
  onClose,
  rows = 22,
  below,
  serverDiff,
}: {
  title: string
  /** Текст до правки — с ним сравнивается черновик. Для нового — ''. */
  saved: string
  draft: string
  onDraft: (v: string) => void
  /** Поля над редактором (имя, заметка, цвет). */
  fields?: ReactNode
  busy?: boolean
  /** Запись; true — успешно, окно закрывается. */
  onSave: () => Promise<boolean>
  onHistory?: () => void
  onClose: () => void
  rows?: number
  /** Под редактором: итог проверки, подсказки. */
  below?: ReactNode
  /** Дифф от самой системы (kubectl diff) — в окне изменений под
   * текстовым: что получится после записи с учётом значений по умолчанию. */
  serverDiff?: { title: string; load: () => Promise<string> }
}) {
  const { t } = useTranslation()
  const [preview, setPreview] = useState<{ diff: string; confirm: boolean } | null>(null)
  const dirty = draft !== saved
  const diff = () => unifiedDiff(saved, draft, t('editModal.saved'), t('editModal.draft'))

  async function write() {
    setPreview(null)
    if (await onSave()) onClose()
  }

  return (
    <Modal title={title} onClose={onClose} width={1100} maskClosable={false} sizeKey="edit">
      {fields}
      <CodeEditor value={draft} onChange={(e) => onDraft(e.target.value)} rows={rows} fill />
      {below}
      <div className="row" style={{ gap: '0.5rem', marginTop: '0.6rem', alignItems: 'center' }}>
        {dirty && <span className="small" style={{ color: 'var(--status-warning)' }}>{t('configs.unsavedChanges')}</span>}
        <Button onClick={() => onDraft(saved)} disabled={!dirty}>
          {t('configs.reset')}
        </Button>
        <Button onClick={() => setPreview({ diff: diff(), confirm: false })} disabled={!dirty}>
          {t('configs.showChanges')}
        </Button>
        {onHistory && <Button onClick={onHistory}>{t('configs.versionHistoryTitle')}</Button>}
        <Button type="primary" loading={busy} onClick={() => setPreview({ diff: diff(), confirm: true })}>
          {t('common.save')}
        </Button>
      </div>
      {preview && (
        <Modal title={t(preview.confirm ? 'blocks.previewTitle' : 'editModal.changes')} onClose={() => setPreview(null)} width={900} maskClosable={false} sizeKey="diff">
          {preview.diff === '' ? <p className="small muted">{t('editModal.noChanges')}</p> : <DiffView text={preview.diff} />}
          {serverDiff && <ServerDiff title={serverDiff.title} load={serverDiff.load} />}
          {preview.confirm && (
            <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
              {/* Сохранить без изменений можно — у нового профиля или при
                  смене одной лишь заметки/цвета текст не меняется. */}
              <Button type="primary" loading={busy} onClick={() => void write()}>
                {t('virt.applyChanges')}
              </Button>
              <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
            </div>
          )}
        </Modal>
      )}
    </Modal>
  )
}

function ServerDiff({ title, load }: { title: string; load: () => Promise<string> }) {
  const { t } = useTranslation()
  const [state, setState] = useState<{ text?: string; error?: string } | null>(null)
  useEffect(() => {
    let cancelled = false
    load()
      .then((text) => !cancelled && setState({ text }))
      .catch((err) => !cancelled && setState({ error: err instanceof Error ? err.message : String(err) }))
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- один раз на открытие окна
  }, [])
  return (
    <div style={{ marginTop: '0.75rem' }}>
      <strong className="small">{title}</strong>
      {!state ? (
        <Loading what={title} />
      ) : state.error ? (
        <Banner kind="error">{state.error}</Banner>
      ) : state.text ? (
        <DiffView text={state.text} />
      ) : (
        <p className="small muted">{t('editModal.noChanges')}</p>
      )}
    </div>
  )
}
