import { useMemo, useRef, useState } from 'react'
import { Button, Input, Progress, Select, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, apiURL, useApi } from '../api'
import { Banner, Card, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { formatBytes } from './charts'
import { confirmAction } from './confirm'
import { useJobLauncher } from './useJobLauncher'
import { msg, tx, type Msg } from '../msg'

type Engine = 'docker' | 'podman' | 'lxd'

interface Archive {
  name: string
  size: number
  mod_time: string
  kind: Engine
}

/** Что можно сохранить в архив: ссылка образа (Docker, Podman) или
 * отпечаток LXD с псевдонимом. */
export interface SaveSource {
  value: string
  label: string
  alias?: string
}

/**
 * Архивы образов на хосте: «Сохранить» (docker/podman save, lxc image
 * export — заданием), «Скачать» на компьютер, «Загрузить» с компьютера,
 * «Загрузить в Docker/Podman» (load) или «Импортировать» в LXD — тоже
 * заданием, «Удалить».
 */
export function ImageArchivesCard({ engine, canControl, sources }: { engine: Engine; canControl: boolean; sources: SaveSource[] }) {
  const { t } = useTranslation()
  const list = useApi<{ archives: Archive[]; dir: string }>('/images/archives', 30_000)
  const archives = useMemo(() => (list.data?.archives ?? []).filter((a) => a.kind === engine), [list.data, engine])
  const launcher = useJobLauncher(() => list.reload())
  const [error, setError] = useState<Msg | null>(null)
  const [saving, setSaving] = useState(false)
  const [importing, setImporting] = useState<Archive | null>(null)
  const [progress, setProgress] = useState<number | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const engineName = engine === 'docker' ? 'Docker' : engine === 'podman' ? 'Podman' : 'LXD'

  async function start(path: string) {
    setError(null)
    try {
      await launcher.start(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  function upload(file: File) {
    setError(null)
    // Своя приставка — чтобы архив попал в список этого движка.
    const name = file.name.startsWith(`${engine}__`) ? file.name : `${engine}__${file.name}`
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', apiURL(`/images/archives/upload?name=${encodeURIComponent(name)}`))
    setProgress(0)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) setProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      setProgress(null)
      if (xhr.status === 200) {
        list.reload()
        return
      }
      try {
        setError((JSON.parse(xhr.responseText) as { error?: string }).error ?? tx('common.httpCode', { code: xhr.status }))
      } catch {
        setError(tx('common.httpCode', { code: xhr.status }))
      }
    }
    xhr.onerror = () => {
      setProgress(null)
      setError(tx('archives.uploadFailed'))
    }
    xhr.send(file)
  }

  async function remove(a: Archive) {
    if (!(await confirmAction(t('archives.deleteConfirm', { name: a.name }), { okText: t('common.delete') }))) return
    try {
      await api(`/images/archives/${encodeURIComponent(a.name)}`, { method: 'DELETE' })
      list.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Card
      title={t('archives.title', { engine: engineName })}
      subtitle={list.data?.dir ? t('archives.hint', { dir: list.data.dir }) : undefined}
      actions={
        canControl && (
          <Space wrap>
            <Button size="small" onClick={() => setSaving(true)} disabled={sources.length === 0}>
              {engine === 'lxd' ? t('archives.export') : t('archives.save')}
            </Button>
            <Button size="small" onClick={() => fileRef.current?.click()} loading={progress !== null}>
              {t('archives.upload')}
            </Button>
            <input
              ref={fileRef}
              type="file"
              multiple={engine === 'lxd'}
              style={{ display: 'none' }}
              onChange={(e) => {
                for (const f of Array.from(e.target.files ?? [])) upload(f)
                e.target.value = ''
              }}
            />
          </Space>
        )
      }
    >
      {progress !== null && <Progress percent={progress} size="small" />}
      {error && <Banner kind="error">{msg(error)}</Banner>}
      {archives.length === 0 ? (
        <span className="small muted">{t('archives.none')}</span>
      ) : (
        <div className="table-wrap">
          <DataTable<Archive>
            dataSource={archives}
            rowKey="name"
            columns={[
              { title: t('archives.colName'), key: 'name', render: (_, a) => <span className="mono small">{a.name}</span> },
              { title: t('archives.colSize'), key: 'size', align: 'right', render: (_, a) => <span className="small nowrap">{formatBytes(a.size)}</span> },
              { title: t('archives.colWhen'), key: 'when', render: (_, a) => <span className="small nowrap">{formatRelative(a.mod_time)}</span> },
              {
                title: '',
                key: 'act',
                render: (_, a) =>
                  canControl && (
                    <Space size={4} wrap>
                      <Button size="small" href={apiURL(`/images/archives/${encodeURIComponent(a.name)}/download`)} download={a.name}>
                        {t('archives.download')}
                      </Button>
                      {engine === 'lxd' ? (
                        !a.name.endsWith('.root') && (
                          <Button size="small" onClick={() => setImporting(a)}>
                            {t('archives.import')}
                          </Button>
                        )
                      ) : (
                        <Button size="small" onClick={() => void start(`/images/archives/${encodeURIComponent(a.name)}/load?engine=${engine}`)}>
                          {t('archives.load', { engine: engineName })}
                        </Button>
                      )}
                      <Button size="small" danger onClick={() => void remove(a)}>
                        {t('common.delete')}
                      </Button>
                    </Space>
                  ),
              },
            ]}
          />
        </div>
      )}
      {launcher.modal}
      {saving && (
        <SaveModal
          engine={engine}
          sources={sources}
          onClose={() => setSaving(false)}
          onSave={(src) => {
            setSaving(false)
            void start(
              engine === 'lxd'
                ? `/lxd/images/${encodeURIComponent(src.value)}/export${src.alias ? `?alias=${encodeURIComponent(src.alias)}` : ''}`
                : `/images/archives/save?engine=${engine}&ref=${encodeURIComponent(src.value)}`,
            )
          }}
        />
      )}
      {importing && (
        <LXDImportModal
          meta={importing}
          others={archives.filter((x) => x.name !== importing.name)}
          onClose={() => setImporting(null)}
          onImport={(rootfs, alias) => {
            setImporting(null)
            const q = new URLSearchParams({ meta: importing.name })
            if (rootfs) q.set('rootfs', rootfs)
            if (alias) q.set('alias', alias)
            void start(`/lxd/images/import?${q.toString()}`)
          }}
        />
      )}
    </Card>
  )
}

function SaveModal({ engine, sources, onClose, onSave }: { engine: Engine; sources: SaveSource[]; onClose: () => void; onSave: (s: SaveSource) => void }) {
  const { t } = useTranslation()
  const [value, setValue] = useState<string>(sources[0]?.value ?? '')
  const src = sources.find((s) => s.value === value)
  return (
    <Modal title={engine === 'lxd' ? t('archives.export') : t('archives.save')} onClose={onClose} width={560}>
      <p className="small muted">{t(engine === 'lxd' ? 'archives.exportHint' : 'archives.saveHint')}</p>
      <Select showSearch value={value} onChange={setValue} style={{ width: '100%' }} options={sources.map((s) => ({ value: s.value, label: s.label }))} />
      <div style={{ marginTop: '0.8rem' }}>
        <Button type="primary" disabled={!src} onClick={() => src && onSave(src)}>
          {engine === 'lxd' ? t('archives.export') : t('archives.save')}
        </Button>
      </div>
    </Modal>
  )
}

function LXDImportModal({ meta, others, onClose, onImport }: { meta: Archive; others: Archive[]; onClose: () => void; onImport: (rootfs: string, alias: string) => void }) {
  const { t } = useTranslation()
  // Пара «метаданные + корень»: lxc image export кладёт рядом файл .root.
  const guess = others.find((o) => o.name.startsWith(meta.name.replace(/\.tar(\.\w+)?$/, '')) && o.name.endsWith('.root'))
  const [rootfs, setRootfs] = useState<string>(guess?.name ?? '')
  const [alias, setAlias] = useState('')
  return (
    <Modal title={t('archives.import')} onClose={onClose} width={600}>
      <p className="small muted">{t('archives.importHint')}</p>
      <div className="col" style={{ gap: '0.5rem' }}>
        <div>
          <Tag>{t('archives.meta')}</Tag> <span className="mono small">{meta.name}</span>
        </div>
        <label className="col">
          <span className="small">{t('archives.rootfs')}</span>
          <Select
            value={rootfs}
            onChange={setRootfs}
            options={[{ value: '', label: t('archives.rootfsNone') }, ...others.map((o) => ({ value: o.name, label: o.name }))]}
          />
        </label>
        <label className="col">
          <span className="small">{t('archives.alias')}</span>
          <Input value={alias} onChange={(e) => setAlias(e.target.value.trim())} placeholder="debian/12-custom" />
        </label>
        <div>
          <Button type="primary" onClick={() => onImport(rootfs, alias)}>
            {t('archives.import')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
