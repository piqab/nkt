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

export async function api<T>(path: string, opts: Options = {}): Promise<T> {
  // Authentication always targets the hub itself — the operator only ever
  // logs in once, never per host (see internal/hub's design notes).
  const scoped = hostScope.id !== null && !path.startsWith('/auth/')
  const prefix = scoped ? `/hosts/${hostScope.id}` : ''
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
    if (res.status === 401) onUnauthorized.handler?.()
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
  reload: () => void
}

/**
 * Loads a GET endpoint and re-runs whenever `path` changes. In-flight requests
 * are aborted on change so a slow response cannot overwrite a newer one.
 */
export function useApi<T>(path: string | null, pollMs = 0): Loadable<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(path !== null)
  const [nonce, setNonce] = useState(0)
  const abortRef = useRef<AbortController | null>(null)

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    if (path === null) {
      setData(null)
      setLoading(false)
      return
    }
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    let cancelled = false
    setLoading(true)
    api<T>(path, { signal: controller.signal })
      .then((result) => {
        if (cancelled) return
        setData(result)
        setError(null)
      })
      .catch((err: unknown) => {
        if (cancelled || (err instanceof DOMException && err.name === 'AbortError')) return
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [path, nonce])

  useEffect(() => {
    if (!pollMs || path === null) return
    const timer = window.setInterval(reload, pollMs)
    return () => window.clearInterval(timer)
  }, [pollMs, path, reload])

  return { data, error, loading, reload }
}
