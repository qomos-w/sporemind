import { useEffect } from 'react'
import { Loader2, WifiOff } from 'lucide-react'
import { AuthTitleBar } from './AuthTitleBar'
import { useBrowserOverlay } from '../ai/browserOverlay'
import './auth.css'

export type OverlayState = 'idle' | 'loading' | 'reconnecting' | 'error'

interface LoadingOverlayProps {
  state: OverlayState
  message?: string
  onRetry?: () => void
}

// Elements the user may still focus & edit while the connection is down.
// Matches native inputs plus rich-text editors (ProseMirror/TipTap/CodeMirror
// all surface as contenteditable).
const EDITABLE_SELECTOR =
  'input, textarea, [contenteditable]:not([contenteditable="false"])'

/**
 * Block click/mouse-down dispatch on everything EXCEPT text inputs while the
 * connection is down, so users keep typing/editing but can't fire callables
 * (send message, save card, submit turn) that would silently fail or queue.
 *
 * Runs on the capture phase at document level so it preempts React's
 * synthetic-event delegation (root-level listeners) — stopPropagation here
 * means React onClick never fires for the blocked targets.
 */
function useGuardClicksWhileDown(active: boolean) {
  useEffect(() => {
    if (!active) return
    const guard = (e: MouseEvent) => {
      const t = e.target
      if (t instanceof Element && t.closest(EDITABLE_SELECTOR)) return
      e.preventDefault()
      e.stopPropagation()
    }
    document.addEventListener('click', guard, true)
    document.addEventListener('mousedown', guard, true)
    return () => {
      document.removeEventListener('click', guard, true)
      document.removeEventListener('mousedown', guard, true)
    }
  }, [active])
}

export function LoadingOverlay({ state, message, onRetry }: LoadingOverlayProps) {
  useGuardClicksWhileDown(state === 'reconnecting')

  // The loading/error screens are full-screen fixed layers over the right
  // panel. Register them with the browser overlay manager so the native
  // browser child windows are hidden while they are up (no-op outside
  // BrowserOverlayManager, i.e. before the app shell is mounted). The
  // transparent `reconnecting` state never occludes anything, so it is exempt.
  useBrowserOverlay(state === 'loading' || state === 'error')

  if (state === 'idle') return null

  // Reconnecting: transparent, non-blocking animated overlay with no text.
  // Clicks pass through the visual layer (pointer-events: none) but are
  // selectively absorbed by useGuardClicksWhileDown above.
  if (state === 'reconnecting') {
    return (
      <div className="reconnect-overlay" aria-hidden="true">
        <div className="reconnect-overlay-spinner" />
      </div>
    )
  }

  const isError = state === 'error'

  return (
    <div className="loading-overlay">
      <AuthTitleBar />
      <div className="loading-overlay-body">
        <div className="loading-overlay-content">
          {isError ? (
            <WifiOff size={28} className="loading-overlay-icon loading-overlay-error" />
          ) : (
            <Loader2 size={28} className="loading-overlay-icon spin" />
          )}
          <span className="loading-overlay-text">
            {message || 'Loading...'}
          </span>
          {isError && onRetry && (
            <button className="loading-overlay-retry" onClick={onRetry}>
              Retry
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
