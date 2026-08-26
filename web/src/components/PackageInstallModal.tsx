import { useEffect } from 'react'
import { Modal as AntModal, Button } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
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
  const { t } = useTranslation()
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
      title={t('packageInstall.title', { packageName })}
      open
      onCancel={handleClose}
      width={760}
      footer={<Button onClick={handleClose}>{t('common.close')}</Button>}
      destroyOnHidden
    >
      <p className="small muted">
        <Trans i18nKey="packageInstall.running" values={{ packageName }} components={{ code: <code className="mono" /> }} />
      </p>
      {status === 'error' && <Banner kind="error">{t('packageInstall.connectError')}</Banner>}
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
          <Banner kind="info">{t('packageInstall.sessionEnded')}</Banner>
        ) : outcome.ok ? (
          <Banner kind="info">
            {t('packageInstall.installed', {
              packageName,
              status: rescanning ? t('packageInstall.rescanning') : t('packageInstall.rescanned'),
            })}
          </Banner>
        ) : (
          <Banner kind="error">
            {t('packageInstall.failed', {
              code:
                outcome.exitCode !== undefined && outcome.exitCode >= 0
                  ? t('packageInstall.failedCode', { code: outcome.exitCode })
                  : '',
            })}
          </Banner>
        ))}
    </AntModal>
  )
}
