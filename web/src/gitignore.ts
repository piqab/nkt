/**
 * Фильтр загрузки папки: скрытое и то, что перечислено в .gitignore.
 *
 * Зачем свой разбор, а не библиотека: сборка идёт без внешних CDN, а
 * нужен ровно тот набор правил, который встречается в настоящих
 * .gitignore — комментарии, отрицания «!», привязка к каталогу правила
 * ведущим «/», завершающий «/» (только каталог), «*», «?» и «**».
 * Остального (вложенные диапазоны [a-z], escape-последовательности) в
 * этих файлах почти не бывает; неподдержанный шаблон просто не совпадёт
 * ни с чем, то есть файл будет загружен — ошибка в сторону «загрузить
 * лишнее», а не «молча пропустить нужное».
 *
 * Правила применяются к путям относительно того каталога, где лежит сам
 * .gitignore, — как это делает git: .gitignore во вложенной папке
 * действует только на своё поддерево.
 */

/** Одно правило: шаблон, отрицание, «только каталог». */
interface Rule {
  re: RegExp
  negate: boolean
  dirOnly: boolean
}

/** Набор правил одного .gitignore вместе с каталогом, где он лежит. */
export interface IgnoreSet {
  /** Каталог правил: «» для корня выбранной папки, иначе «src/» и т.п. */
  base: string
  rules: Rule[]
}

const escapeRe = (s: string) => s.replace(/[.+^${}()|[\]\\]/g, '\\$&')

/** Шаблон .gitignore → регулярное выражение по пути относительно base. */
function compile(pattern: string): RegExp {
  // «/» в середине привязывает шаблон к каталогу правила, иначе он
  // совпадает на любой глубине — это и есть разница между «/build» или
  // «doc/frotz» и просто «build».
  const anchored = pattern.includes('/') && !pattern.endsWith('/') ? pattern.startsWith('/') : pattern.startsWith('/')
  const body = pattern.replace(/^\//, '').replace(/\/$/, '')
  let re = ''
  for (let i = 0; i < body.length; i++) {
    const c = body[i]
    if (c === '*') {
      if (body[i + 1] === '*') {
        // «**/» — любое число каталогов, «/**» — всё внутри.
        if (body[i + 2] === '/') {
          re += '(?:[^/]+/)*'
          i += 2
        } else {
          re += '.*'
          i += 1
        }
        continue
      }
      re += '[^/]*'
      continue
    }
    if (c === '?') {
      re += '[^/]'
      continue
    }
    re += escapeRe(c)
  }
  const middleSlash = body.includes('/')
  const prefix = anchored || middleSlash ? '^' : '^(?:.*/)?'
  // Совпадение каталога распространяется на всё, что в нём лежит:
  // «node_modules» прячет и node_modules/a/b.js.
  return new RegExp(prefix + re + '(?:/.*)?$')
}

/** Разбор содержимого одного .gitignore. base — каталог, где он лежит. */
export function parseGitignore(base: string, text: string): IgnoreSet {
  const rules: Rule[] = []
  for (const raw of text.split('\n')) {
    let line = raw.replace(/\r$/, '')
    if (line.trim() === '' || line.startsWith('#')) continue
    // Пробелы в конце игнорируются, если не экранированы.
    line = line.replace(/(?<!\\)\s+$/, '')
    if (line === '') continue
    const negate = line.startsWith('!')
    if (negate) line = line.slice(1)
    const dirOnly = line.endsWith('/')
    rules.push({ re: compile(line), negate, dirOnly })
  }
  return { base, rules }
}

/**
 * Пропускать ли путь. rel — путь относительно корня загружаемой папки
 * («src/app.py»), isDir здесь всегда false: грузятся файлы, а правило
 * «только каталог» проверяется по тому, есть ли в остатке пути «/».
 *
 * Побеждает последнее подошедшее правило — как в git.
 */
export function ignoredBy(sets: IgnoreSet[], rel: string): boolean {
  let ignored = false
  for (const set of sets) {
    if (set.base !== '' && !rel.startsWith(set.base)) continue
    const sub = set.base === '' ? rel : rel.slice(set.base.length)
    for (const rule of set.rules) {
      if (rule.dirOnly) {
        // Каталог — только если совпадение приходится на часть пути,
        // за которой ещё что-то есть.
        const dirs = sub.split('/').slice(0, -1)
        let hit = false
        for (let i = 0; i < dirs.length; i++) {
          if (rule.re.test(dirs.slice(0, i + 1).join('/'))) {
            hit = true
            break
          }
        }
        if (hit) ignored = !rule.negate
        continue
      }
      if (rule.re.test(sub)) ignored = !rule.negate
    }
  }
  return ignored
}

/** Скрытый путь: любой сегмент начинается с точки (.git, .env, .venv/x). */
export function isHiddenPath(rel: string): boolean {
  return rel.split('/').some((seg) => seg.startsWith('.'))
}

/**
 * Собирает правила из самих загружаемых файлов: каждый .gitignore внутри
 * набора читается и применяется к своему поддереву. Возвращает наборы,
 * отсортированные от корня вглубь — чтобы вложенный .gitignore
 * применялся последним и мог отменить правило верхнего.
 */
export async function collectIgnoreSets(files: { rel: string; file: File }[]): Promise<IgnoreSet[]> {
  const found = files.filter((p) => p.rel === '.gitignore' || p.rel.endsWith('/.gitignore'))
  const sets: IgnoreSet[] = []
  for (const p of found) {
    const base = p.rel.slice(0, p.rel.length - '.gitignore'.length)
    try {
      sets.push(parseGitignore(base, await p.file.text()))
    } catch {
      // Нечитаемый .gitignore — просто не фильтруем по нему.
    }
  }
  sets.sort((a, b) => a.base.length - b.base.length)
  return sets
}
