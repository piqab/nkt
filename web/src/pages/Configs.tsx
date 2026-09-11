import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Form, Input, Segmented, Select, type InputRef, type TableColumnsType } from 'antd'
import { Trans, useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { ConfigVersion, FileContent, ManagedFile, Me, WriteResult } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, InfoHint, Loading, Modal, formatDateTime } from '../components/ui'
import { formatBytes } from '../components/charts'
import BlockTree from '../components/BlockTree'
import i18n from '../i18n'
import { confirmAction } from '../components/confirm'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'

const BLOCK_SERVICES = new Set(['nginx', 'haproxy', 'docker', 'caddy'])

function versionColumns(
  diff: { id: number; text: string } | null,
  showDiff: (id: number) => void,
  rollback: (id: number) => void,
  busy: boolean,
  me: Me,
  // The newest version is the file as it is on disk right now, so diffing it
  // against the current file is always empty — offering the button there
  // reads as "the diff is broken" rather than "there is nothing to compare".
  currentVersionId: number | undefined,
): TableColumnsType<ConfigVersion> {
  const t = i18n.t.bind(i18n)
  return [
    { title: '#', dataIndex: 'id', key: 'id', align: 'right' },
    { title: t('configs.colTs'), key: 'ts', render: (_, v) => <span className="small nowrap">{formatDateTime(v.ts)}</span> },
    { title: t('configs.colAuthor'), dataIndex: 'author', key: 'author', className: 'small' },
    { title: t('configs.colAction'), dataIndex: 'action', key: 'action', className: 'small' },
    { title: t('configs.colNote'), dataIndex: 'note', key: 'note', className: 'small secondary' },
    { title: t('configs.colSize'), key: 'size', align: 'right', render: (_, v) => <span className="num small">{formatBytes(v.size)}</span> },
    {
      title: '',
      key: 'actions',
      render: (_, v) => (
        <div className="row row-nowrap">
          {v.id === currentVersionId ? (
            <span className="small secondary nowrap">{t('configs.isCurrent')}</span>
          ) : (
            <RowAction
              action="details"
              label={diff?.id === v.id ? t('configs.hide') : t('configs.diff')}
              onClick={() => showDiff(v.id)}
            />
          )}
          {me.is_admin && me.allow_mutations && (
            <RowAction action="history" label={t('configs.rollback')} loading={busy} onClick={() => rollback(v.id)} />
          )}
        </div>
      ),
    },
  ]
}

/** GET /configs/ssh/preflight — internal/api/handlers_configs.go. */
interface SSHPreflight {
  probe: { ok: boolean; port: number; banner?: string; error?: string; simulated?: boolean }
  reserve: { ok: boolean; via_hub_tunnel: boolean; nkt_addr: string; detail: string }
}

export default function Configs({ me }: { me: Me }) {
  const { t } = useTranslation()
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
  // Set alongside creatingPath when the new file is a clone of an existing
  // one — NewFileForm starts pre-filled with this instead of blank, and
  // cloneFrom (the source path) just labels the form so it's clear where
  // the content came from.
  const [creatingInitialContent, setCreatingInitialContent] = useState('')
  const [newFileModal, setNewFileModal] = useState<{ cloneFrom?: string; initialContent?: string } | null>(null)
  const [newFilePathInput, setNewFilePathInput] = useState('')
  // Категория списка файлов. 'all' и 'unused' — псевдокатегории: первая
  // ничего не фильтрует, вторая собирает файлы, до которых конфигурация
  // службы не дотягивается (in_use=false) независимо от их службы.
  const [category, setCategory] = useState<string>('all')
  // Резервный канал для правки sshd_config: путь к хосту, не зависящий от
  // самого sshd. Запрашивается только когда открыт файл этой категории —
  // для остальных проверять нечего.
  const [sshForce, setSSHForce] = useState(false)
  const newFilePathInputRef = useRef<InputRef>(null)

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

  // Cloning starts the path field pre-filled with the source's full path
  // (see the "Клонировать" button below) — select just the filename
  // portion so typing a new name is a plain overwrite, the directory
  // stays untouched unless the operator deliberately edits that part too.
  // Keyed on newFileModal itself (a fresh object each time it opens), not
  // on newFilePathInput, so this doesn't re-select on every keystroke.
  useEffect(() => {
    if (!newFileModal) return
    const input = newFilePathInputRef.current
    if (!input) return
    input.focus()
    if (newFileModal.cloneFrom) {
      const slash = newFileModal.cloneFrom.lastIndexOf('/')
      if (slash >= 0) input.setSelectionRange(slash + 1, newFileModal.cloneFrom.length)
    }
  }, [newFileModal])

  // Открытие каталога юниту — запасной путь: обычно запись, которой
  // помешала песочница, повторяется мимо неё, и до этой кнопки дело не
  // доходит.
  const [allowingWrite, setAllowingWrite] = useState(false)
  const [allowNotice, setAllowNotice] = useState<string | null>(null)

  async function allowWrite() {
    if (!path) return
    if (!(await confirmAction(t('configs.allowWriteConfirm')))) return
    setAllowingWrite(true)
    setAllowNotice(null)
    try {
      const res = await api<{ status: string; restarting: boolean; message?: string; commands?: string[] }>(
        '/configs/allow-write',
        { method: 'POST', body: { path } },
      )
      if (res.restarting) {
        setAllowNotice(t('configs.allowWriteRestarting'))
        // Служба перезапускается — файл станет доступен на запись только
        // после этого, поэтому список и сам файл перечитываются с
        // задержкой, а не сразу.
        setTimeout(() => {
          file.reload()
          files.reload()
        }, 6000)
      } else {
        setAllowNotice([res.message, ...(res.commands ?? [])].filter(Boolean).join('\n'))
      }
    } catch (err) {
      setAllowNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setAllowingWrite(false)
    }
  }

  const allFiles = files.data?.files ?? []
  const isSSHPath = file.data?.service === 'ssh'
  const sshPreflight = useApi<SSHPreflight>(isSSHPath ? '/configs/ssh/preflight' : null)

  // Категории строятся из самих данных, а не из фиксированного списка:
  // какие службы окажутся на хосте, заранее неизвестно, и пустая категория
  // в переключателе — это пункт, ведущий в пустой список.
  const categories = useMemo(() => {
    const counts = new Map<string, number>()
    for (const f of allFiles) counts.set(f.service, (counts.get(f.service) ?? 0) + 1)
    const unused = allFiles.filter((f) => !f.in_use).length
    const out = [{ value: 'all', label: `${t('configs.categoryAll')} (${allFiles.length})` }]
    for (const [service, count] of [...counts].sort((a, b) => a[0].localeCompare(b[0]))) {
      out.push({ value: service, label: `${service} (${count})` })
    }
    if (unused > 0) out.push({ value: 'unused', label: `${t('configs.categoryUnused')} (${unused})` })
    return out
  }, [allFiles, t])

  const visibleFiles = useMemo(() => {
    if (category === 'all') return allFiles
    if (category === 'unused') return allFiles.filter((f) => !f.in_use)
    return allFiles.filter((f) => f.service === category)
  }, [allFiles, category])

  // Выбранная категория могла исчезнуть после перезагрузки списка — тогда
  // фильтр показывал бы пустоту без всякого объяснения.
  useEffect(() => {
    if (category !== 'all' && !categories.some((c) => c.value === category)) setCategory('all')
  }, [categories, category])

  // Разрешение «у меня есть консоль» относится к одной конкретной правке,
  // а не к сеансу: при переходе к другому файлу оно сбрасывается.
  useEffect(() => setSSHForce(false), [path])

  // Что не так с введённым путём — одна строка на все места, где это
  // нужно: подпись под полем и состояние кнопки.
  const trimmedNewPath = newFilePathInput.trim()
  const pathProblem =
    trimmedNewPath === ''
      ? null
      : !trimmedNewPath.startsWith('/')
        ? t('configs.pathMustBeAbsolute')
        : trimmedNewPath.includes('..')
          ? t('configs.pathNoDotDot')
          : newFileModal?.cloneFrom && trimmedNewPath === newFileModal.cloneFrom
            ? t('configs.pathSameAsSource')
            : null

  const sshBlocked = isSSHPath && sshPreflight.data?.reserve.ok === false && !sshForce

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
          // Явное «у меня есть консоль» — единственный способ править
          // sshd_config, когда обратного пути в обход sshd не видно.
          force: sshForce,
        },
      })
      setResult(res)
      setNote('')
      file.reload()
      versions.reload()
      // Show what was just written. The endpoint compares a version against
      // the *current* file, so the newest version — which now equals the file
      // on disk — always diffs to nothing; the one before it is the pre-write
      // state, and its diff is exactly this edit.
      //
      // Read back explicitly rather than reusing the list from before the
      // write: on a file edited for the first time there was no history at
      // all, and the pre-write state only comes into existence as part of
      // this very save.
      const fresh = await api<{ versions: ConfigVersion[] }>(`/configs/versions${qs({ path: file.data.path })}`)
      const preWrite = fresh.versions[1]?.id
      if (preWrite !== undefined) await showDiff(preWrite, { force: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function rollback(id: number) {
    if (!(await confirmAction(t('configs.confirmRollback', { id })))) return
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

  async function showDiff(id: number, opts?: { force?: boolean }) {
    if (diff?.id === id && !opts?.force) {
      setDiff(null)
      return
    }
    const res = await api<{ diff: string }>(`/configs/versions/${id}/diff`)
    setDiff({ id, text: res.diff || t('configs.noDiff') })
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('configs.title')}
            <InfoHint>{t('configs.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={files.error} />

      <div className="grid" style={{ gridTemplateColumns: 'minmax(240px, 320px) 1fr' }}>
        <Card
          title={t('configs.filesTitle')}
          subtitle={t('configs.filesCount', { count: visibleFiles.length })}
          actions={
            me.is_admin &&
            me.allow_mutations && (
              <Button type="link" onClick={() => setNewFileModal({})}>
                {t('configs.newFile')}
              </Button>
            )
          }
        >
          {files.loading && !files.data ? (
            <Loading what={t('configs.loadingFileList')} />
          ) : (
            <div className="col" style={{ gap: '0.15rem' }}>
              <Select
                value={category}
                onChange={setCategory}
                options={categories}
                style={{ width: '100%', marginBottom: '0.4rem' }}
              />
              {visibleFiles.map((f) => (
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
                    {!f.editable && t('configs.readOnlySuffix')}
                    {!f.in_use && ` · ${t('configs.notInUse')}`}
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
              initialContent={creatingInitialContent}
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
              <div className="chart-empty">{t('configs.selectFileLeft')}</div>
            </Card>
          ) : file.loading && !file.data ? (
            <Loading what={t('configs.loadingFile')} />
          ) : file.error ? (
            <ErrorNote error={file.error} />
          ) : file.data ? (
            <>
              <Card
                title={file.data.path}
                subtitle={t('configs.modified', { service: file.data.service, size: formatBytes(file.data.size), time: formatDateTime(file.data.mod_time) })}
                actions={
                  <>
                    {BLOCK_SERVICES.has(file.data.service) && (
                      <Segmented
                        value={view}
                        onChange={(v) => setView(v as 'text' | 'blocks')}
                        options={[
                          { value: 'text', label: t('configs.text') },
                          { value: 'blocks', label: t('configs.blocks') },
                        ]}
                      />
                    )}
                    {view === 'text' && (
                      <>
                        {dirty && <span className="small" style={{ color: 'var(--status-warning)' }}>{t('configs.unsavedChanges')}</span>}
                        <Button onClick={() => setDraft(file.data!.content)} disabled={!dirty}>
                          {t('configs.reset')}
                        </Button>
                        {me.is_admin && me.allow_mutations && (
                          <>
                            <Button
                              onClick={() => {
                                setNewFileModal({ cloneFrom: file.data!.path, initialContent: draft })
                                setNewFilePathInput(file.data!.path)
                              }}
                            >
                              {t('configs.clone')}
                            </Button>
                            <Button
                              type="primary"
                              onClick={save}
                              loading={busy}
                              disabled={!dirty || sshBlocked || file.data?.editable === false}
                            >
                              {busy ? t('configs.saving') : t('configs.validateAndSave')}
                            </Button>
                          </>
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
                            {t('configs.validation', { output: result.validation.stdout || result.validation.stderr || t('configs.noOutput') })}
                          </div>
                        )}
                        {!result.validated && (
                          <div className="small muted">{t('configs.noValidation')}</div>
                        )}
                      </Banner>
                    )}

                    {/* Правка sshd_config — единственная, после которой хост
                        можно потерять целиком: если демон не поднимется,
                        чинить будет уже нечем. Поэтому здесь видно и то,
                        отвечает ли sshd прямо сейчас, и то, останется ли
                        путь к хосту в обход него. */}
                    {isSSHPath && sshPreflight.data && (
                      <Banner kind={sshPreflight.data.reserve.ok ? 'info' : 'warn'}>
                        <strong>{t('configs.sshGuardTitle')}</strong>
                        <div className="small" style={{ marginTop: '0.25rem' }}>
                          {sshPreflight.data.reserve.ok
                            ? t('configs.sshGuardOk', { detail: sshPreflight.data.reserve.detail })
                            : t('configs.sshGuardBlocked', { detail: sshPreflight.data.reserve.detail })}
                        </div>
                        <div className="small" style={{ marginTop: '0.25rem' }}>
                          {sshPreflight.data.probe.ok
                            ? t('configs.sshGuardProbeOk', {
                                port: sshPreflight.data.probe.port,
                                banner: sshPreflight.data.probe.banner ?? '',
                              })
                            : t('configs.sshGuardProbeFail', {
                                port: sshPreflight.data.probe.port,
                                error: sshPreflight.data.probe.error ?? '',
                              })}
                        </div>
                        {!sshPreflight.data.reserve.ok && me.is_admin && me.allow_mutations && (
                          <label
                            className="small"
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: '0.35rem',
                              marginTop: '0.4rem',
                            }}
                          >
                            <Checkbox checked={sshForce} onChange={(e) => setSSHForce(e.target.checked)} />
                            {t('configs.sshGuardForce')}
                          </label>
                        )}
                      </Banner>
                    )}

                    {me.is_admin && me.allow_mutations && (
                      <div className="filters" style={{ marginBottom: '0.6rem' }}>
                        <label style={{ flex: 1, minWidth: '14rem' }}>
                          {t('configs.editNote')}
                          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('configs.editNotePlaceholder')} />
                        </label>
                        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
                          <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
                          {t('configs.reloadAfterSave')}
                        </label>
                      </div>
                    )}

                    {file.data?.editable === false && (
                      <Banner kind="warn">
                        <div className="col" style={{ gap: '0.4rem' }}>
                          <span>{t('configs.notWritable')}</span>
                          {me.is_admin && me.allow_mutations && (
                            <span>
                              <Button size="small" loading={allowingWrite} onClick={() => void allowWrite()}>
                                {t('configs.allowWrite')}
                              </Button>
                            </span>
                          )}
                          {allowNotice && (
                            <span className="small mono" style={{ whiteSpace: 'pre-wrap' }}>{allowNotice}</span>
                          )}
                        </div>
                      </Banner>
                    )}

                    <CodeEditor
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      rows={22}
                      readOnly={!me.is_admin || !me.allow_mutations || file.data?.editable === false}
                    />
                  </>
                )}
              </Card>

              <Card
                title={
                  <>
                    {t('configs.versionHistoryTitle')}
                    <InfoHint>{t('configs.versionHistoryHint')}</InfoHint>
                  </>
                }
              >
                {versions.data?.versions.length ? (
                  <div className="table-wrap">
                    <DataTable<ConfigVersion>                       dataSource={versions.data.versions}
                      rowKey="id"
                      columns={versionColumns(diff, showDiff, rollback, busy, me, versions.data.versions[0]?.id)}
                    />
                  </div>
                ) : (
                  <div className="chart-empty">{t('configs.emptyHistory')}</div>
                )}

                {diff && (
                  <>
                    <div className="small secondary" style={{ marginTop: '0.75rem' }}>
                      {t('configs.diffCaption', { id: diff.id })}
                    </div>
                    <DiffView text={diff.text} />
                  </>
                )}
              </Card>
            </>
          ) : null}
        </div>
      </div>

      {newFileModal && (
        <Modal title={t(newFileModal.cloneFrom ? 'configs.cloneTitle' : 'configs.newFileTitle')} onClose={() => setNewFileModal(null)}>
          <div className="col">
            {newFileModal.cloneFrom && (
              <p className="small muted" style={{ marginTop: 0 }}>
                <Trans i18nKey="configs.cloneBody" values={{ path: newFileModal.cloneFrom }} components={{ code: <code className="mono" /> }} />
              </p>
            )}
            {/* Путь проверяется здесь же: абсолютный, без «..» и не тот
                самый файл, который клонируем. Сервер отвергнет то же самое,
                но узнавать об опечатке после запроса на хост незачем. */}
            <Form layout="vertical" requiredMark={false}>
              <Form.Item
                label={t('configs.pathLabel')}
                validateStatus={pathProblem ? 'error' : undefined}
                help={pathProblem ?? undefined}
                style={{ marginBottom: '0.4rem' }}
              >
                <Input
                  ref={newFilePathInputRef}
                  value={newFilePathInput}
                  onChange={(e) => setNewFilePathInput(e.target.value)}
                  placeholder="/etc/nginx/sites-enabled/newsite.conf"
                />
              </Form.Item>
            </Form>
            <p className="small muted">
              <Trans i18nKey="configs.pathHint" components={{ code: <code className="mono" /> }} />
            </p>
            <div className="row" style={{ marginTop: '0.4rem' }}>
              <Button
                type="primary"
                disabled={pathProblem !== null || newFilePathInput.trim() === ''}
                onClick={() => {
                  const p = newFilePathInput.trim()
                  setCreatingPath(p)
                  setCreatingInitialContent(newFileModal.initialContent ?? '')
                  setPath(null)
                  setNewFileModal(null)
                  setNewFilePathInput('')
                }}
              >
                {t(newFileModal.cloneFrom ? 'configs.clone' : 'configs.create')}
              </Button>
              <Button type="link" onClick={() => setNewFileModal(null)}>
                {t('configs.cancel')}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </>
  )
}

function NewFileForm({
  path,
  initialContent = '',
  onCreated,
  onCancel,
}: {
  path: string
  initialContent?: string
  onCreated: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const [content, setContent] = useState(initialContent)
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
      subtitle={t('configs.newFileSubtitle')}
      actions={
        <>
          <Button type="link" onClick={onCancel} disabled={busy}>
            {t('configs.cancel')}
          </Button>
          <Button type="primary" onClick={create} loading={busy} disabled={!content.trim()}>
            {busy ? t('configs.creating') : t('configs.validateAndCreate')}
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
              {t('configs.validation', { output: result.validation.stdout || result.validation.stderr || t('configs.noOutput') })}
            </div>
          )}
          {result.rolled_back && (
            <div className="small muted">{t('configs.notCreated')}</div>
          )}
        </Banner>
      )}
      <div className="filters" style={{ marginBottom: '0.6rem' }}>
        <label style={{ flex: 1, minWidth: '14rem' }}>
          {t('configs.commentLabel')}
          <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('configs.commentPlaceholder')} />
        </label>
        <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.35rem' }}>
          <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
          {t('configs.reloadAfterSave')}
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
