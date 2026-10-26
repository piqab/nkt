import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Segmented, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, fetchHubJob, useApi } from '../api'
import { JobLogModal } from '../pages/Jobs'
import type { Job } from '../types'
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
  const [install, setInstall] = useState(false)
  const [settings, setSettings] = useState<PluginInfo | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const plugins = list.data?.plugins ?? []
  const outdated = plugins.filter((p) => p.outdated)

  async function updateAll() {
    try {
      const res = await api<{ job_id: number }>('/hub/plugins/native/update-all', { method: 'POST' })
      setJob(await fetchHubJob<Job>(res.job_id))
    } catch (err) {
      await confirmAction(err instanceof Error ? err.message : String(err), { danger: false })
    }
  }

  // Плагин, поставленный хабом службой: обновить файл или удалить с хоста.
  async function native(p: PluginInfo, action: 'update' | 'remove') {
    if (action === 'remove' && !(await confirmAction(t('plugins.nativeRemoveConfirm', { name: p.name, host: p.host })))) return
    try {
      const res = await api<{ job_id: number }>(`/hub/plugins/${p.name}/native/${action}`, { method: 'POST' })
      setJob(await fetchHubJob<Job>(res.job_id))
    } catch (err) {
      await confirmAction(err instanceof Error ? err.message : String(err), { danger: false })
    }
  }

  // «Настройки»: поставлен хабом — окно; выложен конвейером — его .env.
  async function openSettings(p: PluginInfo) {
    if (p.native) {
      setSettings(p)
      return
    }
    await confirmAction(t('plugins.settingsPipeline', { name: p.name, pipeline: p.pipeline || '—' }), { danger: false, title: t('plugins.settings') })
  }

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
          <>
            {outdated.length > 0 && (
              <Button size="small" type="primary" onClick={() => void updateAll()}>
                {t('plugins.updateAll', { n: outdated.length })}
              </Button>
            )}
            <Button size="small" onClick={() => setInstall(true)}>
              {t('plugins.install')}
            </Button>
            <Button size="small" onClick={() => setConnect(true)}>
              {t('plugins.connect')}
            </Button>
          </>
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
                {p.native ? ` · ${t('plugins.nativeTag')}` : ''}
                {p.outdated && (
                  <>
                    {' '}
                    <Tag color="processing">{t('plugins.outdated')}</Tag>
                  </>
                )}
                {p.needs_settings && (
                  <>
                    {' '}
                    <Tag color="warning">{t('plugins.needsSettings')}</Tag>
                  </>
                )}
                {p.added_at ? ` · ${formatRelative(p.added_at)}` : ''}
              </span>
              {p.error && <span style={{ color: 'var(--status-critical)' }}>{p.error}</span>}
              {admin && (p.native || p.pipeline) && !p.no_settings && (
                <Button size="small" onClick={() => void openSettings(p)}>
                  {t('plugins.settings')}
                </Button>
              )}
              {admin && p.native && (
                <>
                  <Button size="small" onClick={() => void native(p, 'update')}>
                    {t('plugins.nativeUpdate')}
                  </Button>
                  <Button size="small" danger onClick={() => void native(p, 'remove')}>
                    {t('plugins.nativeRemove')}
                  </Button>
                </>
              )}
              {admin && !p.native && (
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
      {install && (
        <InstallModal
          onClose={() => setInstall(false)}
          onJob={(j) => {
            setInstall(false)
            setJob(j)
          }}
        />
      )}
      {settings && (
        <SettingsModal
          plugin={settings}
          onClose={() => setSettings(null)}
          onJob={(j) => {
            setSettings(null)
            setJob(j)
          }}
        />
      )}
      {job && (
        <JobLogModal
          job={job}
          scope="/hosts/local"
          onClose={() => {
            setJob(null)
            // Меню (раздел «Плагины») и слова сценариев — с новой страницы.
            window.location.reload()
          }}
        />
      )}
    </Card>
  )
}

interface CatalogPackage {
  key: string
  name: string
  docker?: boolean
  title: string
  description: string
  run: string
  port: number
  settings: { env: string; title: string; hint?: string; default?: string; secret?: boolean; required?: boolean }[]
}

/** «Установить на хост (без Docker)»: плагин из встроенного каталога —
 * службой systemd на выбранный хост, заданием; подключается сам. */
function InstallModal({ onClose, onJob }: { onClose: () => void; onJob: (job: Job) => void }) {
  const { t } = useTranslation()
  const catalog = useApi<{ packages: CatalogPackage[]; local_ok: boolean }>('/hub/plugins/catalog')
  const installed = useApi<{ plugins: PluginInfo[] }>('/hub/plugins')
  const hosts = useApi<{ name: string }[]>('/hub/hosts')
  const [key, setKey] = useState<string | undefined>()
  const [host, setHost] = useState<string | undefined>()
  const [values, setValues] = useState<Record<string, string>>({})
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const pkgs = catalog.data?.packages ?? []
  const pkg = pkgs.find((p) => p.key === (key ?? pkgs[0]?.key))
  // Имя занято (второй экземпляр) — предложить следующее: dns2, dns3…
  const taken = new Set((installed.data?.plugins ?? []).map((p) => p.name))
  let suggested = pkg?.name ?? ''
  for (let i = 2; suggested && taken.has(suggested); i++) suggested = `${pkg?.name}${i}`
  const hostOptions = (hosts.data ?? []).filter((h) => catalog.data?.local_ok !== false || h.name !== 'localhost')
  const missing = (pkg?.settings ?? []).filter((f) => f.required && !(values[f.env] ?? f.default ?? '').trim())

  async function start() {
    if (!pkg || !host) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/plugins/install', {
        method: 'POST',
        body: { package: pkg.key, host, name: name.trim() || suggested, settings: values },
      })
      onJob(await fetchHubJob<Job>(res.job_id))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('plugins.installTitle')} onClose={onClose} width={720} maskClosable={false}>
      <div className="col small" style={{ gap: '0.6rem' }}>
        <span className="muted">{t('plugins.installHint')}</span>
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('plugins.package')}
          <Select value={pkg?.key} onChange={setKey} options={pkgs.map((p) => ({ value: p.key, label: p.title }))} />
        </label>
        {pkg && <span className="muted">{pkg.description}</span>}
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('plugins.host')}
          <Select showSearch value={host} onChange={setHost} options={hostOptions.map((h) => ({ value: h.name, label: h.name }))} />
          {catalog.data?.local_ok === false && <span className="muted">{t('plugins.localInDocker')}</span>}
        </label>
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('plugins.name')}
          <Input value={name} placeholder={suggested} onChange={(e) => setName(e.target.value.trim().toLowerCase())} />
          <span className="muted">{t('plugins.instanceHint')}</span>
        </label>
        {pkg?.docker && <Banner kind="warn">{t('plugins.dockerWarn')}</Banner>}
        {(pkg?.settings ?? []).map((f) => (
          <label key={f.env} className="col" style={{ gap: '0.2rem' }}>
            <span>
              {f.title}
              {f.required && ' *'} <code className="muted">{f.env}</code>
            </span>
            {f.secret ? (
              <Input.Password autoComplete="off" value={values[f.env] ?? ''} onChange={(e) => setValues({ ...values, [f.env]: e.target.value })} />
            ) : (
              <Input value={values[f.env] ?? ''} placeholder={f.default} onChange={(e) => setValues({ ...values, [f.env]: e.target.value })} />
            )}
            {f.hint && <span className="muted">{f.hint}</span>}
          </label>
        ))}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
          <Button type="primary" loading={busy} disabled={!pkg || !host || missing.length > 0} onClick={() => void start()}>
            {t('plugins.install')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          {missing.length > 0 && <span className="muted">{t('plugins.fieldsMissing', { list: missing.map((f) => f.title).join(', ') })}</span>}
        </div>
      </div>
    </Modal>
  )
}

interface SettingsField {
  env: string
  title: string
  hint?: string
  default?: string
  secret?: boolean
  required?: boolean
  value: string
  set?: boolean
}

interface SettingsVersion {
  at: string
  user: string
  values: Record<string, string>
  changed?: string[]
}

interface SettingsChange {
  env: string
  title: string
  old?: string
  new?: string
  secret?: boolean
}

/** «Настройки» плагина, поставленного на хост: обычные значения — с хоста,
 * секреты — только «задан» и «заменить»; «Сверить» — дифф, «Сохранить» —
 * заданием (плагин не ответил — прежние настройки возвращаются); история
 * обычных значений с возвратом версии. */
function SettingsModal({ plugin, onClose, onJob }: { plugin: PluginInfo; onClose: () => void; onJob: (job: Job) => void }) {
  const { t } = useTranslation()
  const st = useApi<{ fields: SettingsField[]; history: SettingsVersion[]; host: string }>(`/hub/plugins/${plugin.name}/settings`)
  const [values, setValues] = useState<Record<string, string> | null>(null)
  const [check, setCheck] = useState<SettingsChange[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const fields = st.data?.fields ?? []
  const current: Record<string, string> = Object.fromEntries(fields.filter((f) => !f.secret).map((f) => [f.env, f.value]))
  const v = values ?? current
  const set = (k: string, val: string) => {
    setValues({ ...v, [k]: val })
    setCheck(null)
  }
  const missing = fields.filter((f) => f.required && !(f.secret ? f.set || (v[f.env] ?? '').trim() : (v[f.env] || f.default || '').trim()))
  const text = (m: Record<string, string>) =>
    fields
      .filter((f) => !f.secret)
      .map((f) => `${f.env}=${m[f.env] ?? ''}`)
      .join('\n')

  async function run(dry: boolean) {
    setBusy(true)
    setError(null)
    try {
      if (dry) {
        const r = await api<{ changes: SettingsChange[] }>(`/hub/plugins/${plugin.name}/settings`, { method: 'POST', body: { settings: v, dry_run: true } })
        setCheck(r.changes)
      } else {
        const r = await api<{ job_id: number }>(`/hub/plugins/${plugin.name}/settings`, { method: 'POST', body: { settings: v } })
        onJob(await fetchHubJob<Job>(r.job_id))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const after: Record<string, string> = { ...current }
  for (const c of check ?? []) if (!c.secret) after[c.env] = c.new ?? ''
  const diff = check ? unifiedDiff(text(current), text(after), t('plugins.settingsOnHost'), t('editModal.draft')) : ''
  const secrets = (check ?? []).filter((c) => c.secret)

  // Полей нет (чужой плагин без настроек) — сверять нечего: объяснение
  // вместо пустой формы.
  if (st.data && fields.length === 0) {
    return (
      <Modal title={t('plugins.settingsTitle', { name: plugin.name, host: plugin.host })} onClose={onClose} width={560}>
        <div className="col small" style={{ gap: '0.6rem' }}>
          <span>{t('plugins.settingsNone')}</span>
          {plugin.manifest?.provides?.includes('hosts') && <span>{t('plugins.settingsNoneHosts')}</span>}
          <div className="row">
            <Button onClick={onClose}>{t('common.close')}</Button>
          </div>
        </div>
      </Modal>
    )
  }

  return (
    <Modal title={t('plugins.settingsTitle', { name: plugin.name, host: plugin.host })} onClose={onClose} width={760} maskClosable={false}>
      <div className="col small" style={{ gap: '0.6rem' }}>
        <span className="muted">{t('plugins.settingsHint')}</span>
        {st.error && <Banner kind="error">{st.error}</Banner>}
        {(st.data?.history.length ?? 0) > 0 && (
          <label className="col" style={{ gap: '0.2rem' }}>
            {t('plugins.settingsHistory')}
            <Select
              placeholder={t('plugins.settingsVersionPick')}
              value={null}
              onChange={(i: number) => {
                const h = st.data!.history[i]
                setValues({ ...v, ...Object.fromEntries(fields.filter((f) => !f.secret).map((f) => [f.env, h.values[f.env] ?? ''])) })
                setCheck(null)
              }}
              options={st.data!.history.map((h, i) => ({
                value: i,
                label: `${formatRelative(h.at)} · ${h.user || '—'}${h.changed?.length ? ` · ${h.changed.join(', ')}` : ''}`,
              }))}
            />
          </label>
        )}
        {fields.map((f) => (
          <label key={f.env} className="col" style={{ gap: '0.2rem' }}>
            <span>
              {f.title}
              {f.required && ' *'} <code className="muted">{f.env}</code>
              {f.secret && (
                <>
                  {' '}
                  {f.set ? <Tag color="success">{t('plugins.settingsSecretSet')}</Tag> : <Tag color="warning">{t('plugins.settingsSecretUnset')}</Tag>}
                </>
              )}
            </span>
            {f.secret ? (
              <Input.Password
                autoComplete="new-password"
                value={v[f.env] ?? ''}
                placeholder={f.set ? t('plugins.settingsReplace') : ''}
                onChange={(e) => set(f.env, e.target.value)}
              />
            ) : (
              <Input value={v[f.env] ?? ''} placeholder={f.default} onChange={(e) => set(f.env, e.target.value)} />
            )}
            {f.hint && <span className="muted">{f.hint}</span>}
          </label>
        ))}
        {check && check.length === 0 && <span className="muted">{t('plugins.settingsNoChanges')}</span>}
        {diff && <DiffView text={diff} />}
        {secrets.length > 0 && <span>{t('plugins.settingsSecretReplaced', { list: secrets.map((c) => c.title).join(', ') })}</span>}
        {check && check.length > 0 && <Banner kind="info">{t('plugins.settingsRestartHint')}</Banner>}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
          <Button loading={busy && !check} disabled={!st.data || missing.length > 0} onClick={() => void run(true)}>
            {t('plugins.settingsCheck')}
          </Button>
          <Button type="primary" loading={busy && !!check} disabled={!check || check.length === 0} onClick={() => void run(false)}>
            {t('plugins.settingsSave')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
          {missing.length > 0 && <span className="muted">{t('plugins.fieldsMissing', { list: missing.map((f) => f.title).join(', ') })}</span>}
        </div>
      </div>
    </Modal>
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
  conflicts?: string[] | null
  warnings?: string[] | null
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
            {(probe.conflicts?.length ?? 0) > 0 && <Banner kind="error">{probe.conflicts!.join('; ')}</Banner>}
            {(probe.warnings?.length ?? 0) > 0 && <Banner kind="warn">{probe.warnings!.join('; ')}</Banner>}
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
