import { useState } from 'react'
import { Checkbox, Segmented, Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs } from '../api'
import CommandModal from './CommandModal'

/** Логи инстанса LXD: журнал внутри (journalctl, при его отсутствии —
 * syslog) или лог самого LXD об инстансе (запуск, ошибки). */
export default function LXDLogsModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { t } = useTranslation()
  const [source, setSource] = useState<'journal' | 'lxd'>('journal')
  const [tail, setTail] = useState(200)
  const [follow, setFollow] = useState(true)
  const wsPath = `/lxd/instances/${encodeURIComponent(name)}/logs/ws${qs({ source, tail, follow: follow ? 1 : 0 })}`
  return (
    <CommandModal
      key={wsPath}
      title={t('docker.logsTitle', { name })}
      wsPath={wsPath}
      onClose={onClose}
      extra={
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', marginBottom: '0.5rem', flexWrap: 'wrap' }}>
          <Segmented
            size="small"
            value={source}
            onChange={(v) => setSource(v as typeof source)}
            options={[
              { value: 'journal', label: t('lxdLogs.journal') },
              { value: 'lxd', label: t('lxdLogs.lxd') },
            ]}
          />
          {source === 'journal' && (
            <>
              <span className="small muted">{t('docker.logsTail')}</span>
              <Select size="small" value={tail} onChange={setTail} options={[200, 1000, 5000].map((n) => ({ value: n, label: String(n) }))} style={{ width: '6rem' }} />
              <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
                {t('docker.logsFollow')}
              </Checkbox>
            </>
          )}
        </div>
      }
    />
  )
}
