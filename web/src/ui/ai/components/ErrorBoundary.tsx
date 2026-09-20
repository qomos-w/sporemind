import React, { useState, useCallback } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'
import { useI18n } from '../../../i18n'

// ── Fallback UI ──

const MAX_DETAIL_LEN = 512

/** Truncate potentially long error details per project log-truncation constraint. */
function truncate(s: string): string {
  if (s.length > MAX_DETAIL_LEN) return s.slice(0, MAX_DETAIL_LEN) + '…(truncated)'
  return s
}

interface ErrorFallbackProps {
  error: Error
  onReset: () => void
}

/**
 * Inline error placeholder shown when a frame's render throws.
 * Uses native <details>/<summary> for collapsible error details — no
 * floating overlay, so useBrowserOverlay is not required.
 */
const ErrorFallback: React.FC<ErrorFallbackProps> = ({ error, onReset }) => {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const toggle = useCallback(() => setExpanded(v => !v), [])

  const detail = [error.message, error.stack].filter(Boolean).join('\n\n')

  return (
    <div className="ai-error-boundary">
      <div className="ai-error-boundary-header">
        <AlertTriangle size={14} className="ai-error-boundary-icon" />
        <span className="ai-error-boundary-title">{t('ai.error.boundary.title')}</span>
        <button
          className="ai-error-boundary-retry"
          type="button"
          onClick={onReset}
          title={t('ai.error.boundary.retry')}
        >
          <RefreshCw size={12} />
        </button>
      </div>
      <button
        className="ai-error-boundary-toggle"
        type="button"
        onClick={toggle}
        aria-expanded={expanded}
      >
        {t('ai.error.boundary.details')}
      </button>
      {expanded && (
        <pre className="ai-error-boundary-detail">{truncate(detail)}</pre>
      )}
    </div>
  )
}

// ── Error Boundary ──

interface ErrorBoundaryProps {
  children: React.ReactNode
}

interface ErrorBoundaryState {
  error: Error | null
}

/**
 * React Error Boundary that isolates per-frame render failures in the
 * conversation timeline. A single frame throwing during render is caught
 * here; sibling frames continue to render normally.
 *
 * The boundary is placed around each <FrameRenderer> so the blast radius
 * of a render error is exactly one frame.
 */
export class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo): void {
    // eslint-disable-next-line no-console
    console.error('[ErrorBoundary] Frame render failed:', error, info)
  }

  private handleReset = () => {
    this.setState({ error: null })
  }

  render(): React.ReactNode {
    if (this.state.error) {
      return <ErrorFallback error={this.state.error} onReset={this.handleReset} />
    }
    return this.props.children
  }
}
