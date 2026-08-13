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
export default function UpdateModal({ packages, onClose }: { packages: PackageUpdate[]; onClose: () => void }) {
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(
    wsURL('/updates/ws'),
  )

  useEffect(() => {
    start()
    // Deliberately once on mount — start() closes over wsURL(), which is
    // stable for the lifetime of this dialog.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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
      {status === 'closed' && <Banner kind="info">Сессия завершена.</Banner>}
    </AntModal>
  )
}
