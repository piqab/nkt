// Значки узлов (svg) — рядом со скомпилированными узлами.
const fs = require('fs')
const path = require('path')
for (const dir of ['Nkt', 'NktTrigger', 'NktEventTrigger']) {
  const src = path.join(__dirname, '..', 'nodes', dir, 'nkt.svg')
  const dst = path.join(__dirname, '..', 'dist', 'nodes', dir, 'nkt.svg')
  fs.mkdirSync(path.dirname(dst), { recursive: true })
  fs.copyFileSync(src, dst)
}
