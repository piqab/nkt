import { useState } from 'react'
import { Button, Checkbox, Input, Radio } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, apiURL, qs, useApi } from '../api'
import type { Job } from '../types'
import { JobLogModal } from '../pages/Jobs'
import { Banner, Modal, formatDateTime } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'

export type BackupKind = 'vm' | 'docker' | 'podman' | 'compose' | 'lxd'

interface Entry {
  kind: BackupKind
  name: string
  file: string
  path: string
  size: number
  created: string
}

/**
 * Бэкапы одного объекта — машины libvirt, контейнера Docker/Podman или
 * compose-стека: список архивов на хосте, «создать» (фоновое задание с
 * журналом), скачать, удалить, восстановить — поверх или копией под
 * новым именем. Что внутри архива и как он собирается — internal/backup.
 */
export function BackupModal({
  kind,
  name,
  projectDir,
  canControl,
  onClose,
  onRestored,
}: {
  kind: BackupKind
  name: string
  /** compose: каталог проекта (где compose-файл). */
  projectDir?: string
  canControl: boolean
  onClose: () => void
  onRestored?: () => void
}) {
  const { t } = useTranslation()
  const list = useApi<{ backups: Entry[]; root: string }>(`/backups${qs({ kind, name })}`)
  const [job, setJob] = useState<Job | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [withImages, setWithImages] = useState(false)
  const [restoring, setRestoring] = useState<Entry | null>(null)

  async function startJob(path: string, body: unknown) {
    setError(null)
    try {
      const res = await api<{ job_id: number }>(path, { method: 'POST', body })
      setJob(await api<Job>(`/jobs/${res.job_id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function remove(e: Entry) {
    if (!(await confirmAction(t('backups.confirmDelete', { file: e.file })))) return
    try {
      await api('/backups/delete', { method: 'POST', body: { path: e.path } })
      list.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Modal title={t('backups.title', { name })} onClose={onClose} width={900}>
      <p className="small muted" style={{ marginTop: 0 }}>
        {t(`backups.hint.${kind}`)} {list.data?.root && <span className="mono">{list.data.root}</span>}
      </p>
      {error && <Banner kind="error">{error}</Banner>}
      {canControl && (
        <div className="row" style={{ gap: '0.75rem', alignItems: 'center', marginBottom: '0.6rem' }}>
          <Button type="primary" onClick={() => void startJob('/backups', { kind, name, project_dir: projectDir, include_images: withImages })}>
            {t('backups.create')}
          </Button>
          {kind === 'compose' && (
            <Checkbox checked={withImages} onChange={(e) => setWithImages(e.target.checked)}>
              {t('backups.withImages')}
            </Checkbox>
          )}
        </div>
      )}
      {(list.data?.backups.length ?? 0) === 0 ? (
        <div className="chart-empty">{list.loading ? t('backups.loading') : t('backups.empty')}</div>
      ) : (
        <div className="table-wrap">
          <DataTable<Entry>
            dataSource={list.data!.backups}
            rowKey="path"
            columns={[
              { title: t('backups.colFile'), key: 'file', render: (_, e) => <span className="mono small">{e.file}</span> },
              { title: t('backups.colCreated'), key: 'created', render: (_, e) => <span className="small nowrap">{formatDateTime(e.created)}</span> },
              { title: t('backups.colSize'), key: 'size', align: 'right', render: (_, e) => <span className="num small">{formatBytes(e.size)}</span> },
              {
                title: '',
                key: 'actions',
                render: (_, e) => (
                  <div className="row row-nowrap">
                    {canControl && (
                      <RowAction action="download" label={t('backups.download')} onClick={() => window.open(apiURL(`/backups/download${qs({ path: e.path })}`), '_blank')} />
                    )}
                    {canControl && <RowAction action="history" label={t('backups.restore')} onClick={() => setRestoring(e)} />}
                    {canControl && <RowAction action="delete" label={t('common.delete')} danger onClick={() => void remove(e)} />}
                  </div>
                ),
              },
            ]}
          />
        </div>
      )}
      {restoring && (
        <RestoreDialog
          entry={restoring}
          onClose={() => setRestoring(null)}
          onStart={(newName) => {
            setRestoring(null)
            void startJob('/backups/restore', { path: restoring.path, new_name: newName })
          }}
        />
      )}
      {job && (
        <JobLogModal
          job={job}
          onClose={() => setJob(null)}
          onDone={() => {
            list.reload()
            onRestored?.()
          }}
        />
      )}
    </Modal>
  )
}

/** Восстановить поверх или копией под новым именем. Поверх — только
 * если объект остановлен (машина) или будет пересоздан (контейнер). */
function RestoreDialog({ entry, onClose, onStart }: { entry: Entry; onClose: () => void; onStart: (newName: string) => void }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'copy' | 'over'>('copy')
  const [newName, setNewName] = useState(`${entry.name}-restored`)
  const valid = mode === 'over' || /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(newName)
  return (
    <Modal title={t('backups.restoreTitle', { file: entry.file })} onClose={onClose} width={620} maskClosable={false}>
      <Radio.Group value={mode} onChange={(e) => setMode(e.target.value)} style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
        <Radio value="copy">{t('backups.restoreCopy')}</Radio>
        {mode === 'copy' && <Input value={newName} onChange={(e) => setNewName(e.target.value)} style={{ marginLeft: '1.5rem', width: 'calc(100% - 1.5rem)' }} />}
        <Radio value="over">{t('backups.restoreOver', { name: entry.name })}</Radio>
      </Radio.Group>
      <p className="small muted">{t(`backups.restoreHint.${entry.kind}`)}</p>
      <div className="row" style={{ gap: '0.5rem' }}>
        <Button type="primary" danger={mode === 'over'} disabled={!valid} onClick={() => onStart(mode === 'over' ? '' : newName)}>
          {t('backups.restoreStart')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}
