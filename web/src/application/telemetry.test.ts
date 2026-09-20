import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { initTelemetry, destroyTelemetry, reportInteraction, setTelemetryView } from './telemetry'
import * as interfaceClient from '../gen-clients/interfacemanager/client'

// Mock the generated-client and interfacemanager client
vi.mock('../application/generated-client', () => ({
  client: {},
}))

vi.mock('../gen-clients/interfacemanager/client')

const reportInteractionMock = interfaceClient.reportInteraction as ReturnType<typeof vi.fn>

describe('telemetry', () => {
  beforeEach(() => {
    destroyTelemetry()
    vi.clearAllMocks()
    ;(reportInteractionMock as ReturnType<typeof vi.fn>).mockResolvedValue({ Accepted: true })
  })

  afterEach(() => {
    destroyTelemetry()
  })

  describe('initTelemetry', () => {
    it('reports click on annotated element', () => {
      initTelemetry()
      setTelemetryView('conversation')

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'test-btn')
      btn.setAttribute('aria-label', 'Submit form')
      container.appendChild(btn)

      btn.click()

      expect(reportInteractionMock).toHaveBeenCalledTimes(1)
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'click',
          GuideId: 'test-btn',
          Label: 'Submit form',
          View: 'conversation',
        })
      )

      document.body.removeChild(container)
    })

    it('drops click on non-annotated element', () => {
      initTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      container.appendChild(btn)

      btn.click()

      expect(reportInteractionMock).not.toHaveBeenCalled()

      document.body.removeChild(container)
    })

    it('reports change on annotated input without the value', () => {
      initTelemetry()
      setTelemetryView('settings')

      const container = document.createElement('div')
      document.body.appendChild(container)

      const input = document.createElement('input')
      input.type = 'text'
      input.setAttribute('data-guide-id', 'search-input')
      container.appendChild(input)

      // Simulate change event
      const changeEvent = new Event('change', { bubbles: true })
      Object.defineProperty(changeEvent, 'target', { value: input })
      input.dispatchEvent(changeEvent)

      expect(reportInteractionMock).toHaveBeenCalledTimes(1)
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'input',
          GuideId: 'search-input',
          Detail: 'text',
        })
      )

      document.body.removeChild(container)
    })

    it('reports annotated select with correct detail', () => {
      initTelemetry()
      setTelemetryView('topology')

      const container = document.createElement('div')
      document.body.appendChild(container)

      const select = document.createElement('select')
      select.setAttribute('data-guide-id', 'mode-select')
      container.appendChild(select)

      const changeEvent = new Event('change', { bubbles: true })
      Object.defineProperty(changeEvent, 'target', { value: select })
      select.dispatchEvent(changeEvent)

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'input',
          GuideId: 'mode-select',
          Detail: 'select',
        })
      )

      document.body.removeChild(container)
    })

    it('drops change on non-annotated element', () => {
      initTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const input = document.createElement('input')
      container.appendChild(input)

      const changeEvent = new Event('change', { bubbles: true })
      Object.defineProperty(changeEvent, 'target', { value: input })
      input.dispatchEvent(changeEvent)

      expect(reportInteractionMock).not.toHaveBeenCalled()

      document.body.removeChild(container)
    })

    it('uses truncated textContent as label when aria-label is absent', () => {
      initTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'long-text-btn')
      btn.textContent = 'This is a very long button text that exceeds the maximum allowed length'
      container.appendChild(btn)

      btn.click()

      // The actual label is truncated to LABEL_MAX_LENGTH chars
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Label: expect.stringContaining('This is a very long button text that exceeds'),
        })
      )
      // Verify it was truncated (not the full text)
      const callArgs = reportInteractionMock.mock.calls[0]
      if (callArgs) {
        expect((callArgs[1] as { Label?: string }).Label!.length).toBeLessThanOrEqual(50)
      }

      document.body.removeChild(container)
    })

    it('never uses textarea content as label (privacy)', () => {
      initTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const ta = document.createElement('textarea')
      ta.setAttribute('data-guide-id', 'secret-input')
      ta.setAttribute('placeholder', 'Type here')
      ta.textContent = 'user-typed-secret-content'
      container.appendChild(ta)

      ta.click()

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ GuideId: 'secret-input', Label: 'Type here' })
      )
      const callArgs = reportInteractionMock.mock.calls[0]
      expect((callArgs![1] as { Label?: string }).Label).not.toContain('secret')

      document.body.removeChild(container)
    })

    it('ignores elements with empty aria-label and falls back to textContent', () => {
      initTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'empty-aria-btn')
      btn.setAttribute('aria-label', '   ')
      btn.textContent = 'Click me'
      container.appendChild(btn)

      btn.click()

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Label: 'Click me',
        })
      )

      document.body.removeChild(container)
    })

    it('does not report twice when initTelemetry is called multiple times', () => {
      initTelemetry()
      initTelemetry()
      setTelemetryView('conversation')

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'idempotent-btn')
      container.appendChild(btn)

      btn.click()

      expect(reportInteractionMock).toHaveBeenCalledTimes(1)

      document.body.removeChild(container)
    })
  })

  describe('reportInteraction', () => {
    it('reports navigate interaction manually', () => {
      setTelemetryView('topology')
      reportInteraction('navigate', '', { view: 'topology', detail: 'topology' })

      expect(reportInteractionMock).toHaveBeenCalledTimes(1)
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'navigate',
          GuideId: '',
          View: 'topology',
          Detail: 'topology',
        })
      )
    })

    it('silently drops report on failure (fire-and-forget)', async () => {
      ;(reportInteractionMock as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network error'))

      // Should not throw
      reportInteraction('navigate', 'some-guide', { detail: 'test' })

      // Wait for the async fire-and-forget to execute
      await new Promise(resolve => setTimeout(resolve, 0))

      expect(reportInteractionMock).toHaveBeenCalled()
    })

    it('uses current view context when no view provided', () => {
      setTelemetryView('conversation')
      reportInteraction('navigate', 'guide-1')

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          View: 'conversation',
        })
      )
    })
  })

  describe('setTelemetryView', () => {
    it('updates the current view context', () => {
      setTelemetryView('files')

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'file-btn')
      container.appendChild(btn)

      initTelemetry()
      btn.click()

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          View: 'files',
        })
      )

      document.body.removeChild(container)
    })
  })

  describe('destroyTelemetry', () => {
    it('stops reporting after destroy', () => {
      initTelemetry()
      destroyTelemetry()

      const container = document.createElement('div')
      document.body.appendChild(container)

      const btn = document.createElement('button')
      btn.setAttribute('data-guide-id', 'after-destroy-btn')
      container.appendChild(btn)

      btn.click()

      expect(reportInteractionMock).not.toHaveBeenCalled()

      document.body.removeChild(container)
    })
  })
})
