import { useState } from 'react'
import { Button, Checkbox, Form, Input, InputNumber, Segmented, type TableColumnsType } from 'antd'
import { CheckCircleFilled, CloseCircleOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { AIConfigError } from '../components/AIConfigError'
import BlockTree from '../components/BlockTree'
import { VersionHistory } from '../components/VersionHistory'
import { BackupModal } from '../components/BackupModal'
import { ConsoleModal } from '../components/ConsoleModal'
import { VMScreenModal, addVNCGraphics } from '../components/VMScreenModal'
import { useHostRescan } from '../rescan'
import { api, qs, useApi } from '../api'
import type { FileContent, Me, VirtualMachine, WriteResult } from '../types'
import { Banner, Card, CodeEditor, DiffView, ErrorNote, formatBytesShort, InfoHint, Loading, Modal, StateBadge } from '../components/ui'
import i18n from '../i18n'
import { confirmAction, confirmWithOption } from '../components/confirm'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { PowerToggle, vmPowerState } from '../components/PowerToggle'
import VMImagesSection from '../components/VMImagesSection'

function domainXMLSkeleton(name: string): string {
  return domainXMLFromWizard(name, { memoryMB: 2048, vcpus: 2, diskPath: defaultDiskPath(name), bridge: 'br0' })
}

function defaultDiskPath(name: string): string {
  return `/var/lib/libvirt/images/${name}.qcow2`
}

function domainXMLFromWizard(
  name: string,
  p: { memoryMB: number; vcpus: number; diskPath: string; bridge: string },
): string {
  return `<domain type='kvm'>
  <name>${name}</name>
  <memory unit='KiB'>${Math.max(1, p.memoryMB) * 1024}</memory>
  <vcpu placement='static'>${Math.max(1, p.vcpus)}</vcpu>
  <os>
    <type arch='x86_64' machine='pc-q35-8.0'>hvm</type>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='${p.diskPath}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='bridge'>
      <source bridge='${p.bridge}'/>
      <model type='virtio'/>
    </interface>
    <graphics type='vnc' port='-1' autoport='yes'/>
  </devices>
</domain>
`
}

function vmColumns(
  canControl: boolean,
  busy: string | null,
  act: (name: string, action: string) => void,
  toggleAutostart: (name: string, on: boolean) => void,
  del: (name: string) => Promise<void>,
  openBackup: (name: string) => void,
  openConsole: (name: string) => void,
  openScreen: (name: string) => void,
  editXML: (name: string) => void,
): TableColumnsType<VirtualMachine> {
  const t = i18n.t.bind(i18n)
  return [
    {
      title: t('virt.colName'),
      key: 'name',
      render: (_, vm) => (
        <>
          <strong>{vm.name}</strong>
          <div className="small muted">{vm.uuid || '—'}</div>
        </>
      ),
    },
    { title: t('virt.colState'), key: 'state', render: (_, vm) => <StateBadge state={vm.state} /> },
    { title: t('virt.colVcpus'), key: 'vcpus', align: 'right', render: (_, vm) => <span className="num small">{vm.vcpus || '—'}</span> },
    {
      title: t('virt.colMemory'),
      key: 'memory_kb',
      align: 'right',
      render: (_, vm) => <span className="num small">{vm.memory_kb ? formatBytesShort(vm.memory_kb * 1024) : '—'}</span>,
    },
    {
      title: t('virt.colDisks'),
      key: 'disks',
      render: (_, vm) => (
        <span className="small mono">
          {(vm.disks ?? []).map((d, i) => (
            <div key={i}>
              {d.source || '—'}
              {d.bus ? ` (${d.bus})` : ''}
            </div>
          ))}
        </span>
      ),
    },
    {
      title: t('virt.colNetworks'),
      key: 'networks',
      render: (_, vm) => (
        <span className="small">
          {(vm.networks ?? []).map((n, i) => (
            <div key={i}>{n.source || '—'}</div>
          ))}
        </span>
      ),
    },
    {
      title: t('virt.colAutostart'),
      key: 'autostart',
      render: (_, vm) =>
        vm.persistent ? (
          // Состояние — иконкой, как галочка/крестик в списке хостов;
          // слово «включён/выключен» занимало столбец ради двух состояний.
          // Клик по ней переключает, а подсказка говорит и что сейчас, и
          // что будет.
          <RowAction
            icon={
              vm.autostart ? (
                <CheckCircleFilled style={{ color: 'var(--status-good)' }} />
              ) : (
                <CloseCircleOutlined style={{ color: 'var(--text-muted)' }} />
              )
            }
            label={`${t('virt.colAutostart')}: ${vm.autostart ? t('virt.autostartOn') : t('virt.autostartOff')} — ${
              vm.autostart ? t('vmnet.autostartOff') : t('vmnet.autostartOn')
            }`}
            disabled={!canControl}
            loading={busy === `${vm.name}:autostart`}
            onClick={() => toggleAutostart(vm.name, !vm.autostart)}
          />
        ) : (
          <span className="small muted" title={t('virt.transientTooltip')}>
            {t('virt.transientUnavailable')}
          </span>
        ),
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_, vm) => (
        <div className="row">
          {/* Одна кнопка питания: работает — «выключить», выключена —
              «запустить», на паузе — «возобновить». Перезагрузка и пауза —
              только у работающей. */}
          <PowerToggle
            state={vmPowerState(vm.state)}
            labels={{ start: t('virt.action.start', { defaultValue: 'start' }), stop: t('virt.action.shutdown', { defaultValue: 'shutdown' }), resume: t('virt.action.resume', { defaultValue: 'resume' }) }}
            disabled={!canControl}
            loading={busy === `${vm.name}:start` || busy === `${vm.name}:shutdown` || busy === `${vm.name}:resume`}
            onStart={() => act(vm.name, 'start')}
            onStop={() => act(vm.name, 'shutdown')}
            onResume={() => act(vm.name, 'resume')}
          />
          {vmPowerState(vm.state) === 'running' &&
            ['reboot', 'suspend'].map((a) => (
              <RowAction
                key={a}
                action={a}
                label={t(`virt.action.${a}`, { defaultValue: a })}
                disabled={!canControl}
                loading={busy === `${vm.name}:${a}`}
                onClick={() => act(vm.name, a)}
              />
            ))}
          {canControl && vmPowerState(vm.state) !== 'stopped' && (
            <RowAction
              action="destroy"
              label={`${t('virt.forceDestroy')} — ${t('virt.forceDestroyTooltip')}`}
              danger
              loading={busy === `${vm.name}:destroy`}
              onClick={() => act(vm.name, 'destroy')}
            />
          )}
          {/* Конфигурация машины — тот же XML, из которого её и создают:
              /etc/libvirt/qemu/<имя>.xml через редактор конфигураций, с
              историей версий и «применить» (virsh define). У транзитной
              машины файла нет — libvirt хранит её только в памяти. */}
          {canControl && vm.persistent && (
            <RowAction
              action="edit"
              label={`${t('virt.editXML')}${vm.state === 'running' ? ` — ${t('virt.editXMLRunning')}` : ''}`}
              onClick={() => editXML(vm.name)}
            />
          )}
          <RowAction action="backup" label={t('backups.action')} onClick={() => openBackup(vm.name)} />
          {canControl && vmPowerState(vm.state) === 'running' && (
            <>
              <RowAction action="console" label={t('console.action')} onClick={() => openConsole(vm.name)} />
              <RowAction action="screen" label={t('vnc.action')} onClick={() => openScreen(vm.name)} />
            </>
          )}
          {canControl && vm.persistent && (
            <>
              {/* Одна кнопка удаления; «вместе с дисками» — галочка в окне
                  подтверждения, по умолчанию выключена. */}
              <RowAction
                action="delete"
                label={t('common.delete')}
                danger
                loading={busy === `${vm.name}:delete`}
                onClick={() => void del(vm.name)}
              />
            </>
          )}
        </div>
      ),
    },
  ]
}

export default function Virtualization({ me }: { me: Me }) {
  const { t } = useTranslation()
  const vms = useApi<{ vms: VirtualMachine[] }>('/vms', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState<{ name: string; initialContent?: string } | null>(null)
  // Правка XML существующей машины — тот же VMEditor, что и создание.
  const [editing, setEditing] = useState<string | null>(null)
  const [backupFor, setBackupFor] = useState<string | null>(null)
  const [consoleFor, setConsoleFor] = useState<string | null>(null)
  const [screenFor, setScreenFor] = useState<string | null>(null)
  // Заготовка правки XML (добавить VNC) — запись всё равно через дифф.
  const [editPrefill, setEditPrefill] = useState<{ edit: (s: string) => string; intro: string } | null>(null)
  const [chooserOpen, setChooserOpen] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  // Раздел показывает снимок инвентаря: при входе он пересобирается сам,
  // иначе только что поднятого контейнера в списке не окажется.
  const { rescanning, rescan } = useHostRescan({
    reload: () => vms.reload(),
    canScan: canControl,
    onNotice: (kind, text) => setNotice({ kind, text }),
  })
  const allVMs = vms.data?.vms ?? []


  async function act(name: string, action: string) {
    const label = action === 'destroy' ? t('virt.forceDestroy') : action
    if (!(await confirmAction(t('virt.confirmAction', { action: label, name })))) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/vms/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: t('virt.actionDone', { name, action: label }) })
      // The backend only kicks off a fire-and-forget background rescan
      // (rescanLater) — a bare reload() right after would just reread the
      // still-stale cached snapshot. /inventory/refresh runs the same
      // rescan synchronously, same as "Пересканировать" below.
      await api('/inventory/refresh', { method: 'POST' })
      await vms.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function toggleAutostart(name: string, on: boolean) {
    setBusy(`${name}:autostart`)
    setNotice(null)
    try {
      await api(`/vms/${name}/${on ? 'autostart-on' : 'autostart-off'}`, { method: 'POST' })
      await api('/inventory/refresh', { method: 'POST' })
      await vms.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    const answer = await confirmWithOption(t('virt.confirmDeleteDefinition', { name }), t('virt.deleteDisksOption'), {
      optionHint: t('virt.deleteDisksHint'),
    })
    if (!answer) return
    const removeStorage = answer.checked
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/vms/${name}${qs({ remove_storage: removeStorage ? 'true' : '' })}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('virt.definitionDeleted', { name }) })
      await api('/inventory/refresh', { method: 'POST' })
      await vms.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('virt.title')}
            <InfoHint>{t('virt.hint')}</InfoHint>
          </h1>
        </div>
        <div className="row">
          {me.is_admin && (
            <Button onClick={() => void rescan(t('common.hostRescanned'))} loading={rescanning}>
              {rescanning ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={vms.error} />
      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card
        title={t('virt.domains')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setChooserOpen(true)}>
              {t('virt.newVm')}
            </Button>
          )
        }
      >
        {vms.loading && !vms.data ? (
          <Loading what={t('virt.loading')} />
        ) : allVMs.length === 0 ? (
          <p className="small muted">{t('virt.none')}</p>
        ) : (
          // Все домены одним списком: остановленная машина — это не шум
          // вроде неустановленной службы, а та же машина, которую сейчас
          // и надо запустить, переименовать или удалить. Чипсами она
          // теряла и состояние, и все действия над собой.
          <div className="table-wrap">
            <DataTable<VirtualMachine>
              dataSource={allVMs}
              rowKey="name"
              columns={vmColumns(canControl, busy, act, toggleAutostart, del, (name) => setBackupFor(name), (name) => setConsoleFor(name), (name) => setScreenFor(name), (name) => setEditing(name))}
            />
          </div>
        )}
      </Card>

      {chooserOpen && (
        <VMCreateChooser
          onClose={() => setChooserOpen(false)}
          onReady={(name, initialContent) => {
            setChooserOpen(false)
            setCreating({ name, initialContent })
          }}
        />
      )}

      {/* Заготовки, из которых машины и появляются: образы, шаблоны,
          файлы дисков хоста и его сети. Раньше они жили в «Профилях» —
          рядом с описаниями желаемого состояния, к которым отношения не
          имеют. */}
      <VMImagesSection me={me} />

      {consoleFor && <ConsoleModal kind="vm" name={consoleFor} onClose={() => setConsoleFor(null)} />}
      {screenFor && (
        <VMScreenModal
          name={screenFor}
          graphics={vms.data?.vms.find((v) => v.name === screenFor)?.graphics}
          onClose={() => setScreenFor(null)}
          onAddVNC={() => {
            setEditPrefill({ edit: addVNCGraphics, intro: t('screen.addVNCIntro') })
            setEditing(screenFor)
            setScreenFor(null)
          }}
        />
      )}
      {backupFor && (
        <BackupModal kind="vm" name={backupFor} canControl={canControl} onClose={() => setBackupFor(null)} onRestored={() => vms.reload()} />
      )}
      {editing && (
        <VMEditor
          name={editing}
          me={me}
          edit={editPrefill?.edit}
          intro={editPrefill?.intro}
          onClose={() => {
            setEditing(null)
            setEditPrefill(null)
          }}
          onSaved={() => {
            setEditing(null)
            setEditPrefill(null)
            vms.reload()
          }}
        />
      )}

      {creating && (
        <VMEditor
          name={creating.name}
          initialContent={creating.initialContent}
          me={me}
          onClose={() => setCreating(null)}
          onSaved={() => {
            setCreating(null)
            vms.reload()
          }}
        />
      )}
    </>
  )
}

const domainNameRe = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/** Name + a choice of how to get to the XML review step: a short guided form
 * (memory/vCPU/disk/network, with an optional real qcow2 file created to
 * match) or straight to a blank skeleton for hand-editing. Either path lands
 * on the same VMEditor — the wizard only changes what's pre-filled there,
 * never skips the review-before-save step. */
function VMCreateChooser({
  onClose,
  onReady,
}: {
  onClose: () => void
  onReady: (name: string, initialContent?: string) => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'wizard' | 'raw'>('wizard')
  const [name, setName] = useState('')
  const [memoryMB, setMemoryMB] = useState(2048)
  const [vcpus, setVcpus] = useState(2)
  const [diskGB, setDiskGB] = useState(20)
  const [bridge, setBridge] = useState('br0')
  const [createDiskFile, setCreateDiskFile] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const diskPath = defaultDiskPath(name || 'new-vm')

  async function submit() {
    if (!domainNameRe.test(name)) {
      setError(t('virt.invalidName'))
      return
    }
    if (mode === 'raw') {
      onReady(name)
      return
    }
    setError(null)
    setBusy(true)
    try {
      if (createDiskFile) {
        await api('/vms/disks', { method: 'POST', body: { path: diskPath, size_gb: diskGB } })
      }
      onReady(name, domainXMLFromWizard(name, { memoryMB, vcpus, diskPath, bridge }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={
        <>
          {t('virt.newVmTitle')}
          <InfoHint>{t('virt.newVmHint')}</InfoHint>
        </>
      }
      onClose={onClose}
      width={760}
      maskClosable={false}
    >
      <Form layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <Segmented
          value={mode}
          onChange={(v) => setMode(v as 'wizard' | 'raw')}
          options={[
            { value: 'wizard', label: t('virt.wizard') },
            { value: 'raw', label: t('virt.rawXml') },
          ]}
          style={{ marginBottom: '0.75rem' }}
        />
        <Form.Item label={t('virt.vmName')} style={{ maxWidth: '20rem' }}>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="new-vm" autoFocus />
        </Form.Item>

        {mode === 'wizard' && (
          <>
            <div className="filters">
              <Form.Item label={t('virt.memoryMb')} style={{ minWidth: '10rem' }}>
                <InputNumber min={256} step={256} value={memoryMB} onChange={(v) => setMemoryMB(v ?? 256)} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label="vCPU" style={{ minWidth: '8rem' }}>
                <InputNumber min={1} max={64} value={vcpus} onChange={(v) => setVcpus(v ?? 1)} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label={t('virt.diskGb')} style={{ minWidth: '8rem' }}>
                <InputNumber min={1} max={65536} value={diskGB} onChange={(v) => setDiskGB(v ?? 1)} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item label={t('virt.networkBridge')} style={{ minWidth: '10rem' }}>
                <Input value={bridge} onChange={(e) => setBridge(e.target.value)} placeholder="br0" />
              </Form.Item>
            </div>
            <p className="small muted" style={{ margin: 0 }}>
              {t('virt.diskFile')}
              <code className="mono">{diskPath}</code>
            </p>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem', marginTop: '0.5rem' }}>
              <Checkbox checked={createDiskFile} onChange={(e) => setCreateDiskFile(e.target.checked)} />
              {t('virt.createDiskFile')}
            </label>
          </>
        )}

        <Form.Item style={{ marginTop: '0.75rem', marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? t('virt.preparing') : t('virt.nextReviewXml')}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}

/** Creates or edits a domain's XML definition through the same validated
 * write path every other config file already uses — see
 * ConfigManager.serviceForPath/Validate/Write's apply step for
 * model.ServiceLibvirt. No dedicated create/edit API exists for VMs because
 * none is needed. */
function VMEditor({
  name,
  initialContent,
  me,
  onClose,
  onSaved,
  edit,
  intro,
}: {
  name: string
  me: Me
  /** Заготовка черновика из текущего XML (добавить VNC). */
  edit?: (xml: string) => string
  intro?: string
  /** Pre-filled by the wizard; falls back to the plain skeleton when the
   * operator went the raw-XML route instead. */
  initialContent?: string
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const path = `/etc/libvirt/qemu/${name}.xml`
  const existing = useApi<FileContent>(`/configs/file${qs({ path })}`)
  const isNew = existing.error !== null
  const [draft, setDraft] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [apply, setApply] = useState(true)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<WriteResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Дифф «на диске → черновик», который надо подтвердить перед записью:
  // define применяется к работающей машине, и «применить» должно
  // нажиматься глядя на изменения, а не на весь XML.
  const [preview, setPreview] = useState<string | null>(null)
  // «Блоки» — тот же BlockTree, что у nginx/haproxy/docker: элементы
  // домена и устройства по одному. У новой машины файла ещё нет — только
  // текст.
  const [tab, setTab] = useState<'text' | 'blocks' | 'history'>('text')

  const content = draft ?? (existing.data ? (edit ? edit(existing.data.content) : existing.data.content) : isNew ? initialContent ?? domainXMLSkeleton(name) : '')

  async function save() {
    // Новой машине сравнивать не с чем — сразу запись.
    if (isNew) return doSave()
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ changed: boolean; diff: string }>('/configs/preview-diff', {
        method: 'POST',
        body: { path, content },
      })
      if (!res.changed) {
        setError(t('virt.noChanges'))
        return
      }
      setPreview(res.diff)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function doSave() {
    setPreview(null)
    setBusy(true)
    setError(null)
    setResult(null)
    try {
      const res = await api<WriteResult>('/configs/file', {
        method: 'PUT',
        body: {
          path,
          content,
          note: note || t(isNew ? 'virt.createNote' : 'virt.editNote'),
          apply,
          expected_sha256: existing.data?.sha256 ?? '',
        },
      })
      setResult(res)
      if (!res.rolled_back) onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Окном, а не карточкой: карточка рисовалась под списком машин и
  // каталогом образов, и после нажатия карандаша казалось, что ничего не
  // открылось.
  return (
    <Modal title={t(isNew ? 'virt.newVmName' : 'virt.editVmName', { name })} onClose={onClose} width={960} maskClosable={false} sizeKey="edit">
      <div className="small muted mono" style={{ marginBottom: '0.5rem' }}>
        {path}
      </div>
      {existing.loading && !isNew ? (
        <Loading what={t('virt.loadingDefinition')} />
      ) : (
        <div className="col">
          {!isNew && (
            <Segmented
              value={tab}
              onChange={(v) => setTab(v as 'text' | 'blocks' | 'history')}
              options={[
                { value: 'text', label: t('configs.text') },
                { value: 'blocks', label: t('configs.blocks') },
                { value: 'history', label: t('configs.versionHistoryTitle') },
              ]}
            />
          )}
          {tab === 'history' && !isNew ? (
            <VersionHistory path={path} me={me} apply onChanged={() => existing.reload()} />
          ) : tab === 'blocks' && !isNew && existing.data ? (
            <BlockTree
              path={path}
              service="libvirt"
              sha256={existing.data.sha256}
              me={me}
              onSaved={() => {
                existing.reload()
                onSaved()
              }}
            />
          ) : (
            <>
          {intro && <Banner kind="info">{intro}</Banner>}
          {error && (
            <Banner kind="error">
              {error}
              {error !== t('virt.noChanges') && <AIConfigError path={path} service="libvirt" content={content} message={error} />}
            </Banner>
          )}
          {result && (
            <Banner kind={result.rolled_back ? 'error' : 'info'}>
              {result.message}
              {result.rolled_back && <AIConfigError path={path} service="libvirt" content={content} result={result} />}
            </Banner>
          )}
          <CodeEditor value={content} onChange={(e) => setDraft(e.target.value)} rows={20} fill />
          <label>
            {t('virt.note')}
            <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('virt.optional')} />
          </label>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
            <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
            {t('virt.applyVirshDefine')}
          </label>
          <div>
            <Button type="primary" onClick={() => void save()} loading={busy}>
              {busy ? t('virt.saving') : t('virt.save')}
            </Button>
          </div>
            </>
          )}
          {preview !== null && (
            <Modal title={t('virt.reviewChanges', { name })} onClose={() => setPreview(null)} width={900} maskClosable={false}>
              <div className="small muted">{apply ? t('virt.reviewChangesApply') : t('virt.reviewChangesNoApply')}</div>
              <DiffView text={preview} />
              <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
                <Button type="primary" onClick={() => void doSave()} loading={busy}>
                  {t('virt.applyChanges')}
                </Button>
                <Button onClick={() => setPreview(null)}>{t('common.cancel')}</Button>
              </div>
            </Modal>
          )}
        </div>
      )}
    </Modal>
  )
}
