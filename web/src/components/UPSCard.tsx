import { useState } from 'react'
import { Button, Checkbox, InputNumber, Progress, Select, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { Sensitive } from '../privacy'
import { api, qs, useApi } from '../api'
import { Banner, Card, DiffView, Loading, Modal } from './ui'
import { LineChart, type Series } from './charts'
import { useJobLauncher } from './useJobLauncher'
import { unifiedDiff } from './textDiff'
import type { MetricPoint, UPS } from '../types'

// ИБП через NUT: состояние, история, поиск ИБП и следов UPSilon,
// подключение фоновым заданием, пороги оповещений (с диффом и историей).

interface UPSThresholds {
  charge_pct: number
  runtime_min: number
}

interface UPSDetection {
  usb: { vendor: string; product: string; manufacturer?: string; name?: string; driver: string; label: string }[]
  adapters: { vendor: string; product: string; chip: string; port?: string }[]
  serial: string[]
  upsilon: {
    present: boolean
    running: boolean
    procs?: { pid: number; cmd: string }[]
    autostart?: { path: string; kind: string; line?: string }[]
    paths?: string[]
    port?: string
    port_from?: string
  }
  recommended: { driver: string; port: string; reason: string }
  nut_installed: boolean
  nut_configured: boolean
  ups?: UPS[]
  apt_get: boolean
}

function minutes(s: number): string {
  return `${Math.round(s / 60)}`
}

/** Состояние ИБП словами и цветом. */
function upsState(u: UPS, t: (k: string) => string): { text: string; color: string } {
  if (u.error) return { text: t('ups.state.lost'), color: 'error' }
  if (u.on_battery && u.low_battery) return { text: t('ups.state.low'), color: 'error' }
  if (u.on_battery) return { text: t('ups.state.battery'), color: 'warning' }
  return { text: t('ups.state.online'), color: 'success' }
}

export function UPSCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const st = useApi<{ ups: UPS[]; thresholds: UPSThresholds; shutdown: boolean }>('/ups', 30_000)
  const [detect, setDetect] = useState(false)
  const [editTh, setEditTh] = useState(false)
  const list = st.data?.ups ?? []
  const actions = (
    <Space wrap>
      <Button size="small" onClick={() => setDetect(true)}>
        {list.length ? t('ups.findAgain') : t('ups.find')}
      </Button>
      {admin && st.data && (
        <Button size="small" onClick={() => setEditTh(true)}>
          {t('ups.thresholds')}
        </Button>
      )}
    </Space>
  )
  return (
    <Card title={t('ups.title')} subtitle={t('ups.subtitle')} actions={actions}>
      {!st.data && st.loading && <Loading />}
      {st.data && list.length === 0 && <p className="small muted">{t('ups.none')}</p>}
      {list.map((u) => (
        <UPSView key={u.name} u={u} />
      ))}
      {st.data && list.length > 0 && (
        <p className="small muted" style={{ marginBottom: 0 }}>
          {t('ups.thresholdsNow', { charge: st.data.thresholds.charge_pct, runtime: st.data.thresholds.runtime_min })} ·{' '}
          {st.data.shutdown ? t('ups.shutdownOn') : t('ups.shutdownOff')} · {t('ups.configHint')}
        </p>
      )}
      {detect && <DetectModal admin={admin} onClose={() => setDetect(false)} onConnected={() => void st.reload()} />}
      {editTh && st.data && (
        <ThresholdsModal
          current={st.data.thresholds}
          onClose={() => setEditTh(false)}
          onSaved={() => {
            setEditTh(false)
            void st.reload()
          }}
        />
      )}
    </Card>
  )
}

function UPSView({ u }: { u: UPS }) {
  const { t } = useTranslation()
  const s = upsState(u, t)
  const [metric, setMetric] = useState<'charge_pct' | 'load_pct' | 'input_v'>('charge_pct')
  const hist = useApi<{ points: MetricPoint[] }>(
    `/monitor/usage${qs({ source: 'ups', metric, agg: 'avg', since: '24h', granularity: '5m', subjects: u.name })}`,
    120_000,
  )
  const series: Series[] = [{ name: u.name, points: (hist.data?.points ?? []).map((p) => ({ x: p.bucket, y: p.value })) }]
  const facts = [
    [t('ups.model'), [u.manufacturer, u.model].filter(Boolean).join(' ') || '—'],
    [t('ups.driver'), u.driver || '—'],
    [t('ups.input'), u.input_v ? `${u.input_v.toFixed(0)} V` : '—'],
    [t('ups.output'), u.output_v ? `${u.output_v.toFixed(0)} V` : '—'],
    [t('ups.batteryV'), u.battery_v ? `${u.battery_v.toFixed(1)} V` : '—'],
    [t('ups.status'), u.status || '—'],
  ]
  return (
    <div className="col" style={{ gap: '0.5rem', marginBottom: '1rem' }}>
      <Space wrap>
        <strong className="mono">{u.name}</strong>
        <Tag color={s.color}>{s.text}</Tag>
        {u.replace_battery && <Tag color="warning">{t('ups.replaceBattery')}</Tag>}
        {u.overload && <Tag color="error">{t('ups.overload')}</Tag>}
      </Space>
      {u.error ? (
        <Banner kind="error">{t('ups.lostDetail', { error: u.error })}</Banner>
      ) : (
        <div className="row" style={{ gap: '1.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
          {u.has_charge && (
            <div style={{ minWidth: 160 }}>
              <div className="small muted">{t('ups.charge')}</div>
              <Progress percent={Math.round(u.charge_pct ?? 0)} size="small" status={u.on_battery ? 'exception' : 'normal'} />
            </div>
          )}
          <div style={{ minWidth: 160 }}>
            <div className="small muted">{t('ups.load')}</div>
            <Progress percent={Math.round(u.load_pct ?? 0)} size="small" status={u.overload ? 'exception' : 'normal'} />
          </div>
          {u.has_runtime && (
            <div>
              <div className="small muted">{t('ups.runtime')}</div>
              <strong>{t('ups.minutes', { n: minutes(u.runtime_s ?? 0) })}</strong>
            </div>
          )}
          {facts.map(([k, v]) => (
            <div key={k}>
              <div className="small muted">{k}</div>
              <span className="mono small">{v}</span>
            </div>
          ))}
        </div>
      )}
      <Space size={4}>
        {(['charge_pct', 'load_pct', 'input_v'] as const).map((m) => (
          <Button key={m} size="small" type={metric === m ? 'primary' : 'default'} onClick={() => setMetric(m)}>
            {t(`ups.metric.${m}`)}
          </Button>
        ))}
      </Space>
      <LineChart series={series} height={160} yMax={metric === 'input_v' ? undefined : 100} formatValue={(n) => (metric === 'input_v' ? `${n.toFixed(0)} V` : `${n.toFixed(0)}%`)} formatX={(x) => x.slice(11, 16)} area />
    </div>
  )
}

/** «Найти ИБП»: что нашлось, без изменений; отсюда — «Подключить ИБП». */
function DetectModal({ admin, onClose, onConnected }: { admin: boolean; onClose: () => void; onConnected: () => void }) {
  const { t } = useTranslation()
  const d = useApi<UPSDetection>('/ups/detect')
  const [connect, setConnect] = useState(false)
  const data = d.data
  return (
    <Modal title={t('ups.findTitle')} onClose={onClose} width={720}>
      {!data ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <div>
            <strong className="small">{t('ups.foundUSB')}</strong>
            {data.usb.length === 0 && data.adapters.length === 0 ? (
              <p className="small muted">{t('ups.noUSB')}</p>
            ) : (
              <ul className="small">
                {data.usb.map((u) => (
                  <li key={u.vendor + u.product}>
                    <span className="mono">
                      {u.vendor}:{u.product}
                    </span>{' '}
                    {[u.manufacturer, u.name].filter(Boolean).join(' ')} — {u.label}; {t('ups.driverIs', { driver: u.driver })}
                  </li>
                ))}
                {data.adapters.map((a) => (
                  <li key={a.vendor + a.product + (a.port ?? '')}>
                    <span className="mono">
                      {a.vendor}:{a.product}
                    </span>{' '}
                    {t('ups.adapter', { chip: a.chip, port: a.port ?? '?' })}
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="small">
            <strong>{t('ups.serialPorts')}</strong> <span className="mono">{data.serial.length ? data.serial.join(', ') : '—'}</span>
          </div>
          <div className="small">
            <strong>UPSilon:</strong>{' '}
            {!data.upsilon.present ? (
              t('ups.upsilonNone')
            ) : (
              <>
                {data.upsilon.running
                  ? t('ups.upsilonRunningProcs', { procs: (data.upsilon.procs ?? []).map((p) => `${p.cmd} (PID ${p.pid})`).join(', ') })
                  : t('ups.upsilonNotRunning')}
                {(data.upsilon.paths?.length ?? 0) > 0 && <> · {t('ups.upsilonPaths', { paths: data.upsilon.paths!.join(', ') })}</>}
                {data.upsilon.port && <> · {t('ups.upsilonPort', { port: data.upsilon.port, from: data.upsilon.port_from ?? '' })}</>}
                <div>
                  {(data.upsilon.autostart?.length ?? 0) > 0
                    ? t('ups.upsilonAutostart', { list: data.upsilon.autostart!.map((a) => a.path + (a.line ? ` («${a.line}»)` : '')).join('; ') })
                    : data.upsilon.running
                      ? t('ups.upsilonNoAutostart')
                      : null}
                </div>
              </>
            )}
          </div>
          {data.recommended.reason && (
            <div className="small">
              <strong>{t('ups.recommended')}</strong>{' '}
              {t('ups.recommendedValue', { driver: data.recommended.driver, port: data.recommended.port === 'auto' ? 'USB' : data.recommended.port })} —{' '}
              {t(`ups.reason.${data.recommended.reason}`)}
            </div>
          )}
          <div className="small">
            <strong>NUT:</strong> {data.nut_configured ? t('ups.nutConfigured', { n: data.ups?.length ?? 0 }) : data.nut_installed ? t('ups.nutInstalled') : t('ups.nutNone')}
          </div>
          {admin && (
            <Space>
              <Button type="primary" disabled={!data.apt_get && !data.nut_installed} onClick={() => setConnect(true)}>
                {data.nut_configured ? t('ups.reconnect') : t('ups.connect')}
              </Button>
              {!data.apt_get && !data.nut_installed && <span className="small muted">{t('ups.noApt')}</span>}
            </Space>
          )}
        </div>
      )}
      {connect && data && (
        <ConnectModal
          data={data}
          onClose={() => setConnect(false)}
          onDone={() => {
            setConnect(false)
            onConnected()
            void d.reload()
          }}
        />
      )}
    </Modal>
  )
}

const DRIVERS = ['nutdrv_qx', 'usbhid-ups', 'blazer_usb', 'blazer_ser']

/** «Подключить ИБП»: драйвер и порт (по найденному), выключение хоста при
 * садящейся батарее; что будет сделано — списком; запуск — заданием. */
function ConnectModal({ data, onClose, onDone }: { data: UPSDetection; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const usb = data.usb[0]
  const [driver, setDriver] = useState(data.recommended.driver || 'nutdrv_qx')
  const [port, setPort] = useState(data.recommended.port || 'auto')
  const [shutdown, setShutdown] = useState(false)
  const job = useJobLauncher(() => onDone())
  const autostart = (data.upsilon.autostart ?? []).map((a) => a.path)
  const pids = (data.upsilon.procs ?? []).map((p) => p.pid)
  const serialDriver = driver === 'blazer_ser'
  // Порт: у nutdrv_qx — USB (auto) или COM, у blazer_ser — только COM,
  // у USB-драйверов — auto.
  const effPort = serialDriver || driver === 'nutdrv_qx' ? port : 'auto'
  const portOK = !(serialDriver && effPort === 'auto')
  async function start() {
    const p = new URLSearchParams({ driver, port: effPort })
    if (usb && effPort === 'auto') {
      p.set('vendor', usb.vendor)
      p.set('product', usb.product)
    }
    if (shutdown) p.set('shutdown', '1')
    for (const a of autostart) p.append('autostart', a)
    for (const pid of pids) p.append('pid', String(pid))
    await job.start(`/ups/connect/ws?${p}`)
  }
  return (
    <Modal title={t('ups.connectTitle')} onClose={onClose} width={680}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <label>
          {t('ups.driver')}
          <Select
            value={driver}
            onChange={(d) => {
              setDriver(d)
              if (d === 'blazer_ser' && port === 'auto') setPort(data.serial[0] ?? 'auto')
            }}
            options={DRIVERS.map((d) => ({ value: d, label: d }))}
          />
        </label>
        <label>
          {t('ups.port')}
          <Select
            value={effPort}
            disabled={!serialDriver && driver !== 'nutdrv_qx'}
            onChange={setPort}
            options={[...(serialDriver ? [] : [{ value: 'auto', label: t('ups.portUSB') }]), ...data.serial.map((s) => ({ value: s, label: s }))]}
          />
          {!serialDriver && driver !== 'nutdrv_qx' && <span className="small muted">{t('ups.portUSBOnly', { driver })}</span>}
        </label>
        <Checkbox checked={shutdown} onChange={(e) => setShutdown(e.target.checked)}>
          {t('ups.shutdownCheck')}
        </Checkbox>
        <Banner kind="info">
          <div className="small">{t('ups.connectWill')}</div>
          <ul className="small" style={{ margin: '0.3rem 0 0' }}>
            {data.upsilon.running && <li>{t('ups.willStopProcs', { procs: (data.upsilon.procs ?? []).map((p) => p.cmd).join(', ') })}</li>}
            {(data.upsilon.autostart ?? []).map((a) => (
              <li key={a.path}>{a.kind === 'rclocal' || a.kind === 'cron' ? t('ups.willCommentOut', { path: a.path }) : t('ups.willDisableUnit', { path: a.path })}</li>
            ))}
            {data.upsilon.running && !(data.upsilon.autostart?.length ?? 0) && <li>{t('ups.willNoAutostart')}</li>}
            {!data.nut_installed && <li>{t('ups.willInstall')}</li>}
            <li>{t('ups.willConfigure', { driver, port: effPort === 'auto' ? 'USB' : effPort })}</li>
            <li>{t('ups.willTryOthers')}</li>
            <li>{shutdown ? t('ups.willShutdown') : t('ups.willNotShutdown')}</li>
            <li>{t('ups.willRollback')}</li>
          </ul>
        </Banner>
        <Space>
          <Button type="primary" disabled={!portOK} onClick={() => void start()}>
            {t('ups.connect')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
      </div>
      {job.modal}
    </Modal>
  )
}

/** Пороги оповещений: правка с диффом перед записью и историей. */
function ThresholdsModal({ current, onClose, onSaved }: { current: UPSThresholds; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [charge, setCharge] = useState(current.charge_pct)
  const [runtime, setRuntime] = useState(current.runtime_min)
  const [preview, setPreview] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const hist = useApi<{ history: { ts: string; user: string; charge_pct: number; runtime_min: number }[] }>('/ups/thresholds/history')
  const text = (c: number, r: number) => `${t('ups.thCharge')}: ${c}%\n${t('ups.thRuntime')}: ${r} ${t('ups.min')}\n`
  async function save() {
    setBusy(true)
    setError(null)
    try {
      await api('/ups/thresholds', { method: 'PUT', body: { charge_pct: charge, runtime_min: runtime } })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={t('ups.thresholds')} onClose={onClose} width={560}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <p className="small muted">{t('ups.thresholdsHint')}</p>
        <label>
          {t('ups.thCharge')}
          <InputNumber min={5} max={95} value={charge} addonAfter="%" onChange={(v) => setCharge(v ?? current.charge_pct)} />
        </label>
        <label>
          {t('ups.thRuntime')}
          <InputNumber min={1} max={120} value={runtime} addonAfter={t('ups.min')} onChange={(v) => setRuntime(v ?? current.runtime_min)} />
        </label>
        {error && <Banner kind="error">{error}</Banner>}
        <Space>
          <Button type="primary" disabled={charge === current.charge_pct && runtime === current.runtime_min} onClick={() => setPreview(unifiedDiff(text(current.charge_pct, current.runtime_min), text(charge, runtime), t('tokens.saved'), t('tokens.draft')))}>
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </Space>
        {(hist.data?.history.length ?? 0) > 0 && (
          <div className="small">
            <strong>{t('ups.history')}</strong>
            <ul style={{ margin: '0.3rem 0 0' }}>
              {hist.data!.history.map((h) => (
                <li key={h.ts}>
                  {h.ts.slice(0, 16).replace('T', ' ')} · {h.user ? <Sensitive>{h.user}</Sensitive> : '—'} · {t('ups.wasValues', { charge: h.charge_pct, runtime: h.runtime_min })}{' '}
                  <Button
                    size="small"
                    type="link"
                    onClick={() => {
                      setCharge(h.charge_pct)
                      setRuntime(h.runtime_min)
                    }}
                  >
                    {t('ups.restore')}
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
      {preview !== null && (
        <Modal title={t('editModal.changes')} onClose={() => setPreview(null)} width={620}>
          <DiffView text={preview} />
          <Space style={{ marginTop: '0.6rem' }}>
            <Button type="primary" loading={busy} onClick={() => void save()}>
              {t('common.save')}
            </Button>
            <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
          </Space>
        </Modal>
      )}
    </Modal>
  )
}
