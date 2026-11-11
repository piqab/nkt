import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { clusterAgentScope, useApi } from '../api'
import type { Me } from '../types'
import { Loading, Modal } from './ui'
import { K8sObjects } from './K8sObjects'

/**
 * Kubernetes-вид одного кластера из раздела «Кластеры»: тот же компонент
 * объектов, что на вкладке Kubernetes у хоста, но запросы идут через
 * кластерный прокси хаба (/hub/clusters/{id}/agent/…) на control plane
 * кластера (internal/hub/cluster_agent.go) — кластер может лежать на
 * нескольких хостах, и у самого раздела выбранного хоста нет.
 */
export function ClusterK8sModal({ cluster, onClose }: { cluster: { id: number; name: string }; onClose: () => void }) {
  const { t } = useTranslation()
  // The scope must be live before K8sObjects mounts and fires its first
  // fetches — child effects run before parent effects, so set it during
  // render (idempotent) and only clear it on unmount.
  clusterAgentScope.id = cluster.id
  useEffect(() => {
    return () => {
      clusterAgentScope.id = null
    }
  }, [])
  const me = useApi<Me>('/auth/me')
  return (
    <Modal title={t('clusters.k8sTitle', { name: cluster.name })} onClose={onClose} width="min(1280px, 96vw)" sizeKey="k8s">
      {me.data ? <K8sObjects me={me.data} /> : <Loading what="Kubernetes" />}
    </Modal>
  )
}
