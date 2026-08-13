import { useState } from 'react'
import { Button, Input, Tooltip } from 'antd'

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
  const [query, setQuery] = useState('')

  return (
    <div className="row" style={{ gap: '0.4rem', marginBottom: '0.4rem' }}>
      <Tooltip title="Скопировать выделенное">
        <Button size="small" onClick={onCopy}>
          копировать
        </Button>
      </Tooltip>
      <Tooltip title="Очистить видимый буфер">
        <Button size="small" onClick={onClear}>
          очистить
        </Button>
      </Tooltip>
      <Button size="small" onClick={() => onFontSize(-1)} aria-label="уменьшить шрифт">
        A−
      </Button>
      <Button size="small" onClick={() => onFontSize(1)} aria-label="увеличить шрифт">
        A+
      </Button>
      <Input
        size="small"
        placeholder="найти в выводе… (Enter / Shift+Enter)"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onPressEnter={(e) => onSearch(query, e.shiftKey)}
        style={{ maxWidth: '14rem' }}
      />
    </div>
  )
}
