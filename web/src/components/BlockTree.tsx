import { useState } from 'react'
import { api, qs, useApi } from '../api'
import type { BlockKind, ConfigBlock, Me, WriteResult } from '../types'
import { Banner, Modal, Spinner } from './ui'

const KIND_LABEL: Record<BlockKind, string> = {
  server: 'server',
  location: 'location',
  upstream: 'upstream',
  frontend: 'frontend',
  backend: 'backend',
  listen: 'listen',
  global: 'global',
  defaults: 'defaults',
}

// What a "+" button can create at the top level of each service's file — a
// deliberately safe subset (v1 never creates haproxy global/defaults, and
// never creates a nested nginx block from here; location is only ever
// created from its parent server's own "+ location" button).
function creatableKinds(service: string): BlockKind[] {
  if (service === 'nginx') return ['server', 'upstream']
  if (service === 'haproxy') return ['frontend', 'backend', 'listen']
  return []
}

interface ModalState {
  mode: 'create' | 'edit'
  kind: BlockKind
  block?: ConfigBlock // set for edit/delete — carries start_line/end_line
  parentEndLine?: number // set only when creating a location inside a server
}

/** Structural view of one nginx/haproxy config file — the block tree, with
 * create/edit/delete per block. Each write is a line-range splice against
 * the file's raw text (see internal/parse/blocks.go), validated and rolled
 * back on failure exactly like the plain text editor's save. */
export default function BlockTree({
  path,
  service,
  sha256,
  me,
  onSaved,
}: {
  path: string
  service: string
  sha256: string
  me: Me
  onSaved: () => void
}) {
  const blocks = useApi<{ blocks: ConfigBlock[] }>(`/configs/blocks${qs({ path })}`)
  const [selected, setSelected] = useState<string | null>(null)
  const [modal, setModal] = useState<ModalState | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [apply, setApply] = useState(false)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)

  const canControl = me.is_admin && me.allow_mutations
  const list = blocks.data?.blocks ?? []
  const selectedBlock = findBlock(list, selected)

  function openCreate(kind: BlockKind, parentEndLine?: number) {
    setDraft('')
    setNote('')
    setApply(false)
    setModal({ mode: 'create', kind, parentEndLine })
  }

  function openEdit(block: ConfigBlock) {
    setDraft(block.raw)
    setNote('')
    setApply(false)
    setModal({ mode: 'edit', kind: block.kind, block })
  }

  async function writeBlock(body: Record<string, unknown>) {
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<WriteResult>('/configs/blocks', {
        method: 'POST',
        body: { path, expected_sha256: sha256, ...body },
      })
      setNotice({ kind: res.rolled_back ? 'error' : 'info', text: res.message })
      blocks.reload()
      onSaved()
      return true
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  async function submitModal() {
    if (!modal) return
    const ok = await writeBlock({
      op: modal.mode,
      kind: modal.kind,
      start_line: modal.block?.start_line,
      end_line: modal.block?.end_line,
      parent_end_line: modal.parentEndLine,
      content: draft,
      note,
      apply,
    })
    if (ok) setModal(null)
  }

  async function remove(block: ConfigBlock) {
    if (!window.confirm(`Удалить ${KIND_LABEL[block.kind]}${block.name ? ' ' + block.name : ''}?`)) return
    const ok = await writeBlock({
      op: 'delete', kind: block.kind, start_line: block.start_line, end_line: block.end_line, apply: false,
    })
    if (ok) setSelected(null)
  }

  if (blocks.loading && !blocks.data) return <div className="chart-empty">Загрузка структуры файла…</div>
  if (blocks.error) return <Banner kind="error">{blocks.error}</Banner>

  return (
    <div className="col">
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}

      {canControl && creatableKinds(service).length > 0 && (
        <div className="row" style={{ gap: '0.4rem', marginBottom: '0.5rem' }}>
          {creatableKinds(service).map((kind) => (
            <button key={kind} className="ghost" onClick={() => openCreate(kind)}>
              + {KIND_LABEL[kind]}
            </button>
          ))}
        </div>
      )}

      {list.length === 0 ? (
        <div className="chart-empty">В файле не найдено ни одного узнаваемого блока.</div>
      ) : (
        <div className="col" style={{ gap: '0.15rem' }}>
          {list.map((b) => (
            <BlockRow
              key={b.id}
              block={b}
              depth={0}
              selected={selected}
              onSelect={setSelected}
              canControl={canControl}
              onAddLocation={service === 'nginx' ? (parent) => openCreate('location', parent.end_line) : undefined}
            />
          ))}
        </div>
      )}

      {selectedBlock && (
        <div className="card" style={{ marginTop: '0.6rem' }}>
          <div className="card-head">
            <div>
              <h2>
                {KIND_LABEL[selectedBlock.kind]}
                {selectedBlock.name ? ` ${selectedBlock.name}` : ''}
              </h2>
              <p>
                строки {selectedBlock.start_line}–{selectedBlock.end_line}
              </p>
            </div>
            <div className="row">
              {canControl && (
                <button onClick={() => openEdit(selectedBlock)} disabled={busy}>
                  изменить
                </button>
              )}
              {canControl && selectedBlock.editable && (
                <button className="ghost" onClick={() => remove(selectedBlock)} disabled={busy}>
                  {busy && <Spinner />}
                  удалить
                </button>
              )}
              <button className="ghost" onClick={() => setSelected(null)}>
                закрыть
              </button>
            </div>
          </div>
          {!selectedBlock.editable && (
            <p className="small muted">
              Создание и удаление недоступны для этого раздела — только правка.
            </p>
          )}
          <pre className="diff">{selectedBlock.raw}</pre>
        </div>
      )}

      {modal && (
        <Modal
          title={modal.mode === 'create' ? `Новый блок: ${KIND_LABEL[modal.kind]}` : `Правка: ${KIND_LABEL[modal.kind]}`}
          onClose={() => setModal(null)}
        >
          <div className="col">
            <textarea value={draft} onChange={(e) => setDraft(e.target.value)} rows={12} spellCheck={false} />
            <div className="filters" style={{ marginTop: '0.5rem' }}>
              <label style={{ flex: 1, minWidth: '14rem' }}>
                Комментарий к правке
                <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="зачем меняем" />
              </label>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                <input
                  type="checkbox"
                  checked={apply}
                  onChange={(e) => setApply(e.target.checked)}
                  style={{ width: 'auto' }}
                />
                перезагрузить сервис после сохранения
              </label>
            </div>
            <div className="row" style={{ marginTop: '0.5rem' }}>
              <button className="primary" onClick={submitModal} disabled={busy || !draft.trim()}>
                {busy && <Spinner />}
                {busy ? 'Сохраняю…' : 'Сохранить'}
              </button>
              <button className="ghost" onClick={() => setModal(null)} disabled={busy}>
                Отмена
              </button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  )
}

function findBlock(list: ConfigBlock[], id: string | null): ConfigBlock | null {
  if (!id) return null
  for (const b of list) {
    if (b.id === id) return b
    const child = findBlock(b.children ?? [], id)
    if (child) return child
  }
  return null
}

function BlockRow({
  block,
  depth,
  selected,
  onSelect,
  canControl,
  onAddLocation,
}: {
  block: ConfigBlock
  depth: number
  selected: string | null
  onSelect: (id: string) => void
  canControl: boolean
  onAddLocation?: (block: ConfigBlock) => void
}) {
  const firstLine = block.raw.split('\n')[0]?.trim() ?? ''
  return (
    <div>
      <div
        className={`block-row${selected === block.id ? ' selected' : ''}`}
        style={{ paddingLeft: `${depth * 1.25}rem` }}
        onClick={() => onSelect(block.id)}
      >
        <span className="badge sev-info">
          <span className="badge-dot" />
          {block.kind}
        </span>
        <span className="mono small">{block.name || firstLine}</span>
        <span className="small muted">
          строки {block.start_line}–{block.end_line}
        </span>
        {canControl && block.kind === 'server' && onAddLocation && (
          <button
            className="ghost small block-row-action"
            onClick={(e) => {
              e.stopPropagation()
              onAddLocation(block)
            }}
          >
            + location
          </button>
        )}
      </div>
      {block.children?.map((child) => (
        <BlockRow
          key={child.id}
          block={child}
          depth={depth + 1}
          selected={selected}
          onSelect={onSelect}
          canControl={canControl}
          onAddLocation={onAddLocation}
        />
      ))}
    </div>
  )
}
