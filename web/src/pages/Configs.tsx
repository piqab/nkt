import { useEffect, useState } from 'react'
import { Button, Checkbox, Input, Segmented, Table, type TableColumnsType } from 'antd'
import { api, qs, useApi } from '../api'
import type { ConfigVersion, FileContent, ManagedFile, Me, WriteResult } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, Loading, Modal, formatDateTime } from '../components/ui'
import { formatBytes } from '../components/charts'
import BlockTree from '../components/BlockTree'

const BLOCK_SERVICES = new Set(['nginx', 'haproxy', 'docker', 'caddy'])

function versionColumns(
  diff: { id: number; text: string } | null,
  showDiff: (id: number) => void,
  rollback: (id: number) => void,
  busy: boolean,
  me: Me,
): TableColumnsType<ConfigVersion> {
  return [
    { title: '#', dataIndex: 'id', key: 'id', align: 'right' },
    { title: 'Когда', key: 'ts', render: (_, v) => <span className="small nowrap">{formatDateTime(v.ts)}</span> },
    { title: 'Кто', dataIndex: 'author', key: 'author', className: 'small' },
    { title: 'Событие', dataIndex: 'action', key: 'action', className: 'small' },
    { title: 'Комментарий', dataIndex: 'note', key: 'note', className: 'small secondary' },
    { title: 'Размер', key: 'size', align: 'right', render: (_, v) => <span className="num small">{formatBytes(v.size)}</span> },
    {
      title: '',
      key: 'actions',
      render: (_, v) => (
        <div className="row">
          <Button type="link" size="small" onClick={() => showDiff(v.id)}>
            {diff?.id === v.id ? 'скрыть' : 'diff'}
          </Button>
          {me.is_admin && me.allow_mutations && (
            <Button type="link" size="small" loading={busy} onClick={() => rollback(v.id)}>
              откатить
            </Button>
          )}
        </div>
      ),
    },
  ]
}

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
            <code className="mono">caddy validate</code>, <code className="mono">docker compose config</code>);
            если проверка не прошла, файл
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
              <Button type="link" onClick={() => setNewFileModal(true)}>
                + новый файл
              </Button>
            )
          }
        >
          {files.loading && !files.data ? (
            <Loading what="список файлов" />
          ) : (
            <div className="col" style={{ gap: '0.15rem' }}>
              {files.data?.files.map((f) => (
                <Button
                  key={f.path}
                  type="text"
                  onClick={() => {
                    setCreatingPath(null)
                    setPath(f.path)
                    setView('text')
                  }}
                  style={{
                    display: 'block',
                    width: '100%',
                    textAlign: 'left',
                    height: 'auto',
                    whiteSpace: 'normal',
                    background: f.path === path ? 'var(--wash)' : undefined,
                    fontWeight: f.path === path ? 600 : 400,
                    padding: '0.3rem 0.4rem',
                  }}
                >
                  <div className="mono small" style={{ wordBreak: 'break-all' }}>
                    {f.path}
                  </div>
                  {f.sites && f.sites.length > 0 && (
                    <div className="small muted" style={{ wordBreak: 'break-all' }}>
                      {f.sites
                        .map((site) => (site.name_unicode ? `${site.name} (${site.name_unicode})` : site.name))
                        .join(', ')}
                    </div>
                  )}
                  <div className="small muted">
                    {f.service} · {formatBytes(f.size)}
                    {!f.editable && ' · только чтение'}
                  </div>
                </Button>
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
                      <Segmented
                        value={view}
                        onChange={(v) => setView(v as 'text' | 'blocks')}
                        options={[
                          { value: 'text', label: 'текст' },
                          { value: 'blocks', label: 'блоки' },
                        ]}
                      />
                    )}
                    {view === 'text' && (
                      <>
                        {dirty && <span className="small" style={{ color: 'var(--status-warning)' }}>есть несохранённые правки</span>}
                        <Button onClick={() => setDraft(file.data!.content)} disabled={!dirty}>
                          Сбросить
                        </Button>
                        {me.is_admin && me.allow_mutations && (
                          <Button type="primary" onClick={save} loading={busy} disabled={!dirty}>
                            {busy ? 'Сохраняю…' : 'Проверить и сохранить'}
                          </Button>
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
                          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="зачем меняем" />
                        </label>
                        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                          <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
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
                    <Table<ConfigVersion>
                      dataSource={versions.data.versions}
                      rowKey="id"
                      pagination={false}
                      size="small"
                      columns={versionColumns(diff, showDiff, rollback, busy, me)}
                    />
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
              <Input
                value={newFilePathInput}
                onChange={(e) => setNewFilePathInput(e.target.value)}
                placeholder="/etc/nginx/sites-enabled/newsite.conf"
                autoFocus
              />
            </label>
            <p className="small muted">
              Каталог должен относиться к nginx, haproxy, caddy или быть путём из{' '}
              <code className="mono">NKT_COMPOSE_FILES</code> — иначе запись отклонит сервер.
            </p>
            <div className="row" style={{ marginTop: '0.4rem' }}>
              <Button
                type="primary"
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
              </Button>
              <Button type="link" onClick={() => setNewFileModal(false)}>
                Отмена
              </Button>
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
          <Button type="link" onClick={onCancel} disabled={busy}>
            Отмена
          </Button>
          <Button type="primary" onClick={create} loading={busy} disabled={!content.trim()}>
            {busy ? 'Создаю…' : 'Проверить и создать'}
          </Button>
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
          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="зачем создаём" />
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
          <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
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
