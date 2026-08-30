import type { ReactNode } from 'react'
import { Button, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'

export interface InactiveSummaryProps<T> {
  items: T[]
  getKey: (item: T) => string
  getLabel: (item: T) => string
  getTooltip: (item: T) => ReactNode
  onRescan: () => void
  rescanning: boolean
  // Both optional and independent of each other — every existing caller
  // (LXD/Docker/Podman/Virtualization) leaves both unset, keeping their
  // chips exactly as purely informational as before. Services.tsx is the
  // only caller that passes onItemClick, and isClickable alongside it so
  // only the subset it actually means to act on (not-installed services)
  // gets the pointer cursor — onItemClick without isClickable would make
  // every chip clickable, including ones merely stopped-but-installed.
  onItemClick?: (item: T) => void
  isClickable?: (item: T) => boolean
  // Optional antd Tag color — lets a caller (Services.tsx) visually tell
  // "not installed at all" apart from "installed but just stopped", two
  // states this same chip row used to render identically. Omitted keeps
  // every other caller's plain default-gray chip exactly as before.
  getColor?: (item: T) => string | undefined
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
export function InactiveSummary<T>({
  items,
  getKey,
  getLabel,
  getTooltip,
  onRescan,
  rescanning,
  onItemClick,
  isClickable,
  getColor,
}: InactiveSummaryProps<T>) {
  const { t } = useTranslation()
  if (items.length === 0) return null
  return (
    <div className="row" style={{ alignItems: 'center', flexWrap: 'wrap', gap: '0.4rem', marginBottom: '0.75rem' }}>
      <span className="small muted">{t('common.inactive', { count: items.length })}</span>
      {items.map((item) => {
        const clickable = !!onItemClick && (isClickable ? isClickable(item) : true)
        return (
          <Tooltip key={getKey(item)} title={getTooltip(item)}>
            <Tag
              color={getColor?.(item)}
              style={{ cursor: clickable ? 'pointer' : 'default' }}
              onClick={clickable ? () => onItemClick!(item) : undefined}
            >
              {getLabel(item)}
            </Tag>
          </Tooltip>
        )
      })}
      <Button size="small" onClick={onRescan} loading={rescanning} style={{ marginLeft: 'auto' }}>
        {rescanning ? t('common.scanning') : t('common.rescan')}
      </Button>
    </div>
  )
}
