import { describe, it, expect } from 'vitest'
import { frameEqual } from './FrameRenderer.tsx'
import type { ToolFrame } from '../../model/frame-types.ts'

function makeTool(partial: Partial<ToolFrame> = {}): ToolFrame {
  return {
    id: 'f1',
    type: 'tool',
    status: 'completed',
    toolName: 'runcommand',
    input: '{"command":"ls"}',
    ...partial,
  }
}

// A large accumulated stdout: simulate an append-only shell buffer.
const LONG_STDOUT = 'line\n'.repeat(5000)

describe('frameEqual tool case', () => {
  it('returns true for identical scalar content', () => {
    const a = makeTool({ output: 'done', exitCode: 0, durationSeconds: 1.5 })
    const b = makeTool({ output: 'done', exitCode: 0, durationSeconds: 1.5 })
    expect(frameEqual(a, b)).toBe(true)
  })

  it('returns false when a scalar content field changes', () => {
    const base = makeTool()
    expect(frameEqual(base, makeTool({ toolName: 'other' }))).toBe(false)
    expect(frameEqual(base, makeTool({ input: '{"command":"pwd"}' }))).toBe(false)
    expect(frameEqual(base, makeTool({ output: 'done' }))).toBe(false)
    expect(frameEqual(base, makeTool({ exitCode: 1 }))).toBe(false)
    expect(frameEqual(base, makeTool({ status: 'running' }))).toBe(false)
  })

  it('treats undefined and missing sub-objects consistently', () => {
    const base = makeTool()
    // both absent
    expect(frameEqual(base, makeTool())).toBe(true)
    // one side gained a snapshotRef → changed (safe direction)
    expect(frameEqual(base, makeTool({
      snapshotRef: { id: 's1', path: 'a.ts', size: 10 },
    }))).toBe(false)
  })

  it('treats undefined vs missing known fields inside a sub-object as equal', () => {
    const a = makeTool({ progress: {} })
    const b = makeTool({ progress: { phase: undefined } })
    expect(frameEqual(a, b)).toBe(true)

    const c = makeTool({ snapshotRef: { id: 's1', path: 'a.ts', size: 10, offset: undefined } })
    const d = makeTool({ snapshotRef: { id: 's1', path: 'a.ts', size: 10 } })
    expect(frameEqual(c, d)).toBe(true)
  })

  it('returns true for equal long append-only runningOutput', () => {
    const a = makeTool({ runningOutput: { stdout: LONG_STDOUT, stderr: '' } })
    const b = makeTool({ runningOutput: { stdout: LONG_STDOUT, stderr: '' } })
    expect(frameEqual(a, b)).toBe(true)
  })

  it('returns false when runningOutput content changes', () => {
    const a = makeTool({ runningOutput: { stdout: LONG_STDOUT, stderr: '' } })
    // appended content
    const b = makeTool({ runningOutput: { stdout: LONG_STDOUT + 'more\n', stderr: '' } })
    expect(frameEqual(a, b)).toBe(false)
    // stderr change
    const c = makeTool({ runningOutput: { stdout: LONG_STDOUT, stderr: 'err' } })
    expect(frameEqual(a, c)).toBe(false)
    // frozen flip
    const d = makeTool({ runningOutput: { stdout: LONG_STDOUT, stderr: '', frozen: true } })
    expect(frameEqual(a, d)).toBe(false)
    // removed entirely
    expect(frameEqual(a, makeTool())).toBe(false)
  })

  it('returns false when progress content changes', () => {
    const base = makeTool({ progress: { phase: 'search', searchCount: 1, activeWork: [{ Type: 'grep', Target: 'src' }] } })
    expect(frameEqual(base, makeTool({ progress: { phase: 'read', searchCount: 1, activeWork: [{ Type: 'grep', Target: 'src' }] } }))).toBe(false)
    expect(frameEqual(base, makeTool({ progress: { phase: 'search', searchCount: 2, activeWork: [{ Type: 'grep', Target: 'src' }] } }))).toBe(false)
    expect(frameEqual(base, makeTool({ progress: { phase: 'search', searchCount: 1, activeWork: [{ Type: 'read', Target: 'src' }] } }))).toBe(false)
    expect(frameEqual(base, makeTool({ progress: { phase: 'search', searchCount: 1, activeWork: [] } }))).toBe(false)
    expect(frameEqual(base, makeTool())).toBe(false)
  })

  it('returns true for equal progress with same activeWork', () => {
    const a = makeTool({ progress: { phase: 'search', searchCount: 1, summaryText: 'done', activeWork: [{ Type: 'grep', Target: 'src' }] } })
    const b = makeTool({ progress: { phase: 'search', searchCount: 1, summaryText: 'done', activeWork: [{ Type: 'grep', Target: 'src' }] } })
    expect(frameEqual(a, b)).toBe(true)
  })

  it('returns false when snapshotRef content changes', () => {
    const base = makeTool({ snapshotRef: { id: 's1', path: 'a.ts', size: 10, startLine: 1, numLines: 5 } })
    expect(frameEqual(base, makeTool({ snapshotRef: { id: 's1', path: 'a.ts', size: 10, startLine: 2, numLines: 5 } }))).toBe(false)
    expect(frameEqual(base, makeTool({ snapshotRef: { id: 's1', path: 'a.ts', size: 11, startLine: 1, numLines: 5 } }))).toBe(false)
    expect(frameEqual(base, makeTool())).toBe(false)
  })

  it('returns false when fileChanges content changes', () => {
    const base = makeTool({ fileChanges: [{ filename: 'a.ts', additions: 1, deletions: 0, diffContent: '+x' }] })
    expect(frameEqual(base, makeTool({ fileChanges: [{ filename: 'a.ts', additions: 2, deletions: 0, diffContent: '+x' }] }))).toBe(false)
    expect(frameEqual(base, makeTool({ fileChanges: [{ filename: 'a.ts', additions: 1, deletions: 0, diffContent: '+y' }] }))).toBe(false)
    expect(frameEqual(base, makeTool({ fileChanges: [] }))).toBe(false)
    expect(frameEqual(base, makeTool())).toBe(false)
  })

  it('returns true for equal fileChanges', () => {
    const a = makeTool({ fileChanges: [{ filename: 'a.ts', filepath: '/x/a.ts', icon: 'ts', additions: 1, deletions: 0, diffContent: '+x' }] })
    const b = makeTool({ fileChanges: [{ filename: 'a.ts', filepath: '/x/a.ts', icon: 'ts', additions: 1, deletions: 0, diffContent: '+x' }] })
    expect(frameEqual(a, b)).toBe(true)
  })

  it('falls back to deep comparison for unknown fields (no silent false negatives)', () => {
    const a = makeTool({ progress: { phase: 'search', futureField: 1 } as ToolFrame['progress'] })
    const b = makeTool({ progress: { phase: 'search', futureField: 1 } as ToolFrame['progress'] })
    const c = makeTool({ progress: { phase: 'search', futureField: 2 } as ToolFrame['progress'] })
    expect(frameEqual(a, b)).toBe(true)
    expect(frameEqual(a, c)).toBe(false)
  })
})
