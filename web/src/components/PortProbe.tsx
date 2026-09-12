import { useEffect, useMemo, useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Segmented, Select, Tag, Tooltip } from 'antd'
import { ApiOutlined, DownloadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, hostScope, LOCAL_HOST_ID, useApi } from '../api'
import type { NetworkInterface } from '../types'
import { Modal } from './ui'

/**
 * Проверка порта — то, что делают руками, когда «сервис вроде слушает,
 * а не отвечает»: соединиться и прочитать баннер, послать что-то и
 * дождаться ответа, спросить по HTTP с телом и заголовками, посмотреть
 * сертификат. Или запустить настоящий curl со своими параметрами.
 *
 * Структурные проверки выполняет сам nkt (internal/portprobe) — они не
 * зависят от того, что стоит на хосте; эквивалент для терминала при этом
 * показывается. Ответ можно посмотреть текстом, отрисовать (HTML в
 * песочнице без скриптов и сети, картинку, JSON с отступами) или скачать.
 *
 * Два места, откуда проверять: с самого хоста (отвечает ли сервис
 * вообще) и с хаба (доходит ли до него снаружи — через фаервол). Второе
 * доступно, когда раздел открыт через хаб; curl со своими параметрами —
 * только на хосте.
 */

type Kind = 'tcp' | 'http' | 'https' | 'tls' | 'curl'

interface ProbeResult {
  ok: boolean
  elapsed_ms: number
  error?: string
  status?: string
  headers?: string[]
  content_type?: string
  /** base64 */
  body?: string
  truncated?: boolean
  /** base64 */
  received?: string
  printable?: boolean
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

const METHODS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']
const CONTENT_TYPES = [
  { value: 'text/plain', label: 'text/plain' },
  { value: 'application/json', label: 'application/json' },
  { value: 'application/x-www-form-urlencoded', label: 'form (a=1&b=2)' },
  { value: 'application/xml', label: 'application/xml' },
  { value: '', label: '—' },
]

/** Слушающий адрес «отовсюду» — проверять его надо по конкретному. */
function isWildcard(addr: string): boolean {
  return addr === '0.0.0.0' || addr === '*' || addr === '::' || addr === '[::]' || addr === ''
}

function b64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64)
  const out = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function bytesToText(bytes: Uint8Array): string {
  return new TextDecoder('utf-8', { fatal: false }).decode(bytes)
}

/** Первые 4 КиБ байтов столбиком hex + текст — для бинарного ответа. */
function hexDump(bytes: Uint8Array, limit = 4096): string {
  const lines: string[] = []
  for (let off = 0; off < Math.min(bytes.length, limit); off += 16) {
    const chunk = bytes.subarray(off, off + 16)
    const hex = [...chunk].map((b) => b.toString(16).padStart(2, '0')).join(' ')
    const txt = [...chunk].map((b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : '.')).join('')
    lines.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(47)}  ${txt}`)
  }
  if (bytes.length > limit) lines.push('…')
  return lines.join('\n')
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
  const [customMethod, setCustomMethod] = useState('')
  const [host, setHost] = useState('')
  const [headers, setHeaders] = useState('')
  const [body, setBody] = useState('')
  const [contentType, setContentType] = useState('application/json')
  const [insecure, setInsecure] = useState(true)
  const [send, setSend] = useState('')
  const [sendCRLF, setSendCRLF] = useState(true)
  const [timeout, setTimeout_] = useState(5)
  const [busy, setBusy] = useState<'host' | 'hub' | null>(null)
  const [result, setResult] = useState<{ from: 'host' | 'hub'; res: ProbeResult } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [view, setView] = useState<'text' | 'render'>('text')

  const viaHub = hostScope.id !== null && hostScope.id !== LOCAL_HOST_ID
  const isHTTP = kind === 'http' || kind === 'https'
  const effectiveMethod = method === 'custom' ? customMethod.trim().toUpperCase() : method
  const bodyAllowed = isHTTP && !['GET', 'HEAD', 'OPTIONS'].includes(effectiveMethod)

  // Своя строка curl — по умолчанию то, что собрала бы форма: её правят,
  // а не набирают с нуля.
  const [args, setArgs] = useState('')
  useEffect(() => {
    if (kind !== 'curl') return
    setArgs((prev) => prev || `-sS -i -m ${timeout} http://${target}:${port}/`)
  }, [kind, target, port, timeout])

  function buildRequest() {
    return {
      address: target,
      port,
      kind,
      path,
      method: effectiveMethod,
      host,
      headers: headers.split('\n').map((h) => h.trim()).filter(Boolean),
      body: bodyAllowed ? body : '',
      content_type: bodyAllowed ? contentType : '',
      insecure,
      send,
      send_crlf: sendCRLF,
      args,
      timeout_s: timeout,
    }
  }

  async function run(from: 'host' | 'hub') {
    setBusy(from)
    setError(null)
    if (bodyAllowed && contentType === 'application/json' && body.trim()) {
      try {
        JSON.parse(body)
      } catch {
        setError(t('probe.badJSON'))
        setBusy(null)
        return
      }
    }
    try {
      const req = buildRequest()
      const res =
        from === 'host'
          ? await api<ProbeResult>('/ports/probe', { method: 'POST', body: req })
          : // Запрос к самому хабу, минуя область хоста: проверяет он, а не хост.
            await fetch(`/api/hub/hosts/${hostScope.id}/probe`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(req),
            }).then(async (r) => {
              const data = (await r.json()) as ProbeResult & { error?: string }
              if (!r.ok) throw new Error(data.error ?? `HTTP ${r.status}`)
              return data
            })
      setResult({ from, res })
      // HTML и картинки хочется увидеть, а не читать исходником.
      const ct = res.content_type ?? ''
      setView(ct.startsWith('text/html') || ct.startsWith('image/') || ct.includes('json') ? 'render' : 'text')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal title={t('probe.title', { port })} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '0.6rem' }}>
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          {kind !== 'curl' && (
            <label>
              {t('probe.address')}
              <Select
                value={target}
                onChange={setTarget}
                style={{ minWidth: '11rem' }}
                options={candidates.map((a) => ({ value: a, label: a }))}
              />
            </label>
          )}
          <label>
            {t('probe.kind')}
            <Select<Kind>
              value={kind}
              onChange={setKind}
              style={{ minWidth: '14rem' }}
              options={[
                { value: 'tcp', label: t('probe.kindTcp') },
                { value: 'http', label: t('probe.kindHttp') },
                { value: 'https', label: t('probe.kindHttps') },
                { value: 'tls', label: t('probe.kindTls') },
                { value: 'curl', label: t('probe.kindCurl') },
              ]}
            />
          </label>
          <label>
            {t('probe.timeout')}
            <InputNumber min={1} max={30} value={timeout} onChange={(v) => setTimeout_(v ?? 5)} />
          </label>
        </div>

        {kind === 'curl' && (
          <label>
            {t('probe.curlArgs')}
            <Input.TextArea value={args} onChange={(e) => setArgs(e.target.value)} rows={2} className="mono" />
            <span className="small muted">{t('probe.curlHint')}</span>
          </label>
        )}

        {isHTTP && (
          <>
            <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <label>
                {t('probe.method')}
                <Select
                  value={method}
                  onChange={setMethod}
                  style={{ minWidth: '8rem' }}
                  options={[...METHODS.map((m) => ({ value: m, label: m })), { value: 'custom', label: t('probe.methodCustom') }]}
                />
              </label>
              {method === 'custom' && (
                <label>
                  {t('probe.methodName')}
                  <Input value={customMethod} onChange={(e) => setCustomMethod(e.target.value)} placeholder="PROPFIND" />
                </label>
              )}
              <label style={{ flex: 1, minWidth: '10rem' }}>
                {t('probe.path')}
                <Input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/" />
              </label>
            </div>
            <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <label style={{ flex: 1, minWidth: '10rem' }}>
                {t('probe.host')}
                <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="example.com" />
              </label>
              {kind === 'https' && (
                <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
                  <Checkbox checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
                  {t('probe.insecure')}
                </label>
              )}
            </div>
            <label>
              {t('probe.headers')}
              <Input.TextArea
                value={headers}
                onChange={(e) => setHeaders(e.target.value)}
                rows={2}
                className="mono"
                placeholder={'Authorization: Bearer …\nAccept: application/json'}
              />
            </label>
            {bodyAllowed && (
              <div className="col" style={{ gap: '0.3rem' }}>
                <div className="row" style={{ gap: '0.6rem', alignItems: 'flex-end' }}>
                  <label>
                    {t('probe.contentType')}
                    <Select value={contentType} onChange={setContentType} style={{ minWidth: '14rem' }} options={CONTENT_TYPES} />
                  </label>
                </div>
                <label>
                  {t('probe.body')}
                  <Input.TextArea value={body} onChange={(e) => setBody(e.target.value)} rows={4} className="mono" />
                </label>
              </div>
            )}
          </>
        )}

        {kind === 'tls' && (
          <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label style={{ flex: 1, minWidth: '10rem' }}>
              {t('probe.host')}
              <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="example.com" />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
              <Checkbox checked={insecure} onChange={(e) => setInsecure(e.target.checked)} />
              {t('probe.insecure')}
            </label>
          </div>
        )}

        {(kind === 'tcp' || kind === 'tls') && (
          <div className="col" style={{ gap: '0.3rem' }}>
            <label>
              {t('probe.send')}
              <Input.TextArea
                value={send}
                onChange={(e) => setSend(e.target.value)}
                rows={2}
                className="mono"
                placeholder={t('probe.sendPlaceholder')}
              />
            </label>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
              <Checkbox checked={sendCRLF} onChange={(e) => setSendCRLF(e.target.checked)} />
              {t('probe.sendCRLF')}
            </label>
            <span className="small muted">{t('probe.bannerHint')}</span>
          </div>
        )}

        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" loading={busy === 'host'} disabled={!!busy} onClick={() => void run('host')}>
            {t('probe.runHost')}
          </Button>
          {viaHub && kind !== 'curl' && (
            <Tooltip title={t('probe.runHubHint')}>
              <Button loading={busy === 'hub'} disabled={!!busy} onClick={() => void run('hub')}>
                {t('probe.runHub')}
              </Button>
            </Tooltip>
          )}
        </div>

        {error && <div style={{ color: 'var(--status-critical)' }}>{error}</div>}

        {result && <ProbeOutcome result={result.res} from={result.from} view={view} setView={setView} port={port} />}
      </div>
    </Modal>
  )
}

function ProbeOutcome({
  result,
  from,
  view,
  setView,
  port,
}: {
  result: ProbeResult
  from: 'host' | 'hub'
  view: 'text' | 'render'
  setView: (v: 'text' | 'render') => void
  port: number
}) {
  const { t } = useTranslation()
  const bytes = useMemo(() => (result.body ? b64ToBytes(result.body) : new Uint8Array(new ArrayBuffer(0))), [result.body])
  const received = useMemo(() => (result.received ? b64ToBytes(result.received) : new Uint8Array(new ArrayBuffer(0))), [result.received])
  const ct = (result.content_type ?? '').toLowerCase()
  const isHTML = ct.startsWith('text/html')
  const isImage = ct.startsWith('image/')
  const isJSON = ct.includes('json')
  const isPDF = ct.startsWith('application/pdf')
  const isText = ct.startsWith('text/') || isJSON || ct.includes('xml') || ct.includes('javascript') || ct === ''
  const renderable = isHTML || isImage || isJSON || isPDF

  // Скачать — прямо из того, что пришло: тело уже здесь, отдельный
  // запрос ни к чему.
  const [blobURL, setBlobURL] = useState<string | null>(null)
  useEffect(() => {
    if (bytes.length === 0) return
    const url = URL.createObjectURL(new Blob([bytes], { type: result.content_type || 'application/octet-stream' }))
    setBlobURL(url)
    return () => URL.revokeObjectURL(url)
  }, [bytes, result.content_type])

  const ext = isHTML ? 'html' : isJSON ? 'json' : isPDF ? 'pdf' : isImage ? ct.split('/')[1]?.split(';')[0] || 'bin' : isText ? 'txt' : 'bin'

  function renderBody() {
    if (bytes.length === 0) return null
    if (view === 'render' && isHTML) {
      // Песочница: без скриптов, без сети — CSP внутри документа
      // запрещает всё, кроме встроенных стилей и data:-картинок. Так видно
      // ровно то, что пришло с порта, и чужая страница не выполняет свой
      // JS в сессии nkt и не тянет ресурсы с настоящего адреса.
      const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:">`
      const html = bytesToText(bytes)
      const doc = /<head[^>]*>/i.test(html) ? html.replace(/<head[^>]*>/i, (m) => m + csp) : csp + html
      return <iframe title="response" sandbox="" srcDoc={doc} style={{ width: '100%', height: '24rem', border: '1px solid var(--border)', background: '#fff' }} />
    }
    if (view === 'render' && isImage && blobURL) {
      return <img src={blobURL} alt="" style={{ maxWidth: '100%', maxHeight: '24rem', border: '1px solid var(--border)' }} />
    }
    if (view === 'render' && isPDF && blobURL) {
      return <iframe title="pdf" src={blobURL} style={{ width: '100%', height: '28rem', border: '1px solid var(--border)' }} />
    }
    if (view === 'render' && isJSON) {
      let pretty = bytesToText(bytes)
      try {
        pretty = JSON.stringify(JSON.parse(pretty), null, 2)
      } catch {
        // Не разобрался — покажем как есть.
      }
      return <pre className="diff mono" style={{ margin: 0, maxHeight: '24rem', overflow: 'auto' }}>{pretty}</pre>
    }
    if (isText) {
      return (
        <pre className="diff mono" style={{ margin: 0, maxHeight: '24rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
          {bytesToText(bytes)}
          {result.truncated ? '\n…' : ''}
        </pre>
      )
    }
    return <pre className="diff mono" style={{ margin: 0, maxHeight: '24rem', overflow: 'auto' }}>{hexDump(bytes)}</pre>
  }

  return (
    <div className="col" style={{ gap: '0.4rem' }}>
      <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <Tag color={result.ok ? 'success' : 'error'}>{result.ok ? t('probe.ok') : t('probe.fail')}</Tag>
        <span className="small muted">
          {t(from === 'hub' ? 'probe.fromHub' : 'probe.fromHost')} · {result.elapsed_ms} ms
        </span>
        {result.status && <span className="small mono">{result.status}</span>}
        {result.content_type && <span className="small muted mono">{result.content_type}</span>}
        {bytes.length > 0 && (
          <span className="small muted">
            {bytes.length} B{result.truncated ? ` (${t('probe.truncated')})` : ''}
          </span>
        )}
        <span style={{ flex: 1 }} />
        {renderable && (
          <Segmented<'text' | 'render'>
            size="small"
            value={view}
            onChange={setView}
            options={[
              { value: 'text', label: t('probe.viewText') },
              { value: 'render', label: t('probe.viewRender') },
            ]}
          />
        )}
        {blobURL && (
          <a href={blobURL} download={`port-${port}-response.${ext}`}>
            <Button size="small" icon={<DownloadOutlined />}>
              {t('probe.download')}
            </Button>
          </a>
        )}
      </div>
      {result.error && <div className="small" style={{ color: 'var(--status-critical)' }}>{result.error}</div>}
      {result.tls && (
        <div className="small">
          <div>
            {result.tls.version} · {result.tls.cipher} · {result.tls.verified ? t('probe.certVerified') : t('probe.certUnverified')}
          </div>
          <div className="mono">{result.tls.subject}</div>
          <div className="muted">
            {t('probe.certIssuer')}: {result.tls.issuer}
          </div>
          <div className="muted">
            {t('probe.certValid')}: {result.tls.not_before.slice(0, 10)} — {result.tls.not_after.slice(0, 10)}
            {result.tls.dns_names?.length ? ` · ${result.tls.dns_names.join(', ')}` : ''}
          </div>
        </div>
      )}
      {(result.headers?.length ?? 0) > 0 && (
        <pre className="diff mono" style={{ margin: 0, maxHeight: '10rem', overflow: 'auto' }}>
          {result.headers!.join('\n')}
        </pre>
      )}
      {renderBody()}
      {received.length > 0 && (
        <div className="col" style={{ gap: '0.2rem' }}>
          <span className="small muted">{t('probe.received', { bytes: received.length })}</span>
          <pre className="diff mono" style={{ margin: 0, maxHeight: '16rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
            {result.printable ? bytesToText(received) : hexDump(received)}
          </pre>
        </div>
      )}
      {result.ok && received.length === 0 && (result.body?.length ?? 0) === 0 && !result.status && (
        <span className="small muted">{t('probe.silent')}</span>
      )}
      <div className="small muted">
        {t('probe.command')}
        <pre className="diff mono" style={{ margin: '0.2rem 0 0', whiteSpace: 'pre-wrap' }}>
          {result.command}
        </pre>
      </div>
    </div>
  )
}
