import { useState } from 'react'
import { Button, Popconfirm, Table, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, Loading } from './ui'

interface SandboxPackage {
  kind: 'snap' | 'flatpak'
  name: string
  version?: string
  channel?: string
  origin?: string
  id?: string
}

interface SandboxPackages {
  snap_available: boolean
  flatpak_available: boolean
  packages: SandboxPackage[]
  notes?: string[]
}

/**
 * Пакеты snap и flatpak рядом с apt. На сервере их обычно нет вовсе, и
 * тогда карточка честно об этом говорит; на рабочей машине оттуда
 * приходит половина софта, и без них список установленного просто врёт.
 */
export default function SandboxPackagesCard({ me }: { me: Me }) {
  const { t } = useTranslation()
  const data = useApi<SandboxPackages>('/system/sandbox-packages', 120_000)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [output, setOutput] = useState<string | null>(null)

  const canUse = me.is_admin && me.allow_mutations

  async function run(path: string, body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    setOutput(null)
    try {
      const res = await api<{ output?: string }>(path, { method: 'POST', body })
      if (res.output) setOutput(res.output)
      await data.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const columns: TableColumnsType<SandboxPackage> = [
    {
      title: t('sandboxPkg.colName'),
      key: 'name',
      render: (_, p) => (
        <div className="col">
          <code className="mono">{p.name}</code>
          {p.id && p.id !== p.name && <span className="small muted mono">{p.id}</span>}
        </div>
      ),
    },
    { title: t('sandboxPkg.colKind'), key: 'kind', width: '7rem', render: (_, p) => <Tag>{p.kind}</Tag> },
    {
      title: t('sandboxPkg.colVersion'),
      key: 'version',
      render: (_, p) => <span className="small mono">{p.version ?? '—'}</span>,
    },
    {
      title: t('sandboxPkg.colChannel'),
      key: 'channel',
      render: (_, p) => (
        <span className="small muted">
          {p.channel ?? '—'}
          {p.origin && ` · ${p.origin}`}
        </span>
      ),
    },
    {
      title: '',
      key: 'actions',
      width: '8rem',
      render: (_, p) =>
        canUse ? (
          <Popconfirm
            title={t('sandboxPkg.removeConfirm', { name: p.name })}
            onConfirm={() => run('/system/sandbox-packages/remove', { kind: p.kind, name: p.id || p.name })}
          >
            <Button size="small" danger disabled={busy}>
              {t('sandboxPkg.remove')}
            </Button>
          </Popconfirm>
        ) : null,
    },
  ]

  if (!data.data) return null
  const { snap_available, flatpak_available, packages } = data.data

  return (
    <Card
      title={t('sandboxPkg.title')}
      subtitle={t('sandboxPkg.count', { count: packages.length })}
      actions={
        canUse && (
          <div className="row" style={{ gap: '0.5rem' }}>
            {snap_available && (
              <Button size="small" loading={busy} onClick={() => run('/system/sandbox-packages/update', { kind: 'snap' })}>
                {t('sandboxPkg.updateSnap')}
              </Button>
            )}
            {flatpak_available && (
              <Button
                size="small"
                loading={busy}
                onClick={() => run('/system/sandbox-packages/update', { kind: 'flatpak' })}
              >
                {t('sandboxPkg.updateFlatpak')}
              </Button>
            )}
          </div>
        )
      }
    >
      {error && <Banner kind="error">{error}</Banner>}
      {output && <pre className="small mono" style={{ whiteSpace: 'pre-wrap' }}>{output}</pre>}
      {data.loading && !data.data && <Loading what={t('sandboxPkg.title')} />}
      {data.data.notes?.map((n) => (
        <p key={n} className="small muted">
          {n}
        </p>
      ))}
      {packages.length > 0 && (
        <div className="table-wrap">
          <Table<SandboxPackage>
            dataSource={packages}
            rowKey={(p) => `${p.kind}/${p.id || p.name}`}
            size="small"
            pagination={false}
            columns={columns}
          />
        </div>
      )}
    </Card>
  )
}
