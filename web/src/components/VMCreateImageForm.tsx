import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, NetworkInterface, VMImage, VMNetwork, VMSpec, VMTemplate } from '../types'
import { nicLabel, nicSuitable, nicV4 } from '../nics'
import { Banner, ErrorNote, Modal } from './ui'
import { msg, type Msg } from '../msg'

/**
 * Форма создания машины из образа — одна на всё приложение: её открывают
 и раздел «Образы» (кнопка «создать машину» у строки образа), и окно
 «+ Новая» в списке машин libvirt (вкладкой по умолчанию). Механика одна:
 POST /vm/create → фоновое задание хоста (диск из образа, cloud-init,
 define, запуск), приватный ключ показывается один раз.
 *
 * Ключ обязателен по существу: облачные образы приходят без пароля, и
 * машина без ключа окажется недоступной — о чём здесь и сказано прямо, а
 * не выяснится после первого запуска.
 */
/** Пункт выбора образа: сама запись и предупреждение про неё (файл
 * каталога дисков может быть диском работающей машины). */
export interface VMImageChoice {
  image: VMImage
  warning?: string
}

export function VMCreateImageForm({
  image,
  choices,
  warning,
  templates,
  onSaved,
  onClose,
  onStarted,
  onKey,
}: {
  /** Образ, выбранный заранее (строка каталога или диск хоста). */
  image: VMImage | null
  /** Когда образа нет заранее — выбрать из скачанных на хосте. */
  choices?: VMImageChoice[]
  /** Предупреждение о самом образе: например, что это диск работающей
   * машины и копия получится снятой на ходу. */
  warning?: string
  templates: VMTemplate[]
  onSaved: () => void
  onClose: () => void
  onStarted: (job: Job) => void
  onKey: (privateKey: string) => void
}) {
  const { t } = useTranslation()
  const [picked, setPicked] = useState('')
  const choice = choices?.find((c) => c.image.id === picked)
  const chosen = image ?? choice?.image ?? null
  const shownWarning = warning ?? choice?.warning
  const [name, setName] = useState('')
  const [user, setUser] = useState('deploy')
  const [sshKey, setSSHKey] = useState('')
  // Пароль — необязательный вход на экран и в консоль; уходит в cloud-init
  // хэшем и хранится в nkt зашифрованным, в шаблон не попадает.
  const [vmPassword, setVMPassword] = useState('')
  const [vmGenerate, setVMGenerate] = useState(false)
  const [diskGB, setDiskGB] = useState(20)
  const [memoryMB, setMemoryMB] = useState(2048)
  const [vcpus, setVCPUs] = useState(2)
  // Сеть по умолчанию — первая существующая. Ставить «default» вслепую
  // нельзя: на минимальной установке libvirt её нет, и машина падала бы
  // на запуске. Если сетей нет вовсе, имя можно ввести — задание заведёт
  // её само.
  const [network, setNetwork] = useState('')
  const [bridge, setBridge] = useState('')
  // macvtap: машина включается в устройство хоста напрямую, мимо мостов и
  // сетей libvirt. device — имя этого устройства.
  const [device, setDevice] = useState('')
  // Сети хоста: выбирать из списка вернее, чем вписывать имя наугад —
  // ошибка в нём выясняется только при запуске машины.
  const nets = useApi<{ networks: VMNetwork[] }>('/vm/networks', 60_000)
  const known = nets.data?.networks ?? []
  // Устройства хоста: их на хосте бывает два и больше, поэтому выбирать
  // надо списком, а не вписывать имя вслепую. Пригодность устройства хост
  // проверит сам при создании машины — здесь только то, что видно по снимку.
  const ifs = useApi<{ interfaces?: NetworkInterface[] }>('/interfaces', 300_000)
  const nics = (ifs.data?.interfaces ?? []).filter(nicSuitable)
  const chosenNetwork = network || known[0]?.name || 'default'
  const [autostart, setAutostart] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Msg | null>(null)
  const [templateName, setTemplateName] = useState('')

  // Шаблон подставляет железо и пользователя, но не имя машины: имя у
  // каждой своё, и подставлять чужое было бы приглашением к опечатке.
  function applyTemplate(tpl: VMTemplate) {
    const spec = parseSpec(tpl.spec)
    if (spec.disk_gb) setDiskGB(spec.disk_gb)
    if (spec.memory_mb) setMemoryMB(spec.memory_mb)
    if (spec.vcpus) setVCPUs(spec.vcpus)
    if (spec.user) setUser(spec.user)
    if (spec.ssh_key) setSSHKey(spec.ssh_key)
    // Источник подключения — один: шаблон описывает машину, а не три
    // сети сразу, и два названных вида был бы отказом хоста при создании.
    if (spec.bridge) {
      setBridge(spec.bridge)
      setDevice('')
    }
    if (spec.direct) {
      setDevice(spec.direct)
      setBridge('')
    }
    if (spec.network) setNetwork(spec.network)
  }

  async function saveTemplate() {
    if (!chosen) return
    const tplName = templateName.trim()
    if (!tplName) return
    setError(null)
    try {
      await api('/vm/templates', {
        method: 'POST',
        body: {
          name: tplName,
          spec: {
            image_id: chosen.id,
            disk_gb: diskGB,
            memory_mb: memoryMB,
            vcpus,
            user,
            ssh_key: sshKey.trim(),
            network: bridge.trim() || device.trim() ? '' : chosenNetwork,
            bridge: bridge.trim(),
            direct: device.trim(),
          },
        },
      })
      setTemplateName('')
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function create() {
    if (!chosen) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number; private_key?: string; public_key?: string }>('/vm/create', {
        method: 'POST',
        body: {
          name,
          image_id: chosen.id,
          disk_gb: diskGB,
          memory_mb: memoryMB,
          vcpus,
          network: bridge.trim() || device.trim() ? '' : network,
          bridge: bridge.trim(),
          direct: device.trim(),
          user,
          ssh_key: sshKey.trim(),
          password: vmGenerate ? generatePassword() : vmPassword || undefined,
          autostart,
        },
      })
      const job = await api<Job>(`/jobs/${res.job_id}`)
      if (res.private_key) {
        // Ключ показывается один раз: нигде больше он не хранится, и
        // закрыть это окно, не сохранив его, — значит потерять доступ.
        onKey(res.private_key)
      }
      onStarted(job)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <ErrorNote error={msg(error)} />
      {shownWarning && <Banner kind="warn">{shownWarning}</Banner>}
      {!sshKey.trim() && <Banner kind="info">{t('vmimages.keyWillBeGenerated')}</Banner>}

      {!image && (
        <label style={{ marginBottom: '0.6rem' }}>
          {t('vmimages.imageChoice')}
          <Select
            value={picked || undefined}
            onChange={(v: string) => setPicked(v)}
            placeholder={t('vmimages.imageChoiceEmpty')}
            style={{ width: '100%' }}
            options={(choices ?? []).map((c) => ({
              value: c.image.id,
              label: `${c.image.name}${c.image.custom ? ` — ${t('vmimages.customBadge')}` : ''}`,
            }))}
          />
        </label>
      )}

      <div className="grid grid-2" style={{ marginBottom: '0.6rem' }}>
        <label>
          {t('vmimages.name')}
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="web-01" />
        </label>
        <label>
          {t('vmimages.user')}
          <Input value={user} onChange={(e) => setUser(e.target.value)} />
        </label>
        <label>
          {t('vmimages.disk')}
          <InputNumber value={diskGB} min={1} max={4096} onChange={(v) => setDiskGB(v ?? 20)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('vmimages.memory')}
          <InputNumber value={memoryMB} min={256} step={256} onChange={(v) => setMemoryMB(v ?? 2048)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('vmimages.vcpus')}
          <InputNumber value={vcpus} min={1} max={256} onChange={(v) => setVCPUs(v ?? 2)} style={{ width: '100%' }} />
        </label>
        <label>
          {t('vmimages.network')}
          <Select
            value={bridge ? '__bridge__' : device ? '__direct__' : chosenNetwork}
            onChange={(v: string) => {
              if (v === '__bridge__') {
                setDevice('')
                setBridge('br0')
                return
              }
              if (v === '__direct__') {
                setBridge('')
                setNetwork('')
                // Первое пригодное устройство: на хосте их бывает два и
                // больше, и выбирать всё равно отсюда.
                setDevice(nics[0]?.name ?? 'eth0')
                return
              }
              setBridge('')
              setDevice('')
              setNetwork(v)
            }}
            options={[
              ...known.map((n) => ({
                value: n.name,
                label: n.active ? n.name : `${n.name} (${t('vmnet.inactive')})`,
              })),
              // Сетей нет вовсе — предлагаем завести «default»: задание
              // создаст её само при первом же запуске машины.
              ...(known.length === 0 ? [{ value: 'default', label: t('vmimages.networkWillCreate') }] : []),
              { value: '__bridge__', label: t('vmimages.bridgeOption') },
              { value: '__direct__', label: t('vmimages.directOption') },
            ]}
          />
          {bridge !== '' && (
            <Input
              value={bridge}
              onChange={(e) => setBridge(e.target.value)}
              placeholder="br0"
              style={{ marginTop: '0.25rem' }}
            />
          )}
          {device !== '' &&
            (nics.length > 0 ? (
              <Select
                value={device}
                onChange={(v: string) => setDevice(v)}
                style={{ width: '100%', marginTop: '0.25rem' }}
                options={nics.map((n) => ({ value: n.name, label: nicLabel(n.name, nicV4(n)) }))}
              />
            ) : (
              // Снимок интерфейсов не отвечен — имя вписывают руками, хост
              // проверит его при создании машины и откажет по имени.
              <Input
                value={device}
                onChange={(e) => setDevice(e.target.value.trim())}
                placeholder="eth0"
                style={{ marginTop: '0.25rem' }}
              />
            ))}
          {device !== '' && <span className="small muted">{t('vmimages.directHint')}</span>}
        </label>
      </div>

      <label style={{ marginBottom: '0.6rem' }}>
        {t('vmimages.sshKey')}
        <Input.TextArea rows={3} value={sshKey} onChange={(e) => setSSHKey(e.target.value)} placeholder="ssh-ed25519 AAAA..." />
      </label>
      <div>
        <div className="small muted">{t('guestLogin.createHint')}</div>
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <Input.Password
            value={vmPassword}
            onChange={(e) => setVMPassword(e.target.value)}
            disabled={vmGenerate}
            placeholder={t('guestLogin.password')}
            style={{ width: '14rem' }}
            autoComplete="new-password"
          />
          <Checkbox checked={vmGenerate} onChange={(e) => setVMGenerate(e.target.checked)}>
            {t('guestLogin.generate')}
          </Checkbox>
        </div>
      </div>

      <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', margin: '0.6rem 0' }}>
        <Checkbox checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
        {t('vmimages.autostart')}
      </label>

      {templates.length > 0 && (
        <div className="row" style={{ gap: '0.35rem', marginBottom: '0.6rem' }}>
          <span className="small muted">{t('vmimages.useTemplate')}</span>
          {templates.map((tpl) => (
            <Button key={tpl.id} size="small" onClick={() => applyTemplate(tpl)}>
              {tpl.name}
            </Button>
          ))}
        </div>
      )}

      <div className="row" style={{ gap: '0.5rem' }}>
        <Button type="primary" loading={busy} disabled={!name.trim() || !chosen} onClick={() => void create()}>
          {t('vmimages.create')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <span className="row" style={{ gap: '0.35rem', marginLeft: 'auto' }}>
          <Input
            value={templateName}
            onChange={(e) => setTemplateName(e.target.value)}
            placeholder={t('vmimages.templateName')}
            style={{ width: '11rem' }}
          />
          <Button size="small" disabled={!templateName.trim()} onClick={() => void saveTemplate()}>
            {t('vmimages.saveTemplate')}
          </Button>
        </span>
      </div>
    </>
  )
}

/** Та же форма окном — для кнопок раздела «Образы», где образ уже выбран. */
export function VMCreateImageModal({
  image,
  warning,
  templates,
  onSaved,
  onClose,
  onStarted,
  onKey,
}: {
  image: VMImage
  warning?: string
  templates: VMTemplate[]
  onSaved: () => void
  onClose: () => void
  onStarted: (job: Job) => void
  onKey: (privateKey: string) => void
}) {
  const { t } = useTranslation()
  return (
    <Modal title={t('vmimages.createTitle', { image: image.name })} onClose={onClose} width={720}>
      <VMCreateImageForm
        image={image}
        warning={warning}
        templates={templates}
        onSaved={onSaved}
        onClose={onClose}
        onStarted={onStarted}
        onKey={onKey}
      />
    </Modal>
  )
}

/** Описание шаблона хранится строкой JSON: набор полей меняется вместе с
 * формой, и разбирать его строго типизированно здесь незачем. */
export function parseSpec(raw: string): Partial<VMSpec> {
  try {
    return JSON.parse(raw) as Partial<VMSpec>
  } catch {
    return {}
  }
}

/** Случайный пароль для машины (16 знаков без похожих символов). */
export function generatePassword(): string {
  const alphabet = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  const buf = new Uint32Array(16)
  crypto.getRandomValues(buf)
  return Array.from(buf, (n) => alphabet[n % alphabet.length]).join('')
}
