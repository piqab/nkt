import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, Me, VMImage, VMImageLocal, VMTemplate, VMSpec, VMTool, VMHostImage, VMNetwork } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { JobLogModal } from '../pages/Jobs'
import VMNetworksCard from './VMNetworksCard'

/** Пока идёт скачивание, список надо перечитывать: недокачанный кусок
 * растёт, и оператор должен видеть, что дело движется. */
const POLL_MS = 5_000

/**
 * Образы машин и шаблоны — часть раздела «Профили».
 *
 * Место не случайно: профиль описывает, каким должен быть хост, образ и
 * шаблон — каким должна быть машина. Это заготовки одного рода, и
 * держать их в разных разделах значило бы разводить по углам то, чем
 * пользуются вместе.
 */
export default function VMImagesSection({ me }: { me: Me }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  const images = useApi<{
    catalog: VMImage[]
    local: VMImageLocal[]
    custom: VMImage[] | null
    custom_local: VMImageLocal[] | null
    host_images: VMHostImage[] | null
    dir: string
    tools: VMTool[]
    missing: VMTool[] | null
  }>('/vm/images', POLL_MS)
  const [adding, setAdding] = useState(false)
  const [creatingHost, setCreatingHost] = useState<VMHostImage | null>(null)

  // Файл из каталога дисков libvirt удаляется насовсем: если это диск
  // машины, она останется без него — об этом и спрашиваем отдельно.
  async function deleteHostImage(img: VMHostImage) {
    const question = img.used_by
      ? t('vmimages.confirmDeleteHostBusy', { name: img.name, vm: img.used_by })
      : t('vmimages.confirmDeleteHost', { name: img.name })
    if (!(await confirmAction(question, { danger: true }))) return
    setError(null)
    try {
      await api('/vm/images/host-delete', { method: 'POST', body: { name: img.name } })
      images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [creating, setCreating] = useState<VMImage | null>(null)
  const [generatedKey, setGeneratedKey] = useState<string | null>(null)
  const templates = useApi<{ templates: VMTemplate[] }>('/vm/templates', 60_000)
  const [error, setError] = useState<string | null>(null)

  // Свои образы идут тем же списком и теми же кнопками: для создания
  // машины разницы между ними и каталожными нет.
  const local = new Map(
    [...(images.data?.local ?? []), ...(images.data?.custom_local ?? [])].map((l) => [l.id, l]),
  )
  const allImages = [...(images.data?.catalog ?? []), ...(images.data?.custom ?? [])]

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
          {img.custom && <Tag style={{ marginLeft: '0.4rem' }}>{t('vmimages.customBadge')}</Tag>}
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
            {canEdit && !l?.downloaded && !img.custom && (
              <RowAction
                action="download"
                label={l?.partial ? t('vmimages.resume') : t('vmimages.download')}
                onClick={() => void startJob('/vm/images/download', { image_id: img.id })}
              />
            )}
            {canEdit && l?.downloaded && (
              <RowAction action="create" label={t('vmimages.createVM')} onClick={() => setCreating(img)} />
            )}
            {canEdit && (l?.downloaded || l?.partial) && (
              <RowAction
                action="delete"
                label={t('common.delete')}
                danger
                onClick={async () => {
                  if (!(await confirmAction(t('vmimages.confirmDelete', { name: img.name })))) return
                  await api('/vm/images/delete', { method: 'POST', body: { image_id: img.id } })
                  images.reload()
                }}
              />
            )}
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <h2 style={{ margin: 0 }}>
          {t('vmimages.title')}
          <InfoHint>{t('vmimages.hint')}</InfoHint>
        </h2>
      </div>

      <ErrorNote error={error} />
      <ErrorNote error={images.error} />

      {/* Без qemu-img и virsh форма создания только обманывала бы
          ожидания, поэтому нехватка видна до, а не после нажатия. */}
      {(images.data?.missing?.length ?? 0) > 0 && (
        <Banner kind="warn">
          <div className="col" style={{ gap: '0.4rem' }}>
            <span>{t('vmimages.toolsMissing')}</span>
            <ul style={{ margin: 0, paddingLeft: '1.1rem' }}>
              {(images.data?.missing ?? []).map((tool) => (
                <li key={tool.command} className="small">
                  {/* Пояснение берётся из переводов по имени команды:
                      с сервера оно пришло бы на одном языке. */}
                  <span className="mono">{tool.command}</span> —{' '}
                  {t(`vmimages.toolWhy.${tool.command}`, { defaultValue: tool.why })} ({t('vmimages.toolPackage')}{' '}
                  <span className="mono">{tool.package}</span>)
                </li>
              ))}
            </ul>
            {canEdit && (
              <span>
                <Button size="small" onClick={() => void startJob('/vm/tools/install', {})}>
                  {t('vmimages.installTools')}
                </Button>
              </span>
            )}
          </div>
        </Banner>
      )}

      <Card title={t('vmimages.catalogTitle')} subtitle={images.data?.dir}>
        {images.loading && !images.data ? (
          <Loading what={t('vmimages.loading')} />
        ) : (
          <div className="table-wrap">
            <DataTable<VMImage>
              dataSource={allImages}
              columns={columns}
              rowKey="id"
              tableLayout="auto"
            />
          </div>
        )}
      </Card>

      {/* Каталог дисков libvirt: и образы, положенные туда руками, и
          диски существующих машин. Показываем всё — место занято именно
          ими, — но помечаем, чьё что. */}
      <Card
        title={t('vmimages.hostTitle')}
        subtitle={t('vmimages.hostHint')}
        actions={
          canEdit && (
            <Button size="small" onClick={() => setAdding(true)}>
              {t('vmimages.addOwn')}
            </Button>
          )
        }
      >
        {(images.data?.host_images?.length ?? 0) === 0 ? (
          <p className="small muted">{t('vmimages.hostEmpty')}</p>
        ) : (
          <div className="table-wrap">
            <DataTable<VMHostImage>
              dataSource={images.data?.host_images ?? []}
              columns={hostColumns(t, canEdit, (img) => setCreatingHost(img), deleteHostImage)}
              rowKey="path"
              tableLayout="auto"
            />
          </div>
        )}
      </Card>

      {/* Сети — рядом с образами: и то, и другое нужно, чтобы машина
          поднялась, и искать их в разных местах незачем. */}
      <VMNetworksCard me={me} />

      <Card title={t('vmimages.templatesTitle')} subtitle={t('vmimages.templatesHint')}>
        {(templates.data?.templates ?? []).length === 0 ? (
          <p className="small muted">{t('vmimages.templatesEmpty')}</p>
        ) : (
          <div className="col" style={{ gap: '0.3rem' }}>
            {(templates.data?.templates ?? []).map((tpl) => {
              const spec = parseSpec(tpl.spec)
              return (
                <div key={tpl.id} className="row spread">
                  <span className="small">
                    <strong>{tpl.name}</strong>
                    <span className="muted">
                      {' '}
                      {t('vmimages.templateSummary', {
                        image: spec.image_id,
                        vcpus: spec.vcpus,
                        memory: spec.memory_mb,
                        disk: spec.disk_gb,
                      })}
                    </span>
                  </span>
                  {canEdit && (
                    <RowAction
                      action="delete"
                      label={t('common.delete')}
                      danger
                      onClick={async () => {
                        if (!(await confirmAction(t('vmimages.confirmDeleteTemplate', { name: tpl.name })))) return
                        await api(`/vm/templates/${tpl.id}`, { method: 'DELETE' })
                        templates.reload()
                      }}
                    />
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Card>

      {adding && (
        <AddImageModal
          onClose={() => setAdding(false)}
          onStarted={(job) => {
            setAdding(false)
            setOpenJob(job)
            images.reload()
          }}
          onUploaded={() => {
            setAdding(false)
            images.reload()
          }}
        />
      )}

      {creatingHost && (
        <CreateVMModal
          image={{
            id: 'host:' + creatingHost.name,
            name: creatingHost.name,
            os: '',
            arch: '',
            file_name: creatingHost.name,
            custom: true,
          }}
          warning={
            creatingHost.used_by
              ? t(creatingHost.running ? 'vmimages.hostBusyRunning' : 'vmimages.hostBusy', {
                  vm: creatingHost.used_by,
                })
              : undefined
          }
          templates={templates.data?.templates ?? []}
          onSaved={() => templates.reload()}
          onKey={setGeneratedKey}
          onClose={() => setCreatingHost(null)}
          onStarted={(job) => {
            setCreatingHost(null)
            setOpenJob(job)
          }}
        />
      )}

      {creating && (
        <CreateVMModal
          image={creating}
          templates={templates.data?.templates ?? []}
          onSaved={() => templates.reload()}
          onKey={setGeneratedKey}
          onClose={() => setCreating(null)}
          onStarted={(job) => {
            setCreating(null)
            setOpenJob(job)
          }}
        />
      )}

      {generatedKey && (
        <Modal title={t('vmimages.keyTitle')} onClose={() => setGeneratedKey(null)} maskClosable={false} width={720}>
          <Banner kind="warn">{t('vmimages.keyOnce')}</Banner>
          <pre className="diff mono" style={{ maxHeight: '18rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
            {generatedKey}
          </pre>
          <div className="row" style={{ gap: '0.5rem' }}>
            <Button
              onClick={() => {
                void navigator.clipboard?.writeText(generatedKey)
              }}
            >
              {t('common.copy')}
            </Button>
            <Button type="primary" onClick={() => setGeneratedKey(null)}>
              {t('vmimages.keySaved')}
            </Button>
          </div>
        </Modal>
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
  warning,
  templates,
  onSaved,
  onClose,
  onStarted,
  onKey,
}: {
  image: VMImage
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
  const [name, setName] = useState('')
  const [user, setUser] = useState('deploy')
  const [sshKey, setSSHKey] = useState('')
  const [diskGB, setDiskGB] = useState(20)
  const [memoryMB, setMemoryMB] = useState(2048)
  const [vcpus, setVCPUs] = useState(2)
  // Сеть по умолчанию — первая существующая. Ставить «default» вслепую
  // нельзя: на минимальной установке libvirt её нет, и машина падала бы
  // на запуске. Если сетей нет вовсе, имя можно ввести — задание заведёт
  // её само.
  const [network, setNetwork] = useState('')
  const [bridge, setBridge] = useState('')
  // Сети хоста: выбирать из списка вернее, чем вписывать имя наугад —
  // ошибка в нём выясняется только при запуске машины.
  const nets = useApi<{ networks: VMNetwork[] }>('/vm/networks', 60_000)
  const known = nets.data?.networks ?? []
  const chosenNetwork = network || known[0]?.name || 'default' 
  const [autostart, setAutostart] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
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
    if (spec.bridge) setBridge(spec.bridge)
    if (spec.network) setNetwork(spec.network)
  }

  async function saveTemplate() {
    const name = templateName.trim()
    if (!name) return
    setError(null)
    try {
      await api('/vm/templates', {
        method: 'POST',
        body: {
          name,
          spec: {
            image_id: image.id,
            disk_gb: diskGB,
            memory_mb: memoryMB,
            vcpus,
            user,
            ssh_key: sshKey.trim(),
            network: bridge.trim() ? '' : chosenNetwork,
            bridge: bridge.trim(),
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
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number; private_key?: string; public_key?: string }>('/vm/create', {
        method: 'POST',
        body: {
          name,
          image_id: image.id,
          disk_gb: diskGB,
          memory_mb: memoryMB,
          vcpus,
          network: bridge.trim() ? '' : network,
          bridge: bridge.trim(),
          user,
          ssh_key: sshKey.trim(),
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
    <Modal title={t('vmimages.createTitle', { image: image.name })} onClose={onClose} width={720}>
      <ErrorNote error={error} />
      {warning && <Banner kind="warn">{warning}</Banner>}
      {!sshKey.trim() && <Banner kind="info">{t('vmimages.keyWillBeGenerated')}</Banner>}

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
            value={bridge ? '__bridge__' : chosenNetwork}
            onChange={(v: string) => {
              if (v === '__bridge__') {
                setBridge('br0')
                return
              }
              setBridge('')
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
        <Button type="primary" loading={busy} disabled={!name.trim()} onClick={() => void create()}>
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
    </Modal>
  )
}

/** Описание шаблона хранится строкой JSON: набор полей меняется вместе с
 * формой, и разбирать его строго типизированно здесь незачем. */
function parseSpec(raw: string): Partial<VMSpec> {
  try {
    return JSON.parse(raw) as Partial<VMSpec>
  } catch {
    return {}
  }
}

/**
 * Свой образ: по ссылке или файлом с компьютера.
 *
 * По ссылке качает то же задание, что и каталожные образы — с докачкой и
 * проверкой суммы, если её задали. Файл идёт прямо в тело запроса: образ
 * весит сотни мегабайт, и разбирать такую форму в памяти незачем.
 */
function AddImageModal({
  onClose,
  onStarted,
  onUploaded,
}: {
  onClose: () => void
  onStarted: (job: Job) => void
  onUploaded: () => void
}) {
  const { t } = useTranslation()
  const [url, setURL] = useState('')
  const [fileName, setFileName] = useState('')
  const [checksum, setChecksum] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [uploading, setUploading] = useState<number | null>(null)

  async function fetchByURL() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>('/vm/images/download', {
        method: 'POST',
        body: { url: url.trim(), file_name: fileName.trim(), checksum: checksum.trim() },
      })
      onStarted(await api<Job>(`/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Загрузка идёт XMLHttpRequest, а не fetch: только он показывает, сколько
  // отправлено, а на семистах мегабайтах полоса — не украшение.
  function upload(file: File) {
    setError(null)
    setUploading(0)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api/vm/images/upload?name=${encodeURIComponent(file.name)}`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setUploading(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      setUploading(null)
      if (xhr.status === 200) {
        onUploaded()
        return
      }
      try {
        setError((JSON.parse(xhr.responseText) as { error?: string }).error ?? `код ${xhr.status}`)
      } catch {
        setError(`код ${xhr.status}`)
      }
    }
    xhr.onerror = () => {
      setUploading(null)
      setError(t('vmimages.uploadFailed'))
    }
    xhr.send(file)
  }

  return (
    <Modal title={t('vmimages.addOwnTitle')} onClose={onClose} width={720}>
      <p className="small muted">{t('vmimages.addOwnBody')}</p>
      <ErrorNote error={error} />

      <Card title={t('vmimages.byURL')} subtitle={t('vmimages.byURLHint')}>
        <div className="col" style={{ gap: '0.5rem' }}>
          <label>
            {t('vmimages.url')}
            <Input value={url} onChange={(e) => setURL(e.target.value)} placeholder="https://…/image.qcow2" />
          </label>
          <div className="grid grid-2">
            <label>
              {t('vmimages.fileName')}
              <Input
                value={fileName}
                onChange={(e) => setFileName(e.target.value)}
                placeholder={t('vmimages.fileNameAuto')}
              />
            </label>
            <label>
              {t('vmimages.checksum')}
              <Input value={checksum} onChange={(e) => setChecksum(e.target.value)} placeholder="sha256" />
            </label>
          </div>
          <span>
            <Button type="primary" loading={busy} disabled={!url.trim()} onClick={() => void fetchByURL()}>
              {t('vmimages.fetch')}
            </Button>
          </span>
        </div>
      </Card>

      <Card title={t('vmimages.byFile')} subtitle={t('vmimages.byFileHint')}>
        {uploading !== null ? (
          <p className="small">{t('vmimages.uploading', { percent: uploading })}</p>
        ) : (
          <input
            type="file"
            accept=".qcow2,.img,.raw"
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) upload(file)
            }}
          />
        )}
      </Card>
    </Modal>
  )
}

/** Колонки списка файлов каталога дисков libvirt. */
function hostColumns(
  t: (key: string, opts?: Record<string, unknown>) => string,
  canEdit: boolean,
  onCreate: (img: VMHostImage) => void,
  onDelete: (img: VMHostImage) => void,
): TableColumnsType<VMHostImage> {
  return [
    {
      title: t('vmimages.colFile'),
      key: 'name',
      render: (_, img) => (
        <div style={{ minWidth: '14rem' }}>
          <strong className="mono">{img.name}</strong>
          <div className="small muted">{formatBytes(img.size)}</div>
        </div>
      ),
    },
    {
      title: t('vmimages.colUsedBy'),
      key: 'used_by',
      render: (_, img) =>
        img.used_by ? (
          <span className="nowrap">
            <Tag color={img.running ? 'processing' : 'default'}>{img.used_by}</Tag>
            {img.running && <span className="small muted">{t('vmimages.hostRunning')}</span>}
          </span>
        ) : (
          <span className="small muted">{t('vmimages.hostFree')}</span>
        ),
    },
    {
      title: '',
      key: 'actions',
      className: 'nowrap',
      render: (_, img) =>
        canEdit && (
          <div className="row row-nowrap">
            <RowAction action="create" label={t('vmimages.createVM')} onClick={() => onCreate(img)} />
            <RowAction action="delete" label={t('common.delete')} danger onClick={() => onDelete(img)} />
          </div>
        ),
    },
  ]
}
