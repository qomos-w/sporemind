import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ApprovalAuditPanel, PERMISSION_BYPASS_SOURCE } from './ApprovalAuditPanel'
import type { Diagnostic } from '../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Identity translator: returns the key so assertions verify the component
// wires the correct i18n keys (mirrors the AgentReviewToolView test pattern).
vi.mock('../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

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

function makeBypassDiagnostic(overrides: Partial<Diagnostic> = {}): Diagnostic {
  return {
    Id: 'diag-1',
    Severity: 'info',
    Source: PERMISSION_BYPASS_SOURCE,
    Message: 'bypass approved by fast model',
    Timestamp: '2026-08-29T12:00:00.000000000Z',
    AgentId: 'agent-1',
    TurnId: 'turn-42',
    CallableId: 'project.write,project.shell_exec',
    Input: '1. Tool: write_file (project.write)\n   Input: {path:"README.md"}\n2. Tool: exec (project.shell_exec)\n   Input: ls',
    Unit: { model: 'fast-model', provider: 'fast-provider' },
    ...overrides,
  }
}

describe('ApprovalAuditPanel rendering', () => {
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
      root.render(<ApprovalAuditPanel />)
    })
  }

  it('renders only permission_bypass records from history', async () => {
    const items: Diagnostic[] = [
      makeBypassDiagnostic({ Id: 'd1', Severity: 'info' }),
      makeBypassDiagnostic({ Id: 'd2', Severity: 'warning', Message: 'bypass denied by fast model: policy restrict' }),
      { Id: 'd3', Severity: 'error', Source: 'tool_call', Message: 'tool call failed', Timestamp: '2026-08-29T12:00:00.000000000Z' },
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    expect(container.textContent).toContain('bypass approved by fast model')
    expect(container.textContent).toContain('bypass denied by fast model: policy restrict')
    expect(container.textContent).toContain('project.write,project.shell_exec')
    expect(container.textContent).not.toContain('tool call failed')
    expect(container.textContent).toMatch(/approvalAudit\.filter\.all\s*2/)
  })

  it('shows decision counts (All/Approved/Denied)', async () => {
    const items: Diagnostic[] = [
      makeBypassDiagnostic({ Id: 'd1', Severity: 'info' }),
      makeBypassDiagnostic({ Id: 'd2', Severity: 'info' }),
      makeBypassDiagnostic({ Id: 'd3', Severity: 'warning' }),
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    expect(container.textContent).toMatch(/approvalAudit\.filter\.all\s*3/)
    expect(container.textContent).toMatch(/approvalAudit\.filter\.approved\s*2/)
    expect(container.textContent).toMatch(/approvalAudit\.filter\.denied\s*1/)
  })

  it('Approved filter narrows to approvals only', async () => {
    const items: Diagnostic[] = [
      makeBypassDiagnostic({ Id: 'd1', Severity: 'info' }),
      makeBypassDiagnostic({ Id: 'd2', Severity: 'warning', Message: 'bypass denied by fast model' }),
    ]
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: items })

    await renderPanel()

    const approvedBtn = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('approvalAudit.filter.approved'))
    expect(approvedBtn).toBeDefined()

    await act(async () => {
      approvedBtn!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('bypass approved by fast model')
    expect(container.textContent).not.toContain('bypass denied by fast model')
  })

  it('expands to show tools, input and fast model', async () => {
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: [makeBypassDiagnostic()] })

    await renderPanel()

    const rowLabel = Array.from(container.querySelectorAll('span'))
      .find(s => s.textContent === 'approvalAudit.status.approved')
    expect(rowLabel).toBeDefined()
    const row = rowLabel!.closest('div')?.parentElement as HTMLElement
    expect(row).not.toBeNull()

    await act(async () => {
      row.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const expanded = container.textContent ?? ''
    expect(expanded).toContain('bypass approved by fast model')
    expect(expanded).toContain('approvalAudit.field.callable: project.write,project.shell_exec')
    expect(expanded).toContain('write_file (project.write)')
    expect(expanded).toContain('approvalAudit.field.model: fast-model')
    expect(expanded).toContain('approvalAudit.field.provider: fast-provider')
    expect(expanded).toContain('approvalAudit.field.agent: agent-1')
    expect(expanded).toContain('approvalAudit.field.turn: turn-42')
  })

  it('subscribes to real-time bypass approvals and ignores other diagnostics', async () => {
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: [] })

    await renderPanel()

    expect(oracleMocks.onDiagnosticHandlers.size).toBe(1)

    await act(async () => {
      oracleMocks.onDiagnosticHandlers.forEach(h => h(makeBypassDiagnostic({ Id: 'live-1', Message: 'bypass denied by fast model: safety' })))
      oracleMocks.onDiagnosticHandlers.forEach(h => h({ Id: 'live-2', Severity: 'error', Source: 'tool_call', Message: 'tool call failed', Timestamp: '2026-08-29T12:00:00.000000000Z' }))
    })

    expect(container.textContent).toContain('bypass denied by fast model: safety')
    expect(container.textContent).not.toContain('tool call failed')
  })

  it('caps the real-time approval list at 200 entries', async () => {
    oracleMocks.listDiagnostics.mockResolvedValue({ Items: [] })

    await renderPanel()

    await act(async () => {
      for (let i = 0; i < 250; i++) {
        // Use a unique Message per record so the cap can be observed in the
        // rendered textContent (the Id itself is not part of the row UI).
        oracleMocks.onDiagnosticHandlers.forEach(h => h(makeBypassDiagnostic({
          Id: `live-${i}`,
          Message: `bypass approved by fast model #${i}`,
        })))
      }
    })

    expect(container.textContent).not.toContain('bypass approved by fast model #0')
    expect(container.textContent).not.toContain('bypass approved by fast model #49')
    expect(container.textContent).toContain('bypass approved by fast model #50')
    expect(container.textContent).toContain('bypass approved by fast model #249')
  })
})
