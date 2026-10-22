import { useState } from 'react'
import { Button, Dropdown, Input } from 'antd'
import { BulbOutlined, DownOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, DiffView, Modal } from './ui'
import { AIBody, Thinking, useElapsed } from './AIExplain'
import { unifiedDiff } from './textDiff'

/**
 * «ИИ ▾» в окне редактора сценария: объяснить выделенное (или весь текст),
 * написать команды по строкам «# ИИ: …», написать сценарий по описанию
 * задачи. Модель только предлагает: новый текст попадает в редактор через
 * окно с диффом и результатом «Проверить», а запускает сценарий человек.
 */

interface Reply {
  mode: string
  text?: string
  script?: string
  issues: { line: number; error: string }[]
  model: string
  attempts: number
}

type Mode = 'explain' | 'comment' | 'task'

/** Выделение в поле редактора открытого окна (у кнопки фокуса уже нет, но
 * поле помнит выделение). */
function editorSelection(): string {
  const ta = document.querySelector<HTMLTextAreaElement>('.ant-modal textarea')
  if (!ta || ta.selectionStart === ta.selectionEnd) return ''
  return ta.value.slice(ta.selectionStart, ta.selectionEnd)
}

export function AIScriptMenu({ draft, onApply }: { draft: string; onApply: (text: string) => void }) {
  const { t } = useTranslation()
  const status = useApi<{ enabled: boolean }>('/hub/ai')
  const [busy, setBusy] = useState<Mode | null>(null)
  const [reply, setReply] = useState<Reply | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [taskOpen, setTaskOpen] = useState(false)
  const [task, setTask] = useState('')
  const elapsed = useElapsed(busy !== null)
  if (!status.data?.enabled) return null

  async function ask(mode: Mode, extra: { selection?: string; task?: string } = {}) {
    setBusy(mode)
    setError(null)
    setReply(null)
    try {
      setReply(await api<Reply>('/hub/ai/script', { method: 'POST', timeoutMs: 600_000, body: { mode, content: draft, ...extra } }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(null)
    }
  }

  const close = () => {
    setReply(null)
    setError(null)
  }
  return (
    <>
      <Dropdown
        trigger={['click']}
        menu={{
          items: [
            { key: 'explain', label: t('scripts.ai.explain') },
            { key: 'comment', label: t('scripts.ai.comment') },
            { key: 'task', label: t('scripts.ai.task') },
          ],
          onClick: ({ key }) => {
            if (key === 'explain') void ask('explain', { selection: editorSelection() })
            else if (key === 'comment') void ask('comment')
            else setTaskOpen(true)
          },
        }}
      >
        <Button icon={<BulbOutlined />} loading={busy !== null}>
          {t('scripts.ai.button')} <DownOutlined />
        </Button>
      </Dropdown>

      {taskOpen && (
        <Modal title={t('scripts.ai.task')} onClose={() => setTaskOpen(false)} width={640}>
          <div className="col" style={{ gap: '0.5rem' }}>
            <span className="small muted">{t('scripts.ai.taskHint')}</span>
            <Input.TextArea rows={5} value={task} onChange={(e) => setTask(e.target.value)} placeholder={t('scripts.ai.taskPlaceholder')} autoFocus />
            <div className="row" style={{ gap: '0.4rem' }}>
              <Button
                type="primary"
                disabled={!task.trim()}
                onClick={() => {
                  setTaskOpen(false)
                  void ask('task', { task })
                }}
              >
                {t('scripts.ai.write')}
              </Button>
              <Button onClick={() => setTaskOpen(false)}>{t('common.cancel')}</Button>
            </div>
          </div>
        </Modal>
      )}

      {(busy !== null || reply || error) && (
        <Modal title={t(busy === 'explain' || reply?.mode === 'explain' ? 'scripts.ai.explainTitle' : 'scripts.ai.resultTitle')} onClose={close} width={860}>
          <div className="col" style={{ gap: '0.6rem' }}>
            {busy !== null && <Thinking seconds={elapsed} />}
            {error && <Banner kind="error">{error}</Banner>}
            {reply?.text && <AIBody text={reply.text} />}
            {reply?.script !== undefined && reply.script !== '' && (
              <>
                {reply.issues.length > 0 ? (
                  <Banner kind="warn">
                    {t('scripts.ai.issues', { count: reply.issues.length })}
                    <ul className="small" style={{ margin: '0.3rem 0 0', paddingLeft: '1.2rem' }}>
                      {reply.issues.map((i, n) => (
                        <li key={n}>
                          {t('scripts.line', { line: i.line })} {i.error}
                        </li>
                      ))}
                    </ul>
                  </Banner>
                ) : (
                  <Banner kind="info">{t('scripts.ai.checked')}</Banner>
                )}
                <DiffView text={unifiedDiff(draft, reply.script, 'editor', 'ai') || t('configs.noDiff')} />
                <div className="row" style={{ gap: '0.4rem' }}>
                  <Button
                    type="primary"
                    onClick={() => {
                      onApply(reply.script!)
                      close()
                    }}
                  >
                    {t('scripts.ai.apply')}
                  </Button>
                  <Button onClick={close}>{t('common.cancel')}</Button>
                </div>
              </>
            )}
            {reply && <span className="small muted">{t('scripts.ai.footer', { model: reply.model, attempts: reply.attempts })}</span>}
          </div>
        </Modal>
      )}
    </>
  )
}
