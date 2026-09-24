import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import type { WriteResult } from '../types'
import { AIExplain } from './AIExplain'

/**
 * Лампочка у отказа при записи конфигурации: правка не прошла проверку
 * или apply и откатилась. Модели уходят вывод проверки и дифф «на диске →
 * черновик» (после отката файл на диске — прежний, так что preview-diff
 * даёт ровно то, что пытались записать), а не весь файл; пароли и токены
 * в них хаб вырезает до отправки.
 */
export function AIConfigError({
  path,
  service,
  content,
  snippet,
  result,
  message,
}: {
  path: string
  service?: string
  /** Черновик всего файла — дифф «на диске → черновик» считает хост… */
  content?: string
  /** …или только правленый фрагмент (блочный редактор): уходит как есть. */
  snippet?: string
  /** Итог записи с откатом (проверка или apply не прошли)… */
  result?: WriteResult
  /** …или ошибка запроса, когда до записи не дошло (путь, валидатор,
   * конфликт версий): текста ошибки модели хватает. */
  message?: string
}) {
  const { t } = useTranslation()
  const [diff, setDiff] = useState<string | null>(snippet ?? null)
  useEffect(() => {
    if (content === undefined) return
    let cancelled = false
    api<{ changed: boolean; diff: string }>('/configs/preview-diff', { method: 'POST', body: { path, content } })
      .then((res) => {
        if (!cancelled) setDiff(res.changed ? res.diff : '')
      })
      .catch(() => {
        // Новый файл: на диске его нет — модели уйдёт сам черновик.
        if (!cancelled) setDiff(content)
      })
    return () => {
      cancelled = true
    }
  }, [path, content])
  useEffect(() => {
    if (snippet !== undefined) setDiff(snippet)
  }, [snippet])
  const output = result?.validation ? result.validation.stdout || result.validation.stderr : ''
  const text = result?.message ?? message ?? ''
  const firstLine = (output || text).split('\n').find((l) => l.trim() !== '') ?? text
  return (
    <span title={t('ai.explainConfigError')}>
      <AIExplain
        disabled={diff === null}
        ctx={{
          kind: 'config-error',
          title: firstLine,
          detail: text,
          service,
          object: path,
          file: path,
          output,
          diff: diff ?? '',
        }}
      />
    </span>
  )
}
