// Новые операции узла: какие запросы уходят хабу (контекст n8n подделан).
const test = require('node:test')
const assert = require('node:assert')
const { Nkt } = require('../dist/nodes/Nkt/Nkt.node.js')

function run(params, reply = () => ({ job_id: 7 })) {
  const calls = []
  const ctx = {
    getInputData: () => [{ json: {} }],
    getNodeParameter: (name, _i, def) => (name in params ? params[name] : def),
    getCredentials: async () => ({ url: 'https://hub.example.com', mode: 'bearer', token: 'nkt_k_s' }),
    getNode: () => ({ name: 'nkt' }),
    continueOnFail: () => false,
    helpers: {
      httpRequest: async (o) => {
        calls.push({ method: o.method, url: o.url.replace('https://hub.example.com', ''), body: o.body ? JSON.parse(o.body) : undefined })
        return { statusCode: 200, body: JSON.stringify(reply(o)) }
      },
    },
  }
  return new Nkt().execute.call(ctx).then((out) => ({ calls, out: out[0].map((x) => x.json) }))
}

test('описание: новые ресурсы', () => {
  const res = new Nkt().description.properties.find((p) => p.name === 'resource')
  for (const v of ['script', 'migration', 'fly', 'cluster']) assert.ok(res.options.some((o) => o.value === v), v)
})

test('Script → Run', async () => {
  const { calls } = await run({ resource: 'script', operation: 'run', scriptId: '3', values: '{"param:FROM":"web1"}', dryRun: true, wait: false })
  assert.deepStrictEqual(calls[0], { method: 'POST', url: '/api/hub/scripts/3/run', body: { values: { 'param:FROM': 'web1' }, dry_run: true } })
})

test('Migration → Move Stack', async () => {
  const { calls } = await run({ resource: 'migration', operation: 'move', fromHostId: '1', toHostId: '-1', container: ' forgejo-forgejo-1 ', switchFly: true, switchPipeline: false, cleanLeftovers: false, wait: false })
  assert.deepStrictEqual(calls[0], {
    method: 'POST', url: '/api/hub/migrate',
    body: { from: 1, to: -1, container: 'forgejo-forgejo-1', switch_fly: true, switch_pipeline: false, clean_leftovers: false },
  })
})

test('Traffic Fly → Apply с ожиданием', async () => {
  const { calls, out } = await run({ resource: 'fly', operation: 'apply', dryRun: false, wait: true, timeout: 60 }, (o) =>
    o.url.endsWith('/apply') ? { job_id: 9 } : o.url.includes('/log') ? { lines: [{ text: 'ok' }] } : { id: 9, status: 'succeeded' },
  )
  assert.deepStrictEqual(calls[0], { method: 'POST', url: '/api/hub/fly/apply', body: { dry_run: false } })
  assert.strictEqual(out[0].succeeded, true)
  assert.deepStrictEqual(out[0].log, ['ok'])
})

test('Cluster → Apply Manifest', async () => {
  const { calls } = await run({ resource: 'cluster', operation: 'apply', clusterIds: ['2', '5'], manifestName: 'hello', manifest: 'kind: Namespace', note: 'n8n', wait: false })
  assert.deepStrictEqual(calls[0], { method: 'POST', url: '/api/hub/k8s/manifests/apply?job=1', body: { name: 'hello', content: 'kind: Namespace', clusters: [2, 5], note: 'n8n' } })
})
