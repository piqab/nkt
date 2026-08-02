import { useEffect, useState } from 'react'
import { api, qs, useApi } from '../api'
import type { ConfigVersion, FileContent, ManagedFile, Me, WriteResult } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, Loading, Modal, Spinner, formatDateTime } from '../components/ui'
import { formatBytes } from '../components/charts'
import BlockTree from '../components/BlockTree'

const BLOCK_SERVICES = new Set(['nginx', 'haproxy', 'docker'])

export default function Configs({ me }: { me: Me }) {
  const [view, setView] = useState<'text' | 'blocks'>('text')
  const files = useApi<{ files: ManagedFile[] }>('/configs')
  const [path, setPath] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [apply, setApply] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<WriteResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [diff, setDiff] = useState<{ id: number; text: string } | null>(null)
  const [creatingPath, setCreatingPath] = useState<string | null>(null)
  const [newFileModal, setNewFileModal] = useState(false)
  const [newFilePathInput, setNewFilePathInput] = useState('')

  const file = useApi<FileContent>(path ? `/configs/file${qs({ path })}` : null)
  const versions = useApi<{ versions: ConfigVersion[] }>(path ? `/configs/versions${qs({ path })}` : null)

  useEffect(() => {
    if (file.data) {
      setDraft(file.data.content)
      setResult(null)
      setError(null)
      setDiff(null)
    }
  }, [file.data])

  const dirty = file.data !== null && draft !== file.data.content

  async function save() {
    if (!file.data) return
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await api<WriteResult>('/configs/file', {
        method: 'PUT',
        body: {
          path: file.data.path,
          content: draft,
          note,
          apply,
          expected_sha256: file.data.sha256,
        },
      })
      setResult(res)
      setNote('')
      file.reload()
      versions.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function rollback(id: number) {
    if (!window.confirm(`Восстановить версию #${id}? Текущее содержимое файла будет заменено.`)) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<WriteResult>(`/configs/versions/${id}/rollback`, {
        method: 'POST',
        body: { apply },
      })
      setResult(res)
      file.reload()
      versions.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function showDiff(id: number) {
    if (diff?.id === id) {
      setDiff(null)
      return
    }
    const res = await api<{ diff: string }>(`/configs/versions/${id}/diff`)
    setDiff({ id, text: res.diff || '(различий нет)' })
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Конфигурации</h1>
          <p>
            Файлы, найденные при разборе конфигурации. Перед записью содержимое проверяется самим
            сервисом (<code className="mono">nginx -t</code>, <code className="mono">haproxy -c</code>,{' '}
            <code className="mono">docker compose config</code>); если проверка не прошла, файл
            автоматически возвращается в прежнее состояние. Каждая правка сохраняется в истории.
          </p>
        </div>
      </div>

      <ErrorNote error={files.error} />

      <div className="grid" style={{ gridTemplateColumns: 'minmax(240px, 320px) 1fr' }}>
        <Card
          title="Файлы"
          subtitle={`${files.data?.files.length ?? 0} шт.`}
          actions={
            me.is_admin &&
            me.allow_mutations && (
              <button className="ghost" onClick={() => setNewFileModal(true)}>
                + новый файл
              </button>
            )
          }
        >
          {files.loading && !files.data ? (
            <Loading what="список файлов" />
          ) : (
            <div className="col" style={{ gap: '0.15rem' }}>
              {files.data?.files.map((f) => (
                <button
                  key={f.path}
                  className="ghost"
                  onClick={() => {
                    setCreatingPath(null)
                    setPath(f.path)
                    setView('text')
                  }}
                  style={{
                    textAlign: 'left',
                    background: f.path === path ? 'var(--wash)' : undefined,
                    fontWeight: f.path === path ? 600 : 400,
                    padding: '0.3rem 0.4rem',
                  }}
                >
                  <div className="mono small" style={{ wordBreak: 'break-all' }}>
                    {f.path}
                  </div>
                  <div className="small muted">
                    {f.service} · {formatBytes(f.size)}
                    {!f.editable && ' · только чтение'}
                  </div>
                </button>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          {creatingPath ? (
            <NewFileForm
              path={creatingPath}
              onCreated={async () => {
                setCreatingPath(null)
                setPath(creatingPath)
                // The file list comes from the last scan, not a live directory
                // read — Write()'s own rescan runs in the background and isn't
                // done yet by the time this fires, so the new file would be
                // missing until whatever triggers the next scan. Wait for a
                // real one here instead of just reloading against stale data.
                try {
                  await api('/inventory/refresh', { method: 'POST' })
                } catch {
                  // A slow/failed rescan shouldn't block getting into the editor —
                  // files.reload() below just shows whatever list is current.
                }
                files.reload()
              }}
              onCancel={() => setCreatingPath(null)}
            />
          ) : !path ? (
            <Card>
              <div className="chart-empty">Выберите файл слева.</div>
            </Card>
          ) : file.loading && !file.data ? (
            <Loading what="файл" />
          ) : file.error ? (
            <ErrorNote error={file.error} />
          ) : file.data ? (
            <>
              <Card
                title={file.data.path}
                subtitle={`${file.data.service} · ${formatBytes(file.data.size)} · изменён ${formatDateTime(file.data.mod_time)}`}
                actions={
                  <>
                    {BLOCK_SERVICES.has(file.data.service) && (
                      <div className="row" style={{ gap: '0.15rem' }}>
                        <button className={view === 'text' ? 'primary' : 'ghost'} onClick={() => setView('text')}>
                          текст
                        </button>
                        <button className={view === 'blocks' ? 'primary' : 'ghost'} onClick={() => setView('blocks')}>
                          блоки
                        </button>
                      </div>
                    )}
                    {view === 'text' && (
                      <>
                        {dirty && <span className="small" style={{ color: 'var(--status-warning)' }}>есть несохранённые правки</span>}
                        <button onClick={() => setDraft(file.data!.content)} disabled={!dirty}>
                          Сбросить
                        </button>
                        {me.is_admin && me.allow_mutations && (
                          <button className="primary" onClick={save} disabled={busy || !dirty}>
                            {busy && <Spinner />}
                            {busy ? 'Сохраняю…' : 'Проверить и сохранить'}
                          </button>
                        )}
                      </>
                    )}
                  </>
                }
              >
                {view === 'blocks' ? (
                  <BlockTree
                    path={file.data.path}
                    service={file.data.service}
                    sha256={file.data.sha256}
                    me={me}
                    onSaved={() => {
                      file.reload()
                      versions.reload()
                    }}
                  />
                ) : (
                  <>
                    {error && <Banner kind="error">{error}</Banner>}
                    {result && (
                      <Banner kind={result.rolled_back ? 'error' : 'info'}>
                        <div>{result.message}</div>
                        {result.validation && (
                          <div className="small mono" style={{ marginTop: '0.25rem' }}>
                            проверка: {result.validation.stdout || result.validation.stderr || 'без вывода'}
                          </div>
                        )}
                        {!result.validated && (
                          <div className="small muted">
                            Проверка конфигурации недоступна для этого сервиса — файл записан без валидации.
                          </div>
                        )}
                      </Banner>
                    )}

                    {me.is_admin && me.allow_mutations && (
                      <div className="filters" style={{ marginBottom: '0.6rem' }}>
                        <label style={{ flex: 1, minWidth: '14rem' }}>
                          Комментарий к правке
                          <input
                            value={note}
                            onChange={(e) => setNote(e.target.value)}
                            placeholder="зачем меняем"
                          />
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
                    )}

                    <CodeEditor
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      rows={22}
                      readOnly={!me.is_admin || !me.allow_mutations}
                    />
                  </>
                )}
              </Card>

              <Card title="История версий" subtitle="Первая запись создаётся автоматически перед первой правкой">
                {versions.data?.versions.length ? (
                  <div className="table-wrap">
                    <table>
                      <thead>
                        <tr>
                          <th className="num">#</th>
                          <th>Когда</th>
                          <th>Кто</th>
                          <th>Событие</th>
                          <th>Комментарий</th>
                          <th className="num">Размер</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {versions.data.versions.map((v) => (
                          <tr key={v.id}>
                            <td className="num">{v.id}</td>
                            <td className="small nowrap">{formatDateTime(v.ts)}</td>
                            <td className="small">{v.author}</td>
                            <td className="small">{v.action}</td>
                            <td className="small secondary">{v.note}</td>
                            <td className="num small">{formatBytes(v.size)}</td>
                            <td className="nowrap">
                              <button className="ghost" onClick={() => showDiff(v.id)}>
                                {diff?.id === v.id ? 'скрыть' : 'diff'}
                              </button>
                              {me.is_admin && me.allow_mutations && (
                                <button className="ghost" onClick={() => rollback(v.id)} disabled={busy}>
                                  {busy && <Spinner />}
                                  откатить
                                </button>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="chart-empty">
                    История пуста — файл ещё не редактировался через этот интерфейс.
                  </div>
                )}

                {diff && <DiffView text={diff.text} />}
              </Card>
            </>
          ) : null}
        </div>
      </div>

      {newFileModal && (
        <Modal title="Новый файл" onClose={() => setNewFileModal(false)}>
          <div className="col">
            <label>
              Путь
              <input
                value={newFilePathInput}
                onChange={(e) => setNewFilePathInput(e.target.value)}
                placeholder="/etc/nginx/sites-enabled/newsite.conf"
                autoFocus
              />
            </label>
            <p className="small muted">
              Каталог должен относиться к nginx, haproxy или быть путём из <code className="mono">NKT_COMPOSE_FILES</code>{' '}
              — иначе запись отклонит сервер.
            </p>
            <div className="row" style={{ marginTop: '0.4rem' }}>
              <button
                className="primary"
                disabled={!newFilePathInput.trim().startsWith('/')}
                onClick={() => {
                  const p = newFilePathInput.trim()
                  setCreatingPath(p)
                  setPath(null)
                  setNewFileModal(false)
                  setNewFilePathInput('')
                }}
              >
                Создать
              </button>
              <button className="ghost" onClick={() => setNewFileModal(false)}>
                Отмена
              </button>
            </div>
          </div>
        </Modal>
      )}
    </>
  )
}

function NewFileForm({ path, onCreated, onCancel }: { path: string; onCreated: () => void; onCancel: () => void }) {
  const [content, setContent] = useState('')
  const [note, setNote] = useState('')
  const [apply, setApply] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<WriteResult | null>(null)

  async function create() {
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await api<WriteResult>('/configs/file', {
        method: 'PUT',
        body: { path, content, note, apply, expected_sha256: '' },
      })
      setResult(res)
      if (!res.rolled_back) onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card
      title={path}
      subtitle="новый файл — пока не существует на диске"
      actions={
        <>
          <button className="ghost" onClick={onCancel} disabled={busy}>
            Отмена
          </button>
          <button className="primary" onClick={create} disabled={busy || !content.trim()}>
            {busy && <Spinner />}
            {busy ? 'Создаю…' : 'Проверить и создать'}
          </button>
        </>
      }
    >
      {error && <Banner kind="error">{error}</Banner>}
      {result && (
        <Banner kind={result.rolled_back ? 'error' : 'info'}>
          <div>{result.message}</div>
          {result.validation && (
            <div className="small mono" style={{ marginTop: '0.25rem' }}>
              проверка: {result.validation.stdout || result.validation.stderr || 'без вывода'}
            </div>
          )}
          {result.rolled_back && (
            <div className="small muted">Файл не был создан — исправьте содержимое и попробуйте снова.</div>
          )}
        </Banner>
      )}
      <div className="filters" style={{ marginBottom: '0.6rem' }}>
        <label style={{ flex: 1, minWidth: '14rem' }}>
          Комментарий
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="зачем создаём" />
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
      <CodeEditor value={content} onChange={(e) => setContent(e.target.value)} rows={22} autoFocus />
    </Card>
  )
}

function DiffView({ text }: { text: string }) {
  return (
    <pre className="diff" style={{ marginTop: '0.75rem' }}>
      {text.split('\n').map((line, i) => {
        const cls = line.startsWith('+')
          ? 'add'
          : line.startsWith('-')
            ? 'del'
            : line.startsWith('@@')
              ? 'hunk'
              : undefined
        return (
          <div key={i} className={cls}>
            {line || ' '}
          </div>
        )
      })}
    </pre>
  )
}
