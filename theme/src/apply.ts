export type ThemeMode = 'dark' | 'light'

export const FONT_SIZE_MIN = 12
export const FONT_SIZE_MAX = 20
export const FONT_SIZE_DEFAULT = 16

export const HUE_MIN = 0
export const HUE_MAX = 360

// Saturation injected into the light-mode background primitives when a hue
// tint is set. Lightness stays on the original token so text/border contrast
// is untouched — the slider shifts only the colour of the page background.
const HUE_SATURATION = 0.1

// Light-theme background primitives retinted by the hue slider. Hex values
// mirror tokens.css `[data-theme="light"]`; surfaces/elevated stay near-white.
const LIGHT_BACKGROUND_TOKENS = [
  ['--_bg-base', '#f7f7f7'],
  ['--_bg-surface', '#f2f1ef'],
  ['--_bg-elevated', '#ffffff'],
  ['--_bg-overlay', '#e8e8e8'],
  ['--_bg-hover', '#e6e6e6'],
  ['--_bg-subtle', '#f4f4f4'],
] as const

export interface ThemeState {
  mode: ThemeMode
  fontSize: number
  /** Light-mode background hue tint in degrees (0–360). Absent = neutral
   *  default palette; ignored in dark mode. */
  hue?: number
}

function hexToRgb(hex: string): [number, number, number] {
  const value = hex.replace('#', '')
  const num = parseInt(value, 16)
  return [(num >> 16) & 0xff, (num >> 8) & 0xff, num & 0xff]
}

function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
  const rn = r / 255
  const gn = g / 255
  const bn = b / 255
  const max = Math.max(rn, gn, bn)
  const min = Math.min(rn, gn, bn)
  const l = (max + min) / 2
  const d = max - min
  if (d === 0) return [0, 0, l]
  const s = d / (1 - Math.abs(2 * l - 1))
  let h: number
  if (max === rn) h = ((gn - bn) / d) % 6
  else if (max === gn) h = (bn - rn) / d + 2
  else h = (rn - gn) / d + 4
  return [h * 60, s, l]
}

/** Recolour a hex token with the slider hue at the token's own lightness. */
function tinted(hex: string, hue: number): string {
  const [r, g, b] = hexToRgb(hex)
  const [, , l] = rgbToHsl(r, g, b)
  return `hsl(${hue} ${HUE_SATURATION * 100}% ${Math.round(l * 100)}%)`
}

export function applyTheme(state: ThemeState): void {
  const el = document.documentElement
  el.setAttribute('data-theme', state.mode)
  el.style.fontSize = `${state.fontSize}px`
  const useTint = state.mode === 'light' && state.hue != null && Number.isFinite(state.hue)
  const hue = useTint ? Math.min(HUE_MAX, Math.max(HUE_MIN, Math.round(state.hue!))) : 0
  for (const [name, hex] of LIGHT_BACKGROUND_TOKENS) {
    if (useTint) {
      el.style.setProperty(name, tinted(hex, hue))
    } else {
      el.style.removeProperty(name)
    }
  }
}
