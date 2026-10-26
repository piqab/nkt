import { useState } from 'react'
import { Button, InputNumber } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, DiffView, Loading, Modal } from './ui'

/**
 * Пул портов хоста (1.17.5): из него владельцы машин — администратор или
 * токен «по правам» с действием «порты» — берут порт хоста на свою
 * машину. По умолчанию пула нет; «выключить» не закрывает уже выданные
 * порты.
 */

interface Pool {
  from: number
  to: number
}

function poolLine(t: (k: string, o?: Record<string, unknown>) => string, p: Pool): string {
  return p.from ? t('portPool.range', { from: p.from, to: p.to }) : t('portPool.off')
}

export function PortPoolModal({ hostRef, hostName, onClose }: { hostRef: string; hostName: string; onClose: () => void }) {
  const { t } = useTranslation()
  const cur = useApi<{ pool: Pool; used: number }>(`/hub/hosts/${hostRef}/port-pool`)
  const [next, setNext] = useState<Pool | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!cur.data) return <Modal title={t('portPool.title', { name: hostName })} onClose={onClose}>{cur.error ? <Banner kind="error">{cur.error}</Banner> : <Loading />}</Modal>
  const before = cur.data.pool ?? { from: 0, to: 0 }
  const value = next ?? before
  const changed = value.from !== before.from || value.to !== before.to
  return (
    <Modal title={t('portPool.title', { name: hostName })} onClose={onClose} width={560}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span className="muted">{t('portPool.hint')}</span>
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
          <InputNumber min={0} max={65535} value={value.from || null} placeholder="20000" onChange={(v) => setNext({ ...value, from: v ?? 0 })} />
          <span>—</span>
          <InputNumber min={0} max={65535} value={value.to || null} placeholder="20999" onChange={(v) => setNext({ ...value, to: v ?? 0 })} />
          <Button size="small" onClick={() => setNext({ from: 0, to: 0 })}>
            {t('portPool.turnOff')}
          </Button>
        </div>
        <span className="muted">{t('portPool.used', { count: cur.data.used })}</span>
        {changed && <DiffView text={`-${poolLine(t, before)}\n+${poolLine(t, value)}`} />}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button
            type="primary"
            disabled={!changed}
            loading={busy}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                await api(`/hub/hosts/${hostRef}/port-pool`, { method: 'PUT', body: value.from ? value : { from: 0, to: 0 } })
                onClose()
              } catch (e) {
                setError(e instanceof Error ? e.message : String(e))
              } finally {
                setBusy(false)
              }
            }}
          >
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}
