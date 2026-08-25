import { useCallback, useEffect, useRef, useState } from 'react'

/** Raised for any non-2xx API response, carrying the server's message. */
export class ApiError extends Error {
  status: number
  payload: unknown
  constructor(status: number, message: string, payload?: unknown) {
    super(message)
    this.status = status
    this.payload = payload
  }
}

type Options = {
  method?: string
  body?: unknown
  signal?: AbortSignal
}

/** Fires when a request comes back 401, so the shell can send the user to /login. */
export const onUnauthorized: { handler: (() => void) | null } = { handler: null }

/**
 * Set by the hub shell when a host is selected, so every existing page's
 * `api()`/`useApi()` calls transparently reach that host's own API through
 * the hub's proxy instead of the hub's own — the pages themselves never
 * need to know they are running under a hub. null (the default, and always
 * the case for a plain single-host nkt) targets the hub/local API directly.
 */
export const hostScope: { id: number | null } = { id: null }

/** Sentinel hostScope.id for the pinned "localhost" row — the machine the
 * hub itself runs on (internal/hub/handlers.go's localHostID). Real hosts
 * autoincrement from 1 in the hub's own db, so -1 never collides; routed
 * to /hosts/local/* (proxyLocal, no SSH) instead of /hosts/{numeric id}
 * (proxyHost, over the SSH tunnel) — the only place that distinction
 * matters, everything else treats it as just another selected host. */
export const LOCAL_HOST_ID = -1

/** A host selected in the hub shell — everything reading through
 * hostScope above scopes its API calls to it once it is set. */
export interface SelectedHost {
  id: number
  name: string
}

const SELECTED_HOST_KEY = 'nkt-hub-host'

/** Reads back whichever host the hub shell last selected — the one piece
 * of hub state that has to survive a page load (App.tsx's Shell seeds its
 * own state from this on mount) or reach an entirely separate window (a
 * detached terminal — see Terminal.tsx's openPopout — carries its host id
 * in its own URL instead, precisely so several such windows for different
 * hosts don't all fight over this one shared value). */
export function readSelectedHost(): SelectedHost | null {
  try {
    const raw = localStorage.getItem(SELECTED_HOST_KEY)
    return raw ? (JSON.parse(raw) as SelectedHost) : null
  } catch {
    return null
  }
}

export function writeSelectedHost(host: SelectedHost | null): void {
  if (host) localStorage.setItem(SELECTED_HOST_KEY, JSON.stringify(host))
  else localStorage.removeItem(SELECTED_HOST_KEY)
}

export async function api<T>(path: string, opts: Options = {}): Promise<T> {
  // Authentication always targets the hub itself — the operator only ever
  // logs in once, never per host (see internal/hub's design notes).
  const scoped = hostScope.id !== null && !path.startsWith('/auth/')
  const prefix = scoped ? `/hosts/${hostScope.id === LOCAL_HOST_ID ? 'local' : hostScope.id}` : ''
  const res = await fetch(`/api${prefix}${path}`, {
    method: opts.method ?? 'GET',
    credentials: 'same-origin',
    headers: opts.body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  })

  const text = await res.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = text
    }
  }

  if (!res.ok) {
    // A 401 from a proxied host's own API (see hostScope above) means that
    // HOST's session needs attention — internal/hub's Proxy re-logs in on
    // its own the next time regardless — not that the browser's hub
    // session is invalid. Bouncing to /login here would log the operator
    // out of the hub itself over something a single managed host did,
    // replacing the whole shell (sidebar, "к списку хостов") with a bare
    // login form and leaving no way back short of reloading.
    if (res.status === 401 && !scoped) onUnauthorized.handler?.()
    const message =
      (payload && typeof payload === 'object' && 'error' in payload
        ? String((payload as { error: unknown }).error)
        : null) ?? `Ошибка ${res.status}`
    throw new ApiError(res.status, message, payload)
  }
  return payload as T
}

/** The browser's UTC offset in minutes, so hourly buckets mean local hours. */
export function tzOffsetMinutes(): number {
  return -new Date().getTimezoneOffset()
}

export function qs(params: Record<string, string | number | undefined | null>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') search.set(key, String(value))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}

export interface Loadable<T> {
  data: T | null
  error: string | null
  loading: boolean
  // Awaitable: a caller that does `await x.reload()` right before clearing
  // its own busy/spinner state is guaranteed the fetch has actually landed
  // (data/error already set) by the time that await resolves — unlike a
  // bare fire-and-forget call, which only *starts* the fetch and returns
  // immediately. Every existing call site that never awaits it keeps
  // working exactly as before; this only adds a capability, not a
  // requirement.
  reload: () => Promise<void>
}

/**
 * Loads a GET endpoint and re-runs whenever `path` changes. In-flight requests
 * are aborted on change so a slow response cannot overwrite a newer one.
 */
export function useApi<T>(path: string | null, pollMs = 0): Loadable<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(path !== null)
  const abortRef = useRef<AbortController | null>(null)

  // The one place an actual fetch happens — used for the initial load, a
  // path change, the poll timer, and an explicit reload() call alike, so
  // "reload" can just be this function directly instead of indirecting
  // through a nonce state bump that only *triggers* a fetch on some later
  // render without anything to await.
  const load = useCallback(async () => {
    if (path === null) {
      setData(null)
      setLoading(false)
      return
    }
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    setLoading(true)
    try {
      const result = await api<T>(path, { signal: controller.signal })
      if (controller.signal.aborted) return
      setData(result)
      setError(null)
    } catch (err) {
      if (controller.signal.aborted || (err instanceof DOMException && err.name === 'AbortError')) return
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }, [path])

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEffect(() => {
    if (!pollMs || path === null) return
    const timer = window.setInterval(load, pollMs)
    return () => window.clearInterval(timer)
  }, [pollMs, path, load])

  return { data, error, loading, reload: load }
}
