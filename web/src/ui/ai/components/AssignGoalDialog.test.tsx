import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AssignGoalDialog } from './AssignGoalDialog'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentInfo } from '../hooks/agentInfoStore'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  onAssign: vi.fn(async () => {}),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const taskCard: MonoCardListItem = {
  id: 'card-1',
  type: 'task',
  tags: [],
  list: [],
  modified: '2026-08-04T00:00:00Z',
  status: 'todo',
}

function makeAgent(id: string, displayName: string, title?: string): AgentInfo {
  return {
    Id: id,
    ActorId: `actor-${id}`,
    DisplayName: displayName,
    Title: title ?? displayName,
    HasTitle: true,
    AgentKind: 'worker',
    ProjectId: 'project-1',
    ProjectName: 'Project 1',
    Status: 'idle',
    StatusLabel: 'idle',
    IsWorking: false,
    IsError: false,
    IsCompleted: false,
    IsAskUserPermission: false,
    IsAskUser: false,
    IsAskPermission: false,
    IsPlanApproval: false, IsGoalSubmit: false,
    Primary: { Candidates: [{ kind: 'unit', Unit: { model: 'deepseek-chat', provider: 'deepseek' } }] },
    CompactionPolicyLoading: false,
    CanDelete: true,
  }
}

describe('AssignGoalDialog', () => {
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

  const renderDialog = async (props: Partial<Parameters<typeof AssignGoalDialog>[0]> = {}) => {
    const base = {
      open: true,
      card: taskCard,
      agents: [makeAgent('a1', 'Worker One'), makeAgent('a2', 'Worker Two')],
      submitting: false,
      error: null,
      onClose: () => {},
      onAssign: hoisted.onAssign,
    }
    await act(async () => {
      root.render(<AssignGoalDialog {...base} {...props} />)
    })
  }

  it('renders the card id without a goal-condition editor', async () => {
    await renderDialog()

    expect(container.querySelector('.assign-goal-dialog-card-id')!.textContent).toBe('card-1')
    expect(container.querySelector('#assign-goal-condition')).toBeNull()
  })

  it('disables assign until a new-agent name is entered', async () => {
    await renderDialog()

    const assign = container.querySelector('.assign-goal-dialog-btn.confirm') as HTMLButtonElement
    expect(assign.disabled).toBe(true)

    const nameInput = container.querySelector('#assign-goal-name') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(nameInput, 'Worker Three')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(assign.disabled).toBe(false)
  })

  it('submits a spawn-assign request for a new agent', async () => {
    await renderDialog()

    const nameInput = container.querySelector('#assign-goal-name') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(nameInput, 'Worker Three')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const assign = container.querySelector('.assign-goal-dialog-btn.confirm') as HTMLButtonElement
    await act(async () => {
      assign.click()
    })

    expect(hoisted.onAssign).toHaveBeenCalledTimes(1)
    expect(hoisted.onAssign).toHaveBeenCalledWith({
      mode: 'new',
      displayName: 'Worker Three',
      agentKind: 'worker',
      agentActorId: '',
    })
  })

  it('submits an existing-agent assign when that mode is selected', async () => {
    await renderDialog()

    const existingBtn = [...container.querySelectorAll('.assign-goal-dialog-mode-btn')]
      .find(b => b.textContent?.includes('assignGoal.modeExisting')) as HTMLButtonElement
    await act(async () => {
      existingBtn.click()
    })

    const trigger = container.querySelector('#assign-goal-agent') as HTMLButtonElement
    expect(trigger).not.toBeNull()
    expect(trigger.className).toContain('assign-goal-agent-trigger')

    await act(async () => {
      trigger.click()
    })

    const options = [...container.querySelectorAll<HTMLButtonElement>('.assign-goal-agent-option')]
    expect(options.map(o => o.getAttribute('aria-selected'))).toEqual(['true', 'false'])
    // Rich option content: avatar chip, status label, mounted-model icon.
    expect(options[0]!.querySelector('.assign-goal-agent-avatar')).not.toBeNull()
    expect(options[0]!.querySelector('.assign-goal-agent-status')!.textContent).toBe('idle')
    expect(options[0]!.querySelector('.assign-goal-agent-model')).not.toBeNull()

    await act(async () => {
      options[1]!.click()
    })

    const assign = container.querySelector('.assign-goal-dialog-btn.confirm') as HTMLButtonElement
    expect(assign.disabled).toBe(false)
    await act(async () => {
      assign.click()
    })

    expect(hoisted.onAssign).toHaveBeenCalledWith({
      mode: 'existing',
      displayName: '',
      agentKind: 'worker',
      agentActorId: 'actor-a2',
    })
  })

  it('renders options in the caller-provided (sidebar) order', async () => {
    await renderDialog({
      agents: [makeAgent('a2', 'Worker Two'), makeAgent('a1', 'Worker One')],
    })

    const existingBtn = [...container.querySelectorAll('.assign-goal-dialog-mode-btn')]
      .find(b => b.textContent?.includes('assignGoal.modeExisting')) as HTMLButtonElement
    await act(async () => {
      existingBtn.click()
    })
    const trigger = container.querySelector('#assign-goal-agent') as HTMLButtonElement
    await act(async () => {
      trigger.click()
    })

    const names = [...container.querySelectorAll('.assign-goal-agent-option-name')].map(n => n.textContent)
    expect(names).toEqual(['Worker Two', 'Worker One'])
    // Default selection follows the first option.
    expect(container.querySelector('.assign-goal-agent-trigger-name')!.textContent).toBe('Worker Two')
  })

  it('combines DisplayName and Title on the trigger after selection', async () => {
    await renderDialog({
      agents: [makeAgent('a1', 'Worker One', 'Fix the login bug'), makeAgent('a2', 'Worker Two')],
    })

    const existingBtn = [...container.querySelectorAll('.assign-goal-dialog-mode-btn')]
      .find(b => b.textContent?.includes('assignGoal.modeExisting')) as HTMLButtonElement
    await act(async () => {
      existingBtn.click()
    })
    const trigger = container.querySelector('#assign-goal-agent') as HTMLButtonElement
    await act(async () => {
      trigger.click()
    })
    const options = [...container.querySelectorAll<HTMLButtonElement>('.assign-goal-agent-option')]
    await act(async () => {
      options[0]!.click()
    })

    expect(container.querySelector('.assign-goal-agent-trigger-name')!.textContent).toBe('Worker One')
    expect(container.querySelector('.assign-goal-agent-trigger-title')!.textContent).toBe('Fix the login bug')
    // Identical DisplayName/Title on a2 must not render a duplicated title.
    await act(async () => {
      trigger.click()
    })
    const second = [...container.querySelectorAll<HTMLButtonElement>('.assign-goal-agent-option')][1]!
    await act(async () => {
      second.click()
    })
    expect(container.querySelector('.assign-goal-agent-trigger-name')!.textContent).toBe('Worker Two')
    expect(container.querySelector('.assign-goal-agent-trigger-title')).toBeNull()
  })

  it('shows a no-agents hint when the project has no agents', async () => {
    await renderDialog({ agents: [] })

    const existingBtn = [...container.querySelectorAll('.assign-goal-dialog-mode-btn')]
      .find(b => b.textContent?.includes('assignGoal.modeExisting')) as HTMLButtonElement
    await act(async () => {
      existingBtn.click()
    })

    expect(container.querySelector('.assign-goal-dialog-empty')).not.toBeNull()
  })

  it('renders the error message when the backend rejects', async () => {
    await renderDialog({ error: 'workspace.agent.assign: card already claimed' })

    expect(container.querySelector('.assign-goal-dialog-error')!.textContent).toContain('card already claimed')
  })
})
