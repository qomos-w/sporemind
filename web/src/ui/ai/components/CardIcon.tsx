import type { CSSProperties } from 'react'
import { HelpCircle } from 'lucide-react'
import { CARD_ICON_LIBRARY } from './cardIconLibrary'
import { routeIconType, type CardIconType } from './cardVisual'

export interface CardIconProps {
  name?: string
  iconType?: CardIconType
  size?: number
  color?: string
  strokeWidth?: number
  className?: string
  style?: CSSProperties
}

export function CardIcon({ name, iconType, size = 20, color = 'currentColor', strokeWidth = 2, className, style }: CardIconProps) {
  // iconType is optional; when omitted, derive it from the name so callers
  // never need to track lucide vs emoji themselves.
  const resolved = iconType ?? routeIconType(name)
  if (resolved === 'emoji' && name) {
    return <span className={className} style={{ ...style, fontSize: size, lineHeight: 1 }} aria-hidden="true">{name}</span>
  }
  const Icon = name ? CARD_ICON_LIBRARY[name] ?? HelpCircle : HelpCircle
  return <Icon size={size} color={color} strokeWidth={strokeWidth} className={className} style={style} aria-hidden="true" />
}
