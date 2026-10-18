// После vite build: крупные ресурсы сборки — сжатыми (имя.gz вместо
// имени). В бинарнике nkt они так на несколько мегабайт меньше, а браузеру
// уходят как есть, с Content-Encoding: gzip (internal/webui/serve.go).
import { readdirSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { gzipSync, constants } from 'node:zlib'

const dir = new URL('../../internal/webui/dist/assets/', import.meta.url).pathname
let before = 0
let after = 0
for (const name of readdirSync(dir)) {
  if (!/\.(js|css|svg|json)$/.test(name)) continue
  const p = join(dir, name)
  // Размер — по прочитанному, а не отдельным stat: файл читается один раз.
  const data = readFileSync(p)
  const size = data.length
  if (size < 1024) continue
  const gz = gzipSync(data, { level: constants.Z_BEST_COMPRESSION })
  writeFileSync(p + '.gz', gz)
  unlinkSync(p)
  before += size
  after += gz.length
}
console.log(`gzip-dist: ${(before / 1048576).toFixed(2)} MB → ${(after / 1048576).toFixed(2)} MB`)
