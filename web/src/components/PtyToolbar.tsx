import { useEffect, useState } from 'react'
import { Button, Input, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'

/**
 * Shared action row for anything built on usePty (Terminal.tsx and
 * UpdateModal.tsx) — copy the current selection, clear the scrollback,
 * bump the font size, or jump to the next/previous match of a search term
 * in the buffer (SearchAddon).
 */
export function PtyToolbar({
  onCopy,
  onClear,
  onFontSize,
  onSearch,
  getIdleRemainingMs,
}: {
  onCopy: () => void
  onClear: () => void
  onFontSize: (delta: number) => void
  onSearch: (query: string, backwards?: boolean) => void
  // Optional: only Terminal.tsx passes this (see its own comment on why
  // the countdown is not shown from the shorter-lived update/install
  // modals) — omitted entirely, IdleCountdown renders nothing.
  getIdleRemainingMs?: () => number | null
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')

  return (
    <div className="row" style={{ gap: '0.4rem', marginBottom: '0.4rem', alignItems: 'center' }}>
      <Tooltip title={t('ptyToolbar.copySelection')}>
        <Button size="small" onClick={onCopy}>
          {t('ptyToolbar.copy')}
        </Button>
      </Tooltip>
      <Tooltip title={t('ptyToolbar.clearBuffer')}>
        <Button size="small" onClick={onClear}>
          {t('ptyToolbar.clear')}
        </Button>
      </Tooltip>
      <Button size="small" onClick={() => onFontSize(-1)} aria-label={t('ptyToolbar.decreaseFont')}>
        A−
      </Button>
      <Button size="small" onClick={() => onFontSize(1)} aria-label={t('ptyToolbar.increaseFont')}>
        A+
      </Button>
      <Input
        size="small"
        placeholder={t('ptyToolbar.searchPlaceholder')}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onPressEnter={(e) => onSearch(query, e.shiftKey)}
        style={{ maxWidth: '14rem' }}
      />
      {getIdleRemainingMs && <IdleCountdown getIdleRemainingMs={getIdleRemainingMs} />}
    </div>
  )
}

/** mm:ss, floored — a live "0:37" is more useful right before it matters
 * than a rounded value that would still say "0:01" one tick before the
 * session actually ends. */
function formatRemaining(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

// Below this, the countdown switches to a warning colour — long enough to
// still be useful (send one more keystroke to reset it) rather than a
// last-second surprise.
const IDLE_WARNING_MS = 60_000

/**
 * Live countdown to the same disconnect the server's own idle timer
 * enforces (see usePty's getIdleRemainingMs and pty_session.go's
 * resetIdle) — polls on its own 1s interval rather than being driven by
 * usePty's state, so the rest of the terminal page (in particular the
 * xterm container) doesn't re-render every second just for this.
 */
function IdleCountdown({ getIdleRemainingMs }: { getIdleRemainingMs: () => number | null }) {
  const { t } = useTranslation()
  const [remainingMs, setRemainingMs] = useState(getIdleRemainingMs)

  useEffect(() => {
    setRemainingMs(getIdleRemainingMs())
    const id = window.setInterval(() => setRemainingMs(getIdleRemainingMs()), 1000)
    return () => window.clearInterval(id)
  }, [getIdleRemainingMs])

  if (remainingMs === null) return null

  return (
    <Tooltip title={t('ptyToolbar.idleCountdownTooltip')}>
      <span
        className={remainingMs <= IDLE_WARNING_MS ? 'small mono' : 'small mono muted'}
        style={{ marginLeft: 'auto', color: remainingMs <= IDLE_WARNING_MS ? 'var(--status-critical)' : undefined }}
      >
        {t('ptyToolbar.idleCountdown', { time: formatRemaining(remainingMs) })}
      </span>
    </Tooltip>
  )
}
