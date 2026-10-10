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

/** Масштаб экрана: 100% — вписан в окно; 50…150%, шаг 10. */
const ZOOM_MIN = 50
const ZOOM_MAX = 150

function readZoom(key: string): number {
  try {
    const v = Number(localStorage.getItem(key))
    return v >= ZOOM_MIN && v <= ZOOM_MAX ? v : 100
  } catch {
    return 100
  }
}

/** Размер области экрана (для вписывания). */
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

/** Что ScreenFrame отдаёт экрану: масштаб, режим перемещения, размер области. */
interface ScreenView {
  zoom: number
  pan: boolean
  box: { w: number; h: number }
}

function VNCScreen({ wsPath, zoomKey }: { wsPath: string; zoomKey: string }) {
  const { t } = useTranslation()
  const [screen, setScreen] = useState<HTMLDivElement | null>(null)
  const { rfbRef, state, reason, sendCredentials } = useVNC(screen, wsPath)
  const [password, setPassword] = useState('')
  const rfb = () => rfbRef.current
  return (
    <ScreenFrame
      zoomKey={zoomKey}
      state={t(`vnc.state.${state}`)}
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
      {(v: ScreenView) => (
        <div
          ref={setScreen}
          style={{
            width: v.box.w ? Math.round((v.box.w * v.zoom) / 100) : '100%',
            height: v.box.h ? Math.round((v.box.h * v.zoom) / 100) : '100%',
            margin: v.zoom < 100 ? '0 auto' : undefined,
            pointerEvents: v.pan ? 'none' : undefined,
          }}
        />
      )}
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
      reason={reason}
      connected={state === 'connected'}
      onCtrlAltDel={() => with2((m, c) => m.sendCtrlAltDel(c))}
      onKey={(code) => with2((m, c) => m.pressKey(c, code))}
      onText={(text) => with2((m, c) => void m.typeText(c, text))}
    >
      {(v: ScreenView) => {
        const fit = nat.w && nat.h && v.box.w && v.box.h ? Math.min(v.box.w / nat.w, v.box.h / nat.h) : 1
        const k = (fit * v.zoom) / 100
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

/** Панель клавиш сверху, экран — на всё остальное. */
function ScreenFrame({
  zoomKey,
  state,
  reason,
  connected,
  onCtrlAltDel,
  onKey,
  onText,
  extra,
  children,
}: {
  state: string
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
  const [zoom, setZoomState] = useState(() => readZoom(zoomKey))
  const [pan, setPan] = useState(false)
  const [area, setArea] = useState<HTMLDivElement | null>(null)
  const box = useBoxSize(area)
  const setZoom = (z: number) => {
    const v = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z))
    setZoomState(v)
    try {
      localStorage.setItem(zoomKey, String(v))
    } catch {
      // без хранилища масштаб просто не запомнится
    }
  }
  const input = useRef<HTMLInputElement | null>(null)
  const handlers = useRef({ onKey, onText })
  handlers.current = { onKey, onText }

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

  const btn = { padding: '0.25rem 0.6rem', fontSize: '0.85rem' }
  return (
    <div style={{ position: 'fixed', inset: 0, display: 'flex', flexDirection: 'column', background: '#000', color: '#ddd' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.3rem', alignItems: 'center', padding: '0.3rem', background: '#16181d' }}>
        <span className="small" style={{ marginRight: '0.3rem' }}>{state}</span>
        <button style={btn} onClick={() => input.current?.focus()}>{t('screen.keyboard')}</button>
        {KEYS.map((k) => (
          <button key={k.code} style={btn} disabled={!connected} onClick={() => onKey(k.code)}>
            {k.label}
          </button>
        ))}
        <button style={btn} disabled={!connected} onClick={onCtrlAltDel}>
          Ctrl+Alt+Del
        </button>
        <span style={{ display: 'inline-flex', gap: '0.2rem', alignItems: 'center', marginLeft: '0.3rem' }}>
          <button style={btn} disabled={zoom <= ZOOM_MIN} onClick={() => setZoom(zoom - 10)} aria-label={t('screen.zoomOut')}>
            −
          </button>
          <span className="small" style={{ minWidth: '2.8rem', textAlign: 'center' }}>
            {zoom}%
          </span>
          <button style={btn} disabled={zoom >= ZOOM_MAX} onClick={() => setZoom(zoom + 10)} aria-label={t('screen.zoomIn')}>
            +
          </button>
          <button style={btn} disabled={zoom === 100} onClick={() => setZoom(100)} title={t('screen.zoomFit')} aria-label={t('screen.zoomFit')}>
            ⤢
          </button>
          <button style={{ ...btn, background: pan ? '#2f6feb' : undefined }} onClick={() => setPan(!pan)} title={t('screen.pan')} aria-pressed={pan}>
            {t('screen.panShort')}
          </button>
        </span>
        {extra}
        <input
          ref={input}
          type="password"
          autoComplete="off"
          aria-label={t('screen.keyboard')}
          style={{ position: 'absolute', left: 0, top: 0, width: 1, height: 1, opacity: 0, border: 0, padding: 0 }}
        />
      </div>
      {reason && <div style={{ padding: '0.4rem', background: '#5c1d1d' }}>{msg(reason)}</div>}
      <div ref={setArea} style={{ flex: 1, minHeight: 0, overflow: zoom > 100 ? 'auto' : 'hidden', touchAction: pan ? 'pan-x pan-y' : 'none' }}>
        {children({ zoom, pan, box })}
      </div>
      <div className="small" style={{ padding: '0.2rem 0.4rem', color: '#999' }}>{t('screen.popoutHint')}</div>
    </div>
  )
}
