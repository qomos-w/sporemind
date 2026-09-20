import type { MonoCardListItem } from '../../../domain/mono-types'
import { isLucideIconName } from './cardIconLibrary'

export type CardAccent = 'blue' | 'green' | 'amber' | 'red' | 'purple' | 'pink' | 'slate' | 'cyan'
export type CardEmphasis = 'subtle' | 'normal' | 'strong'
export type CardIconType = 'lucide' | 'emoji'

export interface CardVisual {
  icon?: string
  iconType?: CardIconType
  accent?: CardAccent
  emphasis?: CardEmphasis
  /** Optional explicit icon color; falls back to accent-derived color. */
  color?: string
  background?: string
  border?: string
  /** Topology node radius (px). Only affects node display. */
  size?: number
}

export const CARD_NODE_SIZE_MIN = 12
export const CARD_NODE_SIZE_MAX = 48
export const CARD_NODE_SIZE_DEFAULT = 24

export interface CardVisualStyle {
  background: string
  border: string
  title: string
  icon: string
  borderWidth: number
  shadow: string
}

export const CARD_ACCENTS: CardAccent[] = ['blue', 'green', 'amber', 'red', 'purple', 'pink', 'slate', 'cyan']
export const CARD_EMPHASIS: CardEmphasis[] = ['subtle', 'normal', 'strong']

const ACCENT_STYLES: Record<CardAccent, { light: [string, string, string]; dark: [string, string, string] }> = {
  blue: { light: ['#eff6ff', '#2563eb', '#1d4ed8'], dark: ['#172554', '#60a5fa', '#bfdbfe'] },
  green: { light: ['#f0fdf4', '#16a34a', '#15803d'], dark: ['#052e16', '#4ade80', '#bbf7d0'] },
  amber: { light: ['#fffbeb', '#d97706', '#b45309'], dark: ['#451a03', '#fbbf24', '#fde68a'] },
  red: { light: ['#fef2f2', '#dc2626', '#b91c1c'], dark: ['#450a0a', '#f87171', '#fecaca'] },
  purple: { light: ['#faf5ff', '#9333ea', '#7e22ce'], dark: ['#3b0764', '#c084fc', '#e9d5ff'] },
  pink: { light: ['#fdf2f8', '#db2777', '#be185d'], dark: ['#500724', '#f472b6', '#fbcfe8'] },
  slate: { light: ['#f8fafc', '#475569', '#334155'], dark: ['#1e293b', '#94a3b8', '#e2e8f0'] },
  cyan: { light: ['#ecfeff', '#0891b2', '#0e7490'], dark: ['#083344', '#22d3ee', '#a5f3fc'] },
}

// Hex used for preset swatches, shared by the icon-color and background-color
// rows so both render identical swatches. Sourced from ACCENT_STYLES (light
// border color) to stay in sync with the palette instead of duplicating hex.
export const CARD_ACCENT_PRESET_HEX: Record<CardAccent, string> = Object.fromEntries(
  CARD_ACCENTS.map(a => [a, ACCENT_STYLES[a].light[1]])
) as Record<CardAccent, string>

/**
 * Route an icon name to its type. Lucide identifiers live in CARD_ICON_LIBRARY;
 * everything else (legacy glyphs, emoji) is treated as an emoji span.
 */
export function routeIconType(icon: string | undefined): CardIconType | undefined {
  if (!icon) return undefined
  return isLucideIconName(icon) ? 'lucide' : 'emoji'
}

export function cardVisual(card: Pick<MonoCardListItem, 'data'>): CardVisual {
  const value = card.data?.visual
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const raw = value as Record<string, unknown>
  const icon = typeof raw.icon === 'string' ? raw.icon : undefined
  // iconType is derived from the icon value, never read from storage. This
  // keeps the persisted shape minimal (only `icon`) and avoids drift when
  // a name is added to or removed from the lucide library.
  const iconType = routeIconType(icon)
  const matchedAccent = CARD_ACCENTS.includes(raw.accent as CardAccent) ? raw.accent as CardAccent : undefined
  // 'slate' is the default accent (defaultCardVisual().accent). Normalising it
  // to undefined makes "default accent" semantically equal to "no custom
  // accent", so a card carrying only `accent: 'slate'` (e.g. left over after
  // clearing other visual fields) is treated as having no custom visual and
  // the topology border/background fall back to type/agent/status colours.
  const accent = matchedAccent === 'slate' ? undefined : matchedAccent
  const emphasis = CARD_EMPHASIS.includes(raw.emphasis as CardEmphasis) ? raw.emphasis as CardEmphasis : undefined
  return {
    icon,
    iconType,
    accent,
    emphasis,
    color: typeof raw.color === 'string' && raw.color !== '' ? raw.color : undefined,
    background: typeof raw.background === 'string' && raw.background !== '' ? raw.background : undefined,
    border: typeof raw.border === 'string' && raw.border !== '' ? raw.border : undefined,
    size: typeof raw.size === 'number' && Number.isFinite(raw.size) ? raw.size : undefined,
  }
}

export function getCardVisualStyle(visual: CardVisual, theme?: 'light' | 'dark'): CardVisualStyle {
  const isDark = theme ? theme === 'dark' : document.documentElement.getAttribute('data-theme') === 'dark'
  const [lightBackground, lightBorder, lightTitle] = ACCENT_STYLES[visual.accent ?? 'slate'].light
  const [darkBackground, darkBorder, darkTitle] = ACCENT_STYLES[visual.accent ?? 'slate'].dark
  const emphasis = visual.emphasis ?? 'normal'
  const background = visual.background ?? (isDark ? darkBackground : lightBackground)
  const border = visual.border ?? (isDark ? darkBorder : lightBorder)
  // Icon color is driven solely by the icon-color picker: an explicit `color`
  // wins, otherwise a fixed neutral grey is used. It must NOT derive from the
  // accent/background so that switching the background never recolors the icon.
  const iconColor = visual.color ?? '#64748b'
  return {
    background: emphasis === 'subtle' ? (isDark ? `color-mix(in srgb, ${background} 55%, #0f172a)` : `color-mix(in srgb, ${background} 65%, white)`) : background,
    border,
    title: isDark ? darkTitle : lightTitle,
    icon: iconColor,
    borderWidth: emphasis === 'strong' ? 2 : 1,
    shadow: emphasis === 'strong' ? `0 2px 8px color-mix(in srgb, ${border} 24%, transparent)` : 'none',
  }
}

export function defaultCardVisual(): Required<Pick<CardVisual, 'icon' | 'iconType' | 'accent' | 'emphasis'>> {
  return { icon: 'file-text', iconType: 'lucide', accent: 'slate', emphasis: 'normal' }
}
