import { createHash, createHmac, randomBytes } from 'crypto'
import type { IDataObject, IExecuteFunctions, ILoadOptionsFunctions, IPollFunctions, IHttpRequestMethods } from 'n8n-workflow'
import { NodeApiError, NodeOperationError } from 'n8n-workflow'

type Ctx = IExecuteFunctions | ILoadOptionsFunctions | IPollFunctions

export interface NktCredentials {
  url: string
  token: string
  mode: 'signed' | 'bearer'
  allowUnauthorizedCerts?: boolean
}

/** Токен «nkt_<ключ 16>_<секрет>» → ключ и секрет. */
export function splitToken(token: string): { keyId: string; secret: string } {
  const t = token.trim()
  const m = /^nkt_([a-z2-7]{16})_(.+)$/.exec(t)
  if (!m) throw new Error('The token must look like nkt_<key>_<secret>')
  return { keyId: m[1], secret: m[2] }
}

/**
 * Подпись запроса хаба: hex HMAC-SHA256 секретом от
 * «NKT-API-1\n<время>\n<nonce>\n<МЕТОД>\n<путь?запрос>\n<hex sha256 тела>».
 */
export function signature(secret: string, ts: string, nonce: string, method: string, uri: string, body: string): string {
  const hash = createHash('sha256').update(body).digest('hex')
  return createHmac('sha256', secret).update(['NKT-API-1', ts, nonce, method.toUpperCase(), uri, hash].join('\n')).digest('hex')
}

export function signHeaders(secret: string, method: string, uri: string, body: string, now = Date.now()): Record<string, string> {
  const ts = String(Math.floor(now / 1000))
  const nonce = randomBytes(16).toString('hex')
  return { 'X-NKT-API-Timestamp': ts, 'X-NKT-API-Nonce': nonce, 'X-NKT-API-Signature': signature(secret, ts, nonce, method, uri, body) }
}

/** Путь с запросом: значения пустые и undefined не попадают. */
export function withQuery(path: string, qs?: IDataObject): string {
  const parts: string[] = []
  for (const [k, v] of Object.entries(qs ?? {})) {
    if (v === undefined || v === null || v === '') continue
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
  }
  return parts.length ? `${path}?${parts.join('&')}` : path
}

/** Хост в пути API: машина хаба — local. */
export function hostPath(hostId: string | number): string {
  const id = String(hostId).trim()
  if (id === '-1' || id === 'local' || id === 'localhost') return 'local'
  if (!/^\d+$/.test(id)) throw new Error(`Not a host id: ${id}`)
  return id
}

/** Запрос к API хаба с токеном. */
export async function nktRequest(this: Ctx, method: IHttpRequestMethods, path: string, body?: IDataObject | IDataObject[], qs?: IDataObject): Promise<any> {
  const cred = (await this.getCredentials('nktApi')) as unknown as NktCredentials
  const base = String(cred.url).replace(/\/+$/, '')
  const uri = withQuery(path, qs)
  const payload = body === undefined ? '' : JSON.stringify(body)
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (payload) headers['Content-Type'] = 'application/json'
  if (cred.mode === 'bearer') {
    headers.Authorization = `Bearer ${cred.token.trim()}`
  } else {
    const { keyId, secret } = splitToken(cred.token)
    headers['X-NKT-API-Key'] = keyId
    Object.assign(headers, signHeaders(secret, method, uri, payload))
  }
  try {
    const res = await this.helpers.httpRequest({
      method,
      url: base + uri,
      headers,
      body: payload || undefined,
      json: false,
      returnFullResponse: true,
      ignoreHttpStatusErrors: true,
      skipSslCertificateValidation: !!cred.allowUnauthorizedCerts,
    })
    const text = typeof res.body === 'string' ? res.body : Buffer.isBuffer(res.body) ? res.body.toString('utf8') : JSON.stringify(res.body)
    let data: any = text
    try {
      data = text ? JSON.parse(text) : {}
    } catch {
      // не JSON — как есть
    }
    if (res.statusCode < 200 || res.statusCode > 299) {
      const msg = data && typeof data === 'object' && data.error ? String(data.error) : `HTTP ${res.statusCode}`
      throw new NodeApiError(this.getNode(), { message: msg, httpCode: String(res.statusCode) } as any, { message: msg, httpCode: String(res.statusCode) })
    }
    return data
  } catch (err) {
    if (err instanceof NodeApiError) throw err
    throw new NodeOperationError(this.getNode(), (err as Error).message)
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** Задание хаба до конца (или до таймаута): задание и журнал. */
export async function waitJob(this: IExecuteFunctions, jobId: number, timeoutSec: number): Promise<IDataObject> {
  const deadline = Date.now() + timeoutSec * 1000
  let job: IDataObject = {}
  for (;;) {
    job = await nktRequest.call(this, 'GET', `/api/hub/jobs/${jobId}`)
    const status = String(job.status)
    if (status !== 'queued' && status !== 'running') break
    if (Date.now() > deadline) {
      return { job, timedOut: true }
    }
    await sleep(3000)
  }
  const log = await nktRequest.call(this, 'GET', `/api/hub/jobs/${jobId}/log`, undefined, { after: 0 })
  const lines = ((log.lines as IDataObject[]) ?? []).map((l) => String(l.text))
  return { job, succeeded: job.status === 'succeeded', log: lines }
}
