import { useEffect, useState } from 'react'
import { Button, Checkbox, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import { Banner, Loading, Modal } from './ui'

/**
 * Подтверждение перед certbot --standalone: кто держит 80/443 и что с
 * ним будет.
 *
 * Раньше окно обещало «nginx и haproxy будут остановлены» — списком по
 * памяти, а не по факту: Flask или самописный сервер на 80 оставались,
 * и certbot падал с «Problem binding». Теперь держатели опрашиваются
 * здесь и показываются по одному, каждый со своей судьбой:
 *
 *   - служба systemd — будет остановлена и запущена обратно;
 *   - ручной процесс — по галочке (включена) остановлен и поднят заново
 *     той же командой, в том же каталоге, под тем же пользователем;
 *     команда видна до подтверждения;
 *   - контейнер или нечитаемый процесс — nkt не тронет, а без него
 *     certbot не пройдёт: кнопка заблокирована, порт освобождают сами.
 */

export interface PortHolder {
  ports: number[]
  pid?: number
  process?: string
  user?: string
  command?: string
  cwd?: string
  uptime_s?: number
  unit?: string
  container_id?: string
  kind: 'service' | 'process' | 'container' | 'unknown'
  restartable: boolean
}

export interface StandalonePlan {
  holders: PortHolder[]
  blocked: boolean
}

export function StandaloneConfirm({
  title,
  onCancel,
  onConfirm,
}: {
  title: string
  onCancel: () => void
  /** restartPIDs — ручные процессы, которые разрешено остановить и поднять. */
  onConfirm: (restartPIDs: number[]) => void
}) {
  const { t } = useTranslation()
  const [plan, setPlan] = useState<StandalonePlan | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [restart, setRestart] = useState<Set<number>>(new Set())

  useEffect(() => {
    let cancelled = false
    api<StandalonePlan>('/certificates/standalone-plan')
      .then((p) => {
        if (cancelled) return
        setPlan(p)
        // По умолчанию — перезапускать: цель в том, чтобы после выпуска
        // сервис работал; снял галочку — не трогаем.
        setRestart(new Set(p.holders.filter((h) => h.kind === 'process' && h.restartable && h.pid).map((h) => h.pid!)))
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)))
    return () => {
      cancelled = true
    }
  }, [])

  // Заблокировано, если есть держатель, которого nkt не уберёт, или
  // ручной процесс, который не разрешили трогать.
  const blocked =
    !!plan &&
    plan.holders.some((h) => !h.restartable || (h.kind === 'process' && h.pid !== undefined && !restart.has(h.pid)))

  return (
    <Modal title={title} onClose={onCancel} width={720}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <p className="small" style={{ margin: 0 }}>
          {t('standalone.intro')}
        </p>
        {error && <Banner kind="error">{error}</Banner>}
        {!plan && !error && <Loading what={t('standalone.checking')} />}
        {plan && plan.holders.length === 0 && <Banner kind="info">{t('standalone.free')}</Banner>}
        {plan?.holders.map((h) => (
          <div key={`${h.ports.join('-')}-${h.pid ?? h.container_id ?? h.process}`} className="col" style={{ gap: '0.25rem', padding: '0.5rem 0.65rem', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)' }}>
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <Tag>{h.ports.map((p) => `:${p}`).join(' ')}</Tag>
              <strong>{h.unit || h.process || t('standalone.unknownProcess')}</strong>
              {h.pid ? <span className="small muted mono">pid {h.pid}</span> : null}
              {h.user && <span className="small muted">{h.user}</span>}
              {h.uptime_s ? <span className="small muted">{t('standalone.uptime', { hours: Math.round(h.uptime_s / 360) / 10 })}</span> : null}
            </div>
            {h.kind === 'service' && <div className="small">{t('standalone.serviceFate', { unit: h.unit })}</div>}
            {h.kind === 'process' && h.restartable && (
              <>
                <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
                  <Checkbox
                    checked={h.pid !== undefined && restart.has(h.pid)}
                    onChange={(e) => {
                      const next = new Set(restart)
                      if (e.target.checked) next.add(h.pid!)
                      else next.delete(h.pid!)
                      setRestart(next)
                    }}
                  />
                  {t('standalone.processFate')}
                </label>
                <pre className="diff mono" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>
                  {h.command}
                </pre>
                <div className="small muted">
                  {t('standalone.processDetail', { cwd: h.cwd || '/', user: h.user || 'root' })}
                </div>
                {h.pid !== undefined && !restart.has(h.pid) && (
                  <div className="small" style={{ color: 'var(--status-warning)' }}>
                    {t('standalone.processLeft')}
                  </div>
                )}
              </>
            )}
            {h.kind === 'container' && (
              <div className="small" style={{ color: 'var(--status-critical)' }}>
                {t('standalone.containerFate', { id: (h.container_id ?? '').slice(0, 12) })}
              </div>
            )}
            {(h.kind === 'unknown' || (h.kind === 'process' && !h.restartable)) && (
              <div className="small" style={{ color: 'var(--status-critical)' }}>{t('standalone.unknownFate')}</div>
            )}
          </div>
        ))}
        {plan && plan.holders.some((h) => h.kind === 'process') && (
          <p className="small muted" style={{ margin: 0 }}>
            {t('standalone.processCaveat')}
          </p>
        )}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" disabled={!plan || blocked} onClick={() => onConfirm([...restart])}>
            {t('standalone.go')}
          </Button>
          <Button onClick={onCancel}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}
