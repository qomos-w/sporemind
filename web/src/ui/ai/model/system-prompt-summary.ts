// ── System-continuation summary extraction ──
// Pure functions that turn a system-originated continuation message
// (Meta=goal / Meta=workflow user steps) into a short folded summary for
// the SystemContinuationBubble. Inputs are opaque English backend texts —
// never rely on them changing; regexes key off stable structural markers.

export type SystemPromptKind = 'goal' | 'workflow' | 'generic'

export interface SystemPromptSummary {
  kind: SystemPromptKind
  /** Single-line folded summary shown when the bubble is collapsed. */
  summary: string
}

const MAX_SUMMARY = 60

const TURN_SUFFIX_RE = /\(turn\s+\d+\s+of\s+\d+\)/i
const GOAL_CONTINUE_RE = /We'll continue working toward the active goal/i
const FRONTIER_RE = /has\s+(\d+)\s+frontier tickets?\s+ready/i
const STATUS_UPDATE_RE = /status update\s*\(\s*(\d+)\s*items?\s*\)/i
const NUDGE_PREFIX = "We'll continue:"
const TREE_EXHAUSTED_RE = /No frontier tasks or active workers remain\.?/i

/** Collapse whitespace and trim to ~MAX_SUMMARY chars, appending an
 *  ellipsis when truncated. */
export function truncate(text: string, max = MAX_SUMMARY): string {
  const collapsed = (text ?? '').replace(/\s+/g, ' ').trim()
  if (collapsed.length <= max) return collapsed
  return collapsed.slice(0, max).trimEnd() + '…'
}

/** Extract the folded one-liner for workflow-originated messages. */
function workflowSummary(
  source: string,
  frontier: RegExpMatchArray | null,
  statusUpdate: RegExpMatchArray | null,
): string {
  if (frontier) return `${frontier[1]} frontier tickets ready`
  // statusUpdate[0] preserves the original singular/plural form, e.g. "(1 item)".
  if (statusUpdate) return `Workflow ${statusUpdate[0]}`
  const treeExhausted = source.match(TREE_EXHAUSTED_RE)
  if (treeExhausted) return treeExhausted[0]
  // nudge (or any unmatched workflow text): drop the random trailing
  // parenthetical closer, then truncate.
  const stripped = source.replace(/\s*\([^)]*\)\s*$/, '')
  return truncate(stripped)
}

/** Folded summary for goal messages: keeps the "(turn N of M)" suffix —
 *  the single most important piece — intact instead of cutting at 60. */
function goalSummary(source: string, turnSuffix: RegExpMatchArray | null): string {
  if (turnSuffix && turnSuffix.index !== undefined) {
    const head = source.slice(0, turnSuffix.index + turnSuffix[0].length)
    return truncate(head, 120)
  }
  return truncate(source)
}

/** Classify a system-continuation message and produce its folded summary.
 *  `meta` wins when it is 'goal' / 'workflow'; otherwise the kind is
 *  inferred from the text markers. Unmatched text degrades to a generic
 *  ~60-char truncation. */
export function summarizeSystemPrompt(meta: string | undefined, text: string): SystemPromptSummary {
  const source = (text ?? '').trim()
  if (!source) return { kind: 'generic', summary: '' }

  const turnSuffix = source.match(TURN_SUFFIX_RE)
  const goalText = GOAL_CONTINUE_RE.test(source)
  const frontier = source.match(FRONTIER_RE)
  const statusUpdate = source.match(STATUS_UPDATE_RE)
  const nudge = source.startsWith(NUDGE_PREFIX)
  const treeExhausted = TREE_EXHAUSTED_RE.test(source)

  let kind: SystemPromptKind
  if (meta === 'goal') kind = 'goal'
  else if (meta === 'workflow') kind = 'workflow'
  else if (turnSuffix || goalText) kind = 'goal'
  else if (frontier || statusUpdate || nudge || treeExhausted) kind = 'workflow'
  else kind = 'generic'

  const summary = kind === 'workflow'
    ? workflowSummary(source, frontier, statusUpdate)
    : kind === 'goal'
      ? goalSummary(source, turnSuffix)
      : truncate(source)
  return { kind, summary }
}