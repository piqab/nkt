import { useEffect, useRef, useState } from 'react'
import { Button, Checkbox, Input } from 'antd'
import { useTranslation } from 'react-i18next'
import RFB from '@novnc/novnc'
import { wsURL } from '../hooks/usePty'
import { Banner, Modal } from './ui'

/**
 * Экран виртуальной машины в браузере (noVNC): WebSocket до хоста (и
 * через туннель хаба), дальше — VNC-порт машины на 127.0.0.1 хоста
 * (api.handleVMVNCWS). Установщик, BIOS, рабочий стол, машина без сети —
 * всё, что последовательная консоль не покажет.
 */
export function VNCModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { t } = useTranslation()
  // Элемент экрана — через callback-ref: окно antd рисует содержимое
  // после открытия, и в момент первого эффекта обычный ref ещё пуст —
  // соединение тогда не создавалось вовсе.
  const [screen, setScreen] = useState<HTMLDivElement | null>(null)
  const rfbRef = useRef<RFB | null>(null)
  const [state, setState] = useState<'connecting' | 'connected' | 'disconnected' | 'password'>('connecting')
  const [reason, setReason] = useState<string | null>(null)
  const [password, setPassword] = useState('')
  const [viewOnly, setViewOnly] = useState(false)

  useEffect(() => {
    if (!screen) return
    const rfb = new RFB(screen, wsURL(`/vms/${encodeURIComponent(name)}/vnc/ws`), { wsProtocols: ['binary'] })
    rfb.scaleViewport = true
    rfb.resizeSession = false
    rfb.focusOnClick = true
    rfbRef.current = rfb
    const onConnect = () => {
      setState('connected')
      rfb.focus()
    }
    const onDisconnect = (e: Event) => {
      setState('disconnected')
      const clean = (e as CustomEvent<{ clean: boolean }>).detail?.clean
      if (!clean) setReason(t('vnc.lost'))
    }
    const onCredentials = () => setState('password')
    const onFailure = (e: Event) => setReason((e as CustomEvent<{ reason?: string }>).detail?.reason ?? t('vnc.failed'))
    // Отказ ещё до установки WebSocket (веб-терминал выключен, у машины
    // нет VNC, демо-режим) noVNC событием не сообщает — окно висело бы на
    // «подключение…». Через 10 секунд без соединения — объяснение.
    let connected = false
    const timer = window.setTimeout(() => {
      if (!connected) {
        setState('disconnected')
        setReason(t('vnc.noConnect'))
      }
    }, 10_000)
    rfb.addEventListener('connect', () => {
      connected = true
      window.clearTimeout(timer)
    })
    rfb.addEventListener('connect', onConnect)
    rfb.addEventListener('disconnect', onDisconnect)
    rfb.addEventListener('credentialsrequired', onCredentials)
    rfb.addEventListener('securityfailure', onFailure)
    return () => {
      rfb.removeEventListener('connect', onConnect)
      rfb.removeEventListener('disconnect', onDisconnect)
      rfb.removeEventListener('credentialsrequired', onCredentials)
      rfb.removeEventListener('securityfailure', onFailure)
      window.clearTimeout(timer)
      rfb.disconnect()
      rfbRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- одно соединение на окно и элемент
  }, [name, screen])

  useEffect(() => {
    if (rfbRef.current) rfbRef.current.viewOnly = viewOnly
  }, [viewOnly])

  return (
    <Modal title={t('vnc.title', { name })} onClose={onClose} width="min(96vw, 1280px)" maskClosable={false}>
      <div className="row" style={{ gap: '0.75rem', alignItems: 'center', marginBottom: '0.5rem', flexWrap: 'wrap' }}>
        <span className="small muted">{t(`vnc.state.${state}`)}</span>
        <Button size="small" disabled={state !== 'connected'} onClick={() => rfbRef.current?.sendCtrlAltDel()}>
          Ctrl+Alt+Del
        </Button>
        <Checkbox checked={viewOnly} onChange={(e) => setViewOnly(e.target.checked)}>
          {t('vnc.viewOnly')}
        </Checkbox>
      </div>
      {reason && <Banner kind="error">{reason}</Banner>}
      {state === 'password' && (
        <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem' }}>
          <Input.Password placeholder={t('vnc.password')} value={password} onChange={(e) => setPassword(e.target.value)} style={{ maxWidth: '16rem' }} />
          <Button
            type="primary"
            onClick={() => {
              rfbRef.current?.sendCredentials({ password })
              setState('connecting')
            }}
          >
            {t('vnc.send')}
          </Button>
        </div>
      )}
      <div ref={setScreen} style={{ width: '100%', height: '70vh', background: '#000', borderRadius: 'var(--radius-sm)', overflow: 'hidden' }} />
      <div className="small muted" style={{ marginTop: '0.4rem' }}>
        {t('vnc.hint')}
      </div>
    </Modal>
  )
}
