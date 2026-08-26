import { useState } from 'react'
import { Button, Checkbox, Form, Input, InputNumber, Segmented, Table, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { FileContent, Me, VirtualMachine, WriteResult } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, InfoHint, Loading, StateBadge, formatBytesShort } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import i18n from '../i18n'

const LIFECYCLE_ACTIONS = ['start', 'shutdown', 'reboot', 'suspend', 'resume']

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
  del: (name: string, removeStorage: boolean) => void,
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
          <Button
            type="link"
            size="small"
            disabled={!canControl}
            loading={busy === `${vm.name}:autostart`}
            onClick={() => toggleAutostart(vm.name, !vm.autostart)}
          >
            {vm.autostart ? t('virt.autostartOn') : t('virt.autostartOff')}
          </Button>
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
          {LIFECYCLE_ACTIONS.map((a) => (
            <Button
              key={a}
              type="link"
              size="small"
              disabled={!canControl}
              loading={busy === `${vm.name}:${a}`}
              onClick={() => act(vm.name, a)}
            >
              {a}
            </Button>
          ))}
          {canControl && (
            <Button
              danger
              type="link"
              size="small"
              loading={busy === `${vm.name}:destroy`}
              onClick={() => act(vm.name, 'destroy')}
              title={t('virt.forceDestroyTooltip')}
            >
              {t('virt.forceDestroy')}
            </Button>
          )}
          {canControl && vm.persistent && (
            <>
              <Button danger type="link" size="small" loading={busy === `${vm.name}:delete`} onClick={() => del(vm.name, false)}>
                {t('common.delete')}
              </Button>
              <Button danger type="link" size="small" loading={busy === `${vm.name}:delete`} onClick={() => del(vm.name, true)}>
                {t('virt.deleteWithDisks')}
              </Button>
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
  const [rescanning, setRescanning] = useState(false)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState<{ name: string; initialContent?: string } | null>(null)
  const [chooserOpen, setChooserOpen] = useState(false)

  const canControl = me.is_admin && me.allow_mutations
  const allVMs = vms.data?.vms ?? []
  const activeVMs = allVMs.filter((vm) => vm.state === 'running')
  const inactiveVMs = allVMs.filter((vm) => vm.state !== 'running')

  async function rescan() {
    setRescanning(true)
    setNotice(null)
    try {
      await api('/inventory/refresh', { method: 'POST' })
      await vms.reload()
      setNotice({ kind: 'info', text: t('common.hostRescanned') })
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setRescanning(false)
    }
  }

  async function act(name: string, action: string) {
    const label = action === 'destroy' ? t('virt.forceDestroy') : action
    if (!window.confirm(t('virt.confirmAction', { action: label, name }))) return
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

  async function del(name: string, removeStorage: boolean) {
    const warning = t(removeStorage ? 'virt.confirmDeleteWithDisks' : 'virt.confirmDeleteDefinition', { name })
    if (!window.confirm(warning)) return
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
            <Button onClick={rescan} loading={rescanning}>
              {rescanning ? t('common.scanning') : t('common.rescan')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={vms.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
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
          <>
            <InactiveSummary
              items={inactiveVMs}
              getKey={(vm) => vm.name}
              getLabel={(vm) => vm.name}
              getTooltip={(vm) => (
                <>
                  <div>{t('virt.state', { state: vm.state })}</div>
                  {vm.vcpus ? <div>vCPU: {vm.vcpus}</div> : null}
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
            />
            <div className="table-wrap">
              <Table<VirtualMachine>
                dataSource={activeVMs}
                rowKey="name"
                pagination={false}
                size="small"
                columns={vmColumns(canControl, busy, act, toggleAutostart, del)}
              />
            </div>
          </>
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

      {creating && (
        <VMEditor
          name={creating.name}
          initialContent={creating.initialContent}
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
    <Card
      title={
        <>
          {t('virt.newVmTitle')}
          <InfoHint>{t('virt.newVmHint')}</InfoHint>
        </>
      }
      actions={
        <Button type="link" onClick={onClose}>
          {t('common.close')}
        </Button>
      }
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
    </Card>
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
  onClose,
  onSaved,
}: {
  name: string
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

  const content = draft ?? existing.data?.content ?? (isNew ? initialContent ?? domainXMLSkeleton(name) : '')

  async function save() {
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

  return (
    <Card
      title={t(isNew ? 'virt.newVmName' : 'virt.editVmName', { name })}
      subtitle={path}
      actions={
        <Button type="link" onClick={onClose}>
          {t('common.close')}
        </Button>
      }
    >
      {existing.loading && !isNew ? (
        <Loading what={t('virt.loadingDefinition')} />
      ) : (
        <div className="col">
          {error && <Banner kind="error">{error}</Banner>}
          {result && (
            <Banner kind={result.rolled_back ? 'error' : 'info'}>{result.message}</Banner>
          )}
          <CodeEditor value={content} onChange={(e) => setDraft(e.target.value)} rows={20} />
          <label>
            {t('virt.note')}
            <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('virt.optional')} />
          </label>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
            <Checkbox checked={apply} onChange={(e) => setApply(e.target.checked)} />
            {t('virt.applyVirshDefine')}
          </label>
          <div>
            <Button type="primary" onClick={save} loading={busy}>
              {busy ? t('virt.saving') : t('virt.save')}
            </Button>
          </div>
        </div>
      )}
    </Card>
  )
}
