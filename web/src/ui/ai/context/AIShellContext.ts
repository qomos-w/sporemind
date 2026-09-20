import { createContext, useContext } from 'react'
import type { ModelUnit } from '../../../domain/types'
import type { ProviderOption, ProviderGroup, ComposerSlotMenuConfig } from '../components/AIComposer'
import type { ThinkingLevel } from '../../../gen-clients/system/types'
import type { SlotRoute } from '../hooks/modelSlot'

export interface AIShellContextValue {
  /** Flat model list — used by the legacy/new-chat selector. Empty when the
   *  grouped selector (groups) is in use. */
  providers: ProviderOption[]
  /** Grouped routes (auto pool + custom aggregators) for the conversation
   *  top-bar selector. When present, AIComposer renders the two-level dropdown. */
  groups?: ProviderGroup[]
  /** Active routing form derived from the primary slot (grouped mode). */
  activeRoute?: SlotRoute
  /** The model unit the aggregator actually resolved for the current dispatch
   *  (β feedback channel). Null when unknown (e.g. idle / not yet dispatched). */
  currentUnit?: ModelUnit | null
  activeUnit: ModelUnit | null
  activeProviderId: string
  onProviderChange: (id: string) => void
  /** Grouped-mode selection handlers. */
  onSelectRoute?: (routeId: string) => void
  onSelectUnit?: (routeId: string, option: ProviderOption) => void
  thinkingLevels: Map<string, ThinkingLevel>
  activeThinkingLevel: ThinkingLevel | null
  onThinkingLevelChange: (level: ThinkingLevel) => void
  onOpenFile?: (filePath: string, diffContent?: string, line?: number, lineEnd?: number) => void
  /** Open an image (data URL) in the right-panel image viewer tab. */
  onOpenImage?: (src: string, label?: string) => void
  /** Open a wiki card by ID in the right panel. */
  onOpenCard?: (cardId: string) => void
  /** Open a wiki card by ID in the right panel, starting in edit mode. */
  onOpenCardForEdit?: (cardId: string) => void
  /** Open a temporary read-only text tab in the right panel (e.g. goal text). */
  onOpenText?: (label: string, text: string) => void
  /** Right-click provider-button slot menu wiring: the five agent model slots
   *  (from the active agent), the composer's provider options, and the
   *  onSelectSlot persist callback. Undefined when there is no active agent, so
   *  the composer keeps the provider button left-click-only (right-click opens
   *  nothing). */
  slotMenu?: ComposerSlotMenuConfig
}

export const AIShellContext = createContext<AIShellContextValue | null>(null)

export function useAIShellContext(): AIShellContextValue {
  const ctx = useContext(AIShellContext)
  if (!ctx) throw new Error('useAIShellContext must be used within AIShellContext.Provider')
  return ctx
}
