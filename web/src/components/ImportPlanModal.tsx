import { useEffect, useState } from 'react'
import { Button, Segmented, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { Banner, Loading, Modal } from './ui'

interface PlanItem {
  name: string
  conflict: boolean
  replaceable: boolean
}

interface PlanSection {
  section: string
  items: PlanItem[]
}

interface Plan {
  version: number
  exported_at: string
  has_key: boolean
  sections: PlanSection[]
}

interface SectionCount {
  added: number
  replaced: number
  skipped: number
}

export interface ImportReport {
  sections?: Record<string, SectionCount>
  errors: string[]
}

type Resolutions = Record<string, Record<string, 'skip' | 'replace'>>

async function post<T>(url: string, body: string): Promise<T> {
  const res = await fetch(url, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body })
  const payload = await res.json().catch(() => null)
  if (!res.ok) throw new Error(payload?.error ?? `HTTP ${res.status}`)
  return payload as T
}

/**
 * Импорт файла хаба: сначала план — что в файле по разделам и что из
 * этого уже есть в хабе (по имени). По каждому совпадению — «пропустить»
 * (по умолчанию) или «заменить»; затем импорт и отчёт по разделам.
 * Файл уже расшифрован в браузере (если был зашифрован паролем).
 */
export function ImportPlanModal({ jsonText, onClose, onDone }: { jsonText: string; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation()
  const [plan, setPlan] = useState<Plan | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [res, setRes] = useState<Resolutions>({})
  const [busy, setBusy] = useState(false)
  const [report, setReport] = useState<ImportReport | null>(null)

  useEffect(() => {
    post<Plan>('/api/hub/import/plan', jsonText)
      .then(setPlan)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
  }, [jsonText])

  const choice = (sec: string, name: string) => res[sec]?.[name] ?? 'skip'
  const setChoice = (sec: string, name: string, v: 'skip' | 'replace') =>
    setRes((r) => ({ ...r, [sec]: { ...(r[sec] ?? {}), [name]: v } }))
  const setAll = (s: PlanSection, v: 'skip' | 'replace') =>
    setRes((r) => ({
      ...r,
      [s.section]: Object.fromEntries(s.items.filter((i) => i.conflict && i.replaceable).map((i) => [i.name, v])),
    }))

  async function run() {
    setBusy(true)
    setError(null)
    try {
      const replace: Record<string, Record<string, string>> = {}
      for (const [sec, items] of Object.entries(res)) {
        for (const [name, v] of Object.entries(items)) {
          if (v === 'replace') replace[sec] = { ...(replace[sec] ?? {}), [name]: 'replace' }
        }
      }
      const body = `{"export":${jsonText},"resolutions":${JSON.stringify(replace)}}`
      const out = await post<{ report: ImportReport }>('/api/hub/import', body)
      setReport(out.report)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const sectionName = (s: string) => t(`hosts.importSection.${s}`, { defaultValue: s })

  if (report) {
    const rows = Object.entries(report.sections ?? {})
    return (
      <Modal title={t('hosts.importReportTitle')} onClose={onClose} width={680}>
        <table className="ant-table" style={{ width: 'auto', borderCollapse: 'collapse', marginBottom: '0.6rem' }}>
          <thead>
            <tr>
              <th style={{ textAlign: 'left', padding: '0.2rem 0.8rem 0.2rem 0' }}>{t('hosts.importColSection')}</th>
              <th style={{ padding: '0.2rem 0.6rem' }}>{t('hosts.importColAdded')}</th>
              <th style={{ padding: '0.2rem 0.6rem' }}>{t('hosts.importColReplaced')}</th>
              <th style={{ padding: '0.2rem 0.6rem' }}>{t('hosts.importColSkipped')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(([sec, c]) => (
              <tr key={sec}>
                <td style={{ padding: '0.15rem 0.8rem 0.15rem 0' }}>{sectionName(sec)}</td>
                <td className="num" style={{ textAlign: 'center' }}>{c.added}</td>
                <td className="num" style={{ textAlign: 'center' }}>{c.replaced}</td>
                <td className="num" style={{ textAlign: 'center' }}>{c.skipped}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {report.errors.length > 0 ? (
          <Banner kind="error">
            <div>{t('hosts.importErrorsTitle', { count: report.errors.length })}</div>
            <ul style={{ margin: '0.3rem 0 0', paddingLeft: '1.2rem' }}>
              {report.errors.map((e, i) => (
                <li key={i} className="small">{e}</li>
              ))}
            </ul>
          </Banner>
        ) : (
          <Banner kind="info">{t('hosts.importNoErrors')}</Banner>
        )}
        <div className="row" style={{ marginTop: '0.6rem' }}>
          <Button type="primary" onClick={onClose}>{t('common.close')}</Button>
        </div>
      </Modal>
    )
  }

  return (
    <Modal title={t('hosts.importPlanTitle')} onClose={onClose} width={820} maskClosable={false}>
      {error && <Banner kind="error">{error}</Banner>}
      {!plan ? (
        !error && <Loading />
      ) : (
        <div className="col">
          <div className="small muted">
            {t('hosts.importPlanHint', { version: plan.version, date: plan.exported_at?.slice(0, 10) ?? '' })}
            {plan.has_key && <> {t('hosts.importPlanHasKey')}</>}
          </div>
          {plan.sections.filter((s) => s.items.length > 0).map((s) => {
            const conflicts = s.items.filter((i) => i.conflict)
            return (
              <div key={s.section} style={{ borderTop: '1px solid var(--border)', paddingTop: '0.4rem' }}>
                <div className="row" style={{ gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                  <strong>{sectionName(s.section)}</strong>
                  <span className="small muted">
                    {t('hosts.importSectionCounts', { total: s.items.length, conflicts: conflicts.length })}
                  </span>
                  {conflicts.some((i) => i.replaceable) && (
                    <>
                      <Button size="small" type="link" onClick={() => setAll(s, 'skip')}>{t('hosts.importAllSkip')}</Button>
                      <Button size="small" type="link" onClick={() => setAll(s, 'replace')}>{s.section === 'monitoring' ? t('hosts.importAllMerge') : t('hosts.importAllReplace')}</Button>
                    </>
                  )}
                </div>
                {conflicts.length > 0 && (
                  <div className="col" style={{ gap: '0.2rem', marginTop: '0.3rem' }}>
                    {conflicts.map((i) => (
                      <div key={i.name} className="row" style={{ gap: '0.6rem', alignItems: 'center' }}>
                        <span className="mono small" style={{ minWidth: 220 }}>{i.name}</span>
                        {i.replaceable ? (
                          <Segmented
                            size="small"
                            value={choice(s.section, i.name)}
                            onChange={(v) => setChoice(s.section, i.name, v as 'skip' | 'replace')}
                            options={[
                              { value: 'skip', label: t('hosts.importSkip') },
                              { value: 'replace', label: s.section === 'monitoring' ? t('hosts.importMerge') : t('hosts.importReplace') },
                            ]}
                          />
                        ) : (
                          <Tag>{t('hosts.importSkipOnly')}</Tag>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
          <div className="small muted">{t('hosts.importReplaceHint')}</div>
          <div className="row">
            <Button type="primary" loading={busy} onClick={() => void run()}>
              {t('hosts.importGo')}
            </Button>
            <Button onClick={onClose}>{t('common.cancel')}</Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
