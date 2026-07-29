import { useCallback, useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { api, onUnauthorized, useApi } from './api'
import type { Me, Overview } from './types'
import Login from './pages/Login'
import OverviewPage from './pages/Overview'
import Findings from './pages/Findings'
import TopologyPage from './pages/Topology'
import Configs from './pages/Configs'
import Services from './pages/Services'
import Firewall from './pages/Firewall'
import Certificates from './pages/Certificates'
import Availability from './pages/Availability'
import Usage from './pages/Usage'
import Audit from './pages/Audit'
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
  { to: '/firewall', label: 'Firewall' },
  { to: '/certificates', label: 'Сертификаты', badge: 'certs' as const },
  { to: '/audit', label: 'Журнал действий' },
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

function Shell({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const [theme, setTheme] = useTheme()
  const [showPassword, setShowPassword] = useState(false)
  const navigate = useNavigate()
  // The overview is polled anyway; reuse it to keep the sidebar badge current.
  const overview = useApi<Overview>('/overview', 60_000)
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

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-name">NetKnownsThat</div>
          <div className="brand-sub">
            {overview.data?.host.hostname ?? '…'} · режим {me.mode}
          </div>
        </div>

        <nav className="nav">
          {NAV.map((item) => (
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
        </div>
      </aside>

      <main className="main">
        <div className="content">
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
            <Route path="/firewall" element={<Firewall me={me} />} />
            <Route path="/certificates" element={<Certificates me={me} />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/login" element={<Navigate to="/" replace />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}
