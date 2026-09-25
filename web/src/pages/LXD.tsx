import { useState } from 'react'
import { Button, Form, Input, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { useHostRescan } from '../rescan'
import { api, useApi } from '../api'
import type { LXDInstance, Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading, Modal, StateBadge, formatBytesShort } from '../components/ui'
import { InactiveSummary } from '../components/InactiveSummary'
import { confirmAction } from '../components/confirm'
import { DataTable } from '../components/DataTable'
import { RowAction } from '../components/RowAction'
import { PowerToggle, containerPowerState } from '../components/PowerToggle'
import { EngineInstallBanner } from '../components/EngineInstallBanner'
import { ConsoleModal } from '../components/ConsoleModal'
import LXDLogsModal from '../components/LXDLogsModal'
import { BackupModal } from '../components/BackupModal'
import LXDSnapshotsModal from '../components/LXDSnapshotsModal'
import LXDConfigModal, { removeYamlDevice } from '../components/LXDConfigModal'
import LXDPortModal from '../components/LXDPortModal'
import { SpiceModal } from '../components/SpiceModal'
import { useJobLauncher } from '../components/useJobLauncher'
import { LXDResources } from '../components/LXDResources'
import { ProbeLink } from '../components/PortProbe'
import { CheckCircleFilled, CloseCircleOutlined } from '@ant-design/icons'
import { LXDImagePicker } from '../components/LXDImagePicker'

export default function LXD({ me }: { me: Me }) {
  const { t } = useTranslation()
  const instances = useApi<{ instances: LXDInstance[] }>('/lxd/instances', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState(false)
  const [consoleFor, setConsoleFor] = useState<string | null>(null)
  const [logsFor, setLogsFor] = useState<string | null>(null)
  const [backupFor, setBackupFor] = useState<string | null>(null)
  const [snapsFor, setSnapsFor] = useState<LXDInstance | null>(null)
  const [configFor, setConfigFor] = useState<{ name: string; edit?: (saved: string) => string; intro?: string } | null>(null)
  const [portFor, setPortFor] = useState<string | null>(null)
  const [screenFor, setScreenFor] = useState<string | null>(null)
  // Создание — фоновым заданием: окно журнала живёт на странице, а не в
  // форме, чтобы пережить её закрытие.
  const launcher = useJobLauncher(() => void api('/inventory/refresh', { method: 'POST' }).then(() => instances.reload()))

  async function toggleAutostart(name: string, on: boolean) {
    setBusy(`${name}:autostart`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}/autostart`, { method: 'POST', body: { on } })
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const canControl = me.is_admin && me.allow_mutations
  // Раздел показывает снимок инвентаря: при входе он пересобирается сам,
  // иначе только что поднятого контейнера в списке не окажется.
  const { rescanning, rescan } = useHostRescan({
    reload: () => instances.reload(),
    canScan: canControl,
    onNotice: (kind, text) => setNotice({ kind, text }),
  })
  const allInstances = instances.data?.instances ?? []
  const activeInstances = allInstances.filter((i) => i.status.toLowerCase() === 'running')
  const inactiveInstances = allInstances.filter((i) => i.status.toLowerCase() !== 'running')


  async function act(name: string, action: string) {
    if (!(await confirmAction(t('lxd.confirmAction', { action, name })))) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: t('lxd.actionDone', { name, action }) })
      // The backend only kicks off a fire-and-forget background rescan
      // (rescanLater) — a bare reload() right after would just reread the
      // still-stale cached snapshot. /inventory/refresh runs the same
      // rescan synchronously, same as "Пересканировать" below.
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string) {
    if (!(await confirmAction(t('common.confirmDelete', { what: t('lxd.instance'), name })))) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/lxd/instances/${name}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: t('common.deleted', { name }) })
      await api('/inventory/refresh', { method: 'POST' })
      await instances.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  const columns: TableColumnsType<LXDInstance> = [
    { title: t('lxd.colName'), key: 'name', render: (_, i) => <strong>{i.name}</strong> },
    { title: t('lxd.colType'), key: 'type', render: (_, i) => <span className="small">{i.type === 'virtual-machine' ? t('lxd.vm') : t('lxd.container')}</span> },
    { title: t('lxd.colState'), key: 'status', render: (_, i) => <StateBadge state={i.status} /> },
    { title: t('lxd.colArch'), key: 'architecture', render: (_, i) => <span className="small mono">{i.architecture || '—'}</span> },
    { title: 'IPv4', key: 'ipv4', render: (_, i) => <span className="small mono">{(i.ipv4 ?? []).join(', ') || '—'}</span> },
    {
      // Потребление — сейчас; лимиты — из конфигурации (с учётом профилей).
      title: t('lxd.colResources'),
      key: 'resources',
      render: (_, i) => (
        <span className="small nowrap">
          {i.memory_bytes ? `${formatBytesShort(i.memory_bytes)}${i.limit_memory ? ` / ${i.limit_memory}` : ''}` : i.limit_memory ? `— / ${i.limit_memory}` : '—'}
          <div className="muted">
            {i.limit_cpu ? `CPU ${i.limit_cpu}` : 'CPU —'}
            {i.disk_bytes ? ` · ${t('lxd.disk')} ${formatBytesShort(i.disk_bytes)}` : ''}
          </div>
        </span>
      ),
    },
    {
      title: t('lxd.colPorts'),
      key: 'ports',
      render: (_, i) => (
        <span className="small mono">
          {canControl && (
            <RowAction action="add" label={t('lxdPort.add')} onClick={() => setPortFor(i.name)} />
          )}
          {(i.ports ?? []).length === 0
            ? canControl
              ? null
              : '—'
            : (i.ports ?? []).map((p) => {
                const [, host, port] = /^tcp:(.*):(\d+)$/.exec(p.listen) ?? []
                return (
                  <div key={p.device}>
                    {p.listen.replace(/^tcp:/, '')} → {p.connect.replace(/^tcp:/, '')}
                    {port && <ProbeLink address={host || '0.0.0.0'} port={Number(port)} protocol="tcp" />}
                    {canControl && (
                      <RowAction
                        action="delete"
                        danger
                        label={t('lxdPort.remove', { device: p.device })}
                        onClick={() =>
                          setConfigFor({ name: i.name, edit: (s) => removeYamlDevice(s, p.device), intro: t('lxdPort.removeIntro', { device: p.device }) })
                        }
                      />
                    )}
                  </div>
                )
              })}
        </span>
      ),
    },
    {
      title: t('virt.colAutostart'),
      key: 'autostart',
      render: (_, i) => (
        <RowAction
          icon={i.autostart ? <CheckCircleFilled style={{ color: 'var(--status-good)' }} /> : <CloseCircleOutlined style={{ color: 'var(--text-muted)' }} />}
          label={`${t('virt.colAutostart')}: ${i.autostart ? t('virt.autostartOn') : t('virt.autostartOff')} — ${i.autostart ? t('vmnet.autostartOff') : t('vmnet.autostartOn')}`}
          disabled={!canControl}
          loading={busy === `${i.name}:autostart`}
          onClick={() => void toggleAutostart(i.name, !i.autostart)}
        />
      ),
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_, i) => (
        <div className="row">
          <PowerToggle
            state={containerPowerState(i.status)}
            labels={{ start: t('docker.action.start', { defaultValue: 'start' }), stop: t('docker.action.stop', { defaultValue: 'stop' }) }}
            disabled={!canControl}
            loading={busy === `${i.name}:start` || busy === `${i.name}:stop`}
            onStart={() => act(i.name, 'start')}
            onStop={() => act(i.name, 'stop')}
            onResume={() => act(i.name, 'start')}
          />
          {containerPowerState(i.status) === 'running' &&
            ['restart', 'pause'].map((a) => (
              <RowAction
                key={a}
                action={a === 'pause' ? 'suspend' : a}
                label={t(`docker.action.${a}`, { defaultValue: a })}
                disabled={!canControl}
                loading={busy === `${i.name}:${a}`}
                onClick={() => act(i.name, a)}
              />
            ))}
          <RowAction action="log" label={t('docker.logs')} onClick={() => setLogsFor(i.name)} />
          <RowAction action="edit" label={t('lxdConfig.action')} onClick={() => setConfigFor({ name: i.name })} />
          <RowAction
            action="snapshot"
            label={`${t('lxdSnap.action')}${i.snapshots ? ` (${i.snapshots})` : ''}`}
            onClick={() => setSnapsFor(i)}
          />
          <RowAction action="backup" label={t('backups.action')} onClick={() => setBackupFor(i.name)} />
          {canControl && containerPowerState(i.status) === 'running' && (
            <RowAction action="console" label={t('console.action')} onClick={() => setConsoleFor(i.name)} />
          )}
          {canControl && i.type === 'virtual-machine' && containerPowerState(i.status) === 'running' && (
            <RowAction action="screen" label={t('screen.action')} onClick={() => setScreenFor(i.name)} />
          )}
          {canControl && (
            <RowAction
              action="delete"
              label={t('common.delete')}
              danger
              loading={busy === `${i.name}:delete`}
              onClick={() => del(i.name)}
            />
          )}
        </div>
      ),
    },
  ]

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            LXD
            <InfoHint>{t('lxd.hint')}</InfoHint>
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

      <EngineInstallBanner service="lxd" canControl={canControl} onInstalled={() => instances.reload()} />
      <ErrorNote error={instances.error} />
      {notice && (
        <Banner kind={notice.kind === 'error' ? 'error' : 'info'} onClose={() => setNotice(null)}>
          {notice.text}
        </Banner>
      )}
      {!canControl && <Banner kind="info">{t('common.mutationsDisabled')}</Banner>}

      <Card
        title={t('lxd.instances')}
        actions={
          canControl && (
            <Button type="link" onClick={() => setCreating(true)}>
              {t('lxd.newInstance')}
            </Button>
          )
        }
      >
        {instances.loading && !instances.data ? (
          <Loading what={t('lxd.loading')} />
        ) : allInstances.length === 0 ? (
          <p className="small muted">{t('lxd.none')}</p>
        ) : (
          <>
            <InactiveSummary
              items={inactiveInstances}
              getKey={(i) => i.name}
              getLabel={(i) => i.name}
              getTooltip={(i) => (
                <>
                  <div>{i.type === 'virtual-machine' ? t('lxd.vm') : t('lxd.container')}</div>
                  <div>{t('lxd.state', { state: i.status })}</div>
                </>
              )}
              onRescan={rescan}
              rescanning={rescanning}
            />
            <div className="table-wrap">
              <DataTable<LXDInstance>                 dataSource={activeInstances}
                columns={columns}
                rowKey="name"
              />
            </div>
          </>
        )}
      </Card>

      <LXDResources canControl={canControl} />

      {creating && (
        <CreateInstanceForm
          onClose={() => setCreating(false)}
          onSubmit={async (values) => {
            await launcher.start('/lxd/instances', values)
            setCreating(false)
          }}
        />
      )}
      {consoleFor && <ConsoleModal kind="lxd" name={consoleFor} onClose={() => setConsoleFor(null)} />}
      {logsFor && <LXDLogsModal name={logsFor} onClose={() => setLogsFor(null)} />}
      {launcher.modal}
      {configFor && (
        <LXDConfigModal
          key={configFor.name + (configFor.intro ?? '')}
          name={configFor.name}
          edit={configFor.edit}
          intro={configFor.intro && <Banner kind="info">{configFor.intro}</Banner>}
          me={me}
          canControl={canControl}
          onClose={() => setConfigFor(null)}
          onSaved={() => void api('/inventory/refresh', { method: 'POST' }).then(() => instances.reload())}
        />
      )}
      {screenFor && (
        <SpiceModal
          title={t('vnc.title', { name: screenFor })}
          wsPath={`/lxd/instances/${encodeURIComponent(screenFor)}/spice/ws`}
          onClose={() => setScreenFor(null)}
        />
      )}
      {portFor && (
        <LXDPortModal
          name={portFor}
          onClose={() => setPortFor(null)}
          onContinue={(edit) => {
            setConfigFor({ name: portFor, edit, intro: t('lxdPort.addIntro') })
            setPortFor(null)
          }}
        />
      )}
      {snapsFor && (
        <LXDSnapshotsModal
          name={snapsFor.name}
          isVM={snapsFor.type === 'virtual-machine'}
          canControl={canControl}
          onClose={() => setSnapsFor(null)}
          onChanged={() => void api('/inventory/refresh', { method: 'POST' }).then(() => instances.reload())}
        />
      )}
      {backupFor && (
        <BackupModal kind="lxd" name={backupFor} canControl={canControl} onClose={() => setBackupFor(null)} onRestored={() => instances.reload()} />
      )}
    </>
  )
}

type CreateInstanceValues = { image: string; name: string; vm?: boolean }

function CreateInstanceForm({ onClose, onSubmit }: { onClose: () => void; onSubmit: (values: CreateInstanceValues & { vm: boolean }) => Promise<void> }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [vm, setVM] = useState(false)
  const [form] = Form.useForm<CreateInstanceValues>()

  async function submit(values: CreateInstanceValues) {
    setBusy(true)
    setError(null)
    try {
      await onSubmit({ ...values, vm })
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
          {t('lxd.newInstanceTitle')}
          <InfoHint>{t('lxd.newInstanceHint')}</InfoHint>
        </>
      }
      onClose={onClose}
      width={760}
      maskClosable={false}
    >
      <Form<CreateInstanceValues> form={form} layout="vertical" onFinish={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="filters">
          <Form.Item name="image" label={t('lxd.image')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '100%' }}>
            <LXDImagePicker
              onChange={(ref, isVM) => {
                form.setFieldValue('image', ref)
                setVM(isVM)
              }}
            />
          </Form.Item>
          <Form.Item name="name" label={t('lxd.instanceName')} rules={[{ required: true }]} style={{ flex: 1, minWidth: '12rem' }}>
            <Input placeholder="my-instance" />
          </Form.Item>
        </div>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={busy}>
            {busy ? t('lxd.launching') : t('lxd.createAndStart')}
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  )
}
