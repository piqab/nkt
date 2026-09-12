import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { DirEntry } from '../types'
import { Banner, Loading, Spinner } from './ui'

const COMPOSE_NAMES = new Set(['docker-compose.yml', 'docker-compose.yaml', 'compose.yml', 'compose.yaml'])

function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

/** The host convention this whole picker leans on: each operator's compose
 * stacks live under their own /home/<user>/... — so the user a container
 * belongs to is just the first path segment, not a separate thing to ask
 * for. Returns null for anything not under /home. */
export function ownerFromPath(path: string): string | null {
  const m = /^\/home\/([^/]+)/.exec(path)
  return m ? m[1] : null
}

function underRoot(path: string, root: string): boolean {
  return path === root || path.startsWith(root.replace(/\/+$/, '') + '/')
}

/**
 * Обход каталога в пределах корня — вглубь можно, наружу нет.
 *
 * Один компонент на «новый контейнер» (корень /home, показываются только
 * compose-файлы, создаётся заготовка стека) и на «новый файл» в
 * конфигурациях (корень категории, показываются все файлы, создание —
 * это открыть редактор с новым путём). Корень задаёт вызывающий: он и
 * есть граница, за которой сервер всё равно откажет, — только здесь
 * отказ виден до нажатия.
 */
export default function PathPicker({
  root,
  initialDir,
  onPick,
  onCancel,
  mode = 'compose',
  defaultName,
}: {
  /** Корень, выше которого не подняться. */
  root: string
  /** Откуда начать; по умолчанию — с корня. */
  initialDir?: string
  onPick: (path: string) => void
  onCancel: () => void
  /** compose — только compose-файлы и заготовка стека при создании;
   * file — любые файлы, создание отдаёт путь без записи. */
  mode?: 'compose' | 'file'
  defaultName?: string
}) {
  const { t } = useTranslation()
  const start = initialDir && underRoot(initialDir, root) ? initialDir : root
  const [dir, setDir] = useState(start)
  useEffect(() => setDir(start), [start])
  const [newName, setNewName] = useState(defaultName ?? (mode === 'compose' ? 'docker-compose.yml' : ''))
  const [newFolderName, setNewFolderName] = useState('')
  const [busy, setBusy] = useState(false)
  const [folderBusy, setFolderBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const listing = useApi<{ entries: DirEntry[] }>(`/configs/browse${qs({ path: dir })}`)

  const entries = listing.data?.entries ?? []
  const dirs = entries.filter((e) => e.is_dir)
  const files = entries.filter((e) => !e.is_dir && (mode === 'file' || COMPOSE_NAMES.has(baseName(e.path))))

  // Хлебные крошки — от корня, не от «/»: выше корня хода нет, и
  // показывать туда ссылки значило бы обещать то, что не сработает.
  const rootSegments = root.split('/').filter(Boolean)
  const segments = dir.split('/').filter(Boolean)
  const owner = mode === 'compose' ? ownerFromPath(dir) : null

  async function createFolder() {
    const name = newFolderName.trim()
    if (!name || name.includes('/') || name === '..') return
    const path = `${dir}/${name}`.replace(/\/+/g, '/')
    setFolderBusy(true)
    setError(null)
    try {
      await api('/configs/mkdir', { method: 'POST', body: { path } })
      setNewFolderName('')
      setDir(path) // navigate straight into it, ready for a file
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setFolderBusy(false)
    }
  }

  async function createHere() {
    const name = newName.trim()
    if (!name || name.includes('/') || name === '..') return
    const path = `${dir}/${name}`.replace(/\/+/g, '/')
    if (mode === 'file') {
      onPick(path)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api('/configs/file', {
        method: 'PUT',
        // "services:" alone parses to null in YAML — docker compose rejects
        // that ("services must be a mapping"). An explicit empty mapping
        // validates, and internal/parse/blocks.go knows to rewrite it back
        // to block style when the first service gets added.
        body: { path, content: 'services: {}\n', note: t('pathPicker.newComposeStackNote'), apply: false, expected_sha256: '' },
      })
      onPick(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="col">
      {error && <Banner kind="error">{error}</Banner>}

      <div className="row small" style={{ flexWrap: 'wrap', gap: '0.15rem', alignItems: 'center' }}>
        {segments.map((seg, i) => {
          const path = '/' + segments.slice(0, i + 1).join('/')
          const insideRoot = i >= rootSegments.length - 1
          return (
            <span key={path}>
              {i > 0 && <span className="muted"> / </span>}
              {insideRoot ? (
                <button className="ghost" style={{ padding: '0.15rem 0.3rem' }} onClick={() => setDir(path)}>
                  {seg}
                </button>
              ) : (
                <span className="muted" style={{ padding: '0.15rem 0.3rem' }}>
                  {seg}
                </span>
              )}
            </span>
          )
        })}
      </div>
      {owner && <div className="small muted">{t('pathPicker.owner', { owner })}</div>}

      {listing.loading && !listing.data ? (
        <Loading what={t('pathPicker.directory')} />
      ) : listing.error ? (
        // Корня категории может ещё не быть (caddy не ставили) — это не
        // ошибка: каталог появится вместе с файлом при записи.
        /no such file|не существует|not exist/i.test(listing.error) ? (
          <div className="small muted">{t('pathPicker.missingDir')}</div>
        ) : (
          <Banner kind="error">{listing.error}</Banner>
        )
      ) : entries.length === 0 ? (
        <div className="chart-empty">{t('pathPicker.empty')}</div>
      ) : (
        <div className="col" style={{ gap: '0.15rem', maxHeight: '14rem', overflowY: 'auto' }}>
          {dirs.map((d) => (
            <button key={d.path} className="ghost" style={{ textAlign: 'left' }} onClick={() => setDir(d.path)}>
              {baseName(d.path)}/
            </button>
          ))}
          {files.map((f) => (
            <button
              key={f.path}
              className="ghost"
              style={{ textAlign: 'left', fontWeight: mode === 'compose' ? 600 : 400 }}
              onClick={() => (mode === 'compose' ? onPick(f.path) : setNewName(baseName(f.path)))}
            >
              {mode === 'compose' ? t('pathPicker.useThisFile', { name: baseName(f.path) }) : baseName(f.path)}
            </button>
          ))}
        </div>
      )}

      <div className="filters">
        <label style={{ flex: 1, minWidth: '12rem' }}>
          {t('pathPicker.newFolderLabel')}
          <input value={newFolderName} onChange={(e) => setNewFolderName(e.target.value)} placeholder="myproject" />
        </label>
        <button onClick={createFolder} disabled={folderBusy || !newFolderName.trim()}>
          {folderBusy && <Spinner />}
          {folderBusy ? t('pathPicker.creating') : t('pathPicker.addFolder')}
        </button>
      </div>

      <div className="filters" style={{ marginTop: '0.6rem' }}>
        <label style={{ flex: 1, minWidth: '12rem' }}>
          {t(mode === 'compose' ? 'pathPicker.newFileLabel' : 'pathPicker.fileNameLabel')}
          <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder={mode === 'compose' ? 'docker-compose.yml' : 'site.conf'} />
        </label>
        <button className="primary" onClick={createHere} disabled={busy || !newName.trim()}>
          {busy && <Spinner />}
          {busy ? t('pathPicker.creating') : t('pathPicker.create')}
        </button>
        <button className="ghost" onClick={onCancel} disabled={busy}>
          {t('pathPicker.cancel')}
        </button>
      </div>
      <div className="small muted mono">{`${dir}/${newName.trim() || '…'}`.replace(/\/+/g, '/')}</div>
    </div>
  )
}
