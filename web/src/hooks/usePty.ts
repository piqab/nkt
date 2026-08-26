import { useCallback, useEffect, useRef, useState } from 'react'
import { Terminal as XTerm, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { CanvasAddon } from '@xterm/addon-canvas'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { hostScope, LOCAL_HOST_ID } from '../api'
import i18n from '../i18n'

export type PtyStatus = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'

const MIN_FONT_SIZE = 9
const MAX_FONT_SIZE = 22

// Mirrors styles.css's --mono token as a literal font stack — xterm.js
// passes this straight into a Canvas 2D context's `font` property (via
// CanvasAddon) to measure and draw glyphs, and unlike a DOM element's
// style, ctx.font does not resolve CSS custom properties: a bare
// `var(--mono)` silently falls back to the canvas default (usually a
// proportional font), which is why glyph spacing looked wrong.
const MONO_FONT_STACK = "ui-monospace, SFMono-Regular, 'Cascadia Mono', Consolas, monospace"

/** Dark terminal palette built from the app's own categorical chart colours
 * (styles.css's --series-N tokens) rather than a generic scheme, so ANSI
 * colour output (ls --color, git status, apt, prompts) reads as part of the
 * same product instead of an unrelated embedded widget. */
const THEME: ITheme = {
  background: '#161616',
  foreground: '#e8e6e1',
  cursor: '#e8e6e1',
  cursorAccent: '#161616',
  selectionBackground: 'rgba(255, 255, 255, 0.28)',
  black: '#161616',
  red: '#e34948', // --series-8
  green: '#1baf7a', // --series-3
  yellow: '#eda100', // --series-4
  blue: '#2a78d6', // --series-1
  magenta: '#e87ba4', // --series-5
  cyan: '#3a9aa0',
  white: '#e8e6e1',
  brightBlack: '#6b6b68',
  brightRed: '#ff7472',
  brightGreen: '#3fd6a0',
  brightYellow: '#ffc247',
  brightBlue: '#5b9bf0',
  brightMagenta: '#f5a3c5',
  brightCyan: '#63c2c8',
  brightWhite: '#ffffff',
}

/**
 * Wires an xterm.js terminal to a PTY-over-WebSocket endpoint (see
 * internal/api/pty_session.go: binary frames carry raw I/O, resize goes out
 * as a JSON text frame). Shared by the general terminal page and the
 * package-update dialog — both stream a live interactive PTY session the
 * same way and differ only in the WebSocket URL and the UI around it.
 *
 * Pinned to @xterm/xterm 5.5.0, not the newer 6.x line: v6 rewrote its
 * rendering internals wholesale (adopted VS Code's own render platform,
 * dropped the canvas renderer entirely) mere months before this was
 * written, and none of the addons below have caught up to it yet (their
 * package.json peer deps still say ^5.0.0) — too fresh to trust for
 * something this central, versus 5.5.0's long, widely deployed track
 * record (VS Code's own integrated terminal included).
 */
export function usePty(wsUrl: string) {
  const [status, setStatus] = useState<PtyStatus>('idle')
  const [fontSize, setFontSize] = useState(13)
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const resizeObserverRef = useRef<ResizeObserver | null>(null)
  const stopWaitRef = useRef<() => void>(() => {})

  const stop = useCallback(() => {
    stopWaitRef.current()
    wsRef.current?.close()
    wsRef.current = null
    resizeObserverRef.current?.disconnect()
    resizeObserverRef.current = null
    termRef.current?.dispose()
    termRef.current = null
    fitAddonRef.current = null
    searchAddonRef.current = null
    setStatus((s) => (s === 'idle' ? s : 'closed'))
  }, [])

  // Belt-and-braces cleanup if the caller unmounts mid-session — the
  // process on the host is killed by the server the moment the socket
  // closes (see runPTYSession's deferred cleanup), not left running.
  useEffect(() => stop, [stop])

  const start = useCallback(() => {
    stop()
    setStatus('connecting')

    // containerRef.current can still be null right here even though the
    // caller just mounted (UpdateModal starts from a useEffect that fires
    // the instant its own component mounts): antd's Modal renders its
    // children into a portal target it may only attach a render or two
    // after this component's own effects run, so the ref this component
    // holds isn't guaranteed to be live yet on the very first tick. Rather
    // than assume either timing (silently doing nothing forever, or
    // crashing on a null container), poll a few animation frames for the
    // ref to actually resolve — the same "wait for the real signal instead
    // of guessing a delay" approach used for the container's size below.
    let cancelled = false
    let attempts = 0
    const MAX_ATTEMPTS = 120 // ~2s at 60fps

    const waitForContainer = () => {
      if (cancelled) return
      const container = containerRef.current
      if (container) {
        runStart(container)
        return
      }
      attempts += 1
      if (attempts >= MAX_ATTEMPTS) {
        // eslint-disable-next-line no-console
        console.error('usePty: container never mounted')
        setStatus('error')
        return
      }
      requestAnimationFrame(waitForContainer)
    }
    requestAnimationFrame(waitForContainer)
    stopWaitRef.current = () => {
      cancelled = true
    }

    function runStart(container: HTMLDivElement) {
      try {
        const term = new XTerm({
          cursorBlink: true,
          fontSize,
          fontFamily: MONO_FONT_STACK,
          theme: THEME,
          scrollback: 5000,
          // Unicode11Addon (below) and setting term.unicode.activeVersion
          // both touch xterm.js's "proposed" (unstable) API surface, which
          // throws synchronously unless this is explicitly opted into —
          // uncaught, that exception aborted start() before the WebSocket
          // was ever created, which is why the terminal was blank with no
          // network request at all, not a connection failure.
          allowProposedApi: true,
        })
        termRef.current = term

        const fit = new FitAddon()
        term.loadAddon(fit)
        fitAddonRef.current = fit
        term.loadAddon(new WebLinksAddon())
        const search = new SearchAddon()
        term.loadAddon(search)
        searchAddonRef.current = search
        term.loadAddon(new Unicode11Addon())
        term.unicode.activeVersion = '11'
        // The canvas renderer measurably smooths scrolling under heavy
        // output (apt-get upgrade logs, `ls` of a large tree) versus the
        // default DOM renderer — optional by design: if the browser refuses
        // a 2D canvas context for any reason, the terminal must keep
        // working on the fallback renderer rather than fail to open at all.
        try {
          term.loadAddon(new CanvasAddon())
        } catch {
          // DOM renderer stays in effect — still fully functional, just
          // slower under very high-volume output.
        }

        // xterm.js measures character-cell metrics as part of Terminal.open()
        // itself — calling it on a container that isn't measurable yet
        // (still display:none, or an antd Modal whose enter animation
        // hasn't laid out its content) permanently bakes in a broken (zero)
        // measurement that no later resize corrects, because what was
        // wrong was the *measurement taken while unmeasurable*, not the
        // container's size changing afterwards. A single deferred frame
        // doesn't reliably cover this — a CSS-driven modal animation can
        // take far longer than one frame — so instead of guessing a delay,
        // open() is held until the ResizeObserver itself reports a real,
        // non-zero box. That observer then keeps calling fit() on every
        // later resize as before, so this is a strict superset of the old
        // one-shot behaviour, not a replacement path only used once.
        let opened = false

        function openAndConnect() {
          if (opened) return
          opened = true
          term.open(container)
          fit.fit()

          const ws = new WebSocket(wsUrl)
          ws.binaryType = 'arraybuffer'
          wsRef.current = ws
          const encoder = new TextEncoder()

          ws.onopen = () => {
            setStatus('connected')
            term.focus()
            // fit.fit() above already sized the terminal correctly for the
            // real container — but that happened before this socket could
            // possibly be open yet (a WS handshake is at least one round
            // trip), so the resize event it fired either had no listener
            // yet (term.onResize is registered below, after this) or, even
            // if it had, would have been silently dropped by that
            // handler's own readyState-OPEN guard. Left uncorrected, the
            // server's PTY (pty.Start with no explicit size) stays at
            // whatever the kernel/pty library defaults to — commonly
            // 80x24 — until some *later* resize happens to fire, which is
            // exactly the reported "терминал не открывается в реальный
            // размер окна". Send the already-correct current size
            // explicitly now that the socket can actually carry it.
            ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
          }
          ws.onmessage = (ev) => {
            if (ev.data instanceof ArrayBuffer) term.write(new Uint8Array(ev.data))
          }
          ws.onerror = () => setStatus('error')
          ws.onclose = () => setStatus((s) => (s === 'error' ? s : 'closed'))

          term.onData((data) => {
            if (ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
          })
          term.onResize(({ cols, rows }) => {
            if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
          })
        }

        const resizeObserver = new ResizeObserver(() => {
          if (!opened) {
            if (container.clientWidth > 0 && container.clientHeight > 0) openAndConnect()
            return
          }
          fit.fit()
        })
        resizeObserver.observe(container)
        resizeObserverRef.current = resizeObserver
      } catch (err) {
        // An uncaught throw partway through must not leave status stuck on
        // "connecting" forever with nothing on screen to explain why — that
        // reads as a hung connection, not the local failure it actually was.
        // eslint-disable-next-line no-console
        console.error('usePty: failed to start session', err)
        setStatus('error')
      }
    }
    // wsUrl is the only real dependency — start() always (re)builds a fresh
    // terminal/socket pair from scratch via stop() above regardless of when
    // it's called, so re-creating the callback on every render buys nothing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsUrl])

  const copySelection = useCallback(() => {
    const text = termRef.current?.getSelection()
    if (text) void navigator.clipboard.writeText(text)
  }, [])

  const clear = useCallback(() => termRef.current?.clear(), [])

  const changeFontSize = useCallback((delta: number) => {
    setFontSize((size) => {
      const next = Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, size + delta))
      if (termRef.current) termRef.current.options.fontSize = next
      // fontSize is not a layout-affecting CSS property the ResizeObserver
      // above would ever see change on its own — cols/rows have to be
      // recomputed for the new glyph metrics explicitly, once the browser
      // has actually re-laid-out the resized character cells.
      requestAnimationFrame(() => fitAddonRef.current?.fit())
      return next
    })
  }, [])

  const search = useCallback((query: string, backwards = false) => {
    if (!query) return
    const addon = searchAddonRef.current
    if (!addon) return
    if (backwards) addon.findPrevious(query)
    else addon.findNext(query)
  }, [])

  return { containerRef, status, start, stop, copySelection, clear, changeFontSize, search }
}

/** Mirrors api.ts's own hostScope-aware prefixing — WebSocket needs its own
 * URL, it cannot go through the fetch-based api() helper. Must special-case
 * LOCAL_HOST_ID exactly like api.ts does: the hub registers the embedded
 * local scanner's routes under the literal path segment "local"
 * (internal/hub/server.go's `/hosts/local/*`), not under the sentinel's
 * numeric id — a plain `/hosts/${hostScope.id}` would build `/hosts/-1/...`,
 * which the hub's router instead parses as a real (nonexistent) host id and
 * fails on, rather than ever reaching the local terminal/updates session.
 *
 * Also appends the current UI language as a `lang` query param — the
 * WebSocket-upgrade equivalent of api.ts's X-NKT-Lang header, since browser
 * JS cannot set custom headers on a WebSocket handshake (see
 * internal/msgs's own doc comment on the backend side of this). */
export function wsURL(path: string): string {
  const prefix = hostScope.id !== null ? `/hosts/${hostScope.id === LOCAL_HOST_ID ? 'local' : hostScope.id}` : ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const sep = path.includes('?') ? '&' : '?'
  return `${proto}//${location.host}/api${prefix}${path}${sep}lang=${i18n.language}`
}
