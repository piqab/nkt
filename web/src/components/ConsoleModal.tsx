import { useState } from 'react'
import { Button, Input } from 'antd'
import { useTranslation } from 'react-i18next'
import { qs } from '../api'
import CommandModal from './CommandModal'
import { Modal } from './ui'
import { GuestLoginBar } from './GuestLogin'

export type ConsoleKind = 'docker' | 'podman' | 'lxd' | 'vm'

/**
 * Консоль внутри контейнера, инстанса LXD или машины — живой терминал
 * в окне (тот же PTY-мост, что у «Терминала»). У контейнеров можно
 * задать пользователя (docker exec -u); у машины — последовательная
 * консоль гостя (virsh console), выход — Ctrl+].
 */
export function ConsoleModal({ kind, name, onClose, canControl = true }: { kind: ConsoleKind; name: string; onClose: () => void; canControl?: boolean }) {
  const { t } = useTranslation()
  const [user, setUser] = useState('')
  const [session, setSession] = useState<string | null>(kind === 'docker' || kind === 'podman' ? null : '')
  if (session === null) {
    return (
      <Modal title={t('console.title', { name })} onClose={onClose} width={560}>
        <p className="small muted">{t('console.userHint')}</p>
        <div className="row" style={{ gap: '0.5rem' }}>
          <Input placeholder={t('console.userPlaceholder')} value={user} onChange={(e) => setUser(e.target.value)} style={{ maxWidth: '16rem' }} onPressEnter={() => setSession(user.trim())} />
          <Button type="primary" onClick={() => setSession(user.trim())}>
            {t('console.open')}
          </Button>
        </div>
      </Modal>
    )
  }
  return (
    <CommandModal
      key={session}
      title={t('console.title', { name })}
      description={t(`console.hint.${kind}`)}
      wsPath={`/console/ws${qs({ kind, name, user: session || undefined })}`}
      onClose={onClose}
      sendOnConnect={kind === 'vm' ? '\r' : undefined}
      extra={kind === 'lxd' || kind === 'vm' ? <GuestLoginBar kind={kind} name={name} canControl={canControl} /> : undefined}
    />
  )
}
