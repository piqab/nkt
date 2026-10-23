import { type ReactNode } from 'react'
import { Dropdown, type MenuProps } from 'antd'
import { useTranslation } from 'react-i18next'
import { api } from '../../api'
import { blurText } from '../../privacy'
import { openInApp } from '../../appNav'
import { confirmAction } from '../confirm'
import { pipelinePower } from '../pipelinePower'

/**
 * Объекты хаба в чате ассистента: имена в тексте — ссылки, щелчок —
 * меню действий по типу объекта (открыть в разделе, спросить ассистента,
 * а администратору — перезапустить, выложить, остановить…). Меняющие
 * действия — с подтверждением и теми же вызовами, что кнопки разделов.
 */

export interface ChatObject {
  kind: 'host' | 'group' | 'pipeline' | 'script' | 'cluster' | 'route' | 'container' | 'service'
  name: string
  id?: number
  ref?: string
  host_id?: number
  host?: string
  status?: string
}

export interface ChatActions {
  /** Отправить вопрос ассистенту. */
  ask: (text: string) => void
  /** Открыть задание хаба (окно журнала). */
  openJob: (id: number) => void
  /** Итог действия (перезапуск и т. п.) — строкой в чате. */
  notice: (kind: 'success' | 'error', text: string) => void
  admin: boolean
}

const hostPath = (id: number) => (id === -1 ? '/hosts/local' : `/hosts/${id}`)

function escapeRe(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Текст с именами объектов ссылками. Имя узнаётся целым словом (не часть
 * «web-10» у «web-1»); одно имя у нескольких объектов (хост и контейнер)
 * — одна ссылка, меню с обоими.
 */
export function linkify(text: string, objects: ChatObject[], actions: ChatActions): ReactNode {
  const byName = new Map<string, ChatObject[]>()
  for (const o of objects) {
    if (o.name.length < 2) continue
    byName.set(o.name, [...(byName.get(o.name) ?? []), o])
  }
  if (byName.size === 0) return blurText(text)
  const names = [...byName.keys()].sort((a, b) => b.length - a.length).map(escapeRe)
  const re = new RegExp(`(?<![\\w.-])(${names.join('|')})(?![\\w-]|\\.\\w)`, 'g')
  const out: ReactNode[] = []
  let last = 0
  let n = 0
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > last) out.push(<span key={n++}>{blurText(text.slice(last, m.index))}</span>)
    out.push(<ObjectLink key={n++} name={m[1]} objects={byName.get(m[1]) ?? []} actions={actions} />)
    last = m.index + m[1].length
  }
  if (last < text.length) out.push(<span key={n++}>{blurText(text.slice(last))}</span>)
  return out
}

function ObjectLink({ name, objects, actions }: { name: string; objects: ChatObject[]; actions: ChatActions }) {
  const { t } = useTranslation()
  const items: MenuProps['items'] = []
  for (const o of objects) {
    if (objects.length > 1) items.push({ type: 'group', key: `g-${o.kind}-${o.host_id ?? ''}`, label: t(`assistant.obj.kind.${o.kind}`) + (o.host ? ` · ${o.host}` : '') })
    for (const it of objectItems(o, actions, t)) items.push(it)
  }
  return (
    <Dropdown trigger={['click']} menu={{ items }}>
      <a className="chat-object" role="button" tabIndex={0} title={t(`assistant.obj.kind.${objects[0]?.kind ?? 'host'}`)} onClick={(e) => e.preventDefault()}>
        {blurText(name)}
      </a>
    </Dropdown>
  )
}

type T = (key: string, opts?: Record<string, unknown>) => string

function objectItems(o: ChatObject, a: ChatActions, t: T): NonNullable<MenuProps['items']> {
  const key = (s: string) => `${o.kind}-${o.host_id ?? ''}-${o.name}-${s}`
  const run = (fn: () => Promise<void>) => () => {
    void fn().catch((err) => a.notice('error', err instanceof Error ? err.message : String(err)))
  }
  switch (o.kind) {
    case 'host': {
      const host = { id: o.id ?? 0, name: o.name }
      return [
        { key: key('open'), label: t('assistant.obj.openHost'), onClick: () => openInApp({ host, path: '/' }) },
        { key: key('findings'), label: t('assistant.obj.findings'), onClick: () => openInApp({ host, path: '/findings' }) },
        { key: key('containers'), label: t('assistant.obj.containers'), onClick: () => openInApp({ host, path: '/containers' }) },
        { key: key('services'), label: t('assistant.obj.services'), onClick: () => openInApp({ host, path: '/services' }) },
        { key: key('ask'), label: t('assistant.obj.askHost', { name: o.name }), onClick: () => a.ask(t('assistant.obj.askHostText', { name: o.name })) },
      ]
    }
    case 'container':
    case 'service': {
      const host = { id: o.host_id ?? 0, name: o.host ?? '' }
      const page = o.kind === 'container' ? '/containers' : '/services'
      const items: NonNullable<MenuProps['items']> = [
        { key: key('open'), label: t('assistant.obj.openOn', { host: o.host }), onClick: () => openInApp({ host, path: `${page}?focus=${encodeURIComponent(o.name)}` }) },
        { key: key('logs'), label: t('assistant.obj.logs'), onClick: () => openInApp({ host, path: '/logs' }) },
        { key: key('ask'), label: t('assistant.obj.askObj'), onClick: () => a.ask(t(`assistant.obj.ask.${o.kind}`, { name: o.name, host: o.host })) },
      ]
      if (a.admin && o.host_id !== undefined) {
        items.push({
          key: key('restart'),
          danger: true,
          label: t('assistant.obj.restart'),
          onClick: run(async () => {
            if (!(await confirmAction(t(`assistant.obj.restartConfirm.${o.kind}`, { name: o.name, host: o.host }), { okText: t('assistant.obj.restart') }))) return
            const path = o.kind === 'container' ? `${hostPath(o.host_id!)}/containers/${encodeURIComponent(o.name)}/restart` : `${hostPath(o.host_id!)}/services/${encodeURIComponent(o.name)}/restart`
            await api(path, { method: 'POST' })
            a.notice('success', t('assistant.obj.restarted', { name: o.name, host: o.host }))
          }),
        })
      }
      return items
    }
    case 'pipeline': {
      const items: NonNullable<MenuProps['items']> = [{ key: key('open'), label: t('assistant.obj.openPipeline'), onClick: () => openInApp({ view: 'deploy', tab: 'pipelines' }) }]
      if (a.admin && o.id) {
        const id = o.id
        items.push(
          {
            key: key('dry'),
            label: t('assistant.obj.dryRun'),
            onClick: run(async () => {
              const res = await api<{ job_id: number }>('/hub/pipelines/dryrun', { method: 'POST', body: { pipeline_id: id } })
              a.openJob(res.job_id)
            }),
          },
          {
            key: key('deploy'),
            label: t('assistant.obj.deploy'),
            onClick: run(async () => {
              if (!(await confirmAction(t('assistant.obj.deployConfirm', { name: o.name }), { okText: t('assistant.obj.deploy') }))) return
              const res = await api<{ job_id: number }>(`/hub/pipelines/${id}/deploy`, { method: 'POST', body: {} })
              if (res.job_id) a.openJob(res.job_id)
            }),
          },
          {
            key: key('start'),
            label: t('deploy.power.start'),
            onClick: run(async () => {
              const job = await pipelinePower(id, o.name, 'start')
              if (job) a.openJob(job)
            }),
          },
          {
            key: key('stop'),
            danger: true,
            label: t('deploy.power.stop'),
            onClick: run(async () => {
              const job = await pipelinePower(id, o.name, 'stop')
              if (job) a.openJob(job)
            }),
          },
        )
      }
      return items
    }
    case 'script':
      return [{ key: key('open'), label: t('assistant.obj.openScripts'), onClick: () => openInApp({ view: 'profiles', tab: 'scripts' }) }]
    case 'cluster':
      return [{ key: key('open'), label: t('assistant.obj.openClusters'), onClick: () => openInApp({ view: 'clusters' }) }]
    case 'route':
      return [{ key: key('open'), label: t('assistant.obj.openFly'), onClick: () => openInApp({ view: 'fly' }) }]
    case 'group':
      return [
        { key: key('open'), label: t('assistant.obj.openHosts'), onClick: () => openInApp({ view: 'hosts' }) },
        { key: key('ask'), label: t('assistant.obj.askObj'), onClick: () => a.ask(t('assistant.obj.ask.group', { name: o.name })) },
      ]
  }
  return []
}
