import { Modal } from 'antd'
import i18n from '../i18n'

/**
 * Подтверждение опасного действия.
 *
 * Раньше это был window.confirm — родное окно браузера посреди интерфейса
 * на antd: чужой вид, блокирует вкладку целиком (в том числе живой
 * терминал и опрос состояния), не поддаётся ни теме, ни переводу кнопок, а
 * часть браузеров подавляет такие окна вовсе, и тогда действие молча
 * выполнялось без спроса.
 *
 * Возвращает промис: true — подтвердили. Такой вид позволил заменить
 * `if (!(await confirmAction(x))) return` на `if (!(await confirmAction(x))) return`
 * без переписывания вызывающего кода.
 */
export function confirmAction(
  content: string,
  opts: { title?: string; okText?: string; danger?: boolean } = {},
): Promise<boolean> {
  return new Promise((resolve) => {
    Modal.confirm({
      title: opts.title ?? i18n.t('common.confirmTitle'),
      content,
      okText: opts.okText ?? i18n.t('common.confirmOk'),
      cancelText: i18n.t('common.cancel'),
      okButtonProps: { danger: opts.danger ?? true },
      // Ширина против умолчания antd: тексты подтверждений здесь длинные —
      // они перечисляют последствия, а не спрашивают «вы уверены?».
      width: 520,
      onOk: () => resolve(true),
      onCancel: () => resolve(false),
    })
  })
}
