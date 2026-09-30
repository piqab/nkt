import { useEffect, useState } from 'react'
import { AIExplain } from '../components/AIExplain'
import { Sensitive, blurText } from '../privacy'
import { Button, Checkbox, Input, InputNumber, Select, Switch, Tag, Tooltip, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { HostEvent, Me } from '../types'
import { Card, ErrorNote, Loading, formatDateTime, formatRelative } from '../components/ui'
import { notificationsEnabled, requestNotificationPermission, setNotificationsEnabled } from '../notifications'
import { DataTable } from '../components/DataTable'
import { IPWithCheck } from '../components/Fail2banParts'
import { ipsInText, isExternalIP } from '../fail2ban'
import { TitleHelp } from '../components/Docs'

const POLL_MS = 30_000

interface EventSettings {
  record: Record<string, boolean>
  notify: Record<string, boolean>
  /** Записывать, но не показывать в журнале и не считать непрочитанным. */
  hide?: Record<string, boolean>
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
function EventSettingsCard({ onSaved }: { onSaved?: () => void }) {
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
    setDraft(patch({ ...settings, record: { ...settings.record }, notify: { ...settings.notify }, hide: { ...(settings.hide ?? {}) } }))
  }

  async function save() {
    if (!draft) return
    setSaving(true)
    try {
      await api('/hub/events/settings', { method: 'POST', body: draft })
      setDraft(null)
      setSaved(true)
      await data.reload()
      onSaved?.()
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
                  <th style={{ padding: '0.25rem 0.75rem' }}>
                    <Tooltip title={t('events.settingHideHint')}>{t('events.settingHide')}</Tooltip>
                  </th>
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
                        checked={!!settings.notify[k] && !settings.hide?.[k]}
                        disabled={settings.record[k] === false || !!settings.hide?.[k]}
                        onChange={(e) => update((s) => ({ ...s, notify: { ...s.notify, [k]: e.target.checked } }))}
                      />
                    </td>
                    <td style={{ textAlign: 'center' }}>
                      <Checkbox
                        checked={!!settings.hide?.[k]}
                        disabled={settings.record[k] === false}
                        onChange={(e) =>
                          update((s) => ({
                            ...s,
                            hide: { ...(s.hide ?? {}), [k]: e.target.checked },
                            // Скрытое не всплывает.
                            notify: e.target.checked ? { ...s.notify, [k]: false } : s.notify,
                          }))
                        }
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
  bans: 'volcano',
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
/** Фильтр журнала — запоминается в этом браузере. */
interface EventFilter {
  kinds: string[]
  host: string
  q: string
  hidden: boolean
}

const FILTER_KEY = 'nkt-events-filter'

function loadFilter(): EventFilter {
  try {
    const raw = localStorage.getItem(FILTER_KEY)
    if (raw) return { kinds: [], host: '', q: '', hidden: false, ...JSON.parse(raw) }
  } catch {
    // нет хранилища — фильтр по умолчанию
  }
  return { kinds: [], host: '', q: '', hidden: false }
}

export default function HostEvents({ me }: { me?: Me }) {
  const { t } = useTranslation()
  const [filter, setFilterState] = useState<EventFilter>(loadFilter)
  const [text, setText] = useState(filter.q)
  const setFilter = (patch: Partial<EventFilter>) =>
    setFilterState((f) => {
      const next = { ...f, ...patch }
      try {
        localStorage.setItem(FILTER_KEY, JSON.stringify(next))
      } catch {
        // не запомнится — не страшно
      }
      return next
    })
  const events = useApi<{ events: HostEvent[]; unread: number; total: number; hosts: string[]; hidden: number; hide?: Record<string, boolean> }>(
    `/hub/events${qs({ limit: 200, kind: filter.kinds.join(','), host: filter.host, q: filter.q, hidden: filter.hidden ? 1 : '' })}`,
    POLL_MS,
  )
  const list = events.data?.events ?? []
  const filtered = filter.kinds.length > 0 || !!filter.host || !!filter.q
  // Адреса своих хостов — не «внешние»: проверять и банить их незачем.
  const hosts = useApi<{ addr: string }[]>('/hub/hosts', 120_000)
  const own = new Set((hosts.data ?? []).map((h) => h.addr))
  const externalIPs = (e: HostEvent) =>
    ipsInText(e.detail ?? '').filter((ip) => isExternalIP(ip) && ip !== e.host_addr && !own.has(ip))

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
          <strong><Sensitive>{e.host_name}</Sensitive></strong>
          <div className="small muted mono"><Sensitive>{e.host_addr}</Sensitive></div>
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
    {
      title: t('events.colDetail'),
      key: 'detail',
      render: (_, e) => {
        // Внешние адреса из текста — отдельно, у каждого своя лампочка
        // (проверка адреса) и «забанить на всех хостах» в окне ответа.
        const ips = externalIPs(e)
        return (
          <div>
            <span className="row row-nowrap" style={{ gap: '0.3rem', alignItems: 'center' }}>
              <span className="small">{e.detail ? blurText(e.detail) : '—'}</span>
              {e.detail && (
                <AIExplain
                  ctx={{ kind: 'event', title: `${e.kind}: ${e.host_name}`, detail: e.detail, severity: e.severity }}
                />
              )}
            </span>
            {ips.length > 0 && (
              <div className="row" style={{ flexWrap: 'wrap', gap: '0.1rem 0.6rem', marginTop: '0.15rem' }}>
                {ips.map((ip) => (
                  <IPWithCheck key={ip} ip={ip} hubLevel me={me} />
                ))}
              </div>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('events.title')}
          <TitleHelp>{t('events.hint')}</TitleHelp>
        </h1>
        <Button onClick={() => events.reload()} loading={events.loading}>
          {t('events.refresh')}
        </Button>
      </div>

      <ErrorNote error={events.error} />

      <EventSettingsCard onSaved={() => events.reload()} />

      <Card
        title={t('events.listTitle')}
        subtitle={
          filtered
            ? t('events.listFiltered', { shown: list.length, total: events.data?.total ?? 0 })
            : t('events.listSubtitle', { count: list.length })
        }
      >
        <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center', marginBottom: '0.5rem' }}>
          <Select
            mode="multiple"
            allowClear
            style={{ minWidth: 220 }}
            placeholder={t('events.filterKinds')}
            value={filter.kinds}
            onChange={(v: string[]) => setFilter({ kinds: v })}
            options={Object.keys(KIND_COLOR)
              .concat(['rebooted'])
              .filter((k, i, a) => a.indexOf(k) === i)
              .map((k) => ({ value: k, label: t(`events.kind.${k}`, { defaultValue: k }) }))}
          />
          <Select
            allowClear
            showSearch
            style={{ minWidth: 180 }}
            placeholder={t('events.filterHost')}
            value={filter.host || undefined}
            onChange={(v?: string) => setFilter({ host: v ?? '' })}
            options={(events.data?.hosts ?? []).map((h) => ({ value: h, label: h }))}
          />
          <Input.Search
            allowClear
            style={{ maxWidth: 300 }}
            placeholder={t('events.filterText')}
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              if (!e.target.value) setFilter({ q: '' })
            }}
            onSearch={(v) => setFilter({ q: v.trim() })}
          />
          {(filter.hidden || (events.data?.hidden ?? 0) > 0 || Object.values(events.data?.hide ?? {}).some(Boolean)) && (
            <Checkbox checked={filter.hidden} onChange={(e) => setFilter({ hidden: e.target.checked })}>
              {t('events.showHidden', { count: events.data?.hidden ?? 0 })}
            </Checkbox>
          )}
          {filtered && (
            <Button
              size="small"
              type="link"
              onClick={() => {
                setText('')
                setFilter({ kinds: [], host: '', q: '' })
              }}
            >
              {t('events.filterReset')}
            </Button>
          )}
        </div>
        {events.loading && !events.data ? (
          <Loading what={t('events.title')} />
        ) : list.length === 0 ? (
          <p className="small muted">{filtered ? t('events.emptyFiltered') : t('events.empty')}</p>
        ) : (
          <div className="table-wrap">
            <DataTable<HostEvent> dataSource={list} columns={columns} rowKey="id" />
          </div>
        )}
      </Card>
    </>
  )
}
