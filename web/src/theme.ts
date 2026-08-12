import { theme as antdTheme, type ThemeConfig } from 'antd'

/** Mirrors App.tsx's own Theme type (kept separate to avoid a circular
 * import between the two). */
export type Theme = 'light' | 'dark' | 'auto'

/** Resolves 'auto' against the OS preference — 'light'/'dark' are already
 * resolved by definition. */
export function resolveIsDark(theme: Theme): boolean {
  if (theme === 'dark') return true
  if (theme === 'light') return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

/** styles.css's own custom properties → antd ConfigProvider tokens. Kept to
 * a small, explicit set (not a full palette scrape) — these are the tokens
 * antd's own components actually read for chrome (surfaces, text, borders,
 * status colors, radius); the chart palettes (--series-N, --seq-N) have no
 * antd token equivalent and are passed straight to the still-custom chart
 * components instead, unchanged by this migration. */
const CSS_VAR_TO_TOKEN: Record<string, string> = {
  '--status-critical': 'colorError',
  '--status-warning': 'colorWarning',
  '--status-good': 'colorSuccess',
  '--surface-page': 'colorBgLayout',
  '--surface-1': 'colorBgContainer',
  '--surface-raised': 'colorBgElevated',
  '--text-primary': 'colorText',
  '--text-secondary': 'colorTextSecondary',
  '--text-muted': 'colorTextTertiary',
  // --border is the lighter of the two (rgba alpha .1) — antd's own
  // colorBorderSecondary is the subtler divider color, colorBorder the more
  // prominent one, matching --border-strong (alpha .18).
  '--border-strong': 'colorBorder',
  '--border': 'colorBorderSecondary',
}

/**
 * Builds an antd ThemeConfig from styles.css's own CSS custom properties,
 * so antd-rendered components and the still-custom CSS this migration
 * hasn't reached yet (charts, BlockTree, Topology, PathPicker) share one
 * palette for the whole migration's duration instead of visibly drifting.
 * styles.css stays the single source of truth — this only reads it.
 *
 * Call after `data-theme` has already been written to <html> (see
 * useTheme in App.tsx) — getComputedStyle needs the value that mutation
 * just produced, and a DOM attribute write is synchronous, so calling this
 * immediately afterward (not deferred to a later tick) is correct, not a
 * race.
 */
export function buildAntdTheme(isDark: boolean): ThemeConfig {
  const style = getComputedStyle(document.documentElement)
  const token: Record<string, string | number> = {}

  for (const [cssVar, antdToken] of Object.entries(CSS_VAR_TO_TOKEN)) {
    const value = style.getPropertyValue(cssVar).trim()
    if (value) token[antdToken] = value
  }

  const primary = style.getPropertyValue('--series-1').trim()
  if (primary) token.colorPrimary = primary

  const radius = parseFloat(style.getPropertyValue('--radius'))
  if (!Number.isNaN(radius)) token.borderRadius = radius
  const radiusSm = parseFloat(style.getPropertyValue('--radius-sm'))
  if (!Number.isNaN(radiusSm)) token.borderRadiusSM = radiusSm

  const font = style.getPropertyValue('--font').trim()
  if (font) token.fontFamily = font
  const mono = style.getPropertyValue('--mono').trim()
  if (mono) token.fontFamilyCode = mono

  return {
    algorithm: isDark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token,
  }
}
