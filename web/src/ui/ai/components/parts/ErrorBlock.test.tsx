import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ErrorBlock } from './ErrorBlock'
import type { ErrorFrame } from '../../model/frame-types'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('ErrorBlock', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderBlock = async (frame: ErrorFrame) => {
    await act(async () => {
      root.render(<ErrorBlock frame={frame} />)
    })
  }

  it('titles the block with the failed tool call name and subtitles the message', async () => {
    const frame: ErrorFrame = {
      id: 'err-1',
      type: 'error',
      status: 'error',
      message: 'permission denied',
      toolName: 'project.list',
    }
    await renderBlock(frame)

    const label = container.querySelector('.ai-step-label')
    expect(label).not.toBeNull()
    expect(label!.textContent).toBe('project.list')

    const subtitle = container.querySelector('.ai-error-subtitle-text')
    expect(subtitle).not.toBeNull()
    expect(subtitle!.textContent).toBe('permission denied')
    expect(subtitle!.getAttribute('title')).toBe('permission denied')
  })

  it('falls back to the generic error title when no tool name is present', async () => {
    const frame: ErrorFrame = {
      id: 'err-2',
      type: 'error',
      status: 'error',
      message: 'LLM timeout',
    }
    await renderBlock(frame)

    const label = container.querySelector('.ai-step-label')
    expect(label).not.toBeNull()
    expect(label!.textContent).toBe('ai.step.error')
    expect(hoisted.t).toHaveBeenCalledWith('ai.step.error')
  })

  it('renders the error code badge alongside the title', async () => {
    const frame: ErrorFrame = {
      id: 'err-3',
      type: 'error',
      status: 'error',
      message: 'boom',
      code: 'exec_error',
      toolName: 'project.shell_exec',
    }
    await renderBlock(frame)

    const code = container.querySelector('.ai-error-code')
    expect(code).not.toBeNull()
    expect(code!.textContent).toBe('exec_error')
  })
})
