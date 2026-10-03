import { useState } from 'react'
import { Button, Space } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Loading, Modal } from './ui'
import { confirmAction } from './confirm'

interface SudoInfo {
  status: string
  user: string
  ops: string[]
  rule: string
  rule_path: string
  key_path: string
  command: string
}

/** «Что разрешено хабу через sudo» на хосте: состояние, операции
 * hub-sudo, правило sudoers; «сузить» полный sudo и «снять правило». */
export function SudoInfoModal({ hostId, hostName, onClose, onChanged }: { hostId: number; hostName: string; onClose: () => void; onChanged: () => void }) {
  const { t } = useTranslation()
  const info = useApi<SudoInfo>(`/hub/hosts/${hostId}/sudo`)
  const [busy, setBusy] = useState<string | null>(null)
  const [note, setNote] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const d = info.data

  async function act(kind: 'narrow' | 'remove') {
    if (kind === 'remove' && !(await confirmAction(t('hosts.confirmRemoveSudo', { user: d?.user, name: hostName })))) return
    if (kind === 'narrow' && !(await confirmAction(t('sudo.confirmNarrow', { user: d?.user, name: hostName }), { danger: false }))) return
    setBusy(kind)
    setNote(null)
    try {
      if (kind === 'narrow') {
        const res = await api<{ mode: string }>(`/hub/hosts/${hostId}/sudo/narrow`, { method: 'POST' })
        setNote({ kind: 'info', text: res.mode === 'narrow' ? t('sudo.narrowed') : t('sudo.narrowedOtherRule') })
      } else {
        await api(`/hub/hosts/${hostId}/sudo/remove`, { method: 'POST' })
        setNote({ kind: 'info', text: t('sudo.removed') })
      }
      onChanged()
      await info.reload()
    } catch (err) {
      setNote({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal title={t('sudo.title', { name: hostName })} onClose={onClose} width={680}>
      {!d ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <p className="small" style={{ margin: 0 }}>
            <strong>{t(`sudo.state.${d.status || 'unknown'}`, { defaultValue: d.status })}</strong>
          </p>
          <p className="small muted" style={{ margin: 0 }}>
            {t('sudo.explain')}
          </p>
          {note && <Banner kind={note.kind}>{note.text}</Banner>}
          <div>
            <div className="small" style={{ marginBottom: '0.25rem' }}>
              <strong>{t('sudo.opsTitle', { command: d.command })}</strong>
            </div>
            <ul className="small" style={{ margin: 0, paddingLeft: '1.2rem' }}>
              {d.ops.map((op) => (
                <li key={op}>
                  <span className="mono">{op}</span> — {t(`sudo.op.${op}`, { defaultValue: op })}
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="small" style={{ marginBottom: '0.25rem' }}>
              <strong>{t('sudo.ruleTitle', { path: d.rule_path })}</strong>
            </div>
            <pre className="mono small" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{d.rule}</pre>
            <div className="small muted">{t('sudo.keyNote', { path: d.key_path })}</div>
          </div>
          <Space wrap>
            {d.status === 'nopasswd' && (
              <Button type="primary" loading={busy === 'narrow'} disabled={!!busy} onClick={() => void act('narrow')}>
                {t('sudo.narrow')}
              </Button>
            )}
            {(d.status === 'nopasswd' || d.status === 'narrow') && (
              <Button danger loading={busy === 'remove'} disabled={!!busy} onClick={() => void act('remove')}>
                {t('sudo.remove')}
              </Button>
            )}
          </Space>
        </div>
      )}
    </Modal>
  )
}
