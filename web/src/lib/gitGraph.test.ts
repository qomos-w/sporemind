import { describe, it, expect } from 'vitest'
import { computeGitGraph, GRAPH_COLORS, type GitGraphCommit } from './gitGraph'

function commits(list: Array<[string, string[]]>): GitGraphCommit[] {
  return list.map(([sha, parents]) => ({ sha, parents }))
}

describe('computeGitGraph', () => {
  it('assigns a single linear history a single lane', () => {
    const { rows } = computeGitGraph(commits([
      ['a', ['b']],
      ['b', ['c']],
      ['c', []],
    ]))
    expect(rows.map(r => r.commitCol)).toEqual([0, 0, 0])
    expect(rows[0]!.outputLanes[0]!.id).toBe('b')
    expect(rows[1]!.outputLanes).toHaveLength(1)
    expect(rows[1]!.outputLanes[0]!.id).toBe('c')
  })

  it('keeps the first parent on the same lane (vertical continuation)', () => {
    const { rows } = computeGitGraph(commits([
      ['a', ['b']],
      ['b', []],
    ]))
    expect(rows[1]!.commitCol).toBe(0)
    expect(rows[1]!.inputLanes).toHaveLength(1)
    expect(rows[1]!.inputLanes[0]!.id).toBe('b')
    expect(rows[1]!.outputLanes).toHaveLength(0)
  })

  it('draws a merge with first parent continuing and second parent branching', () => {
    const { rows, maxCols } = computeGitGraph(commits([
      ['m', ['a', 'b']],
      ['a', []],
      ['b', []],
    ]))
    const mergeRow = rows[0]!
    expect(mergeRow.commitCol).toBe(0)
    expect(mergeRow.isMerge).toBe(true)
    expect(maxCols).toBe(2)
    expect(mergeRow.outputLanes[0]!.id).toBe('a')
    expect(mergeRow.outputLanes[1]!.id).toBe('b')
  })

  it('reuses the existing lane of a parent seen earlier', () => {
    const { rows } = computeGitGraph(commits([
      ['c', ['b']],
      ['b', ['a']],
      ['a', []],
    ]))
    expect(rows.map(r => r.commitCol)).toEqual([0, 0, 0])
  })

  it('handles two independent heads that later join', () => {
    const { rows } = computeGitGraph(commits([
      ['h1', ['m']],
      ['h2', ['m']],
      ['m', ['base']],
      ['base', []],
    ]))
    expect(rows[0]!.commitCol).toBe(0)
    expect(rows[1]!.commitCol).toBe(1)
    expect(rows[2]!.commitCol).toBeGreaterThanOrEqual(0)
    expect(rows[2]!.outputLanes[0]!.id).toBe('base')
  })

  it('cycles branch colors', () => {
    const { rows } = computeGitGraph(commits([
      ['m', ['a', 'b', 'c']],
      ['a', []],
      ['b', []],
      ['c', []],
    ]))
    const used = new Set(rows[0]!.outputLanes.map(l => l.color))
    expect(used.size).toBeGreaterThanOrEqual(2)
    expect(GRAPH_COLORS[rows[0]!.outputLanes[0]!.color % GRAPH_COLORS.length]).toBeTruthy()
  })

  it('carries surviving lanes through a parentless commit from an unrelated root', () => {
    // Two unrelated histories interleaved by commit date: r1 (root of history
    // one) is listed while r2 (root of history two) is still pending on a
    // lane. The r2 lane must survive the r1 row so r2 renders on its line.
    const { rows } = computeGitGraph(commits([
      ['m', ['r1', 'f']],
      ['f', ['r2']],
      ['r1', []],
      ['r2', []],
    ]))
    expect(rows[2]!.outputLanes.some(l => l.id === 'r2')).toBe(true)
    const carriedCol = rows[2]!.outputLanes.findIndex(l => l.id === 'r2')
    expect(rows[3]!.commitCol).toBe(carriedCol)
  })
})

describe('computeGitGraph lane survival invariant (fuzz)', () => {
  function mulberry32(seed: number): () => number {
    let a = seed >>> 0
    return () => {
      a = (a + 0x6d2b79f5) >>> 0
      let t = a
      t = Math.imul(t ^ (t >>> 15), t | 1)
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296
    }
  }

  function randomDag(rand: () => number, n: number): GitGraphCommit[] {
    const list: GitGraphCommit[] = []
    for (let i = 0; i < n; i++) {
      const older = n - 1 - i
      const parents: string[] = []
      if (older > 0 && rand() > 0.25) {
        const count = 1 + Math.floor(rand() * Math.min(3, older))
        const pool = Array.from({ length: older }, (_, k) => `c${i + 1 + k}`)
        for (let p = 0; p < count && pool.length > 0; p++) {
          const pick = Math.floor(rand() * pool.length)
          parents.push(pool.splice(pick, 1)[0]!)
        }
      }
      list.push({ sha: `c${i}`, parents })
    }
    return list
  }

  it('keeps every lane alive until its commit row or the end of history', () => {
    for (let seed = 1; seed <= 200; seed++) {
      const rand = mulberry32(seed)
      const list = randomDag(rand, 4 + Math.floor(rand() * 11))
      const { rows } = computeGitGraph(list)
      const rowOfSha = new Map(list.map((c, i) => [c.sha, i]))
      rows.forEach((row, i) => {
        for (const lane of row.outputLanes) {
          const target = rowOfSha.get(lane.id)
          if (target === undefined || target <= i) continue
          for (let k = i + 1; k < target; k++) {
            expect(
              rows[k]!.outputLanes.some(l => l.id === lane.id),
              `seed ${seed}: lane ${lane.id} died at row ${k} (sha ${rows[k]!.sha}) before its commit row ${target}`,
            ).toBe(true)
          }
        }
      })
    }
  })
})