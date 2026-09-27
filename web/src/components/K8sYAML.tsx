import { useState } from 'react'
import { Input, Select, Space } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, qs, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Loading, Modal } from './ui'
import { EditTextModal } from './EditTextModal'
import { VersionHistory } from './VersionHistory'

type Doc = { content: string; sha256: string; history_path: string }

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

/**
 * YAML объекта кластера: правка в окне, перед записью — текстовый дифф
 * и kubectl diff с кластера, запись — kubectl apply, версии — в истории
 * конфигураций (k8s://…) с откатом.
 */
export function K8sYAMLModal({ kind, namespace, name, me, onClose, onSaved }: { kind: string; namespace?: string; name: string; me: Me; onClose: () => void; onSaved: () => void }) {
  const { t } = useTranslation()
  const url = `/k8s/yaml${qs({ kind, namespace: namespace || undefined, name })}`
  const doc = useApi<Doc>(url)
  const [draft, setDraft] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [history, setHistory] = useState(false)
  const canMutate = me.is_admin && me.allow_mutations
  const id = namespace ? `${namespace}/${name}` : name

  if (!doc.data) {
    return (
      <Modal title={t('k8s.yaml.title', { name: id })} onClose={onClose}>
        {doc.error ? <Banner kind="error">{doc.error}</Banner> : <Loading what="YAML" />}
      </Modal>
    )
  }
  const saved = doc.data.content
  const text = draft ?? saved
  const body = { kind, namespace: namespace ?? '', name, content: text }

  async function save(): Promise<boolean> {
    setBusy(true)
    setError(null)
    try {
      await api('/k8s/yaml', { method: 'PUT', body: { ...body, note: note || t('k8s.yaml.editNote'), expected_sha256: doc.data?.sha256 ?? '' } })
      onSaved()
      return true
    } catch (err) {
      setError(errText(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <EditTextModal
        title={t('k8s.yaml.title', { name: id })}
        saved={saved}
        draft={text}
        onDraft={setDraft}
        busy={busy}
        onSave={canMutate ? save : async () => false}
        onHistory={() => setHistory(true)}
        onClose={onClose}
        serverDiff={canMutate ? { title: t('k8s.yaml.serverDiff'), load: async () => (await api<{ diff: string }>('/k8s/yaml/diff', { method: 'POST', body })).diff } : undefined}
        fields={
          <>
            <p className="small muted">{t('k8s.yaml.hint')}</p>
            {canMutate && <Input size="small" style={{ maxWidth: '24rem', marginBottom: '0.5rem' }} value={note} placeholder={t('k8s.yaml.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />}
          </>
        }
        below={
          error ? (
            <Banner kind="error" onClose={() => setError(null)}>
              {error}
            </Banner>
          ) : !canMutate ? (
            <Banner kind="info">{t('common.mutationsDisabled')}</Banner>
          ) : null
        }
      />
      {history && (
        <Modal title={t('configs.versionHistoryTitle')} onClose={() => setHistory(false)} width={960}>
          <VersionHistory
            path={doc.data.history_path}
            me={me}
            apply
            onChanged={() => {
              setDraft(null)
              void doc.reload()
              onSaved()
            }}
          />
        </Modal>
      )}
    </>
  )
}

const TEMPLATES: Record<string, (name: string, ns: string) => string> = {
  deployment: (name, ns) => `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    app: ${name}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: ${name}
  template:
    metadata:
      labels:
        app: ${name}
    spec:
      containers:
        - name: ${name}
          image: nginx:1.27
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              memory: 256Mi
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  selector:
    app: ${name}
  ports:
    - port: 80
      targetPort: 80
`,
  ingress: (name, ns) => `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  rules:
    - host: ${name}.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ${name}
                port:
                  number: 80
`,
  configmap: (name, ns) => `apiVersion: v1
kind: ConfigMap
metadata:
  name: ${name}
  namespace: ${ns}
data:
  key: value
`,
  cronjob: (name, ns) => `apiVersion: batch/v1
kind: CronJob
metadata:
  name: ${name}
  namespace: ${ns}
spec:
  schedule: "0 3 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: ${name}
              image: busybox:1.36
              command: ["sh", "-c", "date; echo hello"]
`,
  empty: () => '',
}

/** «Новый объект»: манифест из шаблона, дифф с кластером, kubectl apply. */
export function K8sNewObjectModal({ namespace, namespaces, me, onClose, onCreated }: { namespace: string; namespaces: string[]; me: Me; onClose: () => void; onCreated: () => void }) {
  const { t } = useTranslation()
  const [tpl, setTpl] = useState('deployment')
  const [name, setName] = useState('web')
  const [ns, setNs] = useState(namespace || 'default')
  const [draft, setDraft] = useState<string>(() => TEMPLATES.deployment('web', namespace || 'default'))
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const regenerate = (nextTpl: string, nextName: string, nextNs: string) => setDraft(TEMPLATES[nextTpl](nextName || 'web', nextNs || 'default'))

  async function save(): Promise<boolean> {
    setBusy(true)
    setError(null)
    try {
      await api('/k8s/apply', { method: 'POST', body: { content: draft, note: note || t('k8s.yaml.createNote') } })
      onCreated()
      return true
    } catch (err) {
      setError(errText(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <EditTextModal
      title={t('k8s.yaml.newTitle')}
      saved=""
      draft={draft}
      onDraft={setDraft}
      busy={busy}
      onSave={me.is_admin && me.allow_mutations ? save : async () => false}
      onClose={onClose}
      serverDiff={{ title: t('k8s.yaml.serverDiff'), load: async () => (await api<{ diff: string }>('/k8s/yaml/diff', { method: 'POST', body: { content: draft } })).diff }}
      fields={
        <>
          <p className="small muted">{t('k8s.yaml.newHint')}</p>
          <Space wrap style={{ marginBottom: '0.5rem' }}>
            <Select
              size="small"
              style={{ width: '14rem' }}
              value={tpl}
              onChange={(v) => {
                setTpl(v)
                regenerate(v, name, ns)
              }}
              options={Object.keys(TEMPLATES).map((k) => ({ value: k, label: t(`k8s.yaml.tpl.${k}`) }))}
            />
            <Input
              size="small"
              style={{ width: '12rem' }}
              value={name}
              placeholder={t('k8s.yaml.name')}
              onChange={(e) => {
                const v = e.target.value.trim()
                setName(v)
                regenerate(tpl, v, ns)
              }}
            />
            <Select
              size="small"
              showSearch
              style={{ width: '12rem' }}
              value={ns}
              onChange={(v) => {
                setNs(v)
                regenerate(tpl, name, v)
              }}
              options={namespaces.map((n) => ({ value: n, label: n }))}
            />
            <Input size="small" style={{ width: '18rem' }} value={note} placeholder={t('k8s.yaml.notePlaceholder')} onChange={(e) => setNote(e.target.value)} />
          </Space>
        </>
      }
      below={
        error ? (
          <Banner kind="error" onClose={() => setError(null)}>
            {error}
          </Banner>
        ) : null
      }
    />
  )
}
