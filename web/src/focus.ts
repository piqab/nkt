import { useEffect, useState } from 'react'

/**
 * Переход «к ответственному» из находки или карты ресурсов: раздел
 * открывается по ссылке с ?focus=<имя>, нужная строка таблицы подсвечена
 * (класс row-focus) и прокручена в видимую часть.
 */
export function useFocusRow(ready: boolean): string | null {
  const [focus] = useState(() => new URLSearchParams(location.search).get('focus'))
  useEffect(() => {
    if (!focus || !ready) return
    const id = window.setTimeout(() => document.querySelector('.row-focus')?.scrollIntoView({ block: 'center' }), 150)
    return () => window.clearTimeout(id)
  }, [focus, ready])
  return focus
}

/** Вкладка из ?tab=, если она из допустимых. */
export function tabFromQuery<T extends string>(allowed: readonly T[], fallback: T): T {
  const tab = new URLSearchParams(location.search).get('tab') as T | null
  return tab && allowed.includes(tab) ? tab : fallback
}

/** Службы, у которых есть строка в «Сервисах». */
export const SERVICE_PAGE_NAMES = new Set(['nginx', 'haproxy', 'caddy', 'docker', 'podman', 'lxd', 'libvirt', 'ufw', 'firewalld', 'fail2ban'])

/** Куда вести с узла карты ресурсов: путь и подпись (ключ перевода). */
export function nodeTarget(n: { kind: string; label: string; group?: string; meta?: Record<string, string> }): { to: string; labelKey: string; name?: string } | null {
  const q = (p: Record<string, string>) => '?' + new URLSearchParams(p).toString()
  const service = n.meta?.service || n.group || ''
  switch (n.kind) {
    case 'service':
      return { to: '/services' + q({ focus: n.label }), labelKey: 'nav.toService', name: n.label }
    case 'endpoint':
    case 'upstream':
      if (n.meta?.file) return { to: '/configs' + q({ path: n.meta.file }), labelKey: 'nav.toConfig', name: n.meta.file }
      break
    case 'container':
      return { to: '/containers' + q({ tab: 'docker', focus: n.label }), labelKey: 'nav.toContainer', name: n.label }
    case 'podman_container':
      return { to: '/containers' + q({ tab: 'podman', focus: n.label }), labelKey: 'nav.toContainer', name: n.label }
    case 'lxd_instance':
      return { to: '/containers' + q({ tab: 'lxd' }), labelKey: 'nav.toSection', name: 'LXD' }
    case 'vm':
      return { to: '/containers' + q({ tab: 'vms' }), labelKey: 'nav.toSection', name: 'VM' }
    case 'network':
      return { to: '/interfaces', labelKey: 'nav.toSection', name: n.label }
    case 'undeclared':
      return { to: '/firewall', labelKey: 'nav.toFirewall' }
  }
  if (n.kind.startsWith('k8s_')) return { to: '/containers' + q({ tab: 'k8s' }), labelKey: 'nav.toSection', name: 'Kubernetes' }
  if (SERVICE_PAGE_NAMES.has(service)) return { to: '/services' + q({ focus: service }), labelKey: 'nav.toService', name: service }
  return null
}
