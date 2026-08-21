import type { ReactNode } from 'react'
import { Button, Tag, Tooltip } from 'antd'

export interface InactiveSummaryProps<T> {
  items: T[]
  getKey: (item: T) => string
  getLabel: (item: T) => string
  getTooltip: (item: T) => ReactNode
  onRescan: () => void
  rescanning: boolean
}

/**
 * A compact "N неактивны" chip row for lists that otherwise bury stopped/
 * inactive entries among running ones — pulled out of the main table and
 * shortened to just a name here (full state/detail on hover instead), so
 * the main table only shows what's actually active. Comes with its own
 * rescan button: once something's flagged inactive, "is that still true
 * right now" is the obvious next question, and this is the one place an
 * operator is looking specifically because of it.
 */
export function InactiveSummary<T>({ items, getKey, getLabel, getTooltip, onRescan, rescanning }: InactiveSummaryProps<T>) {
  if (items.length === 0) return null
  return (
    <div className="row" style={{ alignItems: 'center', flexWrap: 'wrap', gap: '0.4rem', marginBottom: '0.75rem' }}>
      <span className="small muted">неактивны ({items.length}):</span>
      {items.map((item) => (
        <Tooltip key={getKey(item)} title={getTooltip(item)}>
          <Tag style={{ cursor: 'default' }}>{getLabel(item)}</Tag>
        </Tooltip>
      ))}
      <Button size="small" onClick={onRescan} loading={rescanning} style={{ marginLeft: 'auto' }}>
        {rescanning ? 'Сканирую…' : 'Пересканировать'}
      </Button>
    </div>
  )
}
