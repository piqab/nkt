import { useEffect } from 'react'
import { Modal as AntModal, Button } from 'antd'
import { Banner } from './ui'
import { PtyToolbar } from './PtyToolbar'
import { usePty, wsURL } from '../hooks/usePty'

/**
 * Runs `apt-get install -y <packageName>` live, the same PTY/WebSocket
 * bridge UpdateModal uses for package upgrades — modeled on it directly,
 * just without the "confirm what apt is about to do" framing an upgrade
 * needs: installing one known package from the distro's own repos has no
 * judgment call attached to it the way "which services restart after this
 * upgrade" does, so -y is safe and there's nothing to prompt the operator
 * for. One component for every such install button (ufw, firewalld, ...)
 * — they differ only in which package and which session/ws path.
 */
export default function PackageInstallModal({
  packageName,
  wsPath,
  onClose,
  onFinished,
  outcome,
  rescanning,
}: {
  packageName: string
  wsPath: string
  onClose: () => void
  onFinished?: () => void
  outcome?: { ok: boolean; exitCode?: number } | null
  rescanning?: boolean
}) {
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(wsURL(wsPath))

  useEffect(() => {
    start()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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
      title={`Установка ${packageName}`}
      open
      onCancel={handleClose}
      width={760}
      footer={<Button onClick={handleClose}>Закрыть</Button>}
      destroyOnHidden
    >
      <p className="small muted">
        Выполняется <code className="mono">apt-get install -y {packageName}</code>.
      </p>
      {status === 'error' && <Banner kind="error">Не удалось подключиться к сессии установки.</Banner>}
      {status === 'connected' && (
        <PtyToolbar onCopy={copySelection} onClear={clear} onFontSize={changeFontSize} onSearch={search} />
      )}
      <div
        ref={containerRef}
        style={{
          height: '40vh',
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
            {packageName} установлен. {rescanning ? 'Пересканирую хост…' : 'Хост пересканирован.'}
          </Banner>
        ) : (
          <Banner kind="error">
            Установка завершилась с ошибкой
            {outcome.exitCode !== undefined && outcome.exitCode >= 0 ? ` (код ${outcome.exitCode})` : ''}. Что
            именно пошло не так — в выводе выше.
          </Banner>
        ))}
    </AntModal>
  )
}
