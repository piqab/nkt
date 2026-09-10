import { useEffect, useState } from 'react'
import { Button, Form, Input, Select, Switch, Tag, type TableColumnsType } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import type { Me } from '../types'
import { Banner, Card, ErrorNote, InfoHint, Loading } from '../components/ui'
import { DataTable } from '../components/DataTable'

interface SystemSettings {
  hostname: string
  static_hostname?: string
  operating_system?: string
  kernel?: string
  timezone?: string
  ntp: boolean
  ntp_synchronized: boolean
  can_ntp: boolean
  local_time?: string
  locale?: string
  auto_upgrades_installed: boolean
  auto_upgrades_enabled: boolean
  notes?: string[]
}

interface NMConnection {
  name: string
  uuid: string
  type: string
  device?: string
  active: boolean
}

interface NMDevice {
  device: string
  type: string
  state: string
  connection?: string
}

interface WiFiNetwork {
  ssid: string
  signal: number
  security?: string
  in_use: boolean
}

interface NetworkState {
  available: boolean
  connections?: NMConnection[]
  devices?: NMDevice[]
  wifi?: WiFiNetwork[]
  note?: string
}

/**
 * Имя машины, часовой пояс, время и сеть NetworkManager — настройки,
 * которые задают один раз и потом ищут по документации, потому что
 * каждая делается своей командой. Здесь они в одном месте.
 */
export default function SystemSettingsPage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const settings = useApi<SystemSettings>('/system/settings', 60_000)
  const network = useApi<NetworkState>('/network/manager', 60_000)
  const timezones = useApi<{ timezones: string[] }>('/system/timezones')

  const [form] = Form.useForm<{ hostname: string; timezone: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const [wifiSSID, setWifiSSID] = useState<string | null>(null)
  const [wifiPassword, setWifiPassword] = useState('')

  const canUse = me.is_admin && me.allow_mutations

  // Поля заполняются текущими значениями, а не пустотой: форма должна
  // показывать, что стоит сейчас, а не требовать вспоминать.
  useEffect(() => {
    if (!settings.data) return
    form.setFieldsValue({
      hostname: settings.data.static_hostname || settings.data.hostname,
      timezone: settings.data.timezone,
    })
  }, [settings.data, form])

  async function save(body: Record<string, unknown>, what: string) {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      await api('/system/settings', { method: 'POST', body })
      setNotice(t('sysSettings.saved', { what }))
      await settings.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function networkAction(path: string, body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    try {
      await api(path, { method: 'POST', body })
      await network.reload()
      setWifiSSID(null)
      setWifiPassword('')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const connectionColumns: TableColumnsType<NMConnection> = [
    {
      title: t('sysSettings.colConnection'),
      key: 'name',
      render: (_, c) => (
        <div className="col">
          <span>{c.name}</span>
          <span className="small muted mono">
            {c.type}
            {c.device && ` · ${c.device}`}
          </span>
        </div>
      ),
    },
    {
      title: t('sysSettings.colState'),
      key: 'state',
      width: '9rem',
      render: (_, c) =>
        c.active ? <Tag color="green">{t('sysSettings.active')}</Tag> : <Tag>{t('sysSettings.inactive')}</Tag>,
    },
    {
      title: '',
      key: 'actions',
      width: '10rem',
      render: (_, c) =>
        canUse ? (
          <Button
            disabled={busy}
            onClick={() => networkAction('/network/manager/connection', { uuid: c.uuid, up: !c.active })}
          >
            {c.active ? t('sysSettings.disconnect') : t('sysSettings.connect')}
          </Button>
        ) : null,
    },
  ]

  if (settings.loading && !settings.data) return <Loading what={t('sysSettings.title')} />

  return (
    <>
      <div className="page-head">
        <div>
          <h1>
            {t('sysSettings.title')}
            <InfoHint>{t('sysSettings.hint')}</InfoHint>
          </h1>
        </div>
      </div>

      <ErrorNote error={settings.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="info">{notice}</Banner>}
      {settings.data?.notes?.map((n) => (
        <Banner key={n} kind="warn">
          {n}
        </Banner>
      ))}

      <Card title={t('sysSettings.system')} subtitle={settings.data?.operating_system}>
        {/* Имя проверяется по RFC 1123 прямо здесь — теми же правилами, что
            и на сервере: узнавать об одном лишнем символе после запроса к
            хосту незачем. */}
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          onFinish={(values) => save({ hostname: values.hostname }, t('sysSettings.hostname'))}
        >
          <div className="filters">
            <Form.Item
              name="hostname"
              label={t('sysSettings.hostname')}
              rules={[
                { required: true },
                { pattern: /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/, message: t('sysSettings.hostnameRule') },
              ]}
              style={{ flex: 1, minWidth: '14rem' }}
            >
              <Input disabled={!canUse} />
            </Form.Item>
            <Form.Item label=" ">
              <Button htmlType="submit" disabled={!canUse || busy} loading={busy}>
                {t('common.save')}
              </Button>
            </Form.Item>
          </div>
        </Form>
        <p className="small muted">
          {t('sysSettings.kernel')}: <code className="mono">{settings.data?.kernel}</code>
          {settings.data?.locale && (
            <>
              {' · '}
              {t('sysSettings.locale')}: <code className="mono">{settings.data.locale}</code>
            </>
          )}
        </p>
      </Card>

      <Card title={t('sysSettings.time')} subtitle={settings.data?.local_time}>
        <Form
          form={form}
          layout="vertical"
          onFinish={(values) => save({ timezone: values.timezone }, t('sysSettings.timezone'))}
        >
          <div className="filters">
            <Form.Item name="timezone" label={t('sysSettings.timezone')} style={{ flex: 1, minWidth: '16rem' }}>
              <Select
                showSearch
                disabled={!canUse}
                options={(timezones.data?.timezones ?? []).map((z) => ({ value: z, label: z }))}
                // 485 поясов: без поиска выбирать невозможно, а рисовать их
                // все разом — тяжело для страницы.
                filterOption={(input, option) =>
                  (option?.value ?? '').toString().toLowerCase().includes(input.toLowerCase())
                }
                virtual
              />
            </Form.Item>
            <Form.Item label=" ">
              <Button htmlType="submit" disabled={!canUse || busy} loading={busy}>
                {t('common.save')}
              </Button>
            </Form.Item>
          </div>
        </Form>
        <label style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginTop: '0.5rem' }}>
          <Switch
            checked={settings.data?.ntp ?? false}
            disabled={!canUse || busy || !settings.data?.can_ntp}
            onChange={(on) => save({ ntp: on }, t('sysSettings.ntp'))}
          />
          <span className="small">
            {t('sysSettings.ntp')}
            {settings.data?.ntp_synchronized && ` · ${t('sysSettings.ntpSynced')}`}
            {!settings.data?.can_ntp && ` · ${t('sysSettings.ntpUnavailable')}`}
          </span>
        </label>
        <p className="small muted">
          {settings.data?.auto_upgrades_installed
            ? settings.data.auto_upgrades_enabled
              ? t('sysSettings.autoUpgradesOn')
              : t('sysSettings.autoUpgradesOff')
            : t('sysSettings.autoUpgradesMissing')}
        </p>
      </Card>

      <Card title={t('sysSettings.network')} subtitle={network.data?.note}>
        {!network.data?.available ? (
          <p className="small muted">{network.data?.note}</p>
        ) : (
          <>
            <div className="table-wrap">
              <DataTable<NMConnection>                 dataSource={network.data.connections ?? []}
                rowKey="uuid"
                columns={connectionColumns}
              />
            </div>

            {(network.data.wifi?.length ?? 0) > 0 && (
              <>
                <h3 className="small" style={{ marginTop: '0.9rem' }}>
                  {t('sysSettings.wifi')}
                </h3>
                <div className="table-wrap">
                  <DataTable<WiFiNetwork>                     dataSource={network.data.wifi ?? []}
                    rowKey="ssid"
                    columns={[
                      {
                        title: t('sysSettings.colSSID'),
                        key: 'ssid',
                        render: (_, n) => (
                          <span>
                            {n.ssid} {n.in_use && <Tag color="green">{t('sysSettings.active')}</Tag>}
                          </span>
                        ),
                      },
                      {
                        title: t('sysSettings.colSignal'),
                        key: 'signal',
                        align: 'right',
                        sorter: (a, b) => a.signal - b.signal,
                        defaultSortOrder: 'descend',
                        render: (_, n) => <span className="num">{n.signal}%</span>,
                      },
                      {
                        title: t('sysSettings.colSecurity'),
                        key: 'security',
                        render: (_, n) => <span className="small muted">{n.security || t('sysSettings.open')}</span>,
                      },
                      {
                        title: '',
                        key: 'actions',
                        width: '9rem',
                        render: (_, n) =>
                          canUse && !n.in_use ? (
                            <Button size="small" disabled={busy} onClick={() => setWifiSSID(n.ssid)}>
                              {t('sysSettings.connect')}
                            </Button>
                          ) : null,
                      },
                    ]}
                  />
                </div>
              </>
            )}

            {wifiSSID && (
              <div className="filters" style={{ marginTop: '0.6rem' }}>
                <label style={{ flex: 1, minWidth: '14rem' }}>
                  {t('sysSettings.wifiPassword', { ssid: wifiSSID })}
                  <Input.Password
                    value={wifiPassword}
                    onChange={(e) => setWifiPassword(e.target.value)}
                    autoComplete="new-password"
                  />
                </label>
                <Button
                  type="primary"
                  loading={busy}
                  style={{ alignSelf: 'flex-end' }}
                  onClick={() => networkAction('/network/manager/wifi', { ssid: wifiSSID, password: wifiPassword })}
                >
                  {t('sysSettings.connect')}
                </Button>
                <Button style={{ alignSelf: 'flex-end' }} onClick={() => setWifiSSID(null)}>
                  {t('common.cancel')}
                </Button>
              </div>
            )}
          </>
        )}
      </Card>
    </>
  )
}
