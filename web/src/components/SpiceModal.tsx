import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Button } from 'antd'
import { useTranslation } from 'react-i18next'
import { wsURL } from '../hooks/usePty'
import { Banner, Modal } from './ui'

// Клиент spice-html5 (LGPL-3.0) лежит отдельными неминифицированными
// файлами в /vendor/spice-html5/ и грузится только при открытии окна —
// в сборку nkt он не входит и заменяется простой подменой файлов.
type SpiceConn = { stop: () => void }
type SpiceModule = {
  SpiceMainConn: new (o: {
    uri: string
    password: string
    screen_id: string
    onerror?: (e: unknown) => void
    onsuccess?: () => void
  }) => SpiceConn
  sendCtrlAltDel: (sc: SpiceConn) => void
}

let spiceModule: Promise<SpiceModule> | null = null
function loadSpice(): Promise<SpiceModule> {
  if (!spiceModule) {
    const url = new URL('/vendor/spice-html5/main.js', window.location.origin).href
    spiceModule = import(/* @vite-ignore */ url) as Promise<SpiceModule>
    spiceModule.catch(() => {
      spiceModule = null
    })
  }
  return spiceModule
}

/** Экран машины по SPICE: libvirt со SPICE-графикой или VM LXD. */
export function SpiceModal({ title, wsPath, onClose, extra, below }: { title: string; wsPath: string; onClose: () => void; extra?: ReactNode; below?: ReactNode }) {
  const { t } = useTranslation()
  const screenId = 'spice-' + useId().replace(/[^a-zA-Z0-9]/g, '')
  const [screen, setScreen] = useState<HTMLDivElement | null>(null)
  const connRef = useRef<SpiceConn | null>(null)
  const modRef = useRef<SpiceModule | null>(null)
  const [state, setState] = useState<'connecting' | 'connected' | 'disconnected'>('connecting')
  const [reason, setReason] = useState<string | null>(null)

  useEffect(() => {
    if (!screen) return
    let cancelled = false
    let connected = false
    const timer = window.setTimeout(() => {
      if (!connected && !cancelled) {
        setState('disconnected')
        setReason(t('spice.noConnect'))
      }
    }, 15_000)
    loadSpice()
      .then((mod) => {
        if (cancelled) return
        modRef.current = mod
        try {
          const sc = new mod.SpiceMainConn({
            uri: wsURL(wsPath),
            password: '',
            screen_id: screenId,
            onsuccess: () => {
              connected = true
              window.clearTimeout(timer)
              setState('connected')
            },
            onerror: (e) => {
              setState('disconnected')
              setReason(e instanceof Error ? e.message : typeof e === 'string' ? e : t('spice.lost'))
            },
          })
          connRef.current = sc
          ;(window as unknown as { spice_connection?: SpiceConn }).spice_connection = sc
        } catch (e) {
          setState('disconnected')
          setReason(e instanceof Error ? e.message : String(e))
        }
      })
      .catch((e) => {
        setState('disconnected')
        setReason(t('spice.loadFailed', { error: e instanceof Error ? e.message : String(e) }))
      })
    return () => {
      cancelled = true
      window.clearTimeout(timer)
      try {
        connRef.current?.stop()
      } catch {
        // соединение уже закрыто
      }
      connRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- одно соединение на окно и элемент
  }, [wsPath, screen])

  return (
    <Modal title={title} onClose={onClose} width="min(96vw, 1280px)" maskClosable={false} sizeKey="screen">
      <div className="row" style={{ gap: '0.75rem', alignItems: 'center', marginBottom: '0.5rem', flexWrap: 'wrap' }}>
        <span className="small muted">{t(`vnc.state.${state}`)}</span>
        <Button
          size="small"
          disabled={state !== 'connected'}
          onClick={() => connRef.current && modRef.current?.sendCtrlAltDel(connRef.current)}
        >
          Ctrl+Alt+Del
        </Button>
        {extra}
      </div>
      {below}
      {reason && <Banner kind="error">{reason}</Banner>}
      <div className="modal-fill" style={{ width: '100%', height: '70vh', background: '#000', borderRadius: 'var(--radius-sm)', overflow: 'auto' }}>
        <div id={screenId} ref={setScreen} tabIndex={0} style={{ display: 'inline-block', minWidth: '100%', minHeight: '100%', outline: 'none' }} />
      </div>
      <div className="small muted" style={{ marginTop: '0.4rem' }}>
        {t('spice.hint')}
      </div>
    </Modal>
  )
}
