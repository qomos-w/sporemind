import type { FileChangeEntry } from '../../model/frame-types'

export interface ReviewState {
  fileChanges: FileChangeEntry[]
  turnId: string
}

let currentState: ReviewState | null = null
const listeners = new Set<() => void>()

function notify() {
  for (const l of listeners) l()
}

export function setReviewState(state: ReviewState | null) {
  currentState = state
  notify()
}

export function getReviewState(): ReviewState | null {
  return currentState
}

export function subscribeReview(cb: () => void) {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}
