import { useMemo, useState } from 'react'
import { OsTitle } from './OsIcon'
import { Button, Input, Tabs, Tag, Tooltip } from 'antd'
import { CopyOutlined, EyeOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import { Banner, ErrorNote, Loading, Modal, formatDateTime } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { confirmAction } from './confirm'

interface InspectEnv {
  name: string
  value: string
  masked?: boolean
  origin: 'container' | 'override' | 'image'
}

interface Inspect {
  name: string
  id: string
  created: string
  image: string
  image_id: string
  image_outdated: boolean
  state: string
  started_at: string
  finished_at: string
  exit_code: number
  health: string
  restart_count: number
  restart_policy: string
  restart_retries: number
  memory_limit: number
  nano_cpus: number
  entrypoint?: string[]
  cmd?: string[]
  user?: string
  working_dir?: string
  compose_project?: string
  compose_service?: string
  env: InspectEnv[]
  revealed: boolean
  ports: { container: string; host_ip?: string; host_port?: string }[]
  mounts: { type: string; source: string; name?: string; destination: string; rw: boolean }[]
  networks: { name: string; ip?: string; gateway?: string; mac?: string; aliases?: string[] }[]
  labels: Record<string, string>
  raw: unknown
}

const ORIGIN_COLOR: Record<string, string> = { container: 'blue', override: 'gold', image: 'default' }

/**
 * «Инспект» контейнера Docker: переменные окружения с источником (заданы
 * для контейнера, переопределяют образ или пришли из образа), образ,
 * команда, состояние, порты, тома, сети, метки и полный inspect. Значения
 * переменных — секреты: сервер отдаёт их только администратору по
 * «Показать значения», и это пишется в журнал действий.
 */
export function ContainerInspectModal({ name, admin, onClose }: { name: string; admin: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const [reveal, setReveal] = useState(false)
  const data = useApi<Inspect>(`/containers/${encodeURIComponent(name)}/inspect${reveal ? '?reveal=1' : ''}`)
  const [q, setQ] = useState('')
  const d = data.data
  const env = useMemo(() => {
    const s = q.trim().toLowerCase()
    return (d?.env ?? []).filter((e) => !s || e.name.toLowerCase().includes(s) || (d?.revealed && e.value.toLowerCase().includes(s)))
  }, [d, q])

  async function doReveal() {
    if (!(await confirmAction(t('docker.inspectRevealConfirm', { name })))) return
    setReveal(true)
  }
  const copy = (text: string) => void navigator.clipboard?.writeText(text)
  const cmd = d ? [...(d.entrypoint ?? []), ...(d.cmd ?? [])].join(' ') : ''
  const row = (label: string, value: React.ReactNode) =>
    value === undefined || value === null || value === '' ? null : (
      <div className="row" style={{ gap: '0.6rem', alignItems: 'baseline' }}>
        <span className="small muted" style={{ minWidth: '9rem' }}>
          {label}
        </span>
        <span className="small" style={{ wordBreak: 'break-all' }}>
          {value}
        </span>
      </div>
    )

  return (
    <Modal title={<OsTitle tKey="docker.inspectTitle" kind="docker" name={name} />} onClose={onClose} width={960} sizeKey="container-inspect">
      <ErrorNote error={data.error} />
      {!d ? (
        <Loading />
      ) : (
        <Tabs
          items={[
            {
              key: 'env',
              label: t('docker.inspectEnv', { count: d.env.length }),
              children: (
                <div className="col" style={{ gap: '0.5rem' }}>
                  <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
                    <Input.Search allowClear placeholder={t('docker.inspectSearch')} value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: '18rem' }} />
                    {!d.revealed && admin && (
                      <Button size="small" icon={<EyeOutlined />} onClick={() => void doReveal()}>
                        {t('docker.inspectReveal')}
                      </Button>
                    )}
                    {d.revealed && <Tag color="warning">{t('docker.inspectRevealed')}</Tag>}
                    <span className="small muted">{t('docker.inspectEnvHint')}</span>
                  </div>
                  {!admin && <Banner kind="info">{t('docker.inspectNamesOnly')}</Banner>}
                  <div className="table-wrap">
                    <DataTable<InspectEnv>
                      dataSource={env}
                      rowKey="name"
                      size="small"
                      pagination={env.length > 50 ? { pageSize: 50 } : false}
                      columns={[
                        { title: t('docker.inspectColName'), key: 'name', render: (_, e) => <span className="mono small">{e.name}</span> },
                        {
                          title: t('docker.inspectColValue'),
                          key: 'value',
                          render: (_, e) =>
                            e.masked ? (
                              <span className="muted">••••••</span>
                            ) : (
                              <span className="mono small sensitive-area" style={{ wordBreak: 'break-all' }}>
                                {e.value}
                              </span>
                            ),
                        },
                        {
                          title: t('docker.inspectColOrigin'),
                          key: 'origin',
                          render: (_, e) => (
                            <Tooltip title={t(`docker.inspectOriginHint.${e.origin}`)}>
                              <Tag color={ORIGIN_COLOR[e.origin]}>{t(`docker.inspectOrigin.${e.origin}`)}</Tag>
                            </Tooltip>
                          ),
                        },
                        {
                          title: '',
                          key: 'copy',
                          width: 40,
                          render: (_, e) =>
                            e.masked ? null : (
                              <Tooltip title={t('docker.inspectCopy')}>
                                <Button size="small" type="text" icon={<CopyOutlined />} onClick={() => copy(`${e.name}=${e.value}`)} />
                              </Tooltip>
                            ),
                        },
                      ]}
                    />
                  </div>
                </div>
              ),
            },
            {
              key: 'main',
              label: t('docker.inspectMain'),
              children: (
                <div className="col" style={{ gap: '0.3rem' }}>
                  {d.image_outdated && <Banner kind="warn">{t('docker.inspectImageOutdated', { image: d.image })}</Banner>}
                  {row(t('docker.inspectImage'), <span className="mono">{d.image}</span>)}
                  {row(t('docker.inspectImageId'), <span className="mono">{d.image_id.slice(0, 19)}</span>)}
                  {row(t('docker.inspectState'), `${d.state}${d.health ? ` · ${d.health}` : ''}${d.state !== 'running' ? ` · exit ${d.exit_code}` : ''}`)}
                  {row(t('docker.inspectCreated'), formatDateTime(d.created))}
                  {row(t('docker.inspectStarted'), d.started_at && !d.started_at.startsWith('0001') ? formatDateTime(d.started_at) : '')}
                  {row(t('docker.inspectRestart'), `${d.restart_policy || 'no'}${d.restart_retries ? ` (${d.restart_retries})` : ''} · ${t('docker.inspectRestarts', { count: d.restart_count })}`)}
                  {row(t('docker.inspectCommand'), cmd ? <span className="mono">{cmd}</span> : '')}
                  {row(t('docker.inspectUser'), d.user)}
                  {row(t('docker.inspectWorkdir'), d.working_dir)}
                  {row(t('docker.inspectLimits'), [d.memory_limit ? `${t('docker.inspectMemory')} ${formatBytes(d.memory_limit)}` : '', d.nano_cpus ? `CPU ${d.nano_cpus / 1e9}` : ''].filter(Boolean).join(' · '))}
                  {row(t('docker.inspectCompose'), d.compose_project ? `${d.compose_project} · ${d.compose_service ?? ''}` : '')}
                  {row('ID', <span className="mono">{d.id.slice(0, 12)}</span>)}
                </div>
              ),
            },
            {
              key: 'net',
              label: t('docker.inspectNet'),
              children: (
                <div className="col" style={{ gap: '0.8rem' }}>
                  <div>
                    <strong className="small">{t('docker.inspectPorts')}</strong>
                    {d.ports.length === 0 ? (
                      <div className="small muted">—</div>
                    ) : (
                      d.ports.map((p) => (
                        <div key={p.container + (p.host_ip ?? '') + (p.host_port ?? '')} className="small mono">
                          {p.host_port ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ` : ''}
                          {p.container}
                        </div>
                      ))
                    )}
                  </div>
                  <div>
                    <strong className="small">{t('docker.inspectMounts')}</strong>
                    {d.mounts.length === 0 ? (
                      <div className="small muted">—</div>
                    ) : (
                      d.mounts.map((m) => (
                        <div key={m.destination} className="small">
                          <Tag>{m.type}</Tag>
                          <span className="mono">{m.name || m.source}</span> → <span className="mono">{m.destination}</span> {!m.rw && <Tag>ro</Tag>}
                        </div>
                      ))
                    )}
                  </div>
                  <div>
                    <strong className="small">{t('docker.inspectNetworks')}</strong>
                    {d.networks.map((n) => (
                      <div key={n.name} className="small">
                        <span className="mono">{n.name}</span>
                        {n.ip ? ` · ${n.ip}` : ''}
                        {n.gateway ? ` · ${t('docker.inspectGateway')} ${n.gateway}` : ''}
                        {n.aliases?.length ? <span className="muted"> · {n.aliases.join(', ')}</span> : null}
                      </div>
                    ))}
                  </div>
                </div>
              ),
            },
            {
              key: 'labels',
              label: t('docker.inspectLabels', { count: Object.keys(d.labels).length }),
              children: (
                <div className="col" style={{ gap: '0.15rem' }}>
                  {Object.entries(d.labels)
                    .sort(([a], [b]) => a.localeCompare(b))
                    .map(([k, v]) => (
                      <div key={k} className="small mono" style={{ wordBreak: 'break-all' }}>
                        <span className="muted">{k}</span> = {v}
                      </div>
                    ))}
                </div>
              ),
            },
            {
              key: 'raw',
              label: 'JSON',
              children: (
                <div className="col" style={{ gap: '0.4rem' }}>
                  <div>
                    <Button size="small" icon={<CopyOutlined />} onClick={() => copy(JSON.stringify(d.raw, null, 2))}>
                      {t('docker.inspectCopy')}
                    </Button>
                  </div>
                  <pre className="diff mono small sensitive-area" style={{ maxHeight: '28rem', overflow: 'auto' }}>
                    {JSON.stringify(d.raw, null, 2)}
                  </pre>
                </div>
              ),
            },
          ]}
        />
      )}
    </Modal>
  )
}
