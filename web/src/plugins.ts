/** Протокол внешних плагинов (internal/plugin): манифест и виды разделов. */

export interface PluginOp {
  name: string
  title?: string
  description?: string
  input?: Record<string, unknown>
  read_only?: boolean
  destructive?: boolean
  mcp?: boolean
}

export interface PluginManifest {
  protocol: number
  name: string
  title: string
  version: string
  description?: string
  ops: PluginOp[]
  pages?: { id: string; title: string; op: string; hidden?: boolean }[]
  commands?: { word: string; usage: string; summary?: string; example?: string }[]
  provides?: string[]
  hub_actions?: string[]
}

export interface PluginInfo {
  name: string
  host: string
  port: number
  pipeline?: string
  title: string
  version?: string
  token?: string
  /** Поставлен хабом службой на хост: ключ пакета каталога. */
  native?: string
  /** В каталоге хаба файл плагина новее поставленного. */
  outdated?: boolean
  author?: string
  added_at?: string
  error?: string
  manifest?: PluginManifest
}

export interface PluginField {
  name: string
  title: string
  type?: 'text' | 'textarea' | 'number' | 'select' | 'bool' | 'password'
  options?: { value: string; title?: string }[]
  required?: boolean
  default?: unknown
  placeholder?: string
  hint?: string
}

export interface PluginAction {
  title: string
  op?: string
  args?: Record<string, unknown>
  fields?: PluginField[]
  confirm?: string
  danger?: boolean
  open?: string
  edit?: { get: string; put: string; doc: string; language?: string }
  /** Записи для DNS-плагина хаба (почта: MX, SPF, DKIM). */
  dns?: { owner: string; records: { name: string; type: string; values: string[]; ttl?: number }[] }
}

export interface PluginView {
  type: 'table' | 'text' | 'sections'
  title?: string
  note?: string
  text?: string
  columns?: { key: string; title: string }[]
  rows?: { cells: Record<string, unknown>; actions?: PluginAction[] }[]
  actions?: PluginAction[]
  items?: PluginView[]
}

export interface PluginResponse {
  ok: boolean
  error?: string
  text?: string
  result?: unknown
  view?: PluginView
}

/** Инструмент MCP операции: <плагин>_<операция>. */
export function pluginToolName(plugin: string, op: string): string {
  return `${plugin.replace(/-/g, '_')}_${op}`
}
