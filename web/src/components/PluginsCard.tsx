import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Segmented, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, Modal, formatRelative } from './ui'
import { unifiedDiff } from './textDiff'
import { confirmAction } from './confirm'
import { pluginToolName, type PluginInfo, type PluginManifest } from '../plugins'

/**
 * «О системе» → «Плагины»: подключённые внешние плагины и подключение
 * нового. Плагин выкладывается конвейером (контейнер слушает 127.0.0.1 на
 * своём хосте), хаб ходит к нему по SSH и подписывает вызовы секретом из
 * .env конвейера (NKT_PLUGIN_SECRET). До подключения хаб показывает, что
 * плагин добавит: разделы, команды сценариев, инструменты MCP и права,
 * которые он просит у хаба.
 */
export function PluginsCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const list = useApi<{ plugins: PluginInfo[] }>('/hub/plugins')
  const [connect, setConnect] = useState(false)
  const plugins = list.data?.plugins ?? []

  async function disconnect(p: PluginInfo) {
    if (!(await confirmAction(t('plugins.disconnectConfirm', { name: p.name, host: p.host })))) return
    try {
      await api(`/hub/plugins/${p.name}`, { method: 'DELETE' })
      window.location.reload()
    } catch (err) {
      await confirmAction(err instanceof Error ? err.message : String(err), { danger: false })
    }
  }

  return (
    <Card
      title={t('plugins.title')}
      subtitle={t('plugins.hint')}
      actions={
        admin && (
          <Button size="small" onClick={() => setConnect(true)}>
            {t('plugins.connect')}
          </Button>
        )
      }
    >
      {list.error && <Banner kind="error">{list.error}</Banner>}
      {plugins.length === 0 ? (
        <p className="small muted">{t('plugins.noneHint')}</p>
      ) : (
        <div className="col" style={{ gap: '0.5rem' }}>
          {plugins.map((p) => (
            <div key={p.name} className="row small" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              {p.error ? <Tag color="error">{t('plugins.down')}</Tag> : <Tag color="success">{t('plugins.up')}</Tag>}
              <strong>{p.title || p.name}</strong>
              <span className="muted">
                {p.name} {p.version} · {p.host}:{p.port}
                {p.pipeline ? ` · ${t('plugins.pipeline')}: ${p.pipeline}` : ''}
                {p.token ? ` · ${t('plugins.token')}: ${p.token}` : ''}
                {p.added_at ? ` · ${formatRelative(p.added_at)}` : ''}
              </span>
              {p.error && <span style={{ color: 'var(--status-critical)' }}>{p.error}</span>}
              {admin && (
                <Button size="small" danger onClick={() => void disconnect(p)}>
                  {t('plugins.disconnect')}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {plugins.length > 0 && <DNSBlock admin={admin} />}
      {connect && <ConnectModal onClose={() => setConnect(false)} />}
    </Card>
  )
}

interface DNSState {
  plugins: string[]
  autonames: { zone: string; enabled: boolean }
  history: { ts: string; author?: string; value: { zone: string; enabled: boolean } }[]
  owned: Record<string, { plugin: string; records: { name: string; type: string; values: string[]; failover?: boolean }[]; at?: string }>
}

/** DNS через плагин: что хаб поставил (маршруты Traffic Fly, сайты,
 * автоимена хостов), автоимена с историей и сверка с диффом. */
function DNSBlock({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const st = useApi<DNSState>('/hub/dns')
  const [edit, setEdit] = useState(false)
  const [sync, setSync] = useState<{ log: string[]; error?: string; dry: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  const d = st.data
  if (!d || d.plugins.length === 0) return null
  const owners = Object.entries(d.owned ?? {}).sort(([a], [b]) => a.localeCompare(b))

  async function runSync(dry: boolean) {
    setBusy(true)
    try {
      const r = await api<{ log: string[]; error?: string }>('/hub/dns/sync', { method: 'POST', body: { dry_run: dry }, timeoutMs: 120_000 })
      setSync({ ...r, dry })
      if (!dry) st.reload()
    } catch (err) {
      setSync({ log: [], error: err instanceof Error ? err.message : String(err), dry })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="col small" style={{ gap: '0.4rem', marginTop: '0.8rem', borderTop: '1px solid var(--border)', paddingTop: '0.6rem' }}>
      <strong>{t('plugins.dnsTitle', { list: d.plugins.join(', ') })}</strong>
      <span className="muted">{t('plugins.dnsHint')}</span>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <span>
          {t('plugins.autonames')}:{' '}
          {d.autonames.enabled ? <Tag color="success">{`<host>.${d.autonames.zone}`}</Tag> : <Tag>{t('about.expOff')}</Tag>}
        </span>
        {admin && (
          <>
            <Button size="small" onClick={() => setEdit(true)}>
              {t('common.edit')}
            </Button>
            <Button size="small" loading={busy} onClick={() => void runSync(true)}>
              {t('plugins.dnsCheck')}
            </Button>
          </>
        )}
      </div>
      {owners.length > 0 && (
        <div className="col" style={{ gap: '0.15rem' }}>
          {owners.map(([o, v]) => (
            <span key={o}>
              <code>{o}</code> → {v.records.map((r) => `${r.name} ${r.type} ${r.values.join(', ')}${r.failover ? ' (Failover)' : ''}`).join('; ')}
              <span className="muted"> · {v.plugin}</span>
            </span>
          ))}
        </div>
      )}
      {edit && <AutonamesModal state={d} onClose={() => setEdit(false)} onSaved={() => st.reload()} />}
      {sync && (
        <Modal title={t(sync.dry ? 'plugins.dnsCheck' : 'plugins.dnsApplied')} onClose={() => setSync(null)} width={760}>
          {sync.error && <Banner kind="error">{sync.error}</Banner>}
          {sync.log.length === 0 ? <p className="muted">{t('plugins.dnsNothing')}</p> : <pre className="small" style={{ whiteSpace: 'pre-wrap' }}>{sync.log.join('\n')}</pre>}
          {sync.dry && sync.log.length > 0 && (
            <div className="row" style={{ gap: '0.5rem', marginTop: '0.5rem' }}>
              <Button type="primary" loading={busy} onClick={() => void runSync(false)}>
                {t('plugins.dnsApply')}
              </Button>
              <Button onClick={() => setSync(null)}>{t('common.cancel')}</Button>
            </div>
          )}
        </Modal>
      )}
    </div>
  )
}

function autonamesText(v: { zone: string; enabled: boolean }, t: (k: string) => string): string {
  return `${t('plugins.autonames')}: ${v.enabled ? t('about.expOn') : t('about.expOff')}\n${t('plugins.autonamesZone')}: ${v.zone || '—'}\n`
}

function AutonamesModal({ state, onClose, onSaved }: { state: DNSState; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [v, setV] = useState(state.autonames)
  const [preview, setPreview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [log, setLog] = useState<string[] | null>(null)
  const diff = unifiedDiff(autonamesText(state.autonames, t), autonamesText(v, t), t('editModal.saved'), t('editModal.draft'))

  async function save() {
    setBusy(true)
    setError(null)
    try {
      const r = await api<{ log: string[]; error?: string }>('/hub/dns/autonames', { method: 'PUT', body: v, timeoutMs: 120_000 })
      setLog(r.log ?? [])
      if (r.error) setError(r.error)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
      setPreview(false)
    }
  }

  return (
    <Modal title={t('plugins.autonames')} onClose={onClose} width={720} maskClosable={false}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span className="muted">{t('plugins.autonamesHint')}</span>
        <Checkbox checked={v.enabled} onChange={(e) => setV({ ...v, enabled: e.target.checked })}>
          {t('plugins.autonamesOn')}
        </Checkbox>
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('plugins.autonamesZone')}
          <Input value={v.zone} onChange={(e) => setV({ ...v, zone: e.target.value.trim().toLowerCase() })} placeholder="hosts.example.com" />
        </label>
        {error && <Banner kind="error">{error}</Banner>}
        {log && (log.length === 0 ? <p className="muted">{t('plugins.dnsNothing')}</p> : <pre className="small" style={{ whiteSpace: 'pre-wrap' }}>{log.join('\n')}</pre>)}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" disabled={!diff || (v.enabled && !v.zone)} onClick={() => setPreview(true)}>
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.close')}</Button>
        </div>
        {state.history.length > 0 && (
          <div className="col" style={{ gap: '0.15rem' }}>
            <strong>{t('configs.versionHistoryTitle')}</strong>
            {[...state.history].reverse().map((h, i) => (
              <span key={i} className="muted">
                {formatRelative(h.ts)} · {h.author ?? '—'} · {h.value.enabled ? h.value.zone : t('about.expOff')}{' '}
                <Button size="small" type="link" onClick={() => setV(h.value)}>
                  {t('plugins.restore')}
                </Button>
              </span>
            ))}
          </div>
        )}
      </div>
      {preview && (
        <Modal title={t('blocks.previewTitle')} onClose={() => setPreview(false)} width={700} maskClosable={false}>
          <DiffView text={diff} />
          <div className="row" style={{ gap: '0.5rem', marginTop: '0.6rem' }}>
            <Button type="primary" loading={busy} onClick={() => void save()}>
              {t('virt.applyChanges')}
            </Button>
            <Button onClick={() => setPreview(false)}>{t('common.cancel')}</Button>
          </div>
        </Modal>
      )}
    </Modal>
  )
}

interface Probe {
  host: string
  port: number
  pipeline?: string
  manifest: PluginManifest
  conflicts?: string[]
}

function ConnectModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const pipelines = useApi<{ pipelines: { id: number; name: string; action?: string }[] }>('/hub/pipelines')
  const hosts = useApi<{ name: string }[]>('/hub/hosts')
  const [mode, setMode] = useState<'pipeline' | 'manual'>('pipeline')
  const [pipelineID, setPipelineID] = useState<number | undefined>()
  const [host, setHost] = useState<string | undefined>()
  const [port, setPort] = useState<number | null>(9180)
  const [secret, setSecret] = useState('')
  const [name, setName] = useState('')
  const [hubURL, setHubURL] = useState(window.location.origin)
  const [grant, setGrant] = useState(false)
  const [probe, setProbe] = useState<Probe | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const hostNames = (hosts.data ?? []).map((h) => h.name)
  const body = () =>
    mode === 'pipeline'
      ? { pipeline_id: pipelineID, port: port ?? undefined, name: name.trim() || undefined }
      : { host, port: port ?? undefined, secret, name: name.trim() || undefined }
  const ready = mode === 'pipeline' ? !!pipelineID : !!host && secret.length >= 16

  async function check() {
    setBusy(true)
    setError(null)
    setProbe(null)
    try {
      setProbe(await api<Probe>('/hub/plugins/probe', { method: 'POST', body: body() }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function connect() {
    setBusy(true)
    setError(null)
    try {
      await api('/hub/plugins', { method: 'POST', body: { ...body(), hub_url: hubURL, grant_hub: grant } })
      // Меню (раздел «Плагины») и слова сценариев — с новой страницы.
      window.location.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  const m = probe?.manifest
  const asks = m?.hub_actions ?? []
  return (
    <Modal title={t('plugins.connectTitle')} onClose={onClose} width={720} maskClosable={false}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <Segmented
          value={mode}
          onChange={(v) => {
            setMode(v as 'pipeline' | 'manual')
            setProbe(null)
          }}
          options={[
            { value: 'pipeline', label: t('plugins.fromPipeline') },
            { value: 'manual', label: t('plugins.manual') },
          ]}
        />
        {mode === 'pipeline' ? (
          <label className="small col" style={{ gap: '0.2rem' }}>
            {t('plugins.pipeline')}
            <Select
              showSearch
              value={pipelineID}
              onChange={(v) => {
                setPipelineID(v)
                setProbe(null)
              }}
              options={(pipelines.data?.pipelines ?? []).filter((p) => p.action === 'compose').map((p) => ({ value: p.id, label: p.name }))}
              placeholder={t('plugins.pipelinePick')}
            />
            <span className="muted">{t('plugins.pipelineHint')}</span>
          </label>
        ) : (
          <>
            <label className="small col" style={{ gap: '0.2rem' }}>
              {t('plugins.host')}
              <Select showSearch value={host} onChange={setHost} options={hostNames.map((h) => ({ value: h, label: h }))} />
            </label>
            <label className="small col" style={{ gap: '0.2rem' }}>
              {t('plugins.secret')}
              <Input.Password value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" />
            </label>
          </>
        )}
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <label className="small col" style={{ gap: '0.2rem' }}>
            {t('plugins.port')}
            <InputNumber min={1} max={65535} value={port} onChange={setPort} />
          </label>
          <label className="small col" style={{ gap: '0.2rem', flex: 1 }}>
            {t('plugins.name')}
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('plugins.namePlaceholder')} />
          </label>
        </div>
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button loading={busy && !probe} disabled={!ready} onClick={() => void check()}>
            {t('plugins.check')}
          </Button>
        </div>
        {error && <Banner kind="error">{error}</Banner>}
        {m && probe && (
          <div className="col small" style={{ gap: '0.35rem' }}>
            <div>
              <strong>{m.title}</strong> {m.version} · {probe.host}:{probe.port}
            </div>
            {m.description && <div className="muted">{m.description}</div>}
            {(m.pages?.length ?? 0) > 0 && (
              <div>
                {t('plugins.pages')}: {m.pages!.map((p) => p.title).join(', ')}
              </div>
            )}
            {(m.commands?.length ?? 0) > 0 && (
              <div>
                {t('plugins.commands')}: {m.commands!.map((c) => c.word).join(', ')}
              </div>
            )}
            {m.ops.some((o) => o.mcp) && (
              <div>
                {t('plugins.tools')}:{' '}
                {m.ops
                  .filter((o) => o.mcp)
                  .map((o) => pluginToolName(name.trim() || m.name, o.name))
                  .join(', ')}
              </div>
            )}
            {(m.provides?.length ?? 0) > 0 && (
              <div>
                {t('plugins.provides')}: {m.provides!.map((p) => t(`plugins.provide.${p}`)).join(', ')}
              </div>
            )}
            {(probe.conflicts?.length ?? 0) > 0 && <Banner kind="warn">{probe.conflicts!.join('; ')}</Banner>}
            {asks.length > 0 && (
              <Banner kind="warn">
                <div className="col" style={{ gap: '0.35rem' }}>
                  <span>{t('plugins.asks', { list: asks.map((a) => t(`tokens.act.${a}`, { defaultValue: a })).join(', ') })}</span>
                  <label className="col" style={{ gap: '0.2rem' }}>
                    {t('plugins.hubURL')}
                    <Input value={hubURL} onChange={(e) => setHubURL(e.target.value)} />
                  </label>
                  <Checkbox checked={grant} onChange={(e) => setGrant(e.target.checked)}>
                    {t('plugins.grant')}
                  </Checkbox>
                </div>
              </Banner>
            )}
            <div className="row" style={{ gap: '0.5rem', marginTop: '0.3rem' }}>
              <Button type="primary" loading={busy} disabled={asks.length > 0 && !grant} onClick={() => void connect()}>
                {t('plugins.connect')}
              </Button>
              <Button onClick={onClose}>{t('common.cancel')}</Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
