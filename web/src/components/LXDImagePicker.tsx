import { useState } from 'react'
import { Segmented, Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import { formatBytes } from './charts'

export interface LXDImage {
  ref: string
  alias: string
  remote: string
  description: string
  type: 'container' | 'virtual-machine'
  arch: string
  size: number
  os: string
  release: string
  fingerprint?: string
}

/**
 * Выбор образа для нового инстанса LXD: уже скачанные на хост и два
 * удалённых источника — images: (images.lxd.canonical.com: Debian,
 * Alpine, Rocky, Fedora, Arch…) и ubuntu: (официальные Ubuntu). Удалённый
 * список запрашивается хостом (нужен интернет) и кэшируется там на сутки.
 */
export function LXDImagePicker({ value, onChange }: { value?: string; onChange?: (ref: string, vm: boolean) => void }) {
  const { t } = useTranslation()
  const [remote, setRemote] = useState<'local' | 'images' | 'ubuntu'>('images')
  const [kind, setKind] = useState<'container' | 'virtual-machine'>('container')
  const list = useApi<{ images: LXDImage[] }>(`/lxd/images?remote=${remote}`)
  const images = (list.data?.images ?? []).filter((i) => i.type === kind)
  return (
    <div className="col" style={{ gap: '0.4rem' }}>
      <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap' }}>
        <Segmented
          size="small"
          value={remote}
          onChange={(v) => setRemote(v as typeof remote)}
          options={[
            { value: 'images', label: 'images:' },
            { value: 'ubuntu', label: 'ubuntu:' },
            { value: 'local', label: t('lxdImages.local') },
          ]}
        />
        <Segmented
          size="small"
          value={kind}
          onChange={(v) => setKind(v as typeof kind)}
          options={[
            { value: 'container', label: t('lxd.container') },
            { value: 'virtual-machine', label: t('lxd.vm') },
          ]}
        />
      </div>
      <Select
        showSearch
        value={value || undefined}
        loading={list.loading}
        placeholder={list.error ? t('lxdImages.unavailable') : t('lxdImages.placeholder')}
        onChange={(v: string) => onChange?.(v, kind === 'virtual-machine')}
        optionFilterProp="label"
        options={images.map((i) => ({
          value: i.ref,
          label: `${i.ref} — ${i.description || `${i.os} ${i.release}`}${i.size ? ` (${formatBytes(i.size)})` : ''}`,
        }))}
        notFoundContent={list.loading ? t('lxdImages.loading') : t('lxdImages.empty')}
      />
      <div className="small muted">{list.error ? `${t('lxdImages.unavailable')}: ${list.error}` : t(`lxdImages.hint.${remote}`)}</div>
    </div>
  )
}
