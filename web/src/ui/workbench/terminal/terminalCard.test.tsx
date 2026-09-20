import { describe, it, expect, vi } from 'vitest'

import { createTerminalCardDescriptor, TERMINAL_CARD_ID } from './terminalCard'
import { WorkbenchTerminalPanel } from './WorkbenchTerminalPanel'

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/shell/client', () => ({
  sessionOpen: vi.fn(),
  sessionFetch: vi.fn(),
  sessionWrite: vi.fn(),
  sessionResize: vi.fn(),
  sessionClose: vi.fn(),
  OnShellSessionOutput: vi.fn(() => () => {}),
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    loadAddon() {}
    open() {}
    onData() { return { dispose() {} } }
    write() {}
    focus() {}
    clear() {}
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

describe('createTerminalCardDescriptor', () => {
  it('produces a terminal descriptor matching the spec contract', () => {
    const descriptor = createTerminalCardDescriptor()
    expect(descriptor.id).toBe(TERMINAL_CARD_ID)
    expect(descriptor.kind).toBe('terminal')
    expect(descriptor.icon).toBe('terminal')
    expect(descriptor.pinned).toBe(false)
    expect(descriptor.compactMeta.statusText).toBeTypeOf('string')
  })

  it('carries options through to the descriptor and its rendered body', () => {
    const onActivity = vi.fn()
    const descriptor = createTerminalCardDescriptor({
      title: 'Terminal',
      icon: 'square-terminal',
      color: '#3a413d',
      statusText: 'running',
      projectRoot: '/proj',
      active: false,
      onActivity,
    })
    expect(descriptor.icon).toBe('square-terminal')
    expect(descriptor.color).toBe('#3a413d')
    expect(descriptor.compactMeta.statusText).toBe('running')

    const element = descriptor.render(false) as { type: unknown; props: Record<string, unknown> }
    expect(element.type).toBe(WorkbenchTerminalPanel)
    expect(element.props.active).toBe(false)
    expect(element.props.projectRoot).toBe('/proj')
    expect(element.props.expanded).toBe(false)
    expect(element.props.onActivity).toBe(onActivity)
  })
})
