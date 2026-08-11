import { FormEvent, useState } from 'react'
import { api, qs, useApi } from '../api'
import type { FileContent, Me, VirtualMachine, WriteResult } from '../types'
import { Banner, Card, CodeEditor, ErrorNote, Loading, Spinner, StateBadge, formatBytesShort } from '../components/ui'

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

export default function Virtualization({ me }: { me: Me }) {
  const vms = useApi<{ vms: VirtualMachine[] }>('/vms', 30_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<{ kind: 'info' | 'error'; text: string } | null>(null)
  const [creating, setCreating] = useState<{ name: string; initialContent?: string } | null>(null)
  const [chooserOpen, setChooserOpen] = useState(false)

  const canControl = me.is_admin && me.allow_mutations

  async function act(name: string, action: string) {
    const label = action === 'destroy' ? 'принудительно завершить' : action
    if (!window.confirm(`Выполнить «${label}» для VM ${name}?`)) return
    setBusy(`${name}:${action}`)
    setNotice(null)
    try {
      await api(`/vms/${name}/${action}`, { method: 'POST' })
      setNotice({ kind: 'info', text: `${name}: ${label} выполнено.` })
      vms.reload()
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
      vms.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function del(name: string, removeStorage: boolean) {
    const warning = removeStorage
      ? `Удалить VM ${name} вместе с дисками? Образы дисков будут удалены безвозвратно.`
      : `Удалить определение VM ${name}? Диски останутся на месте.`
    if (!window.confirm(warning)) return
    setBusy(`${name}:delete`)
    setNotice(null)
    try {
      await api(`/vms/${name}${qs({ remove_storage: removeStorage ? 'true' : '' })}`, { method: 'DELETE' })
      setNotice({ kind: 'info', text: `${name}: определение удалено.` })
      vms.reload()
    } catch (err) {
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Виртуальные машины</h1>
          <p>libvirt/QEMU домены — управление через virsh. Создание и редактирование — через XML-определение.</p>
        </div>
      </div>

      <ErrorNote error={vms.error} />
      {notice && <Banner kind={notice.kind === 'error' ? 'error' : 'info'}>{notice.text}</Banner>}
      {!canControl && (
        <Banner kind="info">Действия недоступны: нужна роль admin и включённые изменения.</Banner>
      )}

      <Card
        title="Домены libvirt"
        actions={
          canControl && (
            <button className="ghost" onClick={() => setChooserOpen(true)}>
              + новая VM
            </button>
          )
        }
      >
        {vms.loading && !vms.data ? (
          <Loading what="виртуальные машины" />
        ) : (vms.data?.vms ?? []).length === 0 ? (
          <p className="small muted">libvirt не обнаружен или доменов нет.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Имя</th>
                  <th>Состояние</th>
                  <th className="num">vCPU</th>
                  <th className="num">Память</th>
                  <th>Диски</th>
                  <th>Сети</th>
                  <th>Автозапуск</th>
                  <th>Действия</th>
                </tr>
              </thead>
              <tbody>
                {(vms.data?.vms ?? []).map((vm) => (
                  <tr key={vm.name}>
                    <td>
                      <strong>{vm.name}</strong>
                      <div className="small muted">{vm.uuid || '—'}</div>
                    </td>
                    <td>
                      <StateBadge state={vm.state} />
                    </td>
                    <td className="num small">{vm.vcpus || '—'}</td>
                    <td className="num small">{vm.memory_kb ? formatBytesShort(vm.memory_kb * 1024) : '—'}</td>
                    <td className="small mono">
                      {(vm.disks ?? []).map((d, i) => (
                        <div key={i}>{d.source || '—'}{d.bus ? ` (${d.bus})` : ''}</div>
                      ))}
                    </td>
                    <td className="small">
                      {(vm.networks ?? []).map((n, i) => <div key={i}>{n.source || '—'}</div>)}
                    </td>
                    <td>
                      {vm.persistent ? (
                        <button
                          className="ghost"
                          disabled={!canControl || busy === `${vm.name}:autostart`}
                          onClick={() => toggleAutostart(vm.name, !vm.autostart)}
                        >
                          {busy === `${vm.name}:autostart` && <Spinner />}
                          {vm.autostart ? 'включён' : 'выключен'}
                        </button>
                      ) : (
                        <span
                          className="small muted"
                          title="Домен временный: не зарегистрирован через virsh define, поэтому у него нет ни сохранённого определения, ни автозапуска. Пропадёт из списка при остановке."
                        >
                          недоступен (временный домен)
                        </span>
                      )}
                    </td>
                    <td className="nowrap">
                      {LIFECYCLE_ACTIONS.map((a) => (
                        <button
                          key={a}
                          className="ghost"
                          disabled={!canControl || busy === `${vm.name}:${a}`}
                          onClick={() => act(vm.name, a)}
                        >
                          {busy === `${vm.name}:${a}` && <Spinner />}
                          {a}
                        </button>
                      ))}
                      {canControl && (
                        <button
                          className="ghost"
                          disabled={busy === `${vm.name}:destroy`}
                          onClick={() => act(vm.name, 'destroy')}
                          title="Немедленное принудительное отключение — как выдернуть шнур питания"
                        >
                          {busy === `${vm.name}:destroy` && <Spinner />}
                          завершить принудительно
                        </button>
                      )}
                      {canControl && vm.persistent && (
                        <>
                          <button
                            className="ghost"
                            disabled={busy === `${vm.name}:delete`}
                            onClick={() => del(vm.name, false)}
                          >
                            удалить
                          </button>
                          <button
                            className="ghost"
                            disabled={busy === `${vm.name}:delete`}
                            onClick={() => del(vm.name, true)}
                          >
                            удалить с дисками
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
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

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!domainNameRe.test(name)) {
      setError('Имя может содержать только латинские буквы, цифры, точку, дефис и подчёркивание')
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
      title="Новая VM"
      subtitle="Оба способа заканчиваются одним и тем же — проверкой и правкой XML перед сохранением."
      actions={
        <button className="ghost" onClick={onClose}>
          закрыть
        </button>
      }
    >
      <form className="col" onSubmit={submit}>
        {error && <Banner kind="error">{error}</Banner>}
        <div className="row" style={{ gap: '0.5rem' }}>
          <button
            type="button"
            className={mode === 'wizard' ? 'primary' : 'ghost'}
            onClick={() => setMode('wizard')}
          >
            Мастер
          </button>
          <button type="button" className={mode === 'raw' ? 'primary' : 'ghost'} onClick={() => setMode('raw')}>
            Сырой XML
          </button>
        </div>
        <label style={{ maxWidth: '20rem' }}>
          Имя VM
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="new-vm" required autoFocus />
        </label>

        {mode === 'wizard' && (
          <>
            <div className="filters">
              <label style={{ minWidth: '10rem' }}>
                Память, МБ
                <input
                  type="number"
                  min={256}
                  step={256}
                  value={memoryMB}
                  onChange={(e) => setMemoryMB(Number(e.target.value))}
                />
              </label>
              <label style={{ minWidth: '8rem' }}>
                vCPU
                <input type="number" min={1} max={64} value={vcpus} onChange={(e) => setVcpus(Number(e.target.value))} />
              </label>
              <label style={{ minWidth: '8rem' }}>
                Диск, ГБ
                <input type="number" min={1} max={65536} value={diskGB} onChange={(e) => setDiskGB(Number(e.target.value))} />
              </label>
              <label style={{ minWidth: '10rem' }}>
                Сетевой мост
                <input value={bridge} onChange={(e) => setBridge(e.target.value)} placeholder="br0" />
              </label>
            </div>
            <p className="small muted" style={{ margin: 0 }}>
              Файл диска: <code className="mono">{diskPath}</code>
            </p>
            <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
              <input type="checkbox" checked={createDiskFile} onChange={(e) => setCreateDiskFile(e.target.checked)} />
              Создать файл диска (qemu-img create) — без этого XML будет ссылаться на несуществующий файл
            </label>
          </>
        )}

        <div>
          <button className="primary" type="submit" disabled={busy}>
            {busy && <Spinner />}
            {busy ? 'Готовлю…' : 'Далее — проверить XML'}
          </button>
        </div>
      </form>
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
          note: note || (isNew ? 'создание VM' : 'правка определения VM'),
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
      title={isNew ? `Новая VM: ${name}` : `Редактирование VM: ${name}`}
      subtitle={path}
      actions={
        <button className="ghost" onClick={onClose}>
          закрыть
        </button>
      }
    >
      {existing.loading && !isNew ? (
        <Loading what="определение VM" />
      ) : (
        <div className="col">
          {error && <Banner kind="error">{error}</Banner>}
          {result && (
            <Banner kind={result.rolled_back ? 'error' : 'info'}>{result.message}</Banner>
          )}
          <CodeEditor value={content} onChange={(e) => setDraft(e.target.value)} rows={20} />
          <label>
            Заметка
            <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="необязательно" />
          </label>
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }}>
            <input type="checkbox" checked={apply} onChange={(e) => setApply(e.target.checked)} />
            Применить (virsh define) — без этого XML сохраняется, но домен не регистрируется
          </label>
          <div>
            <button className="primary" onClick={save} disabled={busy}>
              {busy && <Spinner />}
              {busy ? 'Сохраняю…' : 'Сохранить'}
            </button>
          </div>
        </div>
      )}
    </Card>
  )
}
