import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, Me, VMImage, VMImageLocal } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal } from '../components/ui'
import { formatBytes } from '../components/charts'
import { DataTable } from '../components/DataTable'
import { confirmAction } from '../components/confirm'
import { JobLogModal } from './Jobs'

/** Пока идёт скачивание, список надо перечитывать: недокачанный кусок
 * растёт, и оператор должен видеть, что дело движется. */
const POLL_MS = 5_000

export default function VMImages({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  const images = useApi<{ catalog: VMImage[]; local: VMImageLocal[]; dir: string }>('/vm/images', POLL_MS)
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [creating, setCreating] = useState<VMImage | null>(null)
  const [error, setError] = useState<string | null>(null)

  const local = new Map((images.data?.local ?? []).map((l) => [l.id, l]))

  async function startJob(path: string, body: Record<string, unknown>) {
    setError(null)
    try {
      const res = await api<{ job_id: number }>(path, { method: 'POST', body })
      const job = await api<Job>(`/jobs/${res.job_id}`)
      setOpenJob(job)
      images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const columns: TableColumnsType<VMImage> = [
    {
      title: t('vmimages.colImage'),
      key: 'name',
      render: (_, img) => (
        <div style={{ minWidth: '14rem' }}>
          <strong>{img.name}</strong>
          <div className="small muted mono">{img.file_name}</div>
        </div>
      ),
    },
    {
      title: t('vmimages.colState'),
      key: 'state',
      render: (_, img) => {
        const l = local.get(img.id)
        if (l?.downloaded) {
          return (
            <span className="nowrap">
              <Tag color="success">{t('vmimages.downloaded')}</Tag>
              <span className="small muted">{formatBytes(l.size ?? 0)}</span>
            </span>
          )
        }
        if (l?.partial) {
          return (
            <span className="nowrap">
              <Tag color="processing">{t('vmimages.partial')}</Tag>
              <span className="small muted">{formatBytes(l.partial_size ?? 0)}</span>
            </span>
          )
        }
        return <Tag>{t('vmimages.absent')}</Tag>
      },
    },
    {
      title: '',
      key: 'actions',
      className: 'nowrap',
      render: (_, img) => {
        const l = local.get(img.id)
        return (
          <div className="row row-nowrap">
            {canEdit && !l?.downloaded && (
              <Button type="link" size="small" onClick={() => void startJob('/vm/images/download', { image_id: img.id })}>
                {l?.partial ? t('vmimages.resume') : t('vmimages.download')}
              </Button>
            )}
            {canEdit && l?.downloaded && (
              <Button type="link" size="small" onClick={() => setCreating(img)}>
                {t('vmimages.createVM')}
              </Button>
            )}
            {canEdit && (l?.downloaded || l?.partial) && (
              <Button
                type="link"
                size="small"
                danger
                onClick={async () => {
                  if (!(await confirmAction(t('vmimages.confirmDelete', { name: img.name })))) return
                  await api('/vm/images/delete', { method: 'POST', body: { image_id: img.id } })
                  images.reload()
                }}
              >
                {t('common.delete')}
              </Button>
            )}
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('vmimages.title')}
          <InfoHint>{t('vmimages.hint')}</InfoHint>
        </h1>
      </div>

      <ErrorNote error={error} />
      <ErrorNote error={images.error} />

      <Card title={t('vmimages.catalogTitle')} subtitle={images.data?.dir}>
        {images.loading && !images.data ? (
          <Loading what={t('vmimages.loading')} />
        ) : (
          <div className="table-wrap">
            <DataTable<VMImage>
              dataSource={images.data?.catalog ?? []}
              columns={columns}
              rowKey="id"
              tableLayout="auto"
            />
          </div>
        )}
      </Card>

      {creating && (
        <CreateVMModal
          image={creating}
          onClose={() => setCreating(null)}
          onStarted={(job) => {
            setCreating(null)
            setOpenJob(job)
          }}
        />
      )}

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
    </>
  )
}

/**
 * Создание машины из образа.
 *
 * Ключ обязателен по существу: облачные образы приходят без пароля, и
 * машина без ключа окажется недоступной — о чём здесь и сказано прямо, а
 * не выяснится после первого запуска.
 */
function CreateVMModal({
  image,
  onClose,
  onStarted,
}: {
  image: VMImage
  onClose: () => void
  onStarted: (job: Job) => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [user, setUser] = useState('deploy')
  const [sshKey, setSSHKey] = useState('')
  const [diskGB, setDiskGB] = useState(20)
  const [memoryMB, setMemoryMB] = useState(2048)
  const [vcpus, setVCPUs] = useState(2)
  const [bridge, setBridge] = useState('')
  const [autostart, setAutostart] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function create() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/vm/create', {
        method: 'POST',
        body: {
          name,
          image_id: image.id,
          disk_gb: diskGB,
          memory_mb: memoryMB,
          vcpus,
          bridge: bridge.trim(),
          user,
          ssh_key: sshKey.trim(),
          autostart,
        },
      })
      onStarted(await api<Job>(`/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('vmimages.createTitle', { image: image.name })} onClose={onClose} width={720}>
      <ErrorNote error={error} />
      {!sshKey.trim() && <Banner kind="warn">{t('vmimages.keyRequired')}</Banner>}

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
          {t('vmimages.bridge')}
          <Input value={bridge} onChange={(e) => setBridge(e.target.value)} placeholder={t('vmimages.bridgeDefault')} />
        </label>
      </div>

      <label style={{ marginBottom: '0.6rem' }}>
        {t('vmimages.sshKey')}
        <Input.TextArea rows={3} value={sshKey} onChange={(e) => setSSHKey(e.target.value)} placeholder="ssh-ed25519 AAAA..." />
      </label>

      <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', marginBottom: '0.6rem' }}>
        <Checkbox checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
        {t('vmimages.autostart')}
      </label>

      <div className="row" style={{ gap: '0.5rem' }}>
        <Button type="primary" loading={busy} disabled={!name.trim() || !sshKey.trim()} onClick={() => void create()}>
          {t('vmimages.create')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}
