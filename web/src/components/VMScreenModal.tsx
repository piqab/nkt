import { useState } from 'react'
import { Button, Segmented } from 'antd'
import { useTranslation } from 'react-i18next'
import { VNCModal } from './VNCModal'
import { SpiceModal } from './SpiceModal'
import { Modal } from './ui'

/** Экран машины libvirt: VNC (noVNC), если он есть, иначе SPICE
 * (spice-html5); без VNC — кнопка «добавить VNC» через правку XML. */
export function VMScreenModal({
  name,
  graphics,
  onClose,
  onAddVNC,
}: {
  name: string
  graphics?: string[]
  onClose: () => void
  onAddVNC: () => void
}) {
  const { t } = useTranslation()
  const g = graphics ?? []
  const hasVNC = g.includes('vnc')
  const hasSpice = g.includes('spice')
  // Нет сведений о графике (старый снимок инвентаря) — пробуем VNC.
  const [mode, setMode] = useState<'vnc' | 'spice'>(hasVNC || !hasSpice ? 'vnc' : 'spice')

  const switcher =
    hasVNC && hasSpice ? (
      <Segmented size="small" value={mode} onChange={(v) => setMode(v as typeof mode)} options={[{ value: 'vnc', label: 'VNC' }, { value: 'spice', label: 'SPICE' }]} />
    ) : null
  const addVNC = !hasVNC && (
    <Button size="small" onClick={onAddVNC}>
      {t('screen.addVNC')}
    </Button>
  )

  if (graphics && g.length === 0) {
    return (
      <Modal title={t('vnc.title', { name })} onClose={onClose}>
        <p>{t('screen.noGraphics')}</p>
        <Button type="primary" onClick={onAddVNC}>
          {t('screen.addVNC')}
        </Button>
      </Modal>
    )
  }
  if (mode === 'spice') {
    return (
      <SpiceModal
        title={t('vnc.title', { name })}
        wsPath={`/vms/${encodeURIComponent(name)}/spice/ws`}
        onClose={onClose}
        extra={
          <>
            {switcher}
            {addVNC}
          </>
        }
      />
    )
  }
  return <VNCModal key="vnc" name={name} onClose={onClose} extra={switcher} />
}

/** Добавляет в XML домена графику VNC на 127.0.0.1 (перед </devices>). */
export function addVNCGraphics(xml: string): string {
  if (/<graphics\s+type=['"]vnc['"]/.test(xml)) return xml
  const m = /^([ \t]*)<\/devices>/m.exec(xml)
  if (!m) return xml
  const ind = m[1]
  const inner = ind + '  '
  const block =
    `${inner}<graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'>\n` +
    `${inner}  <listen type='address' address='127.0.0.1'/>\n` +
    `${inner}</graphics>\n`
  return xml.slice(0, m.index) + block + xml.slice(m.index)
}
