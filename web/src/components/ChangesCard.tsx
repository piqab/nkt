import { useState } from 'react'
import { Button, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { StateChange } from '../types'
import { Card, ErrorNote, formatRelative } from './ui'
import { DataTable } from './DataTable'

const POLL_MS = 60_000

const ACTION_COLOR: Record<string, string> = {
  appeared: 'processing',
  disappeared: 'warning',
  changed: 'default',
}

/**
 * «Что изменилось с прошлого захода».
 *
 * Снимки состояния nkt хранит и так — каждое сканирование, отличающееся
 * от предыдущего. Здесь они наконец сравниваются: был открыт порт и стал
 * закрыт, служба работала и остановилась, появился файл конфигурации,
 * которого не было.
 *
 * База сравнения — отметка «я это видел», своя у каждого оператора.
 * Сдвигается она кнопкой, а не открытием страницы: вкладка, забытая на
 * втором мониторе, иначе съедала бы изменения, которых никто не читал.
 */
export default function ChangesCard() {
  const { t } = useTranslation()
  const changes = useApi<{
    changes: StateChange[]
    since?: { id: number; ts: string }
    until?: { id: number; ts: string }
    first?: boolean
    truncated?: boolean
  }>('/changes', POLL_MS)
  const [busy, setBusy] = useState(false)
  const list = changes.data?.changes ?? []

  async function ack() {
    setBusy(true)
    try {
      await api('/changes/ack', { method: 'POST' })
      await changes.reload()
    } finally {
      setBusy(false)
    }
  }

  // Тишина — это хорошая новость, и занимать ею экран незачем: карточка
  // появляется, только когда есть что показать.
  if (changes.error) return <ErrorNote error={changes.error} />
  if (list.length === 0) return null

  const columns: TableColumnsType<StateChange> = [
    {
      title: t('changes.colWhat'),
      key: 'kind',
      render: (_, c) => (
        <span className="nowrap">
          <Tag color={ACTION_COLOR[c.action] ?? 'default'}>
            {t(`changes.action.${c.action}`, { defaultValue: c.action })}
          </Tag>
          <span className="small muted">{t(`changes.kind.${c.kind}`, { defaultValue: c.kind })}</span>
        </span>
      ),
    },
    { title: t('changes.colKey'), key: 'key', render: (_, c) => <span className="small mono">{c.key}</span> },
    {
      title: t('changes.colWas'),
      key: 'was',
      render: (_, c) => <span className="small mono muted">{c.was || '—'}</span>,
    },
    {
      title: t('changes.colNow'),
      key: 'now',
      render: (_, c) => <span className="small mono">{c.now || '—'}</span>,
    },
  ]

  const since = changes.data?.since
  return (
    <Card
      title={t('changes.title')}
      subtitle={
        since
          ? t('changes.subtitle', { when: formatRelative(since.ts), count: list.length })
          : t('changes.subtitleNoBase', { count: list.length })
      }
      actions={
        <Button size="small" loading={busy} onClick={() => void ack()}>
          {t('changes.ack')}
        </Button>
      }
    >
      <div className="table-wrap">
        <DataTable<StateChange>
          dataSource={list}
          columns={columns}
          rowKey={(c) => `${c.kind}:${c.key}:${c.action}`}
          paginateFrom={20}
        />
      </div>
      {changes.data?.truncated && <p className="small muted">{t('changes.truncated')}</p>}
    </Card>
  )
}
