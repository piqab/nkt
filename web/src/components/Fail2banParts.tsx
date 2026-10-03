import { useEffect, useState } from 'react'
import { Button, Checkbox, Input, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, hostScope, useApi } from '../api'
import type { Fail2banTemplate, Job, Me } from '../types'
import { JobLogModal } from '../pages/Jobs'
import { AIExplain } from './AIExplain'
import { Banner, CodeEditor, DiffView, Loading, Modal } from './ui'
import { DataTable } from './DataTable'
import { RowAction } from './RowAction'
import { confirmAction } from './confirm'
import { VersionHistory } from './VersionHistory'
import { unifiedDiff } from './textDiff'
import { BAN_TIMES, fmtDuration, isExternalIP } from '../fail2ban'
import { F2BTemplateFleetModal } from './F2BTemplateFleetModal'

/**
 * Общие части раздела fail2ban: хоста и хаба.
 *
 * Свои шаблоны джейлов живут на хабе (его встроенный API, /hosts/local):
 * под хабом страница любого хоста берёт их оттуда, на одиночном хосте —
 * из его собственного API.
 */

/** Куда ходить за своими шаблонами и их историей. */
export function templatesBase(hubLevel = false): string {
  return hubLevel || hostScope.id !== null ? '/hosts/local' : ''
}

/** Страница открыта через хаб (есть ИИ-разбор и бан на всех хостах). */
export function underHub(hubLevel = false): boolean {
  return hubLevel || hostScope.id !== null
}

interface FleetHost {
  id: number
  name: string
  known: boolean
  installed: boolean
  running: boolean
  banned: number
}

interface FleetParams {
  action: 'ban' | 'unban'
  ips: string[]
  ban_time: number
  host_ids: number[]
}

/** Сколько секунд держится «Отменить» после бана или разбана. */
export const UNDO_SECONDS = 15

/** Обратное задание к завершившемуся успешно: разбан тех же адресов на
 * тех же хостах или бан на неделю (прежний срок не известен). Хосты —
 * из входа задания: сервер записывает туда тех, на ком оно шло. */
export function reverseFleet(j: Job): FleetParams | null {
  if (j.status !== 'succeeded' || !j.params) return null
  try {
    const p = JSON.parse(j.params) as { action?: string; ips?: string[]; host_ids?: number[] }
    if (!p.ips?.length || !p.host_ids?.length) return null
    if (p.action === 'ban') return { action: 'unban', ips: p.ips, ban_time: 0, host_ids: p.host_ids }
    if (p.action === 'unban') return { action: 'ban', ips: p.ips, ban_time: 7 * 86400, host_ids: p.host_ids }
  } catch {
    // Вход не разобрался — отменять нечего.
  }
  return null
}

/** Плашка внизу экрана с обратным отсчётом и «Отменить» — поверх окон. */
export function UndoBar({ text, onUndo, onExpire }: { text: string; onUndo: () => void; onExpire: () => void }) {
  const { t } = useTranslation()
  const [left, setLeft] = useState(UNDO_SECONDS)
  useEffect(() => {
    if (left <= 0) {
      onExpire()
      return
    }
    const id = window.setTimeout(() => setLeft((n) => n - 1), 1000)
    return () => window.clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- отсчёт идёт сам, onExpire зовётся один раз
  }, [left])
  return (
    <div className="undo-bar" role="status">
      <span>{text}</span>
      <Button size="small" type="primary" onClick={onUndo}>
        {t('fail2ban.undo', { n: left })}
      </Button>
    </div>
  )
}

/**
 * Бан (или разбан) адресов на хостах — задание хаба с журналом. Хосты по
 * умолчанию — все, где есть fail2ban.
 */
export function FleetBanModal({
  ips: initial,
  action,
  onClose,
  onDone,
}: {
  ips: string[]
  action: 'ban' | 'unban'
  onClose: () => void
  onDone?: () => void
}) {
  const { t } = useTranslation()
  const hosts = useApi<{ hosts: FleetHost[] }>('/hub/fail2ban/banned')
  const [ipsText, setIpsText] = useState(initial.join('\n'))
  const [banTime, setBanTime] = useState(7 * 86400)
  const [picked, setPicked] = useState<number[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const list = hosts.data?.hosts ?? []
  const eligible = list.filter((h) => !h.known || h.installed)
  const selected = picked ?? eligible.map((h) => h.id)
  const ips = ipsText.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
  // Обратное задание после успеха: «Отменить» на плашке 15 секунд.
  const [undo, setUndo] = useState<FleetParams | null>(null)
  const [logOpen, setLogOpen] = useState(true)
  // Отменённое уже не отменяется: у обратного задания плашки нет.
  const [undone, setUndone] = useState(false)

  async function run(p: FleetParams) {
    const res = await api<{ job_id: number }>('/hub/fail2ban/fleet', { method: 'POST', body: p })
    setLogOpen(true)
    setJob(await api<Job>(`/hosts/local/jobs/${res.job_id}`))
  }

  async function start() {
    // Бан внутреннего адреса (частная сеть, loopback, link-local) может
    // отрезать доступ своим: прокси, VPN, соседней машине, самому хабу.
    const internal = action === 'ban' ? ips.filter((ip) => !isExternalIP(ip)) : []
    if (internal.length > 0 && !(await confirmAction(t('fail2ban.fleetPrivateConfirm', { ips: internal.join(', ') }), { title: t('fail2ban.fleetPrivateTitle') }))) return
    setBusy(true)
    setError(null)
    try {
      await run({ action, ips, ban_time: action === 'ban' ? banTime : 0, host_ids: selected })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (job) {
    return (
      <>
        {logOpen && (
          <JobLogModal
            key={job.id}
            job={job}
            scope="/hosts/local"
            onClose={() => (undo ? setLogOpen(false) : onClose())}
            onDone={(j) => {
              onDone?.()
              const p = reverseFleet(j)
              if (p && !undone) setUndo(p)
            }}
          />
        )}
        {undo && (
          <UndoBar
            text={t(undo.action === 'unban' ? 'fail2ban.undoBanned' : 'fail2ban.undoUnbanned', { ips: undo.ips.join(', '), count: undo.host_ids.length })}
            onUndo={async () => {
              const p = undo
              setUndo(null)
              setUndone(true)
              try {
                await run(p)
              } catch (err) {
                setError(err instanceof Error ? err.message : String(err))
              }
            }}
            onExpire={() => {
              setUndo(null)
              if (!logOpen) onClose()
            }}
          />
        )}
        {error && !logOpen && !undo && (
          <Modal title={t(action === 'ban' ? 'fail2ban.fleetBanTitle' : 'fail2ban.fleetUnbanTitle')} onClose={onClose}>
            <Banner kind="error">{error}</Banner>
          </Modal>
        )}
      </>
    )
  }
  return (
    <Modal title={t(action === 'ban' ? 'fail2ban.fleetBanTitle' : 'fail2ban.fleetUnbanTitle')} onClose={onClose} width={620}>
      <div className="col">
        <p className="small muted">{t(action === 'ban' ? 'fail2ban.fleetBanHint' : 'fail2ban.fleetUnbanHint')}</p>
        <label>
          {t('fail2ban.ipsLabel')}
          <Input.TextArea rows={3} value={ipsText} onChange={(e) => setIpsText(e.target.value)} className="mono" />
        </label>
        {action === 'ban' && (
          <label>
            {t('fail2ban.banTimeLabel')}
            <Select
              value={banTime}
              onChange={setBanTime}
              options={BAN_TIMES.map((s) => ({ value: s, label: fmtDuration(s) }))}
              style={{ maxWidth: 200 }}
            />
          </label>
        )}
        <div>
          <div className="small" style={{ marginBottom: '0.3rem' }}>{t('fail2ban.fleetHosts')}</div>
          {hosts.loading && !hosts.data ? (
            <Loading />
          ) : (
            <div className="row" style={{ flexWrap: 'wrap', gap: '0.3rem 0.9rem' }}>
              {list.map((h) => (
                <Checkbox
                  key={h.id}
                  checked={selected.includes(h.id)}
                  disabled={h.known && !h.installed}
                  onChange={(e) =>
                    setPicked(e.target.checked ? [...selected, h.id] : selected.filter((id) => id !== h.id))
                  }
                >
                  {h.name}{' '}
                  {h.known && !h.installed ? (
                    <Tag>{t('fail2ban.notInstalledShort')}</Tag>
                  ) : !h.known ? (
                    <Tag>{t('fail2ban.unknownShort')}</Tag>
                  ) : null}
                </Checkbox>
              ))}
            </div>
          )}
        </div>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row">
          <Button type="primary" danger={action === 'ban'} loading={busy} disabled={ips.length === 0 || selected.length === 0} onClick={() => void start()}>
            {t(action === 'ban' ? 'fail2ban.fleetBanGo' : 'fail2ban.fleetUnbanGo', { count: selected.length })}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * Лампочка «проверить адрес» (ИИ-разбор со своей инструкцией) — только
 * под хабом и только у внешних адресов; в окне ответа — «Забанить на всех
 * хостах».
 */
export function IPCheck({ ip, hubLevel, me }: { ip: string; hubLevel?: boolean; me?: Me }) {
  const { t } = useTranslation()
  const [fleet, setFleet] = useState(false)
  if (!underHub(hubLevel) || !isExternalIP(ip)) return null
  return (
    <>
      <AIExplain
        ctx={{ kind: 'ip', title: `IP ${ip}`, object: ip }}
        hint={t('fail2ban.ipCheckHint')}
        actions={
          me?.is_admin === false
            ? undefined
            : (close) => (
                <Button
                  danger
                  onClick={() => {
                    close()
                    setFleet(true)
                  }}
                >
                  {t('fail2ban.banEverywhere')}
                </Button>
              )
        }
      />
      {fleet && <FleetBanModal ips={[ip]} action="ban" onClose={() => setFleet(false)} />}
    </>
  )
}

/** Адрес и лампочка рядом. */
export function IPWithCheck({ ip, hubLevel, me }: { ip: string; hubLevel?: boolean; me?: Me }) {
  return (
    <span className="row row-nowrap" style={{ gap: '0.15rem', alignItems: 'center', display: 'inline-flex' }}>
      <span className="mono">{ip}</span>
      <IPCheck ip={ip} hubLevel={hubLevel} me={me} />
    </span>
  )
}

// --- шаблоны -----------------------------------------------------------------

/** Шаблон одним текстом — для диффа перед записью (как в истории). */
function templateDoc(t: { description?: string; jail: string; filter?: string }): string {
  return `## description: ${(t.description ?? '').trim()}\n## jail\n${t.jail.replace(/\n+$/, '')}\n## filter\n${(t.filter ?? '').replace(/\n+$/, '')}\n`
}

/** Правка своего шаблона: описание, джейл, фильтр; проверка фильтра на
 * журнале; дифф перед записью; история версий. */
export function TemplateEditModal({
  base,
  testBase,
  template,
  me,
  onClose,
  onSaved,
}: {
  base: string
  /** Где проверять фильтр: на журнале хоста, чья страница открыта. */
  testBase: string
  template: Fail2banTemplate | null
  me: Me
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const isNew = !template
  const [name, setName] = useState(template?.name ?? '')
  const [description, setDescription] = useState(template?.description ?? '')
  const [jail, setJail] = useState(template?.jail ?? '[myapp]\nenabled = true\nport = http,https\nlogpath = /var/log/myapp/access.log\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\n')
  const [filter, setFilter] = useState(template?.filter ?? (template ? '' : '[Definition]\nfailregex = ^.*login failed from <HOST>\n'))
  const [note, setNote] = useState('')
  const [testLog, setTestLog] = useState('/var/log/auth.log')
  const [test, setTest] = useState<{ matched: number; lines: number; output: string } | null>(null)
  const [preview, setPreview] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const before = template ? templateDoc(template) : ''
  const after = templateDoc({ description, jail, filter })

  async function runTest() {
    setError(null)
    setTest(null)
    try {
      setTest(await api(`${testBase}/fail2ban/regex-test`, { method: 'POST', body: { filter, log: testLog }, timeoutMs: 150_000 }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function save() {
    setBusy(true)
    setError(null)
    try {
      await api(`${base}/fail2ban/templates`, { method: 'PUT', body: { name, description, jail, filter, note } })
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPreview(null)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={isNew ? t('fail2ban.tplNew') : t('fail2ban.tplEdit', { name })} onClose={onClose} width={960} maskClosable={false} sizeKey="edit">
      <div className="col">
        <div className="row" style={{ gap: '0.6rem', flexWrap: 'wrap' }}>
          <label style={{ flex: '0 1 220px' }}>
            {t('fail2ban.tplName')}
            <Input value={name} disabled={!isNew} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="myapp" />
          </label>
          <label style={{ flex: '1 1 320px' }}>
            {t('fail2ban.tplDescription')}
            <Input value={description} onChange={(e) => setDescription(e.target.value)} />
          </label>
        </div>
        <div className="small muted">{t('fail2ban.tplJailHint')}</div>
        <CodeEditor value={jail} onChange={(e) => setJail(e.target.value)} rows={9} />
        <div className="small muted">{t('fail2ban.tplFilterHint', { name: name || 'myapp' })}</div>
        <CodeEditor value={filter} onChange={(e) => setFilter(e.target.value)} rows={5} />
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
          <span className="small">{t('fail2ban.tplTestLog')}</span>
          <Input value={testLog} onChange={(e) => setTestLog(e.target.value)} className="mono" style={{ maxWidth: 280 }} />
          <Button disabled={!filter.trim()} onClick={() => void runTest()}>
            {t('fail2ban.tplTest')}
          </Button>
          {test && (
            <span className="small">
              {test.matched >= 0 ? t('fail2ban.tplTestResult', { matched: test.matched, lines: test.lines }) : t('fail2ban.tplTestUnknown')}
            </span>
          )}
        </div>
        {test && test.matched < 0 && <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', maxHeight: 200, overflow: 'auto' }}>{test.output}</pre>}
        <label>
          {t('configs.editNote')}
          <Input value={note} onChange={(e) => setNote(e.target.value)} />
        </label>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" disabled={!name || !jail.trim()} onClick={() => setPreview(unifiedDiff(before, after, t('editModal.saved'), t('editModal.draft')))}>
            {t('common.save')}
          </Button>
          {!isNew && <Button onClick={() => setHistory(true)}>{t('configs.versionHistoryTitle')}</Button>}
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
      {preview !== null && (
        <Modal title={t('blocks.previewTitle')} onClose={() => setPreview(null)} width={900} sizeKey="diff" maskClosable={false}>
          {preview === '' ? <p className="small muted">{t('editModal.noChanges')}</p> : <DiffView text={preview} />}
          <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
            <Button type="primary" loading={busy} onClick={() => void save()}>
              {t('virt.applyChanges')}
            </Button>
            <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
          </div>
        </Modal>
      )}
      {history && template && (
        <Modal title={t('fail2ban.tplHistory', { name: template.name })} onClose={() => setHistory(false)} width={900}>
          <VersionHistory path={`nkt-f2b-tpl:${template.name}`} me={me} base={`${base}/configs`} onChanged={() => { onSaved(); onClose() }} />
        </Modal>
      )}
    </Modal>
  )
}

export interface ApplyChange {
  path: string
  before: string
  after: string
  exists: boolean
}

/** Дифф всех файлов, которые изменит запись, и «Записать». */
export function FilesDiffModal({
  changes,
  hint,
  busy,
  onWrite,
  onClose,
}: {
  changes: ApplyChange[]
  hint?: string
  busy: boolean
  onWrite: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Modal title={t('blocks.previewTitle')} onClose={onClose} width={900} sizeKey="diff" maskClosable={false}>
      {changes.map((c) => (
        <div key={c.path} style={{ marginBottom: '0.6rem' }}>
          <div className="small mono">
            {c.path} {!c.exists && <Tag color="green">{t('fail2ban.newFile')}</Tag>}
          </div>
          {c.before === c.after ? (
            <p className="small muted">{t('editModal.noChanges')}</p>
          ) : (
            <DiffView text={unifiedDiff(c.before, c.after, t('editModal.saved'), t('editModal.draft'))} />
          )}
        </div>
      ))}
      {hint && <div className="small muted">{hint}</div>}
      <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
        <Button type="primary" loading={busy} onClick={onWrite}>
          {t('virt.applyChanges')}
        </Button>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
      </div>
    </Modal>
  )
}

/** Применение шаблона к хосту: текст можно поправить, перед записью —
 * дифф всех файлов, которые изменятся. */
export function TemplateApplyModal({
  template,
  hubAddr,
  onClose,
  onApplied,
}: {
  template: Fail2banTemplate
  hubAddr?: string
  onClose: () => void
  onApplied: () => void
}) {
  const { t } = useTranslation()
  const [jail, setJail] = useState(template.jail)
  const [filter, setFilter] = useState(template.filter ?? '')
  const [changes, setChanges] = useState<ApplyChange[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const body = { name: template.name, jail, filter }

  async function preview() {
    setError(null)
    try {
      const res = await api<{ files: ApplyChange[] }>('/fail2ban/templates/apply', { method: 'POST', body: { ...body, dry_run: true } })
      setChanges(res.files)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function write() {
    setBusy(true)
    setError(null)
    try {
      await api('/fail2ban/templates/apply', { method: 'POST', body, timeoutMs: 120_000 })
      onApplied()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setChanges(null)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={t('fail2ban.tplApplyTitle', { name: template.name })} onClose={onClose} width={900} maskClosable={false} sizeKey="edit">
      <div className="col">
        <div className="small muted">{t('fail2ban.tplApplyHint')}</div>
        {hubAddr && <div className="small muted">{t('fail2ban.tplApplyHub', { addr: hubAddr })}</div>}
        <CodeEditor value={jail} onChange={(e) => setJail(e.target.value)} rows={9} />
        {(template.filter || filter) && (
          <>
            <div className="small muted">{t('fail2ban.tplFilterHint', { name: template.name })}</div>
            <CodeEditor value={filter} onChange={(e) => setFilter(e.target.value)} rows={5} />
          </>
        )}
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <Button type="primary" onClick={() => void preview()}>
            {t('fail2ban.tplApplyGo')}
          </Button>
          <Button onClick={onClose}>{t('common.cancel')}</Button>
        </div>
      </div>
      {changes && (
        <FilesDiffModal changes={changes} hint={t('fail2ban.tplApplyWriteHint')} busy={busy} onWrite={() => void write()} onClose={() => setChanges(null)} />
      )}
    </Modal>
  )
}

/**
 * Шаблоны: встроенные (с этого хоста — у них признак «программа есть») и
 * свои (с хаба). onApply задан — страница хоста, можно применить.
 */
export function TemplatesPanel({
  me,
  hubLevel,
  onApply,
}: {
  me: Me
  hubLevel?: boolean
  onApply?: (tpl: Fail2banTemplate) => void
}) {
  const { t } = useTranslation()
  const base = templatesBase(hubLevel)
  // Встроенные — с того API, куда смотрит страница (у хаба — его
  // машина); свои — с хаба.
  const local = useApi<{ builtin: Fail2banTemplate[]; custom: Fail2banTemplate[] }>(hubLevel ? null : '/fail2ban/templates')
  const hub = useApi<{ builtin: Fail2banTemplate[]; custom: Fail2banTemplate[] }>(`${base}/fail2ban/templates`)
  const [edit, setEdit] = useState<Fail2banTemplate | null | 'new'>(null)
  const [fleet, setFleet] = useState<Fail2banTemplate | null>(null)
  // У хаба стандартные — список машины хаба (текст под каждый хост хаб
  // берёт у самого хоста при проверке).
  const builtin = hubLevel ? (hub.data?.builtin ?? []) : (local.data?.builtin ?? [])
  const custom = hub.data?.custom ?? []

  async function remove(tpl: Fail2banTemplate) {
    if (!(await confirmAction(t('fail2ban.tplDeleteConfirm', { name: tpl.name })))) return
    await api(`${base}/fail2ban/templates/${encodeURIComponent(tpl.name)}`, { method: 'DELETE' })
    hub.reload()
  }

  const rows = [...builtin, ...custom]
  return (
    <>
      {me.is_admin && (
        <div className="row" style={{ marginBottom: '0.5rem' }}>
          <Button onClick={() => setEdit('new')}>{t('fail2ban.tplNew')}</Button>
          <span className="small muted">{t(underHub(hubLevel) ? 'fail2ban.tplStoredHub' : 'fail2ban.tplStoredHost')}</span>
        </div>
      )}
      {(hub.error || local.error) && <Banner kind="error">{hub.error || local.error}</Banner>}
      <div className="table-wrap">
        <DataTable<Fail2banTemplate>
          dataSource={rows}
          rowKey={(r) => `${r.builtin ? 'b' : 'c'}:${r.name}`}
          columns={[
            {
              title: t('fail2ban.tplName'),
              key: 'name',
              render: (_, r) => (
                <span className="row row-nowrap" style={{ gap: '0.3rem' }}>
                  <strong className="mono">{r.name}</strong>
                  {r.builtin ? <Tag>{t('fail2ban.tplBuiltin')}</Tag> : <Tag color="blue">{t('fail2ban.tplCustom')}</Tag>}
                </span>
              ),
            },
            {
              title: t('fail2ban.tplDescription'),
              key: 'desc',
              render: (_, r) => (
                <span className="small">{r.builtin ? t(`fail2ban.tpl.${r.name}`, { defaultValue: r.name }) : r.description}</span>
              ),
            },
            ...(hubLevel
              ? []
              : [
                  {
                    title: t('fail2ban.tplAvailable'),
                    key: 'avail',
                    render: (_: unknown, r: Fail2banTemplate) =>
                      r.available ? <Tag color="green">{t('fail2ban.tplAvailableYes')}</Tag> : <Tag>{t('fail2ban.tplAvailableNo', { service: r.service })}</Tag>,
                  },
                ]),
            {
              title: '',
              key: 'actions',
              render: (_, r) => (
                <div className="row row-nowrap">
                  {onApply && me.is_admin && (
                    <Button size="small" type={r.available ? 'primary' : 'default'} onClick={() => onApply(r)}>
                      {t('fail2ban.tplApply')}
                    </Button>
                  )}
                  {hubLevel && me.is_admin && (
                    <Button size="small" type="primary" onClick={() => setFleet(r)}>
                      {t('fail2ban.fleetTplButton')}
                    </Button>
                  )}
                  {!r.builtin && me.is_admin && (
                    <>
                      <RowAction action="edit" label={t('configs.edit')} onClick={() => setEdit(r)} />
                      <RowAction action="delete" label={t('common.delete')} danger onClick={() => void remove(r)} />
                    </>
                  )}
                </div>
              ),
            },
          ]}
        />
      </div>
      {fleet && <F2BTemplateFleetModal template={fleet} onClose={() => setFleet(null)} />}
      {edit && (
        <TemplateEditModal
          base={base}
          testBase={hubLevel ? '/hosts/local' : ''}
          template={edit === 'new' ? null : edit}
          me={me}
          onClose={() => setEdit(null)}
          onSaved={() => hub.reload()}
        />
      )}
    </>
  )
}
