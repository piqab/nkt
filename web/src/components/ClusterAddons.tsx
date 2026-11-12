import { useState } from 'react'
import { Button, Checkbox, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, ErrorNote, Modal } from './ui'

/**
 * Дополнения кластера (internal/hub/clusteraddons.go, реестр —
 * internal/k8s/addons.go): список чекбоксов в форме создания и окно
 * «Дополнения» у готового кластера — поставить или обновить заданием.
 * Реестр (имена, версии по умолчанию, признаки) приходит с хаба.
 */

export interface AddonVersion {
  version: string
  kube_version?: string
}

interface AddonVersions {
  default: string
  versions: AddonVersion[]
  error?: string
}

export interface AddonInfo {
  name: string
  default: string
  storage?: boolean
  no_flavor?: string
  min_disk_gb?: number
  prepare?: boolean
}

/** Выбор формы создания: по дополнению — включено ли и версия (пусто — хаб подставит свою по умолчанию). */
export type AddonsChoice = Record<string, { on: boolean; version: string }>

/** Устаревшее поле пресета: только Longhorn (до реестра дополнений). */
export interface LonghornChoice {
  on: boolean
  version: string
}

const parts = (v: string) => v.replace(/^v/, '').split(/[.+-]/).map((x) => parseInt(x, 10) || 0)

function cmp(a: string, b: string) {
  const pa = parts(a)
  const pb = parts(b)
  for (let i = 0; i < 3; i++) if ((pa[i] ?? 0) !== (pb[i] ?? 0)) return (pa[i] ?? 0) - (pb[i] ?? 0)
  return 0
}

/** Подходит ли версия Kubernetes под «>=X.Y» чарта (остальное — подходит). */
export function kubeOK(constraint: string | undefined, k8s: string) {
  const m = /^>=\s*v?(\d+)\.(\d+)/.exec(constraint ?? '')
  if (!m || !k8s) return true
  const [maj, min] = parts(k8s)
  return maj > +m[1] || (maj === +m[1] && min >= +m[2])
}

/** Правило обновления: Longhorn — вперёд и не дальше следующей минорной
 * ветки, остальные — только вперёд (откат запрещён всем). */
export function upgradeOK(name: string, cur: string, next: string) {
  if (!cur) return true
  if (cmp(next, cur) < 0) return false
  if (name !== 'longhorn') return true
  const c = parts(cur)
  const n = parts(next)
  return n[0] === c[0] && n[1] <= c[1] + 1
}

/** addons для тела создания кластера. */
export function addonsBody(choice: AddonsChoice) {
  return Object.entries(choice)
    .filter(([, v]) => v.on)
    .map(([name, v]) => ({ name, version: v.version }))
}

export function useAddonRegistry() {
  return useApi<{ addons: AddonInfo[] }>('/hub/k8s-addons', 3_600_000)
}

function useAddonVersions(name: string | null) {
  return useApi<AddonVersions>(name ? `/hub/k8s-addon-versions?addon=${name}` : null, 3_600_000)
}

function versionOptions(t: (k: string, o?: Record<string, unknown>) => string, data: AddonVersions | null | undefined, k8s: string, allowed: (v: string) => boolean) {
  const def = data?.default ?? ''
  const opts = [{ value: '', label: t('clusters.addonVersionDefault', { v: def || '…' }), disabled: !!def && !allowed(def) }]
  for (const v of data?.versions ?? []) {
    if (v.version === def) continue
    const fits = kubeOK(v.kube_version, k8s)
    const note = v.kube_version ? ` · Kubernetes ${v.kube_version}` : ''
    opts.push({ value: v.version, label: v.version + note + (fits ? '' : ` — ${t('clusters.addonVersionNoFit')}`), disabled: !fits || !allowed(v.version) })
  }
  return opts
}

/** Отображаемое имя и подсказка дополнения — из словарей по имени реестра. */
function useAddonLabels(name: string) {
  const { t } = useTranslation()
  return { title: t(`clusters.addonName.${name}`, { defaultValue: name }), hint: t(`clusters.addonHint.${name}`, { defaultValue: '' }) }
}

/** Строка чекбокса в форме создания: Longhorn раскрывает выбор версии
 * (реплики и StorageClass по умолчанию — его), остальные ставятся своей
 * версией по умолчанию. */
function AddonCheckRow({ info, value, onChange, k8s, flavor }: { info: AddonInfo; value: { on: boolean; version: string }; onChange: (v: { on: boolean; version: string }) => void; k8s: string; flavor: string }) {
  const { t } = useTranslation()
  const { title, hint } = useAddonLabels(info.name)
  const conflict = !!info.no_flavor && info.no_flavor === flavor
  // Версии нужны только раскрытому Longhorn — остальные запросы не плодим.
  const versions = useAddonVersions(value.on && info.storage ? info.name : null)
  return (
    <>
      <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
        <Checkbox checked={value.on && !conflict} disabled={conflict} onChange={(e) => onChange({ ...value, on: e.target.checked })} />
        <span>
          {title}{' '}
          <span className="small muted">
            {conflict ? t('clusters.addonBuiltin') : hint}
          </span>
        </span>
      </label>
      {value.on && info.storage && (
        <div className="row" style={{ gap: '0.5rem', paddingLeft: '1.6rem', flexWrap: 'wrap', alignItems: 'center' }}>
          <Select
            value={value.version}
            onChange={(v: string) => onChange({ ...value, version: v })}
            options={versionOptions(t, versions.data, k8s, () => true)}
            style={{ minWidth: '18rem' }}
          />
          {versions.data?.error && <span className="small muted">{t('clusters.addonIndexDown')}</span>}
        </div>
      )}
    </>
  )
}

/** Поле формы создания: список дополнений. k8s — выбранная минорная версия
 * (пусто — stable), flavor — вариант кластера (k3s/kubeadm). */
export function AddonsField({ value, onChange, k8s, flavor }: { value: AddonsChoice; onChange: (v: AddonsChoice) => void; k8s: string; flavor: string }) {
  const registry = useAddonRegistry()
  const addons = registry.data?.addons ?? []
  const get = (name: string) => value[name] ?? { on: false, version: '' }
  return (
    <div className="col" style={{ gap: '0.35rem' }}>
      {addons.map((a) => (
        <AddonCheckRow
          key={a.name}
          info={a}
          k8s={k8s}
          flavor={flavor}
          value={get(a.name)}
          onChange={(v) => onChange({ ...value, [a.name]: v })}
        />
      ))}
    </div>
  )
}

interface AddonState {
  name: string
  installed: string
  spec?: string
  error?: string
}

/** Строка окна «Дополнения»: состояние, версия, кнопка. */
function AddonRow({ clusterID, info, state, nodes, flavor, clusterReady, onStarted }: { clusterID: number; info: AddonInfo; state?: AddonState; nodes: number; flavor: string; clusterReady: boolean; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const { title, hint } = useAddonLabels(info.name)
  const versions = useAddonVersions(info.name)
  const [version, setVersion] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const cur = state?.installed ?? ''
  const conflict = !!info.no_flavor && info.no_flavor === flavor
  const effective = version || versions.data?.default || info.default
  const allowed = (v: string) => upgradeOK(info.name, cur, v) && v !== cur

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>(`/hub/clusters/${clusterID}/addons`, { method: 'POST', body: { name: info.name, version: effective } })
      onStarted(res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="col" style={{ gap: '0.3rem' }}>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <strong>{title}</strong>
        {state && (cur ? <Tag color="success">{t('clusters.addonInstalled', { v: cur })}</Tag> : <Tag>{t('clusters.addonNotInstalled')}</Tag>)}
        {state?.spec && state.spec !== cur && <Tag color="warning">{t('clusters.addonSpec', { v: state.spec })}</Tag>}
        {conflict && <Tag>{t('clusters.addonBuiltin')}</Tag>}
      </div>
      {hint && <span className="small muted">{hint}</span>}
      {error && <Banner kind="error">{error}</Banner>}
      {state?.error && <Banner kind="warn">{state.error}</Banner>}
      {!conflict && (
        <>
          <span className="small muted">
            {cur ? t('clusters.addonUpgradeHint') : info.storage ? t('clusters.addonInstallHint', { n: Math.min(3, Math.max(1, nodes)) }) : ''}
          </span>
          <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <Select value={version} onChange={(v: string) => setVersion(v)} options={versionOptions(t, versions.data, '', allowed)} style={{ minWidth: '18rem' }} />
            <Button type="primary" loading={busy} disabled={!state || !effective || !allowed(effective) || !clusterReady} onClick={() => void start()}>
              {cur ? t('clusters.addonUpgrade') : t('clusters.addonInstall')}
            </Button>
          </div>
          {versions.data?.error && <span className="small muted">{t('clusters.addonIndexDown')}</span>}
        </>
      )}
    </div>
  )
}

/** Окно «Дополнения» готового кластера. */
export function ClusterAddonsModal({ cluster, onClose, onOpenJob }: { cluster: { id: number; name: string; status: string }; onClose: () => void; onOpenJob: (jobID: number) => void }) {
  const { t } = useTranslation()
  const state = useApi<{ addons: AddonState[]; nodes: number; flavor?: string }>(`/hub/clusters/${cluster.id}/addons`)
  const registry = useAddonRegistry()
  const flavor = state.data?.flavor ?? ''
  return (
    <Modal title={t('clusters.addonsTitle', { name: cluster.name })} onClose={onClose} width={720}>
      <p className="small muted">{t('clusters.addonsAbout')}</p>
      <ErrorNote error={state.error} />
      <div className="col" style={{ gap: '0.9rem' }}>
        {(registry.data?.addons ?? []).map((info) => (
          <AddonRow
            key={info.name}
            clusterID={cluster.id}
            info={info}
            state={state.data?.addons.find((a) => a.name === info.name)}
            nodes={state.data?.nodes ?? 1}
            flavor={flavor}
            clusterReady={cluster.status === 'ready'}
            onStarted={(id) => {
              onClose()
              onOpenJob(id)
            }}
          />
        ))}
      </div>
    </Modal>
  )
}
