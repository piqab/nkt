import { useEffect, useState } from 'react'
import { OsIconOf } from './OsIcon'
import { Button, Checkbox, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Fail2banTemplate, Job } from '../types'
import { JobLogModal } from '../pages/Jobs'
import { Banner, DiffView, Loading, Modal } from './ui'
import { unifiedDiff } from './textDiff'
import { msg, tx, type Msg } from '../msg'

interface HostState {
  id: number
  name: string
  known: boolean
  installed: boolean
  running: boolean
}

interface CheckFile {
  path: string
  before: string
  after: string
  exists: boolean
}

interface CheckHost {
  id: number
  name: string
  status: 'changes' | 'same' | 'skip' | 'error'
  reason?: string
  files?: CheckFile[]
  hub?: 'protected' | 'will_add'
  hub_addr?: string
}

const STATUS_COLOR: Record<CheckHost['status'], string> = { changes: 'blue', same: 'green', skip: 'default', error: 'red' }

/**
 * Шаблон fail2ban на выбранные хосты хаба. Галочки пустые; «Проверить»
 * обязательна — пробный прогон на каждом хосте с диффом файлов; затем
 * задание хаба применяет ровно проверенное. Смена выбора сбрасывает
 * проверку.
 */
export function F2BTemplateFleetModal({ template, onClose }: { template: Fail2banTemplate; onClose: () => void }) {
  const { t } = useTranslation()
  const data = useApi<{ hosts: HostState[] }>('/hub/fail2ban/banned')
  const [picked, setPicked] = useState<number[]>([])
  const [check, setCheck] = useState<{ hosts: CheckHost[]; token?: string } | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Msg | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  // Проверка — задание хаба в очереди fail2ban: окно ждёт его итог.
  const [checkJob, setCheckJob] = useState<number | null>(null)
  const [checkLog, setCheckLog] = useState<Job | null>(null)

  useEffect(() => {
    if (checkJob === null) return
    let stop = false
    const tick = async () => {
      try {
        const res = await api<{ done: boolean; status?: string; hosts?: CheckHost[]; token?: string }>(`/hub/fail2ban/templates/check/${checkJob}`)
        if (stop) return
        if (!res.done) {
          window.setTimeout(() => void tick(), 1500)
          return
        }
        setCheckJob(null)
        if (res.hosts) setCheck({ hosts: res.hosts, token: res.token })
        else setError(tx('fail2ban.fleetTplCheckFailed'))
      } catch (err) {
        if (!stop) {
          setCheckJob(null)
          setError(err instanceof Error ? err.message : String(err))
        }
      }
    }
    void tick()
    return () => {
      stop = true
    }
  }, [checkJob, t])

  if (job) return <JobLogModal job={job} scope="/hosts/local" onClose={onClose} />

  const hosts = data.data?.hosts ?? []
  // Без fail2ban на хосте шаблону некуда ставиться.
  const usable = (h: HostState) => !(h.known && !h.installed)
  const selectable = hosts.filter(usable)
  const pick = (ids: number[]) => {
    if (checkJob !== null) return
    setPicked(ids)
    setCheck(null)
    setError(null)
  }

  async function runCheck() {
    setBusy(true)
    setError(null)
    setCheck(null)
    try {
      const res = await api<{ job_id: number }>('/hub/fail2ban/templates/check', {
        method: 'POST',
        body: { name: template.name, builtin: !!template.builtin, host_ids: picked },
      })
      setCheckJob(res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function apply() {
    if (!check?.token) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/hub/fail2ban/templates/apply', { method: 'POST', body: { token: check.token } })
      setJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setCheck(null)
    } finally {
      setBusy(false)
    }
  }

  const changes = check?.hosts.filter((h) => h.status === 'changes').length ?? 0

  return (
    <Modal title={t('fail2ban.fleetTplTitle', { name: template.name })} onClose={onClose} width={860} maskClosable={false}>
      <p className="small muted">{t(template.builtin ? 'fail2ban.fleetTplHintBuiltin' : 'fail2ban.fleetTplHintCustom')}</p>
      {data.error && <Banner kind="error">{data.error}</Banner>}
      {!data.data ? (
        <Loading />
      ) : (
        <>
          <Space wrap style={{ marginBottom: '0.4rem' }}>
            <Button size="small" onClick={() => pick(selectable.map((h) => h.id))}>
              {t('purgeAll.selectAll')}
            </Button>
            <Button size="small" onClick={() => pick([])}>
              {t('purgeAll.selectNone')}
            </Button>
            <span className="small muted">{t('purgeAll.selected', { count: picked.length, total: selectable.length })}</span>
          </Space>
          <div className="purge-all-list col" style={{ gap: '0.2rem' }}>
            {hosts.map((h) => (
              <Checkbox
                key={h.id}
                disabled={!usable(h)}
                checked={picked.includes(h.id)}
                onChange={(e) => pick(e.target.checked ? [...picked, h.id] : picked.filter((x) => x !== h.id))}
              >
                <strong>
                  <OsIconOf kind="host" hostID={h.id} name={h.name} />
                  {h.name}
                </strong>{' '}
                <span className="small muted">
                  {!h.known
                    ? t('fail2ban.unknownShort')
                    : !h.installed
                      ? t('fail2ban.fleetTplNotInstalled')
                      : h.running
                        ? t('fail2ban.running')
                        : t('fail2ban.stopped')}
                </span>
              </Checkbox>
            ))}
          </div>
        </>
      )}

      {check && (
        <div style={{ marginTop: '0.8rem' }}>
          <div className="small" style={{ marginBottom: '0.3rem' }}>
            <strong>{t('fail2ban.fleetTplCheckTitle')}</strong>
          </div>
          <div className="col" style={{ gap: '0.3rem' }}>
            {check.hosts.map((h) => (
              <div key={h.id}>
                <Space wrap size={4}>
                  <strong>
                    <OsIconOf kind="host" hostID={h.id} name={h.name} />
                    {h.name || `#${h.id}`}
                  </strong>
                  <Tag color={STATUS_COLOR[h.status]}>{t(`fail2ban.fleetTplStatus.${h.status}`)}</Tag>
                  {h.hub && (
                    <Tag color={h.hub === 'protected' ? 'green' : 'orange'}>
                      {t(h.hub === 'protected' ? 'fail2ban.fleetTplHubProtected' : 'fail2ban.fleetTplHubWillAdd', { addr: h.hub_addr })}
                    </Tag>
                  )}
                  {h.reason && <span className="small muted">{h.reason}</span>}
                  {(h.files ?? []).length > 0 && (
                    <Button size="small" type="link" onClick={() => setOpen(open === h.id ? null : h.id)}>
                      {open === h.id ? t('fail2ban.fleetTplHideDiff') : t('fail2ban.fleetTplShowDiff')}
                    </Button>
                  )}
                </Space>
                {open === h.id &&
                  (h.files ?? []).map((f) => (
                    <div key={f.path} style={{ margin: '0.3rem 0 0.5rem' }}>
                      <div className="small mono">
                        {f.path} {!f.exists && <Tag color="green">{t('fail2ban.newFile')}</Tag>}
                      </div>
                      {f.before === f.after ? (
                        <p className="small muted">{t('editModal.noChanges')}</p>
                      ) : (
                        <DiffView text={unifiedDiff(f.before, f.after, t('editModal.saved'), t('editModal.draft'))} />
                      )}
                    </div>
                  ))}
              </div>
            ))}
          </div>
          {!check.token && <p className="small muted">{t('fail2ban.fleetTplNothing')}</p>}
        </div>
      )}

      {checkJob !== null && (
        <Banner kind="info">
          <Space wrap>
            {t('fail2ban.fleetTplChecking', { id: checkJob })}
            <Button size="small" onClick={async () => setCheckLog(await api<Job>(`/hosts/local/jobs/${checkJob}`))}>
              {t('fail2ban.fleetTplCheckLog')}
            </Button>
          </Space>
        </Banner>
      )}
      {checkLog && <JobLogModal job={checkLog} scope="/hosts/local" onClose={() => setCheckLog(null)} />}
      {error && <Banner kind="error">{msg(error)}</Banner>}
      <Space wrap style={{ marginTop: '0.8rem' }}>
        <Button
          type={check ? 'default' : 'primary'}
          loading={(busy && !check) || checkJob !== null}
          disabled={picked.length === 0 || busy || checkJob !== null}
          onClick={() => void runCheck()}
        >
          {t('fail2ban.fleetTplCheck')}
        </Button>
        <Button type="primary" loading={busy && !!check} disabled={!check?.token || busy || checkJob !== null} onClick={() => void apply()}>
          {t('fail2ban.fleetTplApply', { count: changes })}
        </Button>
      </Space>
      {!check && <div className="small muted" style={{ marginTop: '0.3rem' }}>{t('fail2ban.fleetTplCheckFirst')}</div>}
    </Modal>
  )
}
