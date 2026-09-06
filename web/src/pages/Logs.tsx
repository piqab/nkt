import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Checkbox, Input, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { useLocation } from 'react-router-dom'
import { api, hostScope, readSelectedHost, useApi } from '../api'
import { wsURL } from '../hooks/usePty'
import { Card, ErrorNote, InfoHint } from '../components/ui'

type LogSource = {
  kind: 'unit' | 'file'
  name: string
  size?: number
  service?: string
  /** A rotated generation: never grows again, so it is read once. */
  archived?: boolean
  compressed?: boolean
}

/** Line counts offered for the initial read. An archive can be enormous, and
 * the whole of one is never what is wanted. */
const LINE_CHOICES = [500, 1000, 5000]

/** How many lines are kept in the browser. A followed log is unbounded; the
 * tab must not grow with it until it dies. */
const MAX_LINES = 5000


export default function Logs() {
  const { t } = useTranslation()
  const location = useLocation()
  const isPopout = location.pathname === '/logs/popout'

  const sources = useApi<{ sources: LogSource[]; root: string }>('/logs/sources')
  const [selected, setSelected] = useState<string | null>(null)
  const [customPath, setCustomPath] = useState('')
  const [lines, setLines] = useState<string[]>([])
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [showArchived, setShowArchived] = useState(false)
  const [lineCount, setLineCount] = useState(500)
  const [filter, setFilter] = useState('')
  const [highlight, setHighlight] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [follow, setFollow] = useState(true)

  const wsRef = useRef<WebSocket | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  // A popout is opened for one host and stays on it (see App.tsx) — the
  // window title says which, since several can be open at once.
  useEffect(() => {
    if (!isPopout) return
    const name = new URLSearchParams(location.search).get('name')
    document.title = name ? t('logs.popoutTitle', { name }) : t('logs.popoutTitleDefault')
  }, [isPopout, location.search, t])

  // The value encodes both kind and name so one <Select> can offer units and
  // files together without a second control to say which is which.
  function queryFor(value: string): string | null {
    if (value.startsWith('unit:')) return `unit=${encodeURIComponent(value.slice(5))}`
    if (value.startsWith('file:')) return `path=${encodeURIComponent(value.slice(5))}`
    return null
  }

  function stop() {
    wsRef.current?.close()
    wsRef.current = null
    setConnected(false)
  }

  /** True for a value that names a rotated file. */
  function isArchived(value: string): boolean {
    if (!value.startsWith('file:')) return false
    const name = value.slice(5)
    return (sources.data?.sources ?? []).some((s) => s.name === name && s.archived)
  }

  async function start(value: string) {
    stop()
    const query = queryFor(value)
    if (!query) return
    setLines([])
    setError(null)

    // An archive does not grow, so there is nothing to follow — it is read
    // once instead, decompressed on the host if it needs to be.
    if (isArchived(value)) {
      try {
        const res = await api<{ output: string }>(`/logs/tail?${query}&lines=${lineCount}`)
        setLines(res.output ? res.output.split('\n') : [])
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
      return
    }

    const ws = new WebSocket(wsURL(`/logs/ws?${query}&lines=${lineCount}`))
    wsRef.current = ws
    ws.onopen = () => setConnected(true)
    ws.onmessage = (event) => {
      // The server sends whole lines only, several per frame (see
      // handleLogStream) — splitting on newline can never produce a partial
      // line here, so no reassembly buffer is needed.
      const incoming = String(event.data).split('\n')
      setLines((prev) => {
        const next = prev.concat(incoming)
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
      })
    }
    ws.onerror = () => setError(t('logs.connectionFailed'))
    ws.onclose = () => setConnected(false)
  }

  // Closing the tab or navigating away must not leave a `tail -F` running on
  // the host: the server kills it when the socket goes, so the socket has to
  // actually go.
  useEffect(() => () => stop(), [])

  const shown = useMemo(() => {
    if (!filter) return lines
    const needle = caseSensitive ? filter : filter.toLowerCase()
    return lines.filter((line) => (caseSensitive ? line : line.toLowerCase()).includes(needle))
  }, [lines, filter, caseSensitive])

  useEffect(() => {
    if (follow) bottomRef.current?.scrollIntoView({ block: 'end' })
  }, [shown, follow])

  function openPopout() {
    stop()
    const id = hostScope.id
    const params = new URLSearchParams()
    if (id !== null) {
      params.set('host', String(id))
      const name = readSelectedHost()?.name
      if (name) params.set('name', name)
    }
    if (selected) params.set('source', selected)
    const qs = params.toString()
    window.open(`/logs/popout${qs ? `?${qs}` : ''}`, `nkt-logs-${id ?? 'local'}`,
      'width=1100,height=700,resizable=yes')
  }

  // A popout opens already pointed at the log it was detached from.
  useEffect(() => {
    if (!isPopout) return
    const source = new URLSearchParams(location.search).get('source')
    if (source) {
      setSelected(source)
      start(source)
    }
    // Only on mount: re-running this on every render would restart the stream.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isPopout])

  const options = useMemo(() => {
    const list = sources.data?.sources ?? []
    return [
      {
        label: t('logs.groupUnits'),
        options: list
          .filter((s) => s.kind === 'unit')
          .map((s) => ({ value: `unit:${s.name}`, label: s.service ? `${s.name} (${s.service})` : s.name })),
      },
      {
        label: t('logs.groupFiles'),
        options: list
          .filter((s) => s.kind === 'file' && !s.archived)
          .map((s) => ({ value: `file:${s.name}`, label: s.name })),
      },
      ...(showArchived
        ? [
            {
              label: t('logs.groupArchived'),
              options: list
                .filter((s) => s.kind === 'file' && s.archived)
                .map((s) => ({
                  value: `file:${s.name}`,
                  label: s.compressed ? `${s.name} ${t('logs.compressedTag')}` : s.name,
                })),
            },
          ]
        : []),
    ]
  }, [sources.data, showArchived, t])

  return (
    <>
      <div className="page-head spread">
        <div>
          <h1>
            {t('logs.title')}
            <InfoHint>{t('logs.hint', { root: sources.data?.root ?? '/var/log' })}</InfoHint>
          </h1>
        </div>
        <div className="row">
          {isPopout ? (
            <Button onClick={() => window.close()}>{t('logs.closeWindow')}</Button>
          ) : (
            <Button onClick={openPopout} title={t('logs.detachTooltip')}>
              {t('logs.detach')}
            </Button>
          )}
        </div>
      </div>

      <ErrorNote error={sources.error} />
      {error && <ErrorNote error={error} />}

      <Card
        title={t('logs.pickerTitle')}
        actions={
          <div className="row">
            {connected ? (
              <Tag color="green">{t('logs.streaming')}</Tag>
            ) : selected && isArchived(selected) ? (
              <Tag>{t('logs.archivedTag')}</Tag>
            ) : (
              <Tag>{t('logs.stopped')}</Tag>
            )}
            <Button size="small" onClick={() => (connected ? stop() : selected && start(selected))}>
              {connected ? t('logs.stop') : t('logs.start')}
            </Button>
          </div>
        }
      >
        <div className="row" style={{ flexWrap: 'wrap', gap: '0.5rem' }}>
          <Select
            showSearch
            style={{ minWidth: 320 }}
            placeholder={t('logs.pickPlaceholder')}
            value={selected}
            options={options}
            onChange={(value) => {
              setSelected(value)
              start(value)
            }}
            filterOption={(input, option) =>
              String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())
            }
          />
          <Input
            style={{ maxWidth: 320 }}
            placeholder={t('logs.customPlaceholder', { root: sources.data?.root ?? '/var/log' })}
            value={customPath}
            onChange={(e) => setCustomPath(e.target.value)}
            onPressEnter={() => {
              if (!customPath.trim()) return
              const value = `file:${customPath.trim()}`
              setSelected(value)
              start(value)
            }}
          />
        </div>

        <div className="row" style={{ flexWrap: 'wrap', gap: '0.5rem', marginTop: '0.75rem' }}>
          <Input
            style={{ maxWidth: 260 }}
            allowClear
            placeholder={t('logs.filterPlaceholder')}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <Input
            style={{ maxWidth: 260 }}
            allowClear
            placeholder={t('logs.highlightPlaceholder')}
            value={highlight}
            onChange={(e) => setHighlight(e.target.value)}
          />
          <Checkbox checked={showArchived} onChange={(e) => setShowArchived(e.target.checked)}>
            {t('logs.showArchived')}
          </Checkbox>
          <Select
            style={{ width: 130 }}
            value={lineCount}
            onChange={setLineCount}
            options={LINE_CHOICES.map((n) => ({ value: n, label: t('logs.lastLines', { n }) }))}
          />
          <Checkbox checked={caseSensitive} onChange={(e) => setCaseSensitive(e.target.checked)}>
            {t('logs.caseSensitive')}
          </Checkbox>
          <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
            {t('logs.autoScroll')}
          </Checkbox>
          <span className="small secondary">
            {t('logs.counter', { shown: shown.length, total: lines.length })}
          </span>
        </div>
      </Card>

      <Card title={t('logs.outputTitle')}>
        <pre className="diff" style={{ maxHeight: isPopout ? '72vh' : '58vh', overflow: 'auto' }}>
          {shown.map((line, i) => (
            <LogLine key={i} text={line} highlight={highlight} caseSensitive={caseSensitive} />
          ))}
          <div ref={bottomRef} />
        </pre>
        {!connected && lines.length === 0 && (
          <div className="chart-empty">{t('logs.empty')}</div>
        )}
      </Card>
    </>
  )
}

/** One line, with every occurrence of the highlight term marked. */
function LogLine({
  text,
  highlight,
  caseSensitive,
}: {
  text: string
  highlight: string
  caseSensitive: boolean
}) {
  if (!highlight) return <div>{text}</div>

  const haystack = caseSensitive ? text : text.toLowerCase()
  const needle = caseSensitive ? highlight : highlight.toLowerCase()
  if (!needle || !haystack.includes(needle)) return <div>{text}</div>

  const parts: React.ReactNode[] = []
  let from = 0
  for (;;) {
    const at = haystack.indexOf(needle, from)
    if (at === -1) break
    if (at > from) parts.push(text.slice(from, at))
    parts.push(<mark key={at}>{text.slice(at, at + needle.length)}</mark>)
    from = at + needle.length
  }
  parts.push(text.slice(from))
  return <div>{parts}</div>
}
