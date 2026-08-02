import { useRef, type ChangeEvent, type ReactNode } from 'react'
import type { Severity } from '../types'

export const SEVERITY_LABEL: Record<Severity, string> = {
  critical: 'критично',
  high: 'высокая',
  medium: 'средняя',
  low: 'низкая',
  info: 'инфо',
}

const SEVERITY_ORDER: Severity[] = ['critical', 'high', 'medium', 'low', 'info']
export const SEVERITIES = SEVERITY_ORDER

/**
 * Status is never carried by colour alone: every badge pairs its dot with a word.
 */
export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={`badge sev-${severity}`}>
      <span className="badge-dot" />
      {SEVERITY_LABEL[severity]}
    </span>
  )
}

export function StateBadge({ state }: { state: string }) {
  const tone =
    state === 'active' || state === 'running'
      ? 'ok'
      : state === 'failed' || state === 'restarting' || state === 'dead'
        ? 'critical'
        : state === 'inactive' || state === 'exited' || state === 'declared'
          ? 'medium'
          : 'info'
  return (
    <span className={`badge sev-${tone}`}>
      <span className="badge-dot" />
      {state}
    </span>
  )
}

export function Card({
  title,
  subtitle,
  actions,
  children,
  className,
}: {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={`card${className ? ` ${className}` : ''}`}>
      {(title || actions) && (
        <div className="card-head">
          <div>
            {title && <h2>{title}</h2>}
            {subtitle && <p>{subtitle}</p>}
          </div>
          {actions && <div className="row">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  )
}

export function Banner({
  kind = 'info',
  icon,
  children,
}: {
  kind?: 'info' | 'warn' | 'error'
  icon?: string
  children: ReactNode
}) {
  const defaultIcon = kind === 'error' ? '✖' : kind === 'warn' ? '▲' : 'ℹ'
  return (
    <div className={`banner ${kind}`} role={kind === 'error' ? 'alert' : undefined}>
      <span className="banner-icon" aria-hidden="true">
        {icon ?? defaultIcon}
      </span>
      <div>{children}</div>
    </div>
  )
}

export function Loading({ what = 'данные' }: { what?: string }) {
  return <div className="chart-empty">Загружаю {what}…</div>
}

/** Inline busy indicator for a button mid-action — a long operation (certbot,
 * service restart, config validation) needs more than disabled+text, since
 * that alone reads the same as "nothing is happening yet". */
export function Spinner() {
  return <span className="spinner" aria-hidden="true" />
}

/**
 * A dismissible window over the page, for showing something that outlives a
 * single request — a background job's live progress, in particular. Closing
 * it does not cancel whatever it was watching: the job (e.g. a certbot
 * renewal already underway on the host) keeps running regardless.
 */
export function Modal({
  title,
  onClose,
  closeLabel = 'Закрыть',
  children,
}: {
  title: string
  onClose?: () => void
  closeLabel?: string
  children: ReactNode
}) {
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2>{title}</h2>
          {onClose && (
            <button className="ghost" onClick={onClose}>
              {closeLabel}
            </button>
          )}
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}

/**
 * A plain textarea with a synced line-number gutter — every editable config
 * window (whole-file editor, new-file form, block create/edit) uses this
 * instead of a bare `<textarea>` so a line a validator error or a block's
 * start/end line refers to can actually be found by eye.
 *
 * Wrapping is turned off deliberately: with wrapping on, one array index
 * from split("\n") can span several visual rows and the numbers stop lining
 * up with the text. A long line scrolls horizontally instead.
 */
export function CodeEditor({
  value,
  onChange,
  rows = 16,
  readOnly,
  autoFocus,
}: {
  value: string
  onChange?: (e: ChangeEvent<HTMLTextAreaElement>) => void
  rows?: number
  readOnly?: boolean
  autoFocus?: boolean
}) {
  const gutterRef = useRef<HTMLDivElement>(null)
  const lineCount = value === '' ? 1 : value.split('\n').length

  return (
    <div className="code-editor">
      <div className="code-gutter" ref={gutterRef} aria-hidden="true">
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <textarea
        className="code-textarea"
        value={value}
        onChange={onChange}
        onScroll={(e) => {
          if (gutterRef.current) gutterRef.current.scrollTop = e.currentTarget.scrollTop
        }}
        rows={rows}
        spellCheck={false}
        readOnly={readOnly}
        autoFocus={autoFocus}
        wrap="off"
      />
    </div>
  )
}

export function ErrorNote({ error }: { error: string | null }) {
  if (!error) return null
  return (
    <Banner kind="error">
      <strong>Не удалось выполнить запрос.</strong> {error}
    </Banner>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="chart-empty">{children}</div>
}

export function formatDateTime(iso: string | undefined | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'medium' })
}

export function formatRelative(iso: string | undefined | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const diff = (Date.now() - d.getTime()) / 1000
  if (diff < 60) return 'только что'
  if (diff < 3600) return `${Math.floor(diff / 60)} мин назад`
  if (diff < 86400) return `${Math.floor(diff / 3600)} ч назад`
  return `${Math.floor(diff / 86400)} дн назад`
}

export function formatBytesShort(n: number): string {
  if (!n) return '0'
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
