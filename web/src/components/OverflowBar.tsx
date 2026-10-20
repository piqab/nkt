import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from 'react'
import { Badge, Button, Popover } from 'antd'
import { MenuOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'

// Панель кнопок, переключателей и галочек, которая в узком окне не
// переносится на две-три строки, а прячет второстепенное в «☰». Решает
// ширина самой панели (не окна — боковое меню бывает раскрыто): скрытая
// копия всех элементов в одну строку меряет, сколько им нужно; не
// помещается — снаружи остаются только keep, остальное — в меню. Галочки
// и переключатели в меню работают на месте, меню при этом не закрывается.

export interface BarItem {
  key: string
  node: ReactNode
  /** Всегда снаружи. */
  keep?: boolean
  /** Вид в меню, если отличается (например, кнопка во всю ширину). */
  menu?: ReactNode
  /** Включённый фильтр: спрятанный в меню — точка на «☰». */
  active?: boolean
}

/** Ширина элемента, следит за изменениями. */
export function useWidth(ref: RefObject<HTMLElement | null>): number {
  const [w, setW] = useState(0)
  const watched = useRef<{ el: HTMLElement; ro: ResizeObserver } | null>(null)
  // Элемент может появиться позже (таблица — после загрузки данных):
  // проверка на каждой отрисовке, наблюдатель — один на элемент.
  useLayoutEffect(() => {
    const el = ref.current
    if (watched.current?.el === el) return
    watched.current?.ro.disconnect()
    watched.current = null
    if (!el) return
    setW(el.clientWidth)
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    watched.current = { el, ro }
  })
  useEffect(() => () => watched.current?.ro.disconnect(), [])
  return w
}

export function OverflowBar({
  items,
  gap = '0.5rem',
  style,
  label,
  align = 'start',
}: {
  items: BarItem[]
  gap?: string
  style?: CSSProperties
  /** Подпись кнопки меню (по умолчанию — только «☰»). */
  label?: ReactNode
  /** end — элементы прижаты вправо (шапка карточки или раздела). */
  align?: 'start' | 'end'
}) {
  const { t } = useTranslation()
  const boxRef = useRef<HTMLDivElement>(null)
  const probeRef = useRef<HTMLDivElement>(null)
  const width = useWidth(boxRef)
  const [need, setNeed] = useState(0)
  // Сколько нужно всем элементам в одну строку — по скрытой копии (её
  // содержимое меняется: подписи, счётчики).
  useEffect(() => {
    const el = probeRef.current
    if (!el) return
    setNeed(el.scrollWidth)
    const ro = new ResizeObserver(() => setNeed(el.scrollWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const compact = width > 0 && need > width + 1
  const hidden = compact ? items.filter((i) => !i.keep) : []
  const shown = compact ? items.filter((i) => i.keep) : items
  const row: CSSProperties = { display: 'flex', alignItems: 'center', gap, justifyContent: align === 'end' ? 'flex-end' : 'flex-start' }
  return (
    <div ref={boxRef} style={{ position: 'relative', minWidth: 0, flex: '1 1 auto', ...style }}>
      {/* Мерка: все элементы в одну строку, невидимо и без фокуса. */}
      <div
        ref={probeRef}
        aria-hidden
        {...{ inert: '' }}
        style={{ ...row, position: 'absolute', visibility: 'hidden', pointerEvents: 'none', whiteSpace: 'nowrap', width: 'max-content', top: 0, left: 0, height: 0, overflow: 'hidden' }}
      >
        {items.map((i) => (
          <span key={i.key} style={{ flex: '0 0 auto' }}>
            {i.node}
          </span>
        ))}
        {/* место под «☰» не нужно: в полном виде его нет */}
      </div>
      <div style={{ ...row, flexWrap: 'wrap' }}>
        {shown.map((i) => (
          <span key={i.key} style={{ display: 'inline-flex', alignItems: 'center', minWidth: 0 }}>
            {i.node}
          </span>
        ))}
        {hidden.length > 0 && (
          <Popover
            trigger="click"
            placement="bottomRight"
            content={
              <div className="col" style={{ gap: '0.45rem', alignItems: 'flex-start', maxWidth: 'min(90vw, 360px)' }}>
                {hidden.map((i) => (
                  <div key={i.key}>{i.menu ?? i.node}</div>
                ))}
              </div>
            }
          >
            <Badge dot={hidden.some((i) => i.active)} offset={[-2, 2]}>
              <Button size="small" icon={<MenuOutlined />} aria-label={t('common.moreActions')}>
                {label}
              </Button>
            </Badge>
          </Popover>
        )}
      </div>
    </div>
  )
}
