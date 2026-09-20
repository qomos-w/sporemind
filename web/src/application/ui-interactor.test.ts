import { describe, it, expect, beforeEach, vi } from 'vitest'
import { executeInteraction } from './ui-interactor'
import * as interfaceClient from '../gen-clients/interfacemanager/client'

// Mock the generated-client and interfacemanager client
vi.mock('../application/generated-client', () => ({
  client: {},
}))

vi.mock('../gen-clients/interfacemanager/client')

const reportInteractionMock = interfaceClient.reportInteraction as ReturnType<typeof vi.fn>

describe('ui-interactor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(reportInteractionMock as ReturnType<typeof vi.fn>).mockResolvedValue({ Accepted: true })
  })

  function makeEl(tag: string, guideId: string): HTMLElement {
    const el = document.createElement(tag)
    el.setAttribute('data-guide-id', guideId)
    return el
  }

  describe('click', () => {
    it('calls el.click() and reports ok:click', () => {
      const btn = makeEl('button', 'test-btn')
      document.body.appendChild(btn)
      const clickSpy = vi.spyOn(btn, 'click')

      executeInteraction('test-btn', 'click')

      expect(clickSpy).toHaveBeenCalledTimes(1)
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'remote_ack',
          GuideId: 'test-btn',
          Detail: 'ok:click',
        })
      )

      document.body.removeChild(btn)
    })

    it('reports error:element-not-found when guideId does not exist', () => {
      executeInteraction('nonexistent', 'click')

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          GuideId: 'nonexistent',
          Detail: 'error:element-not-found:nonexistent',
        })
      )
    })
  })

  describe('focus', () => {
    it('calls el.focus() and reports ok:focus', () => {
      const input = makeEl('input', 'test-input')
      document.body.appendChild(input)
      const focusSpy = vi.spyOn(input, 'focus')

      executeInteraction('test-input', 'focus')

      expect(focusSpy).toHaveBeenCalledTimes(1)
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'remote_ack',
          GuideId: 'test-input',
          Detail: 'ok:focus',
        })
      )

      document.body.removeChild(input)
    })
  })

  describe('input', () => {
    it('sets value on input element and dispatches input/change events', () => {
      const input = makeEl('input', 'search-input') as HTMLInputElement
      document.body.appendChild(input)

      const inputEvents: Event[] = []
      const changeEvents: Event[] = []
      input.addEventListener('input', e => inputEvents.push(e))
      input.addEventListener('change', e => changeEvents.push(e))

      executeInteraction('search-input', 'input', 'hello world')

      expect(input.value).toBe('hello world')
      expect(inputEvents).toHaveLength(1)
      expect(changeEvents).toHaveLength(1)

      document.body.removeChild(input)
    })

    it('sets value on textarea element and dispatches input/change events', () => {
      const textarea = makeEl('textarea', 'comment-area') as HTMLTextAreaElement
      document.body.appendChild(textarea)

      const inputEvents: Event[] = []
      const changeEvents: Event[] = []
      textarea.addEventListener('input', e => inputEvents.push(e))
      textarea.addEventListener('change', e => changeEvents.push(e))

      executeInteraction('comment-area', 'input', 'comment text')

      expect(textarea.value).toBe('comment text')
      expect(inputEvents).toHaveLength(1)
      expect(changeEvents).toHaveLength(1)

      document.body.removeChild(textarea)
    })

    it('sets textContent on contenteditable element and dispatches events', () => {
      const div = makeEl('div', 'editable-div')
      div.setAttribute('contenteditable', 'true')
      document.body.appendChild(div)

      const inputEvents: Event[] = []
      const changeEvents: Event[] = []
      div.addEventListener('input', e => inputEvents.push(e))
      div.addEventListener('change', e => changeEvents.push(e))

      executeInteraction('editable-div', 'input', 'editable content')

      expect(div.textContent).toBe('editable content')
      expect(inputEvents).toHaveLength(1)
      expect(changeEvents).toHaveLength(1)

      document.body.removeChild(div)
    })

    it('reports error:not-input-element for non-input elements', () => {
      const div = makeEl('div', 'not-an-input')
      document.body.appendChild(div)

      executeInteraction('not-an-input', 'input', 'some text')

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          GuideId: 'not-an-input',
          Detail: 'error:not-input-element:not-an-input',
        })
      )

      document.body.removeChild(div)
    })

    it('reports error:element-not-found for nonexistent element', () => {
      executeInteraction('missing', 'input', 'text')

      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          GuideId: 'missing',
          Detail: 'error:element-not-found:missing',
        })
      )
    })
  })

  describe('scroll_into_view', () => {
    it('calls el.scrollIntoView with correct options and reports ok:scroll_into_view', () => {
      const btn = makeEl('button', 'scroll-btn')
      document.body.appendChild(btn)
      const scrollSpy = vi.spyOn(btn, 'scrollIntoView')

      executeInteraction('scroll-btn', 'scroll_into_view')

      expect(scrollSpy).toHaveBeenCalledTimes(1)
      expect(scrollSpy).toHaveBeenCalledWith({ block: 'nearest', behavior: 'smooth' })
      expect(reportInteractionMock).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          Kind: 'remote_ack',
          GuideId: 'scroll-btn',
          Detail: 'ok:scroll_into_view',
        })
      )

      document.body.removeChild(btn)
    })
  })

  describe('unknown primitive', () => {
    it('ignores unknown interaction without crashing', () => {
      const btn = makeEl('button', 'unknown-btn')
      document.body.appendChild(btn)

      // @ts-expect-error — deliberately passing unknown primitive
      executeInteraction('unknown-btn', 'unknown_primitive')

      expect(reportInteractionMock).not.toHaveBeenCalled()

      document.body.removeChild(btn)
    })
  })
})
