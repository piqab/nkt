import { useMemo, useState } from 'react'
import { Button, Input, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me } from '../types'
import { Card, Loading } from './ui'
import PackageInstallModal from './PackageInstallModal'
import CommandModal from './CommandModal'
import { PackageGrid, type PackageRow } from '../pages/Packages'

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
 * Пакеты snap и flatpak рядом с apt — так же, как «Установленные пакеты»:
 * фильтр, сетка с галочками, «удалить выбранные» и обновление — в окне
 * выполнения с живым выводом. Ключ строки — «вид/имя»: имя snap и
 * идентификатор flatpak (удаление flatpak идёт по нему).
 */
export default function SandboxPackagesCard({ me }: { me: Me }) {
  const { t } = useTranslation()
  const data = useApi<SandboxPackages>('/system/sandbox-packages', 120_000)
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string[]>([])
  const [running, setRunning] = useState<{
    title: string
    wsPath: string
    outcome?: { ok: boolean; exitCode?: number; okText?: string; failText?: string } | null
  } | null>(null)
  // Установка самой системы пакетов — apt в том же окне с живым логом.
  const [installing, setInstalling] = useState<string | null>(null)
  const canUse = me.is_admin && me.allow_mutations

  const rows: (PackageRow & { kind: SandboxPackage['kind']; ref: string })[] = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data.data?.packages ?? [])
      // Имя в сетке — как его понимает команда удаления: у snap имя, у
      // flatpak идентификатор (reverse-DNS, с именами snap не совпадает);
      // вид — меткой рядом, канал и источник — в подсказке.
      .map((p) => ({
        name: p.id || p.name,
        ref: p.id || p.name,
        kind: p.kind,
        version: p.version,
        description: [p.name !== (p.id || p.name) ? p.name : '', p.channel, p.origin].filter(Boolean).join(' · ') || undefined,
      }))
      .filter((p) => !q || p.name.toLowerCase().includes(q) || (p.description ?? '').toLowerCase().includes(q))
  }, [data.data, query])

  function removeSelected() {
    const kindOf = new Map((data.data?.packages ?? []).map((p) => [p.id || p.name, p.kind]))
    const snaps = picked.filter((n) => kindOf.get(n) === 'snap')
    const flatpaks = picked.filter((n) => kindOf.get(n) === 'flatpak')
    setRunning({
      title: t('sandboxPkg.removeTitle', { names: [...snaps, ...flatpaks].join(', ') }),
      wsPath: `/system/sandbox-packages/ws${qs({ op: 'remove', snap: snaps.join(','), flatpak: flatpaks.join(',') })}`,
    })
  }

  function update(kind: 'snap' | 'flatpak') {
    setRunning({
      title: t(kind === 'snap' ? 'sandboxPkg.updateSnap' : 'sandboxPkg.updateFlatpak'),
      wsPath: `/system/sandbox-packages/ws${qs({ op: 'update', [kind]: 1 })}`,
    })
  }

  async function finished() {
    const st = await api<{ succeeded?: boolean; exit_code?: number }>('/system/sandbox-packages/status').catch(() => null)
    setRunning((r) =>
      r
        ? {
            ...r,
            outcome: {
              ok: !!st?.succeeded,
              exitCode: st?.exit_code,
              okText: t('sandboxPkg.doneOk'),
              failText: t('sandboxPkg.doneFailed', { code: st?.exit_code ?? '?' }),
            },
          }
        : r,
    )
    setPicked([])
    await data.reload()
  }

  if (!data.data) return data.loading ? <Loading what={t('sandboxPkg.title')} /> : null
  const { snap_available, flatpak_available, packages } = data.data

  return (
    <Card
      title={t('sandboxPkg.title')}
      subtitle={t('sandboxPkg.count', { count: packages.length })}
      actions={
        canUse && (
          <div className="row" style={{ gap: '0.5rem' }}>
            {snap_available && (
              <Button size="small" onClick={() => update('snap')}>
                {t('sandboxPkg.updateSnap')}
              </Button>
            )}
            {flatpak_available && (
              <Button size="small" onClick={() => update('flatpak')}>
                {t('sandboxPkg.updateFlatpak')}
              </Button>
            )}
          </div>
        )
      }
    >
      {data.data.notes?.map((n) => (
        <p key={n} className="small muted">
          {n}
        </p>
      ))}
      {canUse && (!snap_available || !flatpak_available) && (
        <div className="row" style={{ gap: '0.5rem', marginBottom: '0.6rem' }}>
          {!snap_available && (
            <Button size="small" onClick={() => setInstalling('snapd')}>
              {t('sandboxPkg.installSnap')}
            </Button>
          )}
          {!flatpak_available && (
            <Button size="small" onClick={() => setInstalling('flatpak')}>
              {t('sandboxPkg.installFlatpak')}
            </Button>
          )}
        </div>
      )}
      {packages.length > 0 && (
        <>
          <Input
            placeholder={t('packages.filterPlaceholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            allowClear
            style={{ maxWidth: '20rem', marginBottom: '0.75rem' }}
          />
          {/* Та же строка кнопок, что у «Установленных»: выбранные — одним действием. */}
          <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem', alignItems: 'center' }}>
            <Button danger disabled={!canUse || picked.length === 0} onClick={removeSelected}>
              {t('packages.removeSelected', { count: picked.length })}
            </Button>
            {picked.length > 0 && (
              <>
                <Button size="small" onClick={() => setPicked([])}>
                  {t('packages.clearSelection')}
                </Button>
                <span className="small secondary mono">{picked.join(', ')}</span>
              </>
            )}
          </div>
          <PackageGrid
            rows={rows}
            picked={picked}
            onPick={setPicked}
            canPick={() => canUse}
            action={(p) => <Tag>{(p as (typeof rows)[number]).kind}</Tag>}
            showVersion
            showState={false}
          />
        </>
      )}
      {installing && (
        <PackageInstallModal
          packageName={installing}
          wsPath={`/system/apt/install/ws?pkgs=${installing}`}
          onClose={() => setInstalling(null)}
          onFinished={() => void data.reload()}
          outcome={null}
          action="install"
        />
      )}
      {running && (
        <CommandModal
          title={running.title}
          wsPath={running.wsPath}
          outcome={running.outcome ?? null}
          onFinished={() => void finished()}
          onClose={() => setRunning(null)}
        />
      )}
    </Card>
  )
}
