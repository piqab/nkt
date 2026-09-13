import { useEffect, useMemo, useState } from 'react'
import { Button, Checkbox, ColorPicker, Input, Tag, Tooltip, type TableColumnsType } from 'antd'
import { QuestionCircleOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Job, Me, Profile, ProfilePlan, PlanChange, ProfileVersion } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, InfoHint, Loading, Modal, formatDateTime } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { confirmAction } from '../components/confirm'
import { JobLogModal } from './Jobs'

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

/**
 * Справка по формату профиля — всё, что нужно знать, чтобы написать его
 * самому.
 *
 * Отдельным окном, а не подсказкой у заголовка: подсказка отвечает
 * «что это за раздел», а здесь нужен справочник с примерами — их не
 * уместить в всплывающую строку, но и уводить за ними в репозиторий
 * незачем, писать профиль человек будет прямо на этом экране.
 *
 * Примеры YAML живут здесь, а не в переводах: это код, он одинаков на
 * любом языке, и держать две его копии значило бы однажды поправить
 * одну. Поэтому и комментариев внутри них нет — комментарий это проза,
 * и в английском интерфейсе он остался бы русским; всё, что нужно
 * сказать, сказано текстом раздела.
 */
const GUIDE: { key: string; example: string }[] = [
  { key: 'head', example: 'version: 1\nname: web-server' },
  { key: 'packages', example: 'packages:\n  - nginx\n  - btop' },
  {
    key: 'services',
    example: 'services:\n  nginx:\n    enabled: true\n    active: true\n  apache2:\n    active: false',
  },
  {
    key: 'files',
    example: 'files:\n  - path: /etc/motd\n    mode: "0644"\n    content: |\n      Managed by nkt',
  },
  {
    key: 'firewall',
    example: 'firewall:\n  allow:\n    - port: 443\n    - port: 5432\n      proto: tcp\n      from: 10.0.0.0/24',
  },
  {
    key: 'users',
    example: 'users:\n  - name: deploy\n    sudo: true\n    keys:\n      - "ssh-ed25519 AAAA... deploy@laptop"',
  },
  { key: 'system', example: 'system:\n  hostname: web-1\n  timezone: Europe/Moscow' },
  {
    key: 'compose',
    example:
      'compose:\n  - name: shop\n    up: true\n    content: |\n      services:\n        web:\n          image: nginx:1.27\n          ports: ["8080:80"]',
  },
]

function ProfileGuide({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  return (
    <Modal title={t('profiles.guide.title')} onClose={onClose} width={860}>
      <div className="col" style={{ gap: '1rem', maxHeight: '70vh', overflowY: 'auto' }}>
        <div className="col" style={{ gap: '0.4rem' }}>
          <p style={{ margin: 0 }}>{t('profiles.guide.intro')}</p>
          <p style={{ margin: 0 }}>{t('profiles.guide.additive')}</p>
          <p style={{ margin: 0 }}>{t('profiles.guide.flow')}</p>
        </div>

        {GUIDE.map((s) => (
          <div key={s.key} className="col" style={{ gap: '0.3rem' }}>
            <strong>{t(`profiles.guide.section.${s.key}.title`)}</strong>
            <span className="small">{t(`profiles.guide.section.${s.key}.body`)}</span>
            <pre className="diff" style={{ margin: 0 }}>
              {s.example}
            </pre>
          </div>
        ))}

        <div className="col" style={{ gap: '0.3rem' }}>
          <strong>{t('profiles.guide.limits.title')}</strong>
          <ul className="small" style={{ margin: 0, paddingLeft: '1.1rem' }}>
            <li>{t('profiles.guide.limits.packages')}</li>
            <li>{t('profiles.guide.limits.files')}</li>
            <li>{t('profiles.guide.limits.order')}</li>
            <li>{t('profiles.guide.limits.volumes')}</li>
          </ul>
        </div>
      </div>
    </Modal>
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
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [guideOpen, setGuideOpen] = useState(false)
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
    }
  }, [current.data])

  const profiles = list.data?.profiles ?? []

  async function save() {
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
      setNotice({ kind: 'info', text: t('profiles.saved') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
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

  return (
    <>
      <div className="page-head spread">
        <h1>
          {t('profiles.title')}
          <InfoHint>{t(hubLevel ? 'profiles.hubHint' : 'profiles.hint')}</InfoHint>
          {/* Подсказка у заголовка отвечает «что это за раздел», а писать
              профиль приходится здесь же — за справочником с примерами
              уходить в репозиторий незачем. */}
          <Tooltip title={t('profiles.guide.open')}>
            <Button
              type="text"
              size="small"
              aria-label={t('profiles.guide.open')}
              icon={<QuestionCircleOutlined />}
              onClick={() => setGuideOpen(true)}
            />
          </Tooltip>
        </h1>
        {canEdit && (
          <Button
            onClick={() => {
              setSelected(null)
              setDraft(TEMPLATE)
              setPlan(null)
              setNote('')
              // Новому профилю — следующий свободный цвет палитры.
              setColor(PROFILE_COLORS.find((c) => !profiles.some((p) => p.color === c)) ?? PROFILE_COLORS[0])
            }}
          >
            {t('profiles.newProfile')}
          </Button>
        )}
      </div>

      {guideOpen && <ProfileGuide onClose={() => setGuideOpen(false)} />}

      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {notice.text}
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
            title={current.data?.name ?? t('profiles.newProfile')}
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
                  <Button size="small" loading={busy} onClick={() => void save()}>
                    {t('common.save')}
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
            {canEdit && (
              <div className="filters" style={{ marginBottom: '0.5rem', alignItems: 'flex-end' }}>
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
              </div>
            )}
            <CodeEditor value={draft} onChange={(e) => setDraft(e.target.value)} rows={18} readOnly={!canEdit} />
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
            {detail.detail}
          </pre>
        </Modal>
      )}

      {showVersions && selected && (
        <VersionsModal profileID={selected} onClose={() => setShowVersions(false)} onRestore={(content) => {
          setDraft(content)
          setShowVersions(false)
        }} />
      )}

      {/* Образы и шаблоны машин — такие же заготовки, как профиль, и
          живут здесь же. */}

      {openJob && <JobLogModal job={openJob} onClose={() => setOpenJob(null)} />}
    </>
  )
}

/** История правок профиля: посмотреть и вернуть прошлую редакцию. */
function VersionsModal({
  profileID,
  onClose,
  onRestore,
}: {
  profileID: number
  onClose: () => void
  onRestore: (content: string) => void
}) {
  const { t } = useTranslation()
  const versions = useApi<{ versions: ProfileVersion[] }>(`/profiles/${profileID}/versions`)

  return (
    <Modal title={t('profiles.history')} onClose={onClose} width={720}>
      {versions.loading && !versions.data ? (
        <Loading what={t('profiles.history')} />
      ) : (
        <div className="col" style={{ gap: '0.35rem' }}>
          {(versions.data?.versions ?? []).map((v) => (
            <div key={v.id} className="row spread">
              <span className="small">
                {formatDateTime(v.ts)} · {v.author || '—'}
                {v.note ? ` · ${v.note}` : ''}
              </span>
              <Button
                size="small"
                onClick={async () => {
                  const full = await api<ProfileVersion>(`/profiles/versions/${v.id}`)
                  onRestore(full.content ?? '')
                }}
              >
                {t('profiles.restoreVersion')}
              </Button>
            </div>
          ))}
        </div>
      )}
    </Modal>
  )
}
