import * as workspace from '../../gen-clients/workspace/client'
import type {
  GitFileStatus,
  GitCommitInfo,
  GitBranchInfo,
  GitShowFile,
} from '../../gen-clients/system/types'
import type { ProjectSnapshot } from '../../domain/types'
import { client } from '../../application/generated-client'
import { mapProjectRef } from '../../application/project-adapter'

type Listener = () => void

/** Per-context git state snapshot. */
interface GitContextState {
  projectId: string
  worktreeId: string | null
  branch: string
  files: GitFileStatus[]
  commits: GitCommitInfo[]
  branches: GitBranchInfo[]
  // History log scope: 'all' = every ref (git log --all); else a branch name.
  logBranch: string
  // Current history depth: how many commits gitLog fetches. Grows as the
  // user scrolls to the bottom of the list (continuous scrolling).
  logLimit: number
  selectedCommit: GitCommitInfo | null
  selectedFile: GitFileStatus | null
  diff: string[]
  commitDiff: string[]
  commitFiles: GitShowFile[]
  loading: boolean
  error: string | null
}

function emptyContext(projectId: string, worktreeId: string | null): GitContextState {
  return {
    projectId,
    worktreeId,
    branch: '',
    files: [],
    commits: [],
    branches: [],
    logBranch: 'all',
    logLimit: 100,
    selectedCommit: null,
    selectedFile: null,
    diff: [],
    commitDiff: [],
    commitFiles: [],
    loading: false,
    error: null,
  }
}

function contextKey(projectId: string, worktreeId: string | null): string {
  return `${projectId}:${worktreeId ?? 'root'}`
}

export interface GitState {
  projects: ProjectSnapshot[]
  selectedProjectId: string
  projectId: string
  worktreeId: string | null
  branch: string
  files: GitFileStatus[]
  commits: GitCommitInfo[]
  branches: GitBranchInfo[]
  /** History log scope: 'all' = every ref (git log --all); else a branch name. */
  logBranch: string
  /** Current history page size; grows with continuous scrolling. */
  logLimit: number
  loadingMore: boolean
  selectedCommit: GitCommitInfo | null
  selectedFile: GitFileStatus | null
  diff: string[]
  commitDiff: string[]
  commitFiles: GitShowFile[]
  loading: boolean
  error: string | null
}

class GitStore {
  /** Global + active-context state. Consumers read from this single object. */
  state: GitState = {
    projects: [],
    selectedProjectId: '',
    projectId: '',
    worktreeId: null,
    branch: '',
    files: [],
    commits: [],
    branches: [],
    logBranch: 'all',
    logLimit: 100,
    loadingMore: false,
    selectedCommit: null,
    selectedFile: null,
    diff: [],
    commitDiff: [],
    commitFiles: [],
    loading: false,
    error: null,
  }

  /** Per-context state storage. */
  private contexts = new Map<string, GitContextState>()

  private globalListeners = new Set<Listener>()
  private projectListeners = new Map<string, Set<Listener>>()
  private _version = 0
  private _unsubMounts: (() => void) | null = null
  private _unsubReconnect: (() => void) | null = null
  private _unsubVisibility: (() => void) | null = null

  /** Monotonically increasing request counter for stale-response isolation. */
  private _requestSeq = 0

  getVersion = () => this._version

  private emit(projectId?: string) {
    this._version++
    for (const fn of this.globalListeners) fn()
    if (projectId) {
      const set = this.projectListeners.get(projectId)
      if (set) {
        for (const fn of set) fn()
      }
    }
  }

  subscribe(fn: Listener, projectId?: string): () => void {
    if (projectId) {
      let set = this.projectListeners.get(projectId)
      if (!set) {
        set = new Set()
        this.projectListeners.set(projectId, set)
      }
      set.add(fn)
      return () => { set!.delete(fn) }
    }
    this.globalListeners.add(fn)
    return () => { this.globalListeners.delete(fn) }
  }

  /** Apply the given context state onto the live `state` object. */
  private applyContext(ctx: GitContextState) {
    this.state.projectId = ctx.projectId
    this.state.worktreeId = ctx.worktreeId
    this.state.branch = ctx.branch
    this.state.files = ctx.files
    this.state.commits = ctx.commits
    this.state.branches = ctx.branches
    this.state.logBranch = ctx.logBranch
    this.state.logLimit = ctx.logLimit
    this.state.loadingMore = false
    this.state.selectedCommit = ctx.selectedCommit
    this.state.selectedFile = ctx.selectedFile
    this.state.diff = ctx.diff
    this.state.commitDiff = ctx.commitDiff
    this.state.commitFiles = ctx.commitFiles
    this.state.loading = ctx.loading
    this.state.error = ctx.error
  }

  /**
   * Switch the active git context to the given project+worktree.
   * When worktreeId is null/empty/"root", the project root is used.
   */
  setContext({ projectId, worktreeId }: { projectId: string; worktreeId: string | null | undefined }) {
    const wt = worktreeId && worktreeId !== 'root' ? worktreeId : null
    if (this.state.projectId === projectId && this.state.worktreeId === wt) return
    // Persist current context
    const curKey = this.activeContextKey()
    this.contexts.set(curKey, this.snapshotActiveContext())

    // Load or create new context
    const newKey = contextKey(projectId, wt)
    let ctx = this.contexts.get(newKey)
    if (!ctx) {
      ctx = emptyContext(projectId, wt)
      this.contexts.set(newKey, ctx)
    }
    this.applyContext(ctx)
    this.state.selectedProjectId = projectId
    this.emit(projectId)
    if (projectId) {
      void this.refresh()
    }
  }

  /** Key for the currently active context. */
  private activeContextKey(): string {
    return contextKey(this.state.projectId, this.state.worktreeId)
  }

  /** Snapshot the current active context for persistence. */
  private snapshotActiveContext(): GitContextState {
    const s = this.state
    return {
      projectId: s.projectId,
      worktreeId: s.worktreeId,
      branch: s.branch,
      files: s.files,
      commits: s.commits,
      branches: s.branches,
      logBranch: s.logBranch,
      logLimit: s.logLimit,
      selectedCommit: s.selectedCommit,
      selectedFile: s.selectedFile,
      diff: s.diff,
      commitDiff: s.commitDiff,
      commitFiles: s.commitFiles,
      loading: s.loading,
      error: s.error,
    }
  }

  async loadProjects() {
    try {
      const resp = await workspace.listProject(client)
      const projects = (resp.Items ?? []).map(mapProjectRef)
      this.state.projects = projects
      // Auto-select first project if none selected
      if (!this.state.selectedProjectId && projects.length > 0) {
        const open = projects.find(p => p.IsOpen)
        this.setContext({ projectId: open?.ProjectID ?? projects[0]!.ProjectID, worktreeId: null })
        return
      }
      // Ensure selected project still exists
      if (this.state.selectedProjectId && !projects.some(p => p.ProjectID === this.state.selectedProjectId)) {
        const first = projects[0]
        if (first) {
          this.setContext({ projectId: first.ProjectID, worktreeId: null })
        }
        return
      }
      this.emit()
    } catch {
      this.emit()
    }
  }

  startMountListener() {
    if (this._unsubMounts) return
    this._unsubMounts = workspace.OnMounts(client, (event) => {
      const incoming = (event.Mounts ?? []).map(mapProjectRef)
      this.state.projects = incoming
      if (this.state.selectedProjectId && !incoming.some(p => p.ProjectID === this.state.selectedProjectId)) {
        const first = incoming[0]
        if (first) {
          this.setContext({ projectId: first.ProjectID, worktreeId: null })
        }
      } else if (!this.state.selectedProjectId && incoming.length > 0) {
        const open = incoming.find(p => p.IsOpen)
        this.setContext({ projectId: open?.ProjectID ?? incoming[0]!.ProjectID, worktreeId: null })
      } else {
        this.emit()
      }
    })

    const transport = client.getTransport() as any
    if (transport && typeof transport.onConnected === 'function') {
      this._unsubReconnect = transport.onConnected(({ isReconnect }: { isReconnect: boolean }) => {
        if (isReconnect) {
          void this.loadProjects()
        }
      })
    }

    const onVisibilityChange = () => {
      if (!document.hidden) {
        void this.loadProjects()
      }
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    this._unsubVisibility = () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }

  stopMountListener() {
    if (this._unsubMounts) {
      this._unsubMounts()
      this._unsubMounts = null
    }
    if (this._unsubReconnect) {
      this._unsubReconnect()
      this._unsubReconnect = null
    }
    if (this._unsubVisibility) {
      this._unsubVisibility()
      this._unsubVisibility = null
    }
  }

  /** Legacy: select a project by ID (no worktree → project root). */
  selectProject(projectId: string) {
    if (this.state.selectedProjectId === projectId) return
    this.setContext({ projectId, worktreeId: null })
  }

  /** Legacy direct-set (used by AIConversationPage/MultiConsoleContent when active project changes). */
  setProject(projectId: string, _projectPath: string) {
    if (this.state.projectId === projectId) return
    this.setContext({ projectId, worktreeId: null })
  }

  /** Switch the history log scope ('all' or a branch name) and refetch. */
  async setLogBranch(branch: string) {
    if (this.state.logBranch === branch) return
    this.state.logBranch = branch
    const ctx = this.contexts.get(this.activeContextKey())
    if (ctx) ctx.logBranch = branch
    this.emit(this.state.projectId)
    await this.refresh()
  }

  /** Continuous scrolling: deepen the log window and refetch just the log.
   * No-op when the whole history already fits in the current window. */
  async loadMoreHistory() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId || this.state.loading || this.state.loadingMore) return
    if (this.state.commits.length < this.state.logLimit) return
    const key = this.activeContextKey()
    const seq = ++this._requestSeq
    const limit = this.state.logLimit + 100
    this.state.loadingMore = true
    this.emit(projectId)
    try {
      const log = await workspace.gitLog(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        Limit: limit,
        All: this.state.logBranch === 'all',
        Branch: this.state.logBranch !== 'all' ? this.state.logBranch : undefined,
      })
      if (this.activeContextKey() !== key || seq !== this._requestSeq) return
      this.state.logLimit = limit
      this.state.commits = log.Commits ?? []
      const ctx = this.contexts.get(key)
      if (ctx) {
        ctx.logLimit = limit
        ctx.commits = this.state.commits
      }
    } catch (e) {
      if (this.activeContextKey() !== key || seq !== this._requestSeq) return
      this.state.error = String(e)
    } finally {
      if (this.activeContextKey() === key && seq === this._requestSeq) {
        this.state.loadingMore = false
      }
      this.emit(projectId)
    }
  }

  async refresh() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    const key = this.activeContextKey()
    const seq = ++this._requestSeq
    this.state.loading = true
    this.state.error = null
    this.emit(projectId)
    try {
      const [status, log, branches] = await Promise.all([
        workspace.gitStatus(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined }),
        workspace.gitLog(client, {
          ProjectId: projectId,
          WorktreeID: worktreeId ?? undefined,
          Limit: this.state.logLimit,
          All: this.state.logBranch === 'all',
          Branch: this.state.logBranch !== 'all' ? this.state.logBranch : undefined,
        }),
        workspace.gitBranch(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined }),
      ])
      // Stale-response guard: only apply if context + seq still match
      if (this.activeContextKey() !== key || seq !== this._requestSeq) return
      this.state.branch = status.Branch
      this.state.files = status.Files ?? []
      this.state.commits = log.Commits ?? []
      this.state.branches = branches.Branches ?? []
      // Persist to context map
      const ctx = this.contexts.get(key)
      if (ctx) {
        ctx.branch = this.state.branch
        ctx.files = this.state.files
        ctx.commits = this.state.commits
        ctx.logLimit = this.state.logLimit
        ctx.branches = this.state.branches
        ctx.commitFiles = this.state.commitFiles
      }
    } catch (e) {
      if (this.activeContextKey() !== key || seq !== this._requestSeq) return
      this.state.error = String(e)
    } finally {
      if (this.activeContextKey() === key && seq === this._requestSeq) {
        this.state.loading = false
      }
      this.emit(projectId)
    }
  }

  async selectFile(file: GitFileStatus | null): Promise<string[]> {
    this.state.selectedCommit = null
    this.state.selectedFile = file
    this.state.diff = []
    this.state.commitDiff = []
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!file || !projectId) {
      this.emit(projectId)
      return []
    }
    try {
      const result = await workspace.gitDiff(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        FilePath: file.Path,
        CommitHash: '',
      })
      this.state.diff = result.Diff ?? []
    } catch {
      this.state.diff = []
    }
    this.emit(projectId)
    return this.state.diff
  }

  async loadCommitFileDiff(file: GitFileStatus | null): Promise<string[]> {
    this.state.selectedFile = file
    this.state.diff = []
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!file || !projectId) {
      this.emit(projectId)
      return []
    }
    try {
      const commitHash = this.state.selectedCommit?.Hash ?? ''
      const result = await workspace.gitDiff(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        FilePath: file.Path,
        CommitHash: commitHash,
      })
      this.state.diff = result.Diff ?? []
    } catch {
      this.state.diff = []
    }
    this.emit(projectId)
    return this.state.diff
  }

  async loadCommitFileDiffByPath(path: string): Promise<string[]> {
    this.state.selectedFile = this.state.files.find(f => f.Path === path) ?? { Path: path, Staging: ' ', Worktree: ' ' }
    this.state.diff = []
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) {
      this.emit(projectId)
      return []
    }
    try {
      const commitHash = this.state.selectedCommit?.Hash ?? ''
      const result = await workspace.gitDiff(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        FilePath: path,
        CommitHash: commitHash,
      })
      this.state.diff = result.Diff ?? []
    } catch {
      this.state.diff = []
    }
    this.emit(projectId)
    return this.state.diff
  }

  async selectCommit(commit: GitCommitInfo | null): Promise<string[]> {
    this.state.selectedCommit = commit
    this.state.selectedFile = null
    this.state.diff = []
    this.state.commitDiff = []
    this.state.commitFiles = []
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!commit || !projectId) {
      this.emit(projectId)
      return []
    }
    try {
      // Load commit changed files via gitShow
      const showResult = await workspace.gitShow(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        Ref: commit.Hash,
      })
      this.state.commitFiles = showResult.Files ?? []
      // Also load the full commit diff for the right panel
      const diffResult = await workspace.gitDiff(client, {
        ProjectId: projectId,
        WorktreeID: worktreeId ?? undefined,
        FilePath: '',
        CommitHash: commit.Hash,
      })
      this.state.diff = diffResult.Diff ?? []
      this.state.commitDiff = this.state.diff
    } catch {
      this.state.diff = []
      this.state.commitDiff = []
      this.state.commitFiles = []
    }
    this.emit(projectId)
    return this.state.commitDiff
  }

  async stageFile(path: string) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitAdd(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Paths: [path] })
    await this.refresh()
  }

  async stageAll() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitAdd(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Paths: ['.'] })
    await this.refresh()
  }

  async unstageFile(path: string) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitReset(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Paths: [path] })
    await this.refresh()
  }

  async unstageAll() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitReset(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Paths: [] })
    await this.refresh()
  }

  async checkout(branch: string, create = false) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    this.state.loading = true
    this.state.error = null
    this.emit(projectId)
    try {
      await workspace.gitCheckout(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Branch: branch, Create: create })
      await this.refresh()
    } catch (e) {
      this.state.error = String(e)
      this.state.loading = false
      this.emit(projectId)
    }
  }

  async stashSave(message?: string) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitStashSave(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Message: message ?? '' })
    await this.refresh()
  }

  async stashPop() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitStashPop(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Index: 0 })
    await this.refresh()
  }

  async stashList() {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return []
    const result = await workspace.gitStashList(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined })
    return result.Stashes ?? []
  }

  async stashDrop(index = 0) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    await workspace.gitStashDrop(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Index: index })
    await this.refresh()
  }

  async commit(message: string) {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId || !message.trim()) return
    await workspace.gitCommit(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Message: message })
    await this.refresh()
  }

  async push(remote = 'origin') {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    this.state.loading = true
    this.emit(projectId)
    try {
      await workspace.gitPush(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Remote: remote })
      await this.refresh()
    } catch (e) {
      if (this.state.projectId === projectId) {
        this.state.error = String(e)
        this.state.loading = false
      }
      this.emit(projectId)
    }
  }

  async pull(remote = 'origin') {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    this.state.loading = true
    this.emit(projectId)
    try {
      await workspace.gitPull(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Remote: remote })
      await this.refresh()
    } catch (e) {
      if (this.state.projectId === projectId) {
        this.state.error = String(e)
        this.state.loading = false
      }
      this.emit(projectId)
    }
  }

  async fetch(remote = 'origin') {
    const projectId = this.state.projectId
    const worktreeId = this.state.worktreeId
    if (!projectId) return
    this.state.loading = true
    this.emit(projectId)
    try {
      await workspace.gitFetch(client, { ProjectId: projectId, WorktreeID: worktreeId ?? undefined, Remote: remote })
      await this.refresh()
    } catch (e) {
      if (this.state.projectId === projectId) {
        this.state.error = String(e)
        this.state.loading = false
      }
      this.emit(projectId)
    }
  }

  stagedFiles(): GitFileStatus[] {
    return this.state.files.filter(f => f.Staging !== ' ' && f.Staging !== '?')
  }

  unstagedFiles(): GitFileStatus[] {
    return this.state.files.filter(f => f.Worktree !== ' ' || f.Staging === '?')
  }
}

export const gitStore = new GitStore()