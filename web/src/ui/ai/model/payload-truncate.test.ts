import { describe, it, expect } from 'vitest'
import { truncatePayloadString, truncateHistoryEnvelope, truncateHistoryEnvelopes } from './payload-truncate'
import type { TurnEnvelope, ToolFrame, TextFrame } from './frame-types'

describe('truncatePayloadString', () => {
  it('leaves short strings unchanged', () => {
    const value = 'a'.repeat(2048)
    expect(truncatePayloadString(value)).toBe(value)
  })

  it('truncates long strings keeping head and tail with total length marker', () => {
    const head = 'A'.repeat(1024)
    const middle = 'B'.repeat(5000)
    const tail = 'C'.repeat(1024)
    const value = head + middle + tail

    const result = truncatePayloadString(value)

    expect(result.length).toBeLessThan(2100)
    expect(result.startsWith(head)).toBe(true)
    expect(result.endsWith(tail)).toBe(true)
    expect(result).toContain(`...(truncated ${value.length} chars)...`)
  })

  it('returns the exact input at the boundary length', () => {
    const value = 'x'.repeat(2048)
    expect(truncatePayloadString(value)).toBe(value)
  })

  it('truncates strings just over the limit', () => {
    const value = 'x'.repeat(2049)
    const result = truncatePayloadString(value)
    expect(result).toContain('...(truncated 2049 chars)...')
  })

  it('head and tail remain stable after re-truncation', () => {
    const original = 'A'.repeat(1024) + 'B'.repeat(5000) + 'C'.repeat(1024)
    const once = truncatePayloadString(original)
    const twice = truncatePayloadString(once)
    // Head and tail are identical across truncations
    expect(twice.slice(0, 1024)).toBe(once.slice(0, 1024))
    expect(twice.slice(-1024)).toBe(once.slice(-1024))
    // Marker reflects the truncated length on re-application
    expect(twice).toContain(`...(truncated ${once.length} chars)...`)
  })
})

describe('truncateHistoryEnvelope', () => {
  it('leaves non-tool frames untouched', () => {
    const envelope: TurnEnvelope = {
      id: 'e1',
      role: 'assistant',
      timestamp: '1',
      frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'a'.repeat(5000) } as TextFrame],
    }
    expect(truncateHistoryEnvelope(envelope)).toBe(envelope)
  })

  it('truncates tool frame input and output', () => {
    const longInput = JSON.stringify({ file: 'a'.repeat(5000) })
    const longOutput = 'b'.repeat(5000)
    const envelope: TurnEnvelope = {
      id: 'e1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 't1',
        type: 'tool',
        toolName: 'project.read',
        status: 'completed',
        input: longInput,
        output: longOutput,
      } as ToolFrame],
    }

    const result = truncateHistoryEnvelope(envelope)
    expect(result).not.toBe(envelope)
    const tool = result.frames[0] as ToolFrame
    expect(tool.input.length).toBeLessThan(2100)
    expect(tool.input).toContain('...(truncated')
    expect(tool.output).toBeDefined()
    expect(tool.output!.length).toBeLessThan(2100)
    expect(tool.output).toContain('...(truncated')
  })

  it('leaves short tool payloads unchanged', () => {
    const envelope: TurnEnvelope = {
      id: 'e1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 't1',
        type: 'tool',
        toolName: 'create_task',
        status: 'completed',
        input: JSON.stringify({ Subject: 'short' }),
        output: 'ok',
      } as ToolFrame],
    }
    expect(truncateHistoryEnvelope(envelope)).toBe(envelope)
  })

  it('handles tool frames with undefined output', () => {
    const envelope: TurnEnvelope = {
      id: 'e1',
      role: 'assistant',
      timestamp: '1',
      frames: [{
        id: 't1',
        type: 'tool',
        toolName: 'project.read',
        status: 'running',
        input: JSON.stringify({ Path: 'a'.repeat(5000) }),
      } as ToolFrame],
    }
    const result = truncateHistoryEnvelope(envelope)
    const tool = result.frames[0] as ToolFrame
    expect(tool.output).toBeUndefined()
    expect(tool.input).toContain('...(truncated')
  })
})

describe('truncateHistoryEnvelopes', () => {
  it('returns the same array reference when nothing changes', () => {
    const envelopes: TurnEnvelope[] = [
      {
        id: 'e1',
        role: 'assistant',
        timestamp: '1',
        frames: [{ id: 'f1', type: 'text', status: 'completed', content: 'hi' } as TextFrame],
      },
    ]
    expect(truncateHistoryEnvelopes(envelopes)).toBe(envelopes)
  })

  it('returns a new array when at least one envelope is truncated', () => {
    const envelopes: TurnEnvelope[] = [
      {
        id: 'e1',
        role: 'assistant',
        timestamp: '1',
        frames: [{
          id: 't1',
          type: 'tool',
          toolName: 'project.read',
          status: 'completed',
          input: 'a'.repeat(3000),
          output: 'b'.repeat(3000),
        } as ToolFrame],
      },
    ]
    const result = truncateHistoryEnvelopes(envelopes)
    expect(result).not.toBe(envelopes)
    expect(result[0]).not.toBe(envelopes[0])
  })
})
