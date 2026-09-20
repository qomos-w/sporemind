/**
 * UI interactor — remote-driven DOM interaction executor.
 *
 * Subscribes to interface_manager_event with action="interact" and executes
 * DOM operations (click/focus/input/scroll_into_view) on elements annotated
 * with data-guide-id.
 *
 * Hard boundary: only operates on elements with data-guide-id; no arbitrary JS.
 *
 * Usage:
 *   import { executeInteraction } from './ui-interactor'
 *
 *   // Called internally by AIShellLayout on interface_manager_event.
 *   // Direct usage is discouraged; prefer triggering via interfacemanager.control.
 */
import { reportInteraction } from './telemetry'

export type InteractionPrimitive = 'click' | 'focus' | 'input' | 'scroll_into_view'

/**
 * Execute a remote interaction on a DOM element identified by data-guide-id.
 *
 * @param guideId  - The data-guide-id attribute value to locate the target element.
 * @param interaction - The primitive action to perform.
 * @param text     - For 'input' interactions: the text value to set.
 *
 * Behavior:
 * - Element not found → reports remote_ack with error:element-not-found:<guideId>
 * - 'input' on non-input element → reports remote_ack with error:not-input-element:<guideId>
 * - Success → reports remote_ack with ok:<interaction>
 *
 * Hard boundary: only touches elements with data-guide-id; no arbitrary JS execution.
 */
export function executeInteraction(
  guideId: string,
  interaction: InteractionPrimitive,
  text?: string,
): void {
  const el = document.querySelector(`[data-guide-id="${guideId}"]`) as HTMLElement | null

  if (!el) {
    reportInteraction('remote_ack', guideId, { detail: `error:element-not-found:${guideId}` })
    return
  }

  switch (interaction) {
    case 'click': {
      el.click()
      reportInteraction('remote_ack', guideId, { detail: 'ok:click' })
      break
    }
    case 'focus': {
      el.focus()
      reportInteraction('remote_ack', guideId, { detail: 'ok:focus' })
      break
    }
    case 'input': {
      const tag = el.tagName.toLowerCase()
      if (tag !== 'input' && tag !== 'textarea' && !el.isContentEditable) {
        reportInteraction('remote_ack', guideId, { detail: `error:not-input-element:${guideId}` })
        return
      }
      // Set value and dispatch events so React controlled components see the change.
      if (tag === 'input') {
        const inputEl = el as HTMLInputElement
        const nativeInputSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        if (nativeInputSetter) {
          nativeInputSetter.call(inputEl, text ?? '')
        } else {
          inputEl.value = text ?? ''
        }
        inputEl.dispatchEvent(new Event('input', { bubbles: true }))
        inputEl.dispatchEvent(new Event('change', { bubbles: true }))
      } else if (tag === 'textarea') {
        const textareaEl = el as HTMLTextAreaElement
        const nativeInputSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
        if (nativeInputSetter) {
          nativeInputSetter.call(textareaEl, text ?? '')
        } else {
          textareaEl.value = text ?? ''
        }
        textareaEl.dispatchEvent(new Event('input', { bubbles: true }))
        textareaEl.dispatchEvent(new Event('change', { bubbles: true }))
      } else {
        // contenteditable
        el.textContent = text ?? ''
        el.dispatchEvent(new Event('input', { bubbles: true }))
        el.dispatchEvent(new Event('change', { bubbles: true }))
      }
      reportInteraction('remote_ack', guideId, { detail: 'ok:input' })
      break
    }
    case 'scroll_into_view': {
      el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
      reportInteraction('remote_ack', guideId, { detail: 'ok:scroll_into_view' })
      break
    }
    default: {
      // Unknown primitive — ignore silently
      break
    }
  }
}
