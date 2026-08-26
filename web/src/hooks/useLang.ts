import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getStoredLang, setStoredLang, type Lang } from '../i18n'

/** Mirrors App.tsx's own useTheme localStorage-first persistence (see
 * i18n/index.ts for why getStoredLang isn't just "always start from
 * i18n's own default"). Language switches are instant
 * (i18next.changeLanguage doesn't reload anything) — the returned setter
 * both persists the choice and applies it. Lives in its own module (not
 * App.tsx) so Login.tsx — rendered before App.tsx's own Shell, for the
 * unauthenticated case — can use the same hook without an App↔Login
 * circular import. */
export function useLang(): [Lang, (l: Lang) => void] {
  const { i18n } = useTranslation()
  const [lang, setLangState] = useState<Lang>(() => getStoredLang())
  const setLang = useCallback(
    (l: Lang) => {
      setStoredLang(l)
      setLangState(l)
      void i18n.changeLanguage(l)
    },
    [i18n],
  )
  return [lang, setLang]
}
