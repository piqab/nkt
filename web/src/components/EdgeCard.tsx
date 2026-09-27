import { useState } from 'react'
import { Button, Checkbox, Input, Select, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { HubHost } from '../types'
import { Banner, Card, Modal, formatRelative } from './ui'
import { confirmAction } from './confirm'

interface EdgeStatus {
  configured: boolean
  enabled: boolean
  address?: string
  domain?: string
  fingerprint?: string
  host_id?: number
  connected: boolean
  since?: string
  last_error?: string
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/**
 * nkt-edge — входной сервис для вебхуков на VPS: хаб сам держит к нему
 * туннель, наружу открыт только edge. Установка на хост хаба — заданием,
 * или настройка вручную.
 */
export function EdgeCard({ onOpenJob }: { onOpenJob: (id: number) => void }) {
  const { t } = useTranslation()
  const st = useApi<EdgeStatus>('/hub/edge', 10_000)
  const [dialog, setDialog] = useState<'install' | 'manual' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const e = st.data
  return (
    <Card
      title="nkt-edge"
      subtitle={t('edge.subtitle')}
      actions={
        <Space>
          <Button size="small" type="primary" onClick={() => setDialog('install')}>
            {t('edge.install')}
          </Button>
          <Button size="small" onClick={() => setDialog('manual')}>
            {t('edge.manual')}
          </Button>
          {e?.configured && !!e.host_id && (
            <Button
              size="small"
              danger
              onClick={async () => {
                if (!(await confirmAction(t('edge.uninstallConfirm')))) return
                try {
                  const res = await api<{ job_id: number }>('/hub/edge/uninstall', { method: 'POST' })
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
          {e?.configured && (
            <Button
              size="small"
              danger
              onClick={async () => {
                if (!(await confirmAction(t('edge.forgetConfirm')))) return
                try {
                  await api('/hub/edge', { method: 'DELETE' })
                  void st.reload()
                } catch (err) {
                  setError(errText(err))
                }
              }}
            >
              {t('edge.forget')}
            </Button>
          )}
        </Space>
      }
    >
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {!e ? null : !e.configured ? (
        <p className="small muted">{t('edge.none')}</p>
      ) : (
        <div className="col small" style={{ gap: '0.3rem' }}>
          <div>
            {e.connected ? <Tag color="success">{t('edge.connected')}</Tag> : e.enabled ? <Tag color="error">{t('edge.disconnected')}</Tag> : <Tag>{t('edge.disabled')}</Tag>}
            {e.connected && e.since && <span className="muted">{formatRelative(e.since)}</span>}
          </div>
          {e.domain && (
            <div>
              {t('edge.hooksAt')}: <span className="mono">https://{e.domain}/hooks/…</span>
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
      )}
      {dialog === 'install' && (
        <InstallModal
          onClose={() => setDialog(null)}
          onStarted={(id) => {
            setDialog(null)
            onOpenJob(id)
            void st.reload()
          }}
        />
      )}
      {dialog === 'manual' && e && <ManualModal st={e} onClose={() => setDialog(null)} onSaved={() => void st.reload()} />}
    </Card>
  )
}

function InstallModal({ onClose, onStarted }: { onClose: () => void; onStarted: (jobID: number) => void }) {
  const { t } = useTranslation()
  const hosts = useApi<HubHost[]>('/hub/hosts')
  const [hostID, setHostID] = useState<number | null>(null)
  const [domain, setDomain] = useState('')
  const [email, setEmail] = useState('')
  const [github, setGithub] = useState(false)
  const [proxyPort, setProxyPort] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const list = (hosts.data ?? []).filter((h) => h.id > 0)
  return (
    <Modal title={t('edge.installTitle')} onClose={onClose} width={640}>
      <p className="small muted">{t('edge.installHint')}</p>
      {error && <Banner kind="error">{error}</Banner>}
      <div className="col" style={{ gap: '0.5rem' }}>
        <Select size="small" showSearch placeholder={t('edge.host')} value={hostID} onChange={setHostID} options={list.map((h) => ({ value: h.id, label: `${h.name} (${h.addr})` }))} />
        <Input size="small" placeholder="hooks.example.com" value={domain} onChange={(ev) => setDomain(ev.target.value.trim())} />
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
            disabled={!hostID || !domain}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                const res = await api<{ job_id: number }>('/hub/edge/install', { method: 'POST', body: { host_id: hostID, domain, email, github_only: github, proxy_port: proxyPort ? Number(proxyPort) : 0 } })
                onStarted(res.job_id)
              } catch (err) {
                setError(errText(err))
              } finally {
                setBusy(false)
              }
            }}
          >
            {t('edge.install')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function ManualModal({ st, onClose, onSaved }: { st: EdgeStatus; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [address, setAddress] = useState(st.address ?? '')
  const [domain, setDomain] = useState(st.domain ?? '')
  const [token, setToken] = useState('')
  const [enabled, setEnabled] = useState(st.configured ? st.enabled : true)
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
        <Input.Password size="small" placeholder={st.configured ? t('edge.tokenKeep') : 'EDGE_TOKEN'} value={token} onChange={(ev) => setToken(ev.target.value)} autoComplete="new-password" />
        <Checkbox checked={enabled} onChange={(ev) => setEnabled(ev.target.checked)}>
          {t('edge.enabled')}
        </Checkbox>
        <Input.TextArea
          rows={5}
          className="mono small"
          placeholder={st.fingerprint ? t('edge.certKeep') : '-----BEGIN CERTIFICATE-----'}
          value={certPEM}
          onChange={(ev) => setCertPEM(ev.target.value)}
        />
        <div>
          <Button
            type="primary"
            loading={busy}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                await api('/hub/edge', { method: 'PUT', body: { enabled, address, domain, token, cert_pem: certPEM } })
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
