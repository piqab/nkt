import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { ConfigVersion, Me, WriteResult } from '../types'
import { Banner, DiffView, formatDateTime } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'

/**
 * История версий файла конфигурации — для окон правки (XML машины и
 * др.): список версий, дифф выбранной с текущим файлом, откат. Та же
 * таблица, что на странице «Конфигурации», но самодостаточная.
 */
export function VersionHistory({ path, me, apply, onChanged }: { path: string; me: Me; apply?: boolean; onChanged?: () => void }) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: ConfigVersion[] }>(`/configs/versions${qs({ path })}`)
  const [diff, setDiff] = useState<{ id: number; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const list = versions.data?.versions ?? []
  const currentId = list[0]?.id

  async function showDiff(id: number) {
    if (diff?.id === id) return setDiff(null)
    const res = await api<{ diff: string }>(`/configs/versions/${id}/diff`)
    setDiff({ id, text: res.diff || t('configs.noDiff') })
  }

  async function rollback(id: number) {
    if (!(await confirmAction(t('configs.confirmRollback', { id })))) return
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<WriteResult>(`/configs/versions/${id}/rollback`, { method: 'POST', body: { apply: !!apply } })
      setNotice({ kind: res.rolled_back ? 'error' : 'info', text: res.message })
      versions.reload()
      onChanged?.()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  if (list.length === 0) return <div className="chart-empty">{t('configs.emptyHistory')}</div>
  return (
    <>
      {notice && <Banner kind={notice.kind}>{notice.text}</Banner>}
      <div className="table-wrap">
        <DataTable<ConfigVersion>
          dataSource={list}
          rowKey="id"
          columns={[
            { title: '#', dataIndex: 'id', key: 'id', align: 'right' },
            { title: t('configs.colTs'), key: 'ts', render: (_, v) => <span className="small nowrap">{formatDateTime(v.ts)}</span> },
            { title: t('configs.colAuthor'), dataIndex: 'author', key: 'author', className: 'small' },
            { title: t('configs.colAction'), dataIndex: 'action', key: 'action', className: 'small' },
            { title: t('configs.colNote'), dataIndex: 'note', key: 'note', className: 'small secondary' },
            { title: t('configs.colSize'), key: 'size', align: 'right', render: (_, v) => <span className="num small">{formatBytes(v.size)}</span> },
            {
              title: '',
              key: 'actions',
              render: (_, v) => (
                <div className="row row-nowrap">
                  {v.id === currentId ? (
                    <span className="small secondary nowrap">{t('configs.isCurrent')}</span>
                  ) : (
                    <RowAction action="details" label={diff?.id === v.id ? t('configs.hide') : t('configs.diff')} onClick={() => void showDiff(v.id)} />
                  )}
                  {me.is_admin && me.allow_mutations && v.id !== currentId && (
                    <RowAction action="history" label={t('configs.rollback')} loading={busy} onClick={() => void rollback(v.id)} />
                  )}
                </div>
              ),
            },
          ]}
        />
      </div>
      {diff && (
        <>
          <div className="small secondary" style={{ marginTop: '0.75rem' }}>
            {t('configs.diffCaption', { id: diff.id })}
          </div>
          <DiffView text={diff.text} />
        </>
      )}
    </>
  )
}
