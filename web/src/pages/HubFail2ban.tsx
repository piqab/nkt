import { useMemo, useState } from 'react'
import { Button, Input, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { useApi } from '../api'
import type { Me } from '../types'
import { Card, ErrorNote, Loading } from '../components/ui'
import { DataTable } from '../components/DataTable'
import { FleetBanModal, IPWithCheck, TemplatesPanel } from '../components/Fail2banParts'
import { TitleHelp } from '../components/Docs'

interface BannedIP {
  ip: string
  hosts: { id: number; name: string; jails: string[] }[]
}

interface HostState {
  id: number
  name: string
  known: boolean
  installed: boolean
  running: boolean
  banned: number
}

/**
 * fail2ban по всем хостам хаба: где установлен и сколько забанено,
 * какие адреса забанены и где (сверху — забаненные на большем числе
 * хостов), бан и разбан на всех хостах заданием, свои шаблоны джейлов.
 */
export default function HubFail2ban({ me }: { me: Me }) {
  const { t } = useTranslation()
  const data = useApi<{ ips: BannedIP[]; hosts: HostState[] }>('/hub/fail2ban/banned', 60_000)
  const [selected, setSelected] = useState<string[]>([])
  const [filter, setFilter] = useState('')
  const [fleet, setFleet] = useState<{ ips: string[]; action: 'ban' | 'unban' } | null>(null)
  const ips = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const list = data.data?.ips ?? []
    return q ? list.filter((i) => i.ip.includes(q) || i.hosts.some((h) => h.name.toLowerCase().includes(q))) : list
  }, [data.data, filter])
  const hosts = data.data?.hosts ?? []

  return (
    <>
      <div className="page-head spread">
        <h1>
          fail2ban
          <TitleHelp>{t('fail2ban.hubHint')}</TitleHelp>
        </h1>
        <div className="row">
          {me.is_admin && (
            <Button type="primary" danger onClick={() => setFleet({ ips: [], action: 'ban' })}>
              {t('fail2ban.banEverywhere')}
            </Button>
          )}
          <Button onClick={() => data.reload()} loading={data.loading}>
            {t('fail2ban.refresh')}
          </Button>
        </div>
      </div>
      <ErrorNote error={data.error} />
      {!data.data ? (
        <Loading what="fail2ban" />
      ) : (
        <>
          <Card title={t('fail2ban.hubHostsTitle')}>
            <div className="row" style={{ flexWrap: 'wrap', gap: '0.4rem' }}>
              {hosts.map((h) => (
                <Tag key={h.id} color={!h.known ? 'default' : !h.installed ? 'default' : h.running ? (h.banned > 0 ? 'orange' : 'green') : 'red'}>
                  {h.name}:{' '}
                  {!h.known
                    ? t('fail2ban.unknownShort')
                    : !h.installed
                      ? t('fail2ban.notInstalledShort')
                      : !h.running
                        ? t('fail2ban.stopped')
                        : t('fail2ban.bannedShort', { count: h.banned })}
                </Tag>
              ))}
            </div>
          </Card>

          <Card title={t('fail2ban.hubBannedTitle')} subtitle={t('fail2ban.hubBannedSubtitle', { count: ips.length })}>
            <div className="row" style={{ gap: '0.5rem', marginBottom: '0.5rem', alignItems: 'center' }}>
              <Input.Search allowClear placeholder={t('fail2ban.searchBans')} value={filter} onChange={(e) => setFilter(e.target.value)} style={{ maxWidth: 280 }} />
              {me.is_admin && selected.length > 0 && (
                <>
                  <span className="small">{t('bulk.selected', { count: selected.length })}</span>
                  <Button size="small" danger onClick={() => setFleet({ ips: selected, action: 'ban' })}>
                    {t('fail2ban.banEverywhereSelected', { count: selected.length })}
                  </Button>
                  <Button size="small" onClick={() => setFleet({ ips: selected, action: 'unban' })}>
                    {t('fail2ban.unbanEverywhereSelected', { count: selected.length })}
                  </Button>
                  <Button size="small" type="link" onClick={() => setSelected([])}>
                    {t('bulk.clear')}
                  </Button>
                </>
              )}
            </div>
            <div className="table-wrap">
              <DataTable<BannedIP>
                dataSource={ips}
                rowKey="ip"
                rowSelection={me.is_admin ? { selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) } : undefined}
                columns={[
                  { title: t('fail2ban.colIP'), key: 'ip', render: (_, r) => <IPWithCheck ip={r.ip} hubLevel me={me} /> },
                  {
                    title: t('fail2ban.colHosts'),
                    key: 'hosts',
                    render: (_, r) => (
                      <div className="row" style={{ flexWrap: 'wrap', gap: '0.25rem' }}>
                        {r.hosts.map((h) => (
                          <Tag key={h.id}>
                            {h.name}: <span className="mono">{h.jails.join(', ')}</span>
                          </Tag>
                        ))}
                      </div>
                    ),
                  },
                  { title: t('fail2ban.colCount'), key: 'n', align: 'right', render: (_, r) => <span className="num">{r.hosts.length}</span> },
                ]}
              />
            </div>
          </Card>

          <Card title={t('fail2ban.templates')} subtitle={t('fail2ban.hubTemplatesSubtitle')}>
            <TemplatesPanel me={me} hubLevel />
          </Card>
        </>
      )}
      {fleet && (
        <FleetBanModal
          ips={fleet.ips}
          action={fleet.action}
          onClose={() => setFleet(null)}
          onDone={() => {
            setSelected([])
            data.reload()
          }}
        />
      )}
    </>
  )
}
