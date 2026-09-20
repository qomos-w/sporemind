/** Zero-dependency commit graph layout for newest-first git history. */

export const GRAPH_COLORS = ['#4c8dff', '#e5484d', '#30a46c', '#ffb224', '#b66dff', '#22c3e6', '#f76b15', '#e93d82'] as const

export type GraphColor = number

export interface GitGraphCommit {
  sha: string
  parents: readonly string[]
}

export interface GraphLane {
  id: string
  color: GraphColor
}

export interface GitGraphRow {
  sha: string
  parents: string[]
  commitCol: number
  commitColor: GraphColor
  inputLanes: GraphLane[]
  outputLanes: GraphLane[]
  isHead: boolean
  isMerge: boolean
}

export interface GitGraphLayout {
  rows: GitGraphRow[]
  maxCols: number
}

function cloneLane(lane: GraphLane): GraphLane {
  return { ...lane }
}

function uniqueParents(parents: readonly string[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const rawParent of parents) {
    const parent = rawParent.trim()
    if (!parent || seen.has(parent)) continue
    seen.add(parent)
    result.push(parent)
  }
  return result
}

/**
 * Produces input/output lane snapshots for every history row. Rendering both
 * snapshots preserves lane joins, crossings, and merge-parent branches.
 */
export function computeGitGraph(commits: readonly GitGraphCommit[]): GitGraphLayout {
  if (commits.length === 0) return { rows: [], maxCols: 0 }

  const rows: GitGraphRow[] = []
  let previousOutputLanes: GraphLane[] = []
  let nextColor = -1
  let maxCols = 1

  const allocColor = (): GraphColor => {
    nextColor = (nextColor + 1) % GRAPH_COLORS.length
    return nextColor
  }

  for (let index = 0; index < commits.length; index++) {
    const commit = commits[index]!
    const parents = uniqueParents(commit.parents)
    const inputLanes = previousOutputLanes.map(cloneLane)
    const inputIndex = inputLanes.findIndex(lane => lane.id === commit.sha)
    const commitCol = inputIndex >= 0 ? inputIndex : inputLanes.length
    const commitColor = inputIndex >= 0 ? inputLanes[inputIndex]!.color : allocColor()
    const outputLanes: GraphLane[] = []

    let firstParentAdded = false
    for (const lane of inputLanes) {
      if (lane.id === commit.sha) {
        // The commit's own slot terminates at this row; the first parent
        // inherits it. Parentless (root) commits simply drop their slot
        // while every other lane carries through untouched.
        if (parents.length > 0 && !firstParentAdded) {
          outputLanes.push({ id: parents[0]!, color: commitColor })
          firstParentAdded = true
        }
        continue
      }
      outputLanes.push(cloneLane(lane))
    }
    if (parents.length > 0) {
      if (!firstParentAdded) {
        outputLanes.push({ id: parents[0]!, color: commitColor })
      }
      for (const parent of parents.slice(1)) {
        outputLanes.push({ id: parent, color: allocColor() })
      }
    }

    maxCols = Math.max(maxCols, inputLanes.length, outputLanes.length, commitCol + 1)
    rows.push({
      sha: commit.sha,
      parents,
      commitCol,
      commitColor,
      inputLanes,
      outputLanes,
      isHead: index === 0,
      isMerge: parents.length > 1,
    })
    previousOutputLanes = outputLanes
  }

  return { rows, maxCols }
}
