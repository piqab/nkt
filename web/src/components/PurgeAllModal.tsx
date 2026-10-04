import { useState } from 'react'
import { Button, Checkbox, Input, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, LOCAL_HOST_ID, useApi } from '../api'
import type { HubHost, Job } from '../types'
import { Banner, Card, Loading, Modal } from './ui'
import { ExportPasswordModal, downloadHubExport } from './HubExport'
import { JobLogModal } from '../pages/Jobs'
import { Sensitive } from '../privacy'

interface PurgeOpts {
  service: boolean
  data: boolean
  access: boolean
  user: boolean
  restore_password: boolean
}

const DEFAULT_OPTS: PurgeOpts = { service: true, data: true, access: true, restore_password: true, user: false }

/** «Опасная зона» в «О системе» хаба: удалить nkt со всех (выбранных)
 * хостов. Сначала — обязательный полный экспорт, затем выбор хостов
 * (галочки сняты), затем подтверждение словом и задание хаба. */
export function DangerZoneCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const [stage, setStage] = useState<'idle' | 'export' | 'select' | 'job'>('idle')
  const [exportBusy, setExportBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  if (!admin) return null
  return (
    <Card title={t('purgeAll.title')} subtitle={t('purgeAll.hint')} className="danger-zone">
      {error && <Banner kind="error">{error}</Banner>}
      <Button danger type="primary" onClick={() => setStage('export')}>
        {t('purgeAll.button')}
      </Button>
      {stage === 'export' && (
        <ExportPasswordModal
          busy={exportBusy}
          onDownload={async (password, users, monitoring) => {
            setExportBusy(true)
            setError(null)
            try {
              await downloadHubExport(true, password, users, monitoring)
              setStage('select')
            } catch (err) {
              setError(err instanceof Error ? err.message : String(err))
              setStage('idle')
            } finally {
              setExportBusy(false)
            }
          }}
          onClose={() => setStage('idle')}
        />
      )}
      {stage === 'select' && (
        <PurgeAllModal
          onClose={() => setStage('idle')}
          onStarted={(j) => {
            setJob(j)
            setStage('job')
          }}
        />
      )}
      {stage === 'job' && job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setStage('idle')} />}
    </Card>
  )
}

function PurgeAllModal({ onClose, onStarted }: { onClose: () => void; onStarted: (job: Job) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [picked, setPicked] = useState<number[]>([])
  const [opts, setOpts] = useState<PurgeOpts>(DEFAULT_OPTS)
  const [word, setWord] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // Машина хаба (localhost) — не здесь: хаб так nkt с себя не удаляет.
  const list = (hosts.data ?? []).filter((h) => h.id !== LOCAL_HOST_ID)
  const confirmWord = t('purgeAll.confirmWord')
  const ready = picked.length > 0 && word.trim().toLowerCase() === confirmWord.toLowerCase()

  const warn = (h: HubHost) =>
    h.sudo_status === 'password_required'
      ? t('purgeAll.warnSudo')
      : h.reachable === false
        ? t('purgeAll.warnUnreachable')
        : h.status !== 'online'
          ? t('purgeAll.warnNotInstalled')
          : ''

  const opt = (key: keyof PurgeOpts, label: string) => (
    <Checkbox checked={opts[key]} onChange={(e) => setOpts({ ...opts, [key]: e.target.checked })}>
      {label}
    </Checkbox>
  )

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/purge-all', { method: 'POST', body: { host_ids: picked, purge: opts } })
      onStarted(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('purgeAll.selectTitle')} onClose={onClose} width={720}>
      <Banner kind="warn">{t('purgeAll.selectHint')}</Banner>
      {!hosts.data ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem', marginTop: '0.5rem' }}>
          <Space wrap>
            <Button size="small" onClick={() => setPicked(list.map((h) => h.id))}>
              {t('purgeAll.selectAll')}
            </Button>
            <Button size="small" onClick={() => setPicked([])}>
              {t('purgeAll.selectNone')}
            </Button>
            <span className="small muted">{t('purgeAll.selected', { count: picked.length, total: list.length })}</span>
          </Space>
          <div className="col purge-all-list" style={{ gap: '0.2rem' }}>
            {list.length === 0 && <span className="small muted">{t('purgeAll.noHosts')}</span>}
            {list.map((h) => (
              <label key={h.id} className="row" style={{ gap: '0.5rem', alignItems: 'center', paddingLeft: h.parent_id ? '1.4rem' : 0 }}>
                <Checkbox
                  checked={picked.includes(h.id)}
                  onChange={(e) => setPicked(e.target.checked ? [...picked, h.id] : picked.filter((x) => x !== h.id))}
                />
                <strong>
                  <Sensitive>{h.name}</Sensitive>
                </strong>
                {h.group && <Tag>{h.group}</Tag>}
                {warn(h) && <span className="small" style={{ color: 'var(--status-warning)' }}>{warn(h)}</span>}
              </label>
            ))}
          </div>
          <div className="col" style={{ gap: '0.15rem' }}>
            <strong className="small">{t('purgeAll.whatTitle')}</strong>
            {opt('service', t('hosts.purgeService'))}
            {opt('data', t('hosts.purgeData'))}
            {opt('access', t('hosts.purgeAccess'))}
            {opt('restore_password', t('hosts.purgeRestorePassword'))}
            {opt('user', t('purgeAll.optUser'))}
            <span className="small muted">{t('purgeAll.removedFromHub')}</span>
          </div>
          <label className="small">
            {t('purgeAll.typeWord', { word: confirmWord })}
            <Input value={word} onChange={(e) => setWord(e.target.value)} style={{ maxWidth: 220 }} />
          </label>
          {error && <Banner kind="error">{error}</Banner>}
          <Space>
            <Button danger type="primary" disabled={!ready} loading={busy} onClick={() => void start()}>
              {t('purgeAll.start', { count: picked.length })}
            </Button>
            <Button onClick={onClose}>{t('common.cancel')}</Button>
          </Space>
        </div>
      )}
    </Modal>
  )
}
