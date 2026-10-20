import { useState } from 'react'
import { Button, Checkbox, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, Modal, formatRelative } from './ui'

/** Экспериментальные функции хаба: что показывать в интерфейсе. Галочки
 * только прячут — сами функции работают. Правка — окном с диффом и
 * историей; после записи страница перечитывается (меняется меню). */
interface Experimental {
  map: boolean
  clusters: boolean
}
interface State {
  value: Experimental
  history: { ts: string; author?: string; value: Experimental }[]
}

const KEYS: (keyof Experimental)[] = ['map', 'clusters']

export function ExperimentalCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const st = useApi<State>('/hub/experimental')
  const [edit, setEdit] = useState(false)
  const v = st.data?.value
  return (
    <Card
      title={t('about.expTitle')}
      subtitle={t('about.expHint')}
      actions={
        admin && (
          <Button size="small" disabled={!v} onClick={() => setEdit(true)}>
            {t('common.edit')}
          </Button>
        )
      }
    >
      {v && (
        <div className="col small" style={{ gap: '0.3rem' }}>
          {KEYS.map((k) => (
            <div key={k} className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
              {v[k] ? <Tag color="success">{t('about.expOn')}</Tag> : <Tag>{t('about.expOff')}</Tag>}
              <span>{t(`about.exp.${k}`)}</span>
            </div>
          ))}
        </div>
      )}
      {edit && st.data && <ExperimentalModal state={st.data} onClose={() => setEdit(false)} />}
    </Card>
  )
}

function line(t: (k: string) => string, k: keyof Experimental, on: boolean) {
  return `${t(`about.exp.${k}`)}: ${on ? t('about.expOn') : t('about.expOff')}`
}

function ExperimentalModal({ state, onClose }: { state: State; onClose: () => void }) {
  const { t } = useTranslation()
  const [next, setNext] = useState<Experimental>(state.value)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const changed = KEYS.some((k) => next[k] !== state.value[k])
  const diff = KEYS.filter((k) => next[k] !== state.value[k])
    .map((k) => `-${line(t, k, state.value[k])}\n+${line(t, k, next[k])}`)
    .join('\n')
  return (
    <Modal title={t('about.expTitle')} onClose={onClose} width={560}>
      <div className="col small" style={{ gap: '0.5rem' }}>
        <span className="muted">{t('about.expHint')}</span>
        {KEYS.map((k) => (
          <div key={k}>
            <Checkbox checked={next[k]} onChange={(e) => setNext({ ...next, [k]: e.target.checked })}>
              {t(`about.exp.${k}`)}
            </Checkbox>
            <div className="muted" style={{ paddingLeft: '1.5rem' }}>
              {t(`about.expDesc.${k}`)}
            </div>
          </div>
        ))}
        {changed && <DiffView text={diff} />}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button
            type="primary"
            disabled={!changed}
            loading={busy}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                await api('/hub/experimental', { method: 'PUT', body: next })
                // Меню и разделы собираются при загрузке — перечитать.
                window.location.reload()
              } catch (e) {
                setError(e instanceof Error ? e.message : String(e))
                setBusy(false)
              }
            }}
          >
            {t('common.save')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
        {state.history.length > 0 && (
          <div className="col" style={{ gap: '0.2rem', marginTop: '0.4rem' }}>
            <strong>{t('about.expHistory')}</strong>
            {state.history.slice(0, 10).map((h) => (
              <div key={h.ts} className="row" style={{ gap: '0.5rem', flexWrap: 'wrap' }}>
                <span className="muted">
                  {formatRelative(h.ts)}
                  {h.author ? ` · ${h.author}` : ''}
                </span>
                <span>{KEYS.map((k) => line(t, k, h.value[k])).join('; ')}</span>
                <Button size="small" type="link" onClick={() => setNext(h.value)}>
                  {t('about.expRestore')}
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>
    </Modal>
  )
}
