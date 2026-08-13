import { Tabs } from 'antd'
import { useApi } from '../api'
import type { Container, LXDInstance, Listener, Me, PodmanContainer, VirtualMachine } from '../types'
import Docker from './Docker'
import Podman from './Podman'
import LXD from './LXD'
import Virtualization from './Virtualization'
import Misc from './Misc'

function tabLabel(text: string, count: number | undefined): string {
  return count === undefined ? text : `${text} (${count})`
}

/**
 * Docker/Podman/LXD/виртуальные машины и "Разное" (сервисы вне всех
 * конфигов) used to be five separate nav entries doing the same kind of
 * thing — manage what's actually running on the host. Combined into one
 * section with a tab per source; each page component underneath is
 * untouched, only the entry point changed.
 *
 * Each tab's own count comes from a lightweight fetch here, separate from
 * the fetch the tab's own page component makes once it is actually
 * selected (antd Tabs only mounts the active pane) — a little duplicate
 * traffic against a host-local API in exchange for not threading fetched
 * data through every one of these otherwise-independent pages.
 */
export default function Containers({ me }: { me: Me }) {
  const docker = useApi<{ containers: Container[] }>('/containers', 30_000)
  const podman = useApi<{ containers: PodmanContainer[] }>('/podman/containers', 30_000)
  const lxd = useApi<{ instances: LXDInstance[] }>('/lxd/instances', 30_000)
  const vms = useApi<{ vms: VirtualMachine[] }>('/vms', 30_000)
  const misc = useApi<{ listeners: Listener[] }>('/misc', 60_000)

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
          label: tabLabel('Виртуальные машины', vms.data?.vms.length),
          children: <Virtualization me={me} />,
        },
        {
          key: 'misc',
          label: tabLabel('Разное', misc.data?.listeners.length),
          children: <Misc />,
        },
      ]}
    />
  )
}
