import React, { useContext, useMemo } from 'react'
import type { Frame, UIFrame, TimelineConnectorMode, TextFrame, PlanFrame, ReasoningFrame, ToolFrame, ErrorFrame, ImageFrame, SourcesFrame, AttachmentsFrame, AskUserQuestionFrame, PermissionRequestFrame, CompactionFrame, UserInjectFrame, SkillUseFrame, MemorySaveFrame, GoalReviewFrame, GoalSubmitFrame, GoalCardSubmitFrame, SnapshotRef, FileChangeEntry } from '../../model/frame-types.ts'
import { TextBlock } from './TextBlock.tsx'
import { PlanBlock } from './PlanBlock.tsx'
import { ReasoningBlock } from './ReasoningBlock.tsx'
import { ToolCallBlock } from './ToolCallBlock.tsx'
import { SmoothStreamContext } from '../../context/SmoothStreamContext.ts'

import { SourcesBlock } from './SourcesBlock.tsx'
import { AttachmentsBlock } from './AttachmentsBlock.tsx'
import { ImageBlock } from './ImageBlock.tsx'
import { ErrorBlock } from './ErrorBlock.tsx'
import { UIFrameBlock } from './UIFrameBlock.tsx'
import { AskUserQuestionBlock } from './AskUserQuestionBlock.tsx'
import { PermissionRequestBlock } from './PermissionRequestBlock.tsx'
import { CompactNoticeBlock } from './CompactNoticeBlock.tsx'
import { UserInjectBlock } from './UserInjectBlock.tsx'
import { SkillUseBlock } from './SkillUseBlock.tsx'
import { MemorySaveBlock } from './MemorySaveBlock.tsx'
import { GoalReviewView } from './GoalReviewView.tsx'
import { GoalSubmitView } from './GoalSubmitView.tsx'
import { GoalCardSubmitView } from './GoalCardSubmitView.tsx'

interface FrameRendererProps {
  frame: Frame
  version: number
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
  agentActorId?: string
  /** Per-envelope streaming state. When false, completed frames mount at
   *  their terminal state without replaying the expansion/icon animation. */
  turnStreaming?: boolean
}

/** Field-wise equality guard for the hot-path tool-frame sub-objects.
 *
 *  Correctness rule for frameEqual: a false positive (reporting "changed"
 *  when nothing did) only costs one extra render, but a false negative
 *  freezes stale content on screen. Every comparator below therefore
 *  enumerates its known fields explicitly with === and falls back to
 *  JSON.stringify whenever the object carries a key outside the known set,
 *  so a future-added field can never be silently treated as unchanged. */
function guardedEqual<T extends object>(
  a: T | undefined,
  b: T | undefined,
  knownKeys: readonly string[],
  compareFields: (x: T, y: T) => boolean,
): boolean {
  if (a === b) return true
  // Presence mismatch (undefined vs object): the object may render extra
  // chrome, so treat as changed — the safe direction.
  if (a === undefined || b === undefined) return false
  const known = new Set(knownKeys)
  const onlyKnown = (o: object) => Object.keys(o).every(k => known.has(k))
  if (!onlyKnown(a) || !onlyKnown(b)) {
    return JSON.stringify(a) === JSON.stringify(b)
  }
  return compareFields(a, b)
}

const SNAPSHOT_REF_KEYS = ['id', 'path', 'offset', 'limit', 'size', 'startLine', 'numLines', 'totalLines'] as const

function snapshotRefEqual(a: SnapshotRef | undefined, b: SnapshotRef | undefined): boolean {
  return guardedEqual(a, b, SNAPSHOT_REF_KEYS,
    (x, y) => x.id === y.id && x.path === y.path && x.offset === y.offset &&
      x.limit === y.limit && x.size === y.size && x.startLine === y.startLine &&
      x.numLines === y.numLines && x.totalLines === y.totalLines)
}

const FILE_CHANGE_KEYS = ['filename', 'filepath', 'icon', 'additions', 'deletions', 'diffContent'] as const

function fileChangeEntryEqual(a: FileChangeEntry, b: FileChangeEntry): boolean {
  return guardedEqual(a, b, FILE_CHANGE_KEYS,
    (x, y) => x.filename === y.filename && x.filepath === y.filepath && x.icon === y.icon &&
      x.additions === y.additions && x.deletions === y.deletions && x.diffContent === y.diffContent)
}

function fileChangesEqual(a: FileChangeEntry[] | undefined, b: FileChangeEntry[] | undefined): boolean {
  if (a === b) return true
  if (a === undefined || b === undefined || a.length !== b.length) return false
  return a.every((x, i) => fileChangeEntryEqual(x, b[i]!))
}

const PROGRESS_KEYS = ['phase', 'searchCount', 'readCount', 'inputTokens', 'outputTokens', 'activeWork', 'summaryText'] as const

function activeWorkEqual(
  a: { Type: string; Target: string }[] | undefined,
  b: { Type: string; Target: string }[] | undefined,
): boolean {
  if (a === b) return true
  if (a === undefined || b === undefined || a.length !== b.length) return false
  return a.every((x, i) => x.Type === b[i]!.Type && x.Target === b[i]!.Target)
}

function progressEqual(a: ToolFrame['progress'], b: ToolFrame['progress']): boolean {
  return guardedEqual(a, b, PROGRESS_KEYS,
    (x, y) => x.phase === y.phase && x.searchCount === y.searchCount && x.readCount === y.readCount &&
      x.inputTokens === y.inputTokens && x.outputTokens === y.outputTokens && x.summaryText === y.summaryText &&
      activeWorkEqual(x.activeWork, y.activeWork))
}

const RUNNING_OUTPUT_KEYS = ['stdout', 'stderr', 'frozen'] as const

function runningOutputEqual(a: ToolFrame['runningOutput'], b: ToolFrame['runningOutput']): boolean {
  // stdout/stderr are append-only accumulation buffers, so string === is a
  // memcmp that usually resolves on the first differing byte — no stringify.
  return guardedEqual(a, b, RUNNING_OUTPUT_KEYS,
    (x, y) => x.stdout === y.stdout && x.stderr === y.stderr && x.frozen === y.frozen)
}

/** Deep-equal two frames based on their type-specific content fields.
 *  This lets React.memo skip re-rendering unchanged frames even when
 *  parent envelope objects are re-created by the reducer.
 *
 *  The 'tool' case compares content fields directly instead of JSON.stringify
 *  so the rAF flush over all tool frames stays cheap even when a shell frame
 *  carries a large accumulated runningOutput. */
export function frameEqual(a: Frame, b: Frame): boolean {
  if (a.id !== b.id) return false
  if (a.type !== b.type) return false
  if (a.status !== b.status) return false
  if (a.animation !== b.animation) return false

  switch (a.type) {
    case 'text':
      return (a as TextFrame).content === (b as TextFrame).content && (a as TextFrame).model === (b as TextFrame).model
    case 'plan': {
      const ap = a as PlanFrame
      const bp = b as PlanFrame
      return ap.content === bp.content &&
        ap.requestId === bp.requestId &&
        ap.approvalStatus === bp.approvalStatus &&
        ap.editable === bp.editable &&
        JSON.stringify(ap.tasks) === JSON.stringify(bp.tasks) &&
        JSON.stringify(ap.policy) === JSON.stringify(bp.policy)
    }
    case 'reasoning': {
      const ar = a as ReasoningFrame
      const br = b as ReasoningFrame
      return ar.content === br.content &&
        ar.durationSeconds === br.durationSeconds &&
        ar.inputTokens === br.inputTokens &&
        ar.outputTokens === br.outputTokens &&
        ar.model === br.model
    }
    case 'tool': {
      const at = a as ToolFrame
      const bt = b as ToolFrame
      if (at.toolName !== bt.toolName ||
        at.input !== bt.input ||
        at.output !== bt.output ||
        at.callableId !== bt.callableId ||
        at.targetService !== bt.targetService ||
        at.exitCode !== bt.exitCode ||
        at.durationSeconds !== bt.durationSeconds ||
        at.inputTokens !== bt.inputTokens ||
        at.outputTokens !== bt.outputTokens) return false
      return snapshotRefEqual(at.snapshotRef, bt.snapshotRef) &&
        fileChangesEqual(at.fileChanges, bt.fileChanges) &&
        progressEqual(at.progress, bt.progress) &&
        runningOutputEqual(at.runningOutput, bt.runningOutput)
    }
    case 'error': {
      const ae = a as ErrorFrame
      const be = b as ErrorFrame
      return ae.message === be.message && ae.code === be.code && ae.toolName === be.toolName
    }
    case 'image': {
      const ai = a as ImageFrame
      const bi = b as ImageFrame
      return ai.url === bi.url && ai.alt === bi.alt
    }
    case 'sources':
      return JSON.stringify((a as SourcesFrame).entries) === JSON.stringify((b as SourcesFrame).entries)
    case 'attachments':
      return JSON.stringify((a as AttachmentsFrame).files) === JSON.stringify((b as AttachmentsFrame).files)
    case 'ask_user': {
      const aa = a as AskUserQuestionFrame
      const ba = b as AskUserQuestionFrame
      return aa.requestId === ba.requestId &&
        JSON.stringify(aa.questions) === JSON.stringify(ba.questions) &&
        JSON.stringify(aa.answers) === JSON.stringify(ba.answers)
    }
    case 'permission_request': {
      const ap = a as PermissionRequestFrame
      const bp = b as PermissionRequestFrame
      return ap.requestId === bp.requestId &&
        JSON.stringify(ap.toolCalls) === JSON.stringify(bp.toolCalls) &&
        ap.reason === bp.reason &&
        ap.allowed === bp.allowed
    }
    case 'compaction': {
      const ac = a as CompactionFrame
      const bc = b as CompactionFrame
      return ac.status === bc.status &&
        ac.trigger === bc.trigger &&
        ac.beforeTokens === bc.beforeTokens &&
        ac.afterTokens === bc.afterTokens &&
        ac.contextWindowSize === bc.contextWindowSize &&
        ac.model === bc.model &&
        ac.error === bc.error &&
        JSON.stringify(ac.beforeLayout) === JSON.stringify(bc.beforeLayout) &&
        JSON.stringify(ac.afterLayout) === JSON.stringify(bc.afterLayout) &&
        JSON.stringify(ac.rounds) === JSON.stringify(bc.rounds)
    }
    case 'ui':
      return (a as UIFrame).html === (b as UIFrame).html
    case 'user_inject': {
      const ap = a as UserInjectFrame
      const bp = b as UserInjectFrame
      return ap.items.length === bp.items.length &&
        ap.items.every((m, i) => m.id === bp.items[i]!.id && m.text === bp.items[i]!.text && m.idx === bp.items[i]!.idx)
    }
    case 'skill_use': {
      const ap = a as SkillUseFrame
      const bp = b as SkillUseFrame
      return ap.skillName === bp.skillName &&
        ap.isError === bp.isError
    }
    case 'memory_save': {
      const ap = a as MemorySaveFrame
      const bp = b as MemorySaveFrame
      return ap.content === bp.content &&
        ap.layer === bp.layer &&
        ap.nodeId === bp.nodeId &&
        ap.isError === bp.isError
    }
    case 'goal_review': {
      const ap = a as GoalReviewFrame
      const bp = b as GoalReviewFrame
      return ap.condition === bp.condition &&
        ap.achieved === bp.achieved &&
        ap.reason === bp.reason &&
        ap.turnCount === bp.turnCount &&
        ap.maxTurns === bp.maxTurns &&
        ap.aborted === bp.aborted
    }
    case 'goal_submit': {
      const ap = a as GoalSubmitFrame
      const bp = b as GoalSubmitFrame
      return ap.condition === bp.condition &&
        ap.interpretedGoal === bp.interpretedGoal &&
        ap.requestId === bp.requestId &&
        ap.approvalStatus === bp.approvalStatus
    }
    case 'goal_card_submit': {
      const ap = a as GoalCardSubmitFrame
      const bp = b as GoalCardSubmitFrame
      return ap.cardId === bp.cardId &&
        ap.interpretedGoal === bp.interpretedGoal &&
        ap.requestId === bp.requestId &&
        ap.approvalStatus === bp.approvalStatus
    }
    default:
      return false
  }
}

function propsEqual(prev: FrameRendererProps, next: FrameRendererProps): boolean {
  return frameEqual(prev.frame, next.frame) &&
    prev.version === next.version &&
    prev.onSaveToCard === next.onSaveToCard &&
    prev.connectorMode === next.connectorMode &&
    prev.turnStreaming === next.turnStreaming
  // Note: callback refs are intentionally NOT compared.
  // Event handlers don't affect visual output; comparing them
  // would defeat React.memo because parents recreate closures.
}

export const FrameRenderer: React.FC<FrameRendererProps> = React.memo(({
  frame,
  version,
  connectorMode = 'none',
  onFrameSelect,
  onSaveToCard,
  agentActorId,
  turnStreaming,
}) => {
  // Override the session-level isStreaming with the per-envelope value so that
  // frames from completed turns (e.g. after switching agents) mount at their
  // terminal state without replaying the expansion/icon animation — even when
  // another agent's turn is still streaming at the session level.
  const parentCtx = useContext(SmoothStreamContext)
  const ctx = useMemo(() =>
    turnStreaming !== undefined
      ? { ...parentCtx, isStreaming: turnStreaming }
      : parentCtx,
    // parentCtx is stable unless the provider value changes; turnStreaming is
    // a primitive. Re-create only when one of them actually changes.
    [parentCtx, turnStreaming],
  )

  let content: React.ReactNode
  switch (frame.type) {
    case 'text':
      content = <TextBlock frame={frame} onFrameSelect={onFrameSelect} onSaveToCard={onSaveToCard} />
      break
    case 'plan':
      content = <PlanBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'reasoning':
      content = <ReasoningBlock frame={frame} connectorMode={connectorMode} turnStreaming={turnStreaming} />
      break
    case 'tool':
      content = <ToolCallBlock frame={frame} connectorMode={connectorMode} onFrameSelect={onFrameSelect} agentActorId={agentActorId} turnStreaming={turnStreaming} />
      break
    case 'sources':
      content = <SourcesBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'attachments':
      content = <AttachmentsBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'image':
      content = <ImageBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'error':
      content = <ErrorBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'ui':
      content = <UIFrameBlock frame={frame as UIFrame} version={version} connectorMode={connectorMode} />
      break
    case 'ask_user':
      content = <AskUserQuestionBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'permission_request':
      content = <PermissionRequestBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'compaction':
      content = <CompactNoticeBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'user_inject':
      content = <UserInjectBlock frame={frame} connectorMode={connectorMode} />
      break
    case 'skill_use':
      content = <SkillUseBlock frame={frame as SkillUseFrame} connectorMode={connectorMode} />
      break
    case 'memory_save':
      content = <MemorySaveBlock frame={frame as MemorySaveFrame} connectorMode={connectorMode} />
      break
    case 'goal_review':
      content = <GoalReviewView frame={frame as GoalReviewFrame} connectorMode={connectorMode} />
      break
    case 'goal_submit':
      content = <GoalSubmitView frame={frame as GoalSubmitFrame} connectorMode={connectorMode} />
      break
    case 'goal_card_submit':
      content = <GoalCardSubmitView frame={frame as GoalCardSubmitFrame} connectorMode={connectorMode} />
      break
    default:
      content = null
  }

  if (turnStreaming === undefined) return content
  return <SmoothStreamContext.Provider value={ctx}>{content}</SmoothStreamContext.Provider>
}, propsEqual)
