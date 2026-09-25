import { useState } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { ServiceUnit } from '../types'
import { Banner } from './ui'
import PackageInstallModal from './PackageInstallModal'

/**
 * «Движок не установлен — установить»: для вкладок LXD и Podman. Тот же
 * путь, что у «Служб» (/services/{name}/install/ws): podman — через apt,
 * LXD — snap (snapd ставится сам, если его нет) и `lxd init --auto`.
 */
export function EngineInstallBanner({ service, canControl, onInstalled }: { service: 'lxd' | 'podman'; canControl: boolean; onInstalled: () => void }) {
  const { t } = useTranslation()
  const services = useApi<{ services: ServiceUnit[] }>('/services', 60_000)
  const unit = services.data?.services.find((s) => s.name === service)
  const [installing, setInstalling] = useState(false)
  const [outcome, setOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  if (!unit || unit.installed) return null

  async function finished() {
    const st = await api<{ succeeded?: boolean; exit_code?: number }>(`/services/${service}/install/status`).catch(() => null)
    setOutcome(st?.succeeded ? { ok: true } : { ok: false, exitCode: st?.exit_code })
    await api('/inventory/refresh', { method: 'POST' }).catch(() => undefined)
    services.reload()
    onInstalled()
  }

  return (
    <>
      <Banner kind="warn">
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <span>{t(`engineInstall.${service}`)}</span>
          {canControl && (
            <Button size="small" type="primary" onClick={() => setInstalling(true)}>
              {t('engineInstall.install')}
            </Button>
          )}
        </div>
      </Banner>
      {installing && (
        <PackageInstallModal
          packageName={service}
          wsPath={`/services/${service}/install/ws`}
          onClose={() => setInstalling(false)}
          onFinished={() => void finished()}
          outcome={outcome}
        />
      )}
    </>
  )
}
