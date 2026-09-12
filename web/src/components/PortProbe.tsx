import { useEffect, useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select, Tag, Tooltip } from 'antd'
import { ApiOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, hostScope, LOCAL_HOST_ID, useApi } from '../api'
import type { NetworkInterface } from '../types'
import { Modal } from './ui'

/**
 * Проверка порта — то, что делают руками, когда «сервис вроде слушает,
 * а не отвечает»: соединиться, спросить по HTTP, посмотреть сертификат.
 *
 * Проверку выполняет сам nkt (см. internal/portprobe), а не curl на
 * хосте: так она не зависит от того, что там установлено, и не гоняет
 * произвольную команду с параметрами из браузера. Эквивалентная команда
 * при этом показывается — её можно скопировать в терминал и повторить с
 * любыми ключами.
 *
 * Два места, откуда проверять: с самого хоста (отвечает ли сервис
 * вообще) и с хаба (доходит ли до него снаружи — через фаервол). Второе
 * доступно, когда раздел открыт через хаб.
 */

type Kind = 'tcp' | 'http' | 'https' | 'tls'

interface ProbeResult {
  ok: boolean
  elapsed_ms: number
  error?: string
  status?: string
  headers?: string[]
  body?: string
  truncated?: boolean
  tls?: {
    version: string
    cipher: string
    subject: string
    issuer: string
    dns_names?: string[]
    not_before: string
    not_after: string
    verified: boolean
  }
  command: string
}

/** Слушающий адрес «отовсюду» — проверять его надо по конкретному. */
function isWildcard(addr: string): boolean {
  return addr === '0.0.0.0' || addr === '*' || addr === '::' || addr === '[::]' || addr === ''
}

/** Иконка-ссылка рядом с портом: открывает окно проверки. */
export function ProbeLink({ address, port, protocol }: { address: string; port: number; protocol?: string }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  // UDP не соединяется — проверять нечем, и иконку показывать незачем.
  if (protocol && protocol.toLowerCase().startsWith('udp')) return null
  return (
    <>
      <Tooltip title={t('probe.open', { port })}>
        <Button
          type="text"
          size="small"
          aria-label={t('probe.open', { port })}
          icon={<ApiOutlined />}
          onClick={() => setOpen(true)}
        />
      </Tooltip>
      {open && <PortProbeModal address={address} port={port} onClose={() => setOpen(false)} />}
    </>
  )
}

export function PortProbeModal({ address, port, onClose }: { address: string; port: number; onClose: () => void }) {
  const { t } = useTranslation()
  const ifaces = useApi<{ interfaces: NetworkInterface[] }>('/interfaces', 300_000)

  // Куда стучаться: сокет на 0.0.0.0 слушает на всех адресах хоста, и
  // проверить его можно по любому из них — по умолчанию по loopback.
  const candidates = useMemo(() => {
    const out: string[] = []
    const push = (a: string) => {
      if (a && !out.includes(a)) out.push(a)
    }
    if (!isWildcard(address)) push(address.replace(/^\[|\]$/g, ''))
    push('127.0.0.1')
    for (const i of ifaces.data?.interfaces ?? []) {
      for (const cidr of i.addresses ?? []) {
        const ip = cidr.split('/')[0]
        if (ip && !ip.includes(':')) push(ip)
      }
    }
    return out
  }, [address, ifaces.data])

  const [target, setTarget] = useState(candidates[0] ?? '127.0.0.1')
  useEffect(() => {
    if (!candidates.includes(target)) setTarget(candidates[0] ?? '127.0.0.1')
  }, [candidates, target])

  // Порт 443 и его соседи почти всегда TLS — угадываем вид проверки, а
  // не заставляем выбирать каждый раз.
  const [kind, setKind] = useState<Kind>(port === 443 || port === 8443 ? 'https' : port === 80 || port === 8080 ? 'http' : 'tcp')
  const [path, setPath] = useState('/')
  const [method, setMethod] = useState('GET')
  const [host, setHost] = useState('')
  const [insecure, setInsecure] = useState(true)
  const [timeout, setTimeout_] = useState(5)
  const [busy, setBusy] = useState<'host' | 'hub' | null>(null)
  const [result, setResult] = useState<{ from: 'host' | 'hub'; res: ProbeResult } | null>(null)
  const [error, setError] = useState<string | null>(null)

  const viaHub = hostScope.id !== null && hostScope.id !== LOCAL_HOST_ID

  async function run(from: 'host' | 'hub') {
    setBusy(from)
    setError(null)
    try {
      const body = { address: target, port, kind, path, method, host, insecure, timeout_s: timeout }
      const res =
        from === 'host'
          ? await api<ProbeResult>('/ports/probe', { method: 'POST', body })
          : // Запрос к самому хабу, минуя область хоста: проверяет он, а не хост.
            await fetch(`/api/hub/hosts/${hostScope.id}/probe`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(body),
            }).then(async (r) => {
              const data = (await r.json()) as ProbeResult & { error?: string }
              if (!r.ok) throw new Error(data.error ?? `HTTP ${r.status}`)
              return data
            })
      setResult({ from, res })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const isHTTP = kind === 'http' || kind === 'https'

  return (
    <Modal title={t('probe.title', { port })} onClose={onClose} width={760}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <label>
            {t('probe.address')}
            <Select
              value={target}
              onChange={setTarget}
              style={{ minWidth: '11rem' }}
              options={candidates.map((a) => ({ value: a, label: a }))}
            />
          </label>
          <label>
            {t('probe.kind')}
            <Select<Kind>
              value={kind}
              onChange={setKind}
              style={{ minWidth: '13rem' }}
              options={[
                { value: 'tcp', label: t('probe.kindTcp') },
                { value: 'http', label: t('probe.kindHttp') },
                { value: 'https', label: t('probe.kindHttps') },
                { value: 'tls', label: t('probe.kindTls') },
              ]}
            />
          </label>
          <label>
            {t('probe.timeout')}
            <InputNumber min={1} max={30} value={timeout} onChange={(v) => setTimeout_(v ?? 5)} />
          </label>
        </div>

        {isHTTP && (
          <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label>
              {t('probe.method')}
              <Select
                value={method}
                onChange={setMethod}
                style={{ minWidth: '7rem' }}
                options={['GET', 'HEAD', 'POST', 'OPTIONS'].map((m) => ({ value: m, label: m }))}
              />
            </label>
            <label style={{ flex: 1, minWidth: '10rem' }}>
              {t('probe.path')}
              <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/" />
            </label>
          </div>
        )}
        {(isHTTP || kind === 'tls') && (
          <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label style={{ flex: 1, minWidth: '10rem' }}>
              {t('probe.host')}
              <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="example.com" />
            </label>
            {kind !== 'http' && (
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
                <Checkbox checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
                {t('probe.insecure')}
              </label>
            )}
          </div>
        )}

        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" loading={busy === 'host'} disabled={!!busy} onClick={() => void run('host')}>
            {t('probe.runHost')}
          </Button>
          {viaHub && (
            <Tooltip title={t('probe.runHubHint')}>
              <Button loading={busy === 'hub'} disabled={!!busy} onClick={() => void run('hub')}>
                {t('probe.runHub')}
              </Button>
            </Tooltip>
          )}
        </div>

        {error && <div style={{ color: 'var(--status-critical)' }}>{error}</div>}

        {result && (
          <div className="col" style={{ gap: '0.4rem' }}>
            <div className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
              <Tag color={result.res.ok ? 'success' : 'error'}>{result.res.ok ? t('probe.ok') : t('probe.fail')}</Tag>
              <span className="small muted">
                {t(result.from === 'hub' ? 'probe.fromHub' : 'probe.fromHost')} · {result.res.elapsed_ms} ms
              </span>
              {result.res.status && <span className="small mono">{result.res.status}</span>}
            </div>
            {result.res.error && <div className="small" style={{ color: 'var(--status-critical)' }}>{result.res.error}</div>}
            {result.res.tls && (
              <div className="small">
                <div>
                  {result.res.tls.version} · {result.res.tls.cipher} ·{' '}
                  {result.res.tls.verified ? t('probe.certVerified') : t('probe.certUnverified')}
                </div>
                <div className="mono">{result.res.tls.subject}</div>
                <div className="muted">
                  {t('probe.certIssuer')}: {result.res.tls.issuer}
                </div>
                <div className="muted">
                  {t('probe.certValid')}: {result.res.tls.not_before.slice(0, 10)} — {result.res.tls.not_after.slice(0, 10)}
                  {result.res.tls.dns_names?.length ? ` · ${result.res.tls.dns_names.join(', ')}` : ''}
                </div>
              </div>
            )}
            {(result.res.headers?.length ?? 0) > 0 && (
              <pre className="diff" style={{ margin: 0, maxHeight: '10rem', overflow: 'auto' }}>
                {result.res.headers!.join('\n')}
              </pre>
            )}
            {result.res.body && (
              <pre className="diff" style={{ margin: 0, maxHeight: '12rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
                {result.res.body}
                {result.res.truncated ? '\n…' : ''}
              </pre>
            )}
            <div className="small muted">
              {t('probe.command')}
              <pre className="diff mono" style={{ margin: '0.2rem 0 0', whiteSpace: 'pre-wrap' }}>
                {result.res.command}
              </pre>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
