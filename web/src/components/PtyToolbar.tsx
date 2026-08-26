import { useState } from 'react'
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
}: {
  onCopy: () => void
  onClear: () => void
  onFontSize: (delta: number) => void
  onSearch: (query: string, backwards?: boolean) => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')

  return (
    <div className="row" style={{ gap: '0.4rem', marginBottom: '0.4rem' }}>
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
    </div>
  )
}
