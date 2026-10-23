import { useState } from 'react'
import { Button, Input, Tooltip } from 'antd'
import { DeleteOutlined, EditOutlined, FolderAddOutlined, HolderOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api } from '../../api'
import { blurText } from '../../privacy'
import { confirmAction } from '../confirm'
import { Banner } from '../ui'

/**
 * Избранное ассистента — справа в чате: разделы с сохранёнными вопросами.
 * Щелчок по вопросу отправляет его; перетаскивание меняет порядок и
 * переносит между разделами; разделы заводятся, переименовываются и
 * удаляются тут же. Хранится на хабе у пользователя — одной записью.
 */

export interface FavItem {
  id: string
  text: string
}
export interface FavSection {
  id: string
  title: string
  items: FavItem[]
}
export interface FavData {
  sections: FavSection[]
}

export function newID(): string {
  return Math.random().toString(36).slice(2, 10) + Date.now().toString(36)
}

/** Добавить вопрос в первый раздел (нет разделов — заводится «Общее»). */
export function withFavorite(data: FavData, text: string, defaultTitle: string): FavData {
  const sections = data.sections.length > 0 ? data.sections.map((s) => ({ ...s, items: [...s.items] })) : [{ id: newID(), title: defaultTitle, items: [] }]
  if (sections.some((s) => s.items.some((i) => i.text === text))) return { sections }
  sections[0].items.unshift({ id: newID(), text })
  return { sections }
}

export function hasFavorite(data: FavData | undefined, text: string) {
  return !!data?.sections.some((s) => s.items.some((i) => i.text === text))
}

type Drag = { item: string } | { section: string }

export function FavoritesPanel({ data, onChange, onSend, disabled }: { data: FavData; onChange: (next: FavData) => Promise<void>; onSend: (text: string) => void; disabled: boolean }) {
  const { t } = useTranslation()
  const [drag, setDrag] = useState<Drag | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ id: string; title: string } | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function save(next: FavData) {
    setError(null)
    try {
      await onChange(next)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }
  const clone = (): FavSection[] => data.sections.map((s) => ({ ...s, items: [...s.items] }))

  // Перенос вопроса: перед другим вопросом или в конец раздела.
  function dropItem(itemID: string, toSection: string, beforeItem?: string) {
    const secs = clone()
    let moved: FavItem | undefined
    for (const s of secs) {
      const i = s.items.findIndex((x) => x.id === itemID)
      if (i >= 0) moved = s.items.splice(i, 1)[0]
    }
    const target = secs.find((s) => s.id === toSection)
    if (!moved || !target) return
    const at = beforeItem ? target.items.findIndex((x) => x.id === beforeItem) : -1
    if (at >= 0) target.items.splice(at, 0, moved)
    else target.items.push(moved)
    void save({ sections: secs })
  }
  function dropSection(secID: string, beforeSec: string) {
    if (secID === beforeSec) return
    const secs = clone()
    const i = secs.findIndex((s) => s.id === secID)
    if (i < 0) return
    const [moved] = secs.splice(i, 1)
    const at = secs.findIndex((s) => s.id === beforeSec)
    secs.splice(at < 0 ? secs.length : at, 0, moved)
    void save({ sections: secs })
  }
  function onDrop(section: string, beforeItem?: string) {
    if (!drag) return
    if ('item' in drag) dropItem(drag.item, section, beforeItem)
    else if (!beforeItem) dropSection(drag.section, section)
    setDrag(null)
    setOver(null)
  }

  return (
    <div className="chat-favorites col" style={{ gap: '0.5rem' }}>
      <div className="row spread" style={{ alignItems: 'center' }}>
        <strong className="small">{t('assistant.fav.title')}</strong>
        <Tooltip title={t('assistant.fav.addSection')}>
          <Button
            size="small"
            type="text"
            icon={<FolderAddOutlined />}
            aria-label={t('assistant.fav.addSection')}
            onClick={() => void save({ sections: [...clone(), { id: newID(), title: t('assistant.fav.newSection'), items: [] }] })}
          />
        </Tooltip>
      </div>
      {error && (
        <Banner kind="error" onClose={() => setError(null)}>
          {error}
        </Banner>
      )}
      {data.sections.length === 0 && <span className="small muted">{t('assistant.fav.empty')}</span>}
      {data.sections.map((s) => (
        <div
          key={s.id}
          className={`chat-fav-section${over === s.id ? ' is-over' : ''}`}
          onDragOver={(e) => {
            e.preventDefault()
            setOver(s.id)
          }}
          onDragLeave={() => setOver(null)}
          onDrop={(e) => {
            e.preventDefault()
            onDrop(s.id)
          }}
        >
          <div className="row spread chat-fav-head" draggable onDragStart={() => setDrag({ section: s.id })}>
            {editing?.id === s.id ? (
              <Input
                size="small"
                autoFocus
                value={editing.title}
                maxLength={100}
                onChange={(e) => setEditing({ id: s.id, title: e.target.value })}
                onPressEnter={() => {
                  const title = editing.title.trim()
                  setEditing(null)
                  if (title) void save({ sections: clone().map((x) => (x.id === s.id ? { ...x, title } : x)) })
                }}
                onBlur={() => setEditing(null)}
              />
            ) : (
              <span className="small chat-fav-title">
                <HolderOutlined className="muted" /> {s.title} <span className="muted">· {s.items.length}</span>
              </span>
            )}
            <span className="row" style={{ gap: 0 }}>
              <Button size="small" type="text" icon={<EditOutlined />} aria-label={t('assistant.fav.rename')} onClick={() => setEditing({ id: s.id, title: s.title })} />
              <Button
                size="small"
                type="text"
                icon={<DeleteOutlined />}
                aria-label={t('assistant.fav.deleteSection')}
                onClick={async () => {
                  if (s.items.length > 0 && !(await confirmAction(t('assistant.fav.deleteSectionConfirm', { title: s.title, count: s.items.length })))) return
                  void save({ sections: clone().filter((x) => x.id !== s.id) })
                }}
              />
            </span>
          </div>
          {s.items.map((it) => (
            <div
              key={it.id}
              className="chat-fav-item row"
              draggable
              onDragStart={(e) => {
                e.stopPropagation()
                setDrag({ item: it.id })
              }}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault()
                e.stopPropagation()
                onDrop(s.id, it.id)
              }}
            >
              <HolderOutlined className="muted" />
              <button type="button" className="chat-fav-text" disabled={disabled} title={t('assistant.fav.sendHint')} onClick={() => onSend(it.text)}>
                {blurText(it.text)}
              </button>
              <Button
                size="small"
                type="text"
                icon={<DeleteOutlined />}
                aria-label={t('assistant.fav.delete')}
                onClick={() => void save({ sections: clone().map((x) => (x.id === s.id ? { ...x, items: x.items.filter((i) => i.id !== it.id) } : x)) })}
              />
            </div>
          ))}
        </div>
      ))}
    </div>
  )
}

/** Сохранить избранное на хабе. */
export async function saveFavorites(next: FavData): Promise<FavData> {
  return api<FavData>('/hub/assistant/favorites', { method: 'PUT', body: next })
}
