import { useCallback, useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { api, hostScope, onUnauthorized, useApi } from './api'
import type { Me, Overview } from './types'
import Login from './pages/Login'
import Hosts from './pages/Hosts'
import OverviewPage from './pages/Overview'
import Findings from './pages/Findings'
import TopologyPage from './pages/Topology'
import Configs from './pages/Configs'
import Services from './pages/Services'
import Docker from './pages/Docker'
import Podman from './pages/Podman'
import LXD from './pages/LXD'
import Virtualization from './pages/Virtualization'
import Firewall from './pages/Firewall'
import Certificates from './pages/Certificates'
import Availability from './pages/Availability'
import Usage from './pages/Usage'
import Audit from './pages/Audit'
import Users from './pages/Users'
import { Banner, Card } from './components/ui'
import PasswordForm from './components/PasswordForm'

type Theme = 'light' | 'dark' | 'auto'

function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem('nkt-theme') as Theme | null) ?? 'auto',
  )
  useEffect(() => {
    const root = document.documentElement
    if (theme === 'auto') root.removeAttribute('data-theme')
    else root.setAttribute('data-theme', theme)
    localStorage.setItem('nkt-theme', theme)
  }, [theme])
  return [theme, setTheme]
}

const NAV = [
  { to: '/', label: 'Обзор', end: true },
  { to: '/findings', label: 'Проблемы', badge: 'findings' as const },
  { to: '/topology', label: 'Карта ресурсов' },
  { to: '/availability', label: 'Доступность' },
  { to: '/usage', label: 'Нагрузка' },
  { to: '/configs', label: 'Конфигурации' },
  { to: '/services', label: 'Сервисы' },
  { to: '/containers', label: 'Docker' },
  { to: '/podman', label: 'Podman' },
  { to: '/lxd', label: 'LXD' },
  { to: '/vms', label: 'Виртуальные машины' },
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

  if (!checked) return <div className="login-wrap">Загрузка…</div>

  if (!me) {
    return (
      <Routes>
        <Route path="/login" element={<Login onSuccess={loadMe} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  return <Shell me={me} onLogout={() => setMe(null)} />
}

/** A host selected in the hub shell — everything below scopes its API calls
 * to it (see api.ts's hostScope) once it is set. */
type SelectedHost = { id: number; name: string }

function Shell({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const [theme, setTheme] = useTheme()
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
  useEffect(() => {
    hostScope.id = isHub ? (selectedHost?.id ?? null) : null
    return () => {
      hostScope.id = null
    }
  }, [isHub, selectedHost])

  function selectHost(host: SelectedHost | null) {
    setSelectedHost(host)
    if (host) localStorage.setItem('nkt-hub-host', JSON.stringify(host))
    else localStorage.removeItem('nkt-hub-host')
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
            <Hosts onSelect={selectHost} />
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
            <Route path="/containers" element={<Docker me={me} />} />
            <Route path="/podman" element={<Podman me={me} />} />
            <Route path="/lxd" element={<LXD me={me} />} />
            <Route path="/vms" element={<Virtualization me={me} />} />
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
