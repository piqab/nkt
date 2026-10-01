import type { ICredentialType, INodeProperties } from 'n8n-workflow'

/**
 * API-токен хаба nkt («О системе» → «API-токены»). Подписанные запросы
 * (секрет по сети не идёт — годится и через nkt-edge) или Bearer.
 */
export class NktApi implements ICredentialType {
  name = 'nktApi'
  displayName = 'nkt Hub API'
  documentationUrl = 'https://piqab.github.io/nkt/guide/n8n'
  properties: INodeProperties[] = [
    {
      displayName: 'Hub URL',
      name: 'url',
      type: 'string',
      default: '',
      placeholder: 'https://hub.example.com',
      description: 'The hub address (or an nkt-edge with the API role), without /api',
      required: true,
    },
    {
      displayName: 'Token',
      name: 'token',
      type: 'string',
      typeOptions: { password: true },
      default: '',
      placeholder: 'nkt_<key>_<secret>',
      description: 'The token string shown once when it was created (nkt_<key>_<secret>)',
      required: true,
    },
    {
      displayName: 'Authentication',
      name: 'mode',
      type: 'options',
      options: [
        { name: 'Signed Requests (Recommended)', value: 'signed', description: 'HMAC signature; the secret never crosses the network; required through nkt-edge' },
        { name: 'Bearer', value: 'bearer', description: 'Authorization: Bearer; fine inside your own network or over HTTPS' },
      ],
      default: 'signed',
    },
    {
      displayName: 'Ignore TLS Errors',
      name: 'allowUnauthorizedCerts',
      type: 'boolean',
      default: false,
      description: 'Whether to accept a self-signed hub certificate',
    },
  ]
}
