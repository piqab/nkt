import { useState } from 'react'
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

/**
 * Lets the operator navigate from /home to find or create the
 * docker-compose file a new container's service block will go into,
 * instead of silently reusing whatever the first existing container
 * happened to use.
 */
export default function PathPicker({
  onPick,
  onCancel,
}: {
  onPick: (path: string) => void
  onCancel: () => void
}) {
  const [dir, setDir] = useState('/home')
  const [newName, setNewName] = useState('docker-compose.yml')
  const [newFolderName, setNewFolderName] = useState('')
  const [busy, setBusy] = useState(false)
  const [folderBusy, setFolderBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const listing = useApi<{ entries: DirEntry[] }>(`/configs/browse${qs({ path: dir })}`)

  const entries = listing.data?.entries ?? []
  const dirs = entries.filter((e) => e.is_dir)
  const composeFiles = entries.filter((e) => !e.is_dir && COMPOSE_NAMES.has(baseName(e.path)))

  const segments = dir.split('/').filter(Boolean) // ["home", "alice", ...]
  const owner = ownerFromPath(dir)

  async function createFolder() {
    const name = newFolderName.trim()
    if (!name) return
    const path = `${dir}/${name}`.replace(/\/+/g, '/')
    setFolderBusy(true)
    setError(null)
    try {
      await api('/configs/mkdir', { method: 'POST', body: { path } })
      setNewFolderName('')
      setDir(path) // navigate straight into it, ready for a compose file
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setFolderBusy(false)
    }
  }

  async function createHere() {
    const path = `${dir}/${newName.trim()}`.replace(/\/+/g, '/')
    setBusy(true)
    setError(null)
    try {
      await api('/configs/file', {
        method: 'PUT',
        // "services:" alone parses to null in YAML — docker compose rejects
        // that ("services must be a mapping"). An explicit empty mapping
        // validates, and internal/parse/blocks.go knows to rewrite it back
        // to block style when the first service gets added.
        body: { path, content: 'services: {}\n', note: 'создан новый compose-стек', apply: false, expected_sha256: '' },
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
          return (
            <span key={path}>
              {i > 0 && <span className="muted"> / </span>}
              <button className="ghost" style={{ padding: '0.15rem 0.3rem' }} onClick={() => setDir(path)}>
                {seg}
              </button>
            </span>
          )
        })}
      </div>
      {owner && <div className="small muted">пользователь: {owner}</div>}

      {listing.loading && !listing.data ? (
        <Loading what="каталог" />
      ) : listing.error ? (
        <Banner kind="error">{listing.error}</Banner>
      ) : entries.length === 0 ? (
        <div className="chart-empty">Пусто.</div>
      ) : (
        <div className="col" style={{ gap: '0.15rem', maxHeight: '14rem', overflowY: 'auto' }}>
          {dirs.map((d) => (
            <button
              key={d.path}
              className="ghost"
              style={{ textAlign: 'left' }}
              onClick={() => setDir(d.path)}
            >
              {baseName(d.path)}/
            </button>
          ))}
          {composeFiles.map((f) => (
            <button
              key={f.path}
              className="ghost"
              style={{ textAlign: 'left', fontWeight: 600 }}
              onClick={() => onPick(f.path)}
            >
              {baseName(f.path)} — использовать этот файл
            </button>
          ))}
        </div>
      )}

      <div className="filters">
        <label style={{ flex: 1, minWidth: '12rem' }}>
          Новая папка в этом каталоге
          <input
            value={newFolderName}
            onChange={(e) => setNewFolderName(e.target.value)}
            placeholder="myproject"
          />
        </label>
        <button onClick={createFolder} disabled={folderBusy || !newFolderName.trim()}>
          {folderBusy && <Spinner />}
          {folderBusy ? 'Создаю…' : '+ папка'}
        </button>
      </div>

      <div className="filters" style={{ marginTop: '0.6rem' }}>
        <label style={{ flex: 1, minWidth: '12rem' }}>
          Или создать новый файл в этом каталоге
          <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="docker-compose.yml" />
        </label>
        <button className="primary" onClick={createHere} disabled={busy || !newName.trim()}>
          {busy && <Spinner />}
          {busy ? 'Создаю…' : 'Создать'}
        </button>
        <button className="ghost" onClick={onCancel} disabled={busy}>
          Отмена
        </button>
      </div>
    </div>
  )
}
