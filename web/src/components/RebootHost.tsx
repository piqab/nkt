import { useState } from 'react'
import { Button, Checkbox, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, Loading, Modal } from './ui'

interface Preview {
  reboot_required: boolean
  running: Record<'docker' | 'podman' | 'lxd' | 'vm' | 'service', number>
  no_autostart: { kind: string; name: string; reason?: string }[]
  simulated?: boolean
}

const KINDS = ['docker', 'podman', 'lxd', 'vm', 'service'] as const

/** Кнопка «Перезагрузить хост» с окном: что работает сейчас (количеством),
 * что само не поднимется (списком) и обязательная галочка. */
export function RebootHostButton({ me, size, primary }: { me: Me; size?: 'small' | 'middle'; primary?: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (!me.is_admin || !me.allow_mutations) return null
  return (
    <>
      <Button danger type={primary ? 'primary' : 'default'} size={size} onClick={() => setOpen(true)}>
        {t('reboot.button')}
      </Button>
      {open && <RebootModal onClose={() => setOpen(false)} />}
    </>
  )
}

function RebootModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const pv = useApi<Preview>('/system/reboot/preview')
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<{ simulated?: boolean; delay_s?: number } | null>(null)
  const d = pv.data

  async function reboot() {
    setBusy(true)
    setError(null)
    try {
      setDone(await api<{ simulated?: boolean; delay_s?: number }>('/system/reboot', { method: 'POST', body: { confirm: true } }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('reboot.title')} onClose={onClose} width={680} maskClosable={false}>
      {done ? (
        <Banner kind="info">{done.simulated ? t('reboot.simulated') : t('reboot.started', { s: done.delay_s ?? 5 })}</Banner>
      ) : !d ? (
        pv.error ? <Banner kind="error">{pv.error}</Banner> : <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          {d.reboot_required && <Banner kind="warn">{t('reboot.required')}</Banner>}
          <div>
            <strong>{t('reboot.runningTitle')}</strong>
            <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', marginTop: '0.3rem' }}>
              {KINDS.map((k) => (
                <Tag key={k}>{t(`reboot.kind.${k}`)}: {d.running[k] ?? 0}</Tag>
              ))}
            </div>
          </div>
          <div>
            <strong>{t('reboot.noAutoTitle')}</strong>
            {d.no_autostart.length === 0 ? (
              <div className="small muted">{t('reboot.noAutoNone')}</div>
            ) : (
              <>
                <div className="small muted">{t('reboot.noAutoHint')}</div>
                <div className="col purge-all-list" style={{ gap: '0.15rem', marginTop: '0.3rem' }}>
                  {d.no_autostart.map((n) => (
                    <div key={`${n.kind}:${n.name}`} className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
                      <Tag>{t(`reboot.kind.${n.kind}`)}</Tag>
                      <span className="mono small">{n.name}</span>
                      {n.reason && <span className="small muted">{n.reason}</span>}
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>
          <Checkbox checked={confirm} onChange={(e) => setConfirm(e.target.checked)}>
            {d.no_autostart.length > 0 ? t('reboot.confirmWithList') : t('reboot.confirm')}
          </Checkbox>
          {error && <Banner kind="error">{error}</Banner>}
          <div>
            <Button danger type="primary" loading={busy} disabled={!confirm} onClick={() => void reboot()}>
              {t('reboot.go')}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}

/** Карточка «Перезагрузка хоста» в «Системных настройках» — всегда;
 * выделена, когда перезагрузка требуется. */
export function RebootCard({ me, required }: { me: Me; required: boolean }) {
  const { t } = useTranslation()
  if (!me.is_admin || !me.allow_mutations) return null
  return (
    <Card title={t('reboot.cardTitle')} subtitle={t('reboot.cardHint')}>
      {required && <Banner kind="warn">{t('reboot.required')}</Banner>}
      <RebootHostButton me={me} primary={required} />
    </Card>
  )
}
