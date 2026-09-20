import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { McpToolView } from './McpToolView'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('McpToolView', () => {
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
      root.render(<McpToolView frame={frame} />)
    })
  }

  it('renders input arguments as JSON and the result text', async () => {
    const frame: ToolFrame = {
      id: 'm1',
      type: 'tool',
      toolName: 'mcp.github.get_issue',
      status: 'completed',
      input: JSON.stringify({ owner: 'qomos-w', repo: 'sporemind', issue: 42 }),
      output: 'Issue #42: fix MCP rendering',
    }
    await renderView(frame)

    const sections = container.querySelectorAll('.ai-tool-section')
    expect(sections.length).toBe(2)
    expect(sections[0]?.textContent).toContain('"owner"')
    expect(sections[0]?.textContent).toContain('qomos-w')
    expect(sections[1]?.textContent).toContain('Result')
    const codeBlocks = container.querySelectorAll('.ai-tool-code')
    expect(codeBlocks.length).toBe(2)
    expect(codeBlocks[1]?.textContent).toContain('Issue #42: fix MCP rendering')
  })

  it('shows error styling on failure', async () => {
    const frame: ToolFrame = {
      id: 'm2',
      type: 'tool',
      toolName: 'mcp.github.get_issue',
      status: 'error',
      input: JSON.stringify({ owner: 'missing' }),
      output: 'tool call failed: not found',
    }
    await renderView(frame)

    const err = container.querySelector('.ai-tool-code.error')
    expect(err?.textContent).toContain('tool call failed: not found')
  })

  it('renders spinner while running and hides result', async () => {
    const frame: ToolFrame = {
      id: 'm3',
      type: 'tool',
      toolName: 'mcp.github.get_issue',
      status: 'running',
      input: JSON.stringify({ owner: 'qomos-w' }),
    }
    await renderView(frame)
    expect(container.querySelector('.ai-tool-running')).not.toBeNull()
    expect(container.textContent).not.toContain('Result')
  })

  it('shows callable method row when present', async () => {
    const frame: ToolFrame = {
      id: 'm4',
      type: 'tool',
      toolName: 'mcp.github.get_issue',
      callableId: 'mcp.srv-1.get_issue',
      targetService: 'mcp',
      status: 'completed',
      input: '{}',
      output: 'ok',
    }
    await renderView(frame)
    expect(container.querySelector('.ai-tool-summary-value')?.textContent).toBe('mcp.srv-1.get_issue')
  })

  it('renders a real filesystem-server read_file result', async () => {
    // Mirrors the frame shape the backend turn engine backfills after calling
    // the official filesystem server (turn_engine_mcp_filesystem_e2e_test.go):
    // toolName mcp.<server>.<tool>, callableId mcp.srv-0.<tool>, JSON input
    // args, and the server's text content in output.
    const frame: ToolFrame = {
      id: 'm5',
      type: 'tool',
      toolName: 'mcp.filesystem.read_file',
      callableId: 'mcp.srv-0.read_file',
      targetService: 'mcp',
      status: 'completed',
      input: JSON.stringify({ path: 'C:/tmp/mcp-root/marker.txt' }),
      output: 'hello from the real filesystem server',
    }
    await renderView(frame)

    expect(container.querySelector('.ai-tool-summary-value')?.textContent).toBe('mcp.srv-0.read_file')
    const sections = container.querySelectorAll('.ai-tool-section')
    expect(sections[0]?.textContent).toContain('"path"')
    expect(sections[0]?.textContent).toContain('C:/tmp/mcp-root/marker.txt')
    expect(sections[1]?.textContent).toContain('Result')
    const codeBlocks = container.querySelectorAll('.ai-tool-code')
    expect(codeBlocks.length).toBe(2)
    expect(codeBlocks[1]?.textContent).toContain('hello from the real filesystem server')
    expect(container.querySelector('.ai-tool-section')?.textContent).not.toContain('Error')
  })
})
