import { useEffect, useMemo, useState } from 'react'
import { Button, Input, InputNumber, Segmented, Select, Switch, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { UnlockOutlined } from '@ant-design/icons'
import { ApiError, api, qs, useApi } from '../api'
import type { Fail2banBan, Fail2banEvent, Fail2banJail, Fail2banStatus, Fail2banTemplate, FileContent, Me, WriteResult } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, formatDateTime, formatRelative } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { confirmAction } from '../components/confirm'
import { EditTextModal } from '../components/EditTextModal'
import { VersionHistory } from '../components/VersionHistory'
import PackageInstallModal from '../components/PackageInstallModal'
import { FilesDiffModal, IPWithCheck, TemplateApplyModal, TemplatesPanel, underHub, type ApplyChange } from '../components/Fail2banParts'
import { BAN_TIMES, fmtDuration, getIniKey, ignoreCovers, sameIgnore, setIniKey } from '../fail2ban'

/**
 * fail2ban хоста: джейлы, забаненные адреса (разбан галочками, ручной
 * бан), журнал событий за неделю, правка джейлов в окне с диффом и
 * историей, шаблоны. Защита от самоблокировки: адрес хаба — в ignoreip
 * (файл nkt), свой адрес и адрес хаба забанить нельзя.
 */
export default function Fail2ban({ me }: { me: Me }) {
  const { t } = useTranslation()
  const st = useApi<Fail2banStatus>('/fail2ban', 30_000)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [install, setInstall] = useState(false)
  const [edit, setEdit] = useState<{ jail: string; enabled?: boolean } | null>(null)
  const [history, setHistory] = useState<string | null>(null)
  const [templates, setTemplates] = useState(false)
  const [apply, setApply] = useState<Fail2banTemplate | null>(null)
  const [ban, setBan] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [filter, setFilter] = useState('')
  const data = st.data
  const state = data?.state
  const admin = me.is_admin

  async function run(key: string, fn: () => Promise<unknown>, okText?: string) {
    setBusy(key)
    setNotice(null)
    try {
      await fn()
      if (okText) setNotice({ kind: 'info', text: okText })
      st.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const bans = useMemo(() => {
    const all: (Fail2banBan & { key: string })[] = []
    for (const j of state?.jails ?? []) for (const b of j.bans ?? []) all.push({ ...b, key: `${b.jail}|${b.ip}` })
    const q = filter.trim().toLowerCase()
    return q ? all.filter((b) => b.ip.toLowerCase().includes(q) || b.jail.toLowerCase().includes(q)) : all
  }, [state, filter])

  async function unban(keys: string[]) {
    const byJail = new Map<string, string[]>()
    for (const k of keys) {
      const [jail, ip] = k.split('|')
      byJail.set(jail, [...(byJail.get(jail) ?? []), ip])
    }
    for (const [jail, ips] of byJail) await api('/fail2ban/unban', { method: 'POST', body: { jail, ips } })
    setSelected([])
  }

  const hubCovered = (j: Fail2banJail) => !data?.hub_addr || j.name === data.manual_jail || ignoreCovers(j.ignore_ip ?? [], data.hub_addr)
  const runningNames = new Set((state?.jails ?? []).map((j) => j.name))
  const disabledNkt = (data?.nkt_jails ?? []).filter((j) => !runningNames.has(j.jail))

  return (
    <>
      <div className="page-head spread">
        <h1>
          fail2ban
          <InfoHint>{t('fail2ban.hint')}</InfoHint>
        </h1>
        <div className="row">
          {state?.installed && admin && (
            <>
              <Button onClick={() => setTemplates(true)}>{t('fail2ban.templates')}</Button>
              <Tooltip title={t('fail2ban.setupHint')}>
                <Button loading={busy === 'setup'} onClick={() => void run('setup', () => api('/fail2ban/setup', { method: 'POST', timeoutMs: 120_000 }), t('fail2ban.setupDone'))}>
                  {t('fail2ban.setup')}
                </Button>
              </Tooltip>
              <Button disabled={!state.running} loading={busy === 'reload'} onClick={() => void run('reload', () => api('/fail2ban/reload', { method: 'POST', body: {} }), t('fail2ban.reloaded'))}>
                {t('fail2ban.reload')}
              </Button>
            </>
          )}
          <Button onClick={() => st.reload()} loading={st.loading}>
            {t('fail2ban.refresh')}
          </Button>
        </div>
      </div>

      <ErrorNote error={st.error} />
      {notice && <Banner kind={notice.kind}>{notice.text}</Banner>}

      {!data ? (
        <Loading what="fail2ban" />
      ) : !state?.installed ? (
        <Card title={t('fail2ban.notInstalledTitle')}>
          <p className="small">{t('fail2ban.notInstalledText')}</p>
          {admin && (
            <Button type="primary" onClick={() => setInstall(true)}>
              {t('fail2ban.install')}
            </Button>
          )}
        </Card>
      ) : (
        <>
          <Card title={t('fail2ban.stateTitle')}>
            <div className="row" style={{ gap: '1.2rem', flexWrap: 'wrap', alignItems: 'center' }}>
              {state.running ? <Tag color="green">{t('fail2ban.running')}</Tag> : <Tag color="red">{t('fail2ban.stopped')}</Tag>}
              {state.version && <span className="small">{t('fail2ban.version', { v: state.version })}</span>}
              <span className="small">{t('fail2ban.jailsCount', { count: state.jails.length })}</span>
              <span className="small">{t('fail2ban.bannedNow', { count: state.banned_now })}</span>
              <span className="small">
                {data.hub_addr ? (
                  <>
                    {t('fail2ban.hubAddr')} <span className="mono">{data.hub_addr}</span>{' '}
                    {state.jails.every(hubCovered) ? (
                      <Tag color="green">{t('fail2ban.hubIgnored')}</Tag>
                    ) : (
                      <Tag color="red">{t('fail2ban.hubNotIgnored')}</Tag>
                    )}
                  </>
                ) : (
                  <span className="muted">{t(underHub() ? 'fail2ban.hubAddrPending' : 'fail2ban.hubAddrNone')}</span>
                )}
              </span>
              {data.client_ip && (
                <span className="small muted">
                  {t('fail2ban.yourAddr')} <span className="mono">{data.client_ip}</span>
                </span>
              )}
            </div>
            {!state.running && state.error && <Banner kind="error">{state.error}</Banner>}
          </Card>

          <Card title={t('fail2ban.jailsTitle')} subtitle={t('fail2ban.jailsSubtitle')}>
            <div className="table-wrap">
              <DataTable<Fail2banJail>
                dataSource={state.jails}
                rowKey="name"
                columns={[
                  {
                    title: t('fail2ban.colJail'),
                    key: 'name',
                    render: (_, j) => (
                      <div>
                        <strong className="mono">{j.name}</strong>
                        {j.name === data.manual_jail && <Tag style={{ marginLeft: 6 }}>{t('fail2ban.manualTag')}</Tag>}
                        {!hubCovered(j) && <Tag color="red" style={{ marginLeft: 6 }}>{t('fail2ban.hubNotIgnoredShort')}</Tag>}
                      </div>
                    ),
                  },
                  {
                    title: t('fail2ban.colSource'),
                    key: 'src',
                    render: (_, j) =>
                      j.journal ? (
                        <span className="small">journald: <span className="mono">{j.journal}</span></span>
                      ) : (
                        <span className="small mono">
                          {(j.log_paths ?? []).map((p) => (
                            <div key={p} style={(j.missing_logs ?? []).includes(p) ? { color: 'var(--status-error)' } : undefined}>
                              {p}
                              {(j.missing_logs ?? []).includes(p) && ` (${t('fail2ban.missingLog')})`}
                            </div>
                          ))}
                        </span>
                      ),
                  },
                  {
                    title: t('fail2ban.colRules'),
                    key: 'rules',
                    render: (_, j) => (
                      <span className="small nowrap">
                        {t('fail2ban.rules', { count: j.max_retry, find: fmtDuration(j.find_time), ban: fmtDuration(j.ban_time) })}
                      </span>
                    ),
                  },
                  { title: t('fail2ban.colFailed'), key: 'failed', align: 'right', render: (_, j) => <span className="num small">{j.failed} / {j.total_failed}</span> },
                  { title: t('fail2ban.colBanned'), key: 'banned', align: 'right', render: (_, j) => <span className="num small">{j.banned} / {j.total_banned}</span> },
                  {
                    title: '',
                    key: 'actions',
                    render: (_, j) =>
                      admin && (
                        <div className="row row-nowrap">
                          <RowAction action="edit" label={t('fail2ban.editJail')} onClick={() => setEdit({ jail: j.name })} />
                          <RowAction action="reload" label={t('fail2ban.reloadJail')} loading={busy === `reload:${j.name}`} onClick={() => void run(`reload:${j.name}`, () => api('/fail2ban/reload', { method: 'POST', body: { jail: j.name } }), t('fail2ban.reloadedJail', { jail: j.name }))} />
                          {j.name !== data.manual_jail && (
                            <RowAction action="disable" label={t('fail2ban.disableJail')} danger onClick={() => setEdit({ jail: j.name, enabled: false })} />
                          )}
                          <RowAction action="history" label={t('configs.versionHistoryTitle')} onClick={() => setHistory(`${data.root}/jail.d/nkt-${j.name}.local`)} />
                        </div>
                      ),
                  },
                ]}
              />
            </div>
            {disabledNkt.length > 0 && (
              <div className="small" style={{ marginTop: '0.5rem' }}>
                {t('fail2ban.disabledJails')}{' '}
                {disabledNkt.map((j) => (
                  <span key={j.jail} style={{ marginRight: '0.6rem' }}>
                    <span className="mono">{j.jail}</span>{' '}
                    {admin && (
                      <Button size="small" type="link" onClick={() => setEdit({ jail: j.jail, enabled: true })}>
                        {t('fail2ban.enableJail')}
                      </Button>
                    )}
                  </span>
                ))}
              </div>
            )}
          </Card>

          <IgnoreCard status={data} me={me} onEditJail={(jail) => setEdit({ jail })} onChanged={() => st.reload()} />

          <Card
            title={t('fail2ban.bansTitle')}
            subtitle={t('fail2ban.bansSubtitle', { count: bans.length })}
            actions={
              admin && (
                <>
                  <Button type="primary" danger onClick={() => setBan(true)}>
                    {t('fail2ban.banManual')}
                  </Button>
                  <Button
                    danger
                    disabled={state.banned_now === 0}
                    loading={busy === 'unbanAll'}
                    onClick={async () => {
                      if (!(await confirmAction(t('fail2ban.unbanAllConfirm', { count: state.banned_now })))) return
                      void run('unbanAll', () => api('/fail2ban/unban', { method: 'POST', body: { all: true } }), t('fail2ban.unbannedAll'))
                    }}
                  >
                    {t('fail2ban.unbanAll')}
                  </Button>
                </>
              )
            }
          >
            <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem', alignItems: 'center' }}>
              <Input.Search allowClear placeholder={t('fail2ban.searchBans')} value={filter} onChange={(e) => setFilter(e.target.value)} style={{ maxWidth: 280 }} />
              {admin && selected.length > 0 && (
                <>
                  <span className="small">{t('bulk.selected', { count: selected.length })}</span>
                  <Button size="small" loading={busy === 'unbanSel'} onClick={() => void run('unbanSel', () => unban(selected), t('fail2ban.unbanned', { count: selected.length }))}>
                    {t('fail2ban.unbanSelected', { count: selected.length })}
                  </Button>
                  <Button size="small" type="link" onClick={() => setSelected([])}>
                    {t('bulk.clear')}
                  </Button>
                </>
              )}
            </div>
            <div className="table-wrap">
              <DataTable<Fail2banBan & { key: string }>
                dataSource={bans}
                rowKey="key"
                rowSelection={admin ? { selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) } : undefined}
                columns={[
                  { title: t('fail2ban.colIP'), key: 'ip', render: (_, b) => <IPWithCheck ip={b.ip} me={me} /> },
                  { title: t('fail2ban.colJail'), dataIndex: 'jail', key: 'jail', className: 'mono small' },
                  {
                    title: t('fail2ban.colSince'),
                    key: 'since',
                    render: (_, b) => (b.since ? <span className="small nowrap" title={formatDateTime(b.since)}>{formatRelative(b.since)}</span> : '—'),
                  },
                  {
                    title: t('fail2ban.colUntil'),
                    key: 'until',
                    render: (_, b) => <span className="small nowrap">{b.until ? formatDateTime(b.until) : b.since ? t('fail2ban.durForever') : '—'}</span>,
                  },
                  {
                    title: '',
                    key: 'actions',
                    render: (_, b) =>
                      admin && (
                        <RowAction
                          icon={<UnlockOutlined />}
                          label={t('fail2ban.unban')}
                          onClick={() => void run(`unban:${b.key}`, () => unban([b.key]), t('fail2ban.unbanned', { count: 1 }))}
                        />
                      ),
                  },
                ]}
              />
            </div>
          </Card>

          <BanLog status={data} me={me} />
        </>
      )}

      {install && (
        <PackageInstallModal
          packageName="fail2ban"
          wsPath="/fail2ban/install/ws"
          onClose={() => setInstall(false)}
          onFinished={async () => {
            // Сразу после установки: защита хаба, ручной джейл, sshd через
            // journald там, где журнала в файле нет.
            await api('/fail2ban/setup', { method: 'POST', timeoutMs: 120_000 }).catch(() => undefined)
            st.reload()
          }}
        />
      )}
      {edit && data && (
        <JailEditModal
          jail={edit.jail}
          enabled={edit.enabled}
          runtime={state?.jails.find((j) => j.name === edit.jail)}
          status={data}
          me={me}
          onClose={() => setEdit(null)}
          onSaved={() => st.reload()}
        />
      )}
      {history && (
        <Modal title={t('configs.versionHistoryTitle')} onClose={() => setHistory(null)} width={900}>
          <div className="small mono" style={{ marginBottom: '0.4rem' }}>{history}</div>
          <VersionHistory path={history} me={me} apply onChanged={() => st.reload()} />
        </Modal>
      )}
      {templates && (
        <Modal title={t('fail2ban.templates')} onClose={() => setTemplates(false)} width={960}>
          <TemplatesPanel
            me={me}
            onApply={(tpl) => {
              setTemplates(false)
              setApply(tpl)
            }}
          />
        </Modal>
      )}
      {apply && <TemplateApplyModal template={apply} hubAddr={data?.hub_addr} onClose={() => setApply(null)} onApplied={() => st.reload()} />}
      {ban && data && <ManualBanModal status={data} onClose={() => setBan(false)} onDone={() => st.reload()} />}
    </>
  )
}

/** Ручной бан: адреса, джейл (по умолчанию ручной джейл nkt), срок. */
function ManualBanModal({ status, onClose, onDone }: { status: Fail2banStatus; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [ips, setIps] = useState('')
  const [jail, setJail] = useState(status.manual_jail)
  const [banTime, setBanTime] = useState(7 * 86400)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const list = ips.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
  const own = list.filter((ip) => ip === status.client_ip || ip === status.hub_addr)

  async function go() {
    setBusy(true)
    setError(null)
    try {
      await api('/fail2ban/ban', { method: 'POST', body: { jail, ips: list, ban_time: jail === status.manual_jail ? banTime : 0 } })
      onDone()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('fail2ban.banManual')} onClose={onClose} width={560}>
      <div className="col">
        <p className="small muted">{t('fail2ban.banManualHint')}</p>
        <label>
          {t('fail2ban.ipsLabel')}
          <Input.TextArea rows={3} value={ips} onChange={(e) => setIps(e.target.value)} className="mono" placeholder="203.0.113.10" />
        </label>
        <label>
          {t('fail2ban.colJail')}
          <Select
            value={jail}
            onChange={setJail}
            options={[
              { value: status.manual_jail, label: `${status.manual_jail} — ${t('fail2ban.manualTag')}` },
              ...status.state.jails.filter((j) => j.name !== status.manual_jail).map((j) => ({ value: j.name, label: j.name })),
            ]}
          />
        </label>
        {jail === status.manual_jail ? (
          <label>
            {t('fail2ban.banTimeLabel')}
            <Select value={banTime} onChange={setBanTime} options={BAN_TIMES.map((s) => ({ value: s, label: fmtDuration(s) }))} style={{ maxWidth: 200 }} />
          </label>
        ) : (
          <div className="small muted">{t('fail2ban.jailBanTime')}</div>
        )}
        {own.length > 0 && <Banner kind="error">{t('fail2ban.ownAddrWarning', { ips: own.join(', ') })}</Banner>}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row">
          <Button type="primary" danger loading={busy} disabled={list.length === 0 || own.length > 0} onClick={() => void go()}>
            {t('fail2ban.banGo', { count: list.length })}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * Правка джейла: файл nkt jail.d/nkt-<джейл>.local, общий путь записи
 * конфигураций (fail2ban-client -t, откат при ошибке, перезагрузка,
 * история). Форма меняет строки текста, текст можно править и руками.
 */
function JailEditModal({
  jail,
  enabled,
  runtime,
  status,
  me,
  onClose,
  onSaved,
}: {
  jail: string
  enabled?: boolean
  runtime?: Fail2banJail
  status: Fail2banStatus
  me: Me
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const path = `${status.root}/jail.d/nkt-${jail}.local`
  const [file, setFile] = useState<FileContent | null | undefined>(undefined)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [history, setHistory] = useState(false)

  useEffect(() => {
    api<FileContent>(`/configs/file${qs({ path })}`)
      .then((f) => {
        setFile(f)
        setDraft(enabled === undefined ? f.content : setIniKey(f.content, jail, 'enabled', String(enabled)))
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status !== 404 && err.status !== 400) {
          setResult({ kind: 'error', text: err.message })
        }
        setFile(null)
        const base = `# Managed by nkt\n[${jail}]\n`
        setDraft(enabled === undefined ? base : setIniKey(base, jail, 'enabled', String(enabled)))
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- один раз на открытие
  }, [])

  const get = (key: string) => getIniKey(draft, jail, key) ?? ''
  const set = (key: string, value: string | null) => setDraft((d) => setIniKey(d, jail, key, value))

  // Свой ignoreip у джейла заменяет [DEFAULT] целиком — адрес хаба
  // добавляется в него сам, иначе хаб может забанить сам себя.
  function setIgnore(value: string) {
    let v = value
    const list = v.split(/[\s,]+/).filter(Boolean)
    if (v.trim() && status.hub_addr && !ignoreCovers(list, status.hub_addr)) v = `${v.trim()} ${status.hub_addr}`
    set('ignoreip', v)
  }

  async function save(): Promise<boolean> {
    setBusy(true)
    setResult(null)
    try {
      const res = await api<WriteResult>('/configs/file', {
        method: 'PUT',
        body: { path, content: draft, note, apply: true, expected_sha256: file?.sha256 ?? '' },
        timeoutMs: 120_000,
      })
      onSaved()
      if (res.rolled_back) {
        setResult({ kind: 'error', text: res.message })
        return false
      }
      return true
    } catch (err) {
      setResult({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  if (file === undefined) {
    return (
      <Modal title={t('fail2ban.editTitle', { jail })} onClose={onClose}>
        <Loading />
      </Modal>
    )
  }
  const enabledValue = get('enabled')
  return (
    <>
      <EditTextModal
        title={t('fail2ban.editTitle', { jail })}
        saved={file?.content ?? ''}
        draft={draft}
        onDraft={setDraft}
        busy={busy}
        onSave={save}
        onHistory={file ? () => setHistory(true) : undefined}
        onClose={onClose}
        rows={12}
        fields={
          <div className="col" style={{ marginBottom: '0.5rem' }}>
            <div className="small muted mono">{path}</div>
            <div className="small muted">{t('fail2ban.editHint')}</div>
            <div className="row" style={{ gap: '0.8rem', flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
                <Switch
                  checked={enabledValue === '' ? true : enabledValue.toLowerCase() === 'true'}
                  onChange={(v) => set('enabled', String(v))}
                />
                {t('fail2ban.fieldEnabled')}
              </label>
              <label>
                maxretry
                <InputNumber min={1} max={1000} value={get('maxretry') ? Number(get('maxretry')) : null} placeholder={runtime ? String(runtime.max_retry) : ''} onChange={(v) => set('maxretry', v === null ? null : String(v))} />
              </label>
              <label>
                findtime
                <Input value={get('findtime')} placeholder={runtime ? fmtDuration(runtime.find_time) : '10m'} onChange={(e) => set('findtime', e.target.value)} style={{ width: 110 }} />
              </label>
              <label>
                bantime
                <Input value={get('bantime')} placeholder={runtime ? fmtDuration(runtime.ban_time) : '1h'} onChange={(e) => set('bantime', e.target.value)} style={{ width: 110 }} />
              </label>
              <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
                <Switch checked={get('bantime.increment').toLowerCase() === 'true'} onChange={(v) => set('bantime.increment', v ? 'true' : null)} />
                bantime.increment
              </label>
              <label>
                backend
                <Segmented
                  size="small"
                  value={get('backend') || 'auto'}
                  onChange={(v) => set('backend', v === 'auto' ? null : String(v))}
                  options={['auto', 'systemd', 'polling', 'pyinotify']}
                />
              </label>
            </div>
            <label>
              logpath
              <Input value={get('logpath')} placeholder={runtime?.log_paths?.join(' ') ?? ''} onChange={(e) => set('logpath', e.target.value)} className="mono" />
            </label>
            <label>
              ignoreip
              <Input value={get('ignoreip')} placeholder={t('fail2ban.ignorePlaceholder', { list: status.default_ignore.join(' ') })} onChange={(e) => setIgnore(e.target.value)} className="mono" />
            </label>
            {status.hub_addr && <div className="small muted">{t('fail2ban.ignoreHubHint', { addr: status.hub_addr })}</div>}
            <label>
              {t('configs.editNote')}
              <Input value={note} onChange={(e) => setNote(e.target.value)} />
            </label>
          </div>
        }
        below={result && <Banner kind={result.kind}>{result.text}</Banner>}
      />
      {history && (
        <Modal title={t('configs.versionHistoryTitle')} onClose={() => setHistory(false)} width={900}>
          <VersionHistory path={path} me={me} apply onChanged={() => { onSaved(); onClose() }} />
        </Modal>
      )}
    </>
  )
}

/** Журнал событий fail2ban за последние дни с поиском. */
function BanLog({ status, me }: { status: Fail2banStatus; me: Me }) {
  const { t } = useTranslation()
  const [days, setDays] = useState(7)
  const [query, setQuery] = useState('')
  const [text, setText] = useState('')
  const [jail, setJail] = useState('')
  const [action, setAction] = useState('')
  const log = useApi<{ events: Fail2banEvent[]; total: number; source: string; truncated: boolean }>(
    `/fail2ban/log${qs({ days, q: query, jail, action, limit: 1000 })}`,
  )
  const color: Record<string, string> = { Ban: 'red', Unban: 'green', Found: 'orange', 'Restore Ban': 'volcano', 'Increase Ban': 'magenta' }
  return (
    <Card
      title={t('fail2ban.logTitle')}
      subtitle={log.data ? t('fail2ban.logSubtitle', { total: log.data.total, source: log.data.source }) : undefined}
    >
      <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
        <Segmented size="small" value={days} onChange={(v) => setDays(Number(v))} options={[1, 3, 7].map((d) => ({ value: d, label: t('fail2ban.durDays', { n: d }) }))} />
        <Input.Search
          allowClear
          placeholder={t('fail2ban.logSearch')}
          value={text}
          onChange={(e) => {
            setText(e.target.value)
            if (!e.target.value) setQuery('')
          }}
          onSearch={(v) => setQuery(v.trim())}
          style={{ maxWidth: 280 }}
        />
        <Select
          value={jail}
          onChange={setJail}
          style={{ minWidth: 160 }}
          options={[{ value: '', label: t('fail2ban.allJails') }, ...status.state.jails.map((j) => ({ value: j.name, label: j.name }))]}
        />
        <Select
          value={action}
          onChange={setAction}
          style={{ minWidth: 140 }}
          options={[{ value: '', label: t('fail2ban.allActions') }, ...status.actions.map((a) => ({ value: a, label: a }))]}
        />
      </div>
      <ErrorNote error={log.error} />
      {log.data?.truncated && <div className="small muted">{t('fail2ban.logTruncated', { shown: log.data.events.length })}</div>}
      <div className="table-wrap">
        <DataTable<Fail2banEvent>
          loading={log.loading && !log.data}
          dataSource={log.data?.events ?? []}
          rowKey={(e) => `${e.ts}|${e.line}`}
          columns={[
            { title: t('fail2ban.colTime'), key: 'ts', render: (_, e) => <span className="small nowrap" title={formatDateTime(e.ts)}>{formatDateTime(e.ts)}</span> },
            { title: t('fail2ban.colJail'), dataIndex: 'jail', key: 'jail', className: 'mono small' },
            { title: t('fail2ban.colAction'), key: 'action', render: (_, e) => <Tag color={color[e.action]}>{e.action}</Tag> },
            { title: t('fail2ban.colIP'), key: 'ip', render: (_, e) => <IPWithCheck ip={e.ip} me={me} /> },
            { title: t('fail2ban.colLine'), key: 'line', render: (_, e) => <span className="small muted mono" style={{ wordBreak: 'break-all' }}>{e.line}</span> },
          ]}
        />
      </div>
    </Card>
  )
}

/**
 * Исключения (ignoreip): общий список [DEFAULT] — правится здесь, с
 * диффом и историей; адрес хаба в нём закреплён (файл защиты nkt). Ниже —
 * действующий список каждого джейла: общий или свой (свой заменяет
 * общий целиком и правится в окне джейла).
 */
function IgnoreCard({
  status,
  me,
  onEditJail,
  onChanged,
}: {
  status: Fail2banStatus
  me: Me
  onEditJail: (jail: string) => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const [edit, setEdit] = useState(false)
  const [history, setHistory] = useState(false)
  const common = status.default_ignore
  const effective = status.hub_addr && !ignoreCovers(common, status.hub_addr) ? [...common, status.hub_addr] : common
  const same = (list: string[]) => sameIgnore(list, effective) || sameIgnore(list, common)
  const jails = status.state.jails.filter((j) => j.name !== status.manual_jail)
  return (
    <Card
      title={t('fail2ban.ignoreTitle')}
      subtitle={t('fail2ban.ignoreSubtitle')}
      actions={
        <>
          {me.is_admin && <Button onClick={() => setEdit(true)}>{t('fail2ban.ignoreEdit')}</Button>}
          {status.ignore_defined && <Button onClick={() => setHistory(true)}>{t('configs.versionHistoryTitle')}</Button>}
        </>
      }
    >
      <div className="col" style={{ gap: '0.4rem' }}>
        <div className="row" style={{ flexWrap: 'wrap', gap: '0.3rem', alignItems: 'center' }}>
          <span className="small">{t('fail2ban.ignoreCommon')}</span>
          {common.map((ip) => (
            <Tag key={ip} className="mono">{ip}</Tag>
          ))}
          {status.hub_addr && (
            <Tooltip title={t('fail2ban.ignoreHubPinned')}>
              <Tag color="blue" className="mono">
                {status.hub_addr} · {t('fail2ban.hubTag')}
              </Tag>
            </Tooltip>
          )}
        </div>
        <div className="small muted">
          {status.ignore_defined
            ? t('fail2ban.ignoreSource', { path: status.ignore_source })
            : t('fail2ban.ignoreSourceNew', { path: status.ignore_source })}
        </div>
        {jails.length > 0 && (
          <div className="table-wrap">
            <DataTable<Fail2banJail>
              dataSource={jails}
              rowKey="name"
              columns={[
                { title: t('fail2ban.colJail'), dataIndex: 'name', key: 'name', className: 'mono small' },
                {
                  title: t('fail2ban.ignoreEffective'),
                  key: 'list',
                  render: (_, j) => (
                    <div className="row" style={{ flexWrap: 'wrap', gap: '0.2rem' }}>
                      {(j.ignore_ip ?? []).map((ip) => (
                        <Tag key={ip} className="mono" color={status.hub_addr && ip === status.hub_addr ? 'blue' : undefined}>
                          {ip}
                        </Tag>
                      ))}
                    </div>
                  ),
                },
                {
                  title: '',
                  key: 'kind',
                  render: (_, j) =>
                    same(j.ignore_ip ?? []) ? (
                      <Tag>{t('fail2ban.ignoreKindCommon')}</Tag>
                    ) : (
                      <span className="row row-nowrap" style={{ gap: '0.3rem' }}>
                        <Tooltip title={t('fail2ban.ignoreKindOwnHint')}>
                          <Tag color="gold">{t('fail2ban.ignoreKindOwn')}</Tag>
                        </Tooltip>
                        {me.is_admin && <RowAction action="edit" label={t('fail2ban.editJail')} onClick={() => onEditJail(j.name)} />}
                      </span>
                    ),
                },
              ]}
            />
          </div>
        )}
      </div>
      {edit && <IgnoreEditModal status={status} onClose={() => setEdit(false)} onSaved={onChanged} />}
      {history && (
        <Modal title={t('configs.versionHistoryTitle')} onClose={() => setHistory(false)} width={900}>
          <div className="small mono" style={{ marginBottom: '0.4rem' }}>{status.ignore_source}</div>
          <VersionHistory path={status.ignore_source} me={me} apply onChanged={onChanged} />
        </Modal>
      )}
    </Card>
  )
}

/** Правка общего ignoreip: список, заметка, дифф файлов, запись. */
function IgnoreEditModal({ status, onClose, onSaved }: { status: Fail2banStatus; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const [text, setText] = useState(status.default_ignore.join('\n'))
  const [note, setNote] = useState('')
  const [changes, setChanges] = useState<ApplyChange[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const list = text.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)

  async function preview() {
    setError(null)
    try {
      const res = await api<{ files: ApplyChange[] }>('/fail2ban/ignore', { method: 'PUT', body: { list, dry_run: true } })
      setChanges(res.files)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function write() {
    setBusy(true)
    setError(null)
    try {
      await api('/fail2ban/ignore', { method: 'PUT', body: { list, note }, timeoutMs: 120_000 })
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setChanges(null)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('fail2ban.ignoreEditTitle')} onClose={onClose} width={620} maskClosable={false}>
      <div className="col">
        <p className="small muted">{t('fail2ban.ignoreEditHint', { path: status.ignore_source })}</p>
        <Input.TextArea rows={8} value={text} onChange={(e) => setText(e.target.value)} className="mono" />
        {status.hub_addr && <div className="small muted">{t('fail2ban.ignoreHubPinnedEdit', { addr: status.hub_addr })}</div>}
        {status.client_ip && !ignoreCovers(list, status.client_ip) && (
          <Button size="small" type="link" style={{ alignSelf: 'flex-start', padding: 0 }} onClick={() => setText((v) => `${v.trim()}\n${status.client_ip}`)}>
            {t('fail2ban.ignoreAddMine', { ip: status.client_ip })}
          </Button>
        )}
        <label>
          {t('configs.editNote')}
          <Input value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row">
          <Button type="primary" onClick={() => void preview()}>
            {t('fail2ban.tplApplyGo')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
      {changes && <FilesDiffModal changes={changes} hint={t('fail2ban.tplApplyWriteHint')} busy={busy} onWrite={() => void write()} onClose={() => setChanges(null)} />}
    </Modal>
  )
}
