import { useEffect, useRef, useState } from 'react'
import { Button, Space, Tag } from 'antd'
import { useTranslation } from 'react-i18next'
import { api, useApi } from '../api'
import { Banner, Loading, Modal } from './ui'
import { confirmAction } from './confirm'
import { msg, tx, type Msg } from '../msg'

interface SudoSource {
  file: string
  line: number
  text: string
  kind: 'user' | 'group' | 'all' | 'alias' | 'nkt'
  removable: boolean
}

/** Живая проверка на хосте (GET …/sudo делает её сам). */
interface SudoCheck {
  status: string
  hub_key: boolean
  narrow_rule: boolean
  full: boolean
  full_rules?: string[]
  sources?: SudoSource[]
  password?: string
}

interface SudoInfo {
  check?: SudoCheck
  check_error?: string
  status_changed?: boolean
  status: string
  user: string
  ops: string[]
  rule: string
  rule_path: string
  key_path: string
  command: string
}

/** «Что разрешено хабу через sudo» на хосте: состояние, операции
 * hub-sudo, правило sudoers; «сузить» полный sudo и «снять правило». */
export function SudoInfoModal({ hostId, hostName, onClose, onChanged }: { hostId: number; hostName: string; onClose: () => void; onChanged: () => void }) {
  const { t } = useTranslation()
  // Проверка sudo, сужение и снятие правила — несколько обходов по SSH
  // подряд: на медленном хосте это дольше обычных 30 секунд.
  const info = useApi<SudoInfo>(`/hub/hosts/${hostId}/sudo`, 0, 120_000)
  const [busy, setBusy] = useState<string | null>(null)
  const [note, setNote] = useState<{ kind: 'info' | 'error'; text: Msg } | null>(null)
  const d = info.data
  const c = d?.check
  // Правило nkt уже узкое, полный sudo даёт чужое.
  const narrowedOther = !!c && c.full && c.narrow_rule && c.hub_key

  // Проверка нашла другое состояние, чем помнил хаб, — значок в списке
  // хостов тоже обновить.
  // onChanged от родителя — новая функция на каждую отрисовку: по ссылке
  // её не ждём, иначе перечитывание списка снова вызывало бы эффект.
  const changedRef = useRef(onChanged)
  changedRef.current = onChanged
  useEffect(() => {
    if (d?.status_changed) changedRef.current()
  }, [d])

  async function disableRule(s: SudoSource) {
    const pw = c?.password === 'P' ? '' : '\n\n' + t('sudo.noPasswordWarn', { user: d?.user })
    if (!(await confirmAction(t('sudo.confirmRuleOff', { file: s.file, line: s.line, text: s.text, user: d?.user }) + pw))) return
    setBusy(`rule:${s.file}:${s.line}`)
    setNote(null)
    try {
      await api(`/hub/hosts/${hostId}/sudo/rule-off`, { method: 'POST', body: { file: s.file, line: s.line, text: s.text }, timeoutMs: 120_000 })
      setNote({ kind: 'info', text: tx('sudo.ruleOffDone', { file: s.file }) })
      onChanged()
      await info.reload()
    } catch (err) {
      setNote({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  async function act(kind: 'narrow' | 'remove') {
    if (kind === 'remove' && !(await confirmAction(t('hosts.confirmRemoveSudo', { user: d?.user, name: hostName })))) return
    if (kind === 'narrow' && !(await confirmAction(t('sudo.confirmNarrow', { user: d?.user, name: hostName }), { danger: false }))) return
    setBusy(kind)
    setNote(null)
    try {
      if (kind === 'narrow') {
        const res = await api<{ mode: string }>(`/hub/hosts/${hostId}/sudo/narrow`, { method: 'POST', timeoutMs: 120_000 })
        setNote({
          kind: 'info',
          text: res.mode === 'narrow' ? t('sudo.narrowed') : res.mode === 'already' ? t('sudo.alreadyNarrow') : t('sudo.narrowedOtherRule'),
        })
      } else {
        await api(`/hub/hosts/${hostId}/sudo/remove`, { method: 'POST', timeoutMs: 120_000 })
        setNote({ kind: 'info', text: tx('sudo.removed') })
      }
      onChanged()
      await info.reload()
    } catch (err) {
      setNote({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal title={t('sudo.title', { name: hostName })} onClose={onClose} width={680}>
      {!d ? (
        <Loading />
      ) : (
        <div className="col" style={{ gap: '0.6rem' }}>
          <p className="small" style={{ margin: 0 }}>
            <strong>{narrowedOther ? t('sudo.state.nopasswdOther') : t(`sudo.state.${d.status || 'unknown'}`, { defaultValue: d.status })}</strong>
          </p>
          {d.check_error && <Banner kind="warn">{t('sudo.checkFailed', { error: d.check_error })}</Banner>}
          {c?.full && (c.sources ?? []).length > 0 && (
            <div>
              <div className="small" style={{ marginBottom: '0.25rem' }}>
                <strong>{t('sudo.sourcesTitle')}</strong>
              </div>
              <div className="col" style={{ gap: '0.35rem' }}>
                {(c.sources ?? []).map((s) => (
                  <div key={`${s.file}:${s.line}`} className="sudo-source">
                    <div className="small">
                      <span className="mono">
                        {s.file}:{s.line}
                      </span>{' '}
                      <Tag color={s.kind === 'nkt' ? 'blue' : s.kind === 'user' ? 'orange' : 'red'}>{t(`sudo.kind.${s.kind}`)}</Tag>
                    </div>
                    <pre className="mono small" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{s.text}</pre>
                    {s.removable ? (
                      <Button size="small" danger loading={busy === `rule:${s.file}:${s.line}`} disabled={!!busy} onClick={() => void disableRule(s)}>
                        {t('sudo.ruleOff')}
                      </Button>
                    ) : (
                      s.kind !== 'nkt' && <div className="small muted">{t(`sudo.manual.${narrowedOther ? s.kind : 'notNarrowed'}`)}</div>
                    )}
                  </div>
                ))}
              </div>
              {c.password && c.password !== 'P' && <div className="small muted" style={{ marginTop: '0.3rem' }}>{t('sudo.noPasswordWarn', { user: d.user })}</div>}
            </div>
          )}
          {c?.full && (c.sources ?? []).length === 0 && (c.full_rules ?? []).length > 0 && (
            <div>
              <div className="small">
                <strong>{t('sudo.sourcesUnknown')}</strong>
              </div>
              <pre className="mono small" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{(c.full_rules ?? []).join('\n')}</pre>
            </div>
          )}
          <p className="small muted" style={{ margin: 0 }}>
            {t('sudo.explain')}
          </p>
          {note && <Banner kind={note.kind}>{msg(note.text)}</Banner>}
          <div>
            <div className="small" style={{ marginBottom: '0.25rem' }}>
              <strong>{t('sudo.opsTitle', { command: d.command })}</strong>
            </div>
            <ul className="small" style={{ margin: 0, paddingLeft: '1.2rem' }}>
              {d.ops.map((op) => (
                <li key={op}>
                  <span className="mono">{op}</span> — {t(`sudo.op.${op}`, { defaultValue: op })}
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="small" style={{ marginBottom: '0.25rem' }}>
              <strong>{t('sudo.ruleTitle', { path: d.rule_path })}</strong>
            </div>
            <pre className="mono small" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{d.rule}</pre>
            <div className="small muted">{t('sudo.keyNote', { path: d.key_path })}</div>
          </div>
          <Space wrap>
            {d.status === 'nopasswd' && !narrowedOther && (
              <Button type="primary" loading={busy === 'narrow'} disabled={!!busy} onClick={() => void act('narrow')}>
                {t('sudo.narrow')}
              </Button>
            )}
            {(d.status === 'nopasswd' || d.status === 'narrow') && (
              <Button danger loading={busy === 'remove'} disabled={!!busy} onClick={() => void act('remove')}>
                {t('sudo.remove')}
              </Button>
            )}
            <Button loading={info.loading && !!d} disabled={!!busy} onClick={() => void info.reload()}>
              {t('sudo.recheck')}
            </Button>
          </Space>
        </div>
      )}
    </Modal>
  )
}
