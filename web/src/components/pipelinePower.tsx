import i18n from '../i18n'
import { api } from '../api'
import { confirmAction } from './confirm'

/** Что затронет «Запустить» / «Остановить»: хосты выкладки и маршруты Traffic Fly. */
interface PowerPlan {
  project: string
  hosts: { id: number; name: string }[]
  routes: string[]
}

/**
 * «Остановить» / «Запустить» выкладку конвейера с подтверждением — общее
 * для «Выкладок» и меню конвейера в чате ассистента. «Остановить»
 * спрашивает дважды: что затронет (хосты, маршруты, оповещения) и что
 * сервис станет недоступен. Возвращает id задания хаба или null (отказ);
 * ошибка запроса — исключением.
 */
export async function pipelinePower(id: number, name: string, action: 'start' | 'stop'): Promise<number | null> {
  const t = i18n.t.bind(i18n)
  const plan = await api<PowerPlan>(`/hub/pipelines/${id}/power`)
  const hosts = plan.hosts.map((h) => h.name).join(', ')
  const routes = plan.routes.join(', ')
  if (action === 'stop') {
    const first = (
      <div className="col" style={{ gap: '0.4rem' }}>
        <span>{t('deploy.power.stopConfirm1', { name, hosts })}</span>
        {routes && <span>{t('deploy.power.stopRoutes', { routes })}</span>}
        <span className="small muted">{t('deploy.power.stopKeeps')}</span>
      </div>
    )
    if (!(await confirmAction(first, { title: t('deploy.power.stopTitle', { name }), okText: t('deploy.power.next') }))) return null
    if (!(await confirmAction(t('deploy.power.stopConfirm2', { name, count: plan.hosts.length }), { title: t('deploy.power.stopTitle', { name }), okText: t('deploy.power.stop') }))) return null
  } else if (!(await confirmAction(t('deploy.power.startConfirm', { name, hosts }), { title: t('deploy.power.startTitle', { name }), okText: t('deploy.power.start'), danger: false }))) {
    return null
  }
  const res = await api<{ job_id: number }>(`/hub/pipelines/${id}/${action}`, { method: 'POST' })
  return res.job_id
}
