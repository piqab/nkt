// Подписи узла совпадают с хабом: те же эталоны проверяет Go
// (internal/hub/apitokens_test.go, TestSignatureVectors).
const test = require('node:test')
const assert = require('node:assert')
const { signature, splitToken, hostPath, withQuery } = require('../dist/nodes/Nkt/GenericFunctions')
const { verifySignature } = require('../dist/nodes/NktTrigger/NktTrigger.node')

test('API request signature matches the hub', () => {
  const sig = signature('s3cr3t-token-secret', '1700000000', 'nonce-0123456789abcdef', 'POST', '/api/hub/fail2ban/fleet?x=1', '{"action":"ban","ips":["198.51.100.7"]}')
  assert.strictEqual(sig, '4856857557412ec16c5748ccc1463f0114bbbf6631f1b7007a92dc3ff374db22')
})

test('outgoing webhook signature matches the hub', () => {
  const body = Buffer.from('{"kind":"test","text":"<b> & co"}')
  assert.ok(verifySignature('0f0e0d0c0b0a', '1700000000', body, '70e42b0bc62cd56f2cec516eec7ed2ed473f36f6bec0f8b178bb2b4f5b0fe4d8'))
  assert.ok(!verifySignature('0f0e0d0c0b0a', '1700000001', body, '70e42b0bc62cd56f2cec516eec7ed2ed473f36f6bec0f8b178bb2b4f5b0fe4d8'))
  assert.ok(!verifySignature('', '1700000000', body, 'zz'))
})

test('token, host and query helpers', () => {
  assert.deepStrictEqual(splitToken('nkt_abcdefghijklmnop_se_cr-et'), { keyId: 'abcdefghijklmnop', secret: 'se_cr-et' })
  assert.throws(() => splitToken('Bearer x'))
  assert.strictEqual(hostPath('-1'), 'local')
  assert.strictEqual(hostPath(7), '7')
  assert.throws(() => hostPath('../x'))
  assert.strictEqual(withQuery('/api/hub/events', { after: 5, kind: '', limit: 10 }), '/api/hub/events?after=5&limit=10')
})
