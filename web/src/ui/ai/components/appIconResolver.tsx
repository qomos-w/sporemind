import type { ReactNode } from 'react'
import { Puzzle } from 'lucide-react'
import { CARD_ICON_LIBRARY } from './cardIconLibrary'
import { getHttpUrl } from '../../../application/gateway'

const IMAGE_EXTENSIONS = ['.svg', '.png', '.jpg', '.jpeg', '.webp', '.gif', '.ico', '.bmp']

/**
 * Marker class applied only to a lucide glyph that carries an explicit bundle
 * color. Shared with componentIcon (omnibox bundle entries) so a single
 * dark-theme CSS rule (AIShellLayout.css) can lift exactly those glyphs.
 */
export const APP_ICON_COLORED_CLASS = 'app-icon-colored'

function isImageFileName(name: string): boolean {
  const lower = name.toLowerCase()
  return IMAGE_EXTENSIONS.some(ext => lower.endsWith(ext))
}

function isHttpUrl(name: string): boolean {
  return name.startsWith('http://') || name.startsWith('https://') || name.startsWith('/')
}

/**
 * Resolve an app/bundle icon string into a React icon node.
 *
 * Three cases:
 * 1. Lucide icon name (e.g. "shield-check") → <IconComponent />
 * 2. Image file name (e.g. "icon.png") → <img src={gateway/plugin/<appId>/<name>} />
 * 3. HTTP/path URL (e.g. "/icon.png" or "https://...") → <img src={url} />
 *
 * Falls back to <Puzzle /> when nothing matches.
 *
 * `color` (a bundle-declared hex) tints the lucide glyph only — image icons
 * are used verbatim (an <img> cannot be recolored). The tint is applied via an
 * inline style so it overrides the surrounding `currentColor`, and the glyph is
 * tagged with {@link APP_ICON_COLORED_CLASS} so the dark-theme CSS lift
 * (AIShellLayout.css) targets exactly the colored glyphs.
 */
export function resolveAppIcon(icon: string | undefined, appId?: string, size = 14, color?: string): ReactNode {
  if (!icon) return <Puzzle size={size} />

  // Image file name — resolve against the plugin asset URL.
  if (isImageFileName(icon) && !isHttpUrl(icon)) {
    if (!appId) return <Puzzle size={size} />
    const base = getHttpUrl().replace(/\/$/, '')
    const src = `${base}/plugin/${appId}/${icon}`
    return <img src={src} alt="" width={size} height={size} style={{ objectFit: 'contain', display: 'block' }} />
  }

  // Absolute URL or path — use as-is.
  if (isHttpUrl(icon)) {
    return <img src={icon} alt="" width={size} height={size} style={{ objectFit: 'contain', display: 'block' }} />
  }

  // Lucide icon name.
  const IconComponent = CARD_ICON_LIBRARY[icon]
  if (IconComponent) {
    return (
      <IconComponent
        size={size}
        className={color ? APP_ICON_COLORED_CLASS : undefined}
        style={color ? { color } : undefined}
      />
    )
  }

  return <Puzzle size={size} />
}