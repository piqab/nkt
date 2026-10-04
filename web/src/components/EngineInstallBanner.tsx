import { useState } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { ServiceUnit } from '../types'
import { Banner } from './ui'
import PackageInstallModal from './PackageInstallModal'
import { useJobLauncher } from './useJobLauncher'

type Engine = 'lxd' | 'podman' | 'docker' | 'libvirt'

/**
 * «Движок не установлен — установить» для вкладок «Контейнеров и ВМ».
 * Podman, LXD и libvirt — путь «Служб» (/services/{name}/install/ws):
 * apt, у LXD — snap и `lxd init --auto`, у libvirt — демон, клиенты и
 * qemu своей архитектуры. Docker — своя установка заданием (как в
 * «Выкладках»): официальный репозиторий или пакеты дистрибутива с
 * compose.
 */
export function EngineInstallBanner({ service, canControl, onInstalled }: { service: Engine; canControl: boolean; onInstalled: () => void }) {
  const { t } = useTranslation()
  const services = useApi<{ services: ServiceUnit[] }>('/services', 60_000)
  const unit = services.data?.services.find((s) => s.name === service)
  const [installing, setInstalling] = useState(false)
  const [outcome, setOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const refresh = async () => {
    await api('/inventory/refresh', { method: 'POST' }).catch(() => undefined)
    services.reload()
    onInstalled()
  }
  const docker = useJobLauncher(() => void refresh())
  if (!unit || unit.installed) return null

  async function finished() {
    const st = await api<{ succeeded?: boolean; exit_code?: number }>(`/services/${service}/install/status`).catch(() => null)
    setOutcome(st?.succeeded ? { ok: true } : { ok: false, exitCode: st?.exit_code })
    await refresh()
  }

  async function install() {
    if (service !== 'docker') {
      setInstalling(true)
      return
    }
    setError(null)
    try {
      await docker.start('/system/docker-install/ws')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <>
      <Banner kind="warn">
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <span>{t(`engineInstall.${service}`)}</span>
          {canControl && (
            <Button size="small" type="primary" onClick={() => void install()}>
              {t('engineInstall.install')}
            </Button>
          )}
        </div>
      </Banner>
      {error && <Banner kind="error">{error}</Banner>}
      {docker.modal}
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
