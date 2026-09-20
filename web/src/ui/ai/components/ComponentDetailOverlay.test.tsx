import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import type { ComponentDescriptor } from '../../../gen-types/component'

const projectGet = vi.fn()
const workspaceGet = vi.fn()
const appmanagerGet = vi.fn()

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/project/client', () => ({ componentGet: (...args: unknown[]) => projectGet(...args) }))
vi.mock('../../../gen-clients/workspace/client', () => ({ componentGet: (...args: unknown[]) => workspaceGet(...args) }))
vi.mock('../../../gen-clients/appmanager/client', () => ({ componentGet: (...args: unknown[]) => appmanagerGet(...args) }))
vi.mock('../../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

import { ComponentDetailOverlay } from './ComponentDetailOverlay'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLElement
let root: Root

const descriptor = (over: Partial<ComponentDescriptor> = {}): ComponentDescriptor => ({
  Ref: { CardId: 'mcp:test-bundle', Kind: 'bundle' },
  Title: 'File Tools',
  Icon: 'files',
  Dependencies: [{ CardId: 'builtin:bundle:shell-tools', Required: true }],
  Prompts: [{ Id: 'p1', CardId: 'mcp:test-bundle', Text: 'Use tools for file edits.' }],
  Tools: [
    { Id: 't1', CardId: 'mcp:test-bundle', CallableId: 'project.read', Description: 'Read a file.' },
    { Id: 't2', CardId: 'mcp:test-bundle', CallableId: 'project.write' },
  ],
  ...over,
})

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  projectGet.mockReset()
  workspaceGet.mockReset()
  appmanagerGet.mockReset()
})

afterEach(() => {
  act(() => { root.unmount() })
  container.remove()
})

async function render(props: Partial<React.ComponentProps<typeof ComponentDetailOverlay>> = {}) {
  await act(async () => {
    root.render(
      <ComponentDetailOverlay
        open
        cardId="mcp:test-bundle"
        onClose={() => {}}
        {...props}
      />,
    )
  })
}

describe('ComponentDetailOverlay', () => {
  it('renders the descriptor capabilities, dependencies and guidance', async () => {
    workspaceGet.mockResolvedValue({ Component: descriptor() })
    await render()

    const overlay = document.querySelector('[data-testid="component-detail-overlay"]') as HTMLElement
    expect(overlay).not.toBeNull()
    expect(overlay.textContent).toContain('File Tools')
    expect(overlay.textContent).toContain('bundle')
    expect(overlay.textContent).toContain('project.read')
    // Tool descriptions live in the hover tip on the callable id, not inline.
    expect(overlay.textContent).not.toContain('Read a file.')
    expect(overlay.textContent).toContain('builtin:bundle:shell-tools')
    expect(overlay.textContent).toContain('Use tools for file edits.')
    expect(workspaceGet).toHaveBeenCalledTimes(1)
    expect(projectGet).not.toHaveBeenCalled()
    expect(appmanagerGet).not.toHaveBeenCalled()

    const idAnchor = overlay.querySelector<HTMLElement>('.component-detail-overlay-tool-id')!
    expect(idAnchor.classList.contains('has-desc')).toBe(true)
    expect(document.body.querySelector('[data-testid="component-tool-tip-project.read"]')).toBeNull()
    act(() => { idAnchor.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })) })
    const toolTip = document.body.querySelector('[data-testid="component-tool-tip-project.read"]')
    expect(toolTip).not.toBeNull()
    expect(toolTip!.textContent).toContain('Read a file.')
    act(() => { idAnchor.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })) })
    expect(document.body.querySelector('[data-testid="component-tool-tip-project.read"]')).toBeNull()
  })

  it('prefers the project catalog and skips the workspace when it resolves', async () => {
    projectGet.mockResolvedValue({ Component: descriptor() })
    await render({ projectId: 'p1' })

    expect(projectGet).toHaveBeenCalledTimes(1)
    expect(workspaceGet).not.toHaveBeenCalled()
  })

  it('falls back workspace → appmanager when earlier attempts fail', async () => {
    workspaceGet.mockRejectedValue(new Error('no such card'))
    appmanagerGet.mockResolvedValue({ Component: descriptor({ Ref: { CardId: 'app-bundle:x', Kind: 'bundle' } }) })
    await render()

    expect(appmanagerGet).toHaveBeenCalledTimes(1)
    expect(document.querySelector('[data-testid="component-detail-capabilities"]')).not.toBeNull()
  })

  it('shows the load error when every catalog fails', async () => {
    workspaceGet.mockRejectedValue(new Error('boom'))
    appmanagerGet.mockRejectedValue(new Error('nope'))
    await render({ projectId: 'p1' })

    const overlay = document.querySelector('[data-testid="component-detail-overlay"]') as HTMLElement
    expect(overlay).not.toBeNull()
    expect(overlay.textContent).toContain('composer.badgeDetail.loadError')
    expect(overlay.textContent).toContain('nope')
  })

  it('renders nothing while closed', async () => {
    workspaceGet.mockResolvedValue({ Component: descriptor() })
    await render({ open: false })
    expect(container.textContent).toBe('')
    expect(workspaceGet).not.toHaveBeenCalled()
  })
})
