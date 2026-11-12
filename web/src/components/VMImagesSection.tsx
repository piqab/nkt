import { useEffect, useState } from 'react'
import { beginUploadJob, jobOutlivedTransfer, sendFile } from './uploadJob'
import { Button, Input, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, apiURL, useApi } from '../api'
import type { Job, Me, VMImage, VMImageLocal, VMTemplate, VMTool, VMHostImage } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { JobLogModal } from '../pages/Jobs'
import VMNetworksCard from './VMNetworksCard'
import { VMCreateImageModal, parseSpec } from './VMCreateImageForm'
import { BulkDeleteBar } from './useDeletions'
import { SaveOutlined } from '@ant-design/icons'
import { msg, tx, type Msg } from '../msg'

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
export default function VMImagesSection({ me, reloadKey = 0 }: { me: Me; reloadKey?: number }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  // Удаление — быстрое, без задания; пока идёт, кнопки удаления заблокированы.
  const [delBusy, setDelBusy] = useState<string | null>(null)
  const [selImg, setSelImg] = useState<string[]>([])
  const [selHost, setSelHost] = useState<string[]>([])
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
  // Машину удалили вместе с дисками — список файлов перестроить.
  useEffect(() => {
    if (reloadKey > 0) void images.reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- по сигналу страницы
  }, [reloadKey])
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
    setDelBusy(`host:${img.path}`)
    try {
      await api('/vm/images/host-delete', { method: 'POST', body: { name: img.name } })
      await images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setDelBusy(null)
    }
  }

  // Удалить выбранное подряд; ошибки собираются, остальное удаляется.
  async function deleteMany(kind: 'img' | 'host', labels: string[], dels: (() => Promise<unknown>)[], question: string) {
    if (dels.length === 0) return
    if (!(await confirmAction(question, { danger: true }))) return
    setError(null)
    setDelBusy(`${kind}:bulk`)
    const failed: string[] = []
    for (let i = 0; i < dels.length; i++) {
      try {
        await dels[i]()
      } catch (err) {
        failed.push(`${labels[i]}: ${err instanceof Error ? err.message : String(err)}`)
      }
    }
    setDelBusy(null)
    if (kind === 'img') setSelImg([])
    else setSelHost([])
    if (failed.length > 0) setError(failed.join('\n'))
    await images.reload()
  }
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [creating, setCreating] = useState<VMImage | null>(null)
  const [generatedKey, setGeneratedKey] = useState<string | null>(null)
  const templates = useApi<{ templates: VMTemplate[] }>('/vm/templates', 60_000)
  const [error, setError] = useState<Msg | null>(null)

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
            {canEdit && l?.downloaded && l.path && (
              <RowAction
                icon={<SaveOutlined />}
                label={t('vmimages.toComputer')}
                onClick={() => {
                  window.location.href = apiURL(`/vm/images/file/download?where=library&name=${encodeURIComponent(l.path?.split('/').pop() ?? '')}`)
                }}
              />
            )}
            {canEdit && (l?.downloaded || l?.partial) && (
              <RowAction
                action="delete"
                label={t('common.delete')}
                danger
                disabled={delBusy !== null}
                loading={delBusy === `img:${img.id}`}
                onClick={async () => {
                  if (!(await confirmAction(t('vmimages.confirmDelete', { name: img.name })))) return
                  setDelBusy(`img:${img.id}`)
                  try {
                    await api('/vm/images/delete', { method: 'POST', body: { image_id: img.id } })
                    await images.reload()
                  } catch (err) {
                    setError(err instanceof Error ? err.message : String(err))
                  } finally {
                    setDelBusy(null)
                  }
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

      <ErrorNote error={msg(error)} />
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
            <BulkDeleteBar
              count={selImg.length}
              disabled={delBusy !== null}
              onClear={() => setSelImg([])}
              onDelete={() => {
                const list = allImages.filter((img) => selImg.includes(img.id))
                void deleteMany(
                  'img',
                  list.map((img) => img.name),
                  list.map((img) => () => api('/vm/images/delete', { method: 'POST', body: { image_id: img.id } })),
                  t('bulk.confirmDelete', { count: list.length, names: list.map((img) => img.name).join(', ') }),
                )
              }}
            />
            <DataTable<VMImage>
              dataSource={allImages}
              columns={columns}
              rowKey="id"
              tableLayout="auto"
              rowSelection={
                canEdit
                  ? {
                      selectedRowKeys: selImg,
                      onChange: (k) => setSelImg(k as string[]),
                      getCheckboxProps: (img) => {
                        const l = local.get(img.id)
                        return { disabled: delBusy !== null || !(l?.downloaded || l?.partial) }
                      },
                    }
                  : undefined
              }
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
            <BulkDeleteBar
              count={selHost.length}
              disabled={delBusy !== null}
              onClear={() => setSelHost([])}
              onDelete={() => {
                const list = (images.data?.host_images ?? []).filter((img) => selHost.includes(img.path))
                const busy = list.filter((img) => img.used_by).map((img) => `${img.name} (${img.used_by})`)
                void deleteMany(
                  'host',
                  list.map((img) => img.name),
                  list.map((img) => () => api('/vm/images/host-delete', { method: 'POST', body: { name: img.name } })),
                  t('bulk.confirmDelete', { count: list.length, names: list.map((img) => img.name).join(', ') }) +
                    (busy.length > 0 ? ' ' + t('vmimages.bulkBusyDisks', { list: busy.join(', ') }) : ''),
                )
              }}
            />
            <DataTable<VMHostImage>
              dataSource={images.data?.host_images ?? []}
              columns={hostColumns(t, canEdit, (img) => setCreatingHost(img), deleteHostImage, delBusy)}
              rowKey="path"
              tableLayout="auto"
              rowSelection={
                canEdit
                  ? { selectedRowKeys: selHost, onChange: (k) => setSelHost(k as string[]), getCheckboxProps: () => ({ disabled: delBusy !== null }) }
                  : undefined
              }
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
                      disabled={delBusy !== null}
                      loading={delBusy === `tpl:${tpl.id}`}
                      onClick={async () => {
                        if (!(await confirmAction(t('vmimages.confirmDeleteTemplate', { name: tpl.name })))) return
                        setDelBusy(`tpl:${tpl.id}`)
                        try {
                          await api(`/vm/templates/${tpl.id}`, { method: 'DELETE' })
                          await templates.reload()
                        } catch (err) {
                          setError(err instanceof Error ? err.message : String(err))
                        } finally {
                          setDelBusy(null)
                        }
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
        <VMCreateImageModal
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
        <VMCreateImageModal
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
  const [error, setError] = useState<Msg | null>(null)
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
  // Загрузка — заданием хоста с первого байта: его журнал открывается
  // сразу (onStarted), хост пишет туда проценты и потом сам переносит
  // образ в каталог дисков libvirt.
  async function upload(file: File) {
    setError(null)
    let begun: Awaited<ReturnType<typeof beginUploadJob>>
    try {
      begun = await beginUploadJob({ target: 'vmimage', name: file.name, size: file.size })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return
    }
    if (!begun) {
      setError(tx('upload.oldHost'))
      legacyUpload(file)
      return
    }
    onStarted(begun.job)
    setUploading(0)
    try {
      await sendFile('POST', `/vm/images/upload?upload=${begun.token}`, file, setUploading)
      onUploaded()
    } catch (err) {
      if (await jobOutlivedTransfer(begun.job.id)) {
        onUploaded()
        return
      }
      setError(err instanceof Error && err.message !== 'network' ? err.message : tx('vmimages.uploadFailed'))
    } finally {
      setUploading(null)
    }
  }

  /** Старый хост без /uploads/begin: образ одним запросом. */
  function legacyUpload(file: File) {
    setUploading(0)
    const xhr = new XMLHttpRequest()
    // apiURL, а не строка «/api/…»: через хаб путь хоста получает префикс
    // /hosts/{id}, без него запрос уходил в сам хаб и получал «неизвестный
    // метод API».
    xhr.open('POST', apiURL(`/vm/images/upload?name=${encodeURIComponent(file.name)}`))
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
        setError((JSON.parse(xhr.responseText) as { error?: string }).error ?? t('common.httpCode', { code: xhr.status }))
      } catch {
        setError(tx('common.httpCode', { code: xhr.status }))
      }
    }
    xhr.onerror = () => {
      setUploading(null)
      setError(tx('vmimages.uploadFailed'))
    }
    xhr.send(file)
  }

  return (
    <Modal title={t('vmimages.addOwnTitle')} onClose={onClose} width={720}>
      <p className="small muted">{t('vmimages.addOwnBody')}</p>
      <ErrorNote error={msg(error)} />

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
              if (file) void upload(file)
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
  delBusy: string | null = null,
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
            <RowAction
              icon={<SaveOutlined />}
              label={t('vmimages.toComputer')}
              onClick={() => {
                window.location.href = apiURL(`/vm/images/file/download?where=host&name=${encodeURIComponent(img.name)}`)
              }}
            />
            <RowAction action="delete" label={t('common.delete')} danger disabled={delBusy !== null} loading={delBusy === `host:${img.path}`} onClick={() => onDelete(img)} />
          </div>
        ),
    },
  ]
}
