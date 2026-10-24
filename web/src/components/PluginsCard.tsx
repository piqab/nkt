import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Segmented, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Modal, formatRelative } from './ui'
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
      {connect && <ConnectModal onClose={() => setConnect(false)} />}
    </Card>
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
