import { useMemo, useState } from 'react'
import { Button, Input, Select, Spin, Table, Tag, type TableColumnsType } from 'antd'
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
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)

  // Narrowing the filter can easily leave the table sitting on a page that
  // no longer has anything on it (e.g. page 3 of "все", then filtered down
  // to a severity with only one page's worth) — reset back to the start
  // whenever what's being filtered changes, same as Findings.tsx's own
  // Table would need if it paginated.
  function changeSeverity(v: string) {
    setSeverity(v)
    setPage(1)
  }
  function changeQuery(v: string) {
    setQuery(v)
    setPage(1)
  }
  // Not part of VulnStatus: a 403 (no admin/mutations) or a network error
  // never reaches s.vuln.lastErr on the backend at all (the request gets
  // rejected before handleVulnScanStart ever runs), so it has nowhere to
  // show up in the next status poll — swallowing it here used to mean the
  // button just silently did nothing.
  const [startError, setStartError] = useState<string | null>(null)
  // Once trivy + its DB are already cached (true for every scan after the
  // very first one), a re-scan itself finishes in well under a second —
  // faster than the 3s poll below could ever catch a scanning:true in
  // flight, and often faster than the single reload() right after the
  // POST too. Relying on the polled status alone meant the button's own
  // spinner could flash for 0ms or not appear at all, which read as
  // "clicking did nothing" even though the scan genuinely ran and
  // updated. `starting` gives the click itself immediate, guaranteed-
  // visible feedback for a fixed minimum stretch, independent of how fast
  // the backend actually finishes; status.scanning takes back over
  // seamlessly for a slow first run (downloading trivy + a ~1GB DB) that
  // outlives this window.
  const [starting, setStarting] = useState(false)
  const scanning = starting || (status?.scanning ?? false)

  async function startScan() {
    setStartError(null)
    setStarting(true)
    try {
      await api('/vulnerabilities/scan', { method: 'POST' })
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err))
      setStarting(false)
      return
    }
    // Only the successful path waits out the minimum visible stretch — an
    // error (e.g. 409 "already running") should surface immediately, not
    // after a pointless delay.
    await new Promise((resolve) => setTimeout(resolve, 700))
    setStarting(false)
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
    {
      title: 'CVE',
      dataIndex: 'id',
      width: 180,
      render: (id: string, f: VulnFinding) => (
        <span>
          <span className="mono">{id}</span>
          {f.new && (
            <Tag color="blue" style={{ marginLeft: '0.4rem' }}>
              новое
            </Tag>
          )}
        </span>
      ),
    },
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
          <Button type="primary" disabled={!canUse} loading={scanning} onClick={startScan}>
            {scanning ? status?.progress || 'Сканирую…' : status?.scan ? 'Пересканировать' : 'Сканировать'}
          </Button>
        </div>
      </div>

      {!canUse && (
        <Banner kind="info">Доступно только роли admin с включёнными изменениями (AllowMutations).</Banner>
      )}
      <ErrorNote error={startError ?? status?.error ?? null} />

      {/* Shown for the whole scan, not just while there are no results yet
          — a re-scan over existing findings needs the same "this is
          running" signal just as much as the very first one does. The
          button's own antd `loading` spinner alone was easy to miss during
          a run that can legitimately take several minutes (first-ever scan
          on a host: downloading trivy plus its ~1GB database). */}
      {scanning && (
        <Banner kind="info">
          <Spin size="small" style={{ marginRight: '0.6rem' }} />
          {status?.progress || 'Сканирую…'}
          {!status?.scan && ' Первый запуск может занять несколько минут — идёт загрузка trivy и базы уязвимостей.'}
        </Banner>
      )}

      {!status?.scan ? (
        !scanning && (
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

          {status.scan.compared && (
            <Banner kind={(status.scan.new_count ?? 0) > 0 ? 'warn' : 'success'}>
              {(status.scan.new_count ?? 0) === 0 && (status.scan.fixed_count ?? 0) === 0
                ? 'С прошлого скана ничего не изменилось.'
                : `С прошлого скана: +${status.scan.new_count ?? 0} новых, ${status.scan.fixed_count ?? 0} исправлено.`}
            </Banner>
          )}

          <Card>
            <div className="filters">
              <label>
                Серьёзность
                <Select
                  value={severity}
                  onChange={changeSeverity}
                  style={{ minWidth: '11rem' }}
                  options={[
                    { value: '', label: 'все' },
                    ...SEVERITY_ORDER.map((s) => ({ value: s, label: `${SEVERITY_MAP[s]} (${counts[s] ?? 0})` })),
                  ]}
                />
              </label>
              <label style={{ flex: 1, minWidth: '14rem' }}>
                Поиск
                <Input value={query} onChange={(e) => changeQuery(e.target.value)} placeholder="CVE или имя пакета…" />
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
                  pagination={{
                    current: page,
                    pageSize,
                    total: visible.length,
                    position: ['topRight'],
                    showSizeChanger: true,
                    pageSizeOptions: [...new Set([50, 100, Math.max(visible.length, 50)])],
                    onChange: (p, ps) => {
                      setPage(p)
                      setPageSize(ps)
                    },
                  }}
                />
              </div>
            </Card>
          )}
        </>
      )}
    </>
  )
}
