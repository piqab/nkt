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
          { text: 'Руководство', link: '/guide/getting-started' },
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
          { text: 'Guide', link: '/en/guide/getting-started' },
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
        { text: 'Установка на хост', link: '/guide/getting-started' },
        { text: 'Хаб: много хостов', link: '/guide/hub' },
      ],
    },
  ]
}

function sidebarEn() {
  return [
    {
      text: 'Start',
      items: [
        { text: 'Install on a host', link: '/en/guide/getting-started' },
        { text: 'Hub: many hosts', link: '/en/guide/hub' },
      ],
    },
  ]
}
