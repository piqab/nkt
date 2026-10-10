import { useSyncExternalStore } from 'react'
import { api, hostScope } from './api'
import type { HubHost, OSInfo } from './types'

// Справочник ОС для значков перед именем там, куда передаётся только имя
// (заголовки окон консоли, журналов, экрана; сводки хаба). Списки сами
// записывают сюда ОС того, что показали; хосты хаба справочник при
// надобности подгружает одним запросом /hub/hosts.

/** Что за имя: хост хаба или объект внутри выбранного хоста. */
export type OsKind = 'host' | 'vm' | 'lxd' | 'docker' | 'podman'

const known = new Map<string, OSInfo>()
const listeners = new Set<() => void>()
let version = 0

function key(kind: OsKind, name: string): string {
  // Машины и контейнеры — внутри своего хоста: имена у разных хостов
  // совпадают.
  return kind === 'host' ? `host:${name}` : `${hostScope.id ?? 'self'}:${kind}:${name}`
}

function changed() {
  version++
  listeners.forEach((l) => l())
}

/** Запомнить ОС объектов списка (вызывать при загрузке списка). */
export function rememberOS(kind: OsKind, items: { name: string; os_info?: OSInfo }[] | undefined) {
  if (!items) return
  let dirty = false
  for (const it of items) {
    if (!it.os_info) continue
    const k = key(kind, it.name)
    const cur = known.get(k)
    if (cur?.id !== it.os_info.id || cur?.name !== it.os_info.name || cur?.source !== it.os_info.source) {
      known.set(k, it.os_info)
      dirty = true
    }
  }
  if (dirty) changed()
}

/** Хосты хаба: по имени и по id (сводки хаба знают только id). */
const hostByID = new Map<number, OSInfo>()
let hubFetch: Promise<void> | null = null
let hubFetchedAt = 0

export function rememberHubHosts(hosts: HubHost[] | undefined) {
  if (!hosts) return
  for (const h of hosts) if (h.os_info) hostByID.set(h.id, h.os_info)
  hubFetchedAt = Date.now()
  rememberOS('host', hosts)
}

function loadHubHosts() {
  if (hubFetch || Date.now() - hubFetchedAt < 60_000) return
  hubFetch = api<HubHost[]>('/hub/hosts')
    .then((hosts) => rememberHubHosts(hosts))
    .catch(() => {
      hubFetchedAt = Date.now() // не хаб или нет доступа — не повторять сразу
    })
    .finally(() => {
      hubFetch = null
    })
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

/** ОС по имени (или id хоста хаба); undefined — неизвестно. */
export function useOS(kind: OsKind, name?: string | null, hostID?: number | null): OSInfo | undefined {
  useSyncExternalStore(subscribe, () => version)
  if (kind === 'host' && hostID != null && hostByID.has(hostID)) return hostByID.get(hostID)
  if (!name) return undefined
  const os = known.get(key(kind, name))
  if (!os && kind === 'host') loadHubHosts()
  return os
}
