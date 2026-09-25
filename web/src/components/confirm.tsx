import { Modal } from 'antd'
import { blurText } from '../privacy'
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
      title: blurText(opts.title ?? i18n.t('common.confirmTitle')),
      content: blurText(content),
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

/**
 * Подтверждение с галочкой-уточнением («удалить и диски»): одна кнопка
 * действия вместо двух похожих, а опасный вариант — осознанный выбор в
 * окне, по умолчанию выключенный. Возвращает null при отмене, иначе
 * состояние галочки.
 */
export function confirmWithOption(
  content: string,
  optionLabel: string,
  opts: { title?: string; okText?: string; optionHint?: string } = {},
): Promise<{ checked: boolean } | null> {
  return new Promise((resolve) => {
    let checked = false
    Modal.confirm({
      title: blurText(opts.title ?? i18n.t('common.confirmTitle')),
      content: (
        <div>
          <p>{blurText(content)}</p>
          <label style={{ display: 'flex', flexDirection: 'row', gap: '0.4rem', alignItems: 'flex-start' }}>
            <input type="checkbox" defaultChecked={false} onChange={(e) => (checked = e.target.checked)} style={{ marginTop: '0.2rem' }} />
            <span>
              {optionLabel}
              {opts.optionHint && <div className="small muted">{opts.optionHint}</div>}
            </span>
          </label>
        </div>
      ),
      okText: opts.okText ?? i18n.t('common.confirmOk'),
      cancelText: i18n.t('common.cancel'),
      okButtonProps: { danger: true },
      width: 560,
      onOk: () => resolve({ checked }),
      onCancel: () => resolve(null),
    })
  })
}
