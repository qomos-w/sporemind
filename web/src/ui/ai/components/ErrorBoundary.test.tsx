import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ErrorBoundary } from './ErrorBoundary'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Mock useI18n — returns the key string as-is, matching the runtime fallback.
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// ── Test helpers ──

/** A child component that throws during render. Uses a conditional guard
 *  so TypeScript sees a valid return path (the guard is always true at
 *  runtime since message is always a non-empty string). */
function ThrowOnRender({ message }: { message: string }): React.ReactElement {
  if (message) throw new Error(message)
  return <div />
}

/** A child component that renders normally. */
function PlainChild({ text }: { text: string }) {
  return <div data-testid="plain-child">{text}</div>
}

function renderToDOM(node: React.ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  act(() => {
    root = createRoot(container)
    root.render(node)
  })
  return {
    container,
    unmount: () => { root!.unmount(); container.remove() },
  }
}

// ── Tests ──

describe('ErrorBoundary', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    // React logs caught errors to console.error; suppress for clean test output.
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('displays error fallback when a child throws during render', () => {
    const { container, unmount } = renderToDOM(
      <ErrorBoundary>
        <ThrowOnRender message="boom" />
      </ErrorBoundary>
    )

    const fallback = container.querySelector('.ai-error-boundary')
    expect(fallback).toBeTruthy()
    expect(fallback!.textContent).toContain('ai.error.boundary.title')
    unmount()
  })

  it('does NOT render the child content when error is caught', () => {
    const { container, unmount } = renderToDOM(
      <ErrorBoundary>
        <PlainChild text="should-not-appear" />
        <ThrowOnRender message="crash" />
      </ErrorBoundary>
    )

    // The throwing child and everything after it is replaced by the fallback.
    // The boundary's subtree is unmounted on error.
    expect(container.querySelector('[data-testid="plain-child"]')).toBeNull()
    expect(container.querySelector('.ai-error-boundary')).toBeTruthy()
    unmount()
  })

  it('isolates errors: sibling frames outside the boundary render normally', () => {
    // Simulate the MessageStream pattern: each FrameRenderer is wrapped in
    // its own ErrorBoundary. One frame throws; the other must survive.
    const { container, unmount } = renderToDOM(
      <div>
        <ErrorBoundary key="good">
          <PlainChild text="frame-1-ok" />
        </ErrorBoundary>
        <ErrorBoundary key="bad">
          <ThrowOnRender message="frame-2-crash" />
        </ErrorBoundary>
        <ErrorBoundary key="good2">
          <PlainChild text="frame-3-ok" />
        </ErrorBoundary>
      </div>
    )

    const children = container.querySelectorAll('[data-testid="plain-child"]')
    expect(children).toHaveLength(2)
    expect(children[0]!.textContent).toBe('frame-1-ok')
    expect(children[1]!.textContent).toBe('frame-3-ok')

    // The middle frame shows the error placeholder, not white screen.
    const fallbacks = container.querySelectorAll('.ai-error-boundary')
    expect(fallbacks).toHaveLength(1)
    unmount()
  })

  it('shows collapsible error details when toggled', () => {
    const { container, unmount } = renderToDOM(
      <ErrorBoundary>
        <ThrowOnRender message="detailed-crash" />
      </ErrorBoundary>
    )

    // Details are collapsed by default
    let detail = container.querySelector('.ai-error-boundary-detail')
    expect(detail).toBeNull()

    // Expand
    const toggle = container.querySelector('.ai-error-boundary-toggle') as HTMLButtonElement
    expect(toggle).toBeTruthy()
    act(() => { toggle.click() })

    detail = container.querySelector('.ai-error-boundary-detail')
    expect(detail).toBeTruthy()
    expect(detail!.textContent).toContain('detailed-crash')
    unmount()
  })

  it('can retry rendering after an error via reset button', () => {
    let shouldThrow = true

    function ConditionalThrow() {
      if (shouldThrow) throw new Error('conditional')
      return <div data-testid="recovered">recovered</div>
    }

    const { container, unmount } = renderToDOM(
      <ErrorBoundary>
        <ConditionalThrow />
      </ErrorBoundary>
    )

    // Initially shows error
    expect(container.querySelector('.ai-error-boundary')).toBeTruthy()
    expect(container.querySelector('[data-testid="recovered"]')).toBeNull()

    // Fix the condition and click retry
    shouldThrow = false
    const retry = container.querySelector('.ai-error-boundary-retry') as HTMLButtonElement
    act(() => { retry.click() })

    // Error boundary resets, child renders normally
    expect(container.querySelector('.ai-error-boundary')).toBeNull()
    expect(container.querySelector('[data-testid="recovered"]')).toBeTruthy()
    unmount()
  })
})
