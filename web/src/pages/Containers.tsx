import { Tabs } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import type { Container, LXDInstance, Me, PodmanContainer, VirtualMachine } from '../types'
import Docker from './Docker'
import Podman from './Podman'
import LXD from './LXD'
import Virtualization from './Virtualization'

function tabLabel(text: string, count: number | undefined): string {
  return count === undefined ? text : `${text} (${count})`
}

/**
 * Docker/Podman/LXD/виртуальные машины used to be four separate nav
 * entries doing the same kind of thing — manage what's actually running on
 * the host. Combined into one section with a tab per source; each page
 * component underneath is untouched, only the entry point changed.
 * "Разное" used to be a fifth tab here — moved onto "Сервисы" instead,
 * since after ps/cgroup enrichment most of what shows up there turns out
 * to be a systemd unit, not a container.
 *
 * Each tab's own count comes from a lightweight fetch here, separate from
 * the fetch the tab's own page component makes once it is actually
 * selected (antd Tabs only mounts the active pane) — a little duplicate
 * traffic against a host-local API in exchange for not threading fetched
 * data through every one of these otherwise-independent pages.
 */
export default function Containers({ me }: { me: Me }) {
  const { t } = useTranslation()
  const docker = useApi<{ containers: Container[] }>('/containers', 30_000)
  const podman = useApi<{ containers: PodmanContainer[] }>('/podman/containers', 30_000)
  const lxd = useApi<{ instances: LXDInstance[] }>('/lxd/instances', 30_000)
  const vms = useApi<{ vms: VirtualMachine[] }>('/vms', 30_000)

  return (
    <Tabs
      defaultActiveKey="docker"
      items={[
        {
          key: 'docker',
          label: tabLabel('Docker', docker.data?.containers.length),
          children: <Docker me={me} />,
        },
        {
          key: 'podman',
          label: tabLabel('Podman', podman.data?.containers.length),
          children: <Podman me={me} />,
        },
        {
          key: 'lxd',
          label: tabLabel('LXD', lxd.data?.instances.length),
          children: <LXD me={me} />,
        },
        {
          key: 'vms',
          label: tabLabel(t('containers.vms'), vms.data?.vms.length),
          children: <Virtualization me={me} />,
        },
      ]}
    />
  )
}
