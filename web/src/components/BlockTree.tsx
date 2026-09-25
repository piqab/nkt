import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AIConfigError } from './AIConfigError'
import { api, qs, useApi } from '../api'
import type { BlockKind, ConfigBlock, Me, WriteResult } from '../types'
import { Banner, CodeEditor, DiffView, Modal, Spinner } from './ui'
import { Button } from 'antd'
import { confirmAction } from './confirm'

const KIND_LABEL: Record<BlockKind, string> = {
  server: 'server',
  location: 'location',
  upstream: 'upstream',
  frontend: 'frontend',
  backend: 'backend',
  listen: 'listen',
  global: 'global',
  defaults: 'defaults',
  service: 'service',
  site: 'site',
  setting: 'setting',
  devices: 'devices',
  disk: 'disk',
  interface: 'interface',
  graphics: 'graphics',
  device: 'device',
  network: 'network',
  volume: 'volume',
  secret: 'secret',
  config: 'config',
}

// Шаблон нового устройства libvirt: пустая форма заставляла бы вспоминать
// схему XML наизусть; значения в шаблоне — самые обычные (virtio, qcow2).
const LIBVIRT_TEMPLATE: Partial<Record<BlockKind, string>> = {
  // Имена в шаблонах — заведомо новые: «backend»/«data» часто уже есть,
  // и повтор ключа в YAML тихо перетёр бы существующий элемент.
  network: "my_network:\n  driver: bridge",
  volume: "my_volume:\n  driver: local",
  secret: "my_secret:\n  file: ./my_secret.txt",
  config: "my_config:\n  file: ./my_config.conf",
  disk: "<disk type='file' device='disk'>\n  <driver name='qemu' type='qcow2'/>\n  <source file='/var/lib/libvirt/images/NAME.qcow2'/>\n  <target dev='vdb' bus='virtio'/>\n</disk>",
  interface: "<interface type='bridge'>\n  <source bridge='br0'/>\n  <model type='virtio'/>\n</interface>",
  graphics: "<graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>",
}

// What a "+" button can create at the top level of each service's file — a
// deliberately safe subset (v1 never creates haproxy global/defaults, and
// never creates a nested nginx block from here; location is only ever
// created from its parent server's own "+ location" button). Caddy sites
// are always flat too, same as haproxy's sections — no nested "+" button
// for a handle{}/route{} inside one.
function creatableKinds(service: string): BlockKind[] {
  if (service === 'nginx') return ['server', 'upstream']
  if (service === 'haproxy') return ['frontend', 'backend', 'listen']
  if (service === 'docker') return ['service', 'network', 'volume', 'secret', 'config']
  if (service === 'caddy') return ['site']
  if (service === 'libvirt') return ['disk', 'interface', 'graphics']
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
  focusName,
  autoCreate,
  onSaved,
}: {
  path: string
  service: string
  sha256: string
  me: Me
  /** Select this block by name as soon as the tree loads — set by a deep
   * link from "Сервисы и контейнеры", e.g. "редактировать конфиг". */
  focusName?: string | null
  /** Open the create form for this file's primary block kind as soon as the
   * tree loads — set by a deep link like "+ новый контейнер". */
  autoCreate?: boolean
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const blocks = useApi<{ blocks: ConfigBlock[] }>(`/configs/blocks${qs({ path })}`)
  const [selected, setSelected] = useState<string | null>(null)
  const [modal, setModal] = useState<ModalState | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [apply, setApply] = useState(false)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string; snippet?: string } | null>(null)
  const appliedDeepLink = useRef(false)

  const canControl = me.is_admin && me.allow_mutations
  const list = blocks.data?.blocks ?? []
  const selectedBlock = findBlock(list, selected)

  useEffect(() => {
    if (appliedDeepLink.current || !blocks.data) return
    appliedDeepLink.current = true
    if (focusName) {
      const match = findBlockByName(list, focusName)
      if (match) setSelected(match.id)
      return
    }
    if (autoCreate && canControl) {
      const kinds = creatableKinds(service)
      if (kinds.length > 0) openCreate(kinds[0])
    }
    // Deliberately keyed only on blocks.data: this must run exactly once, the
    // first time the tree loads — not every time it reloads after a save,
    // which is why the ref guard above (not a dependency list) does the
    // actual once-only gating.
  }, [blocks.data])

  function openCreate(kind: BlockKind, parentEndLine?: number) {
    setDraft(LIBVIRT_TEMPLATE[kind] ?? '')
    setNote('')
    setApply(false)
    // Устройство libvirt живёт внутри <devices>: родитель — его блок,
    // а не корень файла.
    if (parentEndLine === undefined && service === 'libvirt') {
      parentEndLine = (blocks.data?.blocks ?? []).find((b) => b.kind === 'devices')?.end_line
    }
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
      setNotice({ kind: res.rolled_back ? 'error' : 'info', text: res.message, snippet: res.rolled_back ? String(body.content ?? '') : undefined })
      blocks.reload()
      onSaved()
      return true
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err), snippet: String(body.content ?? '') })
      return false
    } finally {
      setBusy(false)
    }
  }

  // Дифф перед записью: тот же путь с dry_run возвращает «на диске →
  // после правки» без записи; «Записать» шлёт ту же правку по-настоящему.
  const [preview, setPreview] = useState<{ body: Record<string, unknown>; diff: string; after: () => void } | null>(null)

  async function previewThen(body: Record<string, unknown>, after: () => void) {
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<WriteResult>('/configs/blocks', { method: 'POST', body: { path, expected_sha256: sha256, ...body, dry_run: true } })
      setPreview({ body, diff: res.diff ?? '', after })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err), snippet: String(body.content ?? '') })
    } finally {
      setBusy(false)
    }
  }

  async function confirmPreview() {
    if (!preview) return
    const { body, after } = preview
    setPreview(null)
    if (await writeBlock(body)) after()
  }

  async function submitModal() {
    if (!modal) return
    await previewThen(
      {
        // Окно правки — mode «edit», а сервер понимает «update».
        op: modal.mode === 'edit' ? 'update' : 'create',
        kind: modal.kind,
        start_line: modal.block?.start_line,
        end_line: modal.block?.end_line,
        parent_end_line: modal.parentEndLine,
        content: draft,
        note,
        apply,
      },
      () => setModal(null),
    )
  }

  async function remove(block: ConfigBlock) {
    const label = `${KIND_LABEL[block.kind]}${block.name ? ' ' + block.name : ''}`
    if (!(await confirmAction(t('blocks.confirmDelete', { label })))) return
    await previewThen(
      { op: 'delete', kind: block.kind, start_line: block.start_line, end_line: block.end_line, apply: false },
      () => setSelected(null),
    )
  }

  if (blocks.loading && !blocks.data) return <div className="chart-empty">{t('blocks.loadingStructure')}</div>
  if (blocks.error) return <Banner kind="error">{blocks.error}</Banner>

  return (
    <div className="col">
      {preview && (
        <Modal title={t('blocks.previewTitle')} onClose={() => setPreview(null)} width={900} maskClosable={false}>
          {preview.diff === '' ? <p className="small muted">{t('configs.noChanges')}</p> : <DiffView text={preview.diff} />}
          <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
            <Button type="primary" disabled={preview.diff === ''} loading={busy} onClick={() => void confirmPreview()}>
              {t('virt.applyChanges')}
            </Button>
            <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
          </div>
        </Modal>
      )}
      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>
          {notice.text}
          {notice.kind === 'error' && <AIConfigError path={path} service={service} snippet={notice.snippet ?? ''} message={notice.text} />}
        </Banner>
      )}

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
        <div className="chart-empty">{t('blocks.noBlocksFound')}</div>
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
              <p>{t('blocks.lines', { start: selectedBlock.start_line, end: selectedBlock.end_line })}</p>
            </div>
            <div className="row">
              {canControl && (
                <button onClick={() => openEdit(selectedBlock)} disabled={busy}>
                  {t('blocks.edit')}
                </button>
              )}
              {canControl && selectedBlock.editable && (
                <button className="ghost" onClick={() => remove(selectedBlock)} disabled={busy}>
                  {busy && <Spinner />}
                  {t('blocks.delete')}
                </button>
              )}
              <button className="ghost" onClick={() => setSelected(null)}>
                {t('blocks.close')}
              </button>
            </div>
          </div>
          {!selectedBlock.editable && <p className="small muted">{t('blocks.notEditableNote')}</p>}
          <pre className="diff">{selectedBlock.raw}</pre>
        </div>
      )}

      {modal && (
        <Modal
          title={t(modal.mode === 'create' ? 'blocks.newBlock' : 'blocks.editBlock', { kind: KIND_LABEL[modal.kind] })}
          onClose={() => setModal(null)}
        >
          <div className="col">
            <CodeEditor value={draft} onChange={(e) => setDraft(e.target.value)} rows={12} />
            <div className="filters" style={{ marginTop: '0.5rem' }}>
              <label style={{ flex: 1, minWidth: '14rem' }}>
                {t('blocks.editComment')}
                <input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('blocks.editCommentPlaceholder')} />
              </label>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                <input
                  type="checkbox"
                  checked={apply}
                  onChange={(e) => setApply(e.target.checked)}
                  style={{ width: 'auto' }}
                />
                {t('blocks.reloadServiceAfterSave')}
              </label>
            </div>
            <div className="row" style={{ marginTop: '0.5rem' }}>
              <button className="primary" onClick={submitModal} disabled={busy || !draft.trim()}>
                {busy && <Spinner />}
                {busy ? t('blocks.saving') : t('blocks.save')}
              </button>
              <button className="ghost" onClick={() => setModal(null)} disabled={busy}>
                {t('blocks.cancel')}
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

function findBlockByName(list: ConfigBlock[], name: string): ConfigBlock | null {
  for (const b of list) {
    if (b.name === name) return b
    const child = findBlockByName(b.children ?? [], name)
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
  const { t } = useTranslation()
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
        <span className="small muted">{t('blocks.lines', { start: block.start_line, end: block.end_line })}</span>
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
