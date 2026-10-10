import { Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { type OsKind, useOS } from '../osRegistry'
import type { OSInfo } from '../types'
import { OS_GLYPHS, type OsGlyph } from './osGlyphs'

// ID без своего значка → значок родственной ОС.
const ALIASES: Record<string, string> = {
  amzn: 'fedora',
  ol: 'rhel',
  'centos-stream': 'centos',
  raspbian: 'raspbian',
  'kali-rolling': 'kali',
  'pop-os': 'pop',
}

// Цвета, которые на тёмной теме не видны, — там значок цвета текста.
const DIM = new Set(['000000', '262577', '0D597F', '54487A'])

/** Значок ОС по os_info: свой, родственной ОС (like), пингвин для прочего
 * Linux, «?» — ОС неизвестна. */
export function osGlyph(os?: OSInfo | null): OsGlyph | null {
  if (!os?.id) return null
  const id = ALIASES[os.id] ?? os.id
  return OS_GLYPHS[id] ?? (os.like ? OS_GLYPHS[ALIASES[os.like] ?? os.like] : undefined) ?? (id === 'windows' ? null : OS_GLYPHS.linux)
}

/** Значок ОС перед именем хоста, машины или контейнера; подсказка — полное
 * имя и откуда оно известно. Догадка — полупрозрачный значок с «?». */
export function OsIcon({ os, size = 14, gap = '0.35em' }: { os?: OSInfo | null; size?: number; gap?: string | number }) {
  const { t } = useTranslation()
  const glyph = osGlyph(os)
  const guess = os?.source === 'guess'
  const name = os?.name || glyph?.title || t('os.unknown')
  const source = os?.source ? t(`os.source.${os.source}`, { defaultValue: os.source }) : ''
  const tip = guess ? t('os.guessTip', { name }) : source ? `${name} · ${source}` : name
  const style = { width: size, height: size, flex: 'none', verticalAlign: '-0.15em', marginRight: gap } as const
  return (
    <Tooltip title={tip}>
      <span className="os-icon-wrap" style={{ position: 'relative', display: 'inline-flex' }} aria-label={tip} role="img">
        {glyph ? (
          <svg
            viewBox="0 0 24 24"
            className={`os-icon${DIM.has(glyph.hex) ? ' os-icon-dim' : ''}`}
            style={{ ...style, opacity: guess ? 0.5 : 1, ['--os-color' as string]: `#${glyph.hex}` }}
          >
            <path d={glyph.path} />
          </svg>
        ) : (
          <svg viewBox="0 0 24 24" className="os-icon os-icon-unknown" style={style}>
            <circle cx="12" cy="12" r="10.5" fill="none" stroke="currentColor" strokeWidth="2" />
            <text x="12" y="16.5" textAnchor="middle" fontSize="13" fontWeight="700" fill="currentColor">
              ?
            </text>
          </svg>
        )}
        {guess && glyph && (
          <span className="os-icon-guess" style={{ position: 'absolute', right: '0.15em', bottom: '-0.25em', fontSize: size * 0.6, fontWeight: 700, lineHeight: 1 }}>
            ?
          </span>
        )}
      </span>
    </Tooltip>
  )
}

/** Значок ОС по имени из справочника (там, куда передаётся только имя);
 * ОС неизвестна справочнику — ничего. */
export function OsIconOf({ kind, name, hostID, size }: { kind: OsKind; name?: string | null; hostID?: number | null; size?: number }) {
  const os = useOS(kind, name, hostID)
  return os ? <OsIcon os={os} size={size} /> : null
}

/** Метка, по которой заголовок находит место имени: t('…', { name: NAME_MARK }). */
export const NAME_MARK = '\u0001'

/** Заголовок со значком ОС прямо перед именем: text — перевод с NAME_MARK
 * вместо имени. */
export function NamedTitle({ text, name, icon }: { text: string; name: ReactNode; icon: ReactNode }) {
  const i = text.indexOf(NAME_MARK)
  if (i < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      {icon}
      {name}
      {text.slice(i + NAME_MARK.length)}
    </>
  )
}

/** Заголовок окна «Консоль: имя» со значком ОС перед именем по справочнику
 * (kind + name). key — ключ перевода с параметром name. */
export function OsTitle({ tKey, kind, name, param = 'name' }: { tKey: string; kind: OsKind; name: string; param?: string }) {
  const { t } = useTranslation()
  return <NamedTitle text={t(tKey, { [param]: NAME_MARK })} name={name} icon={<OsIconOf kind={kind} name={name} />} />
}

/** Подпись варианта выпадающего списка: значок ОС и имя (поиск — по
 * полю search с тем же текстом). */
export function osLabel(os: OSInfo | null | undefined, text: string): ReactNode {
  return (
    <span className="nowrap">
      <OsIcon os={os} size={12} />
      {text}
    </span>
  )
}
