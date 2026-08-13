import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Button, Table, Tag, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { FirewallPolicy, Me, Outage, Overview, ServiceUnit, SourceStatus } from '../types'
import { StatTile, formatNumber } from '../components/charts'
import { Banner, Card, ErrorNote, Loading, SeverityBadge, StateBadge, formatDateTime, formatRelative } from '../components/ui'
import UpdateModal from '../components/UpdateModal'

const serviceColumns: TableColumnsType<ServiceUnit> = [
  {
    title: 'Сервис',
    key: 'name',
    render: (_, s) => (
      <>
        <strong>{s.name}</strong>
        <div className="small muted">{s.description}</div>
      </>
    ),
  },
  { title: 'Состояние', key: 'state', render: (_, s) => <StateBadge state={s.active_state} /> },
  { title: 'Автозапуск', key: 'enabled', render: (_, s) => <span className="small">{s.enabled || '—'}</span> },
  { title: 'PID', key: 'main_pid', align: 'right', render: (_, s) => <span className="num small">{s.main_pid || '—'}</span> },
]

const firewallPolicyColumns: TableColumnsType<FirewallPolicy> = [
  { title: 'Цепочка', key: 'chain', render: (_, p) => <span className="mono">{p.backend}/{p.chain}</span> },
  {
    title: 'Политика',
    key: 'policy',
    render: (_, p) => (
      <>
        <StateBadge state={p.policy === 'DROP' || p.policy === 'REJECT' ? 'active' : 'inactive'} />
        <span className="mono small" style={{ marginLeft: 6 }}>
          {p.policy}
        </span>
      </>
    ),
  },
  { title: 'Пакетов', key: 'packets', align: 'right', render: (_, p) => <span className="num">{formatNumber(p.packets)}</span> },
]

const outageColumns: TableColumnsType<Outage> = [
  { title: 'Ресурс', dataIndex: 'label', key: 'label' },
  { title: 'Начало', key: 'start', render: (_, o) => <span className="small nowrap">{formatDateTime(o.start)}</span> },
  { title: 'Проверок', dataIndex: 'checks', key: 'checks', align: 'right' },
  { title: 'Ошибка', key: 'error', render: (_, o) => <span className="small mono">{o.error}</span> },
]

const sourceColumns: TableColumnsType<SourceStatus> = [
  { title: 'Источник', dataIndex: 'name', key: 'name' },
  {
    title: 'Статус',
    key: 'status',
    render: (_, s) => (
      <>
        <StateBadge state={s.error ? 'failed' : s.available ? 'active' : 'inactive'} />
        {s.error && <div className="small muted">{s.error}</div>}
        {s.warnings?.length ? <div className="small muted">предупреждений: {s.warnings.length}</div> : null}
      </>
    ),
  },
  { title: 'Версия', key: 'version', render: (_, s) => <span className="small mono">{s.version || '—'}</span> },
  { title: 'мс', dataIndex: 'duration_ms', key: 'duration_ms', align: 'right' },
]

export default function OverviewPage({ me }: { me: Me }) {
  const { data, error, loading, reload } = useApi<Overview>('/overview', 60_000)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [updating, setUpdating] = useState(false)

  async function rescan() {
    setBusy(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      reload()
      setNotice('Хост пересканирован.')
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (loading && !data) return <Loading what="обзор хоста" />
  if (error && !data) return <ErrorNote error={error} />
  if (!data) return null

  const findings = data.findings
  const worst = (findings.critical ?? 0) + (findings.high ?? 0)
  const av = data.availability
  const pkgSource = data.sources.find((s) => s.name === 'packages')
  const pkgAvailable = pkgSource?.available ?? false
  const pkgUpdates = data.package_updates?.packages ?? []
  const canUpdate = me.is_admin && me.allow_mutations && pkgAvailable

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>{data.host.hostname}</h1>
          <p>
            {data.host.os} · ядро {data.host.kernel || '—'} · последний скан{' '}
            {formatRelative(data.scanned)} ({data.scan_ms} мс)
          </p>
        </div>
        <div className="row">
          {canUpdate && pkgUpdates.length > 0 && (
            <Button
              type="primary"
              onClick={() => {
                if (
                  window.confirm(
                    `Запустить apt-get upgrade на этом хосте (${pkgUpdates.length} пакетов)? ` +
                      'Подтверждение apt (Y/n) нужно будет дать в открывшемся окне.',
                  )
                ) {
                  setUpdating(true)
                }
              }}
            >
              обновить ({pkgUpdates.length})
            </Button>
          )}
          {me.is_admin && (
            <Button onClick={rescan} loading={busy}>
              {busy ? 'Сканирую…' : 'Пересканировать'}
            </Button>
          )}
        </div>
      </div>

      {notice && <Banner kind="info">{notice}</Banner>}
      {data.host.notes?.map((note) => (
        <Banner key={note} kind="info">
          {note}
        </Banner>
      ))}
      {data.package_updates?.reboot_required && (
        <Banner kind="warn">
          Требуется перезагрузка хоста — обновление (не обязательно запущенное отсюда) уже
          применено и ждёт рестарта, например после обновления ядра.
        </Banner>
      )}

      <div className="grid grid-4">
        <StatTile
          label="Проблемы требуют внимания"
          value={formatNumber(worst)}
          note={`критичных ${findings.critical ?? 0}, высоких ${findings.high ?? 0}`}
          tone={worst > 0 ? 'critical' : 'good'}
        />
        <StatTile
          label="Слушателей объявлено"
          value={formatNumber(data.counts.endpoints ?? 0)}
          note={`из них публичных ${data.counts.endpoints_public ?? 0}, с TLS ${data.counts.endpoints_tls ?? 0}`}
        />
        <StatTile
          label="Контейнеры"
          value={`${data.counts.containers_running ?? 0} / ${data.counts.containers ?? 0}`}
          note={`описано в compose: ${data.counts.containers_declared ?? 0}`}
          tone={
            (data.counts.containers ?? 0) > (data.counts.containers_running ?? 0) ? 'warning' : undefined
          }
        />
        <StatTile
          label="Доступность за 24 ч"
          value={`${av.avg_uptime.toFixed(1)}%`}
          note={`целей ${av.targets}: сейчас доступно ${av.up}, недоступно ${av.down}`}
          tone={av.down > 0 ? 'warning' : 'good'}
        />
        {pkgAvailable && (
          <StatTile
            label="Обновления пакетов"
            value={formatNumber(pkgUpdates.length)}
            note={data.package_updates?.reboot_required ? 'нужна перезагрузка' : undefined}
            tone={pkgUpdates.length > 0 ? 'warning' : 'good'}
          />
        )}
      </div>

      <div className="grid grid-2">
        <Card
          title="Что сломано"
          subtitle="Отсортировано по серьёзности. Полный список — на вкладке «Проблемы»."
          actions={<Link to="/findings">все проблемы →</Link>}
        >
          {data.top_findings.length === 0 ? (
            <div className="chart-empty">Проблем не найдено.</div>
          ) : (
            <div className="col">
              {data.top_findings.map((f) => (
                <div key={f.id} style={{ borderBottom: '1px solid var(--gridline)', paddingBottom: '0.5rem' }}>
                  <div className="row" style={{ gap: '0.5rem' }}>
                    <SeverityBadge severity={f.severity} />
                    <strong>{f.title}</strong>
                  </div>
                  <div className="small secondary">{f.detail}</div>
                  {f.object && <Tag>{f.object}</Tag>}
                </div>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card title="Сервисы" actions={<Link to="/services">управление →</Link>}>
            <div className="table-wrap">
              <Table<ServiceUnit> dataSource={data.services} columns={serviceColumns} rowKey="name" pagination={false} size="small" />
            </div>
          </Card>

          <Card title="Firewall" actions={<Link to="/firewall">правила →</Link>}>
            <div className="row">
              <StateBadge state={data.firewall.ufw_active ? 'active' : 'inactive'} />
              <span className="small secondary">ufw · {data.firewall.ufw_policy || 'политика не прочитана'}</span>
            </div>
            <div className="table-wrap" style={{ marginTop: '0.6rem' }}>
              <Table<FirewallPolicy>
                dataSource={(data.firewall.policies ?? []).filter((p) => p.table === 'filter')}
                columns={firewallPolicyColumns}
                rowKey={(p) => `${p.backend}/${p.chain}`}
                pagination={false}
                size="small"
              />
            </div>
          </Card>
        </div>
      </div>

      <div className="grid grid-2">
        <Card title="Последние простои" actions={<Link to="/availability">расписание доступности →</Link>}>
          {av.outages.length === 0 ? (
            <div className="chart-empty">За сутки простоев не зафиксировано.</div>
          ) : (
            <div className="table-wrap">
              <Table<Outage> dataSource={av.outages} columns={outageColumns} rowKey={(_, i) => i ?? 0} pagination={false} size="small" />
            </div>
          )}
        </Card>

        <Card title="Источники данных" subtitle="Что удалось прочитать при последнем скане">
          <div className="table-wrap">
            <Table<SourceStatus> dataSource={data.sources} columns={sourceColumns} rowKey="name" pagination={false} size="small" />
          </div>
        </Card>
      </div>

      {updating && (
        <UpdateModal
          packages={pkgUpdates}
          onClose={() => {
            setUpdating(false)
            reload()
          }}
        />
      )}
    </>
  )
}
