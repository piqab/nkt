import { useEffect, useState } from 'react'
import { Button, Checkbox, InputNumber, Switch, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HostEvent } from '../types'
import { Card, ErrorNote, InfoHint, Loading, formatDateTime, formatRelative } from '../components/ui'
import { notificationsEnabled, requestNotificationPermission, setNotificationsEnabled } from '../notifications'
import { DataTable } from '../components/DataTable'

const POLL_MS = 30_000

interface EventSettings {
  record: Record<string, boolean>
  notify: Record<string, boolean>
  collapse_minutes: number
}

/**
 * Какие события записывать и о каких уведомлять — настройка общая на
 * хаб, не на браузер: журнал один на всех, и «не записывать возвраты»
 * должно действовать для каждого, кто его смотрит. Сворачивание —
 * ответ на «зачем нужно „снова отвечает“»: без пары событий не узнать,
 * сколько хост лежал; а чтобы моргнувшая сеть не оставляла две строки,
 * короткий эпизод сворачивается в одну — «был недоступен N мин».
 */
function EventSettingsCard() {
  const { t } = useTranslation()
  const data = useApi<{ settings: EventSettings; kinds: string[] }>('/hub/events/settings')
  const [draft, setDraft] = useState<EventSettings | null>(null)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const settings = draft ?? data.data?.settings ?? null
  const kinds = data.data?.kinds ?? []
  // Всплывающие уведомления — настройка этого браузера, не хаба: у
  // каждого оператора своя.
  const [notifyOn, setNotifyOn] = useState(() => notificationsEnabled())
  const [denied, setDenied] = useState(false)

  async function toggleNotify(checked: boolean) {
    setDenied(false)
    if (checked) {
      const granted = await requestNotificationPermission()
      if (!granted) {
        setDenied(true)
        return
      }
    }
    setNotificationsEnabled(checked)
    setNotifyOn(checked)
  }

  function update(patch: (s: EventSettings) => EventSettings) {
    if (!settings) return
    setSaved(false)
    setDraft(patch({ ...settings, record: { ...settings.record }, notify: { ...settings.notify } }))
  }

  async function save() {
    if (!draft) return
    setSaving(true)
    try {
      await api('/hub/events/settings', { method: 'POST', body: draft })
      setDraft(null)
      setSaved(true)
      await data.reload()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card title={t('events.settingsTitle')} subtitle={t('events.settingsHint')}>
      <ErrorNote error={data.error} />
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap', marginBottom: '0.6rem' }}>
        <Tooltip title={t('events.notifyTooltip')}>
          <span className="row" style={{ gap: '0.4rem', alignItems: 'center' }}>
            <Switch size="small" checked={notifyOn} onChange={toggleNotify} />
            {t('events.notifyLabel')}
          </span>
        </Tooltip>
        {denied && <span className="small" style={{ color: 'var(--danger)' }}>{t('events.notificationDenied')}</span>}
      </div>
      {!settings ? (
        <Loading what={t('events.settingsTitle')} />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <div className="table-wrap">
            <table className="ant-table" style={{ width: 'auto', borderCollapse: 'collapse' }}>
              <thead>
                <tr>
                  <th style={{ textAlign: 'left', padding: '0.25rem 0.75rem 0.25rem 0' }}>{t('events.colWhat')}</th>
                  <th style={{ padding: '0.25rem 0.75rem' }}>{t('events.settingRecord')}</th>
                  <th style={{ padding: '0.25rem 0.75rem' }}>{t('events.settingNotify')}</th>
                </tr>
              </thead>
              <tbody>
                {kinds.map((k) => (
                  <tr key={k}>
                    <td style={{ padding: '0.2rem 0.75rem 0.2rem 0' }}>
                      <Tag color={KIND_COLOR[k] ?? 'default'}>{t(`events.kind.${k}`, { defaultValue: k })}</Tag>
                    </td>
                    <td style={{ textAlign: 'center' }}>
                      <Checkbox
                        checked={settings.record[k] !== false}
                        onChange={(e) => update((s) => ({ ...s, record: { ...s.record, [k]: e.target.checked } }))}
                      />
                    </td>
                    <td style={{ textAlign: 'center' }}>
                      <Checkbox
                        checked={!!settings.notify[k]}
                        disabled={settings.record[k] === false}
                        onChange={(e) => update((s) => ({ ...s, notify: { ...s.notify, [k]: e.target.checked } }))}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
            {t('events.collapseLabel')}
            <InputNumber
              min={0}
              max={1440}
              value={settings.collapse_minutes}
              onChange={(v) => update((s) => ({ ...s, collapse_minutes: v ?? 0 }))}
            />
            <span className="small muted">{t('events.collapseHint')}</span>
          </label>
          <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
            <Button type="primary" disabled={!draft} loading={saving} onClick={() => void save()}>
              {t('events.save')}
            </Button>
            {saved && <span className="small muted">{t('events.savedNote')}</span>}
          </div>
        </div>
      )}
    </Card>
  )
}

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

      <EventSettingsCard />

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
