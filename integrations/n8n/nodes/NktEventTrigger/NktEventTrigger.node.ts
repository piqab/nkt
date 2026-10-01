import type { IDataObject, INodeExecutionData, INodeType, INodeTypeDescription, IPollFunctions } from 'n8n-workflow'
import { NodeConnectionTypes } from 'n8n-workflow'
import { nktRequest } from '../Nkt/GenericFunctions'
import { EVENT_KINDS } from '../NktTrigger/NktTrigger.node'

/**
 * Опрос оповещений хаба (GET /api/hub/events?after=N) — когда n8n не
 * принимает вебхуки снаружи. Первый запуск запоминает последнее событие и
 * ничего не выдаёт; дальше — только новые.
 */
export class NktEventTrigger implements INodeType {
  description: INodeTypeDescription = {
    displayName: 'nkt Alert Trigger (Polling)',
    name: 'nktEventTrigger',
    icon: 'file:nkt.svg',
    group: ['trigger'],
    version: 1,
    description: 'Polls the nkt hub for new alerts',
    defaults: { name: 'nkt Alert Trigger' },
    polling: true,
    inputs: [],
    outputs: [NodeConnectionTypes.Main],
    credentials: [{ name: 'nktApi', required: true }],
    properties: [
      {
        displayName: 'Events',
        name: 'events',
        type: 'multiOptions',
        options: EVENT_KINDS.filter((k) => !k.value.startsWith('deploy-') && k.value !== 'test'),
        default: [],
        description: 'Alert kinds that start the workflow; empty means all',
      },
    ],
  }

  async poll(this: IPollFunctions): Promise<INodeExecutionData[][] | null> {
    const state = this.getWorkflowStaticData('node') as { lastId?: number }
    const kinds = this.getNodeParameter('events', []) as string[]
    const manual = this.getMode() === 'manual'
    if (state.lastId === undefined && !manual) {
      const res = (await nktRequest.call(this, 'GET', '/api/hub/events', undefined, { limit: 1, hidden: 1 })) as IDataObject
      const first = ((res.events as IDataObject[]) ?? [])[0]
      state.lastId = first ? Number(first.id) : 0
      return null
    }
    const res = (await nktRequest.call(this, 'GET', '/api/hub/events', undefined, {
      after: manual ? 0 : state.lastId,
      limit: manual ? 5 : 200,
      kind: kinds.join(','),
      hidden: 1,
    })) as IDataObject
    const events = ((res.events as IDataObject[]) ?? []).slice().reverse() // старые первыми
    if (!manual && events.length > 0) state.lastId = Math.max(state.lastId ?? 0, ...events.map((e) => Number(e.id)))
    if (events.length === 0) return null
    return [events.map((e) => ({ json: e }))]
  }
}
