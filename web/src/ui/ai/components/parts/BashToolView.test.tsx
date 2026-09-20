import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { BashToolView } from './BashToolView'
import type { ToolFrame } from '../../model/frame-types'

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

function makeCompletedFrame(stdout: string): ToolFrame {
  return {
    id: 'f-bash',
    type: 'tool',
    status: 'completed',
    toolName: 'bash',
    input: JSON.stringify({ Command: 'make', Args: ['test'] }),
    output: JSON.stringify({ Stdout: stdout, Stderr: '', ExitCode: 0 }),
    version: 1,
  } as ToolFrame
}

const outputPre = () =>
  container.querySelector('.ai-tool-output-stdout .ai-tool-code.terminal') as HTMLElement
const outputExpanders = () =>
  [...container.querySelectorAll('.ai-tool-output-stdout .ai-tool-code-expander')] as HTMLButtonElement[]

describe('BashToolView finished-output folding keeps the tail', () => {
  it('shows the last lines and hides earlier output above', () => {
    const stdout = ['l1', 'l2', 'l3', 'l4', 'l5', 'l6', 'l7', 'l8'].join('\n')
    act(() => {
      root.render(<BashToolView frame={makeCompletedFrame(stdout)} />)
    })
    const pre = outputPre()
    expect(pre.textContent).toContain('l8')
    expect(pre.textContent).toContain('l4')
    expect(pre.textContent).not.toContain('l3')
    const expander = outputExpanders()[0]!
    expect(expander.textContent).toContain('+3 earlier lines')
    // The hidden-content indicator sits above the terminal, not below it.
    expect(expander.compareDocumentPosition(pre) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('expand reveals the full output, collapse returns to the tail', () => {
    const stdout = ['l1', 'l2', 'l3', 'l4', 'l5', 'l6'].join('\n')
    act(() => {
      root.render(<BashToolView frame={makeCompletedFrame(stdout)} />)
    })
    let pre = outputPre()
    expect(pre.textContent).not.toContain('l1')

    act(() => {
      outputExpanders()[0]!.click()
    })
    pre = outputPre()
    expect(pre.textContent).toContain('l1')
    expect(pre.textContent).toContain('l6')

    const collapse = outputExpanders().find(el => el.textContent === 'collapse') as HTMLButtonElement
    act(() => { collapse.click() })
    pre = outputPre()
    expect(pre.textContent).not.toContain('l1')
    expect(pre.textContent).toContain('l6')
  })

  it('output within the limit renders whole with no expander', () => {
    const stdout = ['l1', 'l2', 'l3'].join('\n')
    act(() => {
      root.render(<BashToolView frame={makeCompletedFrame(stdout)} />)
    })
    const pre = outputPre()
    expect(pre.textContent).toContain('l1')
    expect(pre.textContent).toContain('l3')
    expect(outputExpanders()).toHaveLength(0)
  })

  it('trailing newline is a terminator, not a phantom line', () => {
    // 5 real lines + trailing newline: within the limit, no folding.
    const stdout = ['l1', 'l2', 'l3', 'l4', 'l5'].join('\n') + '\n'
    act(() => {
      root.render(<BashToolView frame={makeCompletedFrame(stdout)} />)
    })
    expect(outputExpanders()).toHaveLength(0)
  })

  it('pending command shows the terminal cursor without the capsule placeholder', () => {
    const running = makeCompletedFrame('')
    ;(running as { status: string }).status = 'running'
    act(() => {
      root.render(<BashToolView frame={running} />)
    })
    // Same cursor element as the live-output terminal, no dashed capsule.
    expect(container.querySelector('.ai-console .ai-tool-running')).toBeNull()
    const cursor = container.querySelector('.ai-console pre.terminal .ai-terminal-cursor')
    expect(cursor).toBeTruthy()
  })

  it('follows the stream while running even when far from the bottom', async () => {
    const frame = makeCompletedFrame('')
    const runningFrame = frame as typeof frame & { status: string; runningOutput?: { stdout: string; stderr: string } }
    runningFrame.status = 'running'
    runningFrame.runningOutput = { stdout: 'l1\n', stderr: '' }
    act(() => {
      root.render(<BashToolView frame={runningFrame} />)
    })
    const pre = outputPre()
    // jsdom does no layout: emulate an overflowing terminal (~5 lines shown,
    // buffer far below) so the follow effect has a real gap to close.
    Object.defineProperty(pre, 'scrollHeight', { configurable: true, get: () => 1000 })
    Object.defineProperty(pre, 'clientHeight', { configurable: true, get: () => 120 })
    runningFrame.runningOutput = { stdout: 'l1\nl2\nl3\nl4\nl5\nl6\nl7\n', stderr: '' }
    act(() => {
      root.render(<BashToolView frame={runningFrame} />)
    })
    await act(async () => {
      await new Promise<void>(resolve => requestAnimationFrame(() => setTimeout(resolve, 0)))
    })
    expect(pre.scrollTop).toBe(1000)
  })
})
