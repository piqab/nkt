import { useState } from 'react'
import { Button, Checkbox, Input, Select, Space, Tag, Typography } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal } from './ui'
import { unifiedDiff } from './textDiff'
import { HelpButton } from './Docs'

interface Channel {
  id: string
  name?: string
  role: 'read' | 'admin'
  notify: boolean
}

interface SlackStatus {
  enabled: boolean
  team?: string
  bot_user?: string
  has_token: boolean
  has_signing: boolean
  channels: Channel[]
  users: string[]
  kinds: string[]
  lang: 'ru' | 'en' | ''
}

interface EdgeLite {
  domain?: string
  roles: string[]
  connected: boolean
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** «Slack» в «Оповещениях»: оповещения с кнопками, /nkt и кнопки — через вход хаба или edge. */
export function SlackCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const st = useApi<SlackStatus>(admin ? '/hub/slack' : null, 30_000)
  const edges = useApi<{ edges: EdgeLite[] }>(admin ? '/hub/edges' : null)
  const kinds = useApi<{ kinds: string[] }>(admin ? '/hub/webhooks' : null)
  const [edit, setEdit] = useState(false)
  const [note, setNote] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  if (!admin) return null
  const s = st.data
  const via = (edges.data?.edges ?? []).filter((e) => e.roles.includes('callbacks') && e.domain)
  const urls = via.length > 0 ? via.map((e) => `https://${e.domain}/callbacks/slack/`) : [`${window.location.origin}/api/hub/callbacks/slack/`]
  return (
    <Card
      title="Slack"
      subtitle={t('slack.hint')}
      actions={
        <Space size={4}>
          <HelpButton docKey="hub:slack" isHub admin label={t('telegram.help')} />
          <Button size="small" type="primary" onClick={() => setEdit(true)}>
            {t('telegram.configure')}
          </Button>
        </Space>
      }
    >
      <ErrorNote error={st.error} />
      {note && (
        <Banner kind={note.kind} onClose={() => setNote(null)}>
          {note.text}
        </Banner>
      )}
      {!s ? (
        <Loading what="Slack" />
      ) : (
        <div className="col small" style={{ gap: '0.35rem' }}>
          {!s.has_token ? (
            <span className="muted">{t('slack.none')}</span>
          ) : (
            <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <strong>{s.team}</strong>
              <span className="mono muted">@{s.bot_user}</span>
              {s.enabled ? <Tag color="success">{t('slack.on')}</Tag> : <Tag>{t('telegram.off')}</Tag>}
              {!s.has_signing && <Tag color="warning">{t('slack.noSigning')}</Tag>}
            </div>
          )}
          <span className="muted">{t('slack.requestURLs')}</span>
          {urls.map((u) => (
            <span key={u} className="mono">
              <Typography.Text copyable={{ text: u + 'commands' }}>{u}commands</Typography.Text> ·{' '}
              <Typography.Text copyable={{ text: u + 'interactive' }}>{u}interactive</Typography.Text>
            </span>
          ))}
          {via.length === 0 && <span className="muted">{t('slack.noEdge')}</span>}
          {s.channels.map((c) => (
            <div key={c.id} className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <span className="mono">{c.id}</span>
              {c.name && <span>#{c.name}</span>}
              <Tag color={c.role === 'admin' ? 'red' : 'blue'}>{t(`tokens.roles.${c.role}`)}</Tag>
              {c.notify && <Tag>{t('telegram.notify')}</Tag>}
              {s.has_token && (
                <Button
                  size="small"
                  onClick={async () => {
                    try {
                      await api('/hub/slack/test', { method: 'POST', body: { channel: c.id } })
                      setNote({ kind: 'info', text: t('telegram.testSent', { id: c.id }) })
                    } catch (err) {
                      setNote({ kind: 'error', text: errText(err) })
                    }
                  }}
                >
                  {t('telegram.test')}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {edit && s && <SlackModal st={s} kinds={kinds.data?.kinds ?? []} onClose={() => setEdit(false)} onSaved={() => void st.reload()} />}
    </Card>
  )
}

interface Draft {
  enabled: boolean
  token: string
  signing: string
  channels: Channel[]
  users: string
  kinds: string[]
  lang: 'ru' | 'en'
}

function SlackModal({ st, kinds, onClose, onSaved }: { st: SlackStatus; kinds: string[]; onClose: () => void; onSaved: () => void }) {
  const { t, i18n } = useTranslation()
  const kindLabel = (k: string) => t(`webhooks.kind.${k}`, { defaultValue: t(`events.kind.${k}`, { defaultValue: k }) })
  const lang = st.lang || (i18n.language.startsWith('en') ? 'en' : 'ru')
  const initial: Draft = { enabled: st.has_token ? st.enabled : true, token: '', signing: '', channels: st.channels, users: st.users.join(', '), kinds: st.kinds, lang }
  const [d, setD] = useState<Draft>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const text = (x: Draft) =>
    [
      `${t('telegram.enabled')}: ${x.enabled ? t('tokens.yes') : t('tokens.no')}`,
      `${t('slack.token')}: ${x.token ? t('telegram.tokenNew') : t('telegram.tokenKept')}`,
      `${t('slack.signing')}: ${x.signing ? t('telegram.tokenNew') : t('telegram.tokenKept')}`,
      ...x.channels.map((c) => `${t('slack.channel')} ${c.id}${c.name ? ` (#${c.name})` : ''}: ${t(`tokens.roles.${c.role}`)}${c.notify ? ', ' + t('telegram.notify') : ''}`),
      `${t('slack.users')}: ${x.users.trim() || '—'}`,
      `${t('webhooks.kinds')}: ${x.kinds.map(kindLabel).join(', ') || t('webhooks.allKinds')}`,
      `${t('webhooks.lang')}: ${x.lang}`,
      '',
    ].join('\n')
  const diff = unifiedDiff(text(initial), text(d), t('tokens.saved'), t('tokens.draft'))
  const changed = diff.trim() !== ''
  const setCh = (i: number, c: Partial<Channel>) => setD({ ...d, channels: d.channels.map((x, j) => (j === i ? { ...x, ...c } : x)) })
  return (
    <Modal title="Slack" onClose={onClose} width={720}>
      <p className="small muted">{t('slack.setupHint')}</p>
      <div className="col" style={{ gap: '0.5rem' }}>
        <label className="small">
          {t('slack.token')}
          <Input.Password value={d.token} autoComplete="new-password" placeholder={st.has_token ? t('telegram.tokenKeep', { name: st.bot_user }) : 'xoxb-…'} onChange={(e) => setD({ ...d, token: e.target.value.trim() })} />
        </label>
        <label className="small">
          {t('slack.signing')}
          <Input.Password value={d.signing} autoComplete="new-password" placeholder={st.has_signing ? t('slack.signingKeep') : 'Signing Secret'} onChange={(e) => setD({ ...d, signing: e.target.value.trim() })} />
        </label>
        <Checkbox checked={d.enabled} onChange={(e) => setD({ ...d, enabled: e.target.checked })}>
          {t('telegram.enabled')}
        </Checkbox>
        <div className="col small" style={{ gap: '0.3rem' }}>
          <span>{t('slack.channels')}</span>
          {d.channels.map((c, i) => (
            <Space key={i} wrap size={4}>
              <Input size="small" className="mono" style={{ width: '10rem' }} value={c.id} placeholder="C0123ABCD" onChange={(e) => setCh(i, { id: e.target.value.trim().toUpperCase() })} />
              <Input size="small" style={{ width: '10rem' }} value={c.name} placeholder={t('slack.channelName')} onChange={(e) => setCh(i, { name: e.target.value })} />
              <Select
                size="small"
                value={c.role}
                onChange={(role) => setCh(i, { role })}
                options={[
                  { value: 'read', label: t('tokens.roles.read') },
                  { value: 'admin', label: t('tokens.roles.admin') },
                ]}
              />
              <Checkbox checked={c.notify} onChange={(e) => setCh(i, { notify: e.target.checked })}>
                {t('telegram.notify')}
              </Checkbox>
              <Button size="small" danger onClick={() => setD({ ...d, channels: d.channels.filter((_, j) => j !== i) })}>
                {t('telegram.removeChat')}
              </Button>
            </Space>
          ))}
          <div>
            <Button size="small" onClick={() => setD({ ...d, channels: [...d.channels, { id: '', name: '', role: 'read', notify: true }] })}>
              {t('slack.addChannel')}
            </Button>
          </div>
        </div>
        <label className="small">
          {t('slack.users')}
          <Input className="mono" value={d.users} placeholder="U0123ABCD, U0456EFGH" onChange={(e) => setD({ ...d, users: e.target.value })} />
          <span className="muted">{t('slack.usersHint')}</span>
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
            onChange={(v) => setD({ ...d, lang: v })}
            options={[
              { value: 'ru', label: 'русский' },
              { value: 'en', label: 'English' },
            ]}
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
                  .map((u) => u.toUpperCase())
                await api('/hub/slack', {
                  method: 'PUT',
                  body: { enabled: d.enabled, token: d.token, signing_secret: d.signing, channels: d.channels.filter((c) => c.id), users, kinds: d.kinds, lang: d.lang },
                })
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
