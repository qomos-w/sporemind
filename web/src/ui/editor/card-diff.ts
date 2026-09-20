/** Simple line-based diff for previewing card edits. */

export type DiffLineType = 'added' | 'removed' | 'unchanged'

export interface DiffLine {
  type: DiffLineType
  value: string
}

const LCS_THRESHOLD = 100

function lcsDiff<T>(a: T[], b: T[]): DiffLine[] {
  const m = a.length
  const n = b.length
  const dp: number[][] = Array.from({ length: m + 1 }, () => Array.from({ length: n + 1 }, () => 0))
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      if (a[i] === b[j]) {
        dp[i]![j]! = dp[i + 1]![j + 1]! + 1
      } else {
        dp[i]![j]! = Math.max(dp[i + 1]![j]!, dp[i]![j + 1]!)
      }
    }
  }

  const result: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < m || j < n) {
    if (i < m && j < n && a[i] === b[j]) {
      result.push({ type: 'unchanged', value: String(a[i]) })
      i++
      j++
    } else if (j >= n || (i < m && dp[i + 1]![j]! >= dp[i]![j + 1]!)) {
      result.push({ type: 'removed', value: String(a[i]) })
      i++
    } else {
      result.push({ type: 'added', value: String(b[j]) })
      j++
    }
  }
  return result
}

function diffMiddle(a: string[], b: string[]): DiffLine[] {
  if (a.length === 0) return b.map(value => ({ type: 'added', value }))
  if (b.length === 0) return a.map(value => ({ type: 'removed', value }))
  if (a.length > LCS_THRESHOLD || b.length > LCS_THRESHOLD) {
    return [
      ...a.map(value => ({ type: 'removed' as const, value })),
      ...b.map(value => ({ type: 'added' as const, value })),
    ]
  }
  return lcsDiff(a, b)
}

export function computeLineDiff(oldText: string, newText: string): DiffLine[] {
  const oldLines = oldText === '' ? [] : oldText.split('\n')
  const newLines = newText === '' ? [] : newText.split('\n')

  let prefix = 0
  while (prefix < oldLines.length && prefix < newLines.length && oldLines[prefix] === newLines[prefix]) {
    prefix++
  }

  let oldSuffix = oldLines.length
  let newSuffix = newLines.length
  while (oldSuffix > prefix && newSuffix > prefix && oldLines[oldSuffix - 1] === newLines[newSuffix - 1]) {
    oldSuffix--
    newSuffix--
  }

  const middle = diffMiddle(oldLines.slice(prefix, oldSuffix), newLines.slice(prefix, newSuffix))
  return [
    ...oldLines.slice(0, prefix).map(value => ({ type: 'unchanged' as const, value })),
    ...middle,
    ...oldLines.slice(oldSuffix).map(value => ({ type: 'unchanged' as const, value })),
  ]
}

export function computeUnifiedDiff(oldText: string, newText: string, filename = 'card'): string {
  const diff = computeLineDiff(oldText, newText)
  if (diff.every(d => d.type === 'unchanged')) {
    return `--- a/${filename}\n+++ b/${filename}\n@@ -1,${diff.length} +1,${diff.length} @@\n${diff.map(d => ` ${d.value}`).join('\n')}`
  }

  const lines: string[] = [`--- a/${filename}`, `+++ b/${filename}`]
  let index = 0
  while (index < diff.length) {
    if (diff[index]!.type === 'unchanged') {
      index++
      continue
    }

    const hunkStart = Math.max(0, index - 3)
    let hunkEnd = index
    while (hunkEnd < diff.length && (diff[hunkEnd]!.type !== 'unchanged' || hunkEnd - index < 3)) {
      hunkEnd++
    }
    hunkEnd = Math.min(diff.length, hunkEnd + 3)

    let oldStart = 0
    let newStart = 0
    for (let k = 0; k < hunkStart; k++) {
      if (diff[k]!.type !== 'added') oldStart++
      if (diff[k]!.type !== 'removed') newStart++
    }
    let oldCount = 0
    let newCount = 0
    for (let k = hunkStart; k < hunkEnd; k++) {
      if (diff[k]!.type !== 'added') oldCount++
      if (diff[k]!.type !== 'removed') newCount++
    }

    lines.push(`@@ -${oldStart + 1},${oldCount} +${newStart + 1},${newCount} @@`)
    for (let k = hunkStart; k < hunkEnd; k++) {
      const d = diff[k]!
      if (d.type === 'added') lines.push(`+${d.value}`)
      else if (d.type === 'removed') lines.push(`-${d.value}`)
      else lines.push(` ${d.value}`)
    }
    index = hunkEnd
  }
  return lines.join('\n')
}
