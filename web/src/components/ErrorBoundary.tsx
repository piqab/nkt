import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from 'antd'
import i18n from '../i18n'
import { api } from '../api'

/**
 * Перехват ошибок отрисовки вокруг каждого раздела.
 *
 * Без него исключение в отрисовке размонтирует всё дерево React, и вместо
 * интерфейса остаётся белая страница: ни меню, ни навигации, ни намёка на
 * то, что случилось. Именно так «падал» раздел «Диски», когда сервер
 * прислал null вместо пустого списка — и понять это по белому экрану
 * было нельзя.
 *
 * Границей обёрнут каждый раздел по отдельности: сломавшийся показывает
 * карточку с ошибкой, а меню и остальные разделы продолжают работать.
 */
export default class ErrorBoundary extends Component<
  { children: ReactNode; section?: string },
  { error: Error | null; info: string }
> {
  state = { error: null as Error | null, info: '' }

  static getDerivedStateFromError(error: Error) {
    return { error, info: '' }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    this.setState({ info: info.componentStack ?? '' })
    // Отправляется на хост, чтобы ответ на вопрос «что упало» остался в
    // журнале, а не только на экране у того, кто это увидел. Ошибка
    // отправки игнорируется сознательно: если сломано и это, показать
    // карточку всё равно важнее.
    void api('/ui-error', {
      method: 'POST',
      body: {
        section: this.props.section ?? location.pathname,
        message: error.message,
        stack: (error.stack ?? '').slice(0, 4000),
        component_stack: (info.componentStack ?? '').slice(0, 4000),
        url: location.href,
      },
    }).catch(() => {})
  }

  render() {
    const { error, info } = this.state
    if (!error) return this.props.children

    const text = [error.message, error.stack, info].filter(Boolean).join('\n\n')
    return (
      <div className="card" style={{ padding: '1rem' }}>
        <h2 style={{ marginTop: 0 }}>{i18n.t('errorBoundary.title')}</h2>
        <p className="small">{i18n.t('errorBoundary.hint')}</p>
        <pre
          className="small mono"
          style={{
            whiteSpace: 'pre-wrap',
            maxHeight: '18rem',
            overflowY: 'auto',
            background: 'var(--wash)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-sm)',
            padding: '0.5rem 0.6rem',
          }}
        >
          {text}
        </pre>
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" onClick={() => this.setState({ error: null, info: '' })}>
            {i18n.t('errorBoundary.retry')}
          </Button>
          <Button onClick={() => window.location.reload()}>{i18n.t('errorBoundary.reload')}</Button>
          <Button
            onClick={() => {
              try {
                void navigator.clipboard?.writeText(text)
              } catch {
                // Буфер обмена недоступен без HTTPS — текст и так на экране.
              }
            }}
          >
            {i18n.t('common.copy')}
          </Button>
        </div>
      </div>
    )
  }
}
