import { createContext, useContext } from 'react'
import type { TimelineExpansionMode } from '../components/timeline/types'
import { DEFAULT_UI_EXPAND_DURATION_MS, DEFAULT_UI_EXPANSION_MODES, type UiExpandAction } from '../../../application/workspace-ui-state'

/** Global smoothing / expansion preferences exposed to streaming components. */
export interface SmoothStreamContextValue {
  /** Master toggle for all stream smoothing (text reveal + scroll follow). */
  smoothStream: boolean
  /** Whether the conversation turn is currently streaming. Components that
   *  mount mid-stream can use this to distinguish fresh frames from
   *  history-loaded ones. */
  isStreaming: boolean
  /** Expansion choreography per expandable UI kind (reasoning / edit / bash
   *  / search / ...). Merged over DEFAULT_UI_EXPANSION_MODES by the loader. */
  expansionModes: Record<string, UiExpandAction>
  /** Duration (ms) of the shared grid-rows expand/collapse transition. */
  expandDurationMs: number
}

export const SmoothStreamContext = createContext<SmoothStreamContextValue>({
  smoothStream: true,
  isStreaming: false,
  expansionModes: DEFAULT_UI_EXPANSION_MODES,
  expandDurationMs: DEFAULT_UI_EXPAND_DURATION_MS,
})

export function useSmoothStreamContext(): SmoothStreamContextValue {
  return useContext(SmoothStreamContext)
}

/** Map a configured four-state action onto the controller expansion mode.
 *  'none' (不展开) maps to 'manual': no automatic expansion, the user can
 *  still toggle the body open. */
export function actionToExpansionMode(action: UiExpandAction): TimelineExpansionMode {
  switch (action) {
    case 'fold-expand-collapse': return 'auto-while-active'
    case 'fold-expand': return 'auto-expand-once'
    case 'always-open': return 'open'
    case 'none': return 'manual'
  }
}

/** Resolve the configured action for a UI kind with default fallback. */
export function resolveUiExpansionAction(modes: Record<string, UiExpandAction>, kind: string): UiExpandAction {
  return modes[kind] ?? DEFAULT_UI_EXPANSION_MODES[kind] ?? 'fold-expand-collapse'
}

/** Hook: the controller-ready expansion mode for a UI kind. */
export function useUiExpansionMode(kind: string): TimelineExpansionMode {
  const { expansionModes } = useContext(SmoothStreamContext)
  return actionToExpansionMode(resolveUiExpansionAction(expansionModes, kind))
}
