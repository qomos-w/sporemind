import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AgentMessageToolView, parseAgentMessage, isAgentMessageFailed } from './AgentMessageToolView'
import type { ToolFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Identity translator: returns the key (params appended for count keys) so
// assertions verify the component wires the correct i18n keys.
vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) }),
}))

describe('AgentMessageToolView', () => {
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
      root.render(<AgentMessageToolView frame={frame} />)
    })
  }

  const baseFrame = (overrides: Partial<ToolFrame> = {}): ToolFrame => ({
    id: 'am1',
    type: 'tool',
    toolName: 'workspace.agent_send_message',
    status: 'completed',
    input: '',
    ...overrides,
  })

  describe('parseAgentMessage', () => {
    it('parses send input and Sent flag from output', () => {
      const data = parseAgentMessage(
        'workspace.agent_send_message',
        JSON.stringify({ Text: 'hello', ToAgentId: 'Build Weaver#635e' }),
        JSON.stringify({ Sent: true }),
      )
      expect(data.action).toBe('send')
      expect(data.targetAgentId).toBe('Build Weaver#635e')
      expect(data.text).toBe('hello')
      expect(data.sent).toBe(true)
      expect(data.messages).toEqual([])
    })

    it('parses read messages with seq/role/content and HasMore', () => {
      const data = parseAgentMessage(
        'workspace.agent_read_message',
        JSON.stringify({ ToAgentId: 'Build Weaver#635e' }),
        JSON.stringify({
          Items: [
            { Seq: 1, Role: 'user', Content: 'ping' },
            { Seq: 5, Role: 'assistant', Content: 'pong' },
          ],
          HasMore: false,
        }),
      )
      expect(data.action).toBe('read')
      expect(data.targetAgentId).toBe('Build Weaver#635e')
      expect(data.messages).toEqual([
        { seq: 1, role: 'user', content: 'ping' },
        { seq: 5, role: 'assistant', content: 'pong' },
      ])
      expect(data.hasMore).toBe(false)
    })

    it('renders newest-first backend items chronologically', () => {
      const data = parseAgentMessage(
        'workspace.agent_read_message',
        JSON.stringify({ ToAgentId: 'Cache Cat#555a' }),
        JSON.stringify({
          Items: [
            { Seq: 701, Role: 'assistant', Content: 'newest' },
            { Seq: 695, Role: 'assistant', Content: 'oldest' },
            { Seq: 698, Role: 'assistant', Content: 'middle' },
          ],
          HasMore: true,
        }),
      )
      expect(data.messages.map(m => m.seq)).toEqual([695, 698, 701])
      expect(data.messages.map(m => m.content)).toEqual(['oldest', 'middle', 'newest'])
    })

    it('falls back to index-based seq for items missing Seq', () => {
      const data = parseAgentMessage(
        'workspace.agent_read_message',
        '',
        JSON.stringify({ Items: [{ Role: 'assistant', Content: 'hi' }] }),
      )
      expect(data.messages[0]?.seq).toBe(1)
    })

    it('tolerates non-JSON output', () => {
      const data = parseAgentMessage('workspace.agent_read_message', '', 'stream error')
      expect(data.messages).toEqual([])
      expect(data.sent).toBeUndefined()
    })
  })

  describe('isAgentMessageFailed', () => {
    it('fails on frame error status', () => {
      const data = parseAgentMessage('workspace.agent_send_message', '{}', '')
      expect(isAgentMessageFailed(data, 'error')).toBe(true)
    })

    it('fails when send result reports Sent=false', () => {
      const data = parseAgentMessage('workspace.agent_send_message', '{}', JSON.stringify({ Sent: false }))
      expect(isAgentMessageFailed(data, 'completed')).toBe(true)
    })

    it('does not fail on successful send', () => {
      const data = parseAgentMessage('workspace.agent_send_message', '{}', JSON.stringify({ Sent: true }))
      expect(isAgentMessageFailed(data, 'completed')).toBe(false)
    })
  })

  it('renders completed send with delivered badge and outgoing bubble', async () => {
    const frame = baseFrame({
      input: JSON.stringify({ Text: '测试消息', ToAgentId: 'Build Weaver#635e' }),
      output: JSON.stringify({ Sent: true }),
    })
    await renderView(frame)

    expect(container.querySelector('.ai-agent-message-badge')).not.toBeNull()
    expect(container.textContent).toContain('ai.tool.agentMessage.sent')
    expect(container.textContent).toContain('Build Weaver#635e')
    expect(container.textContent).toContain('测试消息')
    const bubbleRow = container.querySelector('.ai-agent-message-bubble-row.out')
    expect(bubbleRow).not.toBeNull()
    expect(bubbleRow?.textContent).toContain('ai.tool.agentMessage.me')
  })

  it('renders read result as incoming/outgoing bubbles with count badge', async () => {
    const frame = baseFrame({
      toolName: 'workspace.agent_read_message',
      input: JSON.stringify({ ToAgentId: 'Build Weaver#635e' }),
      output: JSON.stringify({
        Items: [
          { Seq: 1, Role: 'user', Content: '测试消息' },
          { Seq: 5, Role: 'assistant', Content: '收到' },
        ],
        HasMore: false,
      }),
    })
    await renderView(frame)

    expect(container.textContent).toContain('ai.tool.agentMessage.messages:{"count":2}')
    const out = container.querySelector('.ai-agent-message-bubble-row.out')
    const inc = container.querySelector('.ai-agent-message-bubble-row.in')
    expect(out?.textContent).toContain('测试消息')
    expect(inc?.textContent).toContain('收到')
    expect(inc?.textContent).toContain('ai.tool.agentMessage.peer')
  })

  it('renders empty read result hint', async () => {
    const frame = baseFrame({
      toolName: 'workspace.agent_read_message',
      input: JSON.stringify({ ToAgentId: 'X#1' }),
      output: JSON.stringify({ Items: [], HasMore: false }),
    })
    await renderView(frame)
    expect(container.textContent).toContain('ai.tool.agentMessage.empty')
  })

  it('renders running state label', async () => {
    const frame = baseFrame({
      status: 'running',
      input: JSON.stringify({ Text: 'hi', ToAgentId: 'X#1' }),
    })
    await renderView(frame)
    expect(container.textContent).toContain('ai.tool.agentMessage.sending')
    expect(container.textContent).toContain('X#1')
  })

  it('renders failed state with error output', async () => {
    const frame = baseFrame({
      status: 'error',
      input: JSON.stringify({ Text: 'hi', ToAgentId: 'X#1' }),
      output: 'agent offline',
    })
    await renderView(frame)
    expect(container.textContent).toContain('ai.tool.agentMessage.failed')
    expect(container.textContent).toContain('agent offline')
  })
})
