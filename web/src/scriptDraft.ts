/**
 * Черновик сценария из ответа модели («Открыть как сценарий»): передаётся
 * в памяти вкладки — событием nkt-open-script, страница сценариев забирает
 * его при открытии. В хранилище браузера не пишется: в тексте сценария и
 * в вопросе к модели могут быть имена и адреса.
 */
export interface ScriptDraft {
  content: string
  origin: { kind: 'assistant' | 'finding'; label?: string }
}

let pending: ScriptDraft | null = null

export function setPendingScript(d: ScriptDraft | null) {
  pending = d
}

/** Забрать черновик (один раз). */
export function takePendingScript(): ScriptDraft | null {
  const d = pending
  pending = null
  return d
}
