import { useState } from 'react'
import { Button, Checkbox, Dropdown, Input, InputNumber, Select, Space } from 'antd'
import { MoreOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me } from '../types'
import { Banner, CodeEditor, Loading, Modal } from './ui'
import { confirmAction } from './confirm'
import CommandModal from './CommandModal'
import { useJobLauncher } from './useJobLauncher'
import { K8sYAMLModal } from './K8sYAML'

export interface K8sRow {
  name: string
  namespace?: string
  cols: Record<string, string>
}

/** Действия, которые сервер разрешает виду (см. k8s.Actions). */
const ACTIONS: Record<string, string[]> = {
  deployments: ['scale', 'restart', 'history', 'delete'],
  statefulsets: ['scale', 'restart', 'history', 'delete'],
  daemonsets: ['restart', 'history', 'delete'],
  jobs: ['delete'],
  cronjobs: ['trigger', 'suspend', 'resume', 'delete'],
  pods: ['logs', 'exec', 'delete'],
  services: ['delete'],
  ingresses: ['delete'],
  configmaps: ['delete'],
  secrets: ['delete'],
  pvc: ['delete'],
  nodes: ['cordon', 'uncordon', 'drain'],
  namespaces: ['delete'],
}

type Dialog = { type: 'describe' | 'yaml' | 'scale' | 'history' | 'logs' | 'exec' | 'result'; text?: string }

/**
 * Меню действий строки объекта: описание (kubectl describe) — всем,
 * изменения — администратору при разрешённых изменениях; каждое уходит
 * в аудит хоста. Долгий вывод узла (drain) — заданием.
 */
export function K8sRowActions({ kind, row, me, onChanged, onError }: { kind: string; row: K8sRow; me: Me; onChanged: () => void; onError: (e: string) => void }) {
  const { t } = useTranslation()
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const job = useJobLauncher(() => onChanged())
  const canMutate = me.is_admin && me.allow_mutations
  const id = row.namespace ? `${row.namespace}/${row.name}` : row.name
  const actions = kind.startsWith('cr:') ? ['delete'] : (ACTIONS[kind] ?? [])
  const suspended = row.cols.suspend === 'true'

  async function act(action: string, extra: Record<string, unknown> = {}) {
    try {
      const out = await api<{ output: string }>('/k8s/objects/action', { method: 'POST', body: { kind, namespace: row.namespace ?? '', name: row.name, action, ...extra } })
      onChanged()
      if (out.output) setDialog({ type: 'result', text: out.output })
      return true
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
      return false
    }
  }

  async function run(action: string) {
    switch (action) {
      case 'describe':
      case 'yaml':
      case 'scale':
      case 'history':
      case 'logs':
      case 'exec':
        setDialog({ type: action })
        return
      case 'drain':
        if (!(await confirmAction(t('k8s.act.drainConfirm', { name: row.name }), { okText: t('k8s.act.drain') }))) return
        try {
          await job.start('/k8s/nodes/drain', { name: row.name })
        } catch (err) {
          onError(err instanceof Error ? err.message : String(err))
        }
        return
      case 'delete':
        if (!(await confirmAction(t(kind === 'namespaces' ? 'k8s.act.deleteNsConfirm' : 'k8s.act.deleteConfirm', { name: id }), { okText: t('k8s.act.delete') }))) return
        break
      case 'restart':
        if (!(await confirmAction(t('k8s.act.restartConfirm', { name: id }), { okText: t('k8s.act.restart'), danger: false }))) return
        break
      case 'cordon':
      case 'uncordon':
      case 'trigger':
      case 'suspend':
      case 'resume':
        if (!(await confirmAction(t(`k8s.act.${action}Confirm`, { name: id }), { okText: t(`k8s.act.${action}`), danger: false }))) return
        break
    }
    await act(action)
  }

  const visible = actions.filter((a) => {
    if (a === 'suspend') return !suspended
    if (a === 'resume') return suspended
    // Журнал и консоль пода — PTY-сессии, как у контейнеров: администратору.
    if (a === 'logs' || a === 'exec') return me.is_admin
    return canMutate
  })
  const items = [
    { key: 'describe', label: t('k8s.act.describe') },
    ...(kind !== 'secrets' && kind !== 'events' ? [{ key: 'yaml', label: 'YAML' }] : []),
    ...visible.map((a) => ({ key: a, label: t(`k8s.act.${a}`), danger: a === 'delete' || a === 'drain' })),
  ]

  return (
    <>
      <Dropdown trigger={['click']} menu={{ items, onClick: ({ key }) => void run(key) }}>
        <Button size="small" type="text" icon={<MoreOutlined />} aria-label={t('k8s.act.menu')} />
      </Dropdown>
      {job.modal}
      {dialog?.type === 'describe' && <DescribeModal kind={kind} row={row} onClose={() => setDialog(null)} />}
      {dialog?.type === 'yaml' && <K8sYAMLModal kind={kind} namespace={row.namespace} name={row.name} me={me} onClose={() => setDialog(null)} onSaved={onChanged} />}
      {dialog?.type === 'result' && (
        <Modal title={id} onClose={() => setDialog(null)} width={640}>
          <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0 }}>
            {dialog.text}
          </pre>
        </Modal>
      )}
      {dialog?.type === 'scale' && <ScaleModal row={row} id={id} onClose={() => setDialog(null)} onScale={(n) => act('scale', { replicas: n })} />}
      {dialog?.type === 'history' && <HistoryModal kind={kind} row={row} id={id} canMutate={canMutate} onClose={() => setDialog(null)} onUndo={(rev) => act('undo', { revision: rev })} />}
      {(dialog?.type === 'logs' || dialog?.type === 'exec') && <PodSessionModal row={row} mode={dialog.type} onClose={() => setDialog(null)} />}
    </>
  )
}

function DescribeModal({ kind, row, onClose }: { kind: string; row: K8sRow; onClose: () => void }) {
  const { t } = useTranslation()
  const res = useApi<{ text: string }>(`/k8s/describe${qs({ kind, namespace: row.namespace || undefined, name: row.name })}`)
  return (
    <Modal title={t('k8s.act.describeTitle', { name: row.namespace ? `${row.namespace}/${row.name}` : row.name })} onClose={onClose} width={960} sizeKey="k8s-describe">
      {res.error ? <Banner kind="error">{res.error}</Banner> : !res.data ? <Loading what="describe" /> : <CodeEditor value={res.data.text} readOnly rows={28} fill />}
    </Modal>
  )
}

function ScaleModal({ row, id, onClose, onScale }: { row: K8sRow; id: string; onClose: () => void; onScale: (n: number) => Promise<boolean> }) {
  const { t } = useTranslation()
  // «2/3» — готовы из желаемых: исходное значение — желаемое.
  const want = Number((row.cols.ready ?? '').split('/')[1] ?? 1)
  const [n, setN] = useState<number>(Number.isFinite(want) ? want : 1)
  const [busy, setBusy] = useState(false)
  return (
    <Modal title={t('k8s.act.scaleTitle', { name: id })} onClose={onClose} width={420}>
      <p className="small muted">{t('k8s.act.scaleHint', { current: row.cols.ready ?? '—' })}</p>
      <Space>
        <InputNumber min={0} max={1000} value={n} onChange={(v) => setN(v ?? 0)} />
        <Button
          type="primary"
          loading={busy}
          onClick={async () => {
            if (n === 0 && !(await confirmAction(t('k8s.act.scaleZero', { name: id })))) return
            setBusy(true)
            const ok = await onScale(n)
            setBusy(false)
            if (ok) onClose()
          }}
        >
          {t('k8s.act.scale')}
        </Button>
      </Space>
    </Modal>
  )
}

function HistoryModal({
  kind,
  row,
  id,
  canMutate,
  onClose,
  onUndo,
}: {
  kind: string
  row: K8sRow
  id: string
  canMutate: boolean
  onClose: () => void
  onUndo: (rev: number) => Promise<boolean>
}) {
  const { t } = useTranslation()
  const res = useApi<{ text: string }>(`/k8s/rollout/history${qs({ kind, namespace: row.namespace, name: row.name })}`)
  const revisions = (res.data?.text ?? '')
    .split('\n')
    .map((l) => /^(\d+)\s/.exec(l)?.[1])
    .filter((v): v is string => !!v)
    .map(Number)
  const [rev, setRev] = useState<number | null>(null)
  return (
    <Modal title={t('k8s.act.historyTitle', { name: id })} onClose={onClose} width={760}>
      {res.error ? (
        <Banner kind="error">{res.error}</Banner>
      ) : !res.data ? (
        <Loading what="rollout history" />
      ) : (
        <>
          <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: '0 0 0.75rem' }}>
            {res.data.text}
          </pre>
          {canMutate && (
            <Space wrap>
              <span className="small">{t('k8s.act.undoTo')}</span>
              <Select
                size="small"
                style={{ width: '12rem' }}
                value={rev ?? 0}
                onChange={setRev}
                options={[{ value: 0, label: t('k8s.act.undoPrevious') }, ...revisions.slice(0, -1).map((r) => ({ value: r, label: `${t('k8s.act.revision')} ${r}` }))]}
              />
              <Button
                danger
                size="small"
                onClick={async () => {
                  if (!(await confirmAction(t('k8s.act.undoConfirm', { name: id, rev: rev || t('k8s.act.undoPrevious') }), { okText: t('k8s.act.undo') }))) return
                  if (await onUndo(rev ?? 0)) onClose()
                }}
              >
                {t('k8s.act.undo')}
              </Button>
            </Space>
          )}
        </>
      )}
    </Modal>
  )
}

/** Журнал (kubectl logs) или консоль (kubectl exec) пода: выбор
 * контейнера, для журнала — хвост, слежение и предыдущий запуск. */
function PodSessionModal({ row, mode, onClose }: { row: K8sRow; mode: 'logs' | 'exec'; onClose: () => void }) {
  const { t } = useTranslation()
  const ns = row.namespace ?? ''
  const res = useApi<{ containers: string[] }>(`/k8s/pods/containers${qs({ namespace: ns, name: row.name })}`)
  const [container, setContainer] = useState<string | null>(null)
  const [tail, setTail] = useState(200)
  const [follow, setFollow] = useState(true)
  const [previous, setPrevious] = useState(false)
  const [session, setSession] = useState<string | null>(null)
  const title = t(mode === 'logs' ? 'k8s.act.logsTitle' : 'k8s.act.execTitle', { name: `${ns}/${row.name}` })
  const containers = res.data?.containers ?? []
  const chosen = container ?? containers[containers.length - 1] ?? ''

  if (session !== null) {
    return <CommandModal key={session} title={title} wsPath={session} onClose={onClose} description={mode === 'exec' ? t('k8s.act.execHint') : undefined} />
  }
  const open = () => {
    const q = { namespace: ns, name: row.name, container: chosen }
    setSession(
      mode === 'logs'
        ? `/k8s/pods/logs/ws${qs({ ...q, tail: String(tail), follow: follow ? '1' : undefined, previous: previous ? '1' : undefined })}`
        : `/k8s/pods/exec/ws${qs(q)}`,
    )
  }
  return (
    <Modal title={title} onClose={onClose} width={560}>
      {res.error && <Banner kind="error">{res.error}</Banner>}
      {!res.data && !res.error ? (
        <Loading what="pod" />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <Space wrap>
            <span className="small">{t('k8s.act.container')}</span>
            <Select size="small" style={{ minWidth: '14rem' }} value={chosen} onChange={setContainer} options={containers.map((c) => ({ value: c, label: c }))} />
          </Space>
          {mode === 'logs' && (
            <Space wrap>
              <span className="small">{t('k8s.act.tail')}</span>
              <InputNumber size="small" min={10} max={10000} value={tail} onChange={(v) => setTail(v ?? 200)} />
              <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
                {t('k8s.act.follow')}
              </Checkbox>
              <Checkbox checked={previous} onChange={(e) => setPrevious(e.target.checked)}>
                {t('k8s.act.previous')}
              </Checkbox>
            </Space>
          )}
          <div>
            <Button type="primary" disabled={!chosen} onClick={open}>
              {t(mode === 'logs' ? 'k8s.act.showLogs' : 'k8s.act.openConsole')}
            </Button>
          </div>
        </div>
      )}
    </Modal>
  )
}

/** Кнопка «Создать namespace» над таблицей namespace. */
export function CreateNamespaceButton({ onCreated, onError }: { onCreated: () => void; onError: (e: string) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const valid = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(name)
  async function create() {
    setBusy(true)
    try {
      await api('/k8s/namespaces', { method: 'POST', body: { name } })
      setOpen(false)
      setName('')
      onCreated()
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Button size="small" onClick={() => setOpen(true)}>
        {t('k8s.act.createNs')}
      </Button>
      {open && (
        <Modal title={t('k8s.act.createNs')} onClose={() => setOpen(false)} width={460}>
          <p className="small muted">{t('k8s.act.nsNameHint')}</p>
          <Space>
            <Input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="team-a" status={name && !valid ? 'error' : undefined} onPressEnter={() => valid && void create()} />
            <Button type="primary" disabled={!valid} loading={busy} onClick={() => void create()}>
              {t('k8s.act.create')}
            </Button>
          </Space>
        </Modal>
      )}
    </>
  )
}
