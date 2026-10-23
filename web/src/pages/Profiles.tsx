import { useEffect, useMemo, useState } from 'react'
import { blurText } from '../privacy'
import { Button, Checkbox, ColorPicker, Dropdown, Input, Tabs, Tag, type TableColumnsType } from 'antd'
import { DownOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { TextVersions } from '../components/TextVersions'
import { api, useApi } from '../api'
import type { Job, Me, Profile, ProfilePlan, PlanChange } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { EditTextModal } from '../components/EditTextModal'
import { confirmAction } from '../components/confirm'
import { JobLogModal } from './Jobs'
import { HelpButton, SiteHelpTab, TitleHelp } from '../components/Docs'
import { msg, tx, type Msg } from '../msg'

/** Заготовка для нового профиля: показывает форму, а не пустой экран. */
const TEMPLATE = `version: 1
name: новый-профиль
packages:
  - btop
services:
  ssh:
    enabled: true
    active: true
# files:
#   - path: /etc/motd
#     content: |
#       Привет
# firewall:
#   allow:
#     - port: 443
# users:
#   - name: deploy
#     sudo: true
#     keys: ["ssh-ed25519 AAAA... deploy@laptop"]
# system:
#   timezone: Europe/Moscow
# compose:
#   - name: shop
#     up: true
#     content: |
#       services:
#         web:
#           image: nginx:1.27
#           ports: ["8080:80"]
`

interface ProfileHelpData {
  intro: string[]
  sections: { key: string; title: string; body: string; example: string }[]
  limits: { title: string; items: string[] }
  examples: { key: string; title: string; summary: string; content: string }[]
}

/** Итог «Проверить»: что профиль задаёт или ошибка со строкой. */
interface ProfileCheck {
  ok: boolean
  name?: string
  error?: string
  line?: number
  counts?: Record<string, number>
}

const COUNT_ORDER = ['packages', 'services', 'files', 'firewall', 'users', 'system', 'compose']

function checkSummary(c: ProfileCheck): string {
  return COUNT_ORDER.filter((k) => (c.counts?.[k] ?? 0) > 0)
    .map((k) => `${k}: ${c.counts![k]}`)
    .join(', ')
}

function CheckLine({ check }: { check: ProfileCheck }) {
  const { t } = useTranslation()
  if (check.ok) return <Banner kind="success">{t('profiles.checkOK', { name: check.name, summary: checkSummary(check) || t('profiles.checkEmpty') })}</Banner>
  return <Banner kind="error">{check.line ? t('profiles.checkErrorLine', { line: check.line, error: check.error }) : check.error}</Banner>
}

/**
 * Справка формата профиля, встроенная в nkt («Из nkt» на вкладке
 * «Справка»): те же тексты и примеры, что страница сайта «Формат
 * профиля», — с сервера, из одного описания. Готовые профили
 * вставляются в редактор.
 */
function ProfileHelp({ canInsert, onInsert }: { canInsert: boolean; onInsert: (text: string) => void }) {
  const { t } = useTranslation()
  const help = useApi<ProfileHelpData>('/profiles/help')
  if (help.loading && !help.data) return <Loading what={t('profiles.tabHelp')} />
  if (help.error) return <Banner kind="error">{help.error}</Banner>
  const d = help.data
  if (!d) return null
  return (
    <div className="col" style={{ gap: '1rem' }}>
      <div className="col" style={{ gap: '0.4rem' }}>
        {d.intro.map((p, i) => (
          <p key={i} className="small" style={{ margin: 0 }}>
            {p}
          </p>
        ))}
      </div>
      {d.sections.map((s) => (
        <div key={s.key} className="col" style={{ gap: '0.3rem' }}>
          <strong>{s.title}</strong>
          <span className="small">{s.body}</span>
          <pre className="diff" style={{ margin: 0 }}>
            {s.example}
          </pre>
        </div>
      ))}
      <div className="col" style={{ gap: '0.3rem' }}>
        <strong>{d.limits.title}</strong>
        <ul className="small" style={{ margin: 0, paddingLeft: '1.1rem' }}>
          {d.limits.items.map((it, i) => (
            <li key={i}>{it}</li>
          ))}
        </ul>
      </div>
      <div className="col" style={{ gap: '0.5rem' }}>
        <strong>{t('profiles.ready')}</strong>
        {d.examples.map((e) => (
          <div key={e.key} className="col" style={{ gap: '0.3rem' }}>
            <div className="row spread" style={{ alignItems: 'center' }}>
              <span>
                <strong className="small">{e.title}</strong> <span className="small muted">— {e.summary}</span>
              </span>
              {canInsert && (
                <Button
                  size="small"
                  onClick={() =>
                    void (async () => {
                      if (await confirmAction(t('profiles.confirmInsert'), { okText: t('profiles.insert'), danger: false })) onInsert(e.content)
                    })()
                  }
                >
                  {t('profiles.insert')}
                </Button>
              )}
            </div>
            <pre className="diff" style={{ margin: 0 }}>
              {e.content}
            </pre>
          </div>
        ))}
      </div>
    </div>
  )
}

/** Палитра цветов профиля: приглушённые, чтобы фон строки не спорил с
 * текстом и иконками. Свой цвет тоже можно — через пипетку. */
const PROFILE_COLORS = ['#4f86c6', '#5aa66f', '#c9a227', '#d0743c', '#b95c8a', '#7c6bc4', '#3fa5a5', '#8a8a8a']

export default function Profiles({ me, hubLevel = false }: { me: Me; hubLevel?: boolean }) {
  const { t } = useTranslation()
  const canEdit = me.is_admin && me.allow_mutations
  const list = useApi<{ profiles: Profile[] }>('/profiles', 60_000)
  const [selected, setSelected] = useState<number | null>(null)
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [color, setColor] = useState('')
  const [plan, setPlan] = useState<ProfilePlan | null>(null)
  const [chosen, setChosen] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: Msg } | null>(null)
  const [check, setCheck] = useState<ProfileCheck | null>(null)
  const [tab, setTab] = useState<'text' | 'help'>('text')
  const [openJob, setOpenJob] = useState<Job | null>(null)
  const [detail, setDetail] = useState<PlanChange | null>(null)
  const [showVersions, setShowVersions] = useState(false)

  const current = useApi<Profile>(selected ? `/profiles/${selected}` : null)

  useEffect(() => {
    if (current.data) {
      setDraft(current.data.content ?? '')
      setColor(current.data.color ?? '')
      setNote('')
      setPlan(null)
      setCheck(null)
    }
  }, [current.data])

  const profiles = list.data?.profiles ?? []

  const [editing, setEditing] = useState(false)

  async function save(): Promise<boolean> {
    setBusy(true)
    setNotice(null)
    try {
      if (selected) {
        await api(`/profiles/${selected}`, { method: 'PUT', body: { content: draft, note, color } })
      } else {
        const res = await api<{ id: number }>('/profiles', { method: 'POST', body: { content: draft, note, color } })
        setSelected(res.id)
      }
      await list.reload()
      await current.reload()
      setNotice({ kind: 'info', text: tx('profiles.saved') })
      return true
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  // План строится по тому, что сейчас в редакторе, а не по сохранённому:
  // так видно последствия правки до её записи.
  async function buildPlan() {
    setBusy(true)
    setNotice(null)
    try {
      const res = await api<ProfilePlan>('/profiles/plan', { method: 'POST', body: { content: draft } })
      // changes может прийти null от хоста со старой версией: пустой
      // Go-срез уезжает в JSON именно так, и «.length» тогда роняет всю
      // страницу. Своя сторона это уже не присылает, но верить чужой
      // версии на слово незачем.
      setPlan({ ...res, changes: res.changes ?? [], unknown: res.unknown ?? [] })
      setChosen(new Set((res.changes ?? []).map((_, i) => i)))
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function apply() {
    if (!plan || !selected) return
    const changes = plan.changes.filter((_, i) => chosen.has(i))
    if (changes.length === 0) return
    const risky = changes.filter((c) => c.risk)
    const question = risky.length
      ? t('profiles.confirmApplyRisky', {
          count: changes.length,
          risks: risky.map((c) => t(`profiles.risk.${c.risk}`, { defaultValue: c.risk })).join('; '),
        })
      : t('profiles.confirmApply', { count: changes.length })
    if (!(await confirmAction(question, { danger: risky.length > 0 }))) return

    setBusy(true)
    try {
      const res = await api<{ job_id: number }>(`/profiles/${selected}/apply`, {
        method: 'POST',
        body: { changes },
      })
      const job = await api<Job>(`/jobs/${res.job_id}`)
      setOpenJob(job)
      setPlan(null)
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  const planColumns: TableColumnsType<PlanChange> = useMemo(
    () => [
      {
        title: '',
        key: 'pick',
        width: 40,
        render: (_, __, index) => (
          <Checkbox
            checked={chosen.has(index)}
            disabled={!canEdit}
            onChange={(e) =>
              setChosen((prev) => {
                const next = new Set(prev)
                if (e.target.checked) next.add(index)
                else next.delete(index)
                return next
              })
            }
          />
        ),
      },
      {
        title: t('profiles.colAction'),
        key: 'action',
        render: (_, c) => (
          <div style={{ minWidth: '12rem' }}>
            <span className="mono small">{t(`profiles.action.${c.action}`, { defaultValue: c.action })}</span>
            <div className="small muted mono">{c.target}</div>
          </div>
        ),
      },
      {
        title: t('profiles.colCurrent'),
        key: 'current',
        // Состояния приходят кодами; всё, что кодом не оказалось (имя
        // машины, например), показывается как есть — это само значение.
        render: (_, c) => <span className="small">{t(`profiles.state.${c.current}`, { defaultValue: c.current })}</span>,
      },
      {
        title: t('profiles.colDesired'),
        key: 'desired',
        render: (_, c) => (
          <span className="small">
            {t(`profiles.state.${c.desired}`, { defaultValue: c.desired })}
            {c.detail && (
              <RowAction action="details" label={t('profiles.showDetail')} onClick={() => setDetail(c)} />
            )}
          </span>
        ),
      },
      {
        title: '',
        key: 'risk',
        render: (_, c) =>
          c.risk ? <Tag color="warning">{t(`profiles.risk.${c.risk}`, { defaultValue: c.risk })}</Tag> : null,
      },
    ],
    [chosen, canEdit, t],
  )

  // «?» у профиля и в окне правки — страница «Формат профиля» на сайте
  // справки; подсказка у заголовка раздела — о разделе.
  const guideButton = <HelpButton docKey="profiles:format" isHub={hubLevel} admin={me.is_admin} version={me.hub_version} compact />
  const helpData = useApi<ProfileHelpData>('/profiles/help')

  async function runCheck(text = draft) {
    setBusy(true)
    try {
      setCheck(await api<ProfileCheck>('/profiles/check', { method: 'POST', body: { content: text } }))
    } catch (err) {
      setCheck({ ok: false, error: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function pickExample(content: string) {
    const pristine = draft.trim() === '' || draft === TEMPLATE || (selected !== null && draft === (current.data?.content ?? ''))
    if (!pristine && !(await confirmAction(t('profiles.confirmInsert'), { okText: t('profiles.insert'), danger: false }))) return
    setDraft(content)
    setCheck(null)
  }


  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('profiles.title')}
          <TitleHelp>{t(hubLevel ? 'profiles.hubHint' : 'profiles.hint')}</TitleHelp>
        </h1>
        {canEdit && (
          <Button
            onClick={() => {
              setSelected(null)
              setDraft(TEMPLATE)
              setCheck(null)
              setPlan(null)
              setNote('')
              // Новому профилю — следующий свободный цвет палитры.
              setColor(PROFILE_COLORS.find((c) => !profiles.some((p) => p.color === c)) ?? PROFILE_COLORS[0])
              setEditing(true)
            }}
          >
            {t('profiles.newProfile')}
          </Button>
        )}
      </div>


      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {msg(notice.text)}
        </Banner>
      )}
      <ErrorNote error={list.error} />

      {/* Список слева, редактор справа — как в «Конфигурациях»: раздел
          устроен так же, и разная раскладка сбивала бы с толку. */}
      <div className="grid" style={{ gridTemplateColumns: 'minmax(240px, 320px) 1fr' }}>
        <Card title={t('profiles.listTitle')} subtitle={t('profiles.listSubtitle', { count: profiles.length })}>
          {list.loading && !list.data ? (
            <Loading what={t('profiles.loading')} />
          ) : profiles.length === 0 ? (
            <p className="small muted">{t('profiles.empty')}</p>
          ) : (
            <div className="col" style={{ gap: '0.25rem' }}>
              {profiles.map((p) => (
                <Button
                  key={p.id}
                  type={p.id === selected ? 'default' : 'text'}
                  style={{ textAlign: 'left', height: 'auto', padding: '0.35rem 0.5rem' }}
                  onClick={() => setSelected(p.id)}
                >
                  <span className="row" style={{ gap: '0.5rem', alignItems: 'center' }}>
                    <span className="profile-swatch" style={{ background: p.color || 'transparent' }} />
                    <span>
                      <strong>{p.name}</strong>
                      <div className="small muted">{t('profiles.updated', { when: formatDateTime(p.updated_at) })}</div>
                    </span>
                  </span>
                </Button>
              ))}
            </div>
          )}
        </Card>

        <div className="col">
          <Card
            title={
              <span>
                {current.data?.name ?? t('profiles.newProfile')} {guideButton}
              </span>
            }
            actions={
              <>
                {selected && (
                  <Button size="small" onClick={() => setShowVersions(true)}>
                    {t('profiles.history')}
                  </Button>
                )}
                {selected && (
                  <Button size="small" href={`/api/profiles/${selected}/export`} target="_blank">
                    {t('profiles.export')}
                  </Button>
                )}
                {canEdit && (
                  <Button size="small" type="primary" onClick={() => setEditing(true)}>
                    {t('configs.edit')}
                  </Button>
                )}
                {/* На хабе профиль применяется к хостам через группы, а
                    не к машине хаба — план здесь не строится. */}
                {!hubLevel && (
                  <Button size="small" type="primary" loading={busy} onClick={() => void buildPlan()}>
                    {t('profiles.buildPlan')}
                  </Button>
                )}
                {selected && canEdit && (
                  <Button
                    size="small"
                    danger
                    onClick={async () => {
                      if (!(await confirmAction(t('profiles.confirmDelete', { name: current.data?.name })))) return
                      await api(`/profiles/${selected}`, { method: 'DELETE' })
                      setSelected(null)
                      setDraft('')
                      await list.reload()
                    }}
                  >
                    {t('common.delete')}
                  </Button>
                )}
              </>
            }
          >
            {/* Просмотр; правка — в окне с диффом перед записью. */}
            <Tabs
              activeKey={tab}
              onChange={(k) => setTab(k as typeof tab)}
              items={[
                { key: 'text', label: t('profiles.tabText'), children: <CodeEditor value={draft} onChange={() => undefined} rows={18} readOnly /> },
                {
                  key: 'help',
                  label: t('profiles.tabHelp'),
                  children: (
                    <SiteHelpTab
                      docKey="profiles:format"
                      isHub={hubLevel}
                      storageKey="nkt-profile-help-source"
                      siteHint={t('profiles.siteHint')}
                      nktHint={t('profiles.nktHint')}
                      fallback={
                        <ProfileHelp
                          canInsert={canEdit}
                          onInsert={(text) => {
                            setDraft(text)
                            setCheck(null)
                            setTab('text')
                            setEditing(true)
                          }}
                        />
                      }
                    />
                  ),
                },
              ]}
            />
          </Card>

          {plan && (
            <Card
              title={t('profiles.planTitle')}
              subtitle={
                plan.changes.length === 0
                  ? t('profiles.planClean')
                  : t('profiles.planCount', { count: plan.changes.length })
              }
              actions={
                plan.changes.length > 0 &&
                canEdit && (
                  <Button type="primary" size="small" loading={busy} disabled={chosen.size === 0 || !selected} onClick={() => void apply()}>
                    {t('profiles.apply', { count: chosen.size })}
                  </Button>
                )
              }
            >
              {plan.unknown && plan.unknown.length > 0 && (
                <Banner kind="warn">
                  {t('profiles.unknownIntro')}
                  <ul style={{ margin: '0.3rem 0 0 1rem' }}>
                    {plan.unknown.map((u, i) => (
                      <li key={i} className="small">
                        {u}
                      </li>
                    ))}
                  </ul>
                </Banner>
              )}
              {plan.changes.length === 0 ? (
                <p className="small muted">{t('profiles.planClean')}</p>
              ) : (
                <>
                  {!selected && <Banner kind="warn">{t('profiles.saveBeforeApply')}</Banner>}
                  <div className="table-wrap">
                    <DataTable<PlanChange>
                      dataSource={plan.changes}
                      columns={planColumns}
                      rowKey={(_, i) => i ?? 0}
                      tableLayout="auto"
                    />
                  </div>
                </>
              )}
            </Card>
          )}
        </div>
      </div>

      {detail && (
        <Modal title={detail.target} onClose={() => setDetail(null)} width={760}>
          <pre className="diff mono" style={{ maxHeight: '24rem', overflow: 'auto', whiteSpace: 'pre-wrap' }}>
            {blurText(detail.detail)}
          </pre>
        </Modal>
      )}

      {showVersions && selected && (
        <VersionsModal profileID={selected} canRestore={canEdit} onClose={() => setShowVersions(false)} onRestore={(content) => {
          setDraft(content)
          setShowVersions(false)
        }} />
      )}

      {/* Образы и шаблоны машин — такие же заготовки, как профиль, и
          живут здесь же. */}

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
      {editing && (
        <EditTextModal
          title={
            <span>
              {current.data?.name ?? t('profiles.newProfile')} {guideButton}
            </span>
          }
          saved={selected ? current.data?.content ?? '' : ''}
          draft={draft}
          onDraft={(v) => {
            setDraft(v)
            setCheck(null)
          }}
          busy={busy}
          saveBlocked={!check ? t('profiles.needCheck') : !check.ok ? t('profiles.needFix') : undefined}
          below={check && <div style={{ marginTop: '0.5rem' }}><CheckLine check={check} /></div>}
          fields={(<div className="filters" style={{ marginBottom: '0.5rem', alignItems: 'flex-end' }}>
                <Button loading={busy} onClick={() => void runCheck()}>
                  {t('profiles.check')}
                </Button>
                <label style={{ flex: 1, minWidth: '14rem' }}>
                  {t('profiles.note')}
                  <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('profiles.notePlaceholder')} />
                </label>
                {/* Цвет профиля: им подкрашиваются строки хостов, созданных
                    по этому профилю, — чтобы в списке хаба было видно,
                    из чего машина сделана. */}
                <label>
                  {t('profiles.color')}
                  <span className="row" style={{ gap: '0.3rem', alignItems: 'center' }}>
                    <ColorPicker
                      size="small"
                      value={color || null}
                      allowClear
                      presets={[{ label: t('profiles.colorPresets'), colors: PROFILE_COLORS }]}
                      onChange={(c) => setColor(c ? c.toHexString().slice(0, 7) : '')}
                      onClear={() => setColor('')}
                    />
                    <span className="small muted mono">{color || t('profiles.colorNone')}</span>
                  </span>
                </label>
                <label>
                  {t('profiles.readyStart')}
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: (helpData.data?.examples ?? []).map((e) => ({ key: e.key, label: e.title })),
                      onClick: ({ key }) => {
                        const e = helpData.data?.examples.find((x) => x.key === key)
                        if (e) void pickExample(e.content)
                      },
                    }}
                  >
                    <Button>
                      {t('profiles.ready')} <DownOutlined />
                    </Button>
                  </Dropdown>
                </label>
              </div>)}
          onSave={save}
          onHistory={selected ? () => setShowVersions(true) : undefined}
          onClose={() => {
            setEditing(false)
            if (selected && current.data) setDraft(current.data.content ?? '')
          }}
          rows={20}
        />
      )}
    </>
  )
}

/** История правок профиля: посмотреть и вернуть прошлую редакцию. */
function VersionsModal({
  profileID,
  canRestore,
  onClose,
  onRestore,
}: {
  profileID: number
  canRestore: boolean
  onClose: () => void
  onRestore: (content: string) => void
}) {
  const { t } = useTranslation()

  return (
    <Modal title={t('profiles.history')} onClose={onClose} width={960} sizeKey="versions">
      <TextVersions listPath={`/profiles/${profileID}/versions`} itemPath={(id) => `/profiles/versions/${id}`} canRestore={canRestore} onRestore={onRestore} />
    </Modal>
  )
}
