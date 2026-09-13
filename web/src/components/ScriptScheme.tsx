import { useTranslation } from 'react-i18next'

/** Схема сценария — появится следующей фазой; пока текст. */
export default function ScriptScheme({ content }: { content: string }) {
  const { t } = useTranslation()
  void content
  return <p className="small muted">{t('scripts.schemeSoon')}</p>
}
