import { useState } from 'react'
import { Button, Checkbox, Form, Input } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import i18n from '../i18n'
import { encryptWithPassword } from '../exportCrypto'
import { Modal } from './ui'
import { confirmAction } from './confirm'

/** Экспорт хаба файлом: GET /hub/export (не JSON для интерфейса — мимо
 * api()), при пароле — шифрование в браузере до сохранения (exportCrypto):
 * открытый экспорт на диск не попадает. */
export async function downloadHubExport(includeKey: boolean, password?: string, includeUsers = false, includeMonitoring = false): Promise<void> {
  const q = [includeKey && 'include_key=1', includeUsers && 'include_users=1', includeMonitoring && 'include_monitoring=1'].filter(Boolean).join('&')
  const res = await fetch(`/api/hub/export${q ? `?${q}` : ''}`, { credentials: 'same-origin' })
  if (!res.ok) {
    const payload = await res.json().catch(() => null)
    throw new Error(payload?.error ?? i18n.t('common.httpError', { status: res.status }))
  }
  let blob = await res.blob()
  const filename = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? 'nkt-hub-export.json'
  if (password) {
    const plaintext = new Uint8Array(await blob.arrayBuffer())
    const encrypted = await encryptWithPassword(password, plaintext)
    blob = new Blob([encrypted.buffer as ArrayBuffer], { type: 'application/octet-stream' })
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/** Окно полного экспорта (с ключом): пароль шифрования, учётные записи. */
export function ExportPasswordModal({
  busy,
  onDownload,
  onClose,
}: {
  busy: boolean
  onDownload: (password: string | undefined, users: boolean, monitoring: boolean) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [users, setUsers] = useState(false)
  // История «Мониторинга» — отдельной галочкой: файл с ней заметно больше.
  const [monitoring, setMonitoring] = useState(false)

  async function download() {
    if (!password) {
      if (!(await confirmAction(t('hosts.confirmExportUnencrypted')))) {
        return
      }
      onDownload(undefined, users, monitoring)
      return
    }
    onDownload(password, users, monitoring)
  }

  return (
    <Modal title={t('hosts.exportWithKeyTitle')} onClose={onClose}>
      <p className="small muted">
        <Trans i18nKey="hosts.exportWithKeyBody" components={{ strong: <strong /> }} />
      </p>
      <Form layout="vertical" onFinish={download}>
        <p className="small muted">{t('hosts.exportContents')}</p>
        <Form.Item>
          <Checkbox checked={users} onChange={(e) => setUsers(e.target.checked)}>
            {t('hosts.exportUsers')}
          </Checkbox>
          <div className="small muted">{t('hosts.exportUsersHint')}</div>
        </Form.Item>
        <Form.Item>
          <Checkbox checked={monitoring} onChange={(e) => setMonitoring(e.target.checked)}>
            {t('hosts.exportMonitoring')}
          </Checkbox>
          <div className="small muted">{t('hosts.exportMonitoringHint')}</div>
        </Form.Item>
        <Form.Item label={t('hosts.encryptPasswordLabel')}>
          <Input.Password
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            autoComplete="new-password"
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {password ? t('hosts.downloadEncrypted') : t('hosts.download')}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}
