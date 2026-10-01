import type {
  ICredentialTestFunctions,
  ICredentialsDecrypted,
  IDataObject,
  IExecuteFunctions,
  IHttpRequestMethods,
  ILoadOptionsFunctions,
  INodeCredentialTestResult,
  INodeExecutionData,
  INodePropertyOptions,
  INodeType,
  INodeTypeDescription,
} from 'n8n-workflow'
import { NodeConnectionTypes, NodeOperationError } from 'n8n-workflow'
import { hostPath, nktRequest, signHeaders, splitToken, waitJob, withQuery, type NktCredentials } from './GenericFunctions'

const DRY_CHECKS = [
  ['Docker and compose', 'engine'],
  ['Stack description', 'config'],
  ['Images', 'images'],
  ['Host ports', 'ports'],
  ['Resources', 'resources'],
  ['Healthcheck', 'health'],
  ['Stack on the host', 'stack'],
  ['Site: DNS', 'site_dns'],
  ['Site: from outside', 'site_outside'],
  ['Site: host', 'site_host'],
  ['Site: certificate and nginx', 'site_cert'],
  ['nkt version on the host', 'version'],
].map(([name, value]) => ({ name, value }))

const hostField = (operations: string[], resource: string) => ({
  displayName: 'Host Name or ID',
  name: 'hostId',
  type: 'options' as const,
  typeOptions: { loadOptionsMethod: 'getHosts' },
  default: '',
  required: true,
  description: 'Choose from the list, or specify an ID using an <a href="https://docs.n8n.io/code/expressions/">expression</a>. The hub machine is -1.',
  displayOptions: { show: { resource: [resource], operation: operations } },
})

const pipelineField = (operations: string[]) => ({
  displayName: 'Pipeline Name or ID',
  name: 'pipelineId',
  type: 'options' as const,
  typeOptions: { loadOptionsMethod: 'getPipelines' },
  default: '',
  required: true,
  description: 'Choose from the list, or specify an ID using an <a href="https://docs.n8n.io/code/expressions/">expression</a>',
  displayOptions: { show: { resource: ['pipeline'], operation: operations } },
})

/** Узел хаба nkt: хосты, службы, контейнеры, выкладки, задания, fail2ban, оповещения. */
export class Nkt implements INodeType {
  description: INodeTypeDescription = {
    displayName: 'nkt',
    name: 'nkt',
    icon: 'file:nkt.svg',
    group: ['transform'],
    version: 1,
    subtitle: '={{$parameter["operation"] + ": " + $parameter["resource"]}}',
    description: 'Work with an nkt (NetKnownsThat) hub: hosts, findings, deployments, dry runs, fail2ban, alerts',
    defaults: { name: 'nkt' },
    inputs: [NodeConnectionTypes.Main],
    outputs: [NodeConnectionTypes.Main],
    credentials: [{ name: 'nktApi', required: true, testedBy: 'nktApiTest' }],
    properties: [
      {
        displayName: 'Resource',
        name: 'resource',
        type: 'options',
        noDataExpression: true,
        options: [
          { name: 'API Call', value: 'api' },
          { name: 'Alert', value: 'event' },
          { name: 'Container', value: 'container' },
          { name: 'Fail2ban', value: 'fail2ban' },
          { name: 'Host', value: 'host' },
          { name: 'Job', value: 'job' },
          { name: 'Pipeline', value: 'pipeline' },
          { name: 'Service', value: 'service' },
        ],
        default: 'host',
      },

      // --- host
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['host'] } },
        options: [
          { name: 'Get Findings', value: 'findings', action: 'Get the findings of a host' },
          { name: 'Get Many', value: 'getAll', action: 'Get hosts' },
          { name: 'Get Overview', value: 'overview', action: 'Get the overview of a host' },
          { name: 'Get Services', value: 'services', action: 'Get the services of a host' },
          { name: 'Get Vulnerabilities', value: 'vulnerabilities', action: 'Get the vulnerabilities of a host' },
        ],
        default: 'getAll',
      },
      hostField(['overview', 'findings', 'vulnerabilities', 'services'], 'host'),
      {
        displayName: 'Split Into Items',
        name: 'split',
        type: 'boolean',
        default: true,
        description: 'Whether to output one item per host or finding instead of a single item',
        displayOptions: { show: { resource: ['host'], operation: ['getAll', 'findings'] } },
      },

      // --- service / container
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['service'] } },
        options: [
          { name: 'Reload', value: 'reload', action: 'Reload a service' },
          { name: 'Restart', value: 'restart', action: 'Restart a service' },
          { name: 'Start', value: 'start', action: 'Start a service' },
          { name: 'Stop', value: 'stop', action: 'Stop a service' },
        ],
        default: 'restart',
      },
      hostField(['reload', 'restart', 'start', 'stop'], 'service'),
      {
        displayName: 'Service',
        name: 'name',
        type: 'string',
        default: '',
        required: true,
        placeholder: 'nginx.service',
        displayOptions: { show: { resource: ['service'] } },
      },
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['container'] } },
        options: [
          { name: 'Restart', value: 'restart', action: 'Restart a container' },
          { name: 'Start', value: 'start', action: 'Start a container' },
          { name: 'Stop', value: 'stop', action: 'Stop a container' },
        ],
        default: 'restart',
      },
      hostField(['restart', 'start', 'stop'], 'container'),
      {
        displayName: 'Container',
        name: 'name',
        type: 'string',
        default: '',
        required: true,
        displayOptions: { show: { resource: ['container'] } },
      },

      // --- pipeline
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['pipeline'] } },
        options: [
          { name: 'Deploy', value: 'deploy', action: 'Deploy a pipeline' },
          { name: 'Dry Run', value: 'dryRun', action: 'Dry run a pipeline' },
          { name: 'Get', value: 'get', action: 'Get a pipeline' },
          { name: 'Get Deployments', value: 'deployments', action: 'Get the deployments of a pipeline' },
          { name: 'Get Many', value: 'getAll', action: 'Get pipelines' },
        ],
        default: 'deploy',
      },
      pipelineField(['deploy', 'dryRun', 'get', 'deployments']),
      {
        displayName: 'Ref',
        name: 'ref',
        type: 'string',
        default: '',
        description: 'Branch to deploy; empty means the pipeline branch',
        displayOptions: { show: { resource: ['pipeline'], operation: ['deploy', 'dryRun'] } },
      },
      {
        displayName: 'Tag',
        name: 'tag',
        type: 'string',
        default: '',
        description: 'Tag to deploy; empty means the branch head',
        displayOptions: { show: { resource: ['pipeline'], operation: ['deploy', 'dryRun'] } },
      },
      {
        displayName: 'Skip Checks',
        name: 'skip',
        type: 'multiOptions',
        options: DRY_CHECKS,
        default: [],
        description: 'Dry run checks to skip; everything else is checked',
        displayOptions: { show: { resource: ['pipeline'], operation: ['dryRun'] } },
      },
      {
        displayName: 'Wait for Result',
        name: 'wait',
        type: 'boolean',
        default: true,
        description: 'Whether to wait for the hub job to finish and return its status and log',
        displayOptions: { show: { resource: ['pipeline'], operation: ['deploy', 'dryRun'] } },
      },
      {
        displayName: 'Timeout (Seconds)',
        name: 'timeout',
        type: 'number',
        default: 900,
        typeOptions: { minValue: 10 },
        displayOptions: { show: { resource: ['pipeline'], operation: ['deploy', 'dryRun'], wait: [true] } },
      },

      // --- job
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['job'] } },
        options: [
          { name: 'Get', value: 'get', action: 'Get a hub job' },
          { name: 'Get Log', value: 'log', action: 'Get the log of a hub job' },
          { name: 'Wait', value: 'wait', action: 'Wait for a hub job to finish' },
        ],
        default: 'get',
      },
      {
        displayName: 'Job ID',
        name: 'jobId',
        type: 'number',
        default: 0,
        required: true,
        displayOptions: { show: { resource: ['job'] } },
      },
      {
        displayName: 'Timeout (Seconds)',
        name: 'timeout',
        type: 'number',
        default: 900,
        displayOptions: { show: { resource: ['job'], operation: ['wait'] } },
      },

      // --- fail2ban
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['fail2ban'] } },
        options: [
          { name: 'Ban', value: 'ban', action: 'Ban IP addresses on hosts' },
          { name: 'Get Banned', value: 'banned', action: 'Get banned addresses' },
          { name: 'Unban', value: 'unban', action: 'Unban IP addresses on hosts' },
        ],
        default: 'ban',
      },
      {
        displayName: 'IP Addresses',
        name: 'ips',
        type: 'string',
        default: '',
        required: true,
        placeholder: '198.51.100.7, 203.0.113.0/24',
        description: 'Comma or space separated',
        displayOptions: { show: { resource: ['fail2ban'], operation: ['ban', 'unban'] } },
      },
      {
        displayName: 'Host Names or IDs',
        name: 'hostIds',
        type: 'multiOptions',
        typeOptions: { loadOptionsMethod: 'getHosts' },
        default: [],
        description: 'Empty means every host with fail2ban. Choose from the list, or specify IDs using an <a href="https://docs.n8n.io/code/expressions/">expression</a>.',
        displayOptions: { show: { resource: ['fail2ban'], operation: ['ban', 'unban'] } },
      },
      {
        displayName: 'Wait for Result',
        name: 'wait',
        type: 'boolean',
        default: true,
        displayOptions: { show: { resource: ['fail2ban'], operation: ['ban', 'unban'] } },
      },

      // --- event
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['event'] } },
        options: [{ name: 'Get Many', value: 'getAll', action: 'Get alerts' }],
        default: 'getAll',
      },
      {
        displayName: 'After ID',
        name: 'after',
        type: 'number',
        default: 0,
        description: 'Only alerts newer than this number',
        displayOptions: { show: { resource: ['event'] } },
      },
      {
        displayName: 'Limit',
        name: 'limit',
        type: 'number',
        default: 50,
        typeOptions: { minValue: 1 },
        description: 'Max number of results to return',
        displayOptions: { show: { resource: ['event'] } },
      },

      // --- api
      {
        displayName: 'Operation',
        name: 'operation',
        type: 'options',
        noDataExpression: true,
        displayOptions: { show: { resource: ['api'] } },
        options: [{ name: 'Call', value: 'call', action: 'Call any hub API path available to the token' }],
        default: 'call',
      },
      {
        displayName: 'Method',
        name: 'method',
        type: 'options',
        options: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({ name: m, value: m })),
        default: 'GET',
        displayOptions: { show: { resource: ['api'] } },
      },
      {
        displayName: 'Path',
        name: 'path',
        type: 'string',
        default: '/api/hub/hosts',
        required: true,
        description: 'Starts with /api/; the query string goes here too',
        displayOptions: { show: { resource: ['api'] } },
      },
      {
        displayName: 'Body (JSON)',
        name: 'body',
        type: 'json',
        default: '',
        displayOptions: { show: { resource: ['api'], method: ['POST', 'PUT', 'PATCH', 'DELETE'] } },
      },
    ],
  }

  methods = {
    loadOptions: {
      async getHosts(this: ILoadOptionsFunctions): Promise<INodePropertyOptions[]> {
        const hosts = (await nktRequest.call(this, 'GET', '/api/hub/hosts')) as IDataObject[]
        return hosts.map((h) => ({ name: `${h.name} (${h.id})`, value: String(h.id) }))
      },
      async getPipelines(this: ILoadOptionsFunctions): Promise<INodePropertyOptions[]> {
        const res = (await nktRequest.call(this, 'GET', '/api/hub/pipelines')) as IDataObject
        return ((res.pipelines as IDataObject[]) ?? []).map((p) => ({ name: String(p.name), value: String(p.id) }))
      },
    },
    credentialTest: {
      async nktApiTest(this: ICredentialTestFunctions, credential: ICredentialsDecrypted): Promise<INodeCredentialTestResult> {
        const cred = credential.data as unknown as NktCredentials
        const base = String(cred.url).replace(/\/+$/, '')
        const headers: Record<string, string> = {}
        try {
          if (cred.mode === 'bearer') headers.Authorization = `Bearer ${cred.token.trim()}`
          else {
            const { keyId, secret } = splitToken(cred.token)
            headers['X-NKT-API-Key'] = keyId
            Object.assign(headers, signHeaders(secret, 'GET', '/api/auth/me', ''))
          }
          const res = await fetch(base + '/api/auth/me', { headers })
          const body = (await res.json().catch(() => ({}))) as IDataObject
          if (!res.ok) return { status: 'Error', message: String(body.error ?? `HTTP ${res.status}`) }
          const tok = (body.token ?? {}) as IDataObject
          return { status: 'OK', message: `Token "${tok.name ?? body.username}" (${tok.role ?? body.role})` }
        } catch (err) {
          return { status: 'Error', message: (err as Error).message }
        }
      },
    },
  }

  async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
    const items = this.getInputData()
    const out: INodeExecutionData[] = []
    for (let i = 0; i < items.length; i++) {
      try {
        const resource = this.getNodeParameter('resource', i) as string
        const operation = this.getNodeParameter('operation', i) as string
        const push = (data: IDataObject | IDataObject[]) => {
          for (const d of Array.isArray(data) ? data : [data]) out.push({ json: d, pairedItem: { item: i } })
        }
        if (resource === 'host') {
          if (operation === 'getAll') {
            const hosts = (await nktRequest.call(this, 'GET', '/api/hub/hosts')) as IDataObject[]
            push(this.getNodeParameter('split', i) ? hosts : { hosts })
            continue
          }
          const host = hostPath(this.getNodeParameter('hostId', i) as string)
          const data = await nktRequest.call(this, 'GET', `/api/hosts/${host}/${operation}`)
          if (operation === 'findings' && this.getNodeParameter('split', i)) {
            const list = Array.isArray(data) ? data : ((data as IDataObject).findings as IDataObject[]) ?? []
            push(list as IDataObject[])
          } else push(Array.isArray(data) ? { items: data } : data)
        } else if (resource === 'service' || resource === 'container') {
          const host = hostPath(this.getNodeParameter('hostId', i) as string)
          const name = encodeURIComponent((this.getNodeParameter('name', i) as string).trim())
          const kind = resource === 'service' ? 'services' : 'containers'
          push(await nktRequest.call(this, 'POST', `/api/hosts/${host}/${kind}/${name}/${operation}`))
        } else if (resource === 'pipeline') {
          if (operation === 'getAll') {
            const res = (await nktRequest.call(this, 'GET', '/api/hub/pipelines')) as IDataObject
            push((res.pipelines as IDataObject[]) ?? [])
            continue
          }
          const id = Number(this.getNodeParameter('pipelineId', i))
          if (operation === 'get') {
            push(await nktRequest.call(this, 'GET', `/api/hub/pipelines/${id}`))
          } else if (operation === 'deployments') {
            const res = (await nktRequest.call(this, 'GET', `/api/hub/pipelines/${id}/deployments`)) as IDataObject
            push((res.deployments as IDataObject[]) ?? [])
          } else {
            const ref = this.getNodeParameter('ref', i) as string
            const tag = this.getNodeParameter('tag', i) as string
            const res =
              operation === 'deploy'
                ? ((await nktRequest.call(this, 'POST', `/api/hub/pipelines/${id}/deploy`, { ref, tag })) as IDataObject)
                : ((await nktRequest.call(this, 'POST', '/api/hub/pipelines/dryrun', { pipeline_id: id, ref, tag, skip: this.getNodeParameter('skip', i) as string[] })) as IDataObject)
            const jobId = Number(res.job_id)
            if (this.getNodeParameter('wait', i)) {
              push({ job_id: jobId, ...(await waitJob.call(this, jobId, this.getNodeParameter('timeout', i) as number)) })
            } else push(res)
          }
        } else if (resource === 'job') {
          const id = this.getNodeParameter('jobId', i) as number
          if (operation === 'get') push(await nktRequest.call(this, 'GET', `/api/hub/jobs/${id}`))
          else if (operation === 'log') push(await nktRequest.call(this, 'GET', `/api/hub/jobs/${id}/log`, undefined, { after: 0 }))
          else push({ job_id: id, ...(await waitJob.call(this, id, this.getNodeParameter('timeout', i) as number)) })
        } else if (resource === 'fail2ban') {
          if (operation === 'banned') {
            const res = (await nktRequest.call(this, 'GET', '/api/hub/fail2ban/banned')) as IDataObject
            push((res.ips as IDataObject[]) ?? [])
            continue
          }
          const ips = (this.getNodeParameter('ips', i) as string).split(/[\s,;]+/).filter(Boolean)
          const hostIds = (this.getNodeParameter('hostIds', i) as string[]).map((h) => Number(h === 'local' ? -1 : h))
          const res = (await nktRequest.call(this, 'POST', '/api/hub/fail2ban/fleet', { action: operation, ips, host_ids: hostIds })) as IDataObject
          if (this.getNodeParameter('wait', i) && res.job_id) push({ job_id: res.job_id, ...(await waitJob.call(this, Number(res.job_id), 300)) })
          else push(res)
        } else if (resource === 'event') {
          const res = (await nktRequest.call(this, 'GET', '/api/hub/events', undefined, {
            after: this.getNodeParameter('after', i) as number,
            limit: this.getNodeParameter('limit', i) as number,
          })) as IDataObject
          push((res.events as IDataObject[]) ?? [])
        } else if (resource === 'api') {
          const method = this.getNodeParameter('method', i) as IHttpRequestMethods
          const path = (this.getNodeParameter('path', i) as string).trim()
          if (!path.startsWith('/api/')) throw new NodeOperationError(this.getNode(), 'The path must start with /api/', { itemIndex: i })
          let body: IDataObject | undefined
          if (method !== 'GET') {
            const raw = this.getNodeParameter('body', i, '') as string | IDataObject
            body = typeof raw === 'string' ? (raw.trim() ? JSON.parse(raw) : undefined) : raw
          }
          const [p, q] = path.split('?')
          const data = await nktRequest.call(this, method, q ? `${p}?${q}` : withQuery(p), body)
          push(Array.isArray(data) ? { items: data } : typeof data === 'object' ? data : { result: data })
        }
      } catch (err) {
        if (this.continueOnFail()) {
          out.push({ json: { error: (err as Error).message }, pairedItem: { item: i } })
          continue
        }
        throw err
      }
    }
    return [out]
  }
}
