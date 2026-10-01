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

/** API-токен хаба (секрет не приходит — показывается один раз). */
interface ApiToken {
  id: number
  name: string
  key_id: string
  role: 'read' | 'admin'
  hosts: number[]
  groups: string[]
  ips: string[]
  expires_at?: string
  via_edge: boolean
  author?: string
  created_at: string
  last_used_at?: string
  last_ip?: string
}

interface Issued {
  id: number
  key_id: string
  secret: string
  token: string
}

const LOCAL_ID = -1

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function expired(tk: ApiToken): boolean {
  return !!tk.expires_at && new Date(tk.expires_at).getTime() <= Date.now()
}

/** «API-токены» в «О системе»: доступ для n8n, CI и скриптов. */
export function ApiTokensCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const list = useApi<{ tokens: ApiToken[] }>(admin ? '/hub/tokens' : null)
  const hosts = useApi<HubHost[]>(admin ? '/hub/hosts' : null)
  const [edit, setEdit] = useState<ApiToken | 'new' | null>(null)
  const [issued, setIssued] = useState<{ name: string; data: Issued } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const hostName = useMemo(() => {
    const m = new Map<number, string>()
    for (const h of hosts.data ?? []) m.set(h.id, h.name)
    m.set(LOCAL_ID, 'localhost')
    return m
  }, [hosts.data])

  if (!admin) return null
  const columns = [
    {
      title: t('tokens.name'),
      key: 'name',
      render: (_: unknown, tk: ApiToken) => (
        <span>
          <strong>{tk.name}</strong> <span className="mono small muted">{tk.key_id}</span>
        </span>
      ),
    },
    {
      title: t('tokens.role'),
      key: 'role',
      render: (_: unknown, tk: ApiToken) => (
        <Space size={2} wrap>
          <Tag color={tk.role === 'admin' ? 'red' : 'blue'}>{t(`tokens.roles.${tk.role}`)}</Tag>
          {tk.via_edge && <Tag color="purple">{t('tokens.edgeTag')}</Tag>}
        </Space>
      ),
    },
    {
      title: t('tokens.scope'),
      key: 'scope',
      render: (_: unknown, tk: ApiToken) =>
        tk.hosts.length === 0 && tk.groups.length === 0 ? (
          <span className="small muted">{t('tokens.allHosts')}</span>
        ) : (
          <span className="small">
            {[...tk.groups.map((g) => t('tokens.groupRef', { name: g })), ...tk.hosts.map((id) => hostName.get(id) ?? `#${id}`)].join(', ')}
          </span>
        ),
    },
    {
      title: t('tokens.ips'),
      key: 'ips',
      render: (_: unknown, tk: ApiToken) => <span className="mono small">{tk.ips.length ? tk.ips.join(', ') : <span className="muted">{t('tokens.anyIP')}</span>}</span>,
    },
    {
      title: t('tokens.expires'),
      key: 'expires',
      render: (_: unknown, tk: ApiToken) =>
        !tk.expires_at ? (
          <span className="small muted">{t('tokens.never')}</span>
        ) : expired(tk) ? (
          <Tag color="red">{t('tokens.expired')}</Tag>
        ) : (
          <span className="small nowrap">{new Date(tk.expires_at).toLocaleDateString()}</span>
        ),
    },
    {
      title: t('tokens.lastUsed'),
      key: 'last',
      render: (_: unknown, tk: ApiToken) =>
        tk.last_used_at ? (
          <span className="small nowrap">
            {formatRelative(tk.last_used_at)} <span className="mono muted">{tk.last_ip}</span>
          </span>
        ) : (
          <span className="small muted">{t('tokens.unused')}</span>
        ),
    },
    {
      title: '',
      key: 'actions',
      render: (_: unknown, tk: ApiToken) => (
        <Space size={4} wrap>
          <Button size="small" onClick={() => setEdit(tk)}>
            {t('tokens.edit')}
          </Button>
          <Tooltip title={t('tokens.rotateHint')}>
            <Button
              size="small"
              onClick={async () => {
                if (!(await confirmAction(t('tokens.rotateConfirm', { name: tk.name })))) return
                try {
                  const data = await api<Issued>(`/hub/tokens/${tk.id}/rotate`, { method: 'POST' })
                  setIssued({ name: tk.name, data })
                  void list.reload()
                } catch (err) {
                  setError(errText(err))
                }
              }}
            >
              {t('tokens.rotate')}
            </Button>
          </Tooltip>
          <Button
            size="small"
            danger
            onClick={async () => {
              if (!(await confirmAction(t('tokens.deleteConfirm', { name: tk.name }), { danger: true }))) return
              try {
                await api(`/hub/tokens/${tk.id}`, { method: 'DELETE' })
                void list.reload()
              } catch (err) {
                setError(errText(err))
              }
            }}
          >
            {t('tokens.delete')}
          </Button>
        </Space>
      ),
    },
  ]
  return (
    <Card
      title={t('tokens.title')}
      subtitle={t('tokens.hint')}
      actions={
        <Space size={4}>
          <HelpButton docKey="hub:api" isHub admin label={t('tokens.help')} />
          <Button size="small" type="primary" onClick={() => setEdit('new')}>
            {t('tokens.create')}
          </Button>
        </Space>
      }
    >
      <ErrorNote error={list.error} />
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {list.loading && !list.data ? (
        <Loading what={t('tokens.title')} />
      ) : (list.data?.tokens ?? []).length === 0 ? (
        <p className="small muted">{t('tokens.empty')}</p>
      ) : (
        <DataTable rowKey="id" size="small" pagination={false} columns={columns} dataSource={list.data?.tokens ?? []} />
      )}
      {edit && (
        <TokenModal
          token={edit === 'new' ? undefined : edit}
          hosts={hosts.data ?? []}
          hostName={hostName}
          onClose={() => setEdit(null)}
          onSaved={(name, data) => {
            setEdit(null)
            if (data) setIssued({ name, data })
            void list.reload()
          }}
        />
      )}
      {issued && <IssuedModal name={issued.name} data={issued.data} onClose={() => setIssued(null)} />}
    </Card>
  )
}

interface Draft {
  name: string
  role: 'read' | 'admin'
  hosts: number[]
  groups: string[]
  ips: string
  /** Срок в днях; -1 — оставить прежний (правка). */
  days: number
  viaEdge: boolean
}

/** Текст токена для диффа перед записью. */
function draftText(d: Draft, hostName: Map<number, string>, expiresLabel: string, t: (k: string) => string): string {
  return [
    `${t('tokens.name')}: ${d.name}`,
    `${t('tokens.role')}: ${t(`tokens.roles.${d.role}`)}`,
    `${t('tokens.hosts')}: ${d.hosts.map((id) => hostName.get(id) ?? `#${id}`).join(', ') || '—'}`,
    `${t('tokens.groups')}: ${d.groups.join(', ') || '—'}`,
    `${t('tokens.ips')}: ${splitIPs(d.ips).join(', ') || '—'}`,
    `${t('tokens.expires')}: ${expiresLabel}`,
    `${t('tokens.viaEdge')}: ${d.viaEdge ? t('tokens.yes') : t('tokens.no')}`,
    '',
  ].join('\n')
}

function splitIPs(s: string): string[] {
  return s
    .split(/[\s,;]+/)
    .map((x) => x.trim())
    .filter(Boolean)
}

/** Новый токен или правка: поля, дифф с сохранённым — и только потом запись. */
function TokenModal({
  token,
  hosts,
  hostName,
  onClose,
  onSaved,
}: {
  token?: ApiToken
  hosts: HubHost[]
  hostName: Map<number, string>
  onClose: () => void
  onSaved: (name: string, issued?: Issued) => void
}) {
  const { t } = useTranslation()
  const groups = useApi<{ groups: string[] }>('/hub/groups')
  const initial: Draft = token
    ? { name: token.name, role: token.role, hosts: token.hosts, groups: token.groups, ips: token.ips.join(', '), days: -1, viaEdge: token.via_edge }
    : { name: '', role: 'read', hosts: [], groups: [], ips: '', days: 90, viaEdge: false }
  const [d, setD] = useState<Draft>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const expiryLabel = (days: number) =>
    days === -1
      ? token?.expires_at
        ? new Date(token.expires_at).toLocaleDateString()
        : t('tokens.never')
      : days === 0
        ? t('tokens.never')
        : t('tokens.days', { count: days })
  const diff = token
    ? unifiedDiff(draftText(initial, hostName, expiryLabel(-1), t), draftText(d, hostName, expiryLabel(d.days), t), t('tokens.saved'), t('tokens.draft'))
    : ''
  const changed = !token || diff.trim() !== ''
  const hostOptions = [{ value: LOCAL_ID, label: 'localhost' }, ...hosts.filter((h) => h.id !== LOCAL_ID).map((h) => ({ value: h.id, label: h.name }))]
  return (
    <Modal title={token ? t('tokens.editTitle', { name: token.name }) : t('tokens.newTitle')} onClose={onClose} width={640}>
      <div className="col" style={{ gap: '0.5rem' }}>
        <label className="small">
          {t('tokens.name')}
          <Input value={d.name} maxLength={64} placeholder="n8n" onChange={(e) => setD({ ...d, name: e.target.value })} />
        </label>
        <label className="small">
          {t('tokens.role')}
          <Select
            style={{ width: '100%' }}
            value={d.role}
            onChange={(role) => setD({ ...d, role })}
            options={[
              { value: 'read', label: `${t('tokens.roles.read')} — ${t('tokens.roleHint.read')}` },
              { value: 'admin', label: `${t('tokens.roles.admin')} — ${t('tokens.roleHint.admin')}` },
            ]}
          />
        </label>
        <span className="small muted">{t('tokens.scopeHint')}</span>
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
        <label className="small">
          {t('tokens.ips')}
          <Input className="mono" value={d.ips} placeholder="203.0.113.5, 10.0.0.0/8" onChange={(e) => setD({ ...d, ips: e.target.value })} />
        </label>
        <label className="small">
          {t('tokens.expires')}
          <Select
            style={{ width: '100%' }}
            value={d.days}
            onChange={(days) => setD({ ...d, days })}
            options={[
              ...(token ? [{ value: -1, label: t('tokens.keepExpiry', { date: expiryLabel(-1) }) }] : []),
              { value: 30, label: t('tokens.days', { count: 30 }) },
              { value: 90, label: t('tokens.days', { count: 90 }) },
              { value: 365, label: t('tokens.days', { count: 365 }) },
              { value: 0, label: t('tokens.never') },
            ]}
          />
        </label>
        <Checkbox checked={d.viaEdge} onChange={(e) => setD({ ...d, viaEdge: e.target.checked })}>
          {t('tokens.viaEdge')} <span className="small muted">— {t('tokens.viaEdgeHint')}</span>
        </Checkbox>
        {token && changed && (
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
            disabled={!d.name.trim() || !changed}
            onClick={async () => {
              setBusy(true)
              setError(null)
              const body = {
                name: d.name.trim(),
                role: d.role,
                hosts: d.hosts,
                groups: d.groups,
                ips: splitIPs(d.ips),
                expires_days: Math.max(d.days, 0),
                keep_expiry: d.days === -1,
                via_edge: d.viaEdge,
              }
              try {
                if (token) {
                  await api(`/hub/tokens/${token.id}`, { method: 'PUT', body })
                  onSaved(body.name)
                } else {
                  onSaved(body.name, await api<Issued>('/hub/tokens', { method: 'POST', body }))
                }
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {token ? t('tokens.save') : t('tokens.create')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/** Секрет — один раз: строка для Bearer и пара ключ/секрет для подписи. */
function IssuedModal({ name, data, onClose }: { name: string; data: Issued; onClose: () => void }) {
  const { t } = useTranslation()
  const origin = window.location.origin
  return (
    <Modal title={t('tokens.issuedTitle', { name })} onClose={onClose} width={720} maskClosable={false}>
      <Banner kind="warn">{t('tokens.issuedOnce')}</Banner>
      <p className="small">{t('tokens.issuedBearer')}</p>
      <Typography.Paragraph copyable={{ text: data.token }} className="mono small" style={{ wordBreak: 'break-all' }}>
        {data.token}
      </Typography.Paragraph>
      <p className="small">{t('tokens.issuedSigned')}</p>
      <Typography.Paragraph copyable={{ text: data.key_id }} className="mono small">
        X-NKT-API-Key: {data.key_id}
      </Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: data.secret }} className="mono small" style={{ wordBreak: 'break-all' }}>
        {t('tokens.secret')}: {data.secret}
      </Typography.Paragraph>
      <p className="small">{t('tokens.issuedExample')}</p>
      <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
        {`curl -H "Authorization: Bearer ${data.token}" ${origin}/api/hub/hosts`}
      </pre>
    </Modal>
  )
}
