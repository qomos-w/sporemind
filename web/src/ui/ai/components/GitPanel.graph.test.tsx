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
  commits: [
    // Crafted block: duplicate expectations, adoption, lane shift, multi-convergence.
    mk('R0', ['R1']), mk('M1', ['M1a', 'B']), mk('M1a', ['D', 'B']), mk('S', ['S1']),
    mk('B', ['BP']), mk('D', ['BP']), mk('S1', ['BP']), mk('BP', ['R1']), mk('R1', []),
    // Classic block: merge fan-out + branch-point convergence.
    mk('M', ['K', 'T1']), mk('K', ['BP2']), mk('T1', ['T2']), mk('T2', ['BP2']), mk('BP2', ['O']), mk('O', []),
  ],
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

it('renders lane geometry: departures, convergences, shifts', async () => {
  const { GitPanel } = await import('./GitPanel')
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => { root.render(<GitPanel projectId="project-1" onOpenFile={vi.fn()} />) })
  const cells = host.querySelectorAll('.git-panel-commit-row .git-panel-graph-cell')
  const rows = [...cells].map(cell => [...cell.querySelectorAll('path')].map(p => p.getAttribute('d')))
  // Row geometry for the crafted + classic fixture, newest first. Lanes sit at
  // x = 5.5 + 11·col; dots at y=11; row boundaries at y=0/22. Each row owns
  // the full band of its lanes within [-2, 22]: pass-through/dead lanes span
  // the whole row (the -2 start overlaps the row above, sealing the seam
  // under fractional device-pixel rounding); departures leave the dot center
  // as diagonals onto the branch lane at the boundary below; convergences
  // arrive at the boundary and diagonal into the dot; shifted lanes diagonal
  // onto their new column at the dot line and continue to the boundary as a
  // tail vertical; the first-parent trunk runs from the dot down to the
  // boundary, so no line ever escapes far enough up to paint over the
  // previous row's node. A through-line pierced by a diagonal is split with
  // a 3px gap at the crossing so merges read as passing over instead of the
  // line extending through the junction.
  expect(rows).toEqual([
    ['M 5.5 11 V 22'], // R0: fresh tip, trunk below the dot
    ['M 5.5 -2 V 22', 'M 16.5 11 V 22', 'M 16.5 11 L 27.5 22'], // M1: pass + departure onto born lane 2
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 11', 'M 27.5 -2 V 13.5', 'M 27.5 19.5 V 22', 'M 16.5 11 V 22', 'M 16.5 11 L 38.5 22'], // M1a: pass lanes + gapped crossing + departure onto born lane 3
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 22', 'M 27.5 -2 V 22', 'M 38.5 -2 V 22', 'M 49.5 11 V 22'], // S: fresh tip at lane 4
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 22', 'M 27.5 -2 V 11', 'M 38.5 11 V 22', 'M 27.5 11 V 22', 'M 38.5 -2 V 0 L 27.5 11', 'M 49.5 -2 V 0 L 38.5 11'], // B: duplicate converges + lane shift (tail vertical + diagonal)
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 11', 'M 27.5 -2 V 22', 'M 38.5 -2 V 22', 'M 16.5 11 V 22'], // D
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 22', 'M 27.5 -2 V 22', 'M 38.5 -2 V 11', 'M 38.5 11 V 22'], // S1
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 11', 'M 16.5 11 V 22', 'M 27.5 -2 V 0 L 16.5 11', 'M 38.5 -2 V 0 L 16.5 11'], // BP: two lanes converge into the dot
    ['M 5.5 -2 V 11', 'M 16.5 -2 V 0 L 5.5 11'], // R1: root, last lane converges
    ['M 5.5 11 V 22', 'M 5.5 11 L 16.5 22'], // M: merge departure onto lane 1
    ['M 5.5 -2 V 11', 'M 16.5 -2 V 22', 'M 5.5 11 V 22'], // K
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 11', 'M 16.5 11 V 22'], // T1
    ['M 5.5 -2 V 22', 'M 16.5 -2 V 11', 'M 16.5 11 V 22'], // T2
    ['M 5.5 -2 V 11', 'M 5.5 11 V 22', 'M 16.5 -2 V 0 L 5.5 11'], // BP2: branch converges into the trunk dot
    ['M 5.5 -2 V 11'], // O: root
  ])
  root.unmount()
})
