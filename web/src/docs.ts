import { useEffect, useState } from 'react'
import { api } from './api'
import docsMap from './docsMap.json'

/**
 * Справка — страницы сайта документации. Адрес сайта настраивается в «О
 * системе» (по умолчанию — сайт проекта; можно свою копию). Какая
 * страница и раздел для какого места интерфейса — docsMap.json (его же
 * проверяет тест Go: разделы должны существовать на обоих языках).
 */

export interface DocsTarget {
  page: string
  ru?: string
  en?: string
}

export const DOCS = docsMap as Record<string, DocsTarget>

export const DEFAULT_DOCS_URL = 'https://piqab.github.io/nkt/'

/** Якорь заголовка так, как его делает vitepress: NFKD без надстрочных
 * знаков («Сайты» → «саиты»), знаки → «-», нижний регистр. */
export function docsSlug(heading: string): string {
  return heading
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[\u0000-\u001f]/g, '')
    .replace(/[\s~`!@#$%^&*()\-_+=[\]{}|\;:"'“”‘’<>,.?/]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/^(\d)/, '_$1')
    .toLowerCase()
}

export function docsURL(base: string, lang: string, target: DocsTarget): string {
  const en = lang.startsWith('en')
  const heading = en ? target.en : target.ru
  return base + (en ? 'en/' : '') + 'guide/' + target.page + (heading ? '#' + docsSlug(heading) : '')
}

let cached: string | null = null
const listeners = new Set<(url: string) => void>()

/** Адрес сайта справки: у машины хаба (хаб) или у самого хоста. */
export function docsSettingsPath(isHub: boolean): string {
  return isHub ? '/hosts/local/ui/docs' : '/ui/docs'
}

export function setDocsBase(url: string) {
  cached = url
  listeners.forEach((l) => l(url))
}

export function useDocsBase(isHub: boolean): string {
  const [url, setUrl] = useState(cached ?? DEFAULT_DOCS_URL)
  useEffect(() => {
    listeners.add(setUrl)
    if (cached === null) {
      api<{ url: string }>(docsSettingsPath(isHub))
        .then((r) => setDocsBase(r.url || DEFAULT_DOCS_URL))
        .catch(() => setDocsBase(DEFAULT_DOCS_URL))
    }
    return () => {
      listeners.delete(setUrl)
    }
  }, [isHub])
  return url
}
