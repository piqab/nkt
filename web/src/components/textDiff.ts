/**
 * Unified diff двух текстов на клиенте — для правок, у которых нет
 * файла на хосте (профили, сценарии хаба): «сохранено → черновик» без
 * обращения к серверу. Построчный LCS: тексты здесь — сотни строк, так
 * что квадратичная таблица не страшна; для очень больших текстов —
 * грубый дифф «всё заменено».
 */
export function unifiedDiff(from: string, to: string, fromName = 'saved', toName = 'draft', context = 3): string {
  if (from === to) return ''
  const a = from.split('\n')
  const b = to.split('\n')
  type Op = { kind: ' ' | '-' | '+'; line: string; ai: number; bi: number }
  const ops: Op[] = []
  if (a.length * b.length > 4_000_000) {
    a.forEach((line, i) => ops.push({ kind: '-', line, ai: i, bi: 0 }))
    b.forEach((line, i) => ops.push({ kind: '+', line, ai: a.length, bi: i }))
  } else {
    const n = a.length
    const m = b.length
    const lcs: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1))
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < n && j < m) {
      if (a[i] === b[j]) {
        ops.push({ kind: ' ', line: a[i], ai: i, bi: j })
        i++
        j++
      } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
        ops.push({ kind: '-', line: a[i], ai: i, bi: j })
        i++
      } else {
        ops.push({ kind: '+', line: b[j], ai: i, bi: j })
        j++
      }
    }
    while (i < n) ops.push({ kind: '-', line: a[i], ai: i++, bi: j })
    while (j < m) ops.push({ kind: '+', line: b[j], ai: i, bi: j++ })
  }
  // Ханки: изменения с context строками вокруг.
  const out = [`--- ${fromName}`, `+++ ${toName}`]
  let k = 0
  while (k < ops.length) {
    if (ops[k].kind === ' ') {
      k++
      continue
    }
    const start = Math.max(0, k - context)
    let end = k
    let quiet = 0
    while (end < ops.length && quiet <= context * 2) {
      quiet = ops[end].kind === ' ' ? quiet + 1 : 0
      end++
    }
    end = Math.min(ops.length, end - Math.max(0, quiet - context))
    const hunk = ops.slice(start, end)
    const aStart = hunk[0].ai + 1
    const bStart = hunk[0].bi + 1
    const aLen = hunk.filter((o) => o.kind !== '+').length
    const bLen = hunk.filter((o) => o.kind !== '-').length
    out.push(`@@ -${aStart},${aLen} +${bStart},${bLen} @@`)
    for (const o of hunk) out.push(o.kind + o.line)
    k = end
  }
  return out.join('\n')
}
