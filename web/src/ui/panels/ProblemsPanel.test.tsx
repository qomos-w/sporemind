import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ProblemsPanel } from './ProblemsPanel'
import type { Diagnostic } from '../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// vi.mock factories are hoisted above this module's top-level statements in
// vitest v4, so any value referenced inside the factory must itself be created
// via vi.hoisted — otherwise the factory hits the const temporal dead zone.
const oracleMocks = vi.hoisted(() => {
  const listDiagnostics = vi.fn()
  const onDiagnosticHandlers = new Set<(diag: Diagnostic) => void>()
  const onDiagnostic = vi.fn((_client: unknown, handler: (diag: Diagnostic) => void) => {
    onDiagnosticHandlers.add(handler)
    return () => { onDiagnosticHandlers.delete(handler) }
  })
  return { listDiagnostics, onDiagnostic, onDiagnosticHandlers }
})

vi.mock('../../application/generated-client', () => ({
  client: {
    events: { onService: vi.fn(() => () => {}) },
    invoke: vi.fn(),
  },
}))

vi.mock('../../gen-clients/oracle/client', () => ({
  listDiagnostics: oracleMocks.listDiagnostics,
  OnDiagnostic: oracleMocks.onDiagnostic,
}))

function makeDiagnostic(overrides: Partial<Diagnostic> = {}): Diagnostic {
  return {
    Id: 'diag-1',
    Severity: 'error',
    Source: 'tool_call',
    Message: 'tool call failed',
    Timestamp: '2026-08-29T12:00:00.000000000Z',
    AgentId: 'agent-1',
    TurnId: 'turn-42',
    CallableId: 'project.shell_exec',
    ...overrides,
  }
}

describe('ProblemsPanel rendering', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    oracleMocks.listDiagnostics.mockReset()
    oracleMocks.onDiagnostic.mockClear()
    oracleMocks.onDiagnosticHandlers.clear()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn(() => Promise.resolve()) },
      configurable: true,
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  const renderPanel = async () => {
    await act(async () => {
      root.render(<ProblemsPanel />)
    })
  }

  it('renders history records by default', async () => {
    const items: Diagnostic[] = [
      makeDiagnostic({ Id: 'diag-1', Severity: 'error', Message: 'tool call failed' }),
      makeDiagnostic({ Id: 'diag-2', Severity: 'warning', Message: 'deprecated usage' }),
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    expect(container.textContent).toContain('tool call failed')
    expect(container.textContent).toContain('deprecated usage')
  })

  it('shows severity counts (All/Errors/Warnings/Info)', async () => {
    const items: Diagnostic[] = [
      makeDiagnostic({ Id: 'd1', Severity: 'error' }),
      makeDiagnostic({ Id: 'd2', Severity: 'info' }),
      makeDiagnostic({ Id: 'd3', Severity: 'warning' }),
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    expect(container.textContent).toMatch(/All\s*3/)
    expect(container.textContent).toMatch(/Errors\s*1/)
    expect(container.textContent).toMatch(/Warnings\s*1/)
    expect(container.textContent).toMatch(/Info\s*1/)
  })

  it('excludes permission_bypass approval records from history', async () => {
    const items: Diagnostic[] = [
      makeDiagnostic({ Id: 'd1', Severity: 'error' }),
      makeDiagnostic({ Id: 'd2', Severity: 'info', Source: 'permission_bypass', Message: 'bypass approved by fast model', CallableId: 'project.write' }),
      makeDiagnostic({ Id: 'd3', Severity: 'warning', Source: 'permission_bypass', Message: 'bypass denied by fast model', CallableId: 'project.shell_exec' }),
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    expect(container.textContent).toContain('tool call failed')
    expect(container.textContent).not.toContain('bypass approved by fast model')
    expect(container.textContent).not.toContain('bypass denied by fast model')
    expect(container.textContent).toMatch(/All\s*1/)
  })

  it('ignores realtime permission_bypass diagnostics', async () => {
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: [] })

    await renderPanel()

    await act(async () => {
      oracleMocks.onDiagnosticHandlers.forEach(h => h(makeDiagnostic({
        Id: 'live-1',
        Source: 'permission_bypass',
        Severity: 'info',
        Message: 'bypass approved by fast model',
      })))
      oracleMocks.onDiagnosticHandlers.forEach(h => h(makeDiagnostic({
        Id: 'live-2',
        Severity: 'error',
        Message: 'realtime tool failure',
      })))
    })

    expect(container.textContent).not.toContain('bypass approved by fast model')
    expect(container.textContent).toContain('realtime tool failure')
  })

  it('subscribes to real-time diagnostics and prepends new ones', async () => {
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: [] })

    await renderPanel()

    expect(oracleMocks.onDiagnosticHandlers.size).toBe(1)

    const live = makeDiagnostic({ Id: 'live-1', Message: 'realtime drift detected' })
    await act(async () => {
      oracleMocks.onDiagnosticHandlers.forEach(h => h(live))
    })

    expect(container.textContent).toContain('realtime drift detected')
  })
})
