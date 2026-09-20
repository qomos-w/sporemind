import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { DeleteConfirmModal } from './DeleteConfirmModal'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string, params?: Record<string, string>) => {
    if (params && key.includes('message')) return `msg ${params.name ?? ''}`
    return key
  }),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('DeleteConfirmModal', () => {
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

  const renderModal = async (props: Partial<React.ComponentProps<typeof DeleteConfirmModal>> = {}) => {
    await act(async () => {
      root.render(
        <DeleteConfirmModal
          open
          agentName="My Agent"
          onClose={() => {}}
          onConfirm={() => {}}
          {...props}
        />,
      )
    })
  }

  const deleteBtn = () => container.querySelector('.delete-confirm-delete-btn') as HTMLButtonElement
  const typeInput = () => container.querySelector('.delete-confirm-type-input') as HTMLInputElement

  // React's controlled input tracks value via a native setter; assigning .value
  // directly then dispatching 'input' only fires onChange if we go through the
  // HTMLInputElement.prototype value setter (the jsdom + React gotcha).
  const setType = async (value: string) => {
    const input = typeInput()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('enables delete immediately when requireTypeName is false', async () => {
    await renderModal({ requireTypeName: false })
    expect(deleteBtn().disabled).toBe(false)
    expect(typeInput()).toBeNull()
  })

  it('requires typing the agent name before enabling delete', async () => {
    await renderModal({ requireTypeName: true })
    expect(deleteBtn().disabled).toBe(true)
    expect(typeInput()).not.toBeNull()

    await setType('wrong')
    expect(deleteBtn().disabled).toBe(true)

    await setType('my agent')
    expect(deleteBtn().disabled).toBe(false)
  })

  it('shows the memory warning when requireTypeName is true', async () => {
    await renderModal({ requireTypeName: true })
    expect(container.querySelector('.delete-confirm-memory-warning')).not.toBeNull()
  })

  it('does not show the memory warning when requireTypeName is false', async () => {
    await renderModal({ requireTypeName: false })
    expect(container.querySelector('.delete-confirm-memory-warning')).toBeNull()
  })

  it('shows the worktree warning when hasWorktree is true', async () => {
    await renderModal({ hasWorktree: true })
    const warnings = container.querySelectorAll('.delete-confirm-memory-warning')
    expect(warnings.length).toBe(1)
    expect(warnings[0]?.textContent).toContain('worktreeWarning')
  })

  it('does not show the worktree warning when hasWorktree is false', async () => {
    await renderModal({ hasWorktree: false })
    expect(container.querySelector('.delete-confirm-memory-warning')).toBeNull()
  })

  it('confirms when Enter is pressed with a matching name', async () => {
    const onConfirm = vi.fn()
    await renderModal({ requireTypeName: true, onConfirm })
    await setType('My Agent')
    await act(async () => {
      typeInput().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('does not confirm on Enter when the name does not match', async () => {
    const onConfirm = vi.fn()
    await renderModal({ requireTypeName: true, onConfirm })
    await act(async () => {
      typeInput().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
