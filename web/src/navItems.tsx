import type { ReactNode } from 'react'
import {
  AlertOutlined,
  ApartmentOutlined,
  AppstoreOutlined,
  AuditOutlined,
  BellOutlined,
  BugOutlined,
  ClusterOutlined,
  CodeOutlined,
  DashboardOutlined,
  DesktopOutlined,
  FileTextOutlined,
  FundOutlined,
  HddOutlined,
  HeartOutlined,
  InfoCircleOutlined,
  PlayCircleOutlined,
  ProfileOutlined,
  RocketOutlined,
  SafetyCertificateOutlined,
  SafetyOutlined,
  SecurityScanOutlined,
  SettingOutlined,
  ShareAltOutlined,
  TeamOutlined,
  ToolOutlined,
  UserOutlined,
  WifiOutlined,
} from '@ant-design/icons'

/**
 * Разделы бокового меню хоста и хаба — порядок по умолчанию. Раскладку
 * (порядок, скрытые разделы хоста) администратор меняет в «О системе»
 * хаба, см. navLayout.ts.
 */

// Группы — по тому, чем человек занят: смотрит состояние, наблюдает за
// изменениями во времени, правит сам хост, разбирается с сетью, выдаёт
// доступ.
//
// Порядок разделов — от «что происходит» к «кому что можно»: сверху то,
// ради чего сюда заходят чаще всего. Плоский список без групп: у каждого
// раздела своя иконка, и в свёрнутом сайдбаре меню остаётся тем же
// столбцом иконок.
export const NAV_ITEMS: {
  to: string
  labelKey: string
  icon: ReactNode
  end?: boolean
  adminOnly?: boolean
  badge?: 'findings' | 'certs' | 'jobs'
}[] = [
  { to: '/', labelKey: 'nav.overview', icon: <DashboardOutlined />, end: true },
  { to: '/findings', labelKey: 'nav.findings', icon: <AlertOutlined />, badge: 'findings' },
  { to: '/vulnerabilities', labelKey: 'nav.vulnerabilities', icon: <BugOutlined /> },
  { to: '/topology', labelKey: 'nav.topology', icon: <ApartmentOutlined /> },
  { to: '/availability', labelKey: 'nav.availability', icon: <HeartOutlined /> },
  { to: '/usage', labelKey: 'nav.usage', icon: <FundOutlined /> },
  { to: '/logs', labelKey: 'nav.logs', icon: <FileTextOutlined /> },
  // Задания рядом с журналами: и то, и другое — «что происходило, пока я
  // не смотрел».
  { to: '/jobs', labelKey: 'nav.jobs', icon: <PlayCircleOutlined />, badge: 'jobs' },
  { to: '/audit', labelKey: 'nav.audit', icon: <AuditOutlined /> },
  { to: '/services', labelKey: 'nav.services', icon: <AppstoreOutlined /> },
  { to: '/containers', labelKey: 'nav.containers', icon: <ClusterOutlined /> },
  { to: '/packages', labelKey: 'nav.packages', icon: <ToolOutlined /> },
  { to: '/configs', labelKey: 'nav.configs', icon: <SettingOutlined /> },
  // Профили живут вкладкой в «Контейнеры и ВМ»: там же, где стеки compose
  // и заготовки машин, которыми профиль и распоряжается.
  { to: '/disks', labelKey: 'nav.disks', icon: <HddOutlined /> },
  { to: '/hardware', labelKey: 'nav.hardware', icon: <DesktopOutlined /> },
  { to: '/system', labelKey: 'nav.system', icon: <SafetyOutlined />, adminOnly: true },
  // Терминал viewer'у бесполезен: подключиться он всё равно не сможет, а
  // сервер откажет — поэтому скрыт, а не показан выключенным.
  { to: '/terminal', labelKey: 'nav.terminal', icon: <CodeOutlined />, adminOnly: true },
  { to: '/interfaces', labelKey: 'nav.interfaces', icon: <WifiOutlined /> },
  { to: '/firewall', labelKey: 'nav.firewall', icon: <ShareAltOutlined /> },
  { to: '/fail2ban', labelKey: 'nav.fail2ban', icon: <SecurityScanOutlined /> },
  { to: '/certificates', labelKey: 'nav.certificates', icon: <SafetyCertificateOutlined />, badge: 'certs' },
  // Учётки веб-интерфейса и учётки самой машины рядом, но по-прежнему
  // раздельно: путать их нельзя, вторые дают вход на сам сервер.
  { to: '/users', labelKey: 'nav.users', icon: <UserOutlined />, adminOnly: true },
  { to: '/os-users', labelKey: 'nav.osUsers', icon: <TeamOutlined />, adminOnly: true },
]

/** Разделы меню хаба: ключ — вид раздела хаба (hubView в App). */
export const HUB_NAV: { key: string; labelKey: string; icon: ReactNode }[] = [
  { key: 'hosts', labelKey: 'hosts.title', icon: <ClusterOutlined /> },
  { key: 'events', labelKey: 'events.title', icon: <BellOutlined /> },
  { key: 'fail2ban', labelKey: 'nav.fail2ban', icon: <SecurityScanOutlined /> },
  { key: 'jobs', labelKey: 'nav.jobs', icon: <PlayCircleOutlined /> },
  { key: 'profiles', labelKey: 'nav.profiles', icon: <ProfileOutlined /> },
  { key: 'clusters', labelKey: 'nav.clusters', icon: <ClusterOutlined /> },
  { key: 'deploy', labelKey: 'nav.deploy', icon: <RocketOutlined /> },
  { key: 'about', labelKey: 'nav.about', icon: <InfoCircleOutlined /> },
]
