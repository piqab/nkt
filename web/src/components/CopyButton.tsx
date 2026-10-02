import { useState } from 'react'
import { Button, Tooltip } from 'antd'
import { CheckOutlined, CopyOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'

/** Скопировать текст: Clipboard API, а без защищённого контекста (хаб по
 * http на адресе, не localhost) — через скрытое поле и execCommand. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // ниже — запасной путь
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(ta)
  return ok
}

/** Иконка «копировать» рядом со значением; после щелчка — галочка. */
export function CopyButton({ text, title }: { text: string; title?: string }) {
  const { t } = useTranslation()
  const [done, setDone] = useState(false)
  return (
    <Tooltip title={done ? t('common.copied') : (title ?? t('common.copy'))}>
      <Button
        size="small"
        type="text"
        aria-label={title ?? t('common.copy')}
        icon={done ? <CheckOutlined /> : <CopyOutlined />}
        onClick={async (e) => {
          e.stopPropagation()
          if (await copyText(text)) {
            setDone(true)
            setTimeout(() => setDone(false), 1500)
          }
        }}
      />
    </Tooltip>
  )
}
