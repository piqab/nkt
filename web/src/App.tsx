import { useCallback, useEffect, useState } from 'react'
import { ConfigProvider, Layout, Menu, type MenuProps, type ThemeConfig } from 'antd'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import { LOCAL_HOST_ID, api, hostScope, onUnauthorized, readSelectedHost, type SelectedHost, useApi, writeSelectedHost } from './api'
import { buildAntdTheme, resolveIsDark, type Theme } from './theme'
import type { Lang } from './i18n'
import { useLang } from './hooks/useLang'
import type { HubVersionInfo, Me, Overview } from './types'
import Login from './pages/Login'
import Hosts from './pages/Hosts'
import About from './pages/About'
import OverviewPage from './pages/Overview'
import Findings from './pages/Findings'
import Vulnerabilities from './pages/Vulnerabilities'
import TopologyPage from './pages/Topology'
import Configs from './pages/Configs'
import LogsPage from './pages/Logs'
import JobsPage from './pages/Jobs'
import ProfilesPage from './pages/Profiles'
import VMImagesPage from './pages/VMImages'
import Services from './pages/Services'
import Containers from './pages/Containers'
import Packages from './pages/Packages'
import TerminalPage from './pages/Terminal'
import Firewall from './pages/Firewall'
import Interfaces from './pages/Interfaces'
import Certificates from './pages/Certificates'
import Availability from './pages/Availability'
import Usage from './pages/Usage'
import Audit from './pages/Audit'
import Users from './pages/Users'
import Disks from './pages/Disks'
import ErrorBoundary from './components/ErrorBoundary'
import HardwarePage from './pages/Hardware'
import SystemSettingsPage from './pages/SystemSettings'
import OSUsers from './pages/OSUsers'
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

// label is a translation key (resolved via t() where NAV is rendered), not
// the display text itself — NAV is a module-level constant built once, long
// before i18next has necessarily finished initializing, and it must not go
// stale when the language changes later.
// Разделы сгруппированы по назначению: двадцать с лишним пунктов плоским
// списком читаются как свалка, в которой нужное ищут глазами каждый раз.
// Группы — по тому, чем человек занят: смотрит состояние, наблюдает за
// изменениями во времени, правит сам хост, разбирается с сетью, выдаёт
// доступ.
//
// Порядок групп — от «что происходит» к «кому что можно»: сверху то, ради
// чего сюда заходят чаще всего.
const NAV_GROUPS: {
  key: string
  labelKey: string
  items: { to: string; labelKey: string; end?: boolean; adminOnly?: boolean; badge?: 'findings' | 'certs' | 'jobs' }[]
}[] = [
  {
    key: 'state',
    labelKey: 'navGroup.state',
    items: [
      { to: '/', labelKey: 'nav.overview', end: true },
      { to: '/findings', labelKey: 'nav.findings', badge: 'findings' },
      { to: '/vulnerabilities', labelKey: 'nav.vulnerabilities' },
      { to: '/topology', labelKey: 'nav.topology' },
    ],
  },
  {
    key: 'watch',
    labelKey: 'navGroup.watch',
    items: [
      { to: '/availability', labelKey: 'nav.availability' },
      { to: '/usage', labelKey: 'nav.usage' },
      { to: '/logs', labelKey: 'nav.logs' },
      // Задания рядом с журналами: и то, и другое — «что происходило,
      // пока я не смотрел».
      { to: '/jobs', labelKey: 'nav.jobs', badge: 'jobs' },
      { to: '/audit', labelKey: 'nav.audit' },
    ],
  },
  {
    key: 'host',
    labelKey: 'navGroup.host',
    items: [
      { to: '/services', labelKey: 'nav.services' },
      { to: '/containers', labelKey: 'nav.containers' },
      // Образы машин рядом с контейнерами и ВМ: это заготовки для них.
      { to: '/vm-images', labelKey: 'nav.vmImages', adminOnly: true },
      { to: '/packages', labelKey: 'nav.packages' },
      { to: '/configs', labelKey: 'nav.configs' },
      // Профиль описывает желаемое состояние — соседство с конфигурациями
      // не случайно: это следующий уровень той же работы.
      { to: '/profiles', labelKey: 'nav.profiles', adminOnly: true },
      { to: '/disks', labelKey: 'nav.disks' },
      { to: '/hardware', labelKey: 'nav.hardware' },
      { to: '/system', labelKey: 'nav.system', adminOnly: true },
      // Терминал viewer'у бесполезен: подключиться он всё равно не сможет,
      // а сервер откажет — поэтому скрыт, а не показан выключенным.
      { to: '/terminal', labelKey: 'nav.terminal', adminOnly: true },
    ],
  },
  {
    key: 'network',
    labelKey: 'navGroup.network',
    items: [
      { to: '/interfaces', labelKey: 'nav.interfaces' },
      { to: '/firewall', labelKey: 'nav.firewall' },
      { to: '/certificates', labelKey: 'nav.certificates', badge: 'certs' },
    ],
  },
  {
    key: 'access',
    labelKey: 'navGroup.access',
    items: [
      // Учётки веб-интерфейса и учётки самой машины рядом, но по-прежнему
      // раздельно: путать их нельзя, вторые дают вход на сам сервер.
      { to: '/users', labelKey: 'nav.users', adminOnly: true },
      { to: '/os-users', labelKey: 'nav.osUsers', adminOnly: true },
    ],
  },
]

/** Ключ, под которым запоминается, какие группы меню развёрнуты. */
const NAV_OPEN_KEY = 'nkt-nav-open'


export default function App() {
  const { t } = useTranslation()
  const [me, setMe] = useState<Me | null>(null)
  const [checked, setChecked] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()
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

  // A terminal "откреплённый" into its own browser window (see Terminal.tsx's
  // "Открепить" button, window.open('/terminal/popout', ...)) is a second,
  // separate load of this very app — the session cookie is already shared
  // automatically since it's the same origin, so the only thing actually
  // needed here is skipping Shell's sidebar/nav chrome entirely: a popped-out
  // window is meant to be just the terminal, not a second copy of the whole
  // dashboard. Checked ahead of Shell, not inside it, so Shell's own hooks
  // (several, further down) stay unconditional — this way they're simply
  // never reached for a popout window's render tree at all, rather than
  // conditionally skipped mid-component.
  //
  // hostScope itself does NOT come for free though: it lives in a plain
  // module-level variable (api.ts), not localStorage, and is normally only
  // ever set by Shell's own render body below — which a popout never runs.
  // Left unset, every request from this window would silently fall back to
  // hostScope.id's default (null, "the hub's own API") instead of the
  // managed host actually selected — on a hub, that host almost certainly
  // has no terminal of its own enabled, which is exactly the "Терминал
  // может быть выключен на сервере" this used to show.
  //
  // The host id comes from THIS window's own URL (?host=), not from
  // readSelectedHost()'s shared localStorage entry — several popout
  // windows can be open at once, one per managed host (see Terminal.tsx's
  // openPopout, which gives each host its own window name too), and they
  // would otherwise all fight over that one shared "currently selected"
  // value instead of each staying pinned to the host it was opened for.
  // The localStorage fallback only matters if this URL is ever opened by
  // hand without the param.
  const isPopoutTerminal = me?.is_admin && location.pathname === '/terminal/popout'
  const isPopoutLogs = !!me && location.pathname === '/logs/popout'
  if ((isPopoutTerminal || isPopoutLogs) && me) {
    if (me.mode !== 'hub') {
      hostScope.id = null
    } else {
      const hostParam = new URLSearchParams(location.search).get('host')
      hostScope.id = hostParam !== null && hostParam !== '' ? Number(hostParam) : (readSelectedHost()?.id ?? null)
    }
  }

  return (
    <ConfigProvider theme={antdTheme}>
      {!checked ? (
        <div className="login-wrap">{t('app.loading')}</div>
      ) : !me ? (
        <Routes>
          <Route path="/login" element={<Login onSuccess={loadMe} />} />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      ) : isPopoutTerminal ? (
        <TerminalPage me={me} />
      ) : isPopoutLogs ? (
        <LogsPage />
      ) : (
        <Shell me={me} theme={theme} setTheme={setTheme} onLogout={() => setMe(null)} />
      )}
    </ConfigProvider>
  )
}

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
  const { t } = useTranslation()
  const [lang, setLang] = useLang()
  const [showPassword, setShowPassword] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()
  const isHub = me.mode === 'hub'

  // The terminal's own WebSocket+xterm.js session (see usePty) is torn down
  // the moment its owning component unmounts — desired when the user
  // explicitly closes it, not just because they clicked over to another
  // page and back. Rendering TerminalPage as an ordinary <Route> element
  // would unmount it on every navigation away from /terminal, killing the
  // live shell (and, for a non-tmux session, the remote process itself —
  // see usePty's own comment on why). Instead it renders once (lazily, the
  // first time /terminal is visited — most sessions never open it at all)
  // as a sibling of <Routes>, toggled by plain CSS visibility instead of by
  // mounting/unmounting, so navigating elsewhere and back finds the same
  // live session still connected. The <Route> below still has to match
  // "/terminal" itself (element: null) — without it, <Routes>' own
  // catch-all would redirect away from that path entirely, since nothing
  // else in the switch claims it.
  // Какая группа меню раскрыта — состояние человека, а не приложения:
  // запоминается между заходами, чтобы каждый раз не раскрывать заново.
  const [openGroups, setOpenGroups] = useState<string[]>(() => {
    try {
      const saved = localStorage.getItem(NAV_OPEN_KEY)
      if (saved) return JSON.parse(saved) as string[]
    } catch {
      // Недоступное хранилище — просто раскрываем всё.
    }
    return NAV_GROUPS.map((g) => g.key)
  })

  // Пункт меню — самый длинный подходящий путь: «/» иначе подсвечивался бы
  // на каждой странице, потому что с него начинается любой адрес.
  const navSelectedKey =
    NAV_GROUPS.flatMap((g) => g.items)
      .map((item) => item.to)
      .filter((to) => (to === '/' ? location.pathname === '/' : location.pathname.startsWith(to)))
      .sort((a, b) => b.length - a.length)[0] ?? '/'

  const isTerminalRoute = location.pathname === '/terminal'
  const [terminalMounted, setTerminalMounted] = useState(isTerminalRoute)
  useEffect(() => {
    if (isTerminalRoute) setTerminalMounted(true)
  }, [isTerminalRoute])

  const [selectedHost, setSelectedHost] = useState<SelectedHost | null>(() => (isHub ? readSelectedHost() : null))
  // Which of the two host-picker-level sections is showing — the host
  // registry itself, or the hub's own "О системе" page. Plain local state
  // rather than a route: the picker renders outside <Routes> entirely (see
  // showingHostPicker below), and selectHost's own navigate('/') already
  // depends on the address bar staying whatever it was from a previous
  // host session — introducing routing here would have to interact with
  // that, for no real benefit (this is not something worth bookmarking).
  const [hubView, setHubView] = useState<'hosts' | 'jobs' | 'about'>('hosts')
  // Polled independently of whichever section is actually showing, so the
  // sidebar's own "доступно обновление" badge stays current even while
  // looking at the host list — matches how criticalCount/certAlerts below
  // are always live regardless of which per-host page is open.
  const hubUpdate = useApi<HubVersionInfo>(isHub ? '/hub/version' : null, 5 * 60_000)
  // Счётчик заданий самого хаба: он и подсказывает, что раздел «Задания»
  // здесь есть. Путь указан явно — область запросов в списке хостов не
  // выбрана, и обычный «/jobs» ушёл бы в API хаба, где их нет.
  const hubJobs = useApi<{ active: number }>(isHub ? '/hosts/local/jobs?limit=1' : null, 10_000)

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
  // Задания самого хаба (создание машин, раскатка профилей) живут на его
  // машине, поэтому этот раздел смотрит в её API. Без этого запросы ушли
  // бы в API хаба, где раздела заданий нет вовсе.
  if (isHub && !selectedHost && hubView === 'jobs') hostScope.id = LOCAL_HOST_ID

  function selectHost(host: SelectedHost | null) {
    setSelectedHost(host)
    writeSelectedHost(host)
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
  // Счётчик идущих заданий: вернувшись в браузер, оператор должен сразу
  // видеть, что работа ещё идёт, не заходя в раздел. Опрос частый —
  // задание может закончиться в любой момент, а число в меню, которое
  // врёт минуту, хуже отсутствующего.
  const jobs = useApi<{ active: number }>(showingHostPicker ? null : '/jobs?limit=1', 10_000)
  const activeJobs = jobs.data?.active ?? 0

  async function logout() {
    await api('/auth/logout', { method: 'POST' }).catch(() => undefined)
    onLogout()
    navigate('/login', { replace: true })
  }

  if (showingHostPicker) {
    return (
      <Layout className="shell">
        <Layout.Sider className="sidebar" width={208} theme="light" breakpoint="lg" collapsedWidth={0}>
          <div className="brand">
            <div className="brand-name">NetKnownsThat</div>
            <div className="brand-sub">{t('app.brandSub')}</div>
          </div>

          <nav className="nav">
            <a href="#" className={hubView === 'hosts' ? 'active' : ''} onClick={(e) => { e.preventDefault(); setHubView('hosts') }}>
              <span>{t('hosts.title')}</span>
            </a>
            <a href="#" className={hubView === 'jobs' ? 'active' : ''} onClick={(e) => { e.preventDefault(); setHubView('jobs') }}>
              <span>{t('nav.jobs')}</span>
              {hubJobs.data?.active ? <span className="nav-count nav-count-busy">{hubJobs.data.active}</span> : null}
            </a>
            <a href="#" className={hubView === 'about' ? 'active' : ''} onClick={(e) => { e.preventDefault(); setHubView('about') }}>
              <span>{t('nav.about')}</span>
              {hubUpdate.data?.update_available && <span className="nav-count">1</span>}
            </a>
          </nav>

          <div className="sidebar-foot">
            <div className="row" style={{ marginBottom: '0.4rem' }}>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                {t('app.theme')}
                <select value={theme} onChange={(e) => setTheme(e.target.value as Theme)}>
                  <option value="auto">{t('app.themeAuto')}</option>
                  <option value="light">{t('app.themeLight')}</option>
                  <option value="dark">{t('app.themeDark')}</option>
                </select>
              </label>
            </div>
            <div className="row" style={{ marginBottom: '0.4rem' }}>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                {t('app.language')}
                <select value={lang} onChange={(e) => setLang(e.target.value as Lang)}>
                  <option value="ru">Русский</option>
                  <option value="en">English</option>
                </select>
              </label>
            </div>
            <div>
              {me.username} · {me.role}
            </div>
            <div className="row" style={{ gap: '0.25rem' }}>
              <button className="ghost" onClick={logout}>
                {t('app.logout')}
              </button>
            </div>
          </div>
        </Layout.Sider>
        <Layout.Content className="main">
          <div className="content">
            {hubView === 'hosts' ? (
              <Hosts onSelect={selectHost} hubVersion={me.hub_version} />
            ) : hubView === 'jobs' ? (
              <JobsPage me={me} />
            ) : (
              <About />
            )}
          </div>
        </Layout.Content>
      </Layout>
    )
  }

  const shell = (
    <Layout className="shell">
      <Layout.Sider className="sidebar" width={208} theme="light" breakpoint="lg" collapsedWidth={0}>
        <div className="brand">
          <div className="brand-name">NetKnownsThat</div>
          {!isHub && (
            <div className="brand-sub">
              {overview.data?.host.hostname ?? '…'} · {t('app.mode')} {me.mode}
            </div>
          )}
        </div>

        <Menu
          mode="inline"
          className="nav-menu"
          selectedKeys={[navSelectedKey]}
          openKeys={openGroups}
          onOpenChange={(keys: string[]) => {
            const next = keys
            setOpenGroups(next)
            try {
              localStorage.setItem(NAV_OPEN_KEY, JSON.stringify(next))
            } catch {
              // Приватный режим браузера — состояние просто не запомнится.
            }
          }}
          onClick={({ key }: { key: string }) => navigate(key)}
          items={(NAV_GROUPS.map((group) => ({
            key: group.key,
            label: t(group.labelKey),
            children: group.items
              .filter((item) => !item.adminOnly || me.is_admin)
              .map((item) => ({
                key: item.to,
                label: (
                  <span className="nav-item-label">
                    {t(item.labelKey)}
                    {item.badge === 'findings' && criticalCount > 0 && (
                      <span className="nav-count">{criticalCount}</span>
                    )}
                    {item.badge === 'certs' && certAlerts > 0 && <span className="nav-count">{certAlerts}</span>}
                    {item.badge === 'jobs' && activeJobs > 0 && <span className="nav-count nav-count-busy">{activeJobs}</span>}
                  </span>
                ),
              })),
          })).filter((group) => group.children.length > 0) as MenuProps['items'])}
        />

        <div className="sidebar-foot">
          <div className="row" style={{ marginBottom: '0.4rem' }}>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              {t('app.theme')}
              <select value={theme} onChange={(e) => setTheme(e.target.value as Theme)}>
                <option value="auto">{t('app.themeAuto')}</option>
                <option value="light">{t('app.themeLight')}</option>
                <option value="dark">{t('app.themeDark')}</option>
              </select>
            </label>
          </div>
          <div className="row" style={{ marginBottom: '0.4rem' }}>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
              {t('app.language')}
              <select value={lang} onChange={(e) => setLang(e.target.value as Lang)}>
                <option value="ru">Русский</option>
                <option value="en">English</option>
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
                  {t('app.changePassword')}
                </button>
                <button className="ghost" onClick={logout}>
                  {t('app.logout')}
                </button>
              </div>
            </>
          )}
        </div>
      </Layout.Sider>

      <Layout.Content className="main">
        {/* key remounts every page below on host switch, so their useApi()
            calls re-fetch scoped to the newly selected host instead of
            showing stale data from the previous one. */}
        <div className="content" key={isHub ? selectedHost!.id : 'local'}>
          {showPassword && (
            <Card
              title={t('app.changePasswordTitle')}
              actions={
                <button className="ghost" onClick={() => setShowPassword(false)}>
                  {t('app.close')}
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
              <strong>{t('app.simulatedModeTitle')}</strong>{' '}
              <Trans i18nKey="app.simulatedModeBody" components={{ code: <code className="mono" /> }} />
            </Banner>
          )}
          {!me.allow_mutations && (
            <Banner kind="info">
              <Trans i18nKey="app.readOnlyMode" components={{ code: <code className="mono" /> }} />
            </Banner>
          )}

          {/* Граница вокруг всего блока разделов, но со сбросом при смене
              адреса (key): сломавшийся раздел показывает карточку с
              ошибкой вместо белой страницы, а переход в другой раздел
              возвращает интерфейс к жизни сам, без перезагрузки. */}
          <ErrorBoundary key={location.pathname} section={location.pathname}>
            <Routes>
              <Route path="/" element={<OverviewPage me={me} />} />
              <Route path="/findings" element={<Findings />} />
              <Route path="/vulnerabilities" element={<Vulnerabilities me={me} />} />
              <Route path="/topology" element={<TopologyPage />} />
              <Route path="/availability" element={<Availability />} />
              <Route path="/usage" element={<Usage me={me} />} />
              <Route path="/configs" element={<Configs me={me} />} />
              <Route path="/logs" element={<LogsPage />} />
              <Route path="/jobs" element={<JobsPage me={me} />} />
              <Route path="/profiles" element={<ProfilesPage me={me} />} />
              <Route path="/vm-images" element={<VMImagesPage me={me} />} />
              <Route path="/services" element={<Services me={me} />} />
              <Route path="/containers" element={<Containers me={me} />} />
              <Route path="/packages" element={<Packages me={me} />} />
              {/* Docker/Podman/LXD/ВМ were separate nav entries before —
                  redirect their old URLs to the merged page's default tab
                  rather than a bare 404 for anyone with these bookmarked. */}
              <Route path="/podman" element={<Navigate to="/containers" replace />} />
              <Route path="/lxd" element={<Navigate to="/containers" replace />} />
              <Route path="/vms" element={<Navigate to="/containers" replace />} />
              {me.is_admin && <Route path="/terminal" element={null} />}
              <Route path="/firewall" element={<Firewall me={me} />} />
              <Route path="/interfaces" element={<Interfaces />} />
              <Route path="/certificates" element={<Certificates me={me} />} />
              <Route path="/audit" element={<Audit />} />
              <Route path="/disks" element={<Disks />} />
              <Route path="/hardware" element={<HardwarePage />} />
              {me.is_admin && <Route path="/system" element={<SystemSettingsPage me={me} />} />}
              {me.is_admin && <Route path="/users" element={<Users me={me} />} />}
              {me.is_admin && <Route path="/os-users" element={<OSUsers me={me} />} />}
              <Route path="/login" element={<Navigate to="/" replace />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </ErrorBoundary>
          {me.is_admin && terminalMounted && (
            <div style={{ display: isTerminalRoute ? 'contents' : 'none' }}>
              <TerminalPage me={me} />
            </div>
          )}
        </div>
      </Layout.Content>
    </Layout>
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
          {t('app.backToHostList')}
        </button>
        <span className="hub-topbar-brand">{t('app.hubBrand')}</span>
        <span className="hub-topbar-sep">→</span>
        <span className="hub-topbar-host">{selectedHost!.name}</span>
        <span className="hub-topbar-spacer" />
        <span className="small muted">
          {me.username} · {me.role}
        </span>
        <button className="ghost" onClick={logout}>
          {t('app.logout')}
        </button>
      </div>
      {shell}
    </div>
  )
}
