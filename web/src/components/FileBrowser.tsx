import { useCallback, useEffect, useRef, useState } from 'react'
import type React from 'react'
import { Button, Input, Progress, Select, Tag, Tooltip, type TableColumnsType } from 'antd'
import { FolderOutlined, FolderAddOutlined, FileOutlined, FileZipOutlined, BranchesOutlined, DownloadOutlined, UploadOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, apiURL, qs, useApi } from '../api'
import type { Job } from '../types'
import { Banner, Card, Modal, Spinner } from './ui'
import { formatBytes } from './charts'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { JobLogModal } from '../pages/Jobs'

/**
 * Проводник по каталогам хоста — раздел «Диски → Файлы».
 *
 * Не весь диск: сервер отдаёт список корней, ниже которых можно ходить,
 * а выше — нет; хлебные крошки начинаются с корня, а не с «/». Всё, что
 * меняет файлы, идёт через обычные команды на хосте (mkdir, mv, rm,
 * tar, git), — здесь только форма и таблица.
 *
 * Загрузка — не через api(): ему не показать прогресс, а файл в гигабайт
 * без полосы выглядит как зависание. XMLHttpRequest умеет upload.progress,
 * и тело уходит как есть — сервер кладёт его в файл целиком.
 */

interface Entry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mode: string
  mod_time: string
  archive?: boolean
}

interface RootsInfo {
  roots: string[]
  max_upload: number
}

function joinPath(dir: string, name: string): string {
  return `${dir}/${name}`.replace(/\/+/g, '/')
}

function parentOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

function underRoot(path: string, root: string): boolean {
  return path === root || path.startsWith(root + '/')
}

function badName(name: string): boolean {
  return !name || name === '.' || name === '..' || name.includes('/')
}

/** Один файл в очереди загрузки: имя с путём внутри папки, если грузится
 * папка (webkitRelativePath или обход перетащенного дерева). */
interface Pending {
  rel: string
  file: File
}

/** Сводка по всей очереди: один счётчик и одна полоса, а не строка на
 * каждый из пятисот файлов папки. Ошибки — отдельным списком, они редкие. */
interface UploadSummary {
  total: number
  done: number
  bytesTotal: number
  bytesDone: number
  current: string
  errors: { rel: string; error: string }[]
  finished: boolean
}

/** Сколько файлов грузить разом: через SSH-туннель хаба каждый запрос —
 * свой канал, и сотня параллельных только мешала бы друг другу. */
const UPLOAD_PARALLEL = 3

/** Обход перетащенной папки: FileSystemEntry — единственный способ
 * получить дерево из drop, обычный DataTransfer.files папок не отдаёт. */
async function collectEntries(items: DataTransferItemList): Promise<Pending[]> {
  const out: Pending[] = []
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject))
      out.push({ rel: prefix + entry.name, file })
      return
    }
    if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader()
      // readEntries отдаёт порциями и пустым массивом сигналит конец.
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject))
        if (batch.length === 0) break
        for (const e of batch) await walk(e, prefix + entry.name + '/')
      }
    }
  }
  const entries = Array.from(items)
    .map((it) => (typeof it.webkitGetAsEntry === 'function' ? it.webkitGetAsEntry() : null))
    .filter((e): e is FileSystemEntry => e !== null)
  for (const e of entries) await walk(e, '')
  return out
}

function fromFileList(files: FileList): Pending[] {
  return Array.from(files).map((file) => ({ rel: file.webkitRelativePath || file.name, file }))
}

export default function FileBrowser() {
  const { t } = useTranslation()
  const info = useApi<RootsInfo>('/files/roots')
  const roots = info.data?.roots ?? []
  const [dir, setDir] = useState<string | null>(null)
  // Первый корень становится текущим каталогом, как только список пришёл.
  useEffect(() => {
    if (dir === null && roots.length > 0) setDir(roots[0])
  }, [dir, roots])
  // Самый длинный подходящий корень: если один корень вложен в другой,
  // текущим считается ближайший.
  const root = roots.filter((r) => dir !== null && underRoot(dir, r)).sort((a, b) => b.length - a.length)[0] ?? roots[0] ?? ''
  const listing = useApi<{ entries: Entry[] }>(dir ? `/files/list${qs({ path: dir })}` : null)
  const entries = listing.data?.entries ?? []

  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [folderModal, setFolderModal] = useState(false)
  const [renameTarget, setRenameTarget] = useState<Entry | null>(null)
  const [cloneModal, setCloneModal] = useState(false)
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [upload, setUpload] = useState<UploadSummary | null>(null)
  const [dragging, setDragging] = useState(false)
  const fileInput = useRef<HTMLInputElement | null>(null)
  const dirInput = useRef<HTMLInputElement | null>(null)

  const reload = useCallback(() => listing.reload(), [listing])

  async function mutate(key: string, path: string, body: Record<string, unknown>) {
    setBusy(key)
    setError(null)
    try {
      await api(path, { method: 'POST', body })
      reload()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusy(null)
    }
  }

  async function remove(e: Entry) {
    const ok = await confirmAction(t(e.is_dir ? 'files.confirmDeleteDir' : 'files.confirmDeleteFile', { name: e.name }), {
      okText: t('files.delete'),
    })
    if (!ok) return
    await mutate(`rm:${e.path}`, '/files/delete', { path: e.path })
  }

  async function extract(e: Entry) {
    const ok = await confirmAction(t('files.confirmExtract', { name: e.name, dir }), { okText: t('files.extract'), danger: false })
    if (!ok) return
    await mutate(`x:${e.path}`, '/files/extract', { path: e.path, dest: dir })
  }

  // Загрузка идёт по одному файлу за запрос: серверу так проще — тело и
  // есть файл, без разбора multipart, — а папка отличается от файлов
  // только тем, что в имени есть путь: каталоги хост создаёт по дороге.
  function uploadPending(list: Pending[]) {
    if (!dir || list.length === 0) return
    const max = info.data?.max_upload ?? 0
    const summary: UploadSummary = {
      total: list.length, done: 0, bytesTotal: list.reduce((n, p) => n + p.file.size, 0), bytesDone: 0,
      current: '', errors: [], finished: false,
    }
    const publish = () => setUpload({ ...summary, errors: [...summary.errors] })
    publish()
    const queue = [...list]
    const one = (p: Pending) =>
      new Promise<void>((resolve) => {
        if (max > 0 && p.file.size > max) {
          summary.errors.push({ rel: p.rel, error: t('files.tooBig', { max: formatBytes(max) }) })
          resolve()
          return
        }
        summary.current = p.rel
        let sent = 0
        const xhr = new XMLHttpRequest()
        xhr.open('PUT', apiURL(`/files/upload${qs({ dir, name: p.rel })}`))
        xhr.upload.onprogress = (ev) => {
          if (!ev.lengthComputable) return
          summary.bytesDone += ev.loaded - sent
          sent = ev.loaded
          publish()
        }
        xhr.onload = () => {
          if (xhr.status >= 300) {
            let err: string
            try {
              err = (JSON.parse(xhr.responseText) as { error?: string }).error ?? `HTTP ${xhr.status}`
            } catch {
              err = `HTTP ${xhr.status}`
            }
            summary.errors.push({ rel: p.rel, error: err })
          }
          summary.bytesDone += p.file.size - sent
          resolve()
        }
        xhr.onerror = () => {
          summary.errors.push({ rel: p.rel, error: t('files.uploadFailed') })
          summary.bytesDone += p.file.size - sent
          resolve()
        }
        xhr.send(p.file)
      })
    const worker = async () => {
      for (let p = queue.shift(); p; p = queue.shift()) {
        await one(p)
        summary.done += 1
        publish()
      }
    }
    void Promise.all(Array.from({ length: Math.min(UPLOAD_PARALLEL, list.length) }, worker)).then(() => {
      summary.finished = true
      summary.current = ''
      publish()
      reload()
    })
  }

  async function onDrop(ev: React.DragEvent) {
    ev.preventDefault()
    setDragging(false)
    if (!dir) return
    // Дерево — через entries; если браузер их не дал (или это не файлы
    // из проводника ОС), остаётся плоский список.
    const items = ev.dataTransfer.items
    let list = items && items.length > 0 ? await collectEntries(items) : []
    if (list.length === 0) list = fromFileList(ev.dataTransfer.files)
    uploadPending(list)
  }

  const rootSegments = root.split('/').filter(Boolean)
  const segments = (dir ?? '').split('/').filter(Boolean)

  const columns: TableColumnsType<Entry> = [
    {
      title: t('files.colName'),
      key: 'name',
      render: (_, e) =>
        e.is_dir ? (
          <Button type="link" size="small" style={{ padding: 0 }} onClick={() => setDir(e.path)}>
            <FolderOutlined /> <span className="mono">{e.name}</span>
          </Button>
        ) : (
          <span className="mono">
            {e.archive ? <FileZipOutlined /> : <FileOutlined />} {e.name}
          </span>
        ),
    },
    {
      title: t('files.colSize'),
      key: 'size',
      align: 'right',
      width: 110,
      render: (_, e) => (e.is_dir ? <span className="muted">—</span> : <span className="num">{formatBytes(e.size)}</span>),
    },
    {
      title: t('files.colMode'),
      key: 'mode',
      width: 110,
      responsive: ['md'],
      render: (_, e) => <code className="mono small">{e.mode}</code>,
    },
    {
      title: t('files.colModified'),
      key: 'mod_time',
      width: 170,
      responsive: ['lg'],
      render: (_, e) => <span className="small">{new Date(e.mod_time).toLocaleString()}</span>,
    },
    {
      title: t('files.colActions'),
      key: 'actions',
      align: 'right',
      render: (_, e) => (
        <span className="row" style={{ gap: 0, justifyContent: 'flex-end' }}>
          {!e.is_dir && (
            <Tooltip title={t('files.download')}>
              <a
                href={apiURL(`/files/download${qs({ path: e.path })}`)}
                download={e.name}
                aria-label={t('files.download')}
                className="ant-btn ant-btn-text ant-btn-sm ant-btn-icon-only"
                style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}
              >
                {/* Ссылка, а не кнопка: скачивание — переход браузера, ему нужен href. */}
                <DownloadOutlined />
              </a>
            </Tooltip>
          )}
          {e.archive && (
            <RowAction icon={<FileZipOutlined />} label={t('files.extract')} loading={busy === `x:${e.path}`} onClick={() => extract(e)} />
          )}
          <RowAction action="edit" label={t('files.rename')} onClick={() => setRenameTarget(e)} />
          <RowAction action="delete" label={t('files.delete')} danger loading={busy === `rm:${e.path}`} onClick={() => remove(e)} />
        </span>
      ),
    },
  ]

  return (
    <Card title={t('files.title')} subtitle={t('files.hint')}>
      {info.error && <Banner kind="error">{info.error}</Banner>}
      {error && <Banner kind="error">{error}</Banner>}

      <div className="filters" style={{ alignItems: 'center' }}>
        <Select
          size="small"
          value={root || undefined}
          style={{ minWidth: '7rem', maxWidth: '16rem' }}
          options={roots.map((r) => ({ value: r, label: <span className="mono">{r}</span> }))}
          onChange={(v) => setDir(v)}
        />
        <div className="row small" style={{ flexWrap: 'wrap', gap: '0.15rem', alignItems: 'center', flex: 1 }}>
          {segments.map((seg, i) => {
            const path = '/' + segments.slice(0, i + 1).join('/')
            const insideRoot = i >= rootSegments.length - 1
            return (
              <span key={path}>
                {i > 0 && <span className="muted"> / </span>}
                {insideRoot ? (
                  <Button type="link" size="small" style={{ padding: '0 0.2rem' }} onClick={() => setDir(path)}>
                    <span className="mono">{seg}</span>
                  </Button>
                ) : (
                  <span className="muted mono" style={{ padding: '0 0.2rem' }}>
                    {seg}
                  </span>
                )}
              </span>
            )
          })}
        </div>
        <Button size="small" icon={<FolderAddOutlined />} disabled={!dir} onClick={() => setFolderModal(true)}>
          {t('files.newFolder')}
        </Button>
        <Button size="small" icon={<UploadOutlined />} disabled={!dir} onClick={() => fileInput.current?.click()}>
          {t('files.upload')}
        </Button>
        <Button size="small" icon={<FolderOutlined />} disabled={!dir} onClick={() => dirInput.current?.click()}>
          {t('files.uploadFolder')}
        </Button>
        <input
          ref={fileInput}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) uploadPending(fromFileList(e.target.files))
            e.target.value = ''
          }}
        />
        {/* webkitdirectory — единственный способ выбрать папку в диалоге
            браузера; пустые папки он не отдаёт, только файлы с путями. */}
        <input
          ref={dirInput}
          type="file"
          hidden
          // @ts-expect-error нестандартный, но поддержан всеми браузерами
          webkitdirectory=""
          onChange={(e) => {
            if (e.target.files) uploadPending(fromFileList(e.target.files))
            e.target.value = ''
          }}
        />
        <Button size="small" icon={<BranchesOutlined />} disabled={!dir} onClick={() => setCloneModal(true)}>
          {t('files.clone')}
        </Button>
        <RowAction action="restart" label={t('common.refresh')} loading={listing.loading && !!listing.data} onClick={reload} />
      </div>

      {upload && (
        <div className="col" style={{ gap: '0.2rem', margin: '0.4rem 0' }}>
          <div className="row small" style={{ gap: '0.5rem', alignItems: 'center' }}>
            <span style={{ minWidth: '14rem' }}>
              {t(upload.finished ? 'files.uploadDone' : 'files.uploadProgress', {
                done: upload.done, total: upload.total, size: formatBytes(upload.bytesTotal),
              })}
            </span>
            <Progress
              percent={upload.bytesTotal > 0 ? Math.round((upload.bytesDone / upload.bytesTotal) * 100) : 100}
              size="small"
              style={{ flex: 1, margin: 0 }}
              status={upload.finished ? (upload.errors.length > 0 ? 'exception' : 'success') : 'active'}
            />
          </div>
          {upload.current && <div className="small muted mono">{upload.current}</div>}
          {upload.errors.map((e) => (
            <div key={e.rel} className="row small" style={{ gap: '0.5rem', alignItems: 'center' }}>
              <span className="mono">{e.rel}</span>
              <Tag color="error">{e.error}</Tag>
            </div>
          ))}
          {upload.finished && (
            <Button type="link" size="small" style={{ alignSelf: 'flex-start', padding: 0 }} onClick={() => setUpload(null)}>
              {t('files.clearUploads')}
            </Button>
          )}
        </div>
      )}

      {listing.error ? (
        <Banner kind="error">{listing.error}</Banner>
      ) : !listing.data && dir ? (
        <div className="small muted"><Spinner /> {t('files.loading')}</div>
      ) : (
        <div
          className={`table-wrap file-drop${dragging ? ' file-drop-active' : ''}`}
          onDragOver={(ev) => {
            ev.preventDefault()
            if (!dragging) setDragging(true)
          }}
          onDragLeave={(ev) => {
            if (!ev.currentTarget.contains(ev.relatedTarget as Node | null)) setDragging(false)
          }}
          onDrop={onDrop}
        >
          {dragging && <div className="file-drop-hint">{t('files.dropHint')}</div>}
          <DataTable<Entry>
            dataSource={entries}
            rowKey="path"
            size="small"
            pagination={entries.length > 100 ? { pageSize: 100 } : false}
            columns={columns}
            locale={{ emptyText: t('files.empty') }}
          />
        </div>
      )}

      {folderModal && dir && (
        <NameModal
          title={t('files.newFolderTitle')}
          label={t('files.folderName')}
          okText={t('files.create')}
          onClose={() => setFolderModal(false)}
          onSubmit={async (name) => {
            const ok = await mutate('mkdir', '/files/mkdir', { path: joinPath(dir, name) })
            if (ok) setFolderModal(false)
            return ok
          }}
        />
      )}
      {renameTarget && (
        <NameModal
          title={t('files.renameTitle', { name: renameTarget.name })}
          label={t('files.newName')}
          okText={t('files.rename')}
          initial={renameTarget.name}
          onClose={() => setRenameTarget(null)}
          onSubmit={async (name) => {
            const ok = await mutate('rename', '/files/rename', {
              path: renameTarget.path,
              to: joinPath(parentOf(renameTarget.path), name),
            })
            if (ok) setRenameTarget(null)
            return ok
          }}
        />
      )}
      {cloneModal && dir && (
        <CloneModal
          dir={dir}
          onClose={() => setCloneModal(false)}
          onStarted={(job) => {
            setCloneModal(false)
            setOpenJob(job)
          }}
        />
      )}
      {openJob && (
        <JobLogModal
          job={openJob}
          onClose={() => {
            setOpenJob(null)
            reload()
          }}
        />
      )}
    </Card>
  )
}

function NameModal({
  title,
  label,
  okText,
  initial = '',
  onClose,
  onSubmit,
}: {
  title: string
  label: string
  okText: string
  initial?: string
  onClose: () => void
  onSubmit: (name: string) => Promise<boolean>
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(initial)
  const [busy, setBusy] = useState(false)
  const trimmed = name.trim()
  async function submit() {
    if (badName(trimmed) || trimmed === initial) return
    setBusy(true)
    try {
      await onSubmit(trimmed)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={title} onClose={onClose} closeLabel={t('common.cancel')}>
      <label className="col" style={{ gap: '0.2rem' }}>
        {label}
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} onPressEnter={submit} />
      </label>
      <div className="row" style={{ justifyContent: 'flex-end', marginTop: '0.8rem' }}>
        <Button type="primary" loading={busy} disabled={badName(trimmed) || trimmed === initial} onClick={submit}>
          {okText}
        </Button>
      </div>
    </Modal>
  )
}

type CloneAuth = 'none' | 'token' | 'deploy-key'

function repoDirName(url: string): string {
  const s = url.trim().replace(/\/+$/, '').replace(/\.git$/, '')
  const i = Math.max(s.lastIndexOf('/'), s.lastIndexOf(':'))
  return i >= 0 ? s.slice(i + 1) : s
}

/**
 * git clone заданием. Приватные репозитории — токеном (уходит на хост
 * один раз, в память задания, в базу не пишется) или deploy-ключом
 * хоста: публичную половину показываем здесь, чтобы добавить её в
 * настройки репозитория на GitHub/GitLab.
 */
function CloneModal({ dir, onClose, onStarted }: { dir: string; onClose: () => void; onStarted: (job: Job) => void }) {
  const { t } = useTranslation()
  const [url, setUrl] = useState('')
  const [branch, setBranch] = useState('')
  const [name, setName] = useState('')
  const [auth, setAuth] = useState<CloneAuth>('none')
  const [username, setUsername] = useState('')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const key = useApi<{ public_key: string }>(auth === 'deploy-key' ? '/files/deploy-key' : null)

  const isSSH = /^(ssh:\/\/|[\w.-]+@)/.test(url.trim())
  // SSH-адрес без ключа хоста не откроется — там нет ни пароля, ни
  // токена; https с deploy-ключом тоже не сочетается.
  useEffect(() => {
    if (isSSH && auth !== 'deploy-key') setAuth('deploy-key')
    if (!isSSH && auth === 'deploy-key' && url.trim() !== '') setAuth('none')
  }, [isSSH])

  const effectiveName = name.trim() || repoDirName(url)
  const valid = url.trim() !== '' && effectiveName !== '' && (auth !== 'token' || secret.trim() !== '')

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number; dest: string }>('/files/clone', {
        method: 'POST',
        body: { url: url.trim(), branch: branch.trim(), dir, name: name.trim(), auth, username: username.trim(), secret },
      })
      const job = await api<Job>(`/jobs/${res.job_id}`)
      onStarted(job)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setBusy(false)
    }
  }

  return (
    <Modal title={t('files.cloneTitle')} onClose={onClose} closeLabel={t('common.cancel')} width={620}>
      <div className="col" style={{ gap: '0.6rem' }}>
        {error && <Banner kind="error">{error}</Banner>}
        <label className="col" style={{ gap: '0.2rem' }}>
          {t('files.cloneUrl')}
          <Input autoFocus value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://github.com/org/repo.git" className="mono" />
        </label>
        <div className="filters">
          <label className="col" style={{ gap: '0.2rem', flex: 1 }}>
            {t('files.cloneBranch')}
            <Input value={branch} onChange={(e) => setBranch(e.target.value)} placeholder={t('files.cloneBranchDefault')} />
          </label>
          <label className="col" style={{ gap: '0.2rem', flex: 1 }}>
            {t('files.cloneDirName')}
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={repoDirName(url) || 'repo'} />
          </label>
        </div>
        <div className="small muted mono">{joinPath(dir, effectiveName || '…')}</div>

        <label className="col" style={{ gap: '0.2rem' }}>
          {t('files.cloneAuth')}
          <Select<CloneAuth>
            value={auth}
            onChange={setAuth}
            options={[
              { value: 'none', label: t('files.authNone'), disabled: isSSH },
              { value: 'token', label: t('files.authToken'), disabled: isSSH },
              { value: 'deploy-key', label: t('files.authKey') },
            ]}
          />
        </label>
        {auth === 'token' && (
          <div className="filters">
            <label className="col" style={{ gap: '0.2rem', flex: 1 }}>
              {t('files.cloneUsername')}
              <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder="x-access-token" autoComplete="off" />
            </label>
            <label className="col" style={{ gap: '0.2rem', flex: 2 }}>
              {t('files.cloneSecret')}
              <Input.Password value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="new-password" />
            </label>
            <div className="small muted" style={{ flexBasis: '100%' }}>{t('files.tokenHint')}</div>
          </div>
        )}
        {auth === 'deploy-key' && (
          <div className="col" style={{ gap: '0.3rem' }}>
            <div className="small muted">{t('files.keyHint')}</div>
            {key.error ? (
              <Banner kind="error">{key.error}</Banner>
            ) : key.data ? (
              <>
                <pre className="mono small" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0 }}>{key.data.public_key}</pre>
                <Button
                  size="small"
                  style={{ alignSelf: 'flex-start' }}
                  onClick={() => {
                    void navigator.clipboard?.writeText(key.data!.public_key)
                    setCopied(true)
                  }}
                >
                  {copied ? t('common.copied') : t('common.copy')}
                </Button>
              </>
            ) : (
              <div className="small muted"><Spinner /> {t('files.keyLoading')}</div>
            )}
          </div>
        )}
        <div className="row" style={{ justifyContent: 'flex-end' }}>
          <Button type="primary" loading={busy} disabled={!valid} onClick={start}>
            {t('files.cloneStart')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
