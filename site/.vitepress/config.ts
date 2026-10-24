import { defineConfig } from 'vitepress'

// Сайт nkt: лендинг и руководство на двух языках. Русский — корень,
// английский — /en/; первый заход на корень перебрасывает по языку
// браузера (см. скрипт в head), дальше выбор посетителя запоминается.
// Публикуется на GitHub Pages репозитория, отсюда base.
export default defineConfig({
  base: '/nkt/',
  cleanUrls: true,
  lastUpdated: false,
  head: [
    ['link', { rel: 'icon', href: '/nkt/favicon.svg', type: 'image/svg+xml' }],
    [
      'script',
      {},
      `(function(){try{var b='/nkt/';var p=location.pathname;var en=p.indexOf(b+'en/')===0||p===b+'en';var k='nkt-site-lang';var s=localStorage.getItem(k);if(p===b||p===b+'index.html'){if(!s){var l=(navigator.languages&&navigator.languages[0])||navigator.language||'';if(l.slice(0,2).toLowerCase()!=='ru'){localStorage.setItem(k,'en');location.replace(b+'en/');return}}}localStorage.setItem(k,en?'en':'ru')}catch(e){}})();`,
    ],
  ],
  locales: {
    root: {
      label: 'Русский',
      lang: 'ru',
      title: 'nkt',
      description: 'NetKnownsThat — панель управления Linux-хостами: конфиги, сервисы, контейнеры, firewall, сертификаты, много хостов из одного хаба.',
      themeConfig: {
        nav: [
          { text: 'Руководство', link: '/guide/intro' },
          { text: 'Возможности', link: '/features' },
          { text: 'Релизы', link: 'https://github.com/piqab/nkt/releases' },
        ],
        sidebar: {
          '/guide/': sidebarRu(),
        },
        outline: { label: 'На этой странице', level: [2, 3] },
        docFooter: { prev: 'Назад', next: 'Дальше' },
        returnToTopLabel: 'Наверх',
        sidebarMenuLabel: 'Меню',
        darkModeSwitchLabel: 'Тема',
        langMenuLabel: 'Язык',
        editLink: { pattern: 'https://github.com/piqab/nkt/edit/main/site/:path', text: 'Поправить страницу на GitHub' },
        search: { provider: 'local', options: { locales: { root: { translations: { button: { buttonText: 'Поиск', buttonAriaLabel: 'Поиск' }, modal: { noResultsText: 'Ничего не найдено', resetButtonTitle: 'Очистить', footer: { selectText: 'открыть', navigateText: 'перейти', closeText: 'закрыть' } } } } } } },
      },
    },
    en: {
      label: 'English',
      lang: 'en',
      title: 'nkt',
      description: 'NetKnownsThat — a control panel for Linux hosts: configs, services, containers, firewall, certificates, many hosts from one hub.',
      themeConfig: {
        nav: [
          { text: 'Guide', link: '/en/guide/intro' },
          { text: 'Features', link: '/en/features' },
          { text: 'Releases', link: 'https://github.com/piqab/nkt/releases' },
        ],
        sidebar: {
          '/en/guide/': sidebarEn(),
        },
        outline: { label: 'On this page', level: [2, 3] },
        editLink: { pattern: 'https://github.com/piqab/nkt/edit/main/site/:path', text: 'Edit this page on GitHub' },
      },
    },
  },
  themeConfig: {
    logo: '/favicon.svg',
    socialLinks: [{ icon: 'github', link: 'https://github.com/piqab/nkt' }],
    search: { provider: 'local' },
  },
})

function sidebarRu() {
  return [
    {
      text: 'Начало',
      items: [
        { text: 'Что такое nkt', link: '/guide/intro' },
        { text: 'Командная строка и настройка', link: '/guide/cli' },
      ],
    },
    {
      text: 'Установка',
      items: [
        { text: 'На хост', link: '/guide/getting-started' },
        { text: 'Хаб', link: '/guide/install-hub' },
        { text: 'Порты и доступ', link: '/guide/ports' },
      ],
    },
    {
      text: 'Хост',
      items: [
        { text: 'Обзор и проблемы', link: '/guide/overview' },
        { text: 'Наблюдение', link: '/guide/monitoring' },
        { text: 'Сервисы', link: '/guide/services' },
        { text: 'Контейнеры, ВМ, Kubernetes', link: '/guide/containers' },
        { text: 'Конфигурации', link: '/guide/configs' },
        { text: 'Свои сервисы', link: '/guide/service-descriptions' },
        { text: 'Профили', link: '/guide/profiles' },
        { text: 'Формат профиля', link: '/guide/profile-format' },
        { text: 'Пакеты, диски, файлы, система', link: '/guide/system' },
        { text: 'Сеть и firewall', link: '/guide/network' },
        { text: 'fail2ban', link: '/guide/fail2ban' },
        { text: 'Сертификаты', link: '/guide/certificates' },
        { text: 'Доступ и безопасность', link: '/guide/access' },
      ],
    },
    {
      text: 'Хаб',
      items: [
        { text: 'Хаб: много хостов', link: '/guide/hub' },
        { text: 'Хосты', link: '/guide/hub-hosts' },
        { text: 'Сценарии', link: '/guide/hub-scripts' },
        { text: 'Команды сценариев', link: '/guide/script-commands' },
        { text: 'Кластеры Kubernetes', link: '/guide/hub-clusters' },
        { text: 'Кластер: от нуля до пода', link: '/guide/k8s-first-pod' },
        { text: 'Оповещения, задания, ИИ', link: '/guide/hub-operations' },
        { text: 'Ассистент хаба', link: '/guide/hub-assistant' },
        { text: 'Мониторинг хаба', link: '/guide/hub-monitoring' },
        { text: 'Обновления и база уязвимостей', link: '/guide/hub-updates' },
        { text: 'Кэш пакетов', link: '/guide/hub-cache' },
        { text: 'API и токены', link: '/guide/hub-api' },
        { text: 'MCP для ИИ-агентов', link: '/guide/hub-mcp' },
        { text: 'Плагины', link: '/guide/hub-plugins' },
        { text: 'n8n', link: '/guide/n8n' },
        { text: 'Боты', link: '/guide/bots' },
        { text: 'Мобильное приложение', link: '/guide/android' },
        { text: 'Пример: автоматизация', link: '/guide/case-automation' },
      ],
    },
    {
      text: 'Выкладки (CI/CD)',
      items: [
        { text: 'Выкладки', link: '/guide/hub-deploy' },
        { text: 'nkt-edge', link: '/guide/edge' },
        { text: 'Traffic Fly', link: '/guide/traffic-fly' },
        { text: 'Перенос стека с Traffic Fly', link: '/guide/migrate-with-fly' },
        { text: 'Примеры CI/CD', link: '/guide/cicd-examples' },
      ],
    },
    {
      text: 'Справочник',
      items: [
        { text: 'Настройка', link: '/guide/reference-config' },
        { text: 'Безопасность', link: '/guide/reference-security' },
        { text: 'API', link: '/guide/reference-api' },
        { text: 'Ограничения', link: '/guide/reference-limitations' },
        { text: 'Решение проблем', link: '/guide/troubleshooting' },
      ],
    },
    {
      text: 'Разработка',
      items: [{ text: 'Сборка, тесты, устройство', link: '/guide/development' }],
    },
  ]
}

function sidebarEn() {
  return [
    {
      text: 'Start',
      items: [
        { text: 'What nkt is', link: '/en/guide/intro' },
        { text: 'Command line and configuration', link: '/en/guide/cli' },
      ],
    },
    {
      text: 'Installation',
      items: [
        { text: 'On a host', link: '/en/guide/getting-started' },
        { text: 'Hub', link: '/en/guide/install-hub' },
        { text: 'Ports and access', link: '/en/guide/ports' },
      ],
    },
    {
      text: 'Host',
      items: [
        { text: 'Overview and findings', link: '/en/guide/overview' },
        { text: 'Monitoring', link: '/en/guide/monitoring' },
        { text: 'Services', link: '/en/guide/services' },
        { text: 'Containers, VMs, Kubernetes', link: '/en/guide/containers' },
        { text: 'Configs', link: '/en/guide/configs' },
        { text: 'Your own services', link: '/en/guide/service-descriptions' },
        { text: 'Profiles', link: '/en/guide/profiles' },
        { text: 'Profile format', link: '/en/guide/profile-format' },
        { text: 'Packages, disks, files, system', link: '/en/guide/system' },
        { text: 'Network and firewall', link: '/en/guide/network' },
        { text: 'fail2ban', link: '/en/guide/fail2ban' },
        { text: 'Certificates', link: '/en/guide/certificates' },
        { text: 'Access and security', link: '/en/guide/access' },
      ],
    },
    {
      text: 'Hub',
      items: [
        { text: 'Hub: many hosts', link: '/en/guide/hub' },
        { text: 'Hosts', link: '/en/guide/hub-hosts' },
        { text: 'Scripts', link: '/en/guide/hub-scripts' },
        { text: 'Script commands', link: '/en/guide/script-commands' },
        { text: 'Kubernetes clusters', link: '/en/guide/hub-clusters' },
        { text: 'Cluster: zero to first pod', link: '/en/guide/k8s-first-pod' },
        { text: 'Alerts, jobs, AI', link: '/en/guide/hub-operations' },
        { text: 'Hub assistant', link: '/en/guide/hub-assistant' },
        { text: 'Hub monitoring', link: '/en/guide/hub-monitoring' },
        { text: 'Updates and vulnerability DB', link: '/en/guide/hub-updates' },
        { text: 'Package cache', link: '/en/guide/hub-cache' },
        { text: 'API and tokens', link: '/en/guide/hub-api' },
        { text: 'MCP for AI agents', link: '/en/guide/hub-mcp' },
        { text: 'Plugins', link: '/en/guide/hub-plugins' },
        { text: 'n8n', link: '/en/guide/n8n' },
        { text: 'Bots', link: '/en/guide/bots' },
        { text: 'Mobile app', link: '/en/guide/android' },
        { text: 'Example: automation', link: '/en/guide/case-automation' },
      ],
    },
    {
      text: 'Deployments (CI/CD)',
      items: [
        { text: 'Deployments', link: '/en/guide/hub-deploy' },
        { text: 'nkt-edge', link: '/en/guide/edge' },
        { text: 'Traffic Fly', link: '/en/guide/traffic-fly' },
        { text: 'Moving a stack with Traffic Fly', link: '/en/guide/migrate-with-fly' },
        { text: 'CI/CD examples', link: '/en/guide/cicd-examples' },
      ],
    },
    {
      text: 'Reference',
      items: [
        { text: 'Configuration', link: '/en/guide/reference-config' },
        { text: 'Security', link: '/en/guide/reference-security' },
        { text: 'API', link: '/en/guide/reference-api' },
        { text: 'Limitations', link: '/en/guide/reference-limitations' },
        { text: 'Troubleshooting', link: '/en/guide/troubleshooting' },
      ],
    },
    {
      text: 'Development',
      items: [{ text: 'Build, tests, internals', link: '/en/guide/development' }],
    },
  ]
}
