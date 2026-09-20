import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { TaskToolView } from './TaskToolView'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('TaskToolView', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderView = async (frame: ToolFrame) => {
    await act(async () => {
      root.render(<TaskToolView frame={frame} />)
    })
  }

  it('renders create_task result with subject from response and pending status', async () => {
    const frame: ToolFrame = {
      id: 's1',
      type: 'tool',
      toolName: 'create_task',
      status: 'completed',
      input: JSON.stringify({ Subject: 'Add retry logic', ActiveForm: 'Adding retry logic' }),
      output: JSON.stringify({ Task: { ID: 't1', Subject: 'Add retry logic', Status: 'pending', ActiveForm: 'Adding retry logic' } }),
    }
    await renderView(frame)

    const subject = container.querySelector('.ai-plan-task-subject')
    expect(subject?.textContent).toBe('Add retry logic')
    expect(container.querySelector('.ai-plan-task-active-form')?.textContent).toBe('Adding retry logic')
    expect(container.querySelector('.ai-task-tool')?.classList.contains('task--pending')).toBe(true)
  })

  it('reflects update_task requested status from input', async () => {
    const frame: ToolFrame = {
      id: 's2',
      type: 'tool',
      toolName: 'update_task',
      status: 'completed',
      input: JSON.stringify({ Id: 't1', Status: 'in_progress' }),
      output: JSON.stringify({ Task: { ID: 't1', Subject: 'Wire up handler', Status: 'in_progress' } }),
    }
    await renderView(frame)

    // update_task has no Subject in input; subject comes from the response Task.
    expect(container.querySelector('.ai-plan-task-subject')?.textContent).toBe('Wire up handler')
    expect(container.querySelector('.ai-task-tool')?.classList.contains('task--in_progress')).toBe(true)
  })

  it('shows completed status class when task is completed', async () => {
    const frame: ToolFrame = {
      id: 's3',
      type: 'tool',
      toolName: 'update_task',
      status: 'completed',
      input: JSON.stringify({ Id: 't1', Status: 'completed' }),
      output: JSON.stringify({ Task: { ID: 't1', Subject: 'Done', Status: 'completed' } }),
    }
    await renderView(frame)
    expect(container.querySelector('.ai-task-tool')?.classList.contains('task--completed')).toBe(true)
  })

  it('renders spinner while running and does not show output', async () => {
    const frame: ToolFrame = {
      id: 's4',
      type: 'tool',
      toolName: 'create_task',
      status: 'running',
      input: JSON.stringify({ Subject: 'Add tests' }),
    }
    await renderView(frame)
    expect(container.querySelector('.ai-tool-running')).not.toBeNull()
  })

  it('renders error output on failure', async () => {
    const frame: ToolFrame = {
      id: 's5',
      type: 'tool',
      toolName: 'update_task',
      status: 'error',
      input: JSON.stringify({ Id: 'missing', Status: 'completed' }),
      output: 'task not found: missing',
    }
    await renderView(frame)
    const err = container.querySelector('.ai-tool-code.error')
    expect(err?.textContent).toContain('task not found: missing')
  })

  it('falls back to subject from input when output is unparseable', async () => {
    const frame: ToolFrame = {
      id: 's6',
      type: 'tool',
      toolName: 'create_task',
      status: 'completed',
      input: JSON.stringify({ Subject: 'Add retry logic' }),
      output: 'ok',
    }
    await renderView(frame)
    expect(container.querySelector('.ai-plan-task-subject')?.textContent).toBe('Add retry logic')
  })
})
