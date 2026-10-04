import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { unifiedDiff } from './textDiff'
import { HelpButton } from './Docs'
import { msg, tx, type Msg } from '../msg'

interface Chat {
  id: number
  title?: string
  role: 'read' | 'admin'
  notify: boolean
}

interface TelegramStatus {
  enabled: boolean
  bot_name?: string
  has_token: boolean
  chats: Chat[]
  users: number[]
  kinds: string[]
  lang: 'ru' | 'en'
  timezone?: string
  hub_timezone: string
  running: boolean
  last_error?: string
  last_at?: string
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** «Бот Telegram» в «Оповещениях»: оповещения с кнопками и команды из чата. */
export function TelegramCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const st = useApi<TelegramStatus>(admin ? '/hub/telegram' : null, 15_000)
  const kinds = useApi<{ kinds: string[] }>(admin ? '/hub/webhooks' : null)
  const [edit, setEdit] = useState(false)
  const [note, setNote] = useState<{ kind: 'info' | 'error'; text: Msg } | null>(null)
  if (!admin) return null
  const s = st.data
  return (
    <Card
      title={t('telegram.title')}
      subtitle={t('telegram.hint')}
      actions={
        <Space size={4}>
          <HelpButton docKey="hub:telegram" isHub admin label={t('telegram.help')} />
          <Button size="small" type="primary" onClick={() => setEdit(true)}>
            {t('telegram.configure')}
          </Button>
        </Space>
      }
    >
      <ErrorNote error={st.error} />
      {note && (
        <Banner kind={note.kind} onClose={() => setNote(null)}>
          {msg(note.text)}
        </Banner>
      )}
      {!s ? (
        <Loading what={t('telegram.title')} />
      ) : !s.has_token ? (
        <p className="small muted">{t('telegram.none')}</p>
      ) : (
        <div className="col small" style={{ gap: '0.35rem' }}>
          <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <strong className="mono">@{s.bot_name}</strong>
            {!s.enabled ? <Tag>{t('telegram.off')}</Tag> : s.running ? <Tag color="success">{t('telegram.running')}</Tag> : <Tag color="error">{t('telegram.stopped')}</Tag>}
            {s.last_at && <span className="muted">{formatRelative(s.last_at)}</span>}
          </div>
          {s.last_error && <Banner kind="error">{s.last_error}</Banner>}
          {s.chats.length === 0 ? (
            <span className="muted">{t('telegram.noChats')}</span>
          ) : (
            s.chats.map((c) => (
              <div key={c.id} className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                <span className="mono">{c.id}</span>
                {c.title && <span>{c.title}</span>}
                <Tag color={c.role === 'admin' ? 'red' : 'blue'}>{t(`tokens.roles.${c.role}`)}</Tag>
                {c.notify && <Tag>{t('telegram.notify')}</Tag>}
                <Button
                  size="small"
                  onClick={async () => {
                    try {
                      await api('/hub/telegram/test', { method: 'POST', body: { chat_id: c.id } })
                      setNote({ kind: 'info', text: tx('telegram.testSent', { id: c.id }) })
                    } catch (err) {
                      setNote({ kind: 'error', text: errText(err) })
                    }
                  }}
                >
                  {t('telegram.test')}
                </Button>
              </div>
            ))
          )}
          <span className="muted">{s.users.length ? t('telegram.usersList', { users: s.users.join(', ') }) : t('telegram.usersAny')}</span>
        </div>
      )}
      {edit && s && <TelegramModal st={s} kinds={kinds.data?.kinds ?? []} onClose={() => setEdit(false)} onSaved={() => void st.reload()} />}
    </Card>
  )
}

interface Draft {
  enabled: boolean
  token: string
  chats: Chat[]
  users: string
  kinds: string[]
  lang: 'ru' | 'en'
  timezone: string
}

function draftText(d: Draft, t: (k: string, o?: Record<string, unknown>) => string, kindLabel: (k: string) => string): string {
  return [
    `${t('telegram.enabled')}: ${d.enabled ? t('tokens.yes') : t('tokens.no')}`,
    `${t('telegram.token')}: ${d.token ? t('telegram.tokenNew') : t('telegram.tokenKept')}`,
    ...d.chats.map((c) => `${t('telegram.chat')} ${c.id}${c.title ? ` (${c.title})` : ''}: ${t(`tokens.roles.${c.role}`)}${c.notify ? ', ' + t('telegram.notify') : ''}`),
    `${t('telegram.users')}: ${d.users.trim() || '—'}`,
    `${t('webhooks.kinds')}: ${d.kinds.map(kindLabel).join(', ') || t('webhooks.allKinds')}`,
    `${t('webhooks.lang')}: ${d.lang}`,
    `${t('telegram.timezone')}: ${d.timezone || t('telegram.timezoneHub')}`,
    '',
  ].join('\n')
}

/** Настройка бота: поля, дифф с сохранённым — и только потом запись. */
function TelegramModal({ st, kinds, onClose, onSaved }: { st: TelegramStatus; kinds: string[]; onClose: () => void; onSaved: () => void }) {
  const { t, i18n } = useTranslation()
  const kindLabel = (k: string) => t(`webhooks.kind.${k}`, { defaultValue: t(`events.kind.${k}`, { defaultValue: k }) })
  const lang = st.lang || (i18n.language.startsWith('en') ? 'en' : 'ru')
  const initial: Draft = { enabled: st.has_token ? st.enabled : true, token: '', chats: st.chats, users: st.users.join(', '), kinds: st.kinds, lang, timezone: st.timezone ?? '' }
  const [d, setD] = useState<Draft>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const diff = unifiedDiff(draftText(initial, t, kindLabel), draftText(d, t, kindLabel), t('tokens.saved'), t('tokens.draft'))
  const changed = diff.trim() !== ''
  const setChat = (i: number, c: Partial<Chat>) => setD({ ...d, chats: d.chats.map((x, j) => (j === i ? { ...x, ...c } : x)) })
  return (
    <Modal title={t('telegram.modalTitle')} onClose={onClose} width={720}>
      <p className="small muted">{t('telegram.setupHint')}</p>
      <div className="col" style={{ gap: '0.5rem' }}>
        <label className="small">
          {t('telegram.token')}
          <Input.Password
            value={d.token}
            autoComplete="new-password"
            placeholder={st.has_token ? t('telegram.tokenKeep', { name: st.bot_name }) : '123456789:AA…'}
            onChange={(e) => setD({ ...d, token: e.target.value.trim() })}
          />
        </label>
        <Checkbox checked={d.enabled} onChange={(e) => setD({ ...d, enabled: e.target.checked })}>
          {t('telegram.enabled')}
        </Checkbox>
        <div className="col small" style={{ gap: '0.3rem' }}>
          <span>{t('telegram.chats')}</span>
          {d.chats.map((c, i) => (
            <Space key={i} wrap size={4}>
              <InputNumber size="small" style={{ width: '11rem' }} value={c.id} placeholder="-1001234567890" onChange={(v) => setChat(i, { id: Number(v ?? 0) })} />
              <Input size="small" style={{ width: '12rem' }} value={c.title} placeholder={t('telegram.chatTitle')} onChange={(e) => setChat(i, { title: e.target.value })} />
              <Select
                size="small"
                value={c.role}
                onChange={(role) => setChat(i, { role })}
                options={[
                  { value: 'read', label: t('tokens.roles.read') },
                  { value: 'admin', label: t('tokens.roles.admin') },
                ]}
              />
              <Checkbox checked={c.notify} onChange={(e) => setChat(i, { notify: e.target.checked })}>
                {t('telegram.notify')}
              </Checkbox>
              <Button size="small" danger onClick={() => setD({ ...d, chats: d.chats.filter((_, j) => j !== i) })}>
                {t('telegram.removeChat')}
              </Button>
            </Space>
          ))}
          <div>
            <Button size="small" onClick={() => setD({ ...d, chats: [...d.chats, { id: 0, title: '', role: 'read', notify: true }] })}>
              {t('telegram.addChat')}
            </Button>
          </div>
          <span className="muted">{t('telegram.chatsHint')}</span>
        </div>
        <label className="small">
          {t('telegram.users')}
          <Input className="mono" value={d.users} placeholder="123456789, 987654321" onChange={(e) => setD({ ...d, users: e.target.value })} />
          <span className="muted">{t('telegram.usersHint')}</span>
        </label>
        <div className="col small" style={{ gap: '0.2rem' }}>
          <span>{t('telegram.kinds')}</span>
          <Space size={[8, 2]} wrap>
            {kinds.map((k) => (
              <Checkbox key={k} checked={d.kinds.includes(k)} onChange={(e) => setD({ ...d, kinds: e.target.checked ? kinds.filter((x) => x === k || d.kinds.includes(x)) : d.kinds.filter((x) => x !== k) })}>
                {kindLabel(k)}
              </Checkbox>
            ))}
          </Space>
          <span className="muted">{t('webhooks.kindsHint')}</span>
        </div>
        <label className="small">
          {t('webhooks.lang')}{' '}
          <Select
            size="small"
            value={d.lang}
            onChange={(lang) => setD({ ...d, lang })}
            options={[
              { value: 'ru', label: 'русский' },
              { value: 'en', label: 'English' },
            ]}
          />
        </label>
        <label className="small">
          {t('telegram.timezone')}
          <Select
            showSearch
            style={{ width: '100%' }}
            value={d.timezone}
            onChange={(timezone) => setD({ ...d, timezone })}
            options={zoneOptions(st.hub_timezone, (z) => t('telegram.timezoneHubIs', { zone: z }))}
          />
        </label>
        {changed && (
          <>
            <span className="small muted">{t('tokens.diffHint')}</span>
            <DiffView text={diff} />
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <div>
          <Button
            type="primary"
            loading={busy}
            disabled={!changed}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                const users = d.users
                  .split(/[\s,;]+/)
                  .filter(Boolean)
                  .map(Number)
                if (users.some((u) => !Number.isSafeInteger(u) || u <= 0)) throw new Error(t('telegram.badUsers'))
                await api('/hub/telegram', { method: 'PUT', body: { enabled: d.enabled, token: d.token, chats: d.chats.filter((c) => c.id), users, kinds: d.kinds, lang: d.lang, timezone: d.timezone } })
                onSaved()
                onClose()
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {t('tokens.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/** Часовые пояса IANA из браузера (пусто — как на хабе). */
export function zoneOptions(hubZone: string, label: (zone: string) => string) {
  const list: string[] = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  return [{ value: '', label: label(hubZone) }, ...list.map((z) => ({ value: z, label: z }))]
}
