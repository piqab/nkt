// Browser Notification integration for the hub's host list (Hosts.tsx) —
// fires when a host picks up a new critical/high finding, or loses
// reachability. Deliberately tab-scoped: this only fires while Hosts.tsx is
// mounted and polling, not a true closed-tab push notification (that would
// need a Service Worker + the Push API, which nothing here calls for). The
// underlying data itself stays fresh regardless of any open tab — see
// internal/hub's pollOverviews — only the notification's *delivery* is
// tied to the tab.

import type { HubHost } from './types'

const STORAGE_KEY = 'nkt-hub-notify'

export function notificationsEnabled(): boolean {
  return localStorage.getItem(STORAGE_KEY) === '1'
}

export function setNotificationsEnabled(on: boolean): void {
  localStorage.setItem(STORAGE_KEY, on ? '1' : '0')
}

/** Requests browser permission — must be called from a user gesture (a
 * click handler), never on mount or on a timer; browsers ignore or reject
 * a request that isn't triggered by one. */
export async function requestNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) return false
  if (Notification.permission === 'granted') return true
  if (Notification.permission === 'denied') return false
  const result = await Notification.requestPermission()
  return result === 'granted'
}

type HostSnapshot = { criticalHigh: number; reachable: boolean | undefined }

/** Per-host previous-tick snapshot — the dedup mechanism itself. Comparing
 * the latest poll against this (and updating it every tick) is what stops
 * the same standing problem from re-notifying on every 30s poll; a plain
 * "is it nonzero" check would re-fire constantly for anything not fixed
 * between polls. Owned by the caller (a useRef in Hosts.tsx), not module
 * state, so it doesn't leak across mounts/tests. */
export type NotifyState = Map<number, HostSnapshot>

function criticalHigh(h: HubHost): number {
  return (h.findings?.critical ?? 0) + (h.findings?.high ?? 0)
}

/**
 * Diffs the latest /hub/hosts poll against `state` (mutated in place) and
 * fires a browser Notification for real transitions only:
 *  - critical+high count going up (not any nonzero reading)
 *  - reachable flipping to false (not every tick a host stays down)
 * A host's first-ever sighting only seeds its snapshot — nothing fires on
 * initial page load, and nothing fires for a host that's never been polled
 * (reachable stays undefined throughout, never satisfying either check).
 */
export function checkForNewProblems(hosts: HubHost[], state: NotifyState): void {
  if (!notificationsEnabled()) return
  if (!('Notification' in window) || Notification.permission !== 'granted') return

  for (const h of hosts) {
    const now: HostSnapshot = { criticalHigh: criticalHigh(h), reachable: h.reachable }
    const prev = state.get(h.id)
    state.set(h.id, now)
    if (!prev) continue

    if (now.criticalHigh > prev.criticalHigh) {
      notify(
        `${h.name}: новые проблемы`,
        `critical+high: ${prev.criticalHigh} → ${now.criticalHigh}`,
        `nkt-host-${h.id}-problems`,
      )
    }
    if (prev.reachable !== false && now.reachable === false) {
      notify(`${h.name}: недоступен`, 'Хаб не смог опросить хост при последней попытке', `nkt-host-${h.id}-unreachable`)
    }
  }
}

function notify(title: string, body: string, tag: string): void {
  try {
    // eslint-disable-next-line no-new -- fire-and-forget by design, nothing to await
    new Notification(title, { body, tag })
  } catch {
    // The Notification constructor can throw on some platforms (e.g. iOS
    // Safari, which only allows service-worker-based notifications) — this
    // is a best-effort enhancement, never something to surface as an app
    // error.
  }
}
