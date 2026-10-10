import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useVNC } from '../components/VNCModal'
import { useSpice } from '../components/SpiceModal'
import { msg, type Msg } from '../msg'

/**
 * Экран машины на всю страницу: /screen/popout?host=&kind=vm|lxd&name=&proto=vnc|spice.
 * Его открывает мобильное приложение во встроенном браузере — там нет ни
 * меню, ни окна поверх, а у телефона нет ни Esc, ни стрелок, ни
 * физической клавиатуры: панель сверху даёт эти клавиши, а «Клавиатура»
 * открывает экранную, набор с которой уходит в машину.
 */

// Имена машин libvirt и LXD — без пробелов и слэшей; иное в адрес не идёт.
const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/** Клавиши панели: имя KeyboardEvent.code → keysym X11 (для VNC). */
const KEYS: { code: string; label: string; keysym: number }[] = [
  { code: 'Escape', label: 'Esc', keysym: 0xff1b },
  { code: 'Tab', label: 'Tab', keysym: 0xff09 },
  { code: 'ArrowLeft', label: '←', keysym: 0xff51 },
  { code: 'ArrowUp', label: '↑', keysym: 0xff52 },
  { code: 'ArrowDown', label: '↓', keysym: 0xff54 },
  { code: 'ArrowRight', label: '→', keysym: 0xff53 },
  { code: 'Backspace', label: '⌫', keysym: 0xff08 },
  { code: 'Enter', label: '⏎', keysym: 0xff0d },
]
const KEYSYM = Object.fromEntries(KEYS.map((k) => [k.code, k.keysym]))

export default function ScreenPopout() {
  const { t } = useTranslation()
  const q = new URLSearchParams(window.location.search)
  const kind = q.get('kind') === 'lxd' ? 'lxd' : 'vm'
  const name = q.get('name') ?? ''
  const proto = kind === 'lxd' || q.get('proto') === 'spice' ? 'spice' : 'vnc'
  if (!NAME_RE.test(name)) {
    return <div style={{ padding: '1rem' }}>{t('screen.badName')}</div>
  }
  const enc = encodeURIComponent(name)
  const wsPath = kind === 'lxd' ? `/lxd/instances/${enc}/spice/ws` : `/vms/${enc}/${proto}/ws`
  const zoomKey = `nkt-screen-zoom:${q.get('host') ?? ''}:${kind}:${name}`
  return proto === 'vnc' ? <VNCScreen wsPath={wsPath} zoomKey={zoomKey} /> : <SpiceScreen wsPath={wsPath} zoomKey={zoomKey} />
}

/**
 * Масштаб экрана, шаг 10%. Портрет: 50…150%, 100% — картинка целиком
 * вписана в экран. Ландшафт: 50…200%; до 100% — от вписанной целиком,
 * 150% — ровно по ширине экрана, 200% — на треть шире экрана (между
 * ними — плавно). Помнится для каждой машины и ориентации.
 */
const ZOOM_MIN = 50
const zoomMax = (landscape: boolean) => (landscape ? 200 : 150)

function readNum(key: string, min: number, max: number, def: number): number {
  try {
    const v = Number(localStorage.getItem(key))
    return v >= min && v <= max ? v : def
  } catch {
    return def
  }
}

function writeStore(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // без хранилища просто не запомнится
  }
}

/** Множитель к «вписана целиком» для масштаба zoom (%) при размере
 * области box и картинки nat. */
export function zoomFactor(zoom: number, landscape: boolean, box: { w: number; h: number }, nat: { w: number; h: number }): number {
  if (!landscape || zoom <= 100) return zoom / 100
  // Во сколько раз «по ширине» крупнее «целиком» (≥ 1: картинка уже
  // вписана по ширине, если она шире экрана по пропорциям).
  const contain = nat.w && nat.h && box.w && box.h ? Math.min(box.w / nat.w, box.h / nat.h) : 0
  const wr = contain ? Math.max(1, box.w / nat.w / contain) : 1.5
  if (zoom <= 150) return 1 + ((zoom - 100) / 50) * (wr - 1)
  return (wr * zoom) / 150
}

/** Размер элемента (для вписывания). */
function useBoxSize(el: HTMLElement | null): { w: number; h: number } {
  const [size, setSize] = useState({ w: 0, h: 0 })
  useEffect(() => {
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    return () => ro.disconnect()
  }, [el])
  return size
}

/** Ландшафт: окно шире, чем выше. */
function useLandscape(): boolean {
  const [land, setLand] = useState(() => window.innerWidth > window.innerHeight)
  useEffect(() => {
    const on = () => setLand(window.innerWidth > window.innerHeight)
    window.addEventListener('resize', on)
    return () => window.removeEventListener('resize', on)
  }, [])
  return land
}

/** Что ScreenFrame отдаёт экрану: множитель к «вписана целиком» по
 * размеру картинки, режим перемещения, размер области. */
interface ScreenView {
  factor: (nat: { w: number; h: number }) => number
  pan: boolean
  box: { w: number; h: number }
}

/** Размер кадра noVNC — у холста он в атрибутах width/height. */
function useCanvasSize(el: HTMLElement | null, active: boolean): { w: number; h: number } {
  const [size, setSize] = useState({ w: 0, h: 0 })
  useEffect(() => {
    if (!el || !active) return
    const read = () => {
      const c = el.querySelector('canvas')
      if (c && (c.width !== size.w || c.height !== size.h)) setSize({ w: c.width, h: c.height })
    }
    read()
    const id = window.setInterval(read, 1000)
    return () => window.clearInterval(id)
  }, [el, active, size.w, size.h])
  return size
}

function VNCScreen({ wsPath, zoomKey }: { wsPath: string; zoomKey: string }) {
  const { t } = useTranslation()
  const [screen, setScreen] = useState<HTMLDivElement | null>(null)
  const { rfbRef, state, reason, sendCredentials } = useVNC(screen, wsPath)
  const [password, setPassword] = useState('')
  const nat = useCanvasSize(screen, state === 'connected')
  const rfb = () => rfbRef.current
  return (
    <ScreenFrame
      zoomKey={zoomKey}
      state={t(`vnc.state.${state}`)}
      phase={state === 'connected' ? 'ok' : reason ? 'bad' : 'wait'}
      reason={reason}
      connected={state === 'connected'}
      onCtrlAltDel={() => rfb()?.sendCtrlAltDel()}
      onKey={(code) => KEYSYM[code] && rfb()?.sendKey(KEYSYM[code], code)}
      onText={(text) => {
        for (const ch of text) {
          const cp = ch.codePointAt(0) ?? 0
          // keysym: Latin-1 — сам код, остальное Юникода — 0x01000000 + код.
          rfb()?.sendKey(cp === 10 ? 0xff0d : cp <= 0xff ? cp : 0x01000000 + cp, null)
        }
      }}
      extra={
        state === 'password' && (
          <>
            <input type="password" placeholder={t('vnc.password')} value={password} onChange={(e) => setPassword(e.target.value)} style={{ width: '9rem' }} />
            <button onClick={() => sendCredentials(password)}>{t('vnc.send')}</button>
          </>
        )
      }
    >
      {/* noVNC сам вписывает картинку в этот блок: масштаб — его размер. */}
      {(v: ScreenView) => {
        const k = v.factor(nat)
        return (
          <div
            ref={setScreen}
            style={{
              width: v.box.w ? Math.round(v.box.w * k) : '100%',
              height: v.box.h ? Math.round(v.box.h * k) : '100%',
              margin: k < 1 ? '0 auto' : undefined,
              pointerEvents: v.pan ? 'none' : undefined,
            }}
          />
        )
      }}
    </ScreenFrame>
  )
}

function SpiceScreen({ wsPath, zoomKey }: { wsPath: string; zoomKey: string }) {
  const { t } = useTranslation()
  const screenId = 'spice-' + useId().replace(/[^a-zA-Z0-9]/g, '')
  const [screen, setScreen] = useState<HTMLDivElement | null>(null)
  const { connRef, modRef, state, reason } = useSpice(screen, screenId, wsPath)
  const with2 = (f: (m: NonNullable<typeof modRef.current>, c: NonNullable<typeof connRef.current>) => void) => {
    if (modRef.current && connRef.current) f(modRef.current, connRef.current)
  }
  // Картинка SPICE — в родном разрешении машины; вписывается и
  // масштабируется CSS-трансформацией (spice-html5 берёт координаты мыши
  // из offsetX — с учётом трансформации).
  const nat = useBoxSize(screen)
  return (
    <ScreenFrame
      zoomKey={zoomKey}
      state={t(`vnc.state.${state}`)}
      phase={state === 'connected' ? 'ok' : reason ? 'bad' : 'wait'}
      reason={reason}
      connected={state === 'connected'}
      onCtrlAltDel={() => with2((m, c) => m.sendCtrlAltDel(c))}
      onKey={(code) => with2((m, c) => m.pressKey(c, code))}
      onText={(text) => with2((m, c) => void m.typeText(c, text))}
    >
      {(v: ScreenView) => {
        const fit = nat.w && nat.h && v.box.w && v.box.h ? Math.min(v.box.w / nat.w, v.box.h / nat.h) : 1
        const k = fit * v.factor(nat)
        return (
          <div style={{ width: nat.w ? nat.w * k : '100%', height: nat.h ? nat.h * k : '100%', margin: '0 auto', position: 'relative', overflow: 'hidden' }}>
            <div
              id={screenId}
              ref={setScreen}
              tabIndex={0}
              style={{ display: 'inline-block', outline: 'none', transform: `scale(${k})`, transformOrigin: '0 0', pointerEvents: v.pan ? 'none' : undefined }}
            />
          </div>
        )
      }}
    </ScreenFrame>
  )
}

const FAB = 44
const PHASE_COLOR = { ok: '#2ea043', wait: '#d29922', bad: '#f85149' } as const

/** Экран на всё окно; клавиши, масштаб и подсказка — в плавающем меню
 * поверх картинки: круглая кнопка в углу (цвет — состояние подключения),
 * её можно перетащить пальцем, нажатие разворачивает меню. */
function ScreenFrame({
  zoomKey,
  state,
  phase,
  reason,
  connected,
  onCtrlAltDel,
  onKey,
  onText,
  extra,
  children,
}: {
  state: string
  phase: keyof typeof PHASE_COLOR
  reason: Msg | null
  connected: boolean
  onCtrlAltDel: () => void
  onKey: (code: string) => void
  onText: (text: string) => void
  extra?: ReactNode
  zoomKey: string
  children: (v: ScreenView) => ReactNode
}) {
  const { t } = useTranslation()
  const input = useRef<HTMLInputElement | null>(null)
  const handlers = useRef({ onKey, onText })
  handlers.current = { onKey, onText }
  const landscape = useLandscape()
  const key = `${zoomKey}:${landscape ? 'l' : 'p'}`
  const [zooms, setZooms] = useState<Record<string, number>>({})
  const zoom = zooms[key] ?? readNum(key, ZOOM_MIN, zoomMax(landscape), 100)
  const setZoom = (z: number) => {
    const v = Math.min(zoomMax(landscape), Math.max(ZOOM_MIN, z))
    setZooms({ ...zooms, [key]: v })
    writeStore(key, String(v))
  }
  const [pan, setPan] = useState(false)
  const [open, setOpen] = useState(false)
  const [area, setArea] = useState<HTMLDivElement | null>(null)
  const box = useBoxSize(area)

  // Кнопка меню: место запоминается; перетаскивание — больше 6 px.
  const [pos, setPos] = useState(() => {
    try {
      const p = JSON.parse(localStorage.getItem('nkt-screen-fab') ?? 'null') as { x: number; y: number } | null
      if (p && typeof p.x === 'number' && typeof p.y === 'number') return p
    } catch {
      // нет — в правый нижний угол
    }
    return { x: -1, y: -1 }
  })
  const drag = useRef<{ sx: number; sy: number; ox: number; oy: number; moved: boolean } | null>(null)
  const fabX = pos.x < 0 ? window.innerWidth - FAB - 12 : Math.min(pos.x, window.innerWidth - FAB)
  const fabY = pos.y < 0 ? window.innerHeight - FAB - 12 : Math.min(pos.y, window.innerHeight - FAB)

  // Ошибка — полоской сверху на несколько секунд (и в меню).
  const [toast, setToast] = useState<Msg | null>(null)
  useEffect(() => {
    if (!reason) return
    setToast(reason)
    const id = window.setTimeout(() => setToast(null), 6000)
    return () => window.clearTimeout(id)
  }, [reason])

  // Экранная клавиатура пишет в скрытое поле. Поле — type=password: так
  // клавиатура телефона не подсказывает слова и не держит их «в наборе»,
  // а отдаёт символы по одному. Каждый ввод перехватывается до записи в
  // поле и уходит в машину; поле остаётся пустым.
  useEffect(() => {
    const el = input.current
    if (!el) return
    const onBeforeInput = (e: InputEvent) => {
      e.preventDefault()
      if (e.inputType === 'insertText' && e.data) handlers.current.onText(e.data)
      else if (e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph') handlers.current.onKey('Enter')
      else if (e.inputType === 'deleteContentBackward') handlers.current.onKey('Backspace')
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        handlers.current.onKey('Enter')
      }
    }
    el.addEventListener('beforeinput', onBeforeInput)
    el.addEventListener('keydown', onKeyDown)
    return () => {
      el.removeEventListener('beforeinput', onBeforeInput)
      el.removeEventListener('keydown', onKeyDown)
    }
  }, [])

  const btn = { padding: '0.35rem 0.65rem', fontSize: '0.9rem' }
  const factor = (nat: { w: number; h: number }) => zoomFactor(zoom, landscape, box, nat)
  // Меню — над кнопкой, если она в нижней половине, иначе под ней.
  const below = fabY < window.innerHeight / 2
  return (
    <div style={{ position: 'fixed', inset: 0, background: '#000', color: '#ddd' }}>
      <div ref={setArea} style={{ position: 'absolute', inset: 0, overflow: 'auto', touchAction: pan ? 'pan-x pan-y' : 'none' }}>
        {children({ factor, pan, box })}
      </div>
      {toast && (
        <div style={{ position: 'fixed', top: 0, left: 0, right: 0, padding: '0.45rem 0.6rem', background: 'rgba(92,29,29,0.92)', zIndex: 3 }} onClick={() => setToast(null)}>
          {msg(toast)}
        </div>
      )}
      <input
        ref={input}
        type="password"
        autoComplete="off"
        aria-label={t('screen.keyboard')}
        style={{ position: 'fixed', left: 0, top: 0, width: 1, height: 1, opacity: 0, border: 0, padding: 0 }}
      />
      {open && <div style={{ position: 'fixed', inset: 0, zIndex: 4 }} onPointerDown={() => setOpen(false)} />}
      {open && (
        <div
          style={{
            position: 'fixed',
            zIndex: 5,
            left: 8,
            right: 8,
            ...(below ? { top: fabY + FAB + 8 } : { bottom: window.innerHeight - fabY + 8 }),
            maxHeight: below ? window.innerHeight - fabY - FAB - 16 : fabY - 16,
            overflowY: 'auto',
            maxWidth: 560,
            marginLeft: 'auto',
            background: 'rgba(22,24,29,0.94)',
            borderRadius: 12,
            padding: '0.6rem',
            display: 'flex',
            flexDirection: 'column',
            gap: '0.45rem',
          }}
        >
          <span className="small">{state}</span>
          {reason && <span className="small" style={{ color: '#ff9b93' }}>{msg(reason)}</span>}
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.35rem' }}>
            <button style={btn} onClick={() => input.current?.focus()}>
              {t('screen.keyboard')}
            </button>
            {KEYS.map((k) => (
              <button key={k.code} style={btn} disabled={!connected} onClick={() => onKey(k.code)}>
                {k.label}
              </button>
            ))}
            <button style={btn} disabled={!connected} onClick={onCtrlAltDel}>
              Ctrl+Alt+Del
            </button>
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.35rem', alignItems: 'center' }}>
            <button style={btn} disabled={zoom <= ZOOM_MIN} onClick={() => setZoom(zoom - 10)} aria-label={t('screen.zoomOut')}>
              −
            </button>
            <span className="small" style={{ minWidth: '2.8rem', textAlign: 'center' }}>
              {zoom}%
            </span>
            <button style={btn} disabled={zoom >= zoomMax(landscape)} onClick={() => setZoom(zoom + 10)} aria-label={t('screen.zoomIn')}>
              +
            </button>
            <button style={btn} disabled={zoom === 100} onClick={() => setZoom(100)} title={t('screen.zoomFit')} aria-label={t('screen.zoomFit')}>
              ⤢
            </button>
            {landscape && (
              <button style={btn} disabled={zoom === 150} onClick={() => setZoom(150)} title={t('screen.zoomWidth')}>
                ↔
              </button>
            )}
            <button style={{ ...btn, background: pan ? '#2f6feb' : undefined }} onClick={() => setPan(!pan)} title={t('screen.pan')} aria-pressed={pan}>
              {t('screen.panShort')}
            </button>
            {extra}
          </div>
          <span className="small" style={{ color: '#999' }}>
            {t('screen.popoutHint')}
          </span>
        </div>
      )}
      <button
        aria-label={t('screen.menu')}
        aria-expanded={open}
        style={{
          position: 'fixed',
          zIndex: 6,
          left: fabX,
          top: fabY,
          width: FAB,
          height: FAB,
          borderRadius: '50%',
          border: `3px solid ${PHASE_COLOR[phase]}`,
          background: 'rgba(22,24,29,0.55)',
          color: '#fff',
          fontSize: 20,
          lineHeight: 1,
          padding: 0,
          touchAction: 'none',
        }}
        onPointerDown={(e) => {
          drag.current = { sx: e.clientX, sy: e.clientY, ox: fabX, oy: fabY, moved: false }
          ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
        }}
        onPointerMove={(e) => {
          const d = drag.current
          if (!d) return
          const dx = e.clientX - d.sx
          const dy = e.clientY - d.sy
          if (!d.moved && Math.hypot(dx, dy) < 6) return
          d.moved = true
          setPos({ x: Math.max(0, Math.min(window.innerWidth - FAB, d.ox + dx)), y: Math.max(0, Math.min(window.innerHeight - FAB, d.oy + dy)) })
        }}
        onPointerUp={() => {
          const d = drag.current
          drag.current = null
          if (d?.moved) writeStore('nkt-screen-fab', JSON.stringify(pos))
          else setOpen(!open)
        }}
      >
        ☰
      </button>
    </div>
  )
}
