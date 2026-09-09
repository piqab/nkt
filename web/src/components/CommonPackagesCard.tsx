import { useState } from 'react'
import { Button, Checkbox, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, Loading } from './ui'
import PackageInstallModal from './PackageInstallModal'

interface PackageStatus {
  name: string
  installed: boolean
}

// Static UI copy (what each tool is for), not dynamic/error text — kept
// here in i18n rather than coming from the backend, which only ever
// reports installed/not. Keyed by the same logical name the API uses
// (internal/api/handlers_packages.go's commonPackages).
const PACKAGE_DESC_KEY: Record<string, string> = {
  nvim: 'commonPackages.descNvim',
  tmux: 'commonPackages.descTmux',
  gpg: 'commonPackages.descGpg',
  curl: 'commonPackages.descCurl',
  ssh: 'commonPackages.descSsh',
  git: 'commonPackages.descGit',
  wget: 'commonPackages.descWget',
  htop: 'commonPackages.descHtop',
  ncdu: 'commonPackages.descNcdu',
  tree: 'commonPackages.descTree',
  unzip: 'commonPackages.descUnzip',
  rsync: 'commonPackages.descRsync',
  jq: 'commonPackages.descJq',
  'net-tools': 'commonPackages.descNetTools',
  mtr: 'commonPackages.descMtr',
  tcpdump: 'commonPackages.descTcpdump',
  python3: 'commonPackages.descPython3',
}

/**
 * A small, curated set of everyday CLI tools — checkbox-select several,
 * install or remove them in one apt-get call. Deliberately not a generic
 * "search any apt package" surface: short, recognizable names only. Shares
 * PackageInstallModal with every other install button in the app
 * (tmux/dbus/ufw/firewalld/Services' own per-service install) —
 * pointed at the bulk /system/packages/{install,remove}/ws routes here
 * instead of a single fixed package.
 */
export default function CommonPackagesCard({ canUse }: { canUse: boolean }) {
  const { t } = useTranslation()
  const { data, reload } = useApi<{ packages: PackageStatus[] }>('/system/packages/status', 15_000)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [action, setAction] = useState<'install' | 'remove' | null>(null)
  const [outcome, setOutcome] = useState<{ ok: boolean; exitCode?: number } | null>(null)

  const packages = data?.packages ?? []

  function toggle(name: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  function startAction(next: 'install' | 'remove') {
    if (selected.size === 0) return
    setOutcome(null)
    setAction(next)
  }

  async function handleFinished() {
    if (!action) return
    const fresh = await api<{ succeeded?: boolean; exit_code?: number }>(`/system/packages/${action}/status`).catch(
      () => null,
    )
    setOutcome(fresh?.succeeded ? { ok: true } : { ok: false, exitCode: fresh?.exit_code })
    setSelected(new Set())
    await reload()
  }

  return (
    <Card title={t('commonPackages.title')}>
      {!canUse && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}
      {!data ? (
        <Loading what={t('commonPackages.title')} />
      ) : (
        <>
          {/* Плотная сетка, а не таблица: пакетов около двух десятков, все
              строки одинаковой формы, и таблица на всю ширину растягивала
              их в длинный столбец, который приходилось прокручивать.
              Колонки набираются сами по ширине карточки; описание — во
              всплывающей подсказке, чтобы строка оставалась в одну
              высоту. */}
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(15rem, 1fr))',
              columnGap: '1.2rem',
              rowGap: '0.1rem',
            }}
          >
            {packages.map((p) => (
              <Tooltip key={p.name} title={t(PACKAGE_DESC_KEY[p.name])} placement="topLeft">
                <label
                  className="row"
                  style={{
                    alignItems: 'center',
                    gap: '0.4rem',
                    padding: '0.1rem 0',
                    cursor: canUse ? 'pointer' : 'default',
                  }}
                >
                  <Checkbox
                    checked={selected.has(p.name)}
                    disabled={!canUse}
                    onChange={() => toggle(p.name)}
                  />
                  <code className="mono">{p.name}</code>
                  {p.installed ? (
                    <span className="small" style={{ color: 'var(--status-good)' }}>
                      {t('commonPackages.installed')}
                    </span>
                  ) : (
                    <span className="small muted">—</span>
                  )}
                </label>
              </Tooltip>
            ))}
          </div>
          <div className="row" style={{ gap: '0.5rem', marginTop: '0.75rem' }}>
            <Button type="primary" disabled={!canUse || selected.size === 0} onClick={() => startAction('install')}>
              {t('commonPackages.installSelected')}
            </Button>
            <Button danger disabled={!canUse || selected.size === 0} onClick={() => startAction('remove')}>
              {t('commonPackages.removeSelected')}
            </Button>
          </div>
        </>
      )}

      {action && (
        <PackageInstallModal
          packageName={[...selected].join(', ')}
          wsPath={`/system/packages/${action}/ws?pkgs=${[...selected].join(',')}`}
          onClose={() => setAction(null)}
          onFinished={handleFinished}
          outcome={outcome}
          action={action}
        />
      )}
    </Card>
  )
}
