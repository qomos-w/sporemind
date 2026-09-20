import type { ReactNode } from 'react'
import { CardIcon } from '../ai/components/CardIcon'
import { resolveAppIcon } from '../ai/components/appIconResolver'
import type { WorkbenchCardDescriptor } from './cardTypes'

/**
 * Resolve a workbench card's host icon. App/plugin cards route through
 * {@link resolveAppIcon} (lucide name / plugin asset image / colored glyph);
 * every other card kind uses the card icon library.
 */
export function workbenchCardIcon(card: WorkbenchCardDescriptor, size: number): ReactNode {
  if (card.kind === 'app' || card.kind === 'plugin') {
    const appId = card.id.startsWith('app:') ? card.id.slice(4) : undefined
    return resolveAppIcon(card.icon || undefined, appId, size, card.color)
  }
  return <CardIcon name={card.icon} size={size} color={card.color ?? 'currentColor'} />
}
