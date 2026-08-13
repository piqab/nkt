import { useCallback, useEffect, useRef, useState } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { hostScope } from '../api'

export type PtyStatus = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'

/**
 * Wires an xterm.js terminal to a PTY-over-WebSocket endpoint (see
 * internal/api/pty_session.go: binary frames carry raw I/O, resize goes out
 * as a JSON text frame). Shared by the general terminal page and the
 * package-update dialog — both stream a live interactive PTY session the
 * same way and differ only in the WebSocket URL and the UI around it.
 */
export function usePty(wsUrl: string) {
  const [status, setStatus] = useState<PtyStatus>('idle')
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)

  const stop = useCallback(() => {
    wsRef.current?.close()
    wsRef.current = null
    termRef.current?.dispose()
    termRef.current = null
    fitRef.current = null
    setStatus((s) => (s === 'idle' ? s : 'closed'))
  }, [])

  // Belt-and-braces cleanup if the caller unmounts mid-session — the
  // process on the host is killed by the server the moment the socket
  // closes (see runPTYSession's deferred cleanup), not left running.
  useEffect(() => stop, [stop])

  const start = useCallback(() => {
    if (!containerRef.current) return
    stop()
    setStatus('connecting')

    const term = new XTerm({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'var(--mono)',
      theme: { background: '#141414' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    fit.fit()
    termRef.current = term
    fitRef.current = fit

    const ws = new WebSocket(wsUrl)
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    const encoder = new TextEncoder()

    ws.onopen = () => {
      setStatus('connected')
      term.focus()
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
    // wsUrl is the only real dependency — start() always (re)builds a fresh
    // terminal/socket pair from scratch via stop() above regardless of when
    // it's called, so re-creating the callback on every render buys nothing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wsUrl])

  useEffect(() => {
    if (status !== 'connected') return
    const onResize = () => fitRef.current?.fit()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [status])

  return { containerRef, status, start, stop }
}

/** Mirrors api.ts's own hostScope-aware prefixing — WebSocket needs its own
 * URL, it cannot go through the fetch-based api() helper. */
export function wsURL(path: string): string {
  const prefix = hostScope.id !== null ? `/hosts/${hostScope.id}` : ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api${prefix}${path}`
}
