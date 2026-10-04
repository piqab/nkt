import { useMemo, useState } from 'react'
import { Button, Checkbox, Input, Select, Space, Tag, Tooltip, Typography } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost } from '../types'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { confirmAction } from './confirm'
import { unifiedDiff } from './textDiff'
import { HelpButton } from './Docs'
import { msg, tx, type Msg } from '../msg'

/** Адресат исходящих вебхуков (секрет не приходит — показывается один раз). */
interface OutHook {
  id: number
  name: string
  url: string
  kinds: string[]
  hosts: number[]
  groups: string[]
  lang: 'ru' | 'en'
  enabled: boolean
  has_secret: boolean
  last_at?: string
  last_kind?: string
  last_code?: number
  last_error?: string
}

const LOCAL_ID = -1

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** Название вида события: оповещения хостов и выкладки. */
function useKindLabel() {
  const { t } = useTranslation()
  return (k: string) => t(`webhooks.kind.${k}`, { defaultValue: t(`events.kind.${k}`, { defaultValue: k }) })
}

/** «Исходящие вебхуки» в «Оповещениях»: хаб сам шлёт события наружу. */
export function OutgoingWebhooksCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const kindLabel = useKindLabel()
  const list = useApi<{ hooks: OutHook[]; kinds: string[] }>(admin ? '/hub/webhooks' : null, 15_000)
  const hosts = useApi<HubHost[]>(admin ? '/hub/hosts' : null)
  const [edit, setEdit] = useState<OutHook | 'new' | null>(null)
  const [secret, setSecret] = useState<{ name: string; secret: string } | null>(null)
  const [error, setError] = useState<Msg | null>(null)
  const [testing, setTesting] = useState<number | null>(null)
  const [note, setNote] = useState<Msg | null>(null)
  const hostName = useMemo(() => {
    const m = new Map<number, string>()
    for (const h of hosts.data ?? []) m.set(h.id, h.name)
    m.set(LOCAL_ID, 'localhost')
    return m
  }, [hosts.data])
  if (!admin) return null
  const kinds = list.data?.kinds ?? []
  const columns = [
    {
      title: t('webhooks.name'),
      key: 'name',
      render: (_: unknown, h: OutHook) => (
        <div className="col" style={{ gap: 0 }}>
          <span>
            <strong>{h.name}</strong> {!h.enabled && <Tag>{t('webhooks.off')}</Tag>}
          </span>
          <span className="mono small muted" style={{ wordBreak: 'break-all' }}>
            {h.url}
          </span>
        </div>
      ),
    },
    {
      title: t('webhooks.kinds'),
      key: 'kinds',
      render: (_: unknown, h: OutHook) =>
        h.kinds.length === 0 ? (
          <span className="small muted">{t('webhooks.allKinds')}</span>
        ) : (
          <Space size={2} wrap>
            {h.kinds.map((k) => (
              <Tag key={k}>{kindLabel(k)}</Tag>
            ))}
          </Space>
        ),
    },
    {
      title: t('webhooks.scope'),
      key: 'scope',
      render: (_: unknown, h: OutHook) =>
        h.hosts.length === 0 && h.groups.length === 0 ? (
          <span className="small muted">{t('tokens.allHosts')}</span>
        ) : (
          <span className="small">{[...h.groups.map((g) => t('tokens.groupRef', { name: g })), ...h.hosts.map((id) => hostName.get(id) ?? `#${id}`)].join(', ')}</span>
        ),
    },
    {
      title: t('webhooks.last'),
      key: 'last',
      render: (_: unknown, h: OutHook) =>
        !h.last_at ? (
          <span className="small muted">{t('webhooks.never')}</span>
        ) : (
          <Tooltip title={h.last_error}>
            <span className="small nowrap">
              <Tag color={h.last_error ? 'red' : 'green'}>{h.last_error ? t('webhooks.failed') : h.last_code}</Tag>
              {kindLabel(h.last_kind ?? '')} · {formatRelative(h.last_at)}
            </span>
          </Tooltip>
        ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, h: OutHook) => (
        <Space size={4} wrap>
          <Button size="small" onClick={() => setEdit(h)}>
            {t('tokens.edit')}
          </Button>
          <Button
            size="small"
            loading={testing === h.id}
            onClick={async () => {
              setTesting(h.id)
              setError(null)
              setNote(null)
              try {
                const res = await api<{ code: number; error?: string }>(`/hub/webhooks/${h.id}/test`, { method: 'POST' })
                if (res.error) setError(tx('webhooks.testFailed', { name: h.name, error: res.error }))
                else setNote(tx('webhooks.testOK', { name: h.name, code: res.code }))
                void list.reload()
              } catch (err) {
                setError(errText(err))
              } finally {
                setTesting(null)
              }
            }}
          >
            {t('webhooks.test')}
          </Button>
          <Button
            size="small"
            onClick={async () => {
              if (!(await confirmAction(t('webhooks.rotateConfirm', { name: h.name })))) return
              try {
                const res = await api<{ secret: string }>(`/hub/webhooks/${h.id}/rotate`, { method: 'POST' })
                setSecret({ name: h.name, secret: res.secret })
              } catch (err) {
                setError(errText(err))
              }
            }}
          >
            {t('tokens.rotate')}
          </Button>
          <Button
            size="small"
            danger
            onClick={async () => {
              if (!(await confirmAction(t('webhooks.deleteConfirm', { name: h.name }), { danger: true }))) return
              try {
                await api(`/hub/webhooks/${h.id}`, { method: 'DELETE' })
                void list.reload()
              } catch (err) {
                setError(errText(err))
              }
            }}
          >
            {t('webhooks.delete')}
          </Button>
        </Space>
      ),
    },
  ]
  return (
    <Card
      title={t('webhooks.title')}
      subtitle={t('webhooks.hint')}
      actions={
        <Space size={4}>
          <HelpButton docKey="hub:webhooks" isHub admin label={t('webhooks.help')} />
          <Button size="small" type="primary" onClick={() => setEdit('new')}>
            {t('webhooks.create')}
          </Button>
        </Space>
      }
    >
      <ErrorNote error={list.error} />
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {msg(error)}
        </Banner>
      )}
      {note && (
        <Banner kind="info" onClose={() => setNote(null)}>
          {msg(note)}
        </Banner>
      )}
      {list.loading && !list.data ? (
        <Loading what={t('webhooks.title')} />
      ) : (list.data?.hooks ?? []).length === 0 ? (
        <p className="small muted">{t('webhooks.empty')}</p>
      ) : (
        <DataTable rowKey="id" size="small" pagination={false} columns={columns} dataSource={list.data?.hooks ?? []} />
      )}
      {edit && (
        <HookModal
          hook={edit === 'new' ? undefined : edit}
          kinds={kinds}
          hosts={hosts.data ?? []}
          hostName={hostName}
          onClose={() => setEdit(null)}
          onSaved={(name, sec) => {
            setEdit(null)
            if (sec) setSecret({ name, secret: sec })
            void list.reload()
          }}
        />
      )}
      {secret && <SecretModal name={secret.name} secret={secret.secret} onClose={() => setSecret(null)} />}
    </Card>
  )
}

interface Draft {
  name: string
  url: string
  kinds: string[]
  hosts: number[]
  groups: string[]
  lang: 'ru' | 'en'
  enabled: boolean
}

function draftText(d: Draft, hostName: Map<number, string>, kindLabel: (k: string) => string, t: (k: string) => string): string {
  return [
    `${t('webhooks.name')}: ${d.name}`,
    `URL: ${d.url}`,
    `${t('webhooks.kinds')}: ${d.kinds.map(kindLabel).join(', ') || t('webhooks.allKinds')}`,
    `${t('tokens.hosts')}: ${d.hosts.map((id) => hostName.get(id) ?? `#${id}`).join(', ') || '—'}`,
    `${t('tokens.groups')}: ${d.groups.join(', ') || '—'}`,
    `${t('webhooks.lang')}: ${d.lang}`,
    `${t('webhooks.enabled')}: ${d.enabled ? t('tokens.yes') : t('tokens.no')}`,
    '',
  ].join('\n')
}

/** Новый адресат или правка: поля, дифф с сохранённым — и только потом запись. */
function HookModal({
  hook,
  kinds,
  hosts,
  hostName,
  onClose,
  onSaved,
}: {
  hook?: OutHook
  kinds: string[]
  hosts: HubHost[]
  hostName: Map<number, string>
  onClose: () => void
  onSaved: (name: string, secret?: string) => void
}) {
  const { t, i18n } = useTranslation()
  const kindLabel = useKindLabel()
  const groups = useApi<{ groups: string[] }>('/hub/groups')
  const initial: Draft = hook
    ? { name: hook.name, url: hook.url, kinds: hook.kinds, hosts: hook.hosts, groups: hook.groups, lang: hook.lang, enabled: hook.enabled }
    : { name: '', url: '', kinds: ['unreachable', 'problems', 'job-failed', 'deploy-failed'], hosts: [], groups: [], lang: i18n.language.startsWith('en') ? 'en' : 'ru', enabled: true }
  const [d, setD] = useState<Draft>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Msg | null>(null)
  const diff = hook ? unifiedDiff(draftText(initial, hostName, kindLabel, t), draftText(d, hostName, kindLabel, t), t('tokens.saved'), t('tokens.draft')) : ''
  const changed = !hook || diff.trim() !== ''
  const hostOptions = [{ value: LOCAL_ID, label: 'localhost' }, ...hosts.filter((h) => h.id !== LOCAL_ID).map((h) => ({ value: h.id, label: h.name }))]
  return (
    <Modal title={hook ? t('webhooks.editTitle', { name: hook.name }) : t('webhooks.newTitle')} onClose={onClose} width={680}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <label className="small">
          {t('webhooks.name')}
          <Input value={d.name} maxLength={64} placeholder="n8n" onChange={(e) => setD({ ...d, name: e.target.value })} />
        </label>
        <label className="small">
          URL
          <Input className="mono" value={d.url} placeholder="https://n8n.example.com/webhook/nkt" onChange={(e) => setD({ ...d, url: e.target.value.trim() })} />
        </label>
        <div className="col small" style={{ gap: '0.2rem' }}>
          <span>{t('webhooks.kinds')}</span>
          <Space size={[8, 2]} wrap>
            {kinds.map((k) => (
              <Checkbox key={k} checked={d.kinds.includes(k)} onChange={(e) => setD({ ...d, kinds: e.target.checked ? kinds.filter((x) => x === k || d.kinds.includes(x)) : d.kinds.filter((x) => x !== k) })}>
                {kindLabel(k)}
              </Checkbox>
            ))}
          </Space>
          <span className="muted">{t('webhooks.kindsHint')}</span>
        </div>
        <span className="small muted">{t('webhooks.scopeHint')}</span>
        <label className="small">
          {t('tokens.hosts')}
          <Select mode="multiple" style={{ width: '100%' }} value={d.hosts} placeholder={t('tokens.allHosts')} onChange={(v) => setD({ ...d, hosts: v })} options={hostOptions} optionFilterProp="label" />
        </label>
        <label className="small">
          {t('tokens.groups')}
          <Select
            mode="multiple"
            style={{ width: '100%' }}
            value={d.groups}
            placeholder={t('tokens.allHosts')}
            onChange={(v) => setD({ ...d, groups: v })}
            options={(groups.data?.groups ?? []).map((g) => ({ value: g, label: g }))}
          />
        </label>
        <Space>
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
          <Checkbox checked={d.enabled} onChange={(e) => setD({ ...d, enabled: e.target.checked })}>
            {t('webhooks.enabled')}
          </Checkbox>
        </Space>
        {hook && changed && (
          <>
            <span className="small muted">{t('tokens.diffHint')}</span>
            <DiffView text={diff} />
          </>
        )}
        {error && <Banner kind="error">{msg(error)}</Banner>}
        <div>
          <Button
            type="primary"
            loading={busy}
            disabled={!d.name.trim() || !d.url || !changed}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                const body = { ...d, name: d.name.trim() }
                if (hook) {
                  await api(`/hub/webhooks/${hook.id}`, { method: 'PUT', body })
                  onSaved(body.name)
                } else {
                  const res = await api<{ secret: string }>('/hub/webhooks', { method: 'POST', body })
                  onSaved(body.name, res.secret)
                }
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {hook ? t('tokens.save') : t('webhooks.create')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/** Секрет подписи — один раз, с примером проверки. */
function SecretModal({ name, secret, onClose }: { name: string; secret: string; onClose: () => void }) {
  const { t } = useTranslation()
  return (
    <Modal title={t('webhooks.secretTitle', { name })} onClose={onClose} width={680} maskClosable={false}>
      <Banner kind="warn">{t('tokens.issuedOnce')}</Banner>
      <Typography.Paragraph copyable={{ text: secret }} className="mono small" style={{ wordBreak: 'break-all' }}>
        {secret}
      </Typography.Paragraph>
      <p className="small">{t('webhooks.verifyHint')}</p>
      <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap' }}>
        {`// n8n, Code
const crypto = require('crypto')
const ts = $json.headers['x-nkt-timestamp']
const sig = crypto.createHmac('sha256', '<secret>')
  .update(ts + '.' + JSON.stringify($json.body)).digest('hex')
if (sig !== $json.headers['x-nkt-signature']) throw new Error('bad signature')`}
      </pre>
    </Modal>
  )
}
