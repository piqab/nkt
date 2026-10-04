import { useState } from 'react'
import { Button, Space } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Finding, Me } from '../types'
import { confirmAction } from './confirm'
import { SERVICE_PAGE_NAMES, underFileRoots } from '../focus'
import CommandModal from './CommandModal'
import ContainerLogsModal from './ContainerLogsModal'

/** Что можно сделать с находкой прямо из её карточки. */
export type FindingAction =
  | { kind: 'link'; label: string; to: string }
  | { kind: 'logs'; label: string; container: string }
  | { kind: 'run'; label: string; container: string; action: 'start' | 'restart' }

// Правила, которые лечатся в межсетевом экране: открытый наружу порт,
// докер или kube в обход правил, мёртвое правило.
const FIREWALL_RULES = new Set([
  'sensitive-port-public',
  'docker-bypasses-firewall',
  'public-port-blocked',
  'no-default-deny',
  'stale-firewall-rule',
  'k8s-port-bypasses-firewall',
  'admin-interface-open',
])

/** Кнопки находки по её правилу, объекту и файлу. Изменяющие (запуск и
 * перезапуск контейнера) — только у того, кому разрешены действия. */
export function findingActions(f: Finding, canControl: boolean, t: (k: string, o?: Record<string, unknown>) => string): FindingAction[] {
  const out: FindingAction[] = []
  if (f.rule.startsWith('tls-cert-') && f.object) {
    out.push({ kind: 'link', label: t('findings.act.cert'), to: `/certificates${qs({ focus: f.object })}` })
  }
  if (f.service === 'docker' && f.object && f.rule.startsWith('container-')) {
    if (f.rule === 'container-restarting' || f.rule === 'container-not-running' || f.rule === 'container-undeclared') {
      out.push({ kind: 'logs', label: t('findings.act.logs'), container: f.object })
    }
    if (canControl && f.rule === 'container-restarting') {
      out.push({ kind: 'run', label: t('findings.act.restart'), container: f.object, action: 'restart' })
    }
    if (canControl && f.rule === 'container-not-running') {
      out.push({ kind: 'run', label: t('findings.act.start'), container: f.object, action: 'start' })
    }
  }
  // Кто отвечает: контейнер — его строка в «Контейнерах», служба — её
  // строка в «Сервисах».
  if (f.service === 'docker' && f.object && f.rule.startsWith('container-')) {
    out.push({ kind: 'link', label: t('findings.act.toContainer', { name: f.object }), to: `/containers${qs({ tab: 'docker', focus: f.object })}` })
  } else if (SERVICE_PAGE_NAMES.has(f.service)) {
    out.push({ kind: 'link', label: t('findings.act.toService', { name: f.service }), to: `/services${qs({ focus: f.service })}` })
  }
  if (FIREWALL_RULES.has(f.rule)) {
    out.push({ kind: 'link', label: t('findings.act.firewall'), to: '/firewall' })
  }
  if (f.rule.startsWith('malware-')) {
    out.push({ kind: 'link', label: t('findings.act.malware'), to: `/vulnerabilities${qs({ tab: 'malware', focus: f.id })}` })
    // Файл на хосте — в проводнике, на его строке. Файл внутри контейнера
    // хостовым проводником не открыть.
    if (f.file && f.service !== 'docker') out.push({ kind: 'link', label: t('findings.act.openInFiles'), to: `/disks${qs({ browse: f.file })}` })
  }
  // Неучтённый слушатель: закрыть порт — в межсетевом экране (правила и
  // сокет этого порта подсвечены), кто слушает — на карте ресурсов.
  if (f.rule === 'listening-not-declared' && f.object) {
    const port = f.object.slice(f.object.lastIndexOf(':') + 1)
    out.push({ kind: 'link', label: t('findings.act.firewallPort', { port }), to: `/firewall${qs({ focus: port })}` })
    out.push({ kind: 'link', label: t('findings.act.onMap'), to: `/topology${qs({ focus: f.object })}` })
  }
  if (f.rule === 'fail2ban') {
    out.push({ kind: 'link', label: t('findings.act.fail2ban'), to: '/fail2ban' })
  }
  // Файл — в «Конфигурациях», на строке находки. Файлы вредоносного и
  // сертификаты — не конфиги, их открывать там незачем.
  if (f.file && !f.rule.startsWith('malware-') && !f.rule.startsWith('tls-cert-')) {
    out.push({
      kind: 'link',
      label: f.line ? t('findings.act.openLine', { line: f.line }) : t('findings.act.open'),
      to: `/configs${qs({ path: f.file, line: f.line ? String(f.line) : '' })}`,
    })
  }
  return out
}

/** Ряд кнопок под находкой: переходы в нужный раздел, логи контейнера в
 * окне, запуск и перезапуск — заданием с живым журналом, как в «Docker». */
export function FindingActions({ f, me }: { f: Finding; me: Me | null }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const canControl = !!me?.is_admin && !!me?.allow_mutations
  const [logs, setLogs] = useState<string | null>(null)
  const [run, setRun] = useState<{ name: string; action: 'start' | 'restart'; outcome?: { ok: boolean; exitCode?: number } | null } | null>(null)
  // «Открыть в файлах» — только для файла под корнем проводника.
  const roots = useApi<{ roots: string[] }>(f.file && f.rule.startsWith('malware-') ? '/files/roots' : null)
  const actions = findingActions(f, canControl, t).filter(
    (a) => !(a.kind === 'link' && a.to.startsWith('/disks?browse=') && !underFileRoots(f.file ?? '', roots.data?.roots)),
  )
  if (actions.length === 0) return null

  async function finished() {
    if (!run) return
    const st = await api<{ succeeded?: boolean; exit_code?: number }>(`/containers/${encodeURIComponent(run.name)}/run/status`).catch(() => null)
    setRun((r) => (r ? { ...r, outcome: { ok: !!st?.succeeded, exitCode: st?.exit_code } } : r))
    await api('/inventory/refresh', { method: 'POST' }).catch(() => undefined)
  }

  return (
    <>
      <Space size={6} wrap style={{ marginTop: '0.55rem' }}>
        {actions.map((a) => (
          <Button
            key={a.kind + a.label}
            size="small"
            onClick={async () => {
              if (a.kind === 'link') navigate(a.to)
              else if (a.kind === 'logs') setLogs(a.container)
              else if (await confirmAction(t('docker.confirmAction', { action: t(`docker.action.${a.action}`, { defaultValue: a.action }), name: a.container })))
                setRun({ name: a.container, action: a.action })
            }}
          >
            {a.label}
          </Button>
        ))}
      </Space>
      {logs && <ContainerLogsModal name={logs} base="/containers" onClose={() => setLogs(null)} />}
      {run && (
        <CommandModal
          title={t('docker.runTitle', { action: t(`docker.action.${run.action}`, { defaultValue: run.action }), name: run.name })}
          description={t('docker.runHint')}
          wsPath={`/containers/${encodeURIComponent(run.name)}/run/ws?action=${run.action}`}
          outcome={run.outcome === undefined ? null : run.outcome ? { ...run.outcome, okText: t('docker.runOk', { name: run.name }), failText: t('docker.runFailed', { name: run.name }) } : null}
          onFinished={() => void finished()}
          onClose={() => setRun(null)}
        />
      )}
    </>
  )
}
