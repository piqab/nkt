import { useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Radio, Segmented, Select, Space, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost, Job, Me } from '../types'
import { JobLogModal } from '../pages/Jobs'
import { Banner, Card, ErrorNote, Loading, Modal, formatRelative } from './ui'
import { DataTable } from './DataTable'
import { confirmWithOption } from './confirm'

/**
 * «Сайты» в «Выкладках»: домен → проверка DNS и портов с хаба (снаружи) →
 * прокси на хосте (nginx, HAProxy, Caddy; нет ни одного — ставится
 * nginx) → сервис compose-стека или адрес:порт → сертификат certbot
 * --standalone (у Caddy — свой) → проверка HTTPS снаружи. Конфигурация
 * прокси пишется на хосте через «Конфигурации»: с проверкой, откатом и
 * историей версий.
 */

interface DNSReport {
  domain: string
  domain_ips?: string[]
  host_ips?: string[]
  match: boolean
}

interface HTTPSCheck {
  ok: boolean
  status?: number
  error?: string
  cert_days_left?: number
  cert_issuer?: string
  checked_at: string
}

interface Outside {
  dns: DNSReport[]
  ports: Record<string, string>
  lan?: boolean
  https?: HTTPSCheck
}

interface HostPreflight {
  proxies: { name: string; installed: boolean; running: boolean }[]
  holders?: { port: number; process?: string; unit?: string; container?: string }[]
  stacks: { name: string; file: string; error?: string; services: { name: string; ports: { target: number; published: number; host_ip?: string }[]; expose?: number[] }[] }[]
  firewall: { manager?: string; active: boolean; open80: boolean; open443: boolean }
  engine: string
  certbot: boolean
}

interface Site {
  id: number
  domains: string[]
  host_id: number
  host_name: string
  proxy: string
  stack?: string
  service?: string
  container_port?: number
  upstream?: string
  open_firewall: boolean
  status: string
  error?: string
  check?: Outside
  job_id?: number
  updated_at: string
}

const STATUS_COLOR: Record<string, string> = { ok: 'success', failed: 'error', 'setting-up': 'processing' }
const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

function portTag(t: (k: string) => string, v?: string) {
  if (!v) return <Tag>—</Tag>
  const color = v === 'open' ? 'success' : v === 'refused' ? 'blue' : v === 'timeout' ? 'error' : 'warning'
  const tag = <Tag color={color}>{t(`sites.port.${v.startsWith('error') ? 'error' : v}`)}</Tag>
  return v.startsWith('error') ? <Tooltip title={v.replace(/^error: /, '')}>{tag}</Tooltip> : tag
}

export function SitesPanel({ me }: { me: Me }) {
  const { t } = useTranslation()
  const list = useApi<{ sites: Site[] }>('/hub/sites', 15_000)
  const [wizard, setWizard] = useState<Site | 'new' | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function openJob(id?: number) {
    if (!id) return
    try {
      setJob(await api<Job>(`/hosts/local/jobs/${id}`))
    } catch {
      // задание откроется из «Заданий»
    }
  }

  async function act(s: Site, what: 'check' | 'apply' | 'delete') {
    setBusy(s.id)
    setError(null)
    try {
      if (what === 'check') {
        await api(`/hub/sites/${s.id}/check`, { method: 'POST', timeoutMs: 90_000 })
      } else if (what === 'apply') {
        const res = await api<{ job_id: number }>(`/hub/sites/${s.id}/apply`, { method: 'POST', body: { install_proxy: true } })
        void openJob(res.job_id)
      } else {
        const choice = await confirmWithOption(t('sites.deleteConfirm', { name: s.domains[0] }), t('sites.deleteRemoveConfig'), {
          optionHint: t('sites.deleteRemoveHint'),
        })
        if (!choice) return
        await api(`/hub/sites/${s.id}${choice.checked ? '?remove=1' : ''}`, { method: 'DELETE' })
      }
      list.reload()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }

  const sites = list.data?.sites ?? []
  return (
    <Card
      title={t('sites.title')}
      subtitle={t('sites.subtitle')}
      actions={me.is_admin && <Button type="primary" onClick={() => setWizard('new')}>{t('sites.new')}</Button>}
    >
      <ErrorNote error={list.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {list.loading && !list.data ? (
        <Loading what={t('sites.title')} />
      ) : sites.length === 0 ? (
        <p className="small muted">{t('sites.empty')}</p>
      ) : (
        <div className="table-wrap">
          <DataTable<Site>
            dataSource={sites}
            rowKey="id"
            columns={[
              {
                title: t('sites.colSite'),
                key: 'site',
                render: (_, s) => (
                  <div>
                    <a href={`https://${s.domains[0]}/`} target="_blank" rel="noreferrer" className="mono">
                      {s.domains[0]}
                    </a>
                    {s.domains.length > 1 && <div className="small muted mono">{s.domains.slice(1).join(', ')}</div>}
                  </div>
                ),
              },
              { title: t('sites.colHost'), key: 'host', render: (_, s) => <span>{s.host_name}</span> },
              {
                title: t('sites.colTarget'),
                key: 'target',
                render: (_, s) => (
                  <span className="small">
                    <Tag>{s.proxy}</Tag>
                    <span className="mono">{s.stack ? `${s.stack}/${s.service}:${s.container_port}` : s.upstream}</span>
                  </span>
                ),
              },
              {
                title: t('sites.colState'),
                key: 'state',
                render: (_, s) => (
                  <Space size={4} wrap>
                    <Tag color={STATUS_COLOR[s.status] ?? 'default'}>{t(`sites.status.${s.status || 'new'}`)}</Tag>
                    {s.error && (
                      <Tooltip title={s.error}>
                        <span className="small" style={{ color: 'var(--status-error)' }}>{t('sites.hasError')}</span>
                      </Tooltip>
                    )}
                  </Space>
                ),
              },
              {
                title: t('sites.colCheck'),
                key: 'check',
                render: (_, s) => {
                  const c = s.check
                  if (!c) return <span className="small muted">—</span>
                  const dnsOK = c.dns?.every((d) => d.match)
                  return (
                    <Space size={4} wrap className="small">
                      <Tag color={dnsOK ? 'success' : 'error'}>DNS</Tag>
                      {c.https?.checked_at && (c.https.ok ? (
                        <Tag color="success">HTTPS {c.https.status}</Tag>
                      ) : (
                        <Tooltip title={c.https.error}>
                          <Tag color="error">HTTPS</Tag>
                        </Tooltip>
                      ))}
                      {c.https?.cert_days_left !== undefined && c.https.ok && (
                        <Tag color={c.https.cert_days_left < 14 ? 'warning' : 'default'}>{t('sites.certDays', { n: c.https.cert_days_left })}</Tag>
                      )}
                      {c.https?.checked_at && <span className="muted">{formatRelative(c.https.checked_at)}</span>}
                    </Space>
                  )
                },
              },
              {
                title: '',
                key: 'actions',
                render: (_, s) => (
                  <Space size={4} wrap>
                    <Button size="small" loading={busy === s.id} onClick={() => void act(s, 'check')}>
                      {t('sites.check')}
                    </Button>
                    {s.job_id ? (
                      <Button size="small" type="link" onClick={() => void openJob(s.job_id)}>
                        {t('sites.log')}
                      </Button>
                    ) : null}
                    {me.is_admin && (
                      <>
                        <Button size="small" onClick={() => setWizard(s)}>
                          {t('sites.edit')}
                        </Button>
                        <Button size="small" onClick={() => void act(s, 'apply')}>
                          {t('sites.reapply')}
                        </Button>
                        <Button size="small" danger onClick={() => void act(s, 'delete')}>
                          {t('sites.delete')}
                        </Button>
                      </>
                    )}
                  </Space>
                ),
              },
            ]}
          />
        </div>
      )}
      {wizard && (
        <SiteWizard
          site={wizard === 'new' ? null : wizard}
          onClose={() => setWizard(null)}
          onStarted={(jobID) => {
            setWizard(null)
            list.reload()
            void openJob(jobID)
          }}
        />
      )}
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} onDone={() => list.reload()} />}
    </Card>
  )
}

/** Мастер сайта: хост и имена → проверка → прокси и цель → настройка. */
function SiteWizard({ site, onClose, onStarted }: { site: Site | null; onClose: () => void; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [hostID, setHostID] = useState<number | null>(site?.host_id ?? null)
  const [domains, setDomains] = useState(site?.domains.join(', ') ?? '')
  const [pre, setPre] = useState<{ outside?: Outside; host?: HostPreflight; host_error?: string } | null>(null)
  const [checking, setChecking] = useState(false)
  const [proxy, setProxy] = useState<string | null>(site?.proxy ?? null)
  const [targetKind, setTargetKind] = useState<'stack' | 'addr'>(site && !site.stack ? 'addr' : 'stack')
  const [stack, setStack] = useState(site?.stack ?? '')
  const [service, setService] = useState(site?.service ?? '')
  const [port, setPort] = useState<number | null>(site?.container_port ?? null)
  const [upstream, setUpstream] = useState(site?.upstream ?? '127.0.0.1:8080')
  const [openFW, setOpenFW] = useState(site?.open_firewall ?? true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function check() {
    if (hostID === null) return
    setChecking(true)
    setError(null)
    try {
      const res = await api<{ outside?: Outside; host?: HostPreflight; host_error?: string }>('/hub/sites/preflight', {
        method: 'POST',
        body: { host_id: hostID, domains: domains.split(/[\s,]+/).filter(Boolean) },
        timeoutMs: 90_000,
      })
      setPre(res)
      const px = res.host?.proxies ?? []
      if (!proxy) {
        setProxy(px.find((p) => p.running)?.name ?? px.find((p) => p.installed)?.name ?? 'nginx')
      }
      if (res.host && !(res.host.firewall.open80 && res.host.firewall.open443)) setOpenFW(true)
    } catch (err) {
      setError(errText(err))
    } finally {
      setChecking(false)
    }
  }

  const stacks = pre?.host?.stacks ?? []
  const services = stacks.find((s) => s.name === stack)?.services ?? []
  const svc = services.find((s) => s.name === service)
  const portOptions = useMemo(() => {
    const set = new Set<number>()
    for (const p of svc?.ports ?? []) set.add(p.target)
    for (const p of svc?.expose ?? []) set.add(p)
    return [...set]
  }, [svc])
  const chosen = pre?.host?.proxies.find((p) => p.name === proxy)
  const noneInstalled = pre?.host ? !pre.host.proxies.some((p) => p.installed) : false
  const holders80 = (pre?.host?.holders ?? []).filter((h) => h.container)

  async function start() {
    if (hostID === null || !proxy) return
    setBusy(true)
    setError(null)
    try {
      const body = {
        domains: domains.split(/[\s,]+/).filter(Boolean),
        host_id: hostID,
        proxy,
        stack: targetKind === 'stack' ? stack : '',
        service: targetKind === 'stack' ? service : '',
        container_port: targetKind === 'stack' ? port ?? 0 : 0,
        upstream: targetKind === 'addr' ? upstream : '',
        open_firewall: openFW,
        install_proxy: true,
      }
      const res = await api<{ job_id: number }>(site ? `/hub/sites/${site.id}` : '/hub/sites', { method: site ? 'PUT' : 'POST', body })
      onStarted(res.job_id)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  const ready = hostID !== null && !!proxy && domains.trim() !== '' && (targetKind === 'addr' ? !!upstream : !!stack && !!service && !!port)

  return (
    <Modal title={site ? t('sites.editTitle', { name: site.domains[0] }) : t('sites.newTitle')} onClose={onClose} width={860} maskClosable={false}>
      <div className="col">
        <p className="small muted">{t('sites.wizardHint')}</p>
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <label style={{ minWidth: 200 }}>
            {t('sites.host')}
            <Select
              value={hostID ?? undefined}
              onChange={(v: number) => {
                setHostID(v)
                setPre(null)
              }}
              options={(hosts.data ?? []).filter((h) => h.status === 'online').map((h) => ({ value: h.id, label: h.name }))}
              placeholder={t('sites.hostPlaceholder')}
            />
          </label>
          <label style={{ flex: 1, minWidth: 260 }}>
            {t('sites.domains')}
            <Input value={domains} onChange={(e) => setDomains(e.target.value)} placeholder="app.example.com, www.app.example.com" className="mono" />
          </label>
          <Button loading={checking} disabled={hostID === null || !domains.trim()} onClick={() => void check()}>
            {t('sites.checkBtn')}
          </Button>
        </div>

        {pre?.outside && (
          <div className="col" style={{ gap: '0.2rem' }}>
            <strong className="small">{t('sites.outside')}</strong>
            {pre.outside.dns.map((d) => (
              <div key={d.domain} className="small">
                <Tag color={d.match ? 'success' : 'error'}>{d.match ? t('sites.dnsOK') : t('sites.dnsBad')}</Tag>
                <span className="mono">{d.domain}</span> → <span className="mono">{(d.domain_ips ?? []).join(', ') || '—'}</span>
                {!d.match && <span className="muted"> · {t('sites.hostIPs', { ips: (d.host_ips ?? []).join(', ') })}</span>}
              </div>
            ))}
            <div className="small">
              80: {portTag(t, pre.outside.ports['80'])} 443: {portTag(t, pre.outside.ports['443'])}
              {pre.outside.ports['80'] === 'timeout' && <span style={{ color: 'var(--status-error)' }}> {t('sites.port80Blocked')}</span>}
            </div>
            {pre.outside.lan && <div className="small muted">{t('sites.lan')}</div>}
          </div>
        )}
        {pre?.host_error && <Banner kind="error">{pre.host_error}</Banner>}
        {pre?.host && (
          <>
            <div className="col" style={{ gap: '0.3rem' }}>
              <strong className="small">{t('sites.proxy')}</strong>
              <Radio.Group value={proxy} onChange={(e) => setProxy(e.target.value)}>
                {pre.host.proxies.map((p) => (
                  <Radio key={p.name} value={p.name}>
                    {p.name}{' '}
                    {p.running ? <Tag color="success">{t('sites.running')}</Tag> : p.installed ? <Tag>{t('sites.installed')}</Tag> : <Tag color="orange">{t('sites.willInstall')}</Tag>}
                  </Radio>
                ))}
              </Radio.Group>
              {noneInstalled && <div className="small muted">{t('sites.noneInstalled')}</div>}
              {proxy === 'caddy' && <div className="small muted">{t('sites.caddyNote')}</div>}
              {chosen && !chosen.installed && !noneInstalled && <div className="small muted">{t('sites.installNote', { name: proxy })}</div>}
              {holders80.length > 0 && (
                <Banner kind="warn">
                  {t('sites.containerHolds', { list: holders80.map((h) => `${h.port} (${h.process ?? '?'})`).join(', ') })}
                </Banner>
              )}
              {!pre.host.certbot && proxy !== 'caddy' && <div className="small" style={{ color: 'var(--status-error)' }}>{t('sites.noCertbot')}</div>}
            </div>
            <div className="col" style={{ gap: '0.3rem' }}>
              <strong className="small">{t('sites.target')}</strong>
              <Segmented
                size="small"
                value={targetKind}
                onChange={(v) => setTargetKind(v as 'stack' | 'addr')}
                options={[
                  { value: 'stack', label: t('sites.targetStack') },
                  { value: 'addr', label: t('sites.targetAddr') },
                ]}
              />
              {targetKind === 'stack' ? (
                stacks.length === 0 ? (
                  <div className="small muted">{t('sites.noStacks')}</div>
                ) : (
                  <div className="row" style={{ gap: '0.5rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
                    <label style={{ minWidth: 160 }}>
                      {t('sites.stack')}
                      <Select value={stack || undefined} onChange={(v: string) => { setStack(v); setService(''); setPort(null) }} options={stacks.map((s) => ({ value: s.name, label: s.name }))} />
                    </label>
                    <label style={{ minWidth: 160 }}>
                      {t('sites.service')}
                      <Select
                        value={service || undefined}
                        onChange={(v: string) => {
                          setService(v)
                          const sv = services.find((s) => s.name === v)
                          setPort(sv?.ports[0]?.target ?? sv?.expose?.[0] ?? null)
                        }}
                        options={services.map((s) => ({ value: s.name, label: s.name }))}
                      />
                    </label>
                    <label>
                      {t('sites.containerPort')}
                      <InputNumber min={1} max={65535} value={port} onChange={(v) => setPort(v)} />
                    </label>
                    {portOptions.length > 0 && <span className="small muted">{t('sites.portsKnown', { list: portOptions.join(', ') })}</span>}
                  </div>
                )
              ) : (
                <label style={{ maxWidth: 260 }}>
                  {t('sites.upstream')}
                  <Input value={upstream} onChange={(e) => setUpstream(e.target.value)} className="mono" />
                </label>
              )}
              {targetKind === 'stack' && <div className="small muted">{t('sites.publishNote')}</div>}
            </div>
            <Checkbox checked={openFW} onChange={(e) => setOpenFW(e.target.checked)}>
              {t('sites.openFirewall')}{' '}
              {pre.host.firewall.active ? (
                <span className="small muted">
                  ({pre.host.firewall.manager}: 80 {pre.host.firewall.open80 ? '✓' : '✗'}, 443 {pre.host.firewall.open443 ? '✓' : '✗'})
                </span>
              ) : (
                <span className="small muted">({t('sites.firewallOff')})</span>
              )}
            </Checkbox>
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row">
          <Button type="primary" loading={busy} disabled={!ready || !pre?.host} onClick={() => void start()}>
            {t('sites.setup')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}
