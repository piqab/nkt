import { useState } from 'react'
import { Button, Checkbox, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, ErrorNote, Modal } from './ui'

/**
 * Дополнения кластера (internal/hub/clusteraddons.go): поле «Longhorn» с
 * выбором версии чарта в формах создания и окно «Дополнения» у готового
 * кластера — поставить или обновить заданием.
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

/** Правило обновления Longhorn: вперёд и не дальше следующей минорной ветки. */
export function upgradeOK(cur: string, next: string) {
  if (!cur) return true
  if (cmp(next, cur) < 0) return false
  const c = parts(cur)
  const n = parts(next)
  return n[0] === c[0] && n[1] <= c[1] + 1
}

/** addons для тела создания кластера. */
export function addonsBody(lh: LonghornChoice) {
  return lh.on ? [{ name: 'longhorn', version: lh.version }] : []
}

function useLonghornVersions() {
  return useApi<AddonVersions>('/hub/k8s-addon-versions?addon=longhorn', 3_600_000)
}

function versionOptions(t: (k: string, o?: Record<string, unknown>) => string, data: AddonVersions | null | undefined, k8s: string, allowed: (v: string) => boolean) {
  const def = data?.default ?? ''
  const opts = [{ value: '', label: t('clusters.longhornDefault', { v: def || '…' }), disabled: !!def && !allowed(def) }]
  for (const v of data?.versions ?? []) {
    if (v.version === def) continue
    const fits = kubeOK(v.kube_version, k8s)
    const note = v.kube_version ? ` · Kubernetes ${v.kube_version}` : ''
    opts.push({ value: v.version, label: v.version + note + (fits ? '' : ` — ${t('clusters.longhornNoFit')}`), disabled: !fits || !allowed(v.version) })
  }
  return opts
}

/** Поле формы создания: галочка и версия. k8s — выбранная минорная версия (пусто — stable). */
export function LonghornField({ value, onChange, k8s }: { value: LonghornChoice; onChange: (v: LonghornChoice) => void; k8s: string }) {
  const { t } = useTranslation()
  const versions = useLonghornVersions()
  return (
    <>
      <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
        <Checkbox checked={value.on} onChange={(e) => onChange({ ...value, on: e.target.checked })} />
        <span>
          {t('clusters.longhorn')} <span className="small muted">{t('clusters.longhornHint')}</span>
        </span>
      </label>
      {value.on && (
        <div className="row" style={{ gap: '0.5rem', paddingLeft: '1.6rem', flexWrap: 'wrap', alignItems: 'center' }}>
          <Select
            value={value.version}
            onChange={(v: string) => onChange({ ...value, version: v })}
            options={versionOptions(t, versions.data, k8s, () => true)}
            style={{ minWidth: '18rem' }}
          />
          {versions.data?.error && <span className="small muted">{t('clusters.longhornIndexDown')}</span>}
        </div>
      )}
    </>
  )
}

interface AddonState {
  name: string
  installed: string
  spec?: string
  error?: string
}

/** Окно «Дополнения» готового кластера. */
export function ClusterAddonsModal({ cluster, onClose, onOpenJob }: { cluster: { id: number; name: string; status: string }; onClose: () => void; onOpenJob: (jobID: number) => void }) {
  const { t } = useTranslation()
  const state = useApi<{ addons: AddonState[]; nodes: number }>(`/hub/clusters/${cluster.id}/addons`)
  const versions = useLonghornVersions()
  const [version, setVersion] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const lh = state.data?.addons.find((a) => a.name === 'longhorn')
  const cur = lh?.installed ?? ''
  const effective = version || versions.data?.default || ''
  const allowed = (v: string) => upgradeOK(cur, v) && v !== cur

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>(`/hub/clusters/${cluster.id}/addons`, { method: 'POST', body: { name: 'longhorn', version: effective } })
      onClose()
      onOpenJob(res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('clusters.addonsTitle', { name: cluster.name })} onClose={onClose} width={640}>
      <p className="small muted">{t('clusters.addonsAbout')}</p>
      <ErrorNote error={state.error ?? error} />
      {lh?.error && <Banner kind="warn">{lh.error}</Banner>}
      <div className="col" style={{ gap: '0.5rem' }}>
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <strong>Longhorn</strong>
          {state.data && (cur ? <Tag color="success">{t('clusters.addonInstalled', { v: cur })}</Tag> : <Tag>{t('clusters.addonNotInstalled')}</Tag>)}
          {lh?.spec && lh.spec !== cur && <Tag color="warning">{t('clusters.addonSpec', { v: lh.spec })}</Tag>}
        </div>
        <span className="small muted">{cur ? t('clusters.addonUpgradeHint') : t('clusters.addonInstallHint', { n: Math.min(3, Math.max(1, state.data?.nodes ?? 1)) })}</span>
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <Select value={version} onChange={(v: string) => setVersion(v)} options={versionOptions(t, versions.data, '', allowed)} style={{ minWidth: '18rem' }} />
          <Button type="primary" loading={busy} disabled={!state.data || !effective || !allowed(effective) || cluster.status !== 'ready'} onClick={() => void start()}>
            {cur ? t('clusters.addonUpgrade') : t('clusters.addonInstall')}
          </Button>
        </div>
        {versions.data?.error && <span className="small muted">{t('clusters.longhornIndexDown')}</span>}
      </div>
    </Modal>
  )
}
