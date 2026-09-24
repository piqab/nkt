import { useState } from 'react'
import { Button } from 'antd'
import { BulbOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Loading, formatDateTime } from './ui'
import { blurText } from '../privacy'
import type { Graph } from '../types'

/**
 * Архитектурный ревьюер: модель смотрит на карту ресурсов целиком, а не
 * на одну находку. Карта уже собрана хостом (internal/topology) — узлы,
 * рёбра, находки, — и именно она уходит к модели текстом: пересказывать
 * сырой снимок незачем, граф и есть та структура, о которой стоит
 * спрашивать.
 *
 * Разборы сохраняются на хабе с датой: смысл ревизии не в одном ответе,
 * а в том, чтобы вернуться через месяц и увидеть, что изменилось.
 */

interface AIAnswer {
  answer: string
  sections: { title: string; body: string }[]
  model: string
  prompt: string
  notice: string
}

interface Review {
  id: number
  scope: string
  model: string
  answer: string
  created_at: string
  author: string
}

/** Карта → строки для модели. Берётся то, что важно для архитектуры:
 * что это, в каком состоянии, публично ли, с чем связано. */
export function graphToLines(g: Graph): string[] {
  const byID = new Map(g.nodes.map((n) => [n.id, n]))
  const lines: string[] = []
  lines.push(`Узлов: ${g.nodes.length}, связей: ${(g.edges ?? []).length}`)
  for (const n of g.nodes) {
    const bits = [`${n.kind}: ${n.label}`]
    if (n.sublabel) bits.push(n.sublabel)
    if (n.port) bits.push(`порт ${n.port}`)
    if (n.public) bits.push('доступен снаружи')
    if (n.status && n.status !== 'ok') bits.push(`состояние ${n.status}`)
    if (n.findings) bits.push(`проблем: ${n.findings}${n.severity ? ` (${n.severity})` : ''}`)
    lines.push('- ' + bits.join(', '))
  }
  for (const e of g.edges ?? []) {
    const from = byID.get(e.from)?.label ?? e.from
    const to = byID.get(e.to)?.label ?? e.to
    lines.push(`- связь: ${from} → ${to}${e.label ? ` (${e.label})` : ''}${e.status && e.status !== 'ok' ? `, ${e.status}` : ''}`)
  }
  return lines
}

export function AIReviewCard({ graph, hostID, scope }: { graph?: Graph; hostID?: number; scope: 'host' | 'hub' }) {
  const { t } = useTranslation()
  const scopeKey = scope === 'hub' ? 'hub' : `host:${hostID ?? 0}`
  const history = useApi<{ reviews: Review[] }>(`/hub/ai/reviews?scope=${encodeURIComponent(scopeKey)}`)
  const [answer, setAnswer] = useState<AIAnswer | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function run() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<AIAnswer>('/hub/ai/review', {
        method: 'POST',
        body: {
          scope,
          host_id: hostID ?? 0,
          lines: graph ? graphToLines(graph) : [],
        },
      })
      setAnswer(res)
      history.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const shown = answer?.answer ?? history.data?.reviews?.[0]?.answer ?? ''
  const shownAt = answer ? null : history.data?.reviews?.[0]?.created_at

  return (
    <Card
      title={t('ai.review')}
      subtitle={t('ai.reviewHint')}
      actions={
        <Button type="primary" size="small" icon={<BulbOutlined />} loading={busy} onClick={() => void run()}>
          {scope === 'hub' ? `${t('ai.reviewRun')} — ${t('ai.reviewHub')}` : t('ai.reviewRun')}
        </Button>
      }
    >
      {error && <Banner kind="error">{error}</Banner>}
      {busy && !answer ? (
        <Loading what={t('ai.thinking')} />
      ) : shown === '' ? (
        <p className="small muted">{t('ai.reviewEmpty')}</p>
      ) : (
        <div className="col">
          {shownAt && <div className="small muted">{t('ai.reviewAt', { date: formatDateTime(shownAt) })}</div>}
          <div className="small" style={{ whiteSpace: 'pre-wrap' }}>
            {blurText(shown)}
          </div>
          {answer && <div className="small muted">{answer.notice}</div>}
        </div>
      )}
      {(history.data?.reviews?.length ?? 0) > 1 && (
        <details style={{ marginTop: '0.6rem' }}>
          <summary className="small muted">{t('ai.reviewHistory')}</summary>
          <div className="col" style={{ marginTop: '0.4rem' }}>
            {history.data!.reviews.slice(1).map((r) => (
              <details key={r.id}>
                <summary className="small">
                  {formatDateTime(r.created_at)} · {r.model} · {r.author}
                </summary>
                <div className="small" style={{ whiteSpace: 'pre-wrap' }}>
                  {blurText(r.answer)}
                </div>
              </details>
            ))}
          </div>
        </details>
      )}
    </Card>
  )
}
