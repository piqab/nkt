import { useState } from 'react'
import { Button, Select, Space } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import { Banner, Loading, Modal } from './ui'
import { confirmAction } from './confirm'

interface UpgradeInfo {
  flavor: string
  role: string
  version: string
  minor: string
  channels: string[]
}

/**
 * Обновление Kubernetes: текущая версия и выбор минорной (у k3s —
 * каналы update.k3s.io, у kubeadm — не дальше следующей). Запуск —
 * фоновым заданием (onStart).
 */
export function K8sUpgradeModal({ title, infoPath, hint, onStart, onClose }: { title: string; infoPath: string; hint: string; onStart: (version: string, info: UpgradeInfo) => Promise<boolean>; onClose: () => void }) {
  const { t } = useTranslation()
  const info = useApi<UpgradeInfo>(infoPath)
  const [version, setVersion] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const channels = info.data?.channels ?? []
  const newer = channels.filter((c) => c !== info.data?.minor)
  const chosen = version ?? newer[0] ?? info.data?.minor ?? null
  return (
    <Modal title={title} onClose={onClose} width={620}>
      {info.error ? (
        <Banner kind="error">{info.error}</Banner>
      ) : !info.data ? (
        <Loading what={t('k8s.upgrade.title')} />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <p className="small">{t('k8s.upgrade.current', { version: info.data.version, flavor: info.data.flavor })}</p>
          <p className="small muted">{hint}</p>
          {info.data.flavor === 'kubeadm' && <p className="small muted">{t('k8s.upgrade.kubeadmStep')}</p>}
          <Space wrap>
            <span className="small">{t('k8s.upgrade.target')}</span>
            <Select size="small" style={{ width: '9rem' }} value={chosen} onChange={setVersion} options={channels.map((c) => ({ value: c, label: c === info.data?.minor ? `${c} (${t('k8s.upgrade.patch')})` : c }))} />
            <Button
              type="primary"
              danger
              loading={busy}
              disabled={!chosen}
              onClick={async () => {
                if (!chosen || !info.data) return
                if (!(await confirmAction(t('k8s.upgrade.confirm', { version: chosen }), { okText: t('k8s.upgrade.start') }))) return
                setBusy(true)
                const ok = await onStart(chosen, info.data)
                setBusy(false)
                if (ok) onClose()
              }}
            >
              {t('k8s.upgrade.start')}
            </Button>
          </Space>
        </div>
      )}
    </Modal>
  )
}
