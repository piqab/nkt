import { useEffect } from 'react'
import { Button, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HostEvent } from '../types'
import { Card, ErrorNote, InfoHint, Loading, formatDateTime, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'

const POLL_MS = 30_000

const KIND_COLOR: Record<string, string> = {
  unreachable: 'error',
  recovered: 'success',
  problems: 'warning',
  resolved: 'success',
  'job-failed': 'error',
}

/**
 * Журнал оповещений хаба.
 *
 * Всплывающее уведомление браузера живёт, пока открыта вкладка: закрыл —
 * и не узнал, что ночью отвалился хост. События записывает сам хаб, в
 * фоновом опросе, поэтому здесь видно и то, что случилось без единого
 * открытого браузера.
 *
 * Имя и адрес хоста записаны в само событие, а не подставляются из
 * текущего списка: хост переименуют, переедет или будет удалён, а строка
 * журнала должна остаться понятной.
 */
export default function HostEvents() {
  const { t } = useTranslation()
  const events = useApi<{ events: HostEvent[]; unread: number }>('/hub/events?limit=200', POLL_MS)
  const list = events.data?.events ?? []

  // Открытый раздел и есть «прочитано»: счётчик в меню гаснет, как только
  // на события посмотрели, а не по отдельной кнопке, которую ещё надо
  // не забыть нажать.
  useEffect(() => {
    if (!events.data || events.data.unread === 0) return
    void api('/hub/events/seen', { method: 'POST' }).then(() => events.reload())
    // eslint-disable-next-line react-hooks/exhaustive-deps -- перечитываем один раз на появление непрочитанных
  }, [events.data?.unread])

  const columns: TableColumnsType<HostEvent> = [
    {
      title: t('events.colWhen'),
      key: 'ts',
      render: (_, e) => (
        <span className="small nowrap" title={formatDateTime(e.ts)}>
          {formatRelative(e.ts)}
        </span>
      ),
    },
    {
      title: t('events.colHost'),
      key: 'host',
      render: (_, e) => (
        <div>
          <strong>{e.host_name}</strong>
          <div className="small muted mono">{e.host_addr}</div>
        </div>
      ),
    },
    {
      title: t('events.colWhat'),
      key: 'kind',
      render: (_, e) => (
        <span className="nowrap">
          <Tag color={KIND_COLOR[e.kind] ?? 'default'}>{t(`events.kind.${e.kind}`, { defaultValue: e.kind })}</Tag>
          {e.severity && <span className="small muted">{e.severity}</span>}
        </span>
      ),
    },
    { title: t('events.colDetail'), key: 'detail', render: (_, e) => <span className="small">{e.detail || '—'}</span> },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('events.title')}
          <InfoHint>{t('events.hint')}</InfoHint>
        </h1>
        <Button onClick={() => events.reload()} loading={events.loading}>
          {t('events.refresh')}
        </Button>
      </div>

      <ErrorNote error={events.error} />

      <Card title={t('events.listTitle')} subtitle={t('events.listSubtitle', { count: list.length })}>
        {events.loading && !events.data ? (
          <Loading what={t('events.title')} />
        ) : list.length === 0 ? (
          <p className="small muted">{t('events.empty')}</p>
        ) : (
          <div className="table-wrap">
            <DataTable<HostEvent> dataSource={list} columns={columns} rowKey="id" />
          </div>
        )}
      </Card>
    </>
  )
}
