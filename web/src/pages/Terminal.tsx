import { useEffect, useState } from 'react'
import { Button } from 'antd'
import { useLocation } from 'react-router-dom'
import type { Me } from '../types'
import { api, hostScope, readSelectedHost, useApi } from '../api'
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
 *
 * Also the component App.tsx renders bare (no sidebar/nav) for a "detached"
 * terminal window — see isPopout below, which swaps "Открепить" for
 * "Закрыть окно" there, since offering to detach a window that is already
 * its own separate window makes no sense.
 */
export default function TerminalPage({ me }: { me: Me }) {
  const canUse = me.is_admin && me.allow_mutations
  const location = useLocation()
  const isPopout = location.pathname === '/terminal/popout'

  // tmuxMode picks which WebSocket endpoint the next start() connects to
  // (plain login shell vs. `tmux new-session -A`, see handleTerminalWS) —
  // it has to be state, not a query-time flag, because usePty's start()
  // closes over wsUrl at the moment it's (re)created, and this component
  // only ever keeps one usePty session alive at a time regardless of which
  // mode opened it.
  const [tmuxMode, setTmuxMode] = useState(false)
  const [pendingStart, setPendingStart] = useState(false)
  const wsUrl = wsURL(tmuxMode ? '/terminal/ws?tmux=1' : '/terminal/ws')
  const { containerRef, status, start, stop, copySelection, clear, changeFontSize, search } = usePty(wsUrl)

  // Deferred one tick: setTmuxMode above only takes effect on the next
  // render, which is also when usePty hands back a start() closing over the
  // now-updated wsUrl — calling start() in the same event handler that just
  // changed tmuxMode would still fire the *previous* mode's connection.
  useEffect(() => {
    if (!pendingStart) return
    setPendingStart(false)
    start()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingStart, wsUrl])

  // A distinct browser-taskbar-visible title per popout window is the whole
  // point of being able to open several at once (see openPopout below) —
  // without it every detached terminal window looks identical from the OS
  // window switcher. The host's display name travels in this window's own
  // URL (?name=) rather than through readSelectedHost()'s shared
  // localStorage entry: that reflects whichever host the *main tab* has
  // selected right now, which is not necessarily this window's host once
  // more than one popout is open or the main tab has since switched hosts.
  useEffect(() => {
    if (!isPopout) return
    const name = new URLSearchParams(location.search).get('name')
    document.title = name ? `Терминал: ${name}` : 'Терминал'
  }, [isPopout, location.search])

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

  // Whether tmux is on this host's PATH — the "Открыть в tmux" button uses
  // this to decide between connecting directly and offering to install it
  // first (see handleTmuxButtonClick below).
  const { data: tmuxStatus, reload: reloadTmuxStatus } = useApi<{ available: boolean }>('/system/tmux-status', 30_000)
  const { data: tmuxInstallStatus, reload: reloadTmuxInstallStatus } = useApi<{
    active: boolean
    finished: boolean
    succeeded: boolean
  }>('/system/tmux-install/status', 5_000)
  const [tmuxInstallOpen, setTmuxInstallOpen] = useState(false)
  const [tmuxInstallOutcome, setTmuxInstallOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  function handleStart(tmux: boolean) {
    if (
      !window.confirm(
        tmux
          ? 'Открыть терминал в tmux на этом хосте? Это полноценный доступ к shell от имени пользователя, ' +
              'под которым запущен nkt (или ssh-пользователя хоста, если он задан). Сессия tmux переживёт ' +
              'закрытие вкладки — переподключиться можно тем же способом. Действие записывается в журнал.'
          : 'Открыть терминал на этом хосте? Это полноценный доступ к shell от имени пользователя, ' +
              'под которым запущен nkt — обычно root, либо ssh-пользователь хоста, если он задан. ' +
              'Действие записывается в журнал.',
      )
    ) {
      return
    }
    // dbusStatus polls every 30s in the background — too stale to trust
    // right at the moment it matters most (right before the shell actually
    // opens and either does or doesn't hit the sandbox). Refresh it here so
    // the badge below reflects the state the terminal is about to run
    // under, not whatever it was up to half a minute ago.
    reloadDbusStatus()
    setTmuxMode(tmux)
    setPendingStart(true)
  }

  // Direct connect when tmux is already there; otherwise offer to install
  // it first (PackageInstallModal below) and auto-connect once that
  // succeeds — see handleTmuxInstallFinished.
  function handleTmuxButtonClick() {
    if (tmuxStatus?.available) {
      handleStart(true)
      return
    }
    if (tmuxInstallStatus?.active) {
      setTmuxInstallOutcome(null)
      setTmuxInstallOpen(true)
      return
    }
    if (window.confirm('tmux не установлен на этом хосте. Установить (apt-get install -y tmux) и открыть сессию?')) {
      setTmuxInstallOutcome(null)
      setTmuxInstallOpen(true)
    }
  }

  async function handleTmuxInstallFinished() {
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>('/system/tmux-install/status').catch(
      () => null,
    )
    reloadTmuxInstallStatus()
    reloadTmuxStatus()
    const ok = !!fresh?.succeeded
    setTmuxInstallOutcome(ok ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    if (ok) {
      setTmuxInstallOpen(false)
      handleStart(true)
    }
  }

  /**
   * Opens this same page in its own, chrome-less browser window (see
   * App.tsx's isPopoutTerminal) — a real OS-level window, not a modal, so it
   * can be moved to another monitor and survives navigating around the main
   * tab (which would otherwise unmount this page and kill the session, see
   * usePty's own cleanup effect). Stops the in-tab session first rather than
   * leaving two live shells behind — "detach", not "duplicate": the popout
   * gets its own fresh "Открыть терминал" to connect with once it opens.
   *
   * Under a hub, both the target host id and a window name keyed by it
   * travel along: window.open reuses/refocuses an already-open window
   * whenever the *name* matches, so without a per-host name, detaching a
   * second host's terminal would just hijack the first host's popout
   * instead of opening an independent one — the host id in the URL is what
   * lets App.tsx scope *that* window's own hostScope correctly (see its own
   * doc comment on why that can't just come from the normal, single shared
   * "currently selected host" the main tab uses).
   */
  function openPopout() {
    if (status === 'connected' || status === 'connecting') stop()
    const id = hostScope.id
    const params = new URLSearchParams()
    if (id !== null) {
      params.set('host', String(id))
      const name = readSelectedHost()?.name
      if (name) params.set('name', name)
    }
    const qs = params.toString()
    const windowName = `nkt-terminal-${id ?? 'local'}`
    window.open(`/terminal/popout${qs ? `?${qs}` : ''}`, windowName, 'width=980,height=640,resizable=yes')
  }

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>Терминал</h1>
          {status !== 'connected' && (
            <p>
              Интерактивный shell на этом хосте прямо в браузере. Требует явно включённой опции
              хоста (<code className="mono">NKT_TERMINAL_ENABLED=true</code>) — если сервер её не
              поднял, подключение завершится ошибкой ниже.
            </p>
          )}
        </div>
        <div className="row">
          {isPopout ? (
            <Button onClick={() => window.close()}>закрыть окно</Button>
          ) : (
            <Button disabled={!canUse} onClick={openPopout} title="Открыть в отдельном окне браузера — переживёт переход по другим страницам">
              Открепить в отдельное окно
            </Button>
          )}
          {status === 'connected' ? (
            <Button danger onClick={stop}>
              закрыть терминал
            </Button>
          ) : (
            <>
              <Button
                type="primary"
                loading={status === 'connecting' && !tmuxMode}
                disabled={!canUse || (status === 'connecting' && tmuxMode)}
                onClick={() => handleStart(false)}
              >
                {status === 'connecting' && !tmuxMode ? 'Подключаюсь…' : 'Открыть терминал'}
              </Button>
              <Button
                loading={status === 'connecting' && tmuxMode}
                disabled={!canUse || (status === 'connecting' && !tmuxMode)}
                onClick={handleTmuxButtonClick}
                title="Сессия tmux переживает закрытие вкладки — переподключение продолжает её же"
              >
                {status === 'connecting' && tmuxMode ? 'Подключаюсь…' : 'Открыть в tmux'}
              </Button>
            </>
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

      {status !== 'connected' &&
        (dbusStatus === null ? (
          <Banner kind="info">Проверяю доступность D-Bus на хосте…</Banner>
        ) : dbusStatus.needed ? (
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
        ) : (
          <Banner kind="success">D-Bus на хосте доступен — песочница не ограничивает терминал и обновления.</Banner>
        ))}

      <div className="row" style={{ alignItems: 'flex-start', gap: '1rem' }}>
        <div style={{ flex: 1, minWidth: 0 }}>
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
        </div>

        {tmuxMode && <TmuxHints />}
      </div>

      {dbusInstallOpen && (
        <PackageInstallModal
          packageName="dbus"
          wsPath="/system/dbus-install/ws"
          onClose={() => setDbusInstallOpen(false)}
          onFinished={handleDbusInstallFinished}
          outcome={dbusInstallOutcome}
        />
      )}

      {tmuxInstallOpen && (
        <PackageInstallModal
          packageName="tmux"
          wsPath="/system/tmux-install/ws"
          onClose={() => setTmuxInstallOpen(false)}
          onFinished={handleTmuxInstallFinished}
          outcome={tmuxInstallOutcome}
        />
      )}
    </>
  )
}

// TMUX_HINTS groups tmux's own default key sequences (Ctrl+b is the prefix
// — not something nkt configures) by what they do. Purely a reference: the
// "buttons" below are disabled (see TmuxHints) — an earlier version of this
// panel actually ran these as live commands against the session by clicking
// them, but that path proved unreliable on a systemd-sandboxed hub host
// (see handleTerminalWS's own doc comment on why tmux specifically, unlike
// a plain shell, is at risk there) and was removed; this is deliberately
// just documentation now, styled to stay visually consistent with the rest
// of the page rather than as a plain text block.
const TMUX_HINTS: { title: string; rows: [string, string][] }[] = [
  {
    title: 'Окна',
    rows: [
      ['Ctrl+b c', 'новое окно'],
      ['Ctrl+b ,', 'переименовать текущее окно'],
      ['Ctrl+b n / p', 'следующее / предыдущее окно'],
      ['Ctrl+b 0…9', 'переключиться на окно N'],
      ['Ctrl+b w', 'список окон (интерактивный)'],
      ['Ctrl+b &', 'закрыть текущее окно'],
    ],
  },
  {
    title: 'Панели',
    rows: [
      ['Ctrl+b %', 'разделить панель по вертикали'],
      ['Ctrl+b "', 'разделить панель по горизонтали'],
      ['Ctrl+b ←↑↓→', 'переключиться на соседнюю панель'],
      ['Ctrl+b z', 'развернуть/свернуть панель на весь экран'],
      ['Ctrl+b x', 'закрыть текущую панель'],
    ],
  },
  {
    title: 'Сессия',
    rows: [
      ['Ctrl+b d', 'отключиться (сессия продолжает работать)'],
      ['Ctrl+b [', 'режим прокрутки/копирования (q — выйти)'],
      ['Ctrl+b ]', 'вставить скопированное'],
    ],
  },
]

/**
 * TmuxHints — a static reference for tmux's own default key sequences,
 * shown next to the terminal only in tmux mode (see TMUX_HINTS above for
 * why this is documentation, not live controls). The key combination is a
 * plain chip (background + border, ordinary text colour) rather than an
 * antd disabled Button — disabled buttons render their label in antd's own
 * low-contrast disabled grey, which made the keys themselves hard to read;
 * a chip keeps the same "this is a key, not prose" visual grouping without
 * that contrast loss.
 */
function TmuxHints() {
  return (
    <div style={{ width: 240, flexShrink: 0 }}>
      <Card title="Управление tmux">
        <p className="small muted" style={{ marginTop: 0 }}>
          Сессия называется <code className="mono">nkt</code> — «Открыть в tmux» переподключается к
          ней же, если она ещё жива на хосте.
        </p>
        {TMUX_HINTS.map((group) => (
          <div key={group.title} style={{ marginTop: '0.5rem' }}>
            <div className="small muted" style={{ marginBottom: '0.2rem' }}>
              {group.title}
            </div>
            <div className="col" style={{ gap: '0.15rem' }}>
              {group.rows.map(([keys, desc]) => (
                <div key={keys} className="row" style={{ flexWrap: 'nowrap', gap: '0.4rem', alignItems: 'baseline' }}>
                  <span
                    className="mono"
                    style={{
                      flexShrink: 0,
                      fontSize: '0.72rem',
                      padding: '0.05rem 0.3rem',
                      background: 'var(--surface-1)',
                      border: '1px solid var(--border)',
                      borderRadius: 'var(--radius-sm)',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {keys}
                  </span>
                  <span className="small muted">{desc}</span>
                </div>
              ))}
            </div>
          </div>
        ))}
      </Card>
    </div>
  )
}
