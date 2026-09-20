import React from 'react'
import type { MonoCardListItem } from '../../../gen-clients/system/types'
import type { I18nKey } from '../../../i18n/types'
import { APP_ICON_COLORED_CLASS } from './appIconResolver'
import { CardIcon } from './CardIcon'
import { cardVisual } from './cardVisual'

// Strip the leading "builtin:<kind>:" namespace so the omnibox shows a clean
// token instead of the raw card id (e.g. "builtin:mode:goal" -> "goal").
export function componentLabel(card: MonoCardListItem): string {
  const title = typeof card.Data?.title === 'string' ? card.Data.title.trim() : ''
  if (title) return title
  return card.Id.replace(/^builtin:[a-z]+:/, '')
}

// Canonical hyphenated slash spelling of a component card: the
// namespace-stripped, lowercased card id ("builtin:bundle:web-search" ->
// "web-search"). Slash input is space-delimited, so a spaced title ("Web
// Search") can never be typed as one token; the slug is the space-free form
// the user can actually type after "/".
export function componentSlug(card: MonoCardListItem): string {
  return card.Id.replace(/^(?:builtin:[a-z]+:|app-bundle:)/, '').toLowerCase()
}

// Separator-free compact form ("web-search" -> "websearch") so concatenated
// slash queries still match spaced or hyphenated names.
export function compactToken(s: string): string {
  return s.replace(/[\s_-]+/g, '').toLowerCase()
}

// Resolve a display icon for a bundle/mode card. Renders a colored lucide glyph
// via the card icon library (data.visual.icon + data.visual.color), consistent
// with topology nodes and composer badges. The caller-supplied fallback is used
// only when the card declares no icon.
export function componentIcon(card: MonoCardListItem, fallback: React.ReactNode): React.ReactNode {
  const visual = cardVisual({ data: card.Data })
  const iconName = visual.icon ?? (typeof card.Data?.icon === 'string' ? card.Data.icon.trim() : '')
  if (!iconName) return fallback
  return <CardIcon name={iconName} size={16} color={visual.color} className={visual.color ? APP_ICON_COLORED_CLASS : undefined} />
}

// Show a clean skill name in the omnibox while preserving namespaced IDs for dispatch.
export function skillLabel(name: string): string {
  return name.replace(/^\//, '').replace(/^ext-skill:[^:]+:/, '')
}

export interface ComponentI18nKeys {
  name: I18nKey
  desc: I18nKey
}

export type Translator = (key: I18nKey) => string

// Builtin bundle/mode cards carry English titles in the backend card domain.
// This catalog localizes their display name and short intro for composer
// surfaces (slash menu, mount badges). Keyed by card id so mcp:* and
// app-bundle:* cards fall through to their own titles.
export const COMPONENT_I18N: Record<string, ComponentI18nKeys> = {
  'builtin:bundle:app-tools': { name: 'component.bundle.app-tools.name', desc: 'component.bundle.app-tools.desc' },
  'builtin:bundle:browser-crawl': { name: 'component.bundle.browser-crawl.name', desc: 'component.bundle.browser-crawl.desc' },
  'builtin:bundle:browser-tools': { name: 'component.bundle.browser-tools.name', desc: 'component.bundle.browser-tools.desc' },
  'builtin:bundle:browser-use': { name: 'component.bundle.browser-use.name', desc: 'component.bundle.browser-use.desc' },
  'builtin:bundle:bundle-use': { name: 'component.bundle.bundle-use.name', desc: 'component.bundle.bundle-use.desc' },
  'builtin:bundle:computeruse-tools': { name: 'component.bundle.computeruse-tools.name', desc: 'component.bundle.computeruse-tools.desc' },
  'builtin:bundle:coordinator-wearable': { name: 'component.bundle.coordinator-wearable.name', desc: 'component.bundle.coordinator-wearable.desc' },
  'builtin:bundle:debug': { name: 'component.bundle.debug.name', desc: 'component.bundle.debug.desc' },
  'builtin:bundle:file-tools': { name: 'component.bundle.file-tools.name', desc: 'component.bundle.file-tools.desc' },
  'builtin:bundle:fork-dream': { name: 'component.bundle.fork-dream.name', desc: 'component.bundle.fork-dream.desc' },
  'builtin:bundle:fork-explore': { name: 'component.bundle.fork-explore.name', desc: 'component.bundle.fork-explore.desc' },
  'builtin:bundle:fork-general': { name: 'component.bundle.fork-general.name', desc: 'component.bundle.fork-general.desc' },
  'builtin:bundle:fork-review': { name: 'component.bundle.fork-review.name', desc: 'component.bundle.fork-review.desc' },
  'builtin:bundle:git-tools': { name: 'component.bundle.git-tools.name', desc: 'component.bundle.git-tools.desc' },
  'builtin:bundle:goal': { name: 'component.bundle.goal.name', desc: 'component.bundle.goal.desc' },
  'builtin:bundle:image-gen': { name: 'component.bundle.image-gen.name', desc: 'component.bundle.image-gen.desc' },
  'builtin:bundle:image-recognition': { name: 'component.bundle.image-recognition.name', desc: 'component.bundle.image-recognition.desc' },
  'builtin:bundle:interface-controls': { name: 'component.bundle.interface-controls.name', desc: 'component.bundle.interface-controls.desc' },
  'builtin:bundle:tutor': { name: 'component.bundle.tutor.name', desc: 'component.bundle.tutor.desc' },
  'builtin:bundle:plugin-dev': { name: 'component.bundle.plugin-dev.name', desc: 'component.bundle.plugin-dev.desc' },
  'builtin:bundle:planning': { name: 'component.bundle.planning.name', desc: 'component.bundle.planning.desc' },
  'builtin:bundle:project-wiki': { name: 'component.bundle.project-wiki.name', desc: 'component.bundle.project-wiki.desc' },
  'builtin:bundle:scheduler': { name: 'component.bundle.scheduler.name', desc: 'component.bundle.scheduler.desc' },
  'builtin:bundle:shell-tools': { name: 'component.bundle.shell-tools.name', desc: 'component.bundle.shell-tools.desc' },
  'builtin:bundle:ssh-tools': { name: 'component.bundle.ssh-tools.name', desc: 'component.bundle.ssh-tools.desc' },
  'builtin:bundle:video-gen': { name: 'component.bundle.video-gen.name', desc: 'component.bundle.video-gen.desc' },
  'builtin:bundle:web-search': { name: 'component.bundle.web-search.name', desc: 'component.bundle.web-search.desc' },
  'builtin:bundle:workbench-attention': { name: 'component.bundle.workbench-attention.name', desc: 'component.bundle.workbench-attention.desc' },
  'builtin:bundle:workflow-tools': { name: 'component.bundle.workflow-tools.name', desc: 'component.bundle.workflow-tools.desc' },
  'builtin:bundle:worktree': { name: 'component.bundle.worktree.name', desc: 'component.bundle.worktree.desc' },
  'builtin:bundle:workspace-tools': { name: 'component.bundle.workspace-tools.name', desc: 'component.bundle.workspace-tools.desc' },
  'builtin:mode:goal': { name: 'component.mode.goal.name', desc: 'component.mode.goal.desc' },
  'builtin:mode:memory': { name: 'component.mode.memory.name', desc: 'component.mode.memory.desc' },
  'builtin:mode:scheduler': { name: 'component.mode.scheduler.name', desc: 'component.mode.scheduler.desc' },
  'builtin:mode:workflow': { name: 'component.mode.workflow.name', desc: 'component.mode.workflow.desc' },
  'builtin:mode:worktree': { name: 'component.mode.worktree.name', desc: 'component.mode.worktree.desc' },
}

// Localized display name for a bundle/mode card; non-builtin cards fall back
// to their own title via componentLabel.
export function localizedComponentLabel(card: MonoCardListItem, t: Translator): string {
  const keys = COMPONENT_I18N[card.Id]
  return keys ? t(keys.name) : componentLabel(card)
}

// Localized short intro; undefined when the card has no builtin entry, so the
// caller keeps its own fallback (e.g. the generic mount hint).
export function localizedComponentDescription(card: MonoCardListItem, t: Translator): string | undefined {
  const keys = COMPONENT_I18N[card.Id]
  return keys ? t(keys.desc) : undefined
}

// Localize a mounted badge title by card id, passing non-builtin ids through.
export function localizedCardTitle(cardId: string, fallback: string, t: Translator): string {
  const keys = COMPONENT_I18N[cardId]
  return keys ? t(keys.name) : fallback
}
