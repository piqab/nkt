import { useEffect, useSyncExternalStore } from 'react'
import { api, hostScope, LOCAL_HOST_ID } from './api'

/**
 * Сохранённые ответы модели — ссылки без текста, одним запросом на всю
 * установку: по ним каждая лампочка на странице знает, есть ли у её
 * строки ответ (оранжевая), разбиралась ли такая же находка на другом
 * хосте (синяя) или её ещё не спрашивали (серая). Хранилище общее для
 * всех лампочек, чтобы не слать по запросу на строку.
 */

export interface AIAnswerRef {
  key: string
  host_id: number
  kind: string
  title: string
  object: string
  file: string
  created_at: string
}

let refs: AIAnswerRef[] | null = null
let loading: Promise<void> | null = null
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

function load(): Promise<void> {
  if (!loading) {
    loading = api<{ answers: AIAnswerRef[] }>('/hub/ai/answers')
      .then((res) => {
        refs = res.answers ?? []
      })
      .catch(() => {
        // Нет хаба или ИИ — лампочки просто серые.
        refs = []
      })
      .finally(() => {
        loading = null
        emit()
      })
  }
  return loading
}

/** После нового ответа или удаления — перечитать. */
export function invalidateAIAnswers() {
  refs = null
  void load()
}

/** Хост текущей страницы так, как его знает хаб: 0 — сам хаб. */
export function currentAIHostID(): number {
  return hostScope.id !== null && hostScope.id !== LOCAL_HOST_ID ? hostScope.id : 0
}

export function useAIAnswers(): AIAnswerRef[] {
  const value = useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => refs,
    () => refs,
  )
  useEffect(() => {
    if (refs === null) void load()
  }, [])
  return value ?? []
}

export type AIAnswerState = 'none' | 'own' | 'similar'

/** Есть ли у строки сохранённый ответ — свой или с другого хоста. */
export function aiAnswerState(
  refs: AIAnswerRef[],
  ctx: { kind: string; title: string; object?: string; file?: string },
  hostID: number,
): AIAnswerState {
  const same = (r: AIAnswerRef) =>
    r.kind === ctx.kind && r.title === ctx.title.trim() && r.object === (ctx.object ?? '').trim() && r.file === (ctx.file ?? '').trim()
  let similar = false
  for (const r of refs) {
    if (!same(r)) continue
    if (r.host_id === hostID) return 'own'
    similar = true
  }
  return similar ? 'similar' : 'none'
}
