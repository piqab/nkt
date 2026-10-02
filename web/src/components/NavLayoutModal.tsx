import { useState } from 'react'
import { Button, Checkbox, Space } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined, HolderOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { api, useApi } from '../api'
import { applyNavLayout, type NavLayout } from '../navLayout'
import { Banner, Card, DiffView, Loading, Modal, formatDateTime } from './ui'
import { unifiedDiff } from './textDiff'
import { HUB_NAV, NAV_ITEMS } from '../navItems'

interface NavItem {
  key: string
  label: string
  icon: ReactNode
}

interface NavState {
  layout: NavLayout
  history: { ts: string; author?: string; order: string[]; hidden: string[] }[]
  unhideable?: string[]
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** Карточка «Меню» в «О системе» хаба: порядок разделов хаба и разделы
 * хоста — для всех хостов сразу. */
export function NavLayoutCard({ admin }: { admin: boolean }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState<'hub' | 'host' | null>(null)
  if (!admin) return null
  return (
    <Card title={t('navLayout.title')} subtitle={t('navLayout.hint')}>
      <Space wrap>
        <Button onClick={() => setOpen('hub')}>{t('navLayout.editHub')}</Button>
        <Button onClick={() => setOpen('host')}>{t('navLayout.editHost')}</Button>
      </Space>
      {open && <NavLayoutModal kind={open} onClose={() => setOpen(null)} />}
    </Card>
  )
}

/** Окно раскладки: перетаскивание (и стрелки), у меню хоста — галочка
 * «показывать»; дифф перед записью, история версий, сброс к умолчанию. */
function NavLayoutModal({ kind, onClose }: { kind: 'hub' | 'host'; onClose: () => void }) {
  const { t } = useTranslation()
  const state = useApi<NavState>(`/hub/ui/nav/${kind}`)
  const items: NavItem[] =
    kind === 'hub'
      ? HUB_NAV.map((i) => ({ key: i.key, label: t(i.labelKey), icon: i.icon }))
      : NAV_ITEMS.map((i) => ({ key: i.to, label: t(i.labelKey), icon: i.icon }))
  const defaults: NavLayout = { order: items.map((i) => i.key), hidden: [] }
  const [draft, setDraft] = useState<NavLayout | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!state.data) {
    return (
      <Modal title={t(kind === 'hub' ? 'navLayout.editHub' : 'navLayout.editHost')} onClose={onClose}>
        {state.error ? <Banner kind="error">{errText(state.error)}</Banner> : <Loading />}
      </Modal>
    )
  }
  const unhideable = state.data.unhideable ?? []
  // Сохранённая раскладка в полном виде: все разделы интерфейса по порядку.
  const full = (l: NavLayout): NavLayout => ({ order: applyNavLayout(items, (i) => i.key, l, true).map((i) => i.key), hidden: l.hidden.filter((k) => items.some((i) => i.key === k)) })
  const saved = full(state.data.layout)
  const cur = draft ?? saved
  const label = (k: string) => items.find((i) => i.key === k)?.label ?? k
  const text = (l: NavLayout) => l.order.map((k, n) => `${n + 1}. ${label(k)}${l.hidden.includes(k) ? ` — ${t('navLayout.hiddenMark')}` : ''}`).join('\n') + '\n'
  const diff = unifiedDiff(text(saved), text(cur), t('tokens.saved'), t('tokens.draft'))
  const changed = diff.trim() !== ''

  const move = (key: string, to: number) => {
    const order = cur.order.filter((k) => k !== key)
    order.splice(Math.max(0, Math.min(to, order.length)), 0, key)
    setDraft({ ...cur, order })
  }
  const toggle = (key: string, show: boolean) =>
    setDraft({ ...cur, hidden: show ? cur.hidden.filter((k) => k !== key) : [...cur.hidden, key] })

  async function save() {
    setBusy(true)
    setError(null)
    try {
      await api(`/hub/ui/nav/${kind}`, { method: 'PUT', body: cur })
      window.dispatchEvent(new Event('nkt-nav-changed'))
      await state.reload()
      setDraft(null)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t(kind === 'hub' ? 'navLayout.editHub' : 'navLayout.editHost')} onClose={onClose} width={640}>
      <p className="small muted">{t(kind === 'hub' ? 'navLayout.hubHint' : 'navLayout.hostHint')}</p>
      <div className="nav-layout-list">
        {cur.order.map((k, n) => {
          const item = items.find((i) => i.key === k)
          const hidden = cur.hidden.includes(k)
          return (
            <div
              key={k}
              className={'nav-layout-row' + (dragging === k ? ' dragging' : '') + (hidden ? ' hidden-item' : '')}
              draggable
              onDragStart={(e) => {
                setDragging(k)
                e.dataTransfer.effectAllowed = 'move'
              }}
              onDragOver={(e) => {
                e.preventDefault()
                if (dragging && dragging !== k) move(dragging, n)
              }}
              onDragEnd={() => setDragging(null)}
            >
              <HolderOutlined className="muted nav-layout-handle" aria-hidden="true" />
              <span className="nav-layout-icon">{item?.icon}</span>
              <span className="nav-layout-label">{label(k)}</span>
              {kind === 'host' && (
                <Checkbox checked={!hidden} disabled={unhideable.includes(k)} onChange={(e) => toggle(k, e.target.checked)}>
                  {t('navLayout.show')}
                </Checkbox>
              )}
              <Button size="small" type="text" icon={<ArrowUpOutlined />} aria-label={t('navLayout.up')} disabled={n === 0} onClick={() => move(k, n - 1)} />
              <Button size="small" type="text" icon={<ArrowDownOutlined />} aria-label={t('navLayout.down')} disabled={n === cur.order.length - 1} onClick={() => move(k, n + 1)} />
            </div>
          )
        })}
      </div>
      {changed && (
        <>
          <p className="small muted" style={{ marginTop: '0.6rem' }}>{t('tokens.diffHint')}</p>
          <DiffView text={diff} />
        </>
      )}
      {error && <Banner kind="error">{error}</Banner>}
      <Space wrap style={{ marginTop: '0.6rem' }}>
        <Button type="primary" loading={busy} disabled={!changed} onClick={() => void save()}>
          {t('tokens.save')}
        </Button>
        <Button disabled={text(cur) === text(defaults)} onClick={() => setDraft(defaults)}>
          {t('navLayout.reset')}
        </Button>
        {draft && (
          <Button type="link" onClick={() => setDraft(null)}>
            {t('navLayout.discard')}
          </Button>
        )}
      </Space>
      {state.data.history.length > 0 && (
        <div style={{ marginTop: '0.9rem' }}>
          <div className="small" style={{ marginBottom: '0.3rem' }}>
            <strong>{t('navLayout.history')}</strong>
          </div>
          <div className="col" style={{ gap: '0.25rem' }}>
            {state.data.history.slice(0, 10).map((v, i) => (
              <div key={i} className="row small" style={{ gap: '0.5rem' }}>
                <span className="muted nowrap">{formatDateTime(v.ts)}</span>
                {v.author && <span className="muted">{v.author}</span>}
                <Button size="small" type="link" onClick={() => setDraft(full({ order: v.order ?? [], hidden: v.hidden ?? [] }))}>
                  {t('navLayout.restore')}
                </Button>
              </div>
            ))}
          </div>
        </div>
      )}
    </Modal>
  )
}
