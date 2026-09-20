import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { SshCommandPanel } from './SshCommandPanel'
import { I18nProvider } from '../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../application/generated-client', () => ({ client: {} }))

const commandListMock = vi.fn()

vi.mock('../../gen-clients/sshmanager/client', () => ({
  commandList: (...a: unknown[]) => commandListMock(...a),
  commandCreate: vi.fn(),
  commandUpdate: vi.fn(),
  commandRemove: vi.fn(),
  shellInput: vi.fn(),
}))

const ITEMS = [
  { Id: 'c1', Name: 'Disk usage', Category: 'ops', Content: 'df -h' },
  { Id: 'c2', Name: 'List dir', Category: '', Content: 'ls -la' },
]

function renderPanel() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <SshCommandPanel sessionId="sess-1" />
      </I18nProvider>,
    )
  })
  return { container, root }
}

function click(el: Element) {
  act(() => { el.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
}

beforeEach(() => {
  commandListMock.mockReset().mockResolvedValue({ Items: ITEMS })
})

describe('SshCommandPanel add button', () => {
  it('clears a selected command and focuses the name input', async () => {
    const { container, root } = renderPanel()
    await act(async () => {})

    const item = [...container.querySelectorAll('.ssh-cmd-item')]
      .find(b => b.textContent === 'Disk usage')
    expect(item).toBeTruthy()
    click(item!)

    const nameInput = container.querySelector(
      'input.ssh-command-panel-input',
    ) as HTMLInputElement
    expect(nameInput.value).toBe('Disk usage')

    const addBtn = container.querySelector(
      'button[aria-label="New Command"]',
    ) as HTMLButtonElement
    expect(addBtn).toBeTruthy()
    click(addBtn)

    expect(nameInput.value).toBe('')
    expect(document.activeElement).toBe(nameInput)

    act(() => { root.unmount() })
    container.remove()
  })
})
