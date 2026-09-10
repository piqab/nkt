import { useState } from 'react'
import { Button, Progress, Segmented, Table, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import { formatBytes } from '../components/charts'

interface Filesystem {
  device: string
  type: string
  mount_point: string
  size: number
  used: number
  available: number
  use_percent: number
  pseudo: boolean
}

interface BlockDevice {
  name: string
  path: string
  type: string
  size: number
  model?: string
  serial?: string
  transport?: string
  fstype?: string
  label?: string
  mount_points?: string[]
  rotational: boolean
  children?: BlockDevice[]
}

interface Swap {
  name: string
  type: string
  size: number
  used: number
}

interface DiskOverview {
  filesystems: Filesystem[]
  devices: BlockDevice[]
  swap: Swap[]
  errors?: string[]
}

interface DirEntry {
  path: string
  size: number
}

/** Цвет полосы занятости. Пороги — те же, по которым место кончается на
 * практике: до 75% беспокоиться не о чем, после 90% пора действовать. */
function usageColor(percent: number): string {
  if (percent >= 90) return 'var(--status-critical)'
  if (percent >= 75) return 'var(--status-warning)'
  return 'var(--status-good)'
}

export default function Disks() {
  const { t } = useTranslation()
  const disks = useApi<DiskOverview>('/disks', 60_000)
  // Псевдофайловые системы (tmpfs, overlay, squashfs от snap) на обычной
  // машине занимают две трети списка и место на диске не расходуют —
  // поэтому по умолчанию скрыты, но доступны переключателем.
  const [kind, setKind] = useState<'real' | 'all'>('real')

  const [usagePath, setUsagePath] = useState('/')
  const [usage, setUsage] = useState<DirEntry[] | null>(null)
  const [usageBusy, setUsageBusy] = useState(false)
  const [usageError, setUsageError] = useState<string | null>(null)

  async function scanUsage(path: string) {
    setUsageBusy(true)
    setUsageError(null)
    try {
      const res = await api<{ entries: DirEntry[] }>(`/disks/usage${qs({ path })}`)
      setUsagePath(path)
      setUsage(res.entries)
    } catch (err) {
      setUsageError(err instanceof Error ? err.message : String(err))
    } finally {
      setUsageBusy(false)
    }
  }

  const all = disks.data?.filesystems ?? []
  const filesystems = kind === 'all' ? all : all.filter((f) => !f.pseudo)

  const fsColumns: TableColumnsType<Filesystem> = [
    {
      title: t('disks.colMount'),
      key: 'mount',
      render: (_, f) => (
        <div className="col">
          <code className="mono">{f.mount_point}</code>
          <span className="small muted mono">
            {f.device} · {f.type}
            {f.pseudo && ` · ${t('disks.pseudo')}`}
          </span>
        </div>
      ),
    },
    {
      title: t('disks.colUsage'),
      key: 'usage',
      width: '18rem',
      sorter: (a, b) => a.use_percent - b.use_percent,
      defaultSortOrder: 'descend',
      render: (_, f) => (
        <div>
          <Progress
            percent={Math.round(f.use_percent)}
            size="small"
            strokeColor={usageColor(f.use_percent)}
            showInfo={false}
          />
          <span className="small">
            {t('disks.usedOf', { used: formatBytes(f.used), size: formatBytes(f.size) })} ·{' '}
            {f.use_percent.toFixed(1)}%
          </span>
        </div>
      ),
    },
    {
      title: t('disks.colFree'),
      key: 'free',
      align: 'right',
      sorter: (a, b) => a.available - b.available,
      render: (_, f) => <span className="num">{formatBytes(f.available)}</span>,
    },
    {
      title: '',
      key: 'actions',
      width: '10rem',
      render: (_, f) => (
        <Button size="small" onClick={() => scanUsage(f.mount_point)} disabled={usageBusy}>
          {t('disks.whatTakesSpace')}
        </Button>
      ),
    },
  ]

  const deviceColumns: TableColumnsType<BlockDevice> = [
    {
      title: t('disks.colDevice'),
      key: 'device',
      render: (_, d) => (
        <div className="col">
          <code className="mono">{d.path}</code>
          <span className="small muted">
            {d.type}
            {d.model && ` · ${d.model}`}
            {d.transport && ` · ${d.transport}`}
            {!d.rotational && d.type === 'disk' && ' · SSD'}
          </span>
        </div>
      ),
    },
    {
      title: t('disks.colSize'),
      key: 'size',
      align: 'right',
      render: (_, d) => <span className="num">{formatBytes(d.size)}</span>,
    },
    {
      title: t('disks.colFs'),
      key: 'fs',
      render: (_, d) => (
        <span className="small mono">
          {d.fstype ?? '—'}
          {d.label && ` (${d.label})`}
        </span>
      ),
    },
    {
      title: t('disks.colMountPoints'),
      key: 'mounts',
      render: (_, d) =>
        d.mount_points && d.mount_points.length > 0 ? (
          <span className="small mono">{d.mount_points.join(', ')}</span>
        ) : (
          <span className="small muted">—</span>
        ),
    },
  ]

  if (disks.loading && !disks.data) return <Loading what={t('disks.title')} />

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('disks.title')}
            <InfoHint>{t('disks.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={disks.error} />
      {disks.data?.errors?.map((e) => (
        <Banner key={e} kind="warn">
          {e}
        </Banner>
      ))}

      <Card
        title={t('disks.filesystems')}
        actions={
          <Segmented
            size="small"
            value={kind}
            onChange={(v) => setKind(v as 'real' | 'all')}
            options={[
              { value: 'real', label: t('disks.realOnly', { count: all.filter((f) => !f.pseudo).length }) },
              { value: 'all', label: t('disks.allFs', { count: all.length }) },
            ]}
          />
        }
      >
        <div className="table-wrap">
          <Table<Filesystem>
            dataSource={filesystems}
            rowKey="mount_point"
            size="small"
            pagination={false}
            columns={fsColumns}
          />
        </div>
      </Card>

      {(usage || usageError) && (
        <Card title={t('disks.usageTitle', { path: usagePath })} subtitle={t('disks.usageHint')}>
          {usageError && <Banner kind="error">{usageError}</Banner>}
          {usageBusy && <Loading what={t('disks.scanning')} />}
          {usage && !usageBusy && (
            <div className="table-wrap">
              <Table<DirEntry>
                dataSource={usage}
                rowKey="path"
                size="small"
                pagination={false}
                columns={[
                  {
                    title: t('disks.colDir'),
                    key: 'path',
                    render: (_, e) => (
                      // Клик уходит глубже: каждый шаг — новый быстрый du
                      // на один уровень, а не обход всего дерева сразу.
                      <Button type="link" size="small" onClick={() => scanUsage(e.path)}>
                        <code className="mono">{e.path}</code>
                      </Button>
                    ),
                  },
                  {
                    title: t('disks.colSize'),
                    key: 'size',
                    align: 'right',
                    render: (_, e) => <span className="num">{formatBytes(e.size)}</span>,
                  },
                ]}
              />
            </div>
          )}
        </Card>
      )}

      {(disks.data?.swap.length ?? 0) > 0 && (
        <Card title={t('disks.swap')} subtitle={t('disks.swapHint')}>
          <div className="table-wrap">
            <Table<Swap>
              dataSource={disks.data?.swap ?? []}
              rowKey="name"
              size="small"
              pagination={false}
              columns={[
                { title: t('disks.colDevice'), key: 'name', render: (_, s) => <code className="mono">{s.name}</code> },
                { title: t('disks.colFs'), key: 'type', render: (_, s) => <Tag>{s.type}</Tag> },
                {
                  title: t('disks.colSize'),
                  key: 'size',
                  align: 'right',
                  render: (_, s) => <span className="num">{formatBytes(s.size)}</span>,
                },
                {
                  title: t('disks.colUsed'),
                  key: 'used',
                  align: 'right',
                  render: (_, s) => <span className="num">{formatBytes(s.used)}</span>,
                },
              ]}
            />
          </div>
        </Card>
      )}

      <Card title={t('disks.devices')} subtitle={t('disks.devicesHint')}>
        <div className="table-wrap">
          <Table<BlockDevice>
            dataSource={disks.data?.devices ?? []}
            rowKey="path"
            size="small"
            pagination={false}
            columns={deviceColumns}
            expandable={{ childrenColumnName: 'children', defaultExpandAllRows: true }}
          />
        </div>
      </Card>
    </>
  )
}
