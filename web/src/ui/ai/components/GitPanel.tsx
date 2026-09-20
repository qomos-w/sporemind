import React, { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Check, ChevronDown, CloudDownload, CloudUpload, Download, GitBranch, GitCommit, Plus, RefreshCw, Sparkles } from 'lucide-react'
import { gitStore } from '../../panels/git-store'
import type { GitCommitInfo, GitFileStatus, GitShowFile } from '../../../gen-clients/system/types'
import * as workspace from '../../../gen-clients/workspace/client'
import { completeMessage } from '../../../gen-clients/local/client'
import { buildCommitPrompt } from './commitPrompt'
import { client } from '../../../application/generated-client'
import type { ProviderOption } from './AIComposer'
import { ProviderIcon, iconKeyForModelRow } from './providerIcons'
import { normalizePath } from './parts/tool-display'
import { useI18n } from '../../../i18n'
import { computeGitGraph, GRAPH_COLORS, type GitGraphRow } from '../../../lib/gitGraph'
import './GitPanel.css'


interface GitPanelProps {
  projectId: string | null | undefined
  /** Opens a file or diff in the shell's right-panel editor. */
  onOpenFile?: (filePath: string, diffContent?: string, line?: number, lineEnd?: number, mode?: 'source' | 'diff', tabId?: string) => void
  /** Available AI providers (system pool) for commit message generation. */
  providers?: ProviderOption[]
  /** Active provider unit id from the shell context, used as the default. */
  activeProviderId?: string
  /** Active agent ActorId for AI commit message generation, or null if none. */
  agentActorId?: string | null
  /** Active agent display name. */
  agentName?: string
}

function statusColor(ch: string): string {
  if (ch === 'M') return 'var(--git-modified, #e2c08d)'
  if (ch === 'A') return 'var(--git-added, #81b88b)'
  if (ch === 'D') return 'var(--git-deleted, #c74e39)'
  if (ch === 'U') return 'var(--git-untracked, #73c991)'
  return 'var(--text-secondary)'
}

function fileVisibleStatus(f: GitFileStatus): string {
  const s = f.Staging !== ' ' ? f.Staging : f.Worktree
  return s === '?' ? 'U' : s
}

function isStaged(f: GitFileStatus): boolean {
  return f.Staging !== ' ' && f.Staging !== '?'
}

function pathDisplay(path: string): string {
  return normalizePath(path) ?? path
}

/** History table columns. Dragging a header divider resizes the column on its
 * left; the message column flexes to fill the remaining row width. */
const HISTORY_COLS = [
  { id: 'graph', label: '', width: 76 },
  { id: 'hash', label: '', width: 64 },
  { id: 'msg', label: 'gitPanel.colMessage', width: 240 },
  { id: 'author', label: 'gitPanel.colAuthor', width: 100 },
  { id: 'date', label: 'gitPanel.colDate', width: 120 },
] as const

type HistoryColId = (typeof HISTORY_COLS)[number]['id']

const HISTORY_COL_MIN = 24
const HISTORY_COL_MAX = 600

export function clampHistoryColWidth(width: number): number {
  return Math.min(HISTORY_COL_MAX, Math.max(HISTORY_COL_MIN, Math.round(width)))
}

function historyColStyle(id: HistoryColId, widths: Record<HistoryColId, number>, msgManual: boolean): React.CSSProperties {
  if (id === 'msg') {
    // Auto mode: msg flexes to fill leftover width, which re-absorbs every
    // column resize (boundaries right of it never move). Once the user drags
    // any divider, msg is pinned to its rendered width so the dragged
    // boundary follows the cursor; overflow scrolls horizontally instead.
    return msgManual
      ? { width: `${widths.msg}px`, flex: `0 0 ${widths.msg}px` }
      : { flexGrow: 1, flexShrink: 1, flexBasis: `${widths.msg}px`, minWidth: `${HISTORY_COL_MIN}px` }
  }
  return { width: `${widths[id]}px`, flex: `0 0 ${widths[id]}px` }
}

function defaultHistoryColWidths(): Record<HistoryColId, number> {
  return Object.fromEntries(HISTORY_COLS.map(c => [c.id, c.width])) as Record<HistoryColId, number>
}

/** History graph geometry, matching LiveAgent's 11px swimlanes and 22px rows. */
const LANE_WIDTH = 11
const ROW_HEIGHT = 22
const DOT_Y = LANE_WIDTH
const DOT_RADIUS = 4.5
const LANE_HALF = LANE_WIDTH / 2

function laneX(column: number): number {
  return LANE_WIDTH * (column + 1) - LANE_HALF
}

function columnCount(row: GitGraphRow): number {
  return Math.max(row.inputLanes.length, row.outputLanes.length, row.commitCol + 1, 1)
}

function graphWidth(row: GitGraphRow): number {
  return LANE_WIDTH * (columnCount(row) + 1)
}

function laneColor(color: number): string {
  return GRAPH_COLORS[color % GRAPH_COLORS.length]!
}

function graphVerticalPath(column: number, y1 = 0, y2 = ROW_HEIGHT): string {
  return `M ${laneX(column)} ${y1} V ${y2}`
}

/** Lines start 2px above the row boundary so consecutive rows overlap
 * instead of merely touching — the seam survives fractional device-pixel
 * rounding, and no line escapes far enough up to paint over the previous
 * row's node (the dot halo bottom sits 5.5px above the boundary). */
const SEAL_Y = -2

/** Vertical gap opened in a through-line where a diagonal crosses it, so
 * merges read as passing over instead of the line extending through the
 * junction. Round caps eat 1px on each side, so the visible gap is
 * CROSSING_GAP - 2. */
const CROSSING_GAP = 6

/** Column in row.outputLanes where dot-born lanes (appended parents) start. */
function appendedStart(row: GitGraphRow): number {
  const firstParentInSlot = row.commitCol < row.inputLanes.length
  return row.outputLanes.length - (row.parents.length - 1) - (firstParentInSlot ? 0 : 1)
}

function GitGraphCell({ row, wtBridge }: { row: GitGraphRow; wtBridge?: boolean }): React.ReactElement {
  const width = graphWidth(row)
  const commitColor = laneColor(row.commitColor)
  const dotX = laneX(row.commitCol)
  const bornStart = appendedStart(row)

  // Claim output lanes left to right so duplicate expectations resolve to distinct columns.
  const claimed = new Array<boolean>(row.outputLanes.length).fill(false)
  const takeOutput = (id: string): number => {
    const i = row.outputLanes.findIndex((l, j) => !claimed[j] && l.id === id)
    if (i >= 0) claimed[i] = true
    return i
  }

  // Collect the row's ink as vertical segments and diagonals first, so a
  // vertical pierced by a merging/shifting/fan-out diagonal can be split
  // with a gap at the crossing: the diagonal visually passes over and the
  // through-line no longer extends through the junction.
  interface Diagonal { key: string; d: string; stroke: string; x1: number; y1: number; x2: number; y2: number }
  interface Vertical { key: string; x: number; y1: number; y2: number; stroke: string; cls?: string }
  const diagonals: Diagonal[] = []
  const verticals: Vertical[] = []

  row.inputLanes.forEach((lane, index) => {
    const x = laneX(index)
    const stroke = laneColor(lane.color)
    if (lane.id === row.sha) {
      if (index === row.commitCol) {
        verticals.push({ key: `lane-${index}-${lane.id}`, x, y1: SEAL_Y, y2: DOT_Y, stroke })
      } else {
        diagonals.push({ key: `lane-${index}-${lane.id}`, d: `M ${x} ${SEAL_Y} V 0 L ${dotX} ${DOT_Y}`, stroke, x1: x, y1: 0, x2: dotX, y2: DOT_Y })
      }
      return
    }
    const own = row.outputLanes[index]
    const outIdx = own && !claimed[index] && own.id === lane.id ? (claimed[index] = true, index) : takeOutput(lane.id)
    if (outIdx < 0 || outIdx === index) {
      verticals.push({ key: `lane-${index}-${lane.id}`, x, y1: SEAL_Y, y2: ROW_HEIGHT, stroke })
      return
    }
    // A lane converges into this dot only when its target commit is a
    // parent of this row; a lane that merely compacted into the commit
    // column (root commit terminating its slot) must pass through.
    if (outIdx === row.commitCol && row.parents.includes(lane.id)) {
      diagonals.push({ key: `lane-${index}-${lane.id}`, d: `M ${x} ${SEAL_Y} V 0 L ${dotX} ${DOT_Y}`, stroke, x1: x, y1: 0, x2: dotX, y2: DOT_Y })
      return
    }
    diagonals.push({ key: `lane-${index}-${lane.id}`, d: `M ${x} ${SEAL_Y} V 0 L ${laneX(outIdx)} ${DOT_Y}`, stroke, x1: x, y1: 0, x2: laneX(outIdx), y2: DOT_Y })
    // The below-dot continuation of a shifted lane is a vertical segment in
    // its own right: fan-out diagonals from this dot may cross it.
    verticals.push({ key: `lane-${index}-${lane.id}-tail`, x: laneX(outIdx), y1: DOT_Y, y2: ROW_HEIGHT, stroke })
  })

  if (wtBridge && !row.inputLanes.some(lane => lane.id === row.sha)) {
    verticals.push({ key: 'wt-bridge', x: dotX, y1: SEAL_Y, y2: DOT_Y, stroke: '', cls: 'git-panel-graph-wt-line' })
  }

  // First-parent trunk below the dot — outputLanes[commitCol] is the first
  // parent whenever the commit has parents. Drawn so the dot (painted last)
  // terminates the line at its halo edge.
  if (row.parents.length > 0) {
    verticals.push({ key: 'trunk', x: dotX, y1: DOT_Y, y2: ROW_HEIGHT, stroke: commitColor })
  }

  // Dot-born parents (merge fan-out) leave the node center as a single
  // diagonal down onto the branch lane at the row boundary.
  row.outputLanes.forEach((lane, index) => {
    if (index < bornStart || claimed[index]) return
    const x = laneX(index)
    if (x === dotX) return
    diagonals.push({ key: `born-${index}-${lane.id}`, d: `M ${dotX} ${DOT_Y} L ${x} ${ROW_HEIGHT}`, stroke: laneColor(lane.color), x1: dotX, y1: DOT_Y, x2: x, y2: ROW_HEIGHT })
  })
  // Compaction survivals: lanes the id-claiming above never assigned
  // (duplicate expectations collapsed, or an extra first-parent
  // expectation) still own their column below the dot — without this
  // the line dies at a merge/root row and the seam below breaks. The
  // commit column with parents is already carried by the trunk.
  row.outputLanes.forEach((lane, index) => {
    if (index >= bornStart || claimed[index]) return
    if (index === row.commitCol && row.parents.length > 0) return
    verticals.push({ key: `carry-${index}-${lane.id}`, x: laneX(index), y1: DOT_Y, y2: ROW_HEIGHT, stroke: laneColor(lane.color) })
  })

  // Y coordinates where a diagonal pierces the column of a vertical
  // segment. Crossings whose gap would reach within 0.7px of a segment end
  // are ignored — the boundary-reaching stub must survive so seam continuity
  // (lines touch y=-2 and y=22) always holds.
  const crossingsAt = (x: number, y1: number, y2: number): number[] => {
    const margin = CROSSING_GAP / 2 + 0.7
    const ys: number[] = []
    for (const d of diagonals) {
      const lo = Math.min(d.x1, d.x2)
      const hi = Math.max(d.x1, d.x2)
      if (x <= lo || x >= hi) continue
      const y = d.y1 + (d.y2 - d.y1) * (Math.abs(x - d.x1) / Math.abs(d.x2 - d.x1))
      if (y > y1 + margin && y < y2 - margin) ys.push(y)
    }
    return ys.sort((a, b) => a - b)
  }
  // Split [y1, y2] into segments, removing CROSSING_GAP around each
  // crossing; adjacent gaps merge.
  const gapSegments = (y1: number, y2: number, ys: number[]): Array<[number, number]> => {
    const segs: Array<[number, number]> = []
    let cursor = y1
    for (const y of ys) {
      const g1 = Number((y - CROSSING_GAP / 2).toFixed(2))
      const g2 = Number((y + CROSSING_GAP / 2).toFixed(2))
      if (g1 - cursor > 0.5) segs.push([cursor, g1])
      cursor = Math.max(cursor, g2)
    }
    if (y2 - cursor > 0.5) segs.push([cursor, y2])
    return segs
  }

  return (
    <div className="git-panel-graph-cell" style={{ width, minWidth: width, height: ROW_HEIGHT }}>
      <svg width={width} height={ROW_HEIGHT} className="git-panel-graph" aria-hidden="true">
        {verticals.flatMap(v =>
          gapSegments(v.y1, v.y2, crossingsAt(v.x, v.y1, v.y2)).map(([a, b], i) => (
            <path key={`${v.key}-${i}`} d={`M ${v.x} ${a} V ${b}`} stroke={v.stroke || undefined} className={`git-panel-graph-line${v.cls ? ` ${v.cls}` : ''}`} />
          )),
        )}
        {diagonals.map(d => (
          <path key={d.key} d={d.d} stroke={d.stroke} className="git-panel-graph-line" />
        ))}
        <circle cx={dotX} cy={DOT_Y} r={DOT_RADIUS} fill={commitColor} className="git-panel-graph-node" />
      </svg>
    </div>
  )
}

function formatGitDate(unixTimestamp: string): string {
  const ts = parseInt(unixTimestamp, 10)
  if (isNaN(ts)) return unixTimestamp
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export const GitPanel: React.FC<GitPanelProps> = ({ projectId, onOpenFile, providers, activeProviderId, agentActorId, agentName }) => {
  const { t } = useI18n()
  useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)

  const [commitMsg, setCommitMsg] = useState('')
  const [branchOpen, setBranchOpen] = useState(false)
  // 'all' = show every branch badge in history (default); else a branch name.
  const [branchFilter, setBranchFilter] = useState('all')
  // Working-tree pseudo-node selection: true shows the staged/unstaged dual
  // pane below the history list; selecting a commit clears it.
  const [workingTreeSelected, setWorkingTreeSelected] = useState(true)
  const [colWidths, setColWidths] = useState<Record<HistoryColId, number>>(defaultHistoryColWidths)
  // True once the user drags any history divider: msg stops auto-flexing and
  // keeps the width it was pinned to (see historyColStyle).
  const [msgManual, setMsgManual] = useState(false)
  const historyHeadRef = useRef<HTMLDivElement>(null)
  // Vertical split ratio between the history list (top) and the detail pane (bottom).
  // Value is the detail pane's share of the available height (0–1).
  const [splitRatio, setSplitRatio] = useState(0.45)
  const historyViewRef = useRef<HTMLDivElement>(null)
  const historyListRef = useRef<HTMLDivElement>(null)
  const branchRef = useRef<HTMLDivElement>(null)
  const [providerOpen, setProviderOpen] = useState(false)
  const [selectedProviderId, setSelectedProviderId] = useState('')
  const [generating, setGenerating] = useState(false)
  const [genError, setGenError] = useState<string | null>(null)
  const providerRef = useRef<HTMLDivElement>(null)
  const applyBranchFilter = (filter: string) => {
    setBranchFilter(filter)
    void gitStore.setLogBranch(filter)
  }

  /** Drag a header divider: resize the column on the divider's left. */
  const startColResize = (col: HistoryColId) => (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    let startWidth = colWidths[col]
    if (!msgManual) {
      // Pin the flexible msg column to its current rendered width before
      // resizing, otherwise flex re-absorbs the change and the dragged
      // boundary never moves (dead / reversed handle).
      const actual = historyHeadRef.current
        ?.querySelector<HTMLElement>('.git-panel-col-msg')
        ?.getBoundingClientRect().width
      if (actual && Number.isFinite(actual) && actual > 0) {
        const pinned = clampHistoryColWidth(actual)
        setColWidths(prev => (prev.msg === pinned ? prev : { ...prev, msg: pinned }))
        setMsgManual(true)
        if (col === 'msg') startWidth = pinned
      }
    }
    const move = (ev: PointerEvent) => {
      const next = clampHistoryColWidth(startWidth + ev.clientX - startX)
      setColWidths(prev => (prev[col] === next ? prev : { ...prev, [col]: next }))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.classList.remove('git-panel-col-resizing')
    }
    document.body.classList.add('git-panel-col-resizing')
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  /** Drag the horizontal splitter: resize the detail pane's share. */
  const startSplitResize = (e: React.PointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    e.stopPropagation()
    const view = historyViewRef.current
    if (!view) return
    const startY = e.clientY
    const startRatio = splitRatio
    const viewHeight = view.getBoundingClientRect().height
    const move = (ev: PointerEvent) => {
      if (viewHeight <= 0) return
      const delta = ev.clientY - startY
      // Dragging down moves the divider down: the TOP pane grows, so the
      // bottom detail's share (splitRatio) shrinks.
      const next = Math.min(0.85, Math.max(0.15, startRatio - delta / viewHeight))
      setSplitRatio(prev => (Math.abs(prev - next) < 0.005 ? prev : next))
    }
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.classList.remove('git-panel-row-resizing')
    }
    document.body.classList.add('git-panel-row-resizing')
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  const state = gitStore.state
  const branch = state.branch
  const worktreeId = state.worktreeId
  const files = state.files
  const commits = state.commits
  const branches = state.branches
  const commitFiles = state.commitFiles
  const selectedFile = state.selectedFile
  const selectedCommit = state.selectedCommit
  const isLoading = state.loading

  // Keep the local filter in sync with the shared store scope (context switches).
  useEffect(() => setBranchFilter(state.logBranch), [state.logBranch])

  // Continuous scrolling: deepen history when the user nears the list bottom.
  // The handler reads the store directly so it never needs re-attaching.
  useEffect(() => {
    const el = historyListRef.current
    if (!el) return
    const onScroll = () => {
      if (el.scrollTop + el.clientHeight >= el.scrollHeight - 120) void gitStore.loadMoreHistory()
    }
    el.addEventListener('scroll', onScroll)
    return () => el.removeEventListener('scroll', onScroll)
  }, [])
  // A full window that fits without scrolling (or leaves no scrollbar) must
  // still top up, otherwise there is nothing left to scroll.
  useEffect(() => {
    const el = historyListRef.current
    if (el && el.scrollHeight <= el.clientHeight + 120) void gitStore.loadMoreHistory()
  }, [commits.length, isLoading])

  // Close branch dropdown on outside click
  useEffect(() => {
    if (!branchOpen) return
    const handler = (e: MouseEvent) => {
      if (branchRef.current && !branchRef.current.contains(e.target as Node)) {
        setBranchOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [branchOpen])

  const effectiveProviders = providers ?? []

  // Default the provider selection to the shell's active unit, then the first option.
  useEffect(() => {
    if (effectiveProviders.length === 0) {
      setSelectedProviderId('')
      return
    }
    if (selectedProviderId && effectiveProviders.some(p => p.id === selectedProviderId)) return
    if (activeProviderId && effectiveProviders.some(p => p.id === activeProviderId)) {
      setSelectedProviderId(activeProviderId)
      return
    }
    setSelectedProviderId(effectiveProviders[0]!.id)
  }, [effectiveProviders, activeProviderId, selectedProviderId])

  const selectedProvider = effectiveProviders.find(p => p.id === selectedProviderId) ?? null

  // Close provider dropdown on outside click
  useEffect(() => {
    if (!providerOpen) return
    const handler = (e: MouseEvent) => {
      if (providerRef.current && !providerRef.current.contains(e.target as Node)) {
        setProviderOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [providerOpen])

  // Lane layout for the History view (newest-first, same order as `commits`).
  const graphRows = useMemo(
    () => computeGitGraph(commits.map(c => ({ sha: c.Hash, parents: c.Parents ?? [] }))).rows,
    [commits],
  )

  const openSelectedDiff = async (file: GitFileStatus) => {
    const diff = await gitStore.selectFile(file)
    if (diff.length > 0) onOpenFile?.(file.Path, diff.join('\n'), undefined, undefined, 'diff', `git-diff-file-${projectId}-${file.Path}`)
  }

  const openCommitDiff = async (commit: GitCommitInfo) => {
    // Load commit metadata + changed files; the inline detail panel renders
    // the file list. The right-panel diff opens when a file is clicked.
    setWorkingTreeSelected(false)
    await gitStore.selectCommit(commit)
  }

  const openCommitFileDiff = async (file: GitShowFile) => {
    const diff = await gitStore.loadCommitFileDiffByPath(file.Path)
    if (diff.length > 0) {
      const path = file.OldPath ? `${file.OldPath} → ${file.Path}` : file.Path
      onOpenFile?.(path, diff.join('\n'), undefined, undefined, 'diff', `git-diff-commit-${projectId}-${selectedCommit?.Hash}-${file.Path}`)
    }
  }

  // Branch dropdown filter: 'all' shows every branch badge in history.
  const localBranches = branches.filter(b => !b.IsRemote)
  const remoteBranches = branches.filter(b => b.IsRemote).sort((a, b) => a.Remote.localeCompare(b.Remote))
  const remoteNames = [...new Set(remoteBranches.map(b => b.Remote))].sort()

  // Generate a commit message with a clean unary LLM call — no turn loop involvement.
  const handleGenerate = useCallback(async () => {
    if (!agentActorId || !projectId || generating) return
    setGenerating(true)
    setGenError(null)
    try {
      const staged = files.filter(isStaged)
      const changed = staged.length > 0 ? staged : files

      let diffs: { path: string; diff: string }[] = []
      try {
        const results = await Promise.all(
          changed.map(f =>
            workspace.gitDiff(client, { ProjectId: projectId, FilePath: f.Path, CommitHash: '' })
              .then(r => ({ path: f.Path, diff: (r.Diff ?? []).join('\n') }))
              .catch(() => null),
          ),
        )
        diffs = results.filter(Boolean) as { path: string; diff: string }[]
      } catch {
        // no diff text — the manifest alone still carries the signal
      }

      const recentCommits = commits.slice(0, 2).map(c => `  ${c.Short} ${c.Message}`)

      const prompt = buildCommitPrompt({
        files: changed.map(f => ({
          path: f.Path,
          status: f.Staging !== ' ' ? f.Staging : f.Worktree,
          diff: diffs.find(d => d.path === f.Path)?.diff ?? '',
        })),
        recentCommits,
      })

      const resp = await completeMessage(client, { UserText: prompt, System: '', Unit: selectedProvider?.unit }, { target: agentActorId })
      if (resp?.Text?.trim()) {
        setCommitMsg(resp.Text.trim())
      } else {
        setGenError(t('dialog.commit.aiEmpty'))
      }
    } catch (err) {
      setGenError(t('dialog.commit.aiFailed', { error: err instanceof Error ? err.message : String(err) }))
    } finally {
      setGenerating(false)
    }
  }, [agentActorId, projectId, generating, files, commits, selectedProvider?.unit, t])

  if (!projectId) {
    return (
      <div className="git-panel git-panel-empty">
        <GitBranch size={20} />
        <span>{t('gitPanel.noProject')}</span>
      </div>
    )
  }

  const stagedFiles = files.filter(isStaged)
  const stagedCount = stagedFiles.length
  const unstagedFiles = files.filter(f => f.Worktree !== ' ' || f.Staging === '?')
  const unstagedCount = unstagedFiles.length
  // No pending changes → no working-tree node in history.
  const wtVisible = stagedCount + unstagedCount > 0
  const wtSelected = wtVisible && workingTreeSelected

  /** A staged/unstaged pane row: status letter, path, stage/unstage action. */
  const renderWtFileRow = (f: GitFileStatus, staged: boolean) => {
    const ch = fileVisibleStatus(f)
    const active = selectedFile?.Path === f.Path
    return (
      <div
        key={`${staged ? 's' : 'u'}-${f.Path}`}
        className={`git-panel-file-row${active ? ' active' : ''}`}
        role="button"
        tabIndex={0}
        onClick={() => openSelectedDiff(f)}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') openSelectedDiff(f) }}
      >
        <span className="git-panel-file-status" style={{ color: statusColor(ch) }}>
          {staged ? <Check size={10} /> : ch}
        </span>
        <span className="git-panel-file-path">{pathDisplay(f.Path)}</span>
        <button
          type="button"
          className="git-panel-file-action"
          onClick={(e) => { e.stopPropagation(); void (staged ? gitStore.unstageFile(f.Path) : gitStore.stageFile(f.Path)) }}
        >
          {staged ? t('gitPanel.unstage') : t('gitPanel.stage')}
        </button>
      </div>
    )
  }

  return (
    <div className="git-panel">
      {/* Header: branch selector + WT badge + counts + refresh */}
      <div className="git-panel-header">
        <div className="git-panel-title" ref={branchRef}>
          <div
            className="git-panel-branch-selector"
            onClick={() => setBranchOpen(v => !v)}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setBranchOpen(v => !v) }}
          >
            <GitBranch size={14} />
            {/* The button reflects the history scope (All vs branch), not the
                checked-out branch; the current branch stays in the tooltip. */}
            <span className="git-panel-branch" title={branch || undefined}>
              {branchFilter === 'all' ? t('gitPanel.branchAll') : branchFilter}
            </span>
            <ChevronDown size={10} className="git-panel-branch-chevron" />
          </div>
          {worktreeId && <span className="git-panel-wt-badge">WT</span>}
          <span className="git-panel-counts">
            {stagedCount > 0 && <span className="git-panel-count-staged">{t('gitPanel.staged', { count: stagedCount })}</span>}
            {unstagedCount > 0 && <span className="git-panel-count-unstaged">{t('gitPanel.unstaged', { count: unstagedCount })}</span>}
            {stagedCount === 0 && unstagedCount === 0 && <span className="git-panel-count-clean">{t('gitPanel.clean')}</span>}
          </span>
          {/* Branch dropdown */}
          {branchOpen && (
            <div className="git-panel-branch-dropdown">
              <button
                type="button"
                className={`git-panel-branch-item${branchFilter === 'all' ? ' active' : ''}`}
                onClick={() => applyBranchFilter('all')}
              >
                <span className="git-panel-branch-item-name">{t('gitPanel.branchAll')}</span>
                {branchFilter === 'all' && <span className="git-panel-branch-item-check"><Check size={10} /></span>}
              </button>
              {localBranches.length > 0 && (
                <>
                  <div className="git-panel-branch-section">{t('gitPanel.branchLocal')}</div>
                  {localBranches.map(b => (
                    <button
                      key={b.Name}
                      type="button"
                      className={`git-panel-branch-item${branchFilter === b.Name ? ' active' : ''}`}
                      onClick={() => applyBranchFilter(b.Name)}
                    >
                      <span className="git-panel-branch-item-name">{b.Name}</span>
                      <span className="git-panel-branch-item-hash">{b.Hash}</span>
                      {branchFilter === b.Name && <span className="git-panel-branch-item-check"><Check size={10} /></span>}
                    </button>
                  ))}
                </>
              )}
              {remoteBranches.length > 0 && (
                <>
                  <div className="git-panel-branch-sep" />
                  <div className="git-panel-branch-section">{t('gitPanel.branchRemote')}</div>
                  {remoteNames.map(remote => (
                    <div key={remote}>
                      <div className="git-panel-branch-remote-label">{remote}/</div>
                      {remoteBranches.filter(b => b.Remote === remote).map(b => (
                        <button
                          key={b.Name}
                          type="button"
                          className={`git-panel-branch-item${branchFilter === b.Name ? ' active' : ''}`}
                          onClick={() => applyBranchFilter(b.Name)}
                        >
                          <span className="git-panel-branch-item-name">{b.Name.slice(remote.length + 1)}</span>
                          <span className="git-panel-branch-item-hash">{b.Hash}</span>
                          {branchFilter === b.Name && <span className="git-panel-branch-item-check"><Check size={10} /></span>}
                        </button>
                      ))}
                    </div>
                  ))}
                </>
              )}
            </div>
          )}
        </div>
        <div className="git-panel-header-actions">
          <button
            type="button"
            className="git-panel-icon-btn"
            title={t('gitPanel.refresh')}
            disabled={isLoading}
            onClick={() => void gitStore.refresh()}
          >
            <RefreshCw size={12} className={isLoading ? 'git-panel-spin' : ''} />
          </button>
        </div>
      </div>

      {state.error && <div className="git-panel-error">{state.error}</div>}

      {/* Quick menu: stage-all, commit, fetch, pull, push */}
      <div className="git-panel-quick-menu">
        <button
          type="button"
          className="git-panel-quick-btn"
          title={t('gitPanel.stageAll')}
          disabled={files.length === 0}
          onClick={() => void gitStore.stageAll()}
        >
          <Plus size={12} />
          <span>{t('gitPanel.stageAll')}</span>
        </button>
        <div className="git-panel-quick-sep" />
        <form
          className="git-panel-quick-commit"
          onSubmit={(e) => {
            e.preventDefault()
            const msg = commitMsg.trim()
            if (!msg) return
            void gitStore.commit(msg)
            setCommitMsg('')
          }}
        >
          {agentActorId && effectiveProviders.length > 0 && (
            <div className="git-panel-commit-provider" ref={providerRef}>
              <button
                type="button"
                className="git-panel-commit-provider-btn"
                onClick={() => setProviderOpen(v => !v)}
                disabled={generating}
                title={t('dialog.commit.selectProvider')}
              >
                {(() => {
                  const iconKey = selectedProvider ? iconKeyForModelRow(selectedProvider.label, selectedProvider.endpoint, selectedProvider.subtitle) : undefined
                  return iconKey ? <ProviderIcon id={iconKey} size={12} className="git-panel-commit-provider-btn-icon" /> : null
                })()}
                <span className="git-panel-commit-provider-label">{selectedProvider?.label ?? t('dialog.commit.select')}</span>
                <ChevronDown size={10} />
              </button>
              {providerOpen && (
                <div className="git-panel-commit-provider-dropdown">
                  {effectiveProviders.map(p => (
                    <button
                      key={p.id}
                      type="button"
                      className={`git-panel-commit-provider-item${p.id === selectedProviderId ? ' active' : ''}`}
                      onClick={() => { setSelectedProviderId(p.id); setProviderOpen(false) }}
                    >
                      {(() => {
                        const iconKey = iconKeyForModelRow(p.label, p.endpoint, p.subtitle)
                        return iconKey ? <ProviderIcon id={iconKey} size={12} className="git-panel-commit-provider-item-icon" /> : null
                      })()}
                      <span className="git-panel-commit-provider-name">{p.label}</span>
                      {p.subtitle && <span className="git-panel-commit-provider-sub">{p.subtitle}</span>}
                      {p.id === selectedProviderId && <Check size={10} />}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
          <input
            className="git-panel-quick-commit-input"
            value={commitMsg}
            onChange={(e) => setCommitMsg(e.target.value)}
            placeholder={genError ?? t('gitPanel.commitPlaceholder')}
            disabled={stagedCount === 0}
            title={genError ?? undefined}
          />
          {agentActorId && effectiveProviders.length > 0 && (
            <button
              type="button"
              className="git-panel-commit-gen-btn"
              onClick={() => void handleGenerate()}
              disabled={generating || (stagedCount === 0 && unstagedCount === 0)}
              title={t('dialog.commit.generateTitle', { name: agentName || 'AI' })}
            >
              <Sparkles size={12} className={generating ? 'git-panel-commit-gen-spin' : undefined} />
            </button>
          )}
          <button
            type="submit"
            className="git-panel-quick-commit-btn"
            disabled={stagedCount === 0 || !commitMsg.trim()}
            title={t('gitPanel.commit')}
          >
            <GitCommit size={12} />
          </button>
        </form>
        <div className="git-panel-quick-sep" />
        <button
          type="button"
          className="git-panel-quick-btn"
          title={t('gitPanel.fetch')}
          onClick={() => void gitStore.fetch()}
        >
          <Download size={12} />
          <span>{t('gitPanel.fetch')}</span>
        </button>
        <button
          type="button"
          className="git-panel-quick-btn"
          title={t('gitPanel.pull')}
          onClick={() => void gitStore.pull()}
        >
          <CloudDownload size={12} />
          <span>{t('gitPanel.pull')}</span>
        </button>
        <button
          type="button"
          className="git-panel-quick-btn"
          title={t('gitPanel.push')}
          onClick={() => void gitStore.push()}
        >
          <CloudUpload size={12} />
          <span>{t('gitPanel.push')}</span>
        </button>
      </div>

      {/* Body: unified history view (working-tree node + commits) */}
      <div className="git-panel-body">
        <div className="git-panel-history-view" ref={historyViewRef}>
            <div className="git-panel-list" ref={historyListRef}>
              <div className="git-panel-history-head" ref={historyHeadRef}>
                {HISTORY_COLS.map((col, i) => (
                  <div key={col.id} className={`git-panel-history-head-cell git-panel-col-${col.id}`} style={historyColStyle(col.id, colWidths, msgManual)}>
                    {col.label ? t(col.label) : ''}
                    {i < HISTORY_COLS.length - 1 && (
                      <div
                        className="git-panel-history-head-divider"
                        role="separator"
                        aria-orientation="vertical"
                        onPointerDown={startColResize(col.id)}
                      />
                    )}
                  </div>
                ))}
              </div>
              {/* Working-tree pseudo node: staged/unstaged changes on top of history (hidden when clean) */}
              {wtVisible && (
              <button
                type="button"
                className={`git-panel-commit-row git-panel-wt-row${msgManual ? ' git-panel-hscroll' : ''}${workingTreeSelected ? ' active' : ''}`}
                onClick={() => {
                  setWorkingTreeSelected(true)
                  void gitStore.selectCommit(null)
                }}
              >
                <div className="git-panel-cell git-panel-col-graph" style={historyColStyle('graph', colWidths, msgManual)}>
                  <div className="git-panel-graph-cell" style={{ width: LANE_WIDTH * 2, minWidth: LANE_WIDTH * 2, height: ROW_HEIGHT }}>
                    <svg width={LANE_WIDTH * 2} height={ROW_HEIGHT} className="git-panel-graph" aria-hidden="true">
                      <path d={graphVerticalPath(0, DOT_Y, ROW_HEIGHT)} className="git-panel-graph-line git-panel-graph-wt-line" />
                      <circle cx={laneX(0)} cy={DOT_Y} r={DOT_RADIUS} className="git-panel-graph-node git-panel-graph-wt-node" />
                    </svg>
                  </div>
                </div>
                <div className="git-panel-cell git-panel-col-hash" style={historyColStyle('hash', colWidths, msgManual)} />
                <div className="git-panel-cell git-panel-col-msg" style={historyColStyle('msg', colWidths, msgManual)}>
                  <span className="git-panel-commit-msg">{t('gitPanel.workingTree')}</span>
                  {stagedCount + unstagedCount > 0 && (
                    <span className="git-panel-wt-count">{stagedCount + unstagedCount}</span>
                  )}
                </div>
                <div className="git-panel-cell git-panel-col-author" style={historyColStyle('author', colWidths, msgManual)} />
                <div className="git-panel-cell git-panel-col-date" style={historyColStyle('date', colWidths, msgManual)} />
              </button>
              )}
              {commits.length === 0 && (
                <div className="git-panel-empty">{t('gitPanel.noCommits')}</div>
              )}
              {commits.map((c, idx) => {
                const row = graphRows[idx]!
                const active = selectedCommit?.Hash === c.Hash && !wtSelected
                // Branch Hash is short (7 chars); commit Hash is full-length.
                const refs = state.branches.filter(branchRef =>
                  c.Hash.startsWith(branchRef.Hash) && (branchFilter === 'all' || branchRef.Name === branchFilter))
                const badgeColor = laneColor(row.commitColor)
                return (
                  <button
                    key={c.Hash}
                    type="button"
                    className={`git-panel-commit-row${msgManual ? ' git-panel-hscroll' : ''}${active ? ' active' : ''}`}
                    onClick={() => openCommitDiff(c)}
                  >
                    <div className="git-panel-cell git-panel-col-graph" style={historyColStyle('graph', colWidths, msgManual)}>
                      <GitGraphCell
                        row={row}
                        wtBridge={idx === 0 && wtVisible}
                      />
                    </div>
                    <div className="git-panel-cell git-panel-col-hash" style={historyColStyle('hash', colWidths, msgManual)}>
                      <span className="git-panel-commit-short">{c.Short}</span>
                    </div>
                    <div className="git-panel-cell git-panel-col-msg" style={historyColStyle('msg', colWidths, msgManual)}>
                      {refs.map(ref => (
                        <span
                          key={ref.Name}
                          className={`git-panel-branch-ref${ref.Current ? ' current' : ''}`}
                          title={ref.Name}
                          style={{ borderColor: badgeColor }}
                        >
                          <span className="git-panel-branch-ref-dot" style={{ backgroundColor: badgeColor }} />
                          <span className="git-panel-branch-ref-name">{ref.Name}</span>
                          {ref.Current && <span className="git-panel-branch-ref-current" />}
                        </span>
                      ))}
                      <span className="git-panel-commit-msg">{c.Message}</span>
                    </div>
                    <div className="git-panel-cell git-panel-col-author" style={historyColStyle('author', colWidths, msgManual)} title={c.Author}>
                      {c.Author}
                    </div>
                    <div className="git-panel-cell git-panel-col-date" style={historyColStyle('date', colWidths, msgManual)}>
                      <span className="git-panel-commit-when">{formatGitDate(c.When)}</span>
                    </div>
                  </button>
                )
              })}
              {state.loadingMore && <div className="git-panel-history-more" aria-live="polite" />}
            </div>

            {(wtSelected || selectedCommit) && (
              <div
                className="git-panel-split-handle"
                role="separator"
                aria-orientation="horizontal"
                onPointerDown={startSplitResize}
              />
            )}

            {/* Working-tree detail: staged / unstaged dual pane */}
            {wtSelected && (
              <div className="git-panel-wt-detail" style={{ flex: `0 0 ${(splitRatio * 100).toFixed(1)}%` }}>
                <div className="git-panel-wt-pane">
                  <div className="git-panel-wt-pane-head">{t('gitPanel.staged', { count: stagedCount })}</div>
                  <div className="git-panel-wt-pane-files">
                    {stagedFiles.length === 0 && (
                      <div className="git-panel-wt-empty">{t('gitPanel.noChanges')}</div>
                    )}
                    {stagedFiles.map(f => renderWtFileRow(f, true))}
                  </div>
                </div>
                <div className="git-panel-wt-pane">
                  <div className="git-panel-wt-pane-head">{t('gitPanel.unstaged', { count: unstagedCount })}</div>
                  <div className="git-panel-wt-pane-files">
                    {unstagedFiles.length === 0 && (
                      <div className="git-panel-wt-empty">{t('gitPanel.noChanges')}</div>
                    )}
                    {unstagedFiles.map(f => renderWtFileRow(f, false))}
                  </div>
                </div>
              </div>
            )}

            {/* Commit detail: shown when a commit is selected (not when WT node is active) */}
            {selectedCommit && !wtSelected && (
              <div className="git-panel-commit-detail" style={{ flex: `0 0 ${(splitRatio * 100).toFixed(1)}%` }}>
                <div className="git-panel-commit-detail-header">
                  <div className="git-panel-commit-detail-hash">{selectedCommit.Short}</div>
                  <div className="git-panel-commit-detail-msg">{selectedCommit.Message}</div>
                </div>
                <div className="git-panel-commit-detail-meta">
                  <span className="git-panel-commit-detail-author">{selectedCommit.Author}</span>
                  <span className="git-panel-commit-detail-email">{selectedCommit.Email}</span>
                  <span className="git-panel-commit-detail-when">{formatGitDate(selectedCommit.When)}</span>
                </div>
                <div className="git-panel-commit-detail-files">
                  {commitFiles.length === 0 && (
                    <div className="git-panel-empty">{t('gitPanel.noDiff')}</div>
                  )}
                  {commitFiles.map(file => {
                    const statusChar = file.Status === 'added' ? 'A' : file.Status === 'deleted' ? 'D' : file.Status === 'modified' ? 'M' : file.Status === 'renamed' ? 'R' : file.Status === 'copied' ? 'C' : '?'
                    return (
                    <div
                      key={file.Path}
                      className={`git-panel-commit-detail-file git-panel-file-row${selectedFile?.Path === file.Path ? ' active' : ''}`}
                      role="button"
                      tabIndex={0}
                      onClick={() => void openCommitFileDiff(file)}
                      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') void openCommitFileDiff(file) }}
                    >
                      <span className="git-panel-file-status" style={{ color: statusColor(statusChar) }}>
                        {statusChar}
                      </span>
                      <span className="git-panel-file-path">{pathDisplay(file.OldPath ? `${file.OldPath} → ${file.Path}` : file.Path)}</span>
                    </div>
                    )
                  })}
                </div>
              </div>
            )}
          </div>
      </div>
    </div>
  )
}
