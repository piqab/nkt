import { useState } from 'react'
import { Button, Checkbox, Input, Select, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost } from '../types'
import { Banner, Card, Modal, formatRelative } from './ui'
import { confirmAction } from './confirm'

interface DNSCheck {
  domain: string
  domain_ips?: string[]
  host_ips?: string[]
  match: boolean
}

/** Адрес хоста годится именем вебхуков, если это DNS-имя, а не IP. */
const hostName = (addr: string) => (/^[a-z0-9.-]+\.[a-z]{2,63}$/i.test(addr) && !/^[\d.]+$/.test(addr) ? addr.toLowerCase() : '')

interface EdgeStatus {
  id: number
  configured: boolean
  enabled: boolean
  address?: string
  domain?: string
  fingerprint?: string
  host_id?: number
  roles: string[]
  connected: boolean
  since?: string
  last_error?: string
}

const ROLES = ['hooks', 'api']

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/** Роли edge — галочками. */
function RolesPicker({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const { t } = useTranslation()
  return (
    <div className="col" style={{ gap: '0.2rem' }}>
      <span className="small">{t('edge.roles')}</span>
      {ROLES.map((r) => (
        <Checkbox key={r} checked={value.includes(r)} onChange={(ev) => onChange(ev.target.checked ? ROLES.filter((x) => x === r || value.includes(x)) : value.filter((x) => x !== r))}>
          {t(`edge.role.${r}`)} <span className="small muted">— {t(`edge.roleHint.${r}`)}</span>
        </Checkbox>
      ))}
    </div>
  )
}

/**
 * nkt-edge — входная точка на VPS: хаб сам держит к нему туннель, наружу
 * открыт только edge. Edge может быть несколько; у каждого роли — вебхуки
 * выкладок и (или) API для токенов. Установка на хост хаба — заданием,
 * или настройка вручную.
 */
export function EdgeCard({ onOpenJob }: { onOpenJob: (id: number) => void }) {
  const { t } = useTranslation()
  const st = useApi<{ edges: EdgeStatus[] }>('/hub/edges', 10_000)
  const [dialog, setDialog] = useState<{ kind: 'install'; edge?: EdgeStatus } | { kind: 'manual'; edge?: EdgeStatus } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const edges = st.data?.edges ?? []
  return (
    <Card
      title="nkt-edge"
      subtitle={t('edge.subtitle')}
      actions={
        <Space>
          <Button size="small" type="primary" onClick={() => setDialog({ kind: 'install' })}>
            {t('edge.install')}
          </Button>
          <Button size="small" onClick={() => setDialog({ kind: 'manual' })}>
            {t('edge.manual')}
          </Button>
        </Space>
      }
    >
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {!st.data ? null : edges.length === 0 ? (
        <p className="small muted">{t('edge.none')}</p>
      ) : (
        <div className="col" style={{ gap: '0.8rem' }}>
          {edges.map((e) => (
            <div key={e.id} className="col small" style={{ gap: '0.3rem', borderTop: '1px solid var(--border)', paddingTop: '0.5rem' }}>
              <div className="row" style={{ gap: '0.4rem', flexWrap: 'wrap', alignItems: 'center' }}>
                <strong className="mono">{e.domain || e.address}</strong>
                {e.connected ? <Tag color="success">{t('edge.connected')}</Tag> : e.enabled ? <Tag color="error">{t('edge.disconnected')}</Tag> : <Tag>{t('edge.disabled')}</Tag>}
                {e.connected && e.since && <span className="muted">{formatRelative(e.since)}</span>}
                {e.roles.map((r) => (
                  <Tag key={r} color={r === 'api' ? 'purple' : 'blue'}>
                    {t(`edge.role.${r}`)}
                  </Tag>
                ))}
                <span style={{ flex: 1 }} />
                <Button size="small" onClick={() => setDialog(e.host_id ? { kind: 'install', edge: e } : { kind: 'manual', edge: e })}>
                  {e.host_id ? t('edge.reinstall') : t('edge.edit')}
                </Button>
                {!!e.host_id && (
                  <Button
                    size="small"
                    danger
                    onClick={async () => {
                      if (!(await confirmAction(t('edge.uninstallConfirm')))) return
                      try {
                        const res = await api<{ job_id: number }>(`/hub/edges/${e.id}/uninstall`, { method: 'POST' })
                        onOpenJob(res.job_id)
                        void st.reload()
                      } catch (err) {
                        setError(errText(err))
                      }
                    }}
                  >
                    {t('edge.uninstall')}
                  </Button>
                )}
                <Button
                  size="small"
                  danger
                  onClick={async () => {
                    if (!(await confirmAction(t('edge.forgetConfirm')))) return
                    try {
                      await api(`/hub/edges/${e.id}`, { method: 'DELETE' })
                      void st.reload()
                    } catch (err) {
                      setError(errText(err))
                    }
                  }}
                >
                  {t('edge.forget')}
                </Button>
              </div>
              {e.domain && e.roles.includes('hooks') && (
                <div>
                  {t('edge.hooksAt')}: <span className="mono">https://{e.domain}/hooks/…</span>
                </div>
              )}
              {e.domain && e.roles.includes('api') && (
                <div>
                  {t('edge.apiAt')}: <span className="mono">https://{e.domain}/api/…</span>
                </div>
              )}
              <div>
                {t('edge.tunnel')}: <span className="mono">{e.address}</span>
              </div>
              {e.fingerprint && (
                <div>
                  {t('edge.fingerprint')}: <span className="mono">{e.fingerprint}</span>
                </div>
              )}
              {!e.connected && e.last_error && <Banner kind="error">{e.last_error}</Banner>}
            </div>
          ))}
        </div>
      )}
      {dialog?.kind === 'install' && (
        <InstallModal
          edge={dialog.edge}
          onClose={() => setDialog(null)}
          onStarted={(id) => {
            setDialog(null)
            onOpenJob(id)
            void st.reload()
          }}
        />
      )}
      {dialog?.kind === 'manual' && <ManualModal st={dialog.edge} onClose={() => setDialog(null)} onSaved={() => void st.reload()} />}
    </Card>
  )
}

function InstallModal({ edge, onClose, onStarted }: { edge?: EdgeStatus; onClose: () => void; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [hostID, setHostID] = useState<number | null>(edge?.host_id ?? null)
  const [domain, setDomain] = useState(edge?.domain ?? '')
  const [roles, setRoles] = useState<string[]>(edge?.roles ?? ['hooks'])
  const [email, setEmail] = useState('')
  const [github, setGithub] = useState(false)
  const [proxyPort, setProxyPort] = useState('')
  const [check, setCheck] = useState<DNSCheck | null>(null)
  const [checking, setChecking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const list = (hosts.data ?? []).filter((h) => h.id > 0)
  return (
    <Modal title={edge ? t('edge.reinstallTitle', { name: edge.domain || edge.address }) : t('edge.installTitle')} onClose={onClose} width={640}>
      <p className="small muted">{edge ? t('edge.reinstallHint') : t('edge.installHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <div className="col" style={{ gap: '0.5rem' }}>
        <Select
          size="small"
          showSearch
          disabled={!!edge}
          placeholder={t('edge.host')}
          value={hostID}
          onChange={(id: number) => {
            setHostID(id)
            setCheck(null)
            const h = list.find((x) => x.id === id)
            if (h && hostName(h.addr)) setDomain(hostName(h.addr))
          }}
          options={list.map((h) => ({ value: h.id, label: `${h.name} (${h.addr})` }))}
        />
        <Space.Compact size="small">
          <Input
            size="small"
            placeholder="hooks.example.com"
            value={domain}
            onChange={(ev) => {
              setDomain(ev.target.value.trim().toLowerCase())
              setCheck(null)
            }}
          />
          <Button
            size="small"
            loading={checking}
            disabled={!hostID || !domain}
            onClick={async () => {
              setChecking(true)
              setError(null)
              try {
                setCheck(await api<DNSCheck>('/hub/edges/check', { method: 'POST', body: { host_id: hostID, domain } }))
              } catch (err) {
                setError(errText(err))
              } finally {
                setChecking(false)
              }
            }}
          >
            {t('edge.check')}
          </Button>
        </Space.Compact>
        {check && (
          <Banner kind={check.match ? 'info' : 'error'}>
            {check.match
              ? t('edge.dnsMatch', { domain: check.domain, ips: (check.domain_ips ?? []).join(', ') })
              : (check.domain_ips ?? []).length === 0
                ? t('edge.dnsMissing', { domain: check.domain })
                : t('edge.dnsMismatch', { domain: check.domain, ips: (check.domain_ips ?? []).join(', '), host: (check.host_ips ?? []).join(', ') || '—' })}
          </Banner>
        )}
        <RolesPicker value={roles} onChange={setRoles} />
        <Input size="small" placeholder={t('edge.email')} value={email} onChange={(ev) => setEmail(ev.target.value.trim())} />
        <Checkbox checked={github} onChange={(ev) => setGithub(ev.target.checked)}>
          {t('edge.githubOnly')}
        </Checkbox>
        <Input size="small" inputMode="numeric" placeholder={t('edge.proxyPort')} value={proxyPort} onChange={(ev) => setProxyPort(ev.target.value.replace(/\D/g, ''))} />
        <p className="small muted">{t('edge.proxyHint')}</p>
        <div>
          <Button
            type="primary"
            loading={busy}
            disabled={!hostID || !domain || roles.length === 0}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                const res = await api<{ job_id: number }>('/hub/edges/install', {
                  method: 'POST',
                  body: { host_id: hostID, domain, email, github_only: github, proxy_port: proxyPort ? Number(proxyPort) : 0, roles },
                })
                onStarted(res.job_id)
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {edge ? t('edge.reinstall') : t('edge.install')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function ManualModal({ st, onClose, onSaved }: { st?: EdgeStatus; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [address, setAddress] = useState(st?.address ?? '')
  const [domain, setDomain] = useState(st?.domain ?? '')
  const [token, setToken] = useState('')
  const [enabled, setEnabled] = useState(st ? st.enabled : true)
  const [roles, setRoles] = useState<string[]>(st?.roles ?? ['hooks'])
  const [certPEM, setCertPEM] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <Modal title={t('edge.manualTitle')} onClose={onClose} width={640}>
      <p className="small muted">{t('edge.manualHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <div className="col" style={{ gap: '0.5rem' }}>
        <Input size="small" placeholder="vps.example.com:8444" value={address} onChange={(ev) => setAddress(ev.target.value.trim())} />
        <Input size="small" placeholder="hooks.example.com" value={domain} onChange={(ev) => setDomain(ev.target.value.trim())} />
        <Input.Password size="small" placeholder={st?.configured ? t('edge.tokenKeep') : 'EDGE_TOKEN'} value={token} onChange={(ev) => setToken(ev.target.value)} autoComplete="new-password" />
        <Checkbox checked={enabled} onChange={(ev) => setEnabled(ev.target.checked)}>
          {t('edge.enabled')}
        </Checkbox>
        <RolesPicker value={roles} onChange={setRoles} />
        {roles.includes('api') && <p className="small muted">{t('edge.manualApiHint')}</p>}
        <Input.TextArea
          rows={5}
          className="mono small"
          placeholder={st?.fingerprint ? t('edge.certKeep') : '-----BEGIN CERTIFICATE-----'}
          value={certPEM}
          onChange={(ev) => setCertPEM(ev.target.value)}
        />
        <div>
          <Button
            type="primary"
            loading={busy}
            disabled={roles.length === 0}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                const body = { enabled, address, domain, token, cert_pem: certPEM, roles }
                if (st) await api(`/hub/edges/${st.id}`, { method: 'PUT', body })
                else await api('/hub/edges', { method: 'POST', body })
                onSaved()
                onClose()
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {t('common.save')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
