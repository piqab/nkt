import { useEffect } from 'react'
import { Modal } from './ui'
import { useTranslation } from 'react-i18next'
import { usePty, wsURL } from '../hooks/usePty'
import { Banner } from './ui'
import { PtyToolbar } from './PtyToolbar'
import { JobFirst } from './useJobLauncher'

/**
 * Стандартное окно выполнения команды nkt — тот же PTY-мост, что у
 * обновлений и установок, но с произвольным заголовком: запуск
 * контейнера, просмотр его логов. Итог (успех/ошибка) приходит от
 * вызывающего: у сессий обновления он читается из /…/status после
 * закрытия сокета, у логов итога нет — окно просто следит.
 */
function CommandLive({
  title,
  description,
  wsPath,
  onClose,
  onFinished,
  outcome,
  extra,
}: {
  title: string
  description?: string
  wsPath: string
  onClose: () => void
  onFinished?: () => void
  /** null/undefined — сессия просто завершена; ok — итог. */
  outcome?: { ok: boolean; exitCode?: number; okText?: string; failText?: string } | null
  /** Дополнительные элементы управления над выводом (переключатели логов). */
  extra?: React.ReactNode
}) {
  const { t } = useTranslation()
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(wsURL(wsPath))

  useEffect(() => {
    start()
    return () => stop()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- один раз на wsPath
  }, [wsPath])

  useEffect(() => {
    if (status === 'closed') onFinished?.()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status])

  function handleClose() {
    stop()
    onClose()
  }

  return (
    <Modal title={title} onClose={handleClose} maskClosable={false} width={860} sizeKey="command">
      {description && <p className="small muted">{description}</p>}
      {extra}
      {status === 'error' && <Banner kind="error">{t('commandModal.connectError')}</Banner>}
      {status === 'connected' && <PtyToolbar onCopy={copySelection} onClear={clear} onFontSize={changeFontSize} onSearch={search} />}
      <div ref={containerRef} className="modal-fill" style={{ height: '45vh', background: '#141414', borderRadius: 'var(--radius-sm)', padding: '0.5rem' }} />
      {status === 'closed' &&
        (outcome === null || outcome === undefined ? (
          <Banner kind="info">{t('packageInstall.sessionEnded')}</Banner>
        ) : outcome.ok ? (
          <Banner kind="info">{outcome.okText ?? t('packageInstall.sessionEnded')}</Banner>
        ) : (
          <Banner kind="error">
            {outcome.failText ??
              t('packageInstall.failed', {
                code: outcome.exitCode !== undefined && outcome.exitCode >= 0 ? t('packageInstall.failedCode', { code: outcome.exitCode }) : '',
              })}
          </Banner>
        ))}
    </Modal>
  )
}

type CommandModalProps = Parameters<typeof CommandLive>[0] & {
  /** Долгая операция (установка, скачивание) — сначала фоновым заданием;
   * логи и консоль остаются живым выводом. */
  asJob?: boolean
}

export default function CommandModal({ asJob, ...props }: CommandModalProps) {
  if (!asJob) return <CommandLive {...props} />
  return <JobFirst path={props.wsPath} title={props.title} onClose={props.onClose} onDone={props.onFinished} live={() => <CommandLive {...props} />} />
}
