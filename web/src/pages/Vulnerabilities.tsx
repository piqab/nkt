import { useMemo, useState } from 'react'
import { Button, Input, Select, Table, type TableColumnsType } from 'antd'
import { api, useApi } from '../api'
import type { Me, Severity, VulnFinding, VulnStatus } from '../types'
import { Banner, Card, ErrorNote, InfoHint, SeverityBadge, formatRelative } from '../components/ui'

// Trivy's own severity scale, mapped onto the app's lowercase Severity
// union so this page can reuse SeverityBadge instead of inventing its own
// colour scheme — UNKNOWN (trivy's own "couldn't determine a real
// severity" bucket) reads as "info", the same tone the rest of the app
// already uses for "not actually a problem, just worth knowing".
const SEVERITY_MAP: Record<VulnFinding['severity'], Severity> = {
  CRITICAL: 'critical',
  HIGH: 'high',
  MEDIUM: 'medium',
  LOW: 'low',
  UNKNOWN: 'info',
}
const SEVERITY_ORDER: VulnFinding['severity'][] = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN']

/**
 * OS-package vulnerabilities (CVEs), via a locally cached trivy — see
 * internal/vuln's own doc comment on why the scan runs against a
 * reconstructed package manifest rather than trivy inspecting a live
 * filesystem, and why the (roughly 1GB) vulnerability database stays put
 * rather than travelling anywhere.
 *
 * Scanning is always explicit (the button below), never triggered just by
 * opening this page: the first run on a host can mean downloading trivy
 * itself plus that whole database, not something a page load should risk
 * kicking off by accident.
 */
export default function Vulnerabilities({ me }: { me: Me }) {
  const canUse = me.is_admin && me.allow_mutations
  const { data: status, reload } = useApi<VulnStatus>('/vulnerabilities', 3_000)
  const [severity, setSeverity] = useState('')
  const [query, setQuery] = useState('')
  // Not part of VulnStatus: a 403 (no admin/mutations) or a network error
  // never reaches s.vuln.lastErr on the backend at all (the request gets
  // rejected before handleVulnScanStart ever runs), so it has nowhere to
  // show up in the next status poll — swallowing it here used to mean the
  // button just silently did nothing.
  const [startError, setStartError] = useState<string | null>(null)

  async function startScan() {
    setStartError(null)
    try {
      await api('/vulnerabilities/scan', { method: 'POST' })
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err))
    }
    reload()
  }

  const findings = status?.scan?.findings ?? []
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return findings
      .filter((f) => !severity || f.severity === severity)
      .filter((f) => !needle || f.id.toLowerCase().includes(needle) || f.package.toLowerCase().includes(needle))
      .sort((a, b) => SEVERITY_ORDER.indexOf(a.severity) - SEVERITY_ORDER.indexOf(b.severity))
  }, [findings, severity, query])

  const counts = useMemo(() => {
    const c: Partial<Record<VulnFinding['severity'], number>> = {}
    for (const f of findings) c[f.severity] = (c[f.severity] ?? 0) + 1
    return c
  }, [findings])

  const columns: TableColumnsType<VulnFinding> = [
    { title: 'Серьёзность', dataIndex: 'severity', width: 140, render: (s: VulnFinding['severity']) => <SeverityBadge severity={SEVERITY_MAP[s]} /> },
    { title: 'CVE', dataIndex: 'id', width: 180, className: 'mono' },
    { title: 'Пакет', dataIndex: 'package', width: 200, className: 'mono' },
    { title: 'Установлено', dataIndex: 'installed_version', width: 160, className: 'mono' },
    {
      title: 'Исправление',
      dataIndex: 'fixed_version',
      width: 160,
      className: 'mono',
      render: (v: string | undefined) => v || <span className="small muted">пока нет</span>,
    },
    { title: 'Описание', dataIndex: 'title' },
  ]

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            Уязвимости
            <InfoHint>
              Известные CVE для установленных пакетов ОС — сверка со свежей базой trivy. Пока
              поддерживаются только Debian/Ubuntu (dpkg); контейнерные образы (Docker/Podman/LXD) —
              отдельным этапом позже.
            </InfoHint>
          </h1>
        </div>
        <div className="row">
          <Button type="primary" disabled={!canUse} loading={status?.scanning} onClick={startScan}>
            {status?.scanning ? status.progress || 'Сканирую…' : status?.scan ? 'Пересканировать' : 'Сканировать'}
          </Button>
        </div>
      </div>

      {!canUse && (
        <Banner kind="info">Доступно только роли admin с включёнными изменениями (AllowMutations).</Banner>
      )}
      <ErrorNote error={startError ?? status?.error ?? null} />

      {!status?.scan ? (
        !status?.scanning && (
          <Banner kind="info">
            Сканирование ещё не запускалось на этом хосте. При первом запуске может понадобиться
            скачать trivy и базу уязвимостей (около 1 ГБ) — это займёт несколько минут.
          </Banner>
        )
      ) : !status.scan.available ? (
        <Banner kind="info">
          На этом хосте нет /var/lib/dpkg/status — сканирование пакетов поддерживается только на
          Debian/Ubuntu.
        </Banner>
      ) : (
        <>
          <p className="small muted">
            Просканировано {formatRelative(status.scan.scanned_at)}, база уязвимостей обновлена{' '}
            {formatRelative(status.scan.db_updated)}.
          </p>

          <Card>
            <div className="filters">
              <label>
                Серьёзность
                <Select
                  value={severity}
                  onChange={setSeverity}
                  style={{ minWidth: '11rem' }}
                  options={[
                    { value: '', label: 'все' },
                    ...SEVERITY_ORDER.map((s) => ({ value: s, label: `${SEVERITY_MAP[s]} (${counts[s] ?? 0})` })),
                  ]}
                />
              </label>
              <label style={{ flex: 1, minWidth: '14rem' }}>
                Поиск
                <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="CVE или имя пакета…" />
              </label>
              <span className="small muted" style={{ paddingBottom: '0.4rem' }}>
                показано {visible.length} из {findings.length}
              </span>
            </div>
          </Card>

          {visible.length === 0 ? (
            <Card>
              <div className="chart-empty">
                {findings.length === 0 ? 'Уязвимостей не найдено.' : 'Ничего не найдено под заданные условия.'}
              </div>
            </Card>
          ) : (
            <Card>
              <div className="table-wrap">
                <Table<VulnFinding>
                  dataSource={visible}
                  columns={columns}
                  rowKey={(f) => `${f.id}-${f.package}`}
                  size="small"
                  pagination={{ pageSize: 50, showSizeChanger: false }}
                />
              </div>
            </Card>
          )}
        </>
      )}
    </>
  )
}
