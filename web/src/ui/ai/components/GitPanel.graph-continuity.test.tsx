import { expect, it, vi } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { GitCommitInfo } from '../../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const mk = (hash: string, parents: string[]): GitCommitInfo =>
  ({ Hash: hash, Short: hash, Message: hash, Author: 'a', Email: 'a@x', When: '1767225600', Parents: parents }) as GitCommitInfo

const state = {
  projects: [],
  selectedProjectId: '',
  projectId: 'project-1',
  worktreeId: 'wt-1',
  branch: 'main',
  logBranch: 'all',
  logLimit: 100,
  loadingMore: false,
  files: [],
  commits: [] as GitCommitInfo[],
  branches: [],
  selectedCommit: null,
  selectedFile: null,
  diff: [],
  commitDiff: [],
  commitFiles: [],
  loading: false,
  error: null,
}

vi.mock('../../panels/git-store', () => ({
  gitStore: {
    get state() { return state },
    subscribe() { return () => undefined },
    getVersion: () => 1,
    refresh: vi.fn(),
    selectFile: vi.fn(),
    selectCommit: vi.fn(),
    loadCommitFileDiffByPath: vi.fn(),
    stageAll: vi.fn(),
    commit: vi.fn(),
    pull: vi.fn(),
    push: vi.fn(),
    fetch: vi.fn(),
    checkout: vi.fn(),
    setLogBranch: vi.fn(),
    loadMoreHistory: vi.fn(async () => undefined),
    stageFile: vi.fn(),
    unstageFile: vi.fn(),
  },
}))
vi.mock('../../../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../../../gen-clients/workspace/client', () => ({ gitDiff: vi.fn() }))
vi.mock('../../../application/generated-client', () => ({ client: { invoke: vi.fn() } }))
vi.mock('../../../gen-clients/local/client', () => ({ completeMessage: vi.fn() }))

/** Parse the pen's final position of an SVG path. */
function pathEnd(d: string): { x: number; y: number } {
  const parts = d.split(' ')
  let x = NaN
  let y = NaN
  for (let i = 0; i < parts.length; i++) {
    if (parts[i] === 'M' || parts[i] === 'L') {
      x = parseFloat(parts[i + 1]!)
      y = parseFloat(parts[i + 2]!)
    } else if (parts[i] === 'V') {
      y = parseFloat(parts[i + 1]!)
    }
  }
  return { x, y }
}

function pathStart(d: string): { x: number; y: number } {
  const parts = d.split(' ')
  return { x: parseFloat(parts[1]!), y: parseFloat(parts[2]!) }
}

/** Columns where a path reaches the row's bottom boundary (y = 22). */
function bottomXs(ds: string[]): number[] {
  return ds.map(pathEnd).filter(p => p.y === 22).map(p => p.x).sort((a, b) => a - b)
}

/** Columns where a path enters from the row's top boundary (y <= 0). */
function topXs(ds: string[]): number[] {
  return ds.map(pathStart).filter(p => p.y <= 0).map(p => p.x).sort((a, b) => a - b)
}

async function renderGraph(commitList: Array<[string, string[]]>): Promise<string[][]> {
  const { GitPanel } = await import('./GitPanel')
  state.commits = commitList.map(([sha, parents]) => mk(sha, parents))
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<GitPanel projectId="project-1" onOpenFile={vi.fn()} />) })
  const cells = host.querySelectorAll('.git-panel-commit-row .git-panel-graph-cell')
  const rows = [...cells].map(cell => [...cell.querySelectorAll('path')].map(p => p.getAttribute('d')!))
  root.unmount()
  host.remove()
  return rows
}

/** Seam invariant: every line leaving row i at the bottom boundary must
 * continue at the same column from the top of row i+1, and vice versa. */
function expectSeamContinuity(rows: string[][]) {
  for (let i = 0; i + 1 < rows.length; i++) {
    const below = topXs(rows[i + 1]!)
    const above = bottomXs(rows[i]!)
    expect(above, `row ${i} -> row ${i + 1}: lines break at the seam`).toEqual(below)
  }
}

it('keeps lines connected across an unrelated root commit', async () => {
  const rows = await renderGraph([
    ['m', ['r1', 'f']],
    ['f', ['r2']],
    ['r1', []],
    ['r2', []],
  ])
  expect(rows).toHaveLength(4)
  expectSeamContinuity(rows)
  // r2 must render on the column its line arrives at, not float at column 0.
  const r2Dot = rows[3]!.map(d => d).filter(d => d.startsWith('M'))
  const carriedX = topXs(rows[3]!)[0]!
  expect(bottomXs(rows[2]!)).toContain(carriedX)
  expect(r2Dot.some(d => pathEnd(d).y === 11 && pathEnd(d).x === carriedX)).toBe(true)
})

it('keeps seam continuity across random topologies (fuzz)', async () => {
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
  for (let seed = 1; seed <= 15; seed++) {
    const rand = mulberry32(seed)
    const n = 4 + Math.floor(rand() * 9)
    const list: Array<[string, string[]]> = []
    for (let i = 0; i < n; i++) {
      const older = n - 1 - i
      const parents: string[] = []
      if (older > 0 && rand() > 0.25) {
        const count = 1 + Math.floor(rand() * Math.min(3, older))
        const pool = Array.from({ length: older }, (_, k) => `c${i + 1 + k}`)
        for (let p = 0; p < count && pool.length > 0; p++) {
          parents.push(pool.splice(Math.floor(rand() * pool.length), 1)[0]!)
        }
      }
      list.push([`c${i}`, parents])
    }
    const rows = await renderGraph(list)
    expectSeamContinuity(rows)
  }
})

it('gaps a through-line where a merge-back diagonal crosses it', async () => {
  // Commit a's duplicate expectation (pushed by c) merges back into a's dot
  // from column 2, crossing b's pending lane at column 1 mid-row (y=5.5).
  // The pending vertical must open a 3px gap there instead of extending
  // through the crossing.
  const rows = await renderGraph([
    ['m', ['a', 'b']],
    ['c', ['a']],
    ['a', ['base']],
    ['b', ['base']],
    ['base', []],
  ])
  const aRow = rows[2]!
  expect(aRow).toContain('M 27.5 -2 V 0 L 5.5 11') // merge-back diagonal into a's dot
  expect(aRow).toContain('M 16.5 -2 V 2.5') // pending lane above the crossing
  expect(aRow).toContain('M 16.5 8.5 V 22') // pending lane below the crossing
  expect(aRow).not.toContain('M 16.5 -2 V 22') // no unbroken through-line
  expectSeamContinuity(rows)
})
