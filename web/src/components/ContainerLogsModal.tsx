import { useState } from 'react'
import { Checkbox, Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs } from '../api'
import CommandModal from './CommandModal'

/** Логи контейнера в живом окне: `docker logs --tail N [-f] [-t]`.
 * Смена хвоста/слежения/меток перезапускает сессию с новыми
 * параметрами (у CommandModal ключ — wsPath). */
export default function ContainerLogsModal({ name, base, onClose }: { name: string; base: '/containers' | '/podman/containers'; onClose: () => void }) {
  const { t } = useTranslation()
  const [tail, setTail] = useState(200)
  const [follow, setFollow] = useState(true)
  const [timestamps, setTimestamps] = useState(false)
  const wsPath = `${base}/${encodeURIComponent(name)}/logs/ws${qs({ tail, follow: follow ? 1 : 0, timestamps: timestamps ? 1 : 0 })}`
  return (
    <CommandModal
      key={wsPath}
      title={t('docker.logsTitle', { name })}
      wsPath={wsPath}
      onClose={onClose}
      extra={
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', marginBottom: '0.5rem' }}>
          <span className="small muted">{t('docker.logsTail')}</span>
          <Select size="small" value={tail} onChange={setTail} options={[200, 1000, 5000].map((n) => ({ value: n, label: String(n) }))} style={{ width: '6rem' }} />
          <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
            {t('docker.logsFollow')}
          </Checkbox>
          <Checkbox checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)}>
            {t('docker.logsTimestamps')}
          </Checkbox>
        </div>
      }
    />
  )
}
