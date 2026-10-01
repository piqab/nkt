import { createHmac, timingSafeEqual } from 'crypto'
import type { IDataObject, INodeType, INodeTypeDescription, IWebhookFunctions, IWebhookResponseData } from 'n8n-workflow'
import { NodeConnectionTypes } from 'n8n-workflow'

export const EVENT_KINDS = [
  ['Host Unreachable', 'unreachable'],
  ['Host Recovered', 'recovered'],
  ['New Findings', 'problems'],
  ['Findings Resolved', 'resolved'],
  ['Job Failed', 'job-failed'],
  ['Host Rebooted', 'rebooted'],
  ['New Bans', 'bans'],
  ['Deployment Succeeded', 'deploy-succeeded'],
  ['Deployment Failed', 'deploy-failed'],
  ['Test', 'test'],
].map(([name, value]) => ({ name, value }))

/** Подпись исходящего вебхука хаба: hex HMAC-SHA256 от «время.тело». */
export function verifySignature(secret: string, ts: string, body: Buffer, sig: string): boolean {
  if (!secret || !ts || !/^[0-9a-f]{64}$/i.test(sig)) return false
  const want = createHmac('sha256', secret).update(ts + '.').update(body).digest()
  const got = Buffer.from(sig, 'hex')
  return got.length === want.length && timingSafeEqual(got, want)
}

/**
 * Принимает исходящие вебхуки хаба nkt («Оповещения» → «Исходящие
 * вебхуки»): адрес этого узла — URL адресата на хабе. Подпись проверяется
 * секретом адресата; старые (дольше 10 минут) и чужие запросы — 401.
 */
export class NktTrigger implements INodeType {
  description: INodeTypeDescription = {
    displayName: 'nkt Trigger',
    name: 'nktTrigger',
    icon: 'file:nkt.svg',
    group: ['trigger'],
    version: 1,
    subtitle: '={{$parameter["events"].join(", ") || "all events"}}',
    description: 'Starts the workflow on an nkt hub outgoing webhook (alerts, deployments)',
    defaults: { name: 'nkt Trigger' },
    inputs: [],
    outputs: [NodeConnectionTypes.Main],
    webhooks: [{ name: 'default', httpMethod: 'POST', responseMode: 'onReceived', path: 'nkt' }],
    properties: [
      {
        displayName:
          'Put this node\'s webhook URL into the hub: "Alerts" → "Outgoing webhooks" → "New recipient", and paste the signing secret the hub shows below.',
        name: 'notice',
        type: 'notice',
        default: '',
      },
      {
        displayName: 'Signing Secret',
        name: 'secret',
        type: 'string',
        typeOptions: { password: true },
        default: '',
        required: true,
        description: 'The secret the hub showed when the recipient was created (or after "New secret")',
      },
      {
        displayName: 'Events',
        name: 'events',
        type: 'multiOptions',
        options: EVENT_KINDS,
        default: [],
        description: 'Events that start the workflow; empty means all (the hub also filters by the recipient\'s own choice)',
      },
    ],
  }

  async webhook(this: IWebhookFunctions): Promise<IWebhookResponseData> {
    const req = this.getRequestObject()
    const res = this.getResponseObject()
    const secret = this.getNodeParameter('secret') as string
    const events = this.getNodeParameter('events', []) as string[]
    const raw: Buffer = (req as unknown as { rawBody?: Buffer }).rawBody ?? Buffer.from(JSON.stringify(req.body ?? {}))
    const ts = String(req.headers['x-nkt-timestamp'] ?? '')
    const sig = String(req.headers['x-nkt-signature'] ?? '')
    const age = Math.abs(Date.now() / 1000 - Number(ts))
    if (!verifySignature(secret, ts, raw, sig) || !(age < 600)) {
      res.status(401).json({ error: 'bad signature' })
      return { noWebhookResponse: true }
    }
    const body = (req.body ?? {}) as IDataObject
    const kind = String(req.headers['x-nkt-event'] ?? body.kind ?? '')
    if (events.length > 0 && !events.includes(kind)) {
      return { webhookResponse: { ok: true, skipped: kind } }
    }
    return {
      workflowData: [[{ json: { ...body, delivery: req.headers['x-nkt-delivery'] } }]],
    }
  }
}
