import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import { AgentSettingsCategory } from './settings-data'
import { I18nProvider } from '../../../i18n'
import * as workspace from '../../../gen-clients/workspace/client'

(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))

vi.mock('../../../gen-clients/workspace/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../gen-clients/workspace/client')>()
  return {
    ...actual,
    listAgentKinds: vi.fn(),
    createAgentKind: vi.fn(),
    deleteAgentKind: vi.fn(),
  }
})

// The per-template config view pulls in a large dependency graph; stub it so
// these tests focus on the template selector + create/delete wiring. The stub
// mirrors the real component's delete-button gating.
vi.mock('../../views/AgentKindConfigView', () => ({
  AgentKindConfigView: ({ kind, builtin, onRequestDelete }: { kind: string; builtin?: boolean; onRequestDelete?: () => void }) => (
    <div data-testid={`kind-view-${kind}`}>
      {builtin === false && (
        <button type="button" data-testid="agent-kind-delete" onClick={onRequestDelete}>delete</button>
      )}
    </div>
  ),
}))

type KindItem = { Kind: string; DisplayName: string; UserCreatable: boolean; SystemManaged: boolean; Builtin: boolean }

let kindItems: KindItem[] = []

function kind(Kind: string, DisplayName: string, Builtin: boolean): KindItem {
  return { Kind, DisplayName, UserCreatable: true, SystemManaged: false, Builtin }
}

function render() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  return { container, root }
}

async function flush() {
  await act(async () => {})
}

describe('AgentSettingsCategory template management', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    kindItems = [kind('coder', 'Coder', true), kind('my-agent', 'My Agent', false)]
    vi.mocked(workspace.listAgentKinds).mockImplementation(async () => ({ Items: kindItems } as any))
    vi.mocked(workspace.createAgentKind).mockResolvedValue({ Kind: 'fast-agent', DisplayName: 'Fast Agent', UserCreatable: true, SystemManaged: false, Builtin: false } as any)
    vi.mocked(workspace.deleteAgentKind).mockResolvedValue({ Kind: 'my-agent', Removed: true } as any)
    ;({ container, root } = render())
  })

  afterEach(() => {
    root.unmount()
    container.remove()
    vi.restoreAllMocks()
  })

  async function mountPanel() {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <AgentSettingsCategory />
        </I18nProvider>,
      )
    })
    await flush()
  }

  function selectorButton(label: string): HTMLButtonElement {
    const button = Array.from(container.querySelectorAll('button')).find(b => b.textContent === label)
    expect(button, `selector button: ${label}`).toBeTruthy()
    return button as HTMLButtonElement
  }

  it('renders built-in and custom templates in the selector', async () => {
    await mountPanel()
    expect(selectorButton('Coder')).toBeTruthy()
    expect(selectorButton('My Agent')).toBeTruthy()
    expect(container.querySelector('[data-testid="agent-kind-new"]')).toBeTruthy()
    // The built-in template is selected first and exposes no delete action.
    expect(container.querySelector('[data-testid="kind-view-coder"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="agent-kind-delete"]')).toBeNull()
  })

  it('creates a template from the dialog and selects it afterwards', async () => {
    await mountPanel()

    await act(async () => { (container.querySelector('[data-testid="agent-kind-new"]') as HTMLButtonElement).click() })
    expect(container.querySelector('[data-testid="new-kind-slug"]')).toBeTruthy()

    const slug = container.querySelector('[data-testid="new-kind-slug"]') as HTMLInputElement
    const name = container.querySelector('[data-testid="new-kind-display-name"]') as HTMLInputElement
    await act(async () => { fireEvent.change(slug, { target: { value: 'fast-agent' } }) })
    await act(async () => { fireEvent.change(name, { target: { value: 'Fast Agent' } }) })

    kindItems = [...kindItems, kind('fast-agent', 'Fast Agent', false)]

    const form = container.querySelector('form') as HTMLFormElement
    await act(async () => { fireEvent.submit(form) })
    await flush()

    expect(workspace.createAgentKind).toHaveBeenCalledWith(expect.anything(), {
      Kind: 'fast-agent',
      DisplayName: 'Fast Agent',
      BaseKind: 'coder',
    })
    // Dialog closes and the new template is selected.
    expect(container.querySelector('[data-testid="new-kind-slug"]')).toBeNull()
    expect(container.querySelector('[data-testid="kind-view-fast-agent"]')).toBeTruthy()
  })

  it('deletes a custom template through the confirmation modal', async () => {
    await mountPanel()

    await act(async () => { selectorButton('My Agent').click() })
    const deleteButton = container.querySelector('[data-testid="agent-kind-delete"]') as HTMLButtonElement
    expect(deleteButton).toBeTruthy()

    await act(async () => { deleteButton.click() })
    const confirm = container.querySelector('.delete-confirm-delete-btn') as HTMLButtonElement
    expect(confirm).toBeTruthy()

    kindItems = kindItems.filter(k => k.Kind !== 'my-agent')
    await act(async () => { confirm.click() })
    await flush()

    expect(workspace.deleteAgentKind).toHaveBeenCalledWith(expect.anything(), { Kind: 'my-agent' })
    // Falls back to the first remaining template and drops the deleted one.
    expect(container.querySelector('[data-testid="kind-view-coder"]')).toBeTruthy()
    expect(Array.from(container.querySelectorAll('button')).some(b => b.textContent === 'My Agent')).toBe(false)
  })

  it('surfaces a backend rejection when deleting a template', async () => {
    await mountPanel()
    vi.mocked(workspace.deleteAgentKind).mockRejectedValueOnce(new Error('template has live instances'))

    await act(async () => { selectorButton('My Agent').click() })
    await act(async () => { (container.querySelector('[data-testid="agent-kind-delete"]') as HTMLButtonElement).click() })
    await act(async () => { (container.querySelector('.delete-confirm-delete-btn') as HTMLButtonElement).click() })
    await flush()

    const error = container.querySelector('[data-testid="agent-kind-delete-error"]')
    expect(error?.textContent).toContain('template has live instances')
  })
})
