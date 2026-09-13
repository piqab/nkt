import { useEffect, useState } from 'react'
import { Tag } from 'antd'
import {
  AppstoreOutlined,
  CloudDownloadOutlined,
  ClusterOutlined,
  DesktopOutlined,
  FileTextOutlined,
  ProfileOutlined,
  SafetyOutlined,
  ToolOutlined,
} from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api } from '../api'
import type { CheckResult, ScriptStep } from '../pages/Scripts'
import { Banner, Spinner } from './ui'

/**
 * Схема сценария — только для чтения, строится из разбора текста
 * (тот же /hub/scripts/check): группы → хосты → что на каждом делается,
 * машины — внутри хоста, на котором создаются. Редактируется текст; схема
 * нужна, чтобы окинуть сценарий взглядом и увидеть, что куда попадёт.
 */

interface HostNode {
  name: string
  addr?: string
  auth?: string
  existing: boolean
  install: boolean
  wait: boolean
  actions: ScriptStep[]
  vms: HostNode[]
}

interface GroupNode {
  name: string
  profile?: string
  hosts: HostNode[]
}

function build(steps: ScriptStep[]): { groups: GroupNode[]; existing: HostNode[] } {
  const groups = new Map<string, GroupNode>()
  const hosts = new Map<string, HostNode>()
  const existing: HostNode[] = []
  const hostFor = (name: string): HostNode => {
    let h = hosts.get(name)
    if (!h) {
      // Хост, которого сценарий не заводил: уже есть в хабе.
      h = { name, existing: true, install: false, wait: false, actions: [], vms: [] }
      hosts.set(name, h)
      existing.push(h)
    }
    return h
  }
  for (const s of steps) {
    switch (s.kind) {
      case 'group':
        groups.set(s.name ?? '', { name: s.name ?? '', profile: s.args?.profile, hosts: [] })
        break
      case 'host': {
        const h: HostNode = {
          name: s.name ?? '',
          addr: `${s.args?.addr ?? ''}${s.args?.port && s.args.port !== '22' ? ':' + s.args.port : ''}`,
          auth: s.args?.auth,
          existing: false,
          install: false,
          wait: false,
          actions: [],
          vms: [],
        }
        hosts.set(h.name, h)
        const g = s.args?.group ?? ''
        if (!groups.has(g)) groups.set(g, { name: g, hosts: [] })
        groups.get(g)!.hosts.push(h)
        break
      }
      case 'install':
        for (const n of s.hosts ?? []) hostFor(n).install = true
        break
      case 'wait':
        if (s.host) hostFor(s.host).wait = true
        break
      case 'vm.create': {
        const parent = hostFor(s.host ?? '')
        const vm: HostNode = { name: s.name ?? '', addr: s.args?.image, existing: false, install: s.args?.install === 'true' || !!s.args?.profile, wait: false, actions: [], vms: [] }
        if (s.args?.profile) vm.actions.push({ ...s, kind: 'apply', name: s.args.profile, text: `apply profile ${s.args.profile}` })
        parent.vms.push(vm)
        hosts.set(vm.name, vm)
        break
      }
      default:
        if (s.host) hostFor(s.host).actions.push(s)
    }
  }
  return { groups: [...groups.values()], existing }
}

function actionIcon(kind: string) {
  switch (kind) {
    case 'packages':
      return <ToolOutlined />
    case 'service':
      return <AppstoreOutlined />
    case 'firewall':
      return <SafetyOutlined />
    case 'docker.install':
    case 'docker.stack':
      return <ClusterOutlined />
    case 'vm.action':
      return <DesktopOutlined />
    case 'apply':
      return <ProfileOutlined />
    case 'file.put':
      return <FileTextOutlined />
    default:
      return null
  }
}

function actionLabel(s: ScriptStep): string {
  switch (s.kind) {
    case 'packages':
      return `${s.action} ${(s.list ?? []).join(', ')}`
    case 'service':
      return `${s.name} ${s.action}`
    case 'firewall':
      return `${s.action} ${s.args?.port}/${s.args?.proto}${s.args?.from ? ' from ' + s.args.from : ''}`
    case 'docker.install':
      return 'docker install'
    case 'docker.stack':
      return `stack ${s.args?.path} ${s.action}${s.block ? ' (compose)' : ''}`
    case 'vm.action':
      return `vm ${s.action} ${s.name}`
    case 'apply':
      return `profile ${s.name}`
    case 'file.put':
      return `file ${s.args?.path}`
    default:
      return s.text
  }
}

function HostCard({ h }: { h: HostNode }) {
  const { t } = useTranslation()
  return (
    <div className={`scheme-host${h.existing ? ' scheme-host-existing' : ''}`}>
      <div className="row" style={{ gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
        <DesktopOutlined />
        <strong>{h.name}</strong>
        {h.addr && <span className="small muted mono">{h.addr}</span>}
        {h.auth && <Tag>{t(`scripts.scheme.auth.${h.auth}`)}</Tag>}
        {h.existing && <Tag>{t('scripts.scheme.existing')}</Tag>}
        {h.install && (
          <Tag color="blue">
            <CloudDownloadOutlined /> nkt
          </Tag>
        )}
        {h.wait && <Tag>{t('scripts.scheme.wait')}</Tag>}
      </div>
      {h.actions.length > 0 && (
        <ol className="scheme-actions">
          {h.actions.map((a, i) => (
            <li key={i} title={a.text}>
              {actionIcon(a.kind)} {actionLabel(a)}
            </li>
          ))}
        </ol>
      )}
      {h.vms.length > 0 && (
        <div className="scheme-vms">
          {h.vms.map((vm) => (
            <HostCard key={vm.name} h={vm} />
          ))}
        </div>
      )}
    </div>
  )
}

export default function ScriptScheme({ content }: { content: string }) {
  const { t } = useTranslation()
  const [result, setResult] = useState<CheckResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Разбор — на сервере, с задержкой после правки: схема догоняет текст,
  // не дёргая хаб на каждую букву.
  useEffect(() => {
    let cancelled = false
    const timer = setTimeout(() => {
      api<CheckResult>('/hub/scripts/check', { method: 'POST', body: { content } })
        .then((res) => {
          if (!cancelled) {
            setResult(res)
            setError(null)
          }
        })
        .catch((err) => {
          if (!cancelled) setError(err instanceof Error ? err.message : String(err))
        })
    }, 400)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [content])

  if (error) return <Banner kind="error">{error}</Banner>
  if (!result) {
    return (
      <div className="small muted">
        <Spinner /> {t('scripts.scheme.building')}
      </div>
    )
  }
  const { groups, existing } = build(result.steps)
  if (groups.length === 0 && existing.length === 0) return <p className="small muted">{t('scripts.scheme.empty')}</p>
  return (
    <div className="col" style={{ gap: '0.6rem' }}>
      {result.issues.length > 0 && <Banner kind="warn">{t('scripts.scheme.issues', { count: result.issues.length })}</Banner>}
      <div className="scheme-groups">
        {groups.map((g) => (
          <div key={g.name || '~'} className="scheme-group">
            <div className="scheme-group-head">
              <ClusterOutlined /> <strong>{g.name || t('hosts.groupNone')}</strong>
              {g.profile && <Tag color="blue">{t('scripts.scheme.profile', { name: g.profile })}</Tag>}
            </div>
            {g.hosts.length === 0 && <div className="small muted">{t('scripts.scheme.noHosts')}</div>}
            {g.hosts.map((h) => (
              <HostCard key={h.name} h={h} />
            ))}
          </div>
        ))}
        {existing.length > 0 && (
          <div className="scheme-group scheme-group-existing">
            <div className="scheme-group-head">
              <ClusterOutlined /> <strong>{t('scripts.scheme.existingHosts')}</strong>
            </div>
            {existing.map((h) => (
              <HostCard key={h.name} h={h} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
