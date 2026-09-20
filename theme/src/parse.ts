import type { ThemeMode, ThemeState } from './apply'
import { FONT_SIZE_DEFAULT, FONT_SIZE_MAX, FONT_SIZE_MIN, HUE_MAX, HUE_MIN } from './apply'

export const defaultTheme: ThemeState = { mode: 'light', fontSize: FONT_SIZE_DEFAULT }

export function parseThemeItemId(itemId: string): ThemeState | null {
  const match = itemId.match(/^view\.theme\.(dark|light)$/)
  if (!match) return null
  return { mode: match[1] as ThemeMode, fontSize: FONT_SIZE_DEFAULT }
}

export function parseThemeJSON(raw: string | null | undefined): ThemeState {
  if (!raw) return defaultTheme
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const mode = parsed.mode === 'dark' || parsed.mode === 'light' ? parsed.mode : defaultTheme.mode
    const fontSize =
      typeof parsed.fontSize === 'number' && parsed.fontSize >= FONT_SIZE_MIN && parsed.fontSize <= FONT_SIZE_MAX
        ? parsed.fontSize
        : FONT_SIZE_DEFAULT
    const theme: ThemeState = { mode, fontSize }
    if (typeof parsed.hue === 'number' && Number.isFinite(parsed.hue)) {
      theme.hue = Math.min(HUE_MAX, Math.max(HUE_MIN, Math.round(parsed.hue)))
    }
    return theme
  } catch {
    // fall through to default on malformed JSON
  }
  return defaultTheme
}
