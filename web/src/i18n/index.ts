import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import ru from './ru.json'
import en from './en.json'

export type Lang = 'ru' | 'en'

const LANG_KEY = 'nkt-lang'

/** Mirrors App.tsx's own nkt-theme persistence — same localStorage-first,
 * OS-preference-fallback pattern as useTheme, so the two behave
 * consistently rather than one remembering a choice and the other not. */
export function getStoredLang(): Lang {
  const stored = localStorage.getItem(LANG_KEY)
  if (stored === 'en' || stored === 'ru') return stored
  return navigator.language.toLowerCase().startsWith('en') ? 'en' : 'ru'
}

export function setStoredLang(lang: Lang): void {
  localStorage.setItem(LANG_KEY, lang)
}

void i18n.use(initReactI18next).init({
  resources: {
    ru: { translation: ru },
    en: { translation: en },
  },
  lng: getStoredLang(),
  fallbackLng: 'ru',
  // React already escapes interpolated values when rendering — i18next's
  // own HTML-escaping on top would double-escape anything with special
  // characters (e.g. a hostname containing "&").
  interpolation: { escapeValue: false },
})

export default i18n
