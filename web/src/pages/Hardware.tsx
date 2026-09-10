import { Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import { formatBytes } from '../components/charts'
import { DataTable } from '../components/DataTable'

interface Battery {
  name: string
  capacity: number
  status: string
  model?: string
}

interface SensorValue {
  chip: string
  label: string
  celsius: number
  warn?: number
  crit?: number
}

interface Hardware {
  machine: {
    vendor?: string
    product?: string
    board?: string
    bios_version?: string
    bios_date?: string
    virtualization?: string
    uptime_seconds: number
    load_avg?: number[]
  }
  cpu: {
    model?: string
    vendor?: string
    arch?: string
    cpus: number
    cores_per_socket?: number
    threads_per_core?: number
    sockets?: number
    max_mhz?: number
  }
  memory: { total_bytes: number; available_bytes: number; swap_total_bytes: number }
  batteries?: Battery[]
  sensors?: SensorValue[]
  pci?: string[]
  usb?: string[]
  notes?: string[]
}

// Аптайм словами: «10 д 4 ч» читается быстрее, чем 900000 секунд.
function formatUptime(seconds: number, t: ReturnType<typeof useTranslation>['t']): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return t('hardware.uptimeDays', { days, hours })
  if (hours > 0) return t('hardware.uptimeHours', { hours, minutes })
  return t('hardware.uptimeMinutes', { minutes })
}

/** Цвет температуры: до порога предупреждения спокойный, после — тревожный.
 * Пороги приходят от самого датчика (temp*_max / temp*_crit), а не
 * выдумываются здесь: у процессора и у диска они разные. */
function tempColor(v: SensorValue): string {
  if (v.crit && v.celsius >= v.crit) return 'var(--status-critical)'
  if (v.warn && v.celsius >= v.warn) return 'var(--status-warning)'
  return 'var(--status-good)'
}

export default function HardwarePage() {
  const { t } = useTranslation()
  const hw = useApi<Hardware>('/hardware', 60_000)

  if (hw.loading && !hw.data) return <Loading what={t('hardware.title')} />
  const data = hw.data

  const rows: { key: string; value: string }[] = []
  const add = (key: string, value?: string | number | null) => {
    if (value !== undefined && value !== null && String(value) !== '') {
      rows.push({ key, value: String(value) })
    }
  }
  if (data) {
    add(t('hardware.product'), [data.machine.vendor, data.machine.product].filter(Boolean).join(' '))
    add(t('hardware.board'), data.machine.board)
    add(
      t('hardware.bios'),
      [data.machine.bios_version, data.machine.bios_date].filter(Boolean).join(', '),
    )
    add(t('hardware.virtualization'), data.machine.virtualization)
    add(t('hardware.uptime'), formatUptime(data.machine.uptime_seconds, t))
    add(t('hardware.loadAvg'), data.machine.load_avg?.map((n) => n.toFixed(2)).join(' · '))
    add(t('hardware.cpuModel'), data.cpu.model)
    add(
      t('hardware.cpuLayout'),
      data.cpu.cpus
        ? t('hardware.cpuLayoutValue', {
            cpus: data.cpu.cpus,
            cores: data.cpu.cores_per_socket ?? 0,
            threads: data.cpu.threads_per_core ?? 0,
            sockets: data.cpu.sockets ?? 1,
          })
        : '',
    )
    add(t('hardware.arch'), data.cpu.arch)
    add(t('hardware.maxMhz'), data.cpu.max_mhz ? `${Math.round(data.cpu.max_mhz)} МГц` : '')
    add(
      t('hardware.memory'),
      data.memory.total_bytes
        ? t('hardware.memoryValue', {
            total: formatBytes(data.memory.total_bytes),
            available: formatBytes(data.memory.available_bytes),
          })
        : '',
    )
    add(t('hardware.swap'), data.memory.swap_total_bytes ? formatBytes(data.memory.swap_total_bytes) : '')
  }

  const deviceColumns: TableColumnsType<{ line: string }> = [
    { title: '', key: 'line', render: (_, d) => <span className="small mono">{d.line}</span> },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('hardware.title')}
            <InfoHint>{t('hardware.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={hw.error} />

      <Card title={t('hardware.summary')}>
        <div className="table-wrap">
          <DataTable<{ key: string; value: string }>             dataSource={rows}
            rowKey="key"
            showHeader={false}
            columns={[
              { key: 'k', render: (_, r) => <span className="small muted">{r.key}</span>, width: '16rem' },
              { key: 'v', render: (_, r) => <span className="mono small">{r.value}</span> },
            ]}
          />
        </div>
      </Card>

      {(data?.batteries?.length ?? 0) > 0 && (
        <Card title={t('hardware.batteries')}>
          <div className="table-wrap">
            <DataTable<Battery>               dataSource={data?.batteries ?? []}
              rowKey="name"
              columns={[
                { title: t('hardware.colName'), key: 'name', render: (_, b) => <code className="mono">{b.name}</code> },
                {
                  title: t('hardware.colCharge'),
                  key: 'capacity',
                  render: (_, b) => <span className="num">{b.capacity}%</span>,
                },
                { title: t('hardware.colStatus'), key: 'status', render: (_, b) => <Tag>{b.status}</Tag> },
                {
                  title: t('hardware.colModel'),
                  key: 'model',
                  render: (_, b) => <span className="small muted">{b.model ?? '—'}</span>,
                },
              ]}
            />
          </div>
        </Card>
      )}

      {(data?.sensors?.length ?? 0) > 0 && (
        <Card title={t('hardware.sensors')} subtitle={t('hardware.sensorsHint')}>
          <div className="table-wrap">
            <DataTable<SensorValue>               dataSource={data?.sensors ?? []}
              rowKey={(v) => `${v.chip}/${v.label}`}
              columns={[
                { title: t('hardware.colChip'), key: 'chip', render: (_, v) => <span className="small mono">{v.chip}</span> },
                { title: t('hardware.colSensor'), key: 'label', render: (_, v) => <span className="small">{v.label}</span> },
                {
                  title: t('hardware.colTemp'),
                  key: 'temp',
                  align: 'right',
                  sorter: (a, b) => a.celsius - b.celsius,
                  render: (_, v) => (
                    <span className="num" style={{ color: tempColor(v) }}>
                      {v.celsius.toFixed(1)} °C
                    </span>
                  ),
                },
              ]}
            />
          </div>
        </Card>
      )}

      {data?.notes?.map((n) => (
        <Banner key={n} kind="info">
          {n}
        </Banner>
      ))}

      {(data?.pci?.length ?? 0) > 0 && (
        <Card title={t('hardware.pci')} subtitle={t('hardware.count', { count: data?.pci?.length ?? 0 })}>
          <div className="table-wrap">
            <DataTable               dataSource={(data?.pci ?? []).map((line) => ({ line }))}
              rowKey="line"
              pagination={{ pageSize: 20 }}
              showHeader={false}
              columns={deviceColumns}
            />
          </div>
        </Card>
      )}

      {(data?.usb?.length ?? 0) > 0 && (
        <Card title={t('hardware.usb')} subtitle={t('hardware.count', { count: data?.usb?.length ?? 0 })}>
          <div className="table-wrap">
            <DataTable               dataSource={(data?.usb ?? []).map((line) => ({ line }))}
              rowKey="line"
              pagination={{ pageSize: 20 }}
              showHeader={false}
              columns={deviceColumns}
            />
          </div>
        </Card>
      )}
    </>
  )
}
