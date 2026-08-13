import { useEffect, useRef, useState } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { Button } from 'antd'
import { hostScope } from '../api'
import type { Me } from '../types'
import { Banner, Card } from '../components/ui'

type Status = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'

/** Mirrors api.ts's own hostScope-aware prefixing — WebSocket needs its own
 * URL, it cannot go through the fetch-based api() helper. */
function terminalURL(): string {
  const prefix = hostScope.id !== null ? `/hosts/${hostScope.id}` : ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api${prefix}/terminal/ws`
}

/**
 * A real login shell on the host, streamed over WebSocket into xterm.js.
 * Gated server-side behind admin + AllowMutations + an explicit
 * NKT_TERMINAL_ENABLED opt-in (off by default) — this page itself only
 * adds the same window.confirm the rest of the app uses before any
 * destructive action, since "open a root shell" is the most consequential
 * thing here, not a lesser one.
 */
export default function TerminalPage({ me }: { me: Me }) {
  const canUse = me.is_admin && me.allow_mutations
  const [status, setStatus] = useState<Status>('idle')
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)

  function teardown() {
    wsRef.current?.close()
    wsRef.current = null
    termRef.current?.dispose()
    termRef.current = null
    fitRef.current = null
  }

  // Belt-and-braces cleanup if the operator navigates away mid-session —
  // the shell process on the host is killed by the server the moment the
  // socket closes (see handleTerminalWS's deferred cleanup), not left
  // running.
  useEffect(() => teardown, [])

  function start() {
    if (
      !window.confirm(
        'Открыть терминал на этом хосте? Это полноценный доступ к shell от имени пользователя, ' +
          'под которым запущен nkt — обычно root. Действие записывается в журнал.',
      )
    ) {
      return
    }
    if (!containerRef.current) return
    teardown()
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

    const ws = new WebSocket(terminalURL())
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
  }

  function stop() {
    teardown()
    setStatus('closed')
  }

  useEffect(() => {
    if (status !== 'connected') return
    const onResize = () => fitRef.current?.fit()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [status])

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Терминал</h1>
          <p>
            Интерактивный shell на этом хосте прямо в браузере. Требует явно включённой опции
            хоста (<code className="mono">NKT_TERMINAL_ENABLED=true</code>) — если сервер её не
            поднял, подключение завершится ошибкой ниже.
          </p>
        </div>
        <div className="row">
          {status === 'connected' ? (
            <Button danger onClick={stop}>
              закрыть терминал
            </Button>
          ) : (
            <Button type="primary" loading={status === 'connecting'} disabled={!canUse} onClick={start}>
              {status === 'connecting' ? 'Подключаюсь…' : 'Открыть терминал'}
            </Button>
          )}
        </div>
      </div>

      {!canUse && (
        <Banner kind="info">Доступно только роли admin с включёнными изменениями (AllowMutations).</Banner>
      )}
      {status === 'error' && (
        <Banner kind="error">
          Не удалось подключиться. Терминал может быть выключен на сервере
          (NKT_TERMINAL_ENABLED) — либо это обычная сетевая ошибка.
        </Banner>
      )}
      {status === 'closed' && <Banner kind="info">Сессия завершена.</Banner>}

      <Card>
        <div
          ref={containerRef}
          style={{
            height: '65vh',
            background: '#141414',
            borderRadius: 'var(--radius-sm)',
            padding: '0.5rem',
            display: status === 'idle' ? 'none' : 'block',
          }}
        />
        {status === 'idle' && <div className="chart-empty">Терминал ещё не открыт.</div>}
      </Card>
    </>
  )
}
