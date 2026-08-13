import { useCallback, useEffect, useState } from 'react'
import { ConfigProvider, type ThemeConfig } from 'antd'
import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { api, hostScope, onUnauthorized, useApi } from './api'
import { buildAntdTheme, resolveIsDark, type Theme } from './theme'
import type { Me, Overview } from './types'
import Login from './pages/Login'
import Hosts from './pages/Hosts'
import OverviewPage from './pages/Overview'
import Findings from './pages/Findings'
import TopologyPage from './pages/Topology'
import Configs from './pages/Configs'
import Services from './pages/Services'
import Containers from './pages/Containers'
import TerminalPage from './pages/Terminal'
import Firewall from './pages/Firewall'
import Certificates from './pages/Certificates'
import Availability from './pages/Availability'
import Usage from './pages/Usage'
import Audit from './pages/Audit'
import Users from './pages/Users'
import { Banner, Card } from './components/ui'
import PasswordForm from './components/PasswordForm'

/**
 * Owns both the `data-theme` attribute (what styles.css itself reacts to)
 * and the antd ThemeConfig derived from it (see theme.ts's buildAntdTheme)
 * — one hook, so the two can never independently drift out of sync.
 *
 * The antd theme is recomputed inside the same effect that writes the
 * attribute, immediately after, rather than via a separate
 * `useMemo(..., [isDark])`: a DOM attribute write is synchronous and
 * getComputedStyle forces a synchronous style recalc, so reading it right
 * here already reflects the mutation above — waiting for isDark to change
 * as a memo dependency would miss the case where an explicit ('light'/
 * 'dark') choice is re-applied on mount before the OS-driven CSS media
 * query and the JS-side value could otherwise briefly disagree.
 */
function useTheme(): [Theme, (t: Theme) => void, ThemeConfig] {
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem('nkt-theme') as Theme | null) ?? 'auto',
  )
  const [antdTheme, setAntdTheme] = useState<ThemeConfig>(() =>
    buildAntdTheme(resolveIsDark(theme)),
  )

  useEffect(() => {
    const root = document.documentElement
    if (theme === 'auto') root.removeAttribute('data-theme')
    else root.setAttribute('data-theme', theme)
    localStorage.setItem('nkt-theme', theme)
    setAntdTheme(buildAntdTheme(resolveIsDark(theme)))
  }, [theme])

  // styles.css's own @media (prefers-color-scheme) block already reacts to
  // a live OS theme flip on its own; nothing JS-observable did before this,
  // which would otherwise leave antd's algorithm/tokens silently stuck at
  // whatever 'auto' resolved to at mount.
  useEffect(() => {
    if (theme !== 'auto') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setAntdTheme(buildAntdTheme(mq.matches))
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [theme])

  return [theme, setTheme, antdTheme]
}

const NAV = [
  { to: '/', label: 'Обзор', end: true },
  { to: '/findings', label: 'Проблемы', badge: 'findings' as const },
  { to: '/topology', label: 'Карта ресурсов' },
  { to: '/availability', label: 'Доступность' },
  { to: '/usage', label: 'Нагрузка' },
  { to: '/configs', label: 'Конфигурации' },
  { to: '/services', label: 'Сервисы' },
  { to: '/containers', label: 'Контейнеры и ВМ' },
  // A viewer has nothing to look at here without connecting (unlike the
  // read-only pages above) — hidden rather than shown-but-disabled.
  { to: '/terminal', label: 'Терминал', adminOnly: true },
  { to: '/firewall', label: 'Firewall' },
  { to: '/certificates', label: 'Сертификаты', badge: 'certs' as const },
  { to: '/audit', label: 'Журнал действий' },
  // Managing who can sign in is itself an admin action — a viewer has no use
  // for this screen and the API would refuse every request from it anyway.
  { to: '/users', label: 'Пользователи', adminOnly: true },
]

export default function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [checked, setChecked] = useState(false)
  const navigate = useNavigate()
  // Owned here, not inside Shell, so the antd theme (and the login screen
  // under it) is consistent before a session even exists — not just once
  // someone is signed in.
  const [theme, setTheme, antdTheme] = useTheme()

  const loadMe = useCallback(async () => {
    try {
      setMe(await api<Me>('/auth/me'))
    } catch {
      setMe(null)
    } finally {
      setChecked(true)
    }
  }, [])

  useEffect(() => {
    void loadMe()
  }, [loadMe])

  useEffect(() => {
    onUnauthorized.handler = () => {
      setMe(null)
      navigate('/login', { replace: true })
    }
    return () => {
      onUnauthorized.handler = null
    }
  }, [navigate])

  return (
    <ConfigProvider theme={antdTheme}>
      {!checked ? (
        <div className="login-wrap">Загрузка…</div>
      ) : !me ? (
        <Routes>
          <Route path="/login" element={<Login onSuccess={loadMe} />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      ) : (
        <Shell me={me} theme={theme} setTheme={setTheme} onLogout={() => setMe(null)} />
      )}
    </ConfigProvider>
  )
}

/** A host selected in the hub shell — everything below scopes its API calls
 * to it (see api.ts's hostScope) once it is set. */
type SelectedHost = { id: number; name: string }

function Shell({
  me,
  theme,
  setTheme,
  onLogout,
}: {
  me: Me
  theme: Theme
  setTheme: (t: Theme) => void
  onLogout: () => void
}) {
  const [showPassword, setShowPassword] = useState(false)
  const navigate = useNavigate()
  const isHub = me.mode === 'hub'

  const [selectedHost, setSelectedHost] = useState<SelectedHost | null>(() => {
    if (!isHub) return null
    try {
      const raw = localStorage.getItem('nkt-hub-host')
      return raw ? (JSON.parse(raw) as SelectedHost) : null
    } catch {
      return null
    }
  })

  // Every page below reads through api()/useApi() unmodified; this is the
  // one place that redirects their calls to the selected host's own API
  // through the hub's proxy (see api.ts's hostScope) instead of the hub's.
  //
  // Set inline during render, deliberately not in a useEffect: React fires
  // effects child-first within a commit, so a child page's own effect
  // (useApi's fetch trigger, on the very page instance this same render is
  // about to mount) could otherwise run *before* an effect here updates
  // hostScope — sending that first request unscoped, straight at the hub's
  // own API instead of the host's ("Неизвестный метод API: /api/overview").
  // Render itself is always parent-before-children, so this is not.
  hostScope.id = isHub ? (selectedHost?.id ?? null) : null

  function selectHost(host: SelectedHost | null) {
    setSelectedHost(host)
    if (host) localStorage.setItem('nkt-hub-host', JSON.stringify(host))
    else localStorage.removeItem('nkt-hub-host')
    // The URL otherwise stays wherever it was — the host picker renders
    // outside <Routes> entirely (see showingHostPicker below), so it never
    // changes the address bar on its own. Without this, opening a host
    // after last viewing e.g. "/firewall" on a *different* host (or the
    // same one, earlier) lands back on Firewall instead of the overview,
    // since <Routes> just matches whatever path was already there.
    if (host) navigate('/')
  }

  // A hub with no host selected has nothing of its own to show an
  // overview/findings/etc. for — the host registry is the whole page.
  const showingHostPicker = isHub && !selectedHost

  // The overview is polled anyway; reuse it to keep the sidebar badge current.
  const overview = useApi<Overview>(showingHostPicker ? null : '/overview', 60_000)
  const criticalCount =
    (overview.data?.findings.critical ?? 0) + (overview.data?.findings.high ?? 0)
  // Certificates get their own badge: an expiry is a deadline, not a defect,
  // and it deserves to be visible without opening the findings list.
  const certAlerts =
    (overview.data?.certificates?.expired ?? 0) + (overview.data?.certificates?.expiring ?? 0)

  async function logout() {
    await api('/auth/logout', { method: 'POST' }).catch(() => undefined)
    onLogout()
    navigate('/login', { replace: true })
  }

  if (showingHostPicker) {
    return (
      <div className="shell">
        <aside className="sidebar">
          <div className="brand">
            <div className="brand-name">NetKnownsThat</div>
            <div className="brand-sub">управляющий центр</div>
          </div>
          <div className="sidebar-foot">
            <div>
              {me.username} · {me.role}
            </div>
            <div className="row" style={{ gap: '0.25rem' }}>
              <button className="ghost" onClick={logout}>
                Выйти
              </button>
            </div>
          </div>
        </aside>
        <main className="main">
          <div className="content">
            <Hosts onSelect={selectHost} hubVersion={me.hub_version} />
          </div>
        </main>
      </div>
    )
  }

  const shell = (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-name">NetKnownsThat</div>
          {!isHub && (
            <div className="brand-sub">
              {overview.data?.host.hostname ?? '…'} · режим {me.mode}
            </div>
          )}
        </div>

        <nav className="nav">
          {NAV.filter((item) => !item.adminOnly || me.is_admin).map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end}>
              <span>{item.label}</span>
              {item.badge === 'findings' && criticalCount > 0 && (
                <span className="nav-count">{criticalCount}</span>
              )}
              {item.badge === 'certs' && certAlerts > 0 && (
                <span className="nav-count">{certAlerts}</span>
              )}
            </NavLink>
          ))}
        </nav>

        <div className="sidebar-foot">
          <div className="row" style={{ marginBottom: '0.4rem' }}>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              Тема
              <select value={theme} onChange={(e) => setTheme(e.target.value as Theme)}>
                <option value="auto">системная</option>
                <option value="light">светлая</option>
                <option value="dark">тёмная</option>
              </select>
            </label>
          </div>
          {!isHub && (
            <>
              <div>
                {me.username} · {me.role}
              </div>
              <div className="row" style={{ gap: '0.25rem' }}>
                <button className="ghost" onClick={() => setShowPassword(true)} style={{ paddingLeft: 0 }}>
                  Сменить пароль
                </button>
                <button className="ghost" onClick={logout}>
                  Выйти
                </button>
              </div>
            </>
          )}
        </div>
      </aside>

      <main className="main">
        {/* key remounts every page below on host switch, so their useApi()
            calls re-fetch scoped to the newly selected host instead of
            showing stale data from the previous one. */}
        <div className="content" key={isHub ? selectedHost!.id : 'local'}>
          {showPassword && (
            <Card
              title="Смена пароля"
              actions={
                <button className="ghost" onClick={() => setShowPassword(false)}>
                  закрыть
                </button>
              }
            >
              <PasswordForm
                onDone={() => {
                  setShowPassword(false)
                  onLogout()
                  navigate('/login', { replace: true })
                }}
              />
            </Card>
          )}

          {me.simulated && (
            <Banner kind="warn">
              <strong>Режим снапшота (fixtures).</strong> Конфигурации читаются из каталога
              примеров, а не с реального хоста. Команды управления выполняются в симуляции,
              значения проб и метрик синтетические — они нужны, чтобы показать работу интерфейса.
              Для работы с настоящим сервером запустите с <code className="mono">NKT_MODE=local</code>.
            </Banner>
          )}
          {!me.allow_mutations && (
            <Banner kind="info">
              Изменения запрещены настройкой <code className="mono">NKT_ALLOW_MUTATIONS=false</code> —
              интерфейс работает только на чтение.
            </Banner>
          )}

          <Routes>
            <Route path="/" element={<OverviewPage me={me} />} />
            <Route path="/findings" element={<Findings />} />
            <Route path="/topology" element={<TopologyPage />} />
            <Route path="/availability" element={<Availability />} />
            <Route path="/usage" element={<Usage />} />
            <Route path="/configs" element={<Configs me={me} />} />
            <Route path="/services" element={<Services me={me} />} />
            <Route path="/containers" element={<Containers me={me} />} />
            {/* Docker/Podman/LXD/ВМ were separate nav entries before —
                redirect their old URLs to the merged page's default tab
                rather than a bare 404 for anyone with these bookmarked. */}
            <Route path="/podman" element={<Navigate to="/containers" replace />} />
            <Route path="/lxd" element={<Navigate to="/containers" replace />} />
            <Route path="/vms" element={<Navigate to="/containers" replace />} />
            {me.is_admin && <Route path="/terminal" element={<TerminalPage me={me} />} />}
            <Route path="/firewall" element={<Firewall me={me} />} />
            <Route path="/certificates" element={<Certificates me={me} />} />
            <Route path="/audit" element={<Audit />} />
            {me.is_admin && <Route path="/users" element={<Users me={me} />} />}
            <Route path="/login" element={<Navigate to="/" replace />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )

  if (!isHub) return shell

  // Under a hub, the host's own dashboard (identical markup to a plain
  // nkt's) sits below a hub-level bar — which host this is, and how to get
  // back to the registry — instead of blending hub navigation into the
  // host's own sidebar alongside its Findings/Docker/etc. links.
  return (
    <div className="hub-frame">
      <div className="hub-topbar">
        <button className="ghost" onClick={() => selectHost(null)}>
          ← к списку хостов
        </button>
        <span className="hub-topbar-brand">NetKnownsThat — хаб</span>
        <span className="hub-topbar-sep">→</span>
        <span className="hub-topbar-host">{selectedHost!.name}</span>
        <span className="hub-topbar-spacer" />
        <span className="small muted">
          {me.username} · {me.role}
        </span>
        <button className="ghost" onClick={logout}>
          Выйти
        </button>
      </div>
      {shell}
    </div>
  )
}
