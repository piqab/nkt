import { useState } from 'react'
import { Button, Checkbox, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, LOCAL_HOST_ID, useApi } from '../api'
import type { HubHost, Job } from '../types'
import { Banner, Card, Loading, Modal } from './ui'
import { JobLogModal } from '../pages/Jobs'
import { Sensitive } from '../privacy'

/** «Установить nkt на хосты» в «О системе» хаба — рядом с «Опасной
 * зоной»: обычная установка на выбранных хостах заданием хаба. */
export function InstallAllCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (!admin) return null
  return (
    <Card title={t('installAll.title')} subtitle={t('installAll.hint')}>
      <Button type="primary" onClick={() => setOpen(true)}>
        {t('installAll.button')}
      </Button>
      {open && <InstallAllModal onClose={() => setOpen(false)} />}
    </Card>
  )
}

/** Выбор хостов и запуск. preselect — отмечены заранее (плашка «N хостов
 * без nkt» в списке хостов); иначе галочки сняты. */
export function InstallAllModal({ preselect, onClose }: { preselect?: number[]; onClose: () => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [picked, setPicked] = useState<number[]>(preselect ?? [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  // Машина хаба — не здесь: nkt на ней — сам хаб.
  const list = (hosts.data ?? []).filter((h) => h.id !== LOCAL_HOST_ID)

  if (job) return <JobLogModal job={job} scope="/hosts/local" onClose={onClose} />

  const state = (h: HubHost) =>
    h.status === 'new'
      ? { text: t('installAll.stateNew'), color: 'default' }
      : h.status === 'installing'
        ? { text: t('installAll.stateInstalling'), color: 'processing' }
        : h.status === 'error'
          ? { text: t('installAll.stateError'), color: 'red' }
          : h.reachable === false
            ? { text: t('installAll.stateUnreachable'), color: 'orange' }
            : { text: t('installAll.stateInstalled', { version: h.nkt_version || '?' }), color: 'green' }

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/install-all', { method: 'POST', body: { host_ids: picked } })
      setJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('installAll.selectTitle')} onClose={onClose} width={720}>
      <p className="small muted">{t('installAll.selectHint')}</p>
      {!hosts.data ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
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
            {list.map((h) => {
              const st = state(h)
              return (
                <label key={h.id} className="row" style={{ gap: '0.5rem', alignItems: 'center', paddingLeft: h.parent_id ? '1.4rem' : 0 }}>
                  <Checkbox
                    checked={picked.includes(h.id)}
                    disabled={h.status === 'installing'}
                    onChange={(e) => setPicked(e.target.checked ? [...picked, h.id] : picked.filter((x) => x !== h.id))}
                  />
                  <strong>
                    <Sensitive>{h.name}</Sensitive>
                  </strong>
                  {h.group && <Tag>{h.group}</Tag>}
                  <Tag color={st.color}>{st.text}</Tag>
                </label>
              )
            })}
          </div>
          {error && <Banner kind="error">{error}</Banner>}
          <div>
            <Button type="primary" loading={busy} disabled={picked.length === 0} onClick={() => void start()}>
              {t('installAll.start', { count: picked.length })}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
