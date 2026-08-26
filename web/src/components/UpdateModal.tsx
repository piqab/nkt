import { useEffect } from 'react'
import { Modal as AntModal, Button } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import type { PackageUpdate } from '../types'
import { Banner } from './ui'
import { PtyToolbar } from './PtyToolbar'
import { usePty, wsURL } from '../hooks/usePty'

/**
 * Runs `apt-get update && apt-get dist-upgrade` on this host live —
 * deliberately without -y, so apt's own "Do you want to continue? [Y/n]"
 * (and anything else it needs to ask, including which packages it wants to
 * add/remove to resolve a dependency change) is answered by the operator
 * watching the real output, not decided unattended. Connects automatically
 * on open: the confirmation already happened one step earlier (the
 * "обновить" button on Overview), this dialog IS the thing that was
 * confirmed, not another gate in front of it.
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
  const { t } = useTranslation()
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
      title={t('updateModal.title')}
      open
      onCancel={handleClose}
      width={760}
      footer={<Button onClick={handleClose}>{t('common.close')}</Button>}
      destroyOnHidden
    >
      <p className="small muted">
        <Trans
          i18nKey="updateModal.willOffer"
          values={{ names: packages.map((p) => p.name).join(', ') }}
          components={{ code: <code className="mono" /> }}
        />
      </p>
      {status === 'error' && <Banner kind="error">{t('updateModal.connectError')}</Banner>}
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
          <Banner kind="info">{t('updateModal.sessionEnded')}</Banner>
        ) : outcome.ok ? (
          <Banner kind="info">
            {t('updateModal.succeeded', {
              status: rescanning ? t('updateModal.rescanning') : t('updateModal.rescanned'),
            })}
          </Banner>
        ) : (
          <Banner kind="error">
            {t('updateModal.failed', {
              code:
                outcome.exitCode !== undefined && outcome.exitCode >= 0
                  ? t('updateModal.failedCode', { code: outcome.exitCode })
                  : '',
            })}
          </Banner>
        ))}
    </AntModal>
  )
}
