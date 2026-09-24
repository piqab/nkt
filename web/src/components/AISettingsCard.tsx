import { useState } from 'react'
import { Button, Checkbox, Input, InputNumber, Select } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, ErrorNote, Loading } from './ui'

/**
 * Настройка модели — одна на всю установку, на хабе: ключ хранится
 * здесь зашифрованным, запросы уходят отсюда. Хостам наружу ничего не
 * нужно, и раздавать им ключ тоже незачем.
 *
 * Локальная модель — не экзотика, а единственный вариант, когда наружу
 * нельзя отдавать ничего: «OpenAI-совместимый» плюс адрес вида
 * http://127.0.0.1:11434 (Ollama) — и запрос не покидает машину.
 */

interface AIStatus {
  enabled: boolean
  provider: 'anthropic' | 'openai'
  base_url: string
  model: string
  has_key: boolean
  anonymize: boolean
  daily_limit: number
  usage_today: number
  cache_size: number
}

export function AISettingsCard() {
  const { t } = useTranslation()
  const status = useApi<AIStatus>('/hub/ai')
  const [draft, setDraft] = useState<AIStatus | null>(null)
  const [apiKey, setAPIKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  // Итог проверки: живой запрос к модели теми настройками, что сейчас в
  // форме, — иначе неверный ключ обнаружится только при первом разборе.
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null)

  const cur = draft ?? status.data
  const local = cur?.provider === 'openai' && /^https?:\/\/(127\.0\.0\.1|localhost|\[::1\]|10\.|192\.168\.|172\.)/.test(cur?.base_url ?? '')

  function edit(patch: Partial<AIStatus>) {
    if (!cur) return
    setDraft({ ...cur, ...patch })
    setSaved(false)
  }

  async function save() {
    if (!cur) return
    setBusy(true)
    setError(null)
    try {
      const res = await api<AIStatus>('/hub/ai/settings', {
        method: 'POST',
        body: {
          enabled: cur.enabled,
          provider: cur.provider,
          base_url: cur.base_url,
          model: cur.model,
          anonymize: cur.anonymize,
          daily_limit: cur.daily_limit,
          // Пустая строка оставляет прежний ключ: набирать его заново
          // при каждой правке лимита — ровно тот случай, когда настройку
          // перестают трогать вовсе.
          api_key: apiKey === '' ? null : apiKey,
        },
      })
      setDraft(null)
      setAPIKey('')
      setSaved(true)
      status.reload()
      void res
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function runTest() {
    if (!cur) return
    setBusy(true)
    setTest(null)
    try {
      const res = await api<{ ok: boolean; model: string; took_ms: number; reply: string; message?: string }>('/hub/ai/test', {
        method: 'POST',
        body: {
          enabled: true,
          provider: cur.provider,
          base_url: cur.base_url,
          model: cur.model,
          anonymize: cur.anonymize,
          daily_limit: cur.daily_limit,
          api_key: apiKey === '' ? null : apiKey,
        },
      })
      setTest(
        res.ok
          ? { ok: true, text: t('ai.testOK', { ms: res.took_ms, reply: res.reply }) }
          : { ok: false, text: res.message ?? '' },
      )
      status.reload()
    } catch (err) {
      setTest({ ok: false, text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  async function clearCache() {
    setBusy(true)
    try {
      await api('/hub/ai/cache/clear', { method: 'POST' })
      status.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title={t('ai.title')} subtitle={t('ai.hint')}>
      <ErrorNote error={status.error} />
      {error && <Banner kind="error">{error}</Banner>}
      {!cur ? (
        <Loading what={t('ai.title')} />
      ) : (
        <div className="col">
          <Checkbox checked={cur.enabled} onChange={(e) => edit({ enabled: e.target.checked })}>
            {t('ai.enabled')}
          </Checkbox>
          <div className="filters">
            <label style={{ minWidth: '14rem' }}>
              {t('ai.provider')}
              <Select
                value={cur.provider}
                onChange={(v: AIStatus['provider']) =>
                  edit({
                    provider: v,
                    // Адрес по умолчанию для выбранного провайдера, чтобы
                    // не заставлять вспоминать его наизусть.
                    base_url: v === 'anthropic' ? 'https://api.anthropic.com' : 'http://127.0.0.1:11434',
                  })
                }
                options={[
                  { value: 'anthropic', label: t('ai.providerAnthropic') },
                  { value: 'openai', label: t('ai.providerOpenAI') },
                ]}
              />
            </label>
            <label style={{ flex: 1, minWidth: '16rem' }}>
              {t('ai.baseUrl')}
              <Input className="sensitive" value={cur.base_url} onChange={(e) => edit({ base_url: e.target.value })} />
            </label>
            <label style={{ flex: 1, minWidth: '12rem' }}>
              {t('ai.model')}
              <Input value={cur.model} onChange={(e) => edit({ model: e.target.value })} placeholder="claude-sonnet-5" />
            </label>
          </div>
          <label>
            {t('ai.apiKey')}
            <Input.Password
              className="sensitive"
              value={apiKey}
              onChange={(e) => setAPIKey(e.target.value)}
              autoComplete="new-password"
              placeholder={cur.has_key ? t('ai.apiKeySet') : local ? t('ai.apiKeyLocal') : ''}
            />
          </label>
          {local && <div className="small muted">{t('ai.localHint')}</div>}
          <label style={{ flexDirection: 'row', alignItems: 'center', gap: '0.4rem' }} title={t('ai.anonymizeHint')}>
            <Checkbox checked={cur.anonymize} onChange={(e) => edit({ anonymize: e.target.checked })}>
              {t('ai.anonymize')}
            </Checkbox>
          </label>
          <label style={{ maxWidth: '16rem' }}>
            {t('ai.dailyLimit')}
            <InputNumber
              min={0}
              max={100000}
              value={cur.daily_limit}
              onChange={(v) => edit({ daily_limit: v ?? 0 })}
              title={t('ai.dailyLimitHint')}
              style={{ width: '100%' }}
            />
          </label>
          <div className="row" style={{ gap: '0.75rem', alignItems: 'center' }}>
            <Button type="primary" loading={busy} onClick={() => void save()}>
              {t('ai.save')}
            </Button>
            <Button loading={busy} onClick={() => void runTest()}>
              {t('ai.test')}
            </Button>
            {saved && <span className="small muted">{t('common.savedShort')}</span>}
            <span className="small muted">
              {t('ai.usageToday')}: {cur.usage_today}
              {cur.daily_limit > 0 && <> / {cur.daily_limit}</>} · {t('ai.cacheSize')}: {cur.cache_size}
            </span>
            <Button size="small" disabled={busy || cur.cache_size === 0} onClick={() => void clearCache()}>
              {t('ai.cacheClear')}
            </Button>
          </div>
          {/* Итог — своей строкой: ответ модели бывает длинным и не должен
              растаскивать кнопки и счётчики. */}
          {test && (
            <div className="small" style={{ color: test.ok ? 'var(--status-good)' : 'var(--status-critical)', whiteSpace: 'pre-wrap' }}>
              {test.ok ? '✓' : '✗'} {test.text}
            </div>
          )}
        </div>
      )}
    </Card>
  )
}
