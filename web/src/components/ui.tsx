import { useRef, type ChangeEvent, type ReactNode } from 'react'
import { Alert, Badge, Button, Card as AntCard, Modal as AntModal, Spin, Tooltip } from 'antd'
import { InfoCircleOutlined } from '@ant-design/icons'
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

/** Tone → the same CSS custom property styles.css's own `.sev-*` classes
 * read, so SeverityBadge/StateBadge stay on the one palette every other
 * still-custom bit of the UI (charts, BlockTree, ...) shares — see
 * theme.ts's own doc comment. Badge's `color` prop accepts any CSS color,
 * and `var(--x)` is one. */
const TONE_COLOR_VAR: Record<'critical' | 'high' | 'medium' | 'low' | 'info' | 'ok', string> = {
  critical: 'var(--status-critical)',
  high: 'var(--status-serious)',
  medium: 'var(--status-warning)',
  low: 'var(--seq-300)',
  info: 'var(--text-muted)',
  ok: 'var(--status-good)',
}

/**
 * Status is never carried by colour alone: every badge pairs its dot with a word.
 */
export function SeverityBadge({ severity }: { severity: Severity }) {
  return <Badge color={TONE_COLOR_VAR[severity]} text={SEVERITY_LABEL[severity]} />
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
  return <Badge color={TONE_COLOR_VAR[tone]} text={state} />
}

/**
 * InfoHint is an "i" icon carrying explanatory text in a tooltip, for
 * static "what is this section for" documentation that used to sit
 * visibly next to a heading (a page's own descriptive paragraph, or a
 * Card's `subtitle` when that subtitle is prose rather than data) — useful
 * once and then just permanent clutter on every later visit. Deliberately
 * NOT for anything the user needs to read at a glance without an extra
 * hover/tap — a live count, a status word, a selected item's own details —
 * those stay as plain visible text (Card's own `subtitle` prop still
 * renders that way unchanged; callers pick per call site which one a given
 * subtitle actually is).
 */
export function InfoHint({ children }: { children: ReactNode }) {
  return (
    <Tooltip title={children}>
      <InfoCircleOutlined
        className="muted"
        style={{ marginLeft: '0.4rem', fontSize: '0.8em', cursor: 'help', verticalAlign: 'middle' }}
        tabIndex={0}
      />
    </Tooltip>
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
    <AntCard
      className={className}
      title={
        title || subtitle ? (
          <div>
            {title && <span>{title}</span>}
            {subtitle && (
              <div style={{ fontWeight: 400, fontSize: '0.8rem', color: 'var(--text-muted)', marginTop: '0.15rem' }}>
                {subtitle}
              </div>
            )}
          </div>
        ) : undefined
      }
      extra={actions && <div className="row">{actions}</div>}
    >
      {children}
    </AntCard>
  )
}

export function Banner({
  kind = 'info',
  icon,
  children,
}: {
  kind?: 'info' | 'warn' | 'error' | 'success'
  icon?: string
  children: ReactNode
}) {
  return (
    <Alert
      type={kind === 'warn' ? 'warning' : kind}
      showIcon
      icon={icon ? <span aria-hidden="true">{icon}</span> : undefined}
      message={children}
    />
  )
}

export function Loading({ what = 'данные' }: { what?: string }) {
  return (
    <div className="chart-empty">
      <Spin size="small" /> Загружаю {what}…
    </div>
  )
}

/** Inline busy indicator for a button mid-action — a long operation (certbot,
 * service restart, config validation) needs more than disabled+text, since
 * that alone reads the same as "nothing is happening yet". */
export function Spinner() {
  return <Spin size="small" />
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
    <AntModal
      title={title}
      open
      closable={false}
      maskClosable={!!onClose}
      onCancel={onClose}
      footer={onClose ? <Button onClick={onClose}>{closeLabel}</Button> : null}
      destroyOnHidden
    >
      {children}
    </AntModal>
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
