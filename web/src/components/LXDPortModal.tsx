import { useState } from 'react'
import { Button, Form, Input, InputNumber, Segmented } from 'antd'
import { useTranslation } from 'react-i18next'
import { Modal } from './ui'
import { setYamlDevice, yamlDeviceNames } from './LXDConfigModal'

/** Проброс порта в инстанс LXD (устройство proxy). Форма только
 * собирает устройство: запись — окном конфигурации, через дифф. */
export default function LXDPortModal({
  name,
  onClose,
  onContinue,
}: {
  name: string
  onClose: () => void
  onContinue: (edit: (saved: string) => string) => void
}) {
  const { t } = useTranslation()
  const [proto, setProto] = useState<'tcp' | 'udp'>('tcp')
  const [listenAddr, setListenAddr] = useState('0.0.0.0')
  const [listenPort, setListenPort] = useState<number | null>(null)
  const [connectPort, setConnectPort] = useState<number | null>(null)
  const addrOK = /^[0-9a-fA-F.:]+$/.test(listenAddr)
  const ok = addrOK && !!listenPort && !!connectPort

  function next() {
    if (!ok) return
    const lp = listenPort
    const cp = connectPort
    const host = listenAddr.includes(':') ? `[${listenAddr}]` : listenAddr
    onContinue((saved) => {
      // Имя устройства — по порту; занятое — с суффиксом.
      const names = yamlDeviceNames(saved)
      let dev = `${proto}${lp}`
      for (let i = 2; names.includes(dev); i++) dev = `${proto}${lp}-${i}`
      return setYamlDevice(saved, dev, {
        type: 'proxy',
        listen: `${proto}:${host}:${lp}`,
        connect: `${proto}:127.0.0.1:${cp}`,
      })
    })
  }

  return (
    <Modal title={t('lxdPort.title', { name })} onClose={onClose}>
      <p className="small muted">{t('lxdPort.hint')}</p>
      <Form layout="vertical" onFinish={next}>
        <Form.Item label={t('lxdPort.protocol')}>
          <Segmented value={proto} onChange={(v) => setProto(v as typeof proto)} options={['tcp', 'udp']} />
        </Form.Item>
        <div className="row" style={{ gap: '0.75rem', flexWrap: 'wrap' }}>
          <Form.Item label={t('lxdPort.listenAddr')} validateStatus={addrOK ? undefined : 'error'}>
            <Input value={listenAddr} onChange={(e) => setListenAddr(e.target.value.trim())} style={{ width: '11rem' }} />
          </Form.Item>
          <Form.Item label={t('lxdPort.listenPort')}>
            <InputNumber min={1} max={65535} value={listenPort} onChange={(v) => setListenPort(v)} style={{ width: '8rem' }} />
          </Form.Item>
          <Form.Item label={t('lxdPort.connectPort')}>
            <InputNumber min={1} max={65535} value={connectPort} onChange={(v) => setConnectPort(v)} style={{ width: '8rem' }} />
          </Form.Item>
        </div>
        <Button type="primary" htmlType="submit" disabled={!ok}>
          {t('lxdPort.continue')}
        </Button>
      </Form>
    </Modal>
  )
}
