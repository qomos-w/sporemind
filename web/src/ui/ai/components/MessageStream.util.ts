import type { Frame, TextFrame, PermissionRequestFrame } from '../model/frame-types'

// Frame types that are the terminal "verdict" of a turn. When the final
// visible frame is one of these, keep the preceding frame visible too so the
// user can see the context (e.g. the summary that led to the reviewer verdict).
export const TERMINAL_VERDICT_TYPES = new Set(['goal_review'])

export interface AssistantFrameLayout {
  visible: Frame[]
  foldFrames: Frame[]
  keptFrames: Frame[]
  shouldFold: boolean
}

export function layoutAssistantFrames(frames: Frame[], turnComplete: boolean): AssistantFrameLayout {
  const visible = frames
    .filter((f): f is Frame => !(f.type === 'text' && !(f as TextFrame).content?.trim()))
    .filter(f => f.type !== 'permission_request' || (f as PermissionRequestFrame).allowed !== true)
  const lastFrame = visible.length > 0 ? visible[visible.length - 1] : null
  const lastIsTerminalVerdict = lastFrame != null && TERMINAL_VERDICT_TYPES.has(lastFrame.type)
  const keepVisibleCount = lastIsTerminalVerdict ? 2 : 1
  const shouldFold = turnComplete && visible.length > keepVisibleCount
  const foldFrames = shouldFold ? visible.slice(0, -keepVisibleCount) : []
  const keptFrames = shouldFold ? visible.slice(-keepVisibleCount) : visible
  return { visible, foldFrames, keptFrames, shouldFold }
}
