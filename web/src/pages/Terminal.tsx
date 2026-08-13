import { Button } from 'antd'
import type { Me } from '../types'
import { Banner, Card } from '../components/ui'
import { usePty, wsURL } from '../hooks/usePty'

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
  const { containerRef, status, start, stop } = usePty(wsURL('/terminal/ws'))

  function handleStart() {
    if (
      !window.confirm(
        'Открыть терминал на этом хосте? Это полноценный доступ к shell от имени пользователя, ' +
          'под которым запущен nkt — обычно root. Действие записывается в журнал.',
      )
    ) {
      return
    }
    start()
  }

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
            <Button type="primary" loading={status === 'connecting'} disabled={!canUse} onClick={handleStart}>
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
