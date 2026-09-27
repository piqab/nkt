import { useEffect, useState } from 'react'
import { Button, Dropdown } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import { Banner, CodeEditor, Loading, Modal } from './ui'
import { confirmAction } from './confirm'
import { K8S_ITEM_SNIPPETS, K8S_TEMPLATES } from './k8sTemplates'

/** Блок манифеста (см. internal/k8s/blocks.go). */
interface YBlock {
  id: string
  kind: string
  name: string
  start_line: number
  end_line: number
  raw: string
  children?: YBlock[]
  adds?: string
  indent?: number
}

type Edit = { mode: 'edit'; block: YBlock } | { mode: 'add'; list: YBlock } | { mode: 'object' }

/** Строки [start..end] (с 1) заменяются на replacement ('' — удалить). */
export function spliceLines(text: string, start: number, end: number, replacement: string): string {
  const lines = text.split('\n')
  const repl = replacement === '' ? [] : replacement.replace(/\n$/, '').split('\n')
  lines.splice(start - 1, end - start + 1, ...repl)
  return lines.join('\n')
}

/** Сдвигает текст на n пробелов (пустые строки не трогает). */
export function indentText(text: string, n: number): string {
  const pad = ' '.repeat(n)
  return text
    .replace(/\n$/, '')
    .split('\n')
    .map((l) => (l.trim() === '' ? l : pad + l))
    .join('\n')
}

/** Убирает общий отступ — блок в окне правится «от края». */
export function dedent(text: string): { body: string; indent: number } {
  const lines = text.replace(/\n$/, '').split('\n')
  const indent = Math.min(...lines.filter((l) => l.trim() !== '').map((l) => l.length - l.trimStart().length))
  const n = Number.isFinite(indent) ? indent : 0
  return { body: lines.map((l) => l.slice(Math.min(n, l.length - l.trimStart().length))).join('\n') + '\n', indent: n }
}

/**
 * Блочный режим редактора YAML Kubernetes — как у конфигов: дерево
 * объектов манифеста и их списков (контейнеры, порты, правила, ключи),
 * правка блока в окне, удаление, «+ элемент» в список и «+ объект».
 * Меняет только черновик (onChange); запись — обычным «Сохранить» окна,
 * через дифф и kubectl diff.
 */
export function YamlBlocks({ text, onChange, endpoint, readOnly }: { text: string; onChange: (v: string) => void; endpoint: string; readOnly?: boolean }) {
  const { t } = useTranslation()
  const [blocks, setBlocks] = useState<YBlock[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [edit, setEdit] = useState<Edit | null>(null)
  const [draft, setDraft] = useState('')

  useEffect(() => {
    let cancelled = false
    const timer = window.setTimeout(() => {
      api<{ blocks: YBlock[] }>(endpoint, { method: 'POST', body: { content: text } })
        .then((r) => {
          if (cancelled) return
          setBlocks(r.blocks)
          setError(null)
        })
        .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)))
    }, 250)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [text, endpoint])

  function open(e: Edit) {
    if (e.mode === 'edit') setDraft(dedent(e.block.raw).body)
    else if (e.mode === 'add') setDraft(K8S_ITEM_SNIPPETS[e.list.adds ?? ''] ?? '')
    else setDraft(K8S_TEMPLATES.deployment('app', 'default'))
    setEdit(e)
  }

  function save() {
    if (!edit) return
    if (edit.mode === 'edit') {
      const { indent } = dedent(edit.block.raw)
      onChange(spliceLines(text, edit.block.start_line, edit.block.end_line, indentText(draft, indent)))
    } else if (edit.mode === 'add') {
      const list = edit.list
      // Вставка после последней строки списка, с отступом его элементов.
      const lines = text.split('\n')
      lines.splice(list.end_line, 0, ...indentText(draft, list.indent ?? 0).split('\n'))
      onChange(lines.join('\n'))
    } else {
      const base = text.replace(/\s+$/, '')
      onChange((base ? base + '\n---\n' : '') + draft.replace(/\n?$/, '\n'))
    }
    setEdit(null)
  }

  async function remove(b: YBlock) {
    if (!(await confirmAction(t('yamlBlocks.deleteConfirm', { name: `${b.kind} ${b.name}` })))) return
    let next = spliceLines(text, b.start_line, b.end_line, '')
    // У объекта убирается и разделитель «---» перед ним (или после, если
    // объект первый).
    if (b.kind === 'object') {
      const lines = next.split('\n')
      const i = b.start_line - 2
      if (i >= 0 && lines[i]?.trim() === '---') lines.splice(i, 1)
      else if (lines[b.start_line - 1]?.trim() === '---') lines.splice(b.start_line - 1, 1)
      next = lines.join('\n')
    }
    onChange(next)
    setEdit(null)
  }

  const row = (b: YBlock, depth: number) => (
    <div key={b.id}>
      <div className="block-row" style={{ paddingLeft: `${depth * 1.25}rem` }} onClick={() => b.kind !== 'list' && open({ mode: 'edit', block: b })}>
        <span className="badge sev-info">
          <span className="badge-dot" />
          {b.kind === 'list' ? b.name : b.kind}
        </span>
        <span className="mono small">{b.kind === 'list' ? '' : b.name || b.raw.split('\n')[0].trim()}</span>
        <span className="small muted">{t('blocks.lines', { start: b.start_line, end: b.end_line })}</span>
        {!readOnly && b.kind === 'list' && b.adds && (
          <button
            className="ghost small block-row-action"
            onClick={(e) => {
              e.stopPropagation()
              open({ mode: 'add', list: b })
            }}
          >
            + {t(`yamlBlocks.item.${b.adds}`)}
          </button>
        )}
      </div>
      {b.children?.map((c) => row(c, depth + 1))}
    </div>
  )

  return (
    <div className="col modal-fill" style={{ gap: '0.25rem', minHeight: '20rem', overflow: 'auto' }}>
      {!readOnly && (
        <div className="row" style={{ gap: '0.4rem', marginBottom: '0.3rem' }}>
          <Dropdown
            trigger={['click']}
            menu={{
              items: Object.keys(K8S_TEMPLATES)
                .filter((k) => k !== 'empty')
                .map((k) => ({ key: k, label: t(`k8s.yaml.tpl.${k}`) })),
              onClick: ({ key }) => {
                setDraft(K8S_TEMPLATES[key]('app', 'default'))
                setEdit({ mode: 'object' })
              },
            }}
          >
            <Button size="small" icon={<PlusOutlined />}>
              {t('yamlBlocks.addObject')}
            </Button>
          </Dropdown>
        </div>
      )}
      {error && <Banner kind="error">{t('yamlBlocks.parseError', { error })}</Banner>}
      {!blocks && !error ? (
        <Loading what={t('yamlBlocks.title')} />
      ) : blocks && blocks.length === 0 ? (
        <div className="chart-empty">{t('blocks.noBlocksFound')}</div>
      ) : (
        (blocks ?? []).map((b) => row(b, 0))
      )}
      {edit && (
        <Modal
          title={edit.mode === 'edit' ? t('yamlBlocks.editTitle', { name: `${edit.block.kind} ${edit.block.name}` }) : edit.mode === 'add' ? t('yamlBlocks.addTitle', { kind: t(`yamlBlocks.item.${edit.list.adds}`) }) : t('yamlBlocks.addObject')}
          onClose={() => setEdit(null)}
          width={820}
        >
          <p className="small muted">{t('yamlBlocks.editHint')}</p>
          <CodeEditor value={draft} onChange={(e) => setDraft(e.target.value)} rows={16} readOnly={readOnly} />
          {!readOnly && (
            <div className="row" style={{ gap: '0.5rem', marginTop: '0.6rem' }}>
              <Button type="primary" disabled={!draft.trim()} onClick={save}>
                {t('yamlBlocks.apply')}
              </Button>
              {edit.mode === 'edit' && (
                <Button danger onClick={() => void remove(edit.block)}>
                  {t('yamlBlocks.delete')}
                </Button>
              )}
              <Button onClick={() => setEdit(null)}>{t('common.cancel')}</Button>
            </div>
          )}
        </Modal>
      )}
    </div>
  )
}
