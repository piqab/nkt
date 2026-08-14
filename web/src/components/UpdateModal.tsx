import { useEffect } from 'react'
import { Modal as AntModal, Button } from 'antd'
import type { PackageUpdate } from '../types'
import { Banner } from './ui'
import { PtyToolbar } from './PtyToolbar'
import { usePty, wsURL } from '../hooks/usePty'

/**
 * Runs `apt-get update && apt-get upgrade` on this host live — deliberately
 * without -y, so apt's own "Do you want to continue? [Y/n]" (and anything
 * else it needs to ask) is answered by the operator watching the real
 * output, not decided unattended. Connects automatically on open: the
 * confirmation already happened one step earlier (the "обновить" button on
 * Overview), this dialog IS the thing that was confirmed, not another gate
 * in front of it.
 */
export default function UpdateModal({
  packages,
  onClose,
  onFinished,
  outcome,
  rescanning,
}: {
  packages: PackageUpdate[]
  onClose: () => void
  onFinished?: () => void
  /** How the run ended, once the caller has resolved it — null while it
   * is still going or not yet known. */
  outcome?: { ok: boolean; exitCode?: number } | null
  /** The caller is rescanning the host after a successful run, so the
   * package list it shows is about to change. */
  rescanning?: boolean
}) {
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(
    wsURL('/updates/ws'),
  )

  useEffect(() => {
    start()
    // Deliberately once on mount — start() closes over wsURL(), which is
    // stable for the lifetime of this dialog.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // The server closes the socket the moment the run ends (see
  // runUpdateSession), so this fires as soon as apt is actually done —
  // no waiting on the next /updates/status poll to notice.
  useEffect(() => {
    if (status === 'closed') onFinished?.()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status])

  function handleClose() {
    stop()
    onClose()
  }

  return (
    <AntModal
      title="Обновление пакетов"
      open
      onCancel={handleClose}
      width={760}
      footer={<Button onClick={handleClose}>Закрыть</Button>}
      destroyOnHidden
    >
      <p className="small muted">
        Будет предложено обновить: {packages.map((p) => p.name).join(', ')}. Подтверждение
        (<code className="mono">Y/n</code>) — прямо в окне ниже, как в обычном терминале.
      </p>
      {status === 'error' && (
        <Banner kind="error">Не удалось подключиться к сессии обновления.</Banner>
      )}
      {status === 'connected' && (
        <PtyToolbar onCopy={copySelection} onClear={clear} onFontSize={changeFontSize} onSearch={search} />
      )}
      <div
        ref={containerRef}
        style={{
          height: '50vh',
          background: '#141414',
          borderRadius: 'var(--radius-sm)',
          padding: '0.5rem',
        }}
      />
      {status === 'closed' &&
        (outcome === null || outcome === undefined ? (
          <Banner kind="info">Сессия завершена.</Banner>
        ) : outcome.ok ? (
          <Banner kind="info">
            Обновление завершено успешно.{' '}
            {rescanning ? 'Пересканирую хост…' : 'Хост пересканирован — список пакетов уже обновлён.'}
          </Banner>
        ) : (
          <Banner kind="error">
            Обновление завершилось с ошибкой
            {outcome.exitCode !== undefined && outcome.exitCode >= 0 ? ` (код ${outcome.exitCode})` : ''}. Что
            именно пошло не так — в выводе выше.
          </Banner>
        ))}
    </AntModal>
  )
}
