import { loadPreference, savePreference } from '../../../application/theme-persist'
import type { EpicCategory } from './workflowLayout'

/**
 * Shared store for the workflow epic-tree fold preferences
 * (foldedWorkflows / foldedCategories / expandedBuckets).
 *
 * Formerly local state of TopologyModeView; hoisted so monoStore.load can
 * read the same fold state to build the server-side WorkflowFilter — under
 * plan C the global card list is filtered by fold visibility and other
 * consumers fetch missing cards on demand via monoStore.getCard.
 */

export interface WorkflowFoldSnapshot {
  /** True once the three persisted preferences have settled. */
  loaded: boolean
  foldedWorkflows: ReadonlySet<string>
  foldedCategories: ReadonlySet<EpicCategory>
  expandedBuckets: ReadonlySet<string>
}

type Listener = (snapshot: WorkflowFoldSnapshot) => void

const PREF_WORKFLOWS = 'workflowGraph.foldedWorkflows.v1'
const PREF_CATEGORIES = 'workflowGraph.foldedCategories.v1'
const PREF_BUCKETS = 'workflowGraph.archivedExpanded.v1'

function parseIdArray(raw: string | undefined): string[] {
  if (!raw) return []
  try {
    const arr = JSON.parse(raw) as unknown
    if (!Array.isArray(arr)) return []
    return arr.map(String).filter(Boolean)
  } catch {
    return []
  }
}

class WorkflowFoldStore {
  private snapshot: WorkflowFoldSnapshot = {
    loaded: false,
    foldedWorkflows: new Set(),
    foldedCategories: new Set(),
    expandedBuckets: new Set(),
  }
  private listeners = new Set<Listener>()
  private loadPromise: Promise<void> | null = null
  private versions = {
    workflows: { v: 0 },
    categories: { v: 0 },
    buckets: { v: 0 },
  }

  /** Load the persisted preferences once; concurrent callers share one load. */
  ensureLoaded(): Promise<void> {
    if (this.snapshot.loaded) return Promise.resolve()
    if (!this.loadPromise) {
      this.loadPromise = Promise.all([
        loadPreference(PREF_WORKFLOWS),
        loadPreference(PREF_CATEGORIES),
        loadPreference(PREF_BUCKETS),
      ]).then(([wf, cat, bucket]) => {
        this.snapshot = {
          loaded: true,
          foldedWorkflows: new Set(parseIdArray(wf)),
          foldedCategories: new Set(parseIdArray(cat) as EpicCategory[]),
          expandedBuckets: new Set(parseIdArray(bucket)),
        }
        this.emit()
      }).catch(() => {
        // Fold prefs unreadable: fall back to all-expanded defaults.
        this.snapshot = { ...this.snapshot, loaded: true }
        this.emit()
      })
    }
    return this.loadPromise
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener)
    listener(this.snapshot)
    return () => { this.listeners.delete(listener) }
  }

  /** Reset to the unloaded state (test isolation / project teardown). */
  reset() {
    this.loadPromise = null
    this.snapshot = {
      loaded: false,
      foldedWorkflows: new Set(),
      foldedCategories: new Set(),
      expandedBuckets: new Set(),
    }
    this.emit()
  }

  getSnapshot(): WorkflowFoldSnapshot {
    return this.snapshot
  }

  setFoldedWorkflows(next: ReadonlySet<string>) {
    this.snapshot = { ...this.snapshot, foldedWorkflows: new Set(next) }
    this.emit()
    void savePreference(PREF_WORKFLOWS, JSON.stringify([...next]), 'workflow-graph-expanded', this.versions.workflows).catch(() => {})
  }

  setFoldedCategories(next: ReadonlySet<EpicCategory>) {
    this.snapshot = { ...this.snapshot, foldedCategories: new Set(next) }
    this.emit()
    void savePreference(PREF_CATEGORIES, JSON.stringify([...next]), 'workflow-graph-folded-categories', this.versions.categories).catch(() => {})
  }

  setExpandedBuckets(next: ReadonlySet<string>) {
    this.snapshot = { ...this.snapshot, expandedBuckets: new Set(next) }
    this.emit()
    void savePreference(PREF_BUCKETS, JSON.stringify([...next]), 'workflow-graph-archived-buckets', this.versions.buckets).catch(() => {})
  }

  private emit() {
    for (const listener of this.listeners) {
      listener(this.snapshot)
    }
  }
}

export const workflowFoldStore = new WorkflowFoldStore()

/** Build the server-side WorkflowFilter from the current fold snapshot plus
 * live agent actor ids. Returns null until the fold preferences settle so a
 * cold open does not fetch an unfiltered full list first. */
export function buildWorkflowFilter(
  fold: WorkflowFoldSnapshot,
  agentActorIds: ReadonlySet<string>,
): import('../../../gen-clients/system/types').WikiWorkflowFilter | null {
  if (!fold.loaded) return null
  return {
    FoldedWorkflows: [...fold.foldedWorkflows],
    FoldedCategories: [...fold.foldedCategories],
    ExpandedBuckets: [...fold.expandedBuckets],
    AgentIds: [...agentActorIds],
  }
}
