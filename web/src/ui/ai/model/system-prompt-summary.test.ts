import { describe, it, expect } from 'vitest'
import { summarizeSystemPrompt, truncate } from './system-prompt-summary'

describe('truncate', () => {
  it('returns short text unchanged', () => {
    expect(truncate('hello world')).toBe('hello world')
  })
  it('truncates long text with ellipsis', () => {
    const long = 'x'.repeat(70)
    expect(truncate(long, 60)).toBe('x'.repeat(60) + '…')
  })
  it('collapses intra-line whitespace', () => {
    expect(truncate('a   b\nc   d', 60)).toBe('a b c d')
  })
})

describe('summarizeSystemPrompt', () => {
  // ── Goal messages ────────────────────────────────────────────────

  it('parses goal continue with turn suffix (meta="goal")', () => {
    const result = summarizeSystemPrompt('goal', "We'll continue working toward the active goal. (turn 3 of 10)")
    expect(result.kind).toBe('goal')
    expect(result.summary).toContain('(turn 3 of 10)')
  })

  it('parses goal continue without turn suffix (maxTurns<=0)', () => {
    const result = summarizeSystemPrompt('goal', "We'll continue working toward the active goal.")
    expect(result.kind).toBe('goal')
    expect(result.summary).toContain('active goal')
  })

  it('infers goal from text when meta is undefined', () => {
    const result = summarizeSystemPrompt(undefined, "We'll continue working toward the active goal. (turn 3 of 10)")
    expect(result.kind).toBe('goal')
    expect(result.summary).toContain('(turn 3 of 10)')
  })

  // ── Workflow messages ────────────────────────────────────────────

  it('parses frontier message', () => {
    const text = 'Map [[abc123]] has 3 frontier tickets ready:\n\n- [[card1]]\n- [[card2]]\n\nCheck: ...'
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('3 frontier tickets ready')
  })

  it('parses worker status update ("items" plural)', () => {
    const text = 'Workflow status update (2 items):\n\n- Agent X (actor: ..., task: ...): ready for review branch: ...'
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('Workflow status update (2 items)')
  })

  it('parses worker status update ("item" singular)', () => {
    const text = 'Workflow status update (1 item):\n\n- Agent X: ready for review'
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('Workflow status update (1 item)')
  })

  it('parses nudge message, stripping trailing closer', () => {
    const text = "We'll continue: 2 to review, 1 unresponsive."
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe("We'll continue: 2 to review, 1 unresponsive.")
  })

  it('parses nudge with random closer parenthesis', () => {
    const text = "We'll continue: 2 to review, 1 unresponsive. (they've been idle a while)"
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe("We'll continue: 2 to review, 1 unresponsive.")
  })

  it('parses tree-exhausted message', () => {
    const text = 'No frontier tasks or active workers remain. Assess whether...'
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('No frontier tasks or active workers remain.')
  })

  // ── Meta-based classification ────────────────────────────────────

  it('sets kind=goal from meta=goal even for arbitrary text', () => {
    const result = summarizeSystemPrompt('goal', 'some random text that does not match any pattern')
    expect(result.kind).toBe('goal')
    expect(result.summary).toBe('some random text that does not match any pattern')
  })

  it('sets kind=workflow from meta=workflow even for arbitrary text', () => {
    const result = summarizeSystemPrompt('workflow', 'some random text that does not match any pattern')
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('some random text that does not match any pattern')
  })

  // ── No-match fallback ────────────────────────────────────────────

  it('fallback to generic when no meta and no pattern matches', () => {
    const text = 'This is an ordinary user message that happens to be long enough to trigger truncation for testing purposes.'
    const result = summarizeSystemPrompt(undefined, text)
    expect(result.kind).toBe('generic')
    expect(result.summary.length).toBeLessThanOrEqual(63) // 60 + ellipsis
  })

  it('returns generic with empty summary for empty text', () => {
    const result = summarizeSystemPrompt(undefined, '')
    expect(result.kind).toBe('generic')
    expect(result.summary).toBe('')
  })

  it('returns generic with empty summary for whitespace-only text', () => {
    const result = summarizeSystemPrompt(undefined, '   ')
    expect(result.kind).toBe('generic')
    expect(result.summary).toBe('')
  })

  // ── Variant closer handling ──────────────────────────────────────

  it('parses nudge with different closer style', () => {
    const text = "We'll continue: 1 completed, 2 pending. (check back later)"
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe("We'll continue: 1 completed, 2 pending.")
  })

  it('handles nudge match from meta=workflow with no prefix', () => {
    // This doesn't start with "We'll continue:" but meta=workflow so it's workflow.
    const text = 'Random workflow updater message with no matching pattern.'
    const result = summarizeSystemPrompt('workflow', text)
    expect(result.kind).toBe('workflow')
    expect(result.summary).toBe('Random workflow updater message with no matching pattern.')
  })
})