import { useMemo, useState } from 'react'
import { Button, Checkbox, Spin, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, ErrorNote, Modal } from './ui'

/**
 * Окно шагов создания кластера (internal/hub/cluster_steps.go): конвейер
 * как явный чек-лист — оператор видит очередь действий и правит её.
 *
 * Два входа:
 *  - второй шаг формы «Новый кластер»: все шаги отмечены, снятие — «не
 *    делать» (остальное можно доделать потом через «продолжить»);
 *  - «продолжить» на упавшем кластере и «попробовать снова» в журнале
 *    задания: выполненные шаги сняты, отмеченный заново выполненный шаг
 *    исполняется принудительно (узел сбрасывается) — окно об этом
 *    предупреждает.
 *
 * Конфликт зависимостей подсвечивается сразу: отмеченный шаг, чья
 * зависимость не отмечена и не выполнена, красится, а кнопка пуска гаснет.
 * Хаб проверяет то же самое на приёме (окно — не единственный вход).
 */

export interface ClusterStep {
  /** Идентификатор шага: «preflight», «vm:k8s-cp-1», «join:k8s-cp-2», … */
  id: string
  kind: 'preflight' | 'mesh' | 'vm' | 'listener' | 'cp1' | 'join' | 'ready' | 'addons'
  /** Имя узла для по-узловых шагов. */
  node?: string
  /** Шаг уже выполнен по состоянию кластера — в окне повтора он снят. */
  done: boolean
  /** Жёсткие зависимости: без них шаг не имеет смысла. */
  deps?: string[]
  /** Повтор выполненного шага разрушителен: узел сбрасывается. */
  force?: boolean
}

export type StepSelection = Record<string, boolean>

/** Конфликтующие отмеченные шаги — зеркало validateClusterSteps на хабе. */
export function stepConflicts(steps: ClusterStep[], sel: StepSelection): Set<string> {
  const byID = new Map(steps.map((s) => [s.id, s]))
  const out = new Set<string>()
  for (const st of steps) {
    if (!sel[st.id]) continue
    for (const dep of st.deps ?? []) {
      if (sel[dep] || byID.get(dep)?.done) continue
      out.add(st.id)
      break
    }
  }
  return out
}

/** Подпись шага: тип + имя узла. */
export function stepText(t: (k: string, o?: Record<string, unknown>) => string, st: ClusterStep): string {
  return t(`clusters.steps.kind.${st.kind}`, { node: st.node ?? '' })
}

export function StepsChecklist({ steps, sel, onToggle }: { steps: ClusterStep[]; sel: StepSelection; onToggle: (id: string, v: boolean) => void }) {
  const { t } = useTranslation()
  const conflicts = useMemo(() => stepConflicts(steps, sel), [steps, sel])
  return (
    <div className="col" style={{ gap: '0.25rem' }}>
      {steps.map((st) => {
        const bad = conflicts.has(st.id)
        return (
          <label
            key={st.id}
            className="row"
            style={{
              gap: '0.5rem',
              alignItems: 'center',
              flexWrap: 'wrap',
              margin: 0,
              padding: '0.3rem 0.45rem',
              borderRadius: 6,
              border: bad ? '1px solid var(--series-8, #d4380d)' : '1px solid transparent',
              background: bad ? 'color-mix(in srgb, var(--series-8, #d4380d) 8%, transparent)' : undefined,
            }}
          >
            <Checkbox checked={!!sel[st.id]} onChange={(e) => onToggle(st.id, e.target.checked)} />
            <span className="mono" style={bad ? { color: 'var(--series-8, #d4380d)' } : undefined}>
              {stepText(t, st)}
            </span>
            {st.done && <Tag color="success">{t('clusters.steps.doneTag')}</Tag>}
            {st.done && sel[st.id] && st.force && (
              <Tooltip title={t('clusters.steps.forceHint')}>
                <Tag color="warning">{t('clusters.steps.forceTag')}</Tag>
              </Tooltip>
            )}
            {bad && <span className="small" style={{ color: 'var(--series-8, #d4380d)' }}>{t('clusters.steps.conflictRow')}</span>}
          </label>
        )
      })}
      {conflicts.size > 0 && <Banner kind="error">{t('clusters.steps.conflictBanner')}</Banner>}
    </div>
  )
}

/** Выбор без конфликтов и непустой — иначе пускать нельзя. */
export function stepsChosen(steps: ClusterStep[], sel: StepSelection): string[] {
  return steps.filter((s) => sel[s.id]).map((s) => s.id)
}

/**
 * Окно «продолжить»: чек-лист последнего задания кластера с выполненными
 * шагами снятыми. Пуск — POST /hub/clusters/{id}/retry с явным выбором;
 * новое задание открывается журналом там, откуда позвали.
 */
export function ClusterRetryModal({
  clusterID,
  title,
  onClose,
  onStarted,
}: {
  clusterID: number
  title: string
  onClose: () => void
  /** job_id нового задания — вызывающий открывает его журнал. */
  onStarted: (jobID: number) => void
}) {
  const { t } = useTranslation()
  const data = useApi<{ job_id: number; steps: ClusterStep[]; can_retry: boolean }>(`/hub/clusters/${clusterID}/steps`)
  const [override, setOverride] = useState<StepSelection | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const steps = data.data?.steps ?? []
  // Начальный выбор — все невыполненные шаги; выполненные сняты, и именно
  // их оператор отмечает заново, когда хочет принудительный повтор.
  const sel: StepSelection = override ?? Object.fromEntries(steps.filter((s) => !s.done).map((s) => [s.id, true]))
  const chosen = stepsChosen(steps, sel)
  const conflicts = stepConflicts(steps, sel)
  const canGo = !!data.data?.can_retry && chosen.length > 0 && conflicts.size === 0

  async function go() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ job_id: number }>(`/hub/clusters/${clusterID}/retry`, { method: 'POST', body: { steps: chosen } })
      onStarted(res.job_id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose} width={680}>
      <p className="small muted">{t('clusters.steps.retryAbout')}</p>
      <ErrorNote error={data.error ?? error} />
      {data.data && !data.data.can_retry && <Banner kind="warn">{t('clusters.steps.retryUnavailable')}</Banner>}
      {!data.data && !data.error && (
        <div className="row" style={{ gap: '0.5rem', alignItems: 'center', padding: '1rem 0' }}>
          <Spin size="small" /> <span className="muted">{t('common.loading')}</span>
        </div>
      )}
      {steps.length > 0 && <StepsChecklist steps={steps} sel={sel} onToggle={(id, v) => setOverride({ ...sel, [id]: v })} />}
      <div className="row" style={{ justifyContent: 'flex-end', gap: '0.5rem', alignItems: 'center', marginTop: '0.6rem', flexWrap: 'wrap' }}>
        {steps.length > 0 && <span className="small muted">{t('clusters.steps.chosen', { n: chosen.length })}</span>}
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button type="primary" loading={busy} disabled={!canGo} onClick={() => void go()}>
          {t('clusters.steps.continue')}
        </Button>
      </div>
    </Modal>
  )
}
