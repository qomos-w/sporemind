import { describe, expect, it } from 'vitest'

import { buildCommitPrompt, isGeneratedPath } from './commitPrompt'

function bigDiff(path: string, lines: number): string {
  const body = Array.from({ length: lines }, (_, i) => `+line ${i} of ${path}`).join('\n')
  return `--- a/${path}\n+++ b/${path}\n@@ -1,2 +1,${lines + 1} @@\n context\n${body}`
}

describe('isGeneratedPath', () => {
  it('flags locks, generated Go schema parts and build dirs', () => {
    expect(isGeneratedPath('package-lock.json')).toBe(true)
    expect(isGeneratedPath('web/package-lock.json')).toBe(true)
    expect(isGeneratedPath('pkg/domain/gen/agent.gen.go')).toBe(true)
    expect(isGeneratedPath('pkg/domain/gen/aigen.part1.gen.go')).toBe(true)
    expect(isGeneratedPath('web/dist/main.js')).toBe(true)
    expect(isGeneratedPath('src/foo.ts')).toBe(false)
    expect(isGeneratedPath('a/gen.go.bak')).toBe(false)
  })
})

describe('buildCommitPrompt', () => {
  const input = {
    files: [
      { path: 'src/a.ts', status: 'M', diff: bigDiff('src/a.ts', 3) },
      { path: 'src/b.ts', status: 'A', diff: '' },
    ],
    recentCommits: ['  abc1234 feat: add panel'],
  }

  it('builds a manifest with status letters and +/- counts', () => {
    const p = buildCommitPrompt(input)
    expect(p).toContain('M src/a.ts (+3 -0)')
    expect(p).toContain('A src/b.ts')
    expect(p).toContain('abc1234 feat: add panel')
    expect(p).toContain('Return ONLY the title')
  })

  it('hard-caps the total excerpt budget', () => {
    const many = Array.from({ length: 30 }, (_, i) => ({
      path: `src/f${i}.ts`,
      status: 'M',
      diff: bigDiff(`src/f${i}.ts`, 400),
    }))
    const p = buildCommitPrompt({ files: many, recentCommits: [] })
    expect(p.length).toBeLessThan(6000 + 30 * 40 + 600)
    expect(p).toMatch(/file?s? not excerpted\)/)
  })

  it('caps a single oversized file at the whole-file cap', () => {
    const p = buildCommitPrompt({ files: [{ path: 'src/big.ts', status: 'M', diff: bigDiff('src/big.ts', 5000) }], recentCommits: [] })
    const excerpt = p.split('Diff excerpts:\n')[1] ?? ''
    expect(excerpt.length).toBeLessThan(1400)
    expect(excerpt).toContain('(src/big.ts truncated)')
  })

  it('lists generated files in the manifest but never excerpts them', () => {
    const p = buildCommitPrompt({
      files: [
        { path: 'src/a.ts', status: 'M', diff: bigDiff('src/a.ts', 2) },
        { path: 'pkg/domain/gen/agent.gen.go', status: 'M', diff: bigDiff('agent.gen.go', 2) },
      ],
      recentCommits: [],
    })
    expect(p).toContain('pkg/domain/gen/agent.gen.go')
    expect(p).not.toContain('--- pkg/domain/gen/agent.gen.go')
  })

  it('works with no diff text at all', () => {
    const p = buildCommitPrompt({ files: [{ path: 'src/x.ts', status: 'D', diff: '' }], recentCommits: [] })
    expect(p).toContain('D src/x.ts')
    expect(p).not.toContain('Diff excerpts:')
  })
})
