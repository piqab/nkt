import { useMemo, useState } from 'react'
import { Button, Checkbox, Popconfirm, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, Loading, formatDateTime } from '../components/ui'
import { formatBytes } from '../components/charts'
import { DataTable } from '../components/DataTable'

export interface DockerImage {
  id: string
  tags: string[]
  size: number
  created: string
  in_use: boolean
  used_by?: string[]
  dangling: boolean
}

type Outcome = { ref: string; ok: boolean; error?: string; path?: string }

/**
 * Docker images, with a multi-select for the two things worth doing to
 * several at once: removing them and saving them to a tar archive.
 *
 * An image a container is running from is marked as such and cannot be
 * removed without the force flag — Docker refuses it anyway, and saying so
 * up front beats offering an action that will fail.
 */
export default function Images({ me }: { me: Me }) {
  const { t } = useTranslation()
  const images = useApi<{ images: DockerImage[]; backup_dir: string }>('/images', 30_000)
  const [selected, setSelected] = useState<string[]>([])
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null)
  const [note, setNote] = useState<string | null>(null)

  const canAct = me.is_admin && me.allow_mutations

  /** Prefer a tag over the bare id: it is what the operator recognises, and
   * Docker accepts either. */
  function refOf(image: DockerImage): string {
    return image.tags[0] ?? image.id
  }

  async function run(path: string, body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    setOutcomes(null)
    setNote(null)
    try {
      const res = await api<{ results?: Outcome[]; reclaimed?: number; backup_dir?: string }>(path, {
        method: 'POST',
        body,
      })
      if (res.results) setOutcomes(res.results)
      if (typeof res.reclaimed === 'number') {
        setNote(t('images.reclaimed', { size: formatBytes(res.reclaimed) }))
      }
      setSelected([])
      await images.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const selectedImages = useMemo(
    () => (images.data?.images ?? []).filter((i) => selected.includes(i.id)),
    [images.data, selected],
  )
  const selectedInUse = selectedImages.filter((i) => i.in_use).length

  const columns: TableColumnsType<DockerImage> = [
    {
      title: t('images.colName'),
      key: 'name',
      render: (_, image) => (
        <div className="col">
          {image.tags.length > 0 ? (
            image.tags.map((tag) => (
              <code className="mono" key={tag}>
                {tag}
              </code>
            ))
          ) : (
            <Tag>{t('images.untagged')}</Tag>
          )}
          <span className="small secondary mono">{image.id.replace('sha256:', '').slice(0, 12)}</span>
        </div>
      ),
    },
    {
      title: t('images.colSize'),
      key: 'size',
      align: 'right',
      sorter: (a, b) => a.size - b.size,
      render: (_, image) => <span className="num">{formatBytes(image.size)}</span>,
    },
    {
      title: t('images.colCreated'),
      key: 'created',
      render: (_, image) => <span className="small nowrap">{formatDateTime(image.created)}</span>,
    },
    {
      title: t('images.colState'),
      key: 'state',
      render: (_, image) =>
        image.in_use ? (
          <Tag color="green">{t('images.inUse', { names: (image.used_by ?? []).join(', ') })}</Tag>
        ) : image.dangling ? (
          <Tag color="orange">{t('images.dangling')}</Tag>
        ) : (
          <Tag>{t('images.unused')}</Tag>
        ),
    },
  ]

  if (images.loading && !images.data) return <Loading />

  return (
    <>
      <ErrorNote error={images.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {note && <Banner kind="info">{note}</Banner>}

      {outcomes && (
        <div className="col" style={{ marginBottom: '0.85rem' }}>
          {outcomes.map((o) => (
            <Banner key={o.ref} kind={o.ok ? 'info' : 'error'}>
              <code className="mono">{o.ref}</code>
              {o.ok ? (o.path ? ` → ${o.path}` : ` — ${t('images.done')}`) : ` — ${o.error}`}
            </Banner>
          ))}
        </div>
      )}

      <Card
        title={t('images.title')}
        subtitle={t('images.count', { count: images.data?.images.length ?? 0 })}
        actions={
          canAct && (
            <div className="row" style={{ flexWrap: 'wrap', gap: '0.5rem' }}>
              <Checkbox checked={force} onChange={(e) => setForce(e.target.checked)}>
                {t('images.force')}
              </Checkbox>
              <Button
                disabled={selected.length === 0 || busy}
                loading={busy}
                onClick={() => run('/images/save', { refs: selectedImages.map(refOf) })}
              >
                {t('images.save', { count: selected.length })}
              </Button>
              <Popconfirm
                title={t('images.removeConfirmTitle')}
                description={
                  selectedInUse > 0 && !force
                    ? t('images.removeConfirmInUse', { count: selectedInUse })
                    : t('images.removeConfirm', { count: selected.length })
                }
                okText={t('images.remove', { count: selected.length })}
                disabled={selected.length === 0 || busy}
                onConfirm={() => run('/images/remove', { refs: selectedImages.map(refOf), force })}
              >
                <Button size="small" danger disabled={selected.length === 0 || busy}>
                  {t('images.remove', { count: selected.length })}
                </Button>
              </Popconfirm>
              <Popconfirm
                title={t('images.pruneConfirmTitle')}
                description={t('images.pruneConfirm')}
                onConfirm={() => run('/images/prune', {})}
              >
                <Button size="small" disabled={busy}>
                  {t('images.prune')}
                </Button>
              </Popconfirm>
            </div>
          )
        }
      >
        {images.data?.backup_dir && (
          <p className="small secondary">
            {t('images.backupDir')} <code className="mono">{images.data.backup_dir}</code>
          </p>
        )}
        <div className="table-wrap">
          <DataTable<DockerImage>             dataSource={images.data?.images ?? []}
            rowKey="id"
            columns={columns}
            rowSelection={
              canAct
                ? {
                    selectedRowKeys: selected,
                    onChange: (keys) => setSelected(keys as string[]),
                  }
                : undefined
            }
          />
        </div>
      </Card>
    </>
  )
}
