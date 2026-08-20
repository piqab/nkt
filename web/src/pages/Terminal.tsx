import { useState } from 'react'
import { Button } from 'antd'
import type { Me } from '../types'
import { api, useApi } from '../api'
import { Banner, Card } from '../components/ui'
import { PtyToolbar } from '../components/PtyToolbar'
import { usePty, wsURL } from '../hooks/usePty'
import PackageInstallModal from '../components/PackageInstallModal'

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
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(
    wsURL('/terminal/ws'),
  )

  // Whether the terminal/updates/self-update escape hatch (systemd-run) is
  // currently unusable on this host because D-Bus isn't reachable — see
  // internal/api/pty_session.go's usingSystemdSandbox. needed=false when
  // nkt isn't even running as a systemd unit (nothing to report) or D-Bus
  // already works; can_install=false means even the CAP_SYS_ADMIN/nsenter
  // fallback isn't usable, so there's nothing this page can offer to fix
  // it automatically.
  const { data: dbusStatus, reload: reloadDbusStatus } = useApi<{ needed: boolean; can_install: boolean }>(
    '/system/dbus-status',
    30_000,
  )
  // Polled independently of whether the install dialog is open, same
  // reason as Firewall's own ufw/firewalld install status polls: without
  // it there's no way to tell, from the button alone, whether opening the
  // dialog would reattach to an install already running (started earlier,
  // or by someone else) or start a fresh one.
  const { data: dbusInstallStatus, reload: reloadDbusInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>('/system/dbus-install/status', 5_000)
  const [dbusInstallOpen, setDbusInstallOpen] = useState(false)
  const [dbusInstallOutcome, setDbusInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  async function handleDbusInstallFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/system/dbus-install/status').catch(
      () => null,
    )
    reloadDbusInstallStatus()
    reloadDbusStatus()
    setDbusInstallOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
  }

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

      {dbusStatus?.needed && (
        <Banner kind="warn">
          На хосте не работает D-Bus — терминал (если он вообще открылся), обновление пакетов и
          самообновление хоста через резервный канал ограничены песочницей systemd-юнита: не могут
          писать в системные пути. Причина обычно в том, что dbus не установлен или не запущен
          (некоторые минимальные образы, например Debian 11, не ставят его по умолчанию).{' '}
          {dbusStatus.can_install ? (
            dbusInstallStatus?.active ? (
              <Button
                size="small"
                type="primary"
                onClick={() => {
                  setDbusInstallOutcome(null)
                  setDbusInstallOpen(true)
                }}
              >
                установка выполняется — открыть
              </Button>
            ) : (
              <Button
                size="small"
                type="primary"
                onClick={() => {
                  if (window.confirm('Установить dbus (apt-get install -y dbus) и запустить его на этом хосте?')) {
                    setDbusInstallOutcome(null)
                    setDbusInstallOpen(true)
                  }
                }}
              >
                Установить dbus
              </Button>
            )
          ) : (
            'Автоматическая установка недоступна на этом хосте — поставьте вручную: apt-get install -y dbus && systemctl enable --now dbus.'
          )}
        </Banner>
      )}

      <Card>
        {status === 'connected' && (
          <PtyToolbar onCopy={copySelection} onClear={clear} onFontSize={changeFontSize} onSearch={search} />
        )}
        {/* The xterm container stays mounted and laid out (never
            display:none) from the very first render — xterm.js measures
            character-cell metrics as part of opening, and doing that on a
            hidden element bakes in a broken measurement no later resize
            fixes (see usePty's own comment on this). The "not started yet"
            state is an overlay on top of it instead of a replacement for
            it, so the terminal is always sitting on a real, visible,
            correctly-sized element by the time start() actually opens it. */}
        <div style={{ position: 'relative' }}>
          <div
            ref={containerRef}
            style={{
              height: '65vh',
              background: '#141414',
              borderRadius: 'var(--radius-sm)',
              padding: '0.5rem',
            }}
          />
          {status === 'idle' && (
            <div
              className="chart-empty"
              style={{
                position: 'absolute',
                inset: 0,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                background: '#141414',
                borderRadius: 'var(--radius-sm)',
              }}
            >
              Терминал ещё не открыт.
            </div>
          )}
        </div>
      </Card>

      {dbusInstallOpen && (
        <PackageInstallModal
          packageName="dbus"
          wsPath="/system/dbus-install/ws"
          onClose={() => setDbusInstallOpen(false)}
          onFinished={handleDbusInstallFinished}
          outcome={dbusInstallOutcome}
        />
      )}
    </>
  )
}
