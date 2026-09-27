import { useState } from 'react'
import { Button, Tag } from 'antd'
import { CodeOutlined } from '@ant-design/icons'
import { useTranslation } from 'react-i18next'
import { api, apiURL, hostScope, readSelectedHost, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, Loading } from '../components/ui'
import { K8sObjects } from '../components/K8sObjects'
import { DataTable } from '../components/DataTable'
import { confirmAction } from '../components/confirm'

/**
 * Вкладка Kubernetes на хосте (internal/k8s): что стоит, узлы кластера и
 * поды — только чтение через kubectl на control plane. Управление
 * кластером (создать, добавить узлы, удалить) — на хабе.
 */

interface K8sStatus {
  installed: boolean
  flavor?: string
  role?: string
  version?: string
  active: boolean
}

interface K8sNode {
  name: string
  roles: string
  ready: boolean
  version: string
  internal_ip: string
  age: string
}

export default function Kubernetes({ me }: { me: Me }) {
  const { t } = useTranslation()
  const status = useApi<{ status: K8sStatus; nodes?: K8sNode[]; nodes_error?: string }>('/k8s', 20_000)
  const st = status.data?.status
  const isServer = !!st?.installed && st.role === 'server' && st.active
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  if (status.loading && !status.data) return <Loading what="Kubernetes" />
  if (!st?.installed) return <p className="small muted">{t('k8s.notInstalled')}</p>

  async function uninstall() {
    if (!(await confirmAction(t('k8s.confirmUninstall')))) return
    setBusy(true)
    try {
      await api('/k8s/uninstall', { method: 'POST' })
      setNotice(t('k8s.uninstalled'))
      await status.reload()
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="col" style={{ gap: '0.6rem' }}>
      <Card
        title="Kubernetes"
        subtitle={t('k8s.summary', { flavor: st.flavor, role: st.role === 'server' ? t('k8s.roleServer') : t('k8s.roleAgent'), version: st.version || '?' })}
        actions={
          <div className="row" style={{ gap: '0.3rem' }}>
            {isServer && (
              <a href={apiURL('/k8s/kubeconfig')} download="kubeconfig.yaml">
                <Button size="small">kubeconfig</Button>
              </a>
            )}
            {isServer && me.is_admin && (
              <Button
                size="small"
                icon={<CodeOutlined />}
                title={t('clusters.kubectlHint')}
                onClick={() => {
                  // Откреплённый терминал с kubectl на этом узле (?kubectl=1).
                  const params = new URLSearchParams({ kubectl: '1' })
                  if (hostScope.id !== null) params.set('host', String(hostScope.id))
                  const name = readSelectedHost()?.name
                  if (name) params.set('name', name)
                  window.open(`/terminal/popout?${params.toString()}`, `nkt-kubectl-host-${hostScope.id ?? 'local'}`, 'width=980,height=640,resizable=yes')
                }}
              >
                kubectl
              </Button>
            )}
            {me.is_admin && me.allow_mutations && (
              <Button size="small" danger loading={busy} onClick={() => void uninstall()}>
                {t('k8s.uninstall')}
              </Button>
            )}
          </div>
        }
      >
        {notice && <Banner kind="info" onClose={() => setNotice(null)}>{notice}</Banner>}
        <ErrorNote error={status.error} />
        {!st.active && <Banner kind="warn">{t('k8s.inactive')}</Banner>}
        {status.data?.nodes_error && <Banner kind="error">{status.data.nodes_error}</Banner>}
        {isServer && status.data?.nodes && (
          <div className="table-wrap">
            <DataTable<K8sNode>
              dataSource={status.data.nodes}
              rowKey="name"
              size="small"
              pagination={false}
              columns={[
                { title: t('k8s.colNode'), dataIndex: 'name', key: 'name', render: (v: string) => <span className="mono">{v}</span> },
                { title: t('k8s.colRoles'), dataIndex: 'roles', key: 'roles', render: (v: string) => v.split(',').map((r) => <Tag key={r} color={r === 'control-plane' || r === 'master' ? 'blue' : 'default'}>{r}</Tag>) },
                { title: t('k8s.colReady'), dataIndex: 'ready', key: 'ready', render: (v: boolean) => (v ? <Tag color="success">Ready</Tag> : <Tag color="error">NotReady</Tag>) },
                { title: t('k8s.colVersion'), dataIndex: 'version', key: 'version', render: (v: string) => <span className="mono small">{v}</span> },
                { title: 'IP', dataIndex: 'internal_ip', key: 'ip', render: (v: string) => <span className="mono small">{v}</span> },
                { title: t('k8s.colAge'), dataIndex: 'age', key: 'age' },
              ]}
            />
          </div>
        )}
        {!isServer && st.installed && <p className="small muted">{t('k8s.agentOnly')}</p>}
      </Card>

      {isServer && <K8sObjects me={me} />}
    </div>
  )
}
