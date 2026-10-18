// Операции над целями Traffic Fly с карты — те же, что выполняет хаб
// (internal/hub/fly_ops.go). Здесь — чтобы показать черновик на карте до
// применения: что добавится, что уйдёт в резерв, что уберётся.

export interface FlyOp {
  op: 'add' | 'switch' | 'active' | 'reserve' | 'edit' | 'move' | 'remove'
  route: string
  target?: string
  to?: string
  host_id?: number
  kind?: string
  ref?: string
  port?: number
  weight?: number
}

export interface OpTarget {
  id: string
  host_id: number
  kind: string
  ref?: string
  label?: string
  port: number
  weight?: number
  drain?: boolean
}

export interface OpRoute {
  id: string
  port: number
  port_to?: number
  targets: OpTarget[]
}

/** Метка цели в черновике. */
export type OpMark = 'new' | 'toActive' | 'toReserve' | 'edited' | 'removed'

function newID<T extends OpRoute>(r: T, hostId: number): string {
  const used = new Set(r.targets.map((t) => t.id))
  let id = `c${hostId}`
  for (let n = 2; used.has(id); n++) id = `c${hostId}-${n}`
  return id
}

/** Операции по порядку к копии маршрутов (без проверок живости — их делает
 * хаб); неприменимая операция пропускается. */
export function applyOps<T extends OpRoute>(routes: T[], ops: FlyOp[]): T[] {
  const out = routes.map((r) => ({ ...r, targets: r.targets.map((t) => ({ ...t })) }))
  for (const op of ops) {
    const r = out.find((x) => x.id === op.route)
    if (!r) continue
    const range = !!r.port_to && r.port_to > r.port
    const ti = r.targets.findIndex((t) => t.id === op.target)
    switch (op.op) {
      case 'add':
        r.targets.push({ id: newID(r, op.host_id ?? 0), host_id: op.host_id ?? 0, kind: op.kind ?? 'container', ref: op.ref, port: range ? r.port : op.port ?? 0, drain: true })
        break
      case 'switch':
        if (ti >= 0) r.targets.forEach((t, i) => (t.drain = i !== ti))
        break
      case 'active':
        if (ti >= 0) r.targets[ti].drain = false
        break
      case 'reserve':
        if (ti >= 0) r.targets[ti].drain = true
        break
      case 'edit':
        if (ti >= 0) {
          if (op.port && !range) r.targets[ti].port = op.port
          if (op.weight) r.targets[ti].weight = op.weight
        }
        break
      case 'remove':
        if (ti >= 0) r.targets.splice(ti, 1)
        break
      case 'move': {
        const to = out.find((x) => x.id === op.to)
        if (ti < 0 || !to || to === r) break
        const [t] = r.targets.splice(ti, 1)
        to.targets.push({ ...t, id: newID(to, t.host_id), drain: true, port: to.port_to && to.port_to > to.port ? to.port : t.port })
        break
      }
    }
  }
  return out
}

/** Что черновик меняет: «маршрут/цель» → метка; убранные — отдельным
 * списком (на карте — бледными). */
export function draftMarks<T extends OpRoute>(before: T[], after: T[]): { marks: Map<string, OpMark>; removed: { route: string; target: T['targets'][number] }[] } {
  const marks = new Map<string, OpMark>()
  const removed: { route: string; target: T['targets'][number] }[] = []
  for (const a of after) {
    const b = before.find((x) => x.id === a.id)
    for (const t of a.targets) {
      const was = b?.targets.find((x) => x.id === t.id && x.host_id === t.host_id && x.ref === t.ref)
      const key = `${a.id}/${t.id}`
      if (!was) marks.set(key, 'new')
      else if (!!was.drain !== !!t.drain) marks.set(key, t.drain ? 'toReserve' : 'toActive')
      else if (was.port !== t.port || (was.weight ?? 1) !== (t.weight ?? 1)) marks.set(key, 'edited')
    }
  }
  for (const b of before) {
    const a = after.find((x) => x.id === b.id)
    for (const t of b.targets) {
      if (!a?.targets.some((x) => x.id === t.id && x.host_id === t.host_id && x.ref === t.ref)) removed.push({ route: b.id, target: t })
    }
  }
  return { marks, removed }
}

/** Рабочих (не в резерве) целей у маршрута. */
export function activeCount(r: OpRoute): number {
  return r.targets.filter((t) => !t.drain).length
}

/** Почему операцию нельзя выполнить на этих маршрутах (ключ перевода) —
 * те же правила, что у хаба; null — можно. alive — цель жива по проверке. */
export function opBlocked(routes: OpRoute[], op: FlyOp, alive: (route: string, target: string) => boolean): string | null {
  const r = routes.find((x) => x.id === op.route)
  if (!r) return 'fly.opNoRoute'
  if (op.op === 'add') return null
  const t = r.targets.find((x) => x.id === op.target)
  if (!t) return 'fly.opNoTargetUI'
  switch (op.op) {
    case 'switch':
      return alive(r.id, t.id) ? null : 'fly.opNotAliveUI'
    case 'reserve':
    case 'move':
      return !t.drain && activeCount(r) <= 1 ? 'fly.opLastActiveUI' : null
    case 'remove':
      return t.drain ? null : 'fly.opRemoveActiveUI'
  }
  return null
}

/** Что задание затрагивает на карте: «r:маршрут» и «t:маршрут/цель». */
export function opKeys(op: FlyOp): string[] {
  const keys = [`r:${op.route}`]
  if (op.target) keys.push(`t:${op.route}/${op.target}`)
  if (op.to) keys.push(`r:${op.to}`)
  return keys
}

/** Примерно сколько секунд идёт задание: добавление ждёт подготовку цели,
 * остальное — применение конфигурации. */
export function opEta(ops: FlyOp[]): number {
  return ops.some((o) => o.op === 'add' || o.op === 'move') ? 90 : 20
}

/** В черновик: правка той же цели подряд (колесо — вес ±1) сливается с
 * предыдущей, а не копится сотней шагов. */
export function pushOp(ops: FlyOp[], op: FlyOp): FlyOp[] {
  const last = ops[ops.length - 1]
  if (op.op === 'edit' && last?.op === 'edit' && last.route === op.route && last.target === op.target) {
    return [...ops.slice(0, -1), { ...last, port: op.port ?? last.port, weight: op.weight ?? last.weight }]
  }
  return [...ops, op]
}
