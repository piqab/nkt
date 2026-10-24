import { useState } from 'react'
import { Button, Checkbox, Collapse, InputNumber, Select, Space, Typography } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Loading } from './ui'
import { confirmAction } from './confirm'

/**
 * Токен «по правам»: какие действия ему можно, на каких объектах
 * (конвейеры, сценарии, кластеры по именам, namespace k8s) и потолки
 * ресурсов — суммой по всему, что токен выложил и создал, и на хост.
 * Заготовки заполняют поля для частых случаев.
 */

export interface TokenLimits {
  cpu?: number
  mem_mb?: number
  disk_gb?: number
  host_cpu?: number
  host_mem_mb?: number
  service_cpu?: number
  service_mem_mb?: number
  min_free_gb?: number
  vms?: number
  vm_cpu?: number
  vm_mem_mb?: number
  vm_disk_gb?: number
  k8s_cpu?: number
  k8s_mem_mb?: number
  k8s_storage_gb?: number
  k8s_pods?: number
  deploys_per_hour?: number
}

export interface TokenPerms {
  actions: string[]
  pipelines?: string[]
  scripts?: string[]
  clusters?: string[]
  namespaces?: string[]
  limits: TokenLimits
}

export const TOKEN_ACTIONS = [
  'pipeline.deploy',
  'pipeline.power',
  'pipeline.create',
  'script.run',
  'k8s.apply',
  'vm.manage',
  'container.action',
  'service.action',
  'fly.manage',
  'migrate',
  'fail2ban.manage',
  'vuln.scan',
]

export function emptyPerms(): TokenPerms {
  return { actions: [], pipelines: [], scripts: [], clusters: [], namespaces: [], limits: {} }
}

type T = (k: string, o?: Record<string, unknown>) => string

// Поля потолков по группам: ключ, шаг, единица.
const LIMIT_GROUPS: { key: string; fields: { k: keyof TokenLimits; step: number; unit: string }[] }[] = [
  { key: 'total', fields: [{ k: 'cpu', step: 0.5, unit: 'cpu' }, { k: 'mem_mb', step: 256, unit: 'mb' }, { k: 'disk_gb', step: 10, unit: 'gb' }] },
  { key: 'host', fields: [{ k: 'host_cpu', step: 0.5, unit: 'cpu' }, { k: 'host_mem_mb', step: 256, unit: 'mb' }] },
  { key: 'service', fields: [{ k: 'service_cpu', step: 0.25, unit: 'cpu' }, { k: 'service_mem_mb', step: 128, unit: 'mb' }, { k: 'min_free_gb', step: 5, unit: 'gb' }] },
  { key: 'vm', fields: [{ k: 'vms', step: 1, unit: 'count' }, { k: 'vm_cpu', step: 1, unit: 'cpu' }, { k: 'vm_mem_mb', step: 512, unit: 'mb' }, { k: 'vm_disk_gb', step: 10, unit: 'gb' }] },
  { key: 'k8s', fields: [{ k: 'k8s_cpu', step: 0.5, unit: 'cpu' }, { k: 'k8s_mem_mb', step: 256, unit: 'mb' }, { k: 'k8s_storage_gb', step: 5, unit: 'gb' }, { k: 'k8s_pods', step: 1, unit: 'count' }] },
  { key: 'rate', fields: [{ k: 'deploys_per_hour', step: 1, unit: 'count' }] },
]

/** Заготовки: частые случаи одним выбором. */
const PRESETS: Record<string, TokenPerms | 'read'> = {
  read: 'read',
  ci: { actions: ['pipeline.deploy', 'pipeline.power'], limits: { deploys_per_hour: 20 } },
  compose: {
    actions: ['pipeline.deploy', 'pipeline.power', 'pipeline.create', 'container.action'],
    limits: { cpu: 2, mem_mb: 4096, host_cpu: 2, host_mem_mb: 4096, service_cpu: 1, service_mem_mb: 1024, min_free_gb: 5, deploys_per_hour: 30 },
  },
  k8s: { actions: ['k8s.apply'], namespaces: ['team-a'], limits: { k8s_cpu: 4, k8s_mem_mb: 8192, k8s_storage_gb: 50, k8s_pods: 30, service_cpu: 1, service_mem_mb: 1024, deploys_per_hour: 30 } },
  vps: { actions: ['vm.manage'], limits: { vms: 3, vm_cpu: 2, vm_mem_mb: 4096, vm_disk_gb: 40, cpu: 6, mem_mb: 12288, disk_gb: 120 } },
}

/** Подпись действия: plugin.<имя> — «плагин <имя>: его действия». */
function actLabel(a: string, t: T): string {
  return a.startsWith('plugin.') ? t('tokens.act.pluginAct', { name: a.slice('plugin.'.length) }) : t(`tokens.act.${a}`)
}

/** Права и потолки текстом — для диффа перед записью. */
export function permsText(p: TokenPerms, t: T): string[] {
  const out = [`${t('tokens.actions')}: ${p.actions.map((a) => actLabel(a, t)).join(', ') || '—'}`]
  if (p.pipelines?.length) out.push(`${t('tokens.pipelines')}: ${p.pipelines.join(', ')}`)
  if (p.scripts?.length) out.push(`${t('tokens.scripts')}: ${p.scripts.join(', ')}`)
  if (p.clusters?.length) out.push(`${t('tokens.clusters')}: ${p.clusters.join(', ')}`)
  if (p.namespaces?.length) out.push(`${t('tokens.namespaces')}: ${p.namespaces.join(', ')}`)
  for (const g of LIMIT_GROUPS) {
    for (const f of g.fields) {
      const v = p.limits[f.k]
      if (v) out.push(`${t(`tokens.limit.${f.k}`)}: ${v}`)
    }
  }
  return out
}

export function TokenPermsEditor({ perms, onChange, onRole }: { perms: TokenPerms; onChange: (p: TokenPerms) => void; onRole: (r: 'read') => void }) {
  const { t } = useTranslation()
  const pipelines = useApi<{ pipelines: { name: string }[] }>('/hub/pipelines')
  const scripts = useApi<{ scripts: { name: string }[] }>('/hub/scripts')
  const clusters = useApi<{ clusters: { name: string }[] } | { name: string }[]>('/hub/clusters')
  // Действия подключённых плагинов: plugin.<имя>.
  const plugins = useApi<{ plugins: { name: string }[] }>('/hub/plugins')
  const actions = [...TOKEN_ACTIONS, ...(plugins.data?.plugins ?? []).map((p) => `plugin.${p.name}`)]
  const clusterNames = (() => {
    const d = clusters.data
    const list = Array.isArray(d) ? d : (d?.clusters ?? [])
    return list.map((c) => c.name)
  })()
  const has = (a: string) => perms.actions.includes(a)
  const setLimit = (k: keyof TokenLimits, v: number | null) => onChange({ ...perms, limits: { ...perms.limits, [k]: v ?? undefined } })
  return (
    <div className="col token-perms" style={{ gap: '0.5rem' }}>
      <label className="small">
        {t('tokens.preset')}
        <Select
          style={{ width: '100%' }}
          placeholder={t('tokens.presetPlaceholder')}
          value={null}
          onChange={(k: string) => {
            const p = PRESETS[k]
            if (p === 'read') onRole('read')
            else onChange({ ...emptyPerms(), ...p, limits: { ...p.limits } })
          }}
          options={Object.keys(PRESETS).map((k) => ({ value: k, label: t(`tokens.presets.${k}`) }))}
        />
      </label>
      <div className="small">{t('tokens.actions')}</div>
      <Checkbox.Group
        value={perms.actions}
        onChange={(v) => onChange({ ...perms, actions: v as string[] })}
        options={actions.map((a) => ({ value: a, label: actLabel(a, t) }))}
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(230px, 1fr))', gap: '0.2rem' }}
      />
      {(has('pipeline.deploy') || has('pipeline.power')) && (
        <label className="small">
          {t('tokens.pipelines')}
          <Select mode="multiple" style={{ width: '100%' }} value={perms.pipelines ?? []} placeholder={t('tokens.allInScope')} onChange={(v) => onChange({ ...perms, pipelines: v })} options={(pipelines.data?.pipelines ?? []).map((p) => ({ value: p.name, label: p.name }))} />
        </label>
      )}
      {has('script.run') && (
        <label className="small">
          {t('tokens.scripts')}
          <Select mode="multiple" style={{ width: '100%' }} value={perms.scripts ?? []} placeholder={t('tokens.allInScope')} onChange={(v) => onChange({ ...perms, scripts: v })} options={(scripts.data?.scripts ?? []).map((s) => ({ value: s.name, label: s.name }))} />
        </label>
      )}
      {has('k8s.apply') && (
        <>
          <label className="small">
            {t('tokens.clusters')}
            <Select mode="multiple" style={{ width: '100%' }} value={perms.clusters ?? []} placeholder={t('tokens.allInScope')} onChange={(v) => onChange({ ...perms, clusters: v })} options={clusterNames.map((n) => ({ value: n, label: n }))} />
          </label>
          <label className="small">
            {t('tokens.namespaces')}
            <Select mode="tags" style={{ width: '100%' }} value={perms.namespaces ?? []} placeholder="team-a" onChange={(v) => onChange({ ...perms, namespaces: v })} tokenSeparators={[',', ' ']} />
            <span className="muted">{t('tokens.namespacesHint')}</span>
          </label>
        </>
      )}
      <div className="small">{t('tokens.limits')}</div>
      <span className="small muted">{t('tokens.limitsHint')}</span>
      {LIMIT_GROUPS.map((g) => (
        <div key={g.key} className="token-limit-group">
          <div className="small muted">{t(`tokens.limitGroup.${g.key}`)}</div>
          <Space wrap>
            {g.fields.map((f) => (
              <label key={f.k} className="small" style={{ display: 'inline-flex', flexDirection: 'column' }}>
                {t(`tokens.limit.${f.k}`)}
                <InputNumber min={0} step={f.step} value={perms.limits[f.k] ?? null} placeholder="∞" onChange={(v) => setLimit(f.k, v)} style={{ width: 130 }} addonAfter={t(`tokens.unit.${f.unit}`)} />
              </label>
            ))}
          </Space>
        </div>
      ))}
    </div>
  )
}

interface UsageEntry {
  kind: 'compose' | 'vm' | 'k8s'
  host_id: number
  ref: string
  label?: string
  cpu: number
  mem_mb: number
  disk_gb: number
}
interface UsageView {
  limits: TokenLimits
  usage: UsageEntry[]
  used: { cpu: number; mem_mb: number; disk_gb: number; vms: number }
}

/** Что токен занял: суммы против потолков и записи с «Освободить». */
export function TokenUsagePanel({ tokenID }: { tokenID: number }) {
  const { t } = useTranslation()
  const view = useApi<UsageView>(`/hub/tokens/${tokenID}/usage`)
  const [error, setError] = useState<string | null>(null)
  if (view.loading && !view.data) return <Loading what={t('tokens.usage')} />
  const v = view.data
  if (!v) return null
  const of = (used: number, limit?: number) => (limit ? t('tokens.usedOf', { used: Math.round(used * 100) / 100, limit }) : t('tokens.usedNoLimit', { used: Math.round(used * 100) / 100 }))
  return (
    <div className="col" style={{ gap: '0.3rem' }}>
      <div className="small">
        <strong>{t('tokens.usage')}</strong> · CPU {of(v.used.cpu, v.limits.cpu)} · {t('tokens.unit.mb')} {of(v.used.mem_mb, v.limits.mem_mb)} · {t('tokens.unit.gb')}{' '}
        {of(v.used.disk_gb, v.limits.disk_gb)} · {t('tokens.limit.vms')} {of(v.used.vms, v.limits.vms)}
      </div>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {v.usage.length === 0 ? (
        <span className="small muted">{t('tokens.usageEmpty')}</span>
      ) : (
        v.usage.map((u) => (
          <div key={`${u.kind}-${u.host_id}-${u.ref}`} className="row small" style={{ gap: '0.5rem', alignItems: 'center' }}>
            <span className="muted">{t(`tokens.usageKind.${u.kind}`)}</span>
            <span className="mono">{u.label || u.ref}</span>
            <span className="muted">
              {u.cpu} CPU · {u.mem_mb} {t('tokens.unit.mb')}
              {u.disk_gb ? ` · ${u.disk_gb} ${t('tokens.unit.gb')}` : ''}
            </span>
            <Button
              size="small"
              type="link"
              onClick={async () => {
                if (!(await confirmAction(t('tokens.releaseConfirm', { name: u.label || u.ref })))) return
                try {
                  await api(`/hub/tokens/${tokenID}/usage/release`, { method: 'POST', body: { kind: u.kind, host_id: u.host_id, ref: u.ref } })
                  void view.reload()
                } catch (err) {
                  setError(err instanceof Error ? err.message : String(err))
                }
              }}
            >
              {t('tokens.release')}
            </Button>
          </div>
        ))
      )}
    </div>
  )
}

/** Как подключить агента по MCP с этим токеном. */
export function McpExamples({ token, open }: { token: string; open?: boolean }) {
  const { t } = useTranslation()
  const origin = window.location.origin
  const examples = [
    { key: 'check', text: `curl -s -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \\\n  ${origin}/api/mcp \\\n  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'` },
    { key: 'call', text: `curl -s -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \\\n  ${origin}/api/mcp \\\n  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_hosts","arguments":{}}}'` },
    { key: 'claudeCode', text: `claude mcp add --transport http nkt ${origin}/api/mcp \\\n  --header "Authorization: Bearer ${token}"` },
    {
      key: 'desktop',
      text: JSON.stringify({ mcpServers: { nkt: { command: '/usr/local/bin/nkt', args: ['mcp'], env: { NKT_HUB_URL: origin, NKT_API_TOKEN: token } } } }, null, 2),
    },
  ]
  return (
    <Collapse
      size="small"
      defaultActiveKey={open ? ['mcp'] : []}
      items={[
        {
          key: 'mcp',
          label: t('tokens.mcp.title'),
          children: (
            <div className="col" style={{ gap: '0.5rem' }}>
              <span className="small muted">{t('tokens.mcp.hint')}</span>
              {examples.map((e) => (
                <div key={e.key} className="col" style={{ gap: '0.2rem' }}>
                  <span className="small">{t(`tokens.mcp.${e.key}`)}</span>
                  <Typography.Paragraph copyable={{ text: e.text }} className="mono small" style={{ margin: 0 }}>
                    <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0 }}>
                      {e.text}
                    </pre>
                  </Typography.Paragraph>
                </div>
              ))}
            </div>
          ),
        },
      ]}
    />
  )
}
