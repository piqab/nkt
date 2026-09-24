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
export function AIConfigError({ path, service, content, result }: { path: string; service?: string; content: string; result: WriteResult }) {
  const { t } = useTranslation()
  const [diff, setDiff] = useState<string | null>(null)
  useEffect(() => {
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
  const output = result.validation ? result.validation.stdout || result.validation.stderr : ''
  const firstLine = (output || result.message).split('\n').find((l) => l.trim() !== '') ?? result.message
  return (
    <span title={t('ai.explainConfigError')}>
      <AIExplain
        disabled={diff === null}
        ctx={{
          kind: 'config-error',
          title: firstLine,
          detail: result.message,
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
