import { useState } from 'react'
import { AutoComplete, Button, Checkbox, Input, InputNumber, Select, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Card, DiffView, ErrorNote, Loading, Modal, formatBytesShort } from './ui'
import { invalidateAIAnswers } from '../aiAnswers'
import { EditTextModal } from './EditTextModal'
import { msg, tx, type Msg } from '../msg'

/**
 * Настройка модели — одна на всю установку, на хабе: ключ хранится
 * здесь зашифрованным, запросы уходят отсюда. Хостам наружу ничего не
 * нужно, и раздавать им ключ тоже незачем.
 *
 * Локальная модель — не экзотика, а единственный вариант, когда наружу
 * нельзя отдавать ничего: «OpenAI-совместимый» плюс адрес вида
 * http://127.0.0.1:11434 (Ollama) — и запрос не покидает машину.
 */

/** Модель из списка провайдера (POST /hub/ai/models). */
interface AIModel {
  id: string
  name?: string
  created?: string
  size?: number
  /** Годится для разбора (у OpenAI в списке есть эмбеддинги, речь…). */
  chat: boolean
}

interface AIStatus {
  enabled: boolean
  provider: 'anthropic' | 'openai'
  base_url: string
  model: string
  has_key: boolean
  anonymize: boolean
  daily_limit: number
  timeout_s: number
  usage_today: number
  cache_size: number
}

interface AIPrompt {
  kind: 'finding' | 'map' | 'config' | 'ip' | 'monitoring'
  lang: 'ru' | 'en'
  text: string
  default: string
  modified: boolean
}

export function AISettingsCard() {
  const { t } = useTranslation()
  const status = useApi<AIStatus>('/hub/ai')
  // Инструкции модели: стандартные лежат в бинарнике, правка хранится на
  // хабе; сохранение — через окно с диффом, чтобы было видно, что именно
  // изменилось относительно стандартной.
  const prompts = useApi<{ prompts: AIPrompt[] }>('/hub/ai/prompts')
  const [promptKind, setPromptKind] = useState<AIPrompt['kind']>('finding')
  const [promptLang, setPromptLang] = useState<AIPrompt['lang']>('ru')
  const [promptDrafts, setPromptDrafts] = useState<Record<string, string>>({})
  const [promptDiff, setPromptDiff] = useState<{ diff: string; toDefault: boolean } | null>(null)
  const promptKey = `${promptKind}/${promptLang}`
  const currentPrompt = prompts.data?.prompts.find((p) => p.kind === promptKind && p.lang === promptLang)
  const promptText = promptDrafts[promptKey] ?? currentPrompt?.text ?? ''
  const promptDirty = currentPrompt !== undefined && promptText !== currentPrompt.text

  async function reviewPrompt() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ changed: boolean; diff: string }>('/hub/ai/prompts/diff', {
        method: 'POST',
        body: { kind: promptKind, lang: promptLang, text: promptText },
      })
      setPromptDiff({ diff: res.diff, toDefault: !res.changed })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const [editingPrompt, setEditingPrompt] = useState(false)

  async function savePrompt(text: string): Promise<boolean> {
    setBusy(true)
    setError(null)
    setPromptDiff(null)
    try {
      await api('/hub/ai/prompts', { method: 'POST', body: { kind: promptKind, lang: promptLang, text } })
      setPromptDrafts((d) => {
        const next = { ...d }
        delete next[promptKey]
        return next
      })
      prompts.reload()
      status.reload()
      // Прежние ответы получены другой инструкцией — хаб их снёс.
      invalidateAIAnswers()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusy(false)
    }
  }
  const [draft, setDraft] = useState<AIStatus | null>(null)
  const [apiKey, setAPIKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  // Итог проверки: живой запрос к модели теми настройками, что сейчас в
  // форме, — иначе неверный ключ обнаружится только при первом разборе.
  const [test, setTest] = useState<{ ok: boolean; text: Msg } | null>(null)

  // Список моделей провайдера — только по кнопке и только на время
  // выбора: при смене провайдера или адреса он устаревает.
  const [models, setModels] = useState<AIModel[] | null>(null)
  const [modelsBusy, setModelsBusy] = useState(false)
  const [modelsError, setModelsError] = useState<string | null>(null)
  const [showAllModels, setShowAllModels] = useState(false)
  // Модель только что выбрана из списка — предложить проверить её.
  const [picked, setPicked] = useState(false)
  // Что набрано в поле после загрузки списка: фильтр только по нему —
  // иначе открытый список показывал бы одну уже выбранную модель.
  const [modelQuery, setModelQuery] = useState('')

  const cur = draft ?? status.data
  const local = cur?.provider === 'openai' && /^https?:\/\/(127\.0\.0\.1|localhost|\[::1\]|10\.|192\.168\.|172\.)/.test(cur?.base_url ?? '')

  function edit(patch: Partial<AIStatus>) {
    if (!cur) return
    setDraft({ ...cur, ...patch })
    setSaved(false)
    if (patch.provider !== undefined || patch.base_url !== undefined) {
      setModels(null)
      setModelsError(null)
    }
    if (patch.model !== undefined) setPicked(false)
  }

  async function fetchModels() {
    if (!cur) return
    setModelsBusy(true)
    setModelsError(null)
    try {
      const res = await api<{ models: AIModel[] }>('/hub/ai/models', {
        method: 'POST',
        timeoutMs: 90_000,
        body: {
          provider: cur.provider,
          base_url: cur.base_url,
          model: cur.model,
          timeout_s: cur.timeout_s,
          api_key: apiKey === '' ? null : apiKey,
        },
      })
      setModels(res.models)
      setModelQuery('')
    } catch (err) {
      setModels(null)
      setModelsError(err instanceof Error ? err.message : String(err))
    } finally {
      setModelsBusy(false)
    }
  }

  const hiddenModels = (models ?? []).filter((m) => !m.chat).length
  const shownModels = (models ?? []).filter((m) => showAllModels || m.chat)
  const q = modelQuery.trim().toLowerCase()
  const matchedModels = q ? shownModels.filter((m) => m.id.toLowerCase().includes(q) || (m.name ?? '').toLowerCase().includes(q)) : shownModels
  const modelLabel = (m: AIModel) => (
    <div className="spread" style={{ gap: '0.6rem' }}>
      <span>
        <span className="mono">{m.id}</span>
        {m.name && m.name !== m.id && <span className="muted"> · {m.name}</span>}
      </span>
      <span className="small muted nowrap">
        {[m.created?.slice(0, 10), m.size ? formatBytesShort(m.size) : ''].filter(Boolean).join(' · ')}
      </span>
    </div>
  )

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
          timeout_s: cur.timeout_s,
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
        // Ждём столько, сколько разрешено модели, плюс запас на дорогу:
        // общий таймаут запросов (30 с) короче любого разумного ответа.
        timeoutMs: (cur.timeout_s + 30) * 1000,
        body: {
          enabled: true,
          provider: cur.provider,
          base_url: cur.base_url,
          model: cur.model,
          anonymize: cur.anonymize,
          daily_limit: cur.daily_limit,
          timeout_s: cur.timeout_s,
          api_key: apiKey === '' ? null : apiKey,
        },
      })
      setTest(
        res.ok
          ? { ok: true, text: tx('ai.testOK', { ms: res.took_ms, reply: res.reply }) }
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
            <label style={{ flex: 1, minWidth: '16rem' }}>
              {t('ai.model')}
              <div className="row row-nowrap" style={{ gap: '0.4rem' }}>
                <AutoComplete
                  style={{ flex: 1 }}
                  value={cur.model}
                  onChange={(v: string) => edit({ model: v })}
                  onSearch={setModelQuery}
                  onSelect={(v: string) => {
                    edit({ model: v })
                    setModelQuery('')
                    setPicked(true)
                  }}
                  options={matchedModels.map((m) => ({ value: m.id, label: modelLabel(m) }))}
                  filterOption={false}
                  popupMatchSelectWidth={420}
                  placeholder="claude-sonnet-5"
                />
                <Button loading={modelsBusy} onClick={() => void fetchModels()} title={t('ai.fetchModelsHint')}>
                  {t('ai.fetchModels')}
                </Button>
              </div>
            </label>
          </div>
          <div className="small muted" style={{ marginTop: '-0.3rem' }}>{t('ai.baseUrlHint')}</div>
          {modelsError && <Banner kind="error">{modelsError}</Banner>}
          {models && (
            <div className="row small" style={{ gap: '0.8rem', alignItems: 'center', flexWrap: 'wrap' }}>
              <span className="muted">{t('ai.modelsFound', { count: shownModels.length })}</span>
              {hiddenModels > 0 && (
                <Checkbox checked={showAllModels} onChange={(e) => setShowAllModels(e.target.checked)}>
                  {t('ai.modelsShowAll', { count: hiddenModels })}
                </Checkbox>
              )}
              {cur.model && !models.some((m) => m.id === cur.model) && (
                <Tag color="orange">{t('ai.modelNotListed', { model: cur.model })}</Tag>
              )}
            </div>
          )}
          {picked && (
            <div className="row small" style={{ gap: '0.5rem', alignItems: 'center' }}>
              <span>{t('ai.modelPickedHint', { model: cur.model })}</span>
              <Button
                size="small"
                type="primary"
                loading={busy}
                onClick={() => {
                  setPicked(false)
                  void runTest()
                }}
              >
                {t('ai.test')}
              </Button>
            </div>
          )}
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
          <div className="filters">
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
            <label style={{ maxWidth: '16rem' }}>
              {t('ai.timeout')}
              <InputNumber
                min={10}
                max={1800}
                value={cur.timeout_s}
                onChange={(v) => edit({ timeout_s: v ?? 90 })}
                title={t('ai.timeoutHint')}
                style={{ width: '100%' }}
              />
            </label>
          </div>
          {local && <div className="small muted">{t('ai.timeoutHint')}</div>}
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
              {test.ok ? '✓' : '✗'} {msg(test.text)}
            </div>
          )}

          <details style={{ marginTop: '0.5rem' }}>
            <summary>
              {t('ai.prompts')}
              {prompts.data?.prompts.some((p) => p.modified) && (
                <Tag color="orange" style={{ marginLeft: '0.5rem' }}>
                  {t('ai.promptModified')}
                </Tag>
              )}
            </summary>
            <div className="col" style={{ marginTop: '0.5rem' }}>
              <div className="small muted">{t('ai.promptsHint')}</div>
              <div className="filters">
                <label style={{ minWidth: '16rem' }}>
                  {t('ai.promptKind')}
                  <Select
                    value={promptKind}
                    onChange={(v: AIPrompt['kind']) => setPromptKind(v)}
                    options={[
                      { value: 'finding', label: t('ai.promptFinding') },
                      { value: 'map', label: t('ai.promptMap') },
                      { value: 'config', label: t('ai.promptConfig') },
                      { value: 'ip', label: t('ai.promptIp') },
                      { value: 'monitoring', label: t('ai.promptMonitoring') },
                    ]}
                  />
                </label>
                <label style={{ minWidth: '10rem' }}>
                  {t('ai.promptLang')}
                  <Select
                    value={promptLang}
                    onChange={(v: AIPrompt['lang']) => setPromptLang(v)}
                    options={[
                      { value: 'ru', label: 'Русский' },
                      { value: 'en', label: 'English' },
                    ]}
                  />
                </label>
              </div>
              {currentPrompt?.modified && (
                <div className="small" style={{ color: 'var(--status-warning)' }}>
                  {t('ai.promptModifiedHint')}
                </div>
              )}
              {/* Просмотр; правка — в окне с диффом перед записью. */}
              <pre className="diff mono small" style={{ whiteSpace: 'pre-wrap', margin: 0, maxHeight: '16rem', overflow: 'auto' }}>
                {currentPrompt?.text ?? ''}
              </pre>
              <div className="row" style={{ gap: '0.5rem' }}>
                <Button type="primary" disabled={!currentPrompt} onClick={() => setEditingPrompt(true)}>
                  {t('configs.edit')}
                </Button>
                <Button disabled={!currentPrompt?.modified} loading={busy} onClick={() => void reviewPrompt()}>
                  {t('ai.promptDiffTitle')}
                </Button>
                <Button disabled={!currentPrompt?.modified && !promptDirty} onClick={() => void savePrompt('')}>
                  {t('ai.promptReset')}
                </Button>
              </div>
            </div>
          </details>
          {editingPrompt && currentPrompt && (
            <EditTextModal
              title={`${t('ai.prompts')}: ${promptKind === 'finding' ? t('ai.promptFinding') : promptKind === 'map' ? t('ai.promptMap') : promptKind === 'ip' ? t('ai.promptIp') : promptKind === 'monitoring' ? t('ai.promptMonitoring') : t('ai.promptConfig')} (${promptLang})`}
              saved={currentPrompt.text}
              draft={promptText}
              onDraft={(v) => setPromptDrafts((d) => ({ ...d, [promptKey]: v }))}
              busy={busy}
              rows={16}
              below={<div className="small muted" style={{ marginTop: '0.4rem' }}>{t('ai.promptsHint')}</div>}
              onSave={() => savePrompt(promptText)}
              onClose={() => {
                setEditingPrompt(false)
                setPromptDrafts((d) => {
                  const next = { ...d }
                  delete next[promptKey]
                  return next
                })
              }}
            />
          )}
          {promptDiff && (
            <Modal title={t('ai.promptDiffTitle')} onClose={() => setPromptDiff(null)} width={900}>
              {promptDiff.toDefault ? (
                <p className="small muted">{t('ai.promptToDefault')}</p>
              ) : (
                <DiffView text={promptDiff.diff} />
              )}
              <div className="row" style={{ marginTop: '0.75rem', gap: '0.5rem' }}>
                <Button type="primary" loading={busy} onClick={() => void savePrompt(promptText)}>
                  {t('ai.promptApply')}
                </Button>
                <Button onClick={() => setPromptDiff(null)}>{t('common.cancel')}</Button>
              </div>
            </Modal>
          )}
        </div>
      )}
    </Card>
  )
}
