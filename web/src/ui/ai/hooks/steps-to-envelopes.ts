import type { Step } from '../../../gen-types/aigen'
import { getExecutionProgressAcc, executionProgressAccSig } from './step-reducer'
import type { TurnEnvelope, Frame, ToolFrame, AskUserQuestion, AskUserQuestionFrame, UserInjectFrame, SkillUseFrame, GoalReviewFrame, GoalSubmitFrame, GoalCardSubmitFrame, TaskEntry, LocalInteractionResponse, PlanFrame, TextFrame, MemorySaveFrame } from '../model/frame-types'
import { parseJsonObject, firstString } from '../components/parts/tool-display'
import { inferToolNameFromInput, parseReadBase64Path, parseSnapshotRef, approxBase64ByteSize } from './tool-parsers'
import { isImageExt, mimeFromExt } from '../components/parts/image-utils'

/** Parse a plan approval decision from a step interaction_resolved payload.
 *  The backend stores the decision as a text block containing
 *  `{ decision: 'approve' | 'reject' | 'edit', answer: ... }`.
 */
function parsePlanDecision(content: { Type?: string; Text?: string }[]): 'approve' | 'reject' | 'edit' | undefined {
  for (const block of content) {
    if (block.Type !== 'text' || !block.Text) continue
    try {
      const parsed = JSON.parse(block.Text)
      const decision = parsed?.decision
      if (parsed?.answer != null && (decision === 'approve' || decision === 'reject' || decision === 'edit')) {
        return decision
      }
    } catch {
      // ignore
    }
  }
  return undefined
}

/**
 * Recover the user's answers for a RESOLVED ask_user interaction step from its
 * persisted content. The backend appends the answer payload on resolution:
 *  - live path:   {"answer":"{\"0\":\"yes\"}"}
 *  - restart path: {"0":"yes"} (content replaced wholesale)
 * Without this, a reloaded timeline renders answered questions as pending.
 */
function parseResolvedAskUserAnswers(content: { Type?: string; Text?: string }[]): Record<number, string> | undefined {
  for (let i = content.length - 1; i >= 0; i--) {
    const block = content[i]
    if (block?.Type !== 'text' || !block.Text) continue
    try {
      let parsed: any = JSON.parse(block.Text)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && typeof parsed.answer === 'string') {
        try { parsed = JSON.parse(parsed.answer) } catch { continue }
      }
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        const answers: Record<number, string> = {}
        let found = false
        for (const [k, v] of Object.entries(parsed)) {
          if (/^\d+$/.test(k)) { answers[Number(k)] = String(v); found = true }
        }
        if (found) return answers
      }
    } catch {
      // not the answer payload
    }
  }
  return undefined
}

/**
 * Recover the user's decision for a RESOLVED permission step from its persisted
 * content. Shapes (scanned newest-first):
 *  - live path:   {"allowed":true,"answer":"{...}"}
 *  - restart path: {"allowed":true,"allowInProject":false}
 */
function parseResolvedPermissionDecision(content: { Type?: string; Text?: string }[]): boolean | undefined {
  for (let i = content.length - 1; i >= 0; i--) {
    const block = content[i]
    if (block?.Type !== 'text' || !block.Text) continue
    try {
      const parsed = JSON.parse(block.Text)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && typeof parsed.allowed === 'boolean') {
        return parsed.allowed
      }
    } catch {
      // not the decision payload
    }
  }
  return undefined
}

/** Parse ask_user questions from the various shapes the backend and preview emit:
 *  - Direct array: [{header, question, options, multiSelect}]
 *  - Wrapped raw input string: { questions: '{"questions":[...]}' }
 *  - Wrapped array: { questions: [...] }
 *  - Legacy single object: { question: '...' }
 */
function parseAskUserQuestions(source: string): AskUserQuestion[] {
  if (!source) {
    console.warn('[parseAskUserQuestions] empty source')
    return []
  }
  let parsed: any
  try {
    parsed = JSON.parse(source)
  } catch (e) {
    console.warn('[parseAskUserQuestions] JSON.parse failed:', e, 'source:', source.slice(0, 200))
    return []
  }
  const normalizeOption = (o: any): { label: string; description?: string; recommended?: boolean } => {
    if (typeof o === 'string') return { label: o }
    return {
      label: String(o?.label ?? ''),
      description: o?.description ? String(o.description) : undefined,
      recommended: o?.recommended === true ? true : undefined,
    }
  }
  const normalizeQuestion = (q: any): AskUserQuestion => ({
    header: String(q?.header ?? ''),
    question: String(q?.question ?? ''),
    options: Array.isArray(q?.options) ? q.options.map(normalizeOption) : [],
    multiSelect: Boolean(q?.multiSelect),
  })
  if (Array.isArray(parsed)) {
    return parsed.map(normalizeQuestion)
  }
  if (parsed && typeof parsed === 'object') {
    // Backend wraps the raw ask_user tool input JSON in a "questions" field.
    const raw = parsed.questions
    if (typeof raw === 'string') {
      try {
        const inner = JSON.parse(raw)
        if (Array.isArray(inner)) return inner.map(normalizeQuestion)
        if (inner && typeof inner === 'object' && Array.isArray(inner.questions)) {
          return inner.questions.map(normalizeQuestion)
        }
      } catch { /* ignore */ }
    }
    if (Array.isArray(raw)) {
      return raw.map(normalizeQuestion)
    }
    // Legacy single-question object.
    if (parsed.question != null) {
      return [normalizeQuestion(parsed)]
    }
  }
  console.warn('[parseAskUserQuestions] no matching shape, parsed type:', typeof parsed, 'isArray:', Array.isArray(parsed), 'source:', source.slice(0, 200))
  return []
}

/** Sort steps by monotonic Seq when available, falling back to timestamp. */
function sortStepsBySeq(steps: Step[]): Step[] {
  return [...steps].sort((a, b) => {
    const hasA = a.Seq != null && a.Seq > 0 && !Number.isNaN(a.Seq)
    const hasB = b.Seq != null && b.Seq > 0 && !Number.isNaN(b.Seq)
    if (hasA && hasB) return a.Seq! - b.Seq!
    if (hasA) return 1
    if (hasB) return -1
    return (a.Timestamp || '').localeCompare(b.Timestamp || '')
  })
}

/** Convert a Step's ContentBlock array into Frame(s). */

function stepSignature(step: Step, localResponses?: ReadonlyMap<string, LocalInteractionResponse>): string {
  // The accumulator is not visible via step.Progress (Progress holds only the
  // latest delta), so fold its per-stream lengths in — otherwise a chunk whose
  // delta text is byte-identical to the previous one would leave the signature
  // unchanged and the projection cache would skip the append.
  const accSig = executionProgressAccSig(step)
  let sig = `${step.Seq ?? ''}|${step.Closed}|${step.Type}|${step.Error ?? ''}|${step.ReasoningContent ?? ''}|${step.InteractionStatus ?? ''}|${step.Progress ?? ''}${accSig ? `|pacc:${accSig}` : ''}`
  for (const b of step.Content ?? []) {
    sig += `\x00${b.Type}|${b.Text ?? ''}|${b.ToolUseId ?? ''}|${b.ToolName ?? ''}|${b.Input ?? ''}`
    // Include local interaction response state for ToolUseId-based requestIds (ask_user).
    const tid = b.ToolUseId
    if (tid && localResponses?.has(tid)) {
      const r = localResponses.get(tid)!
      sig += `|lr:${r.kind}:${JSON.stringify(r.answers ?? r.allowed ?? r.decision)}`
    }
  }
  // Include local interaction response for step.Id-based requestIds (permission, plan).
  if (localResponses?.has(step.Id)) {
    const r = localResponses.get(step.Id)!
    sig += `|lr_step:${r.kind}:${JSON.stringify(r.allowed ?? r.decision)}`
  }
  // Include local interaction response for step.RequestId-based requestIds
  // (plan approvals use a dedicated request ID separate from the step ID).
  if (step.RequestId && step.RequestId !== step.Id && localResponses?.has(step.RequestId)) {
    const r = localResponses.get(step.RequestId)!
    sig += `|lr_req:${r.kind}:${JSON.stringify(r.allowed ?? r.decision)}`
  }
  return sig
}

function stepContentToFrames(step: Step, localResponses?: ReadonlyMap<string, LocalInteractionResponse>, stepCache?: Map<string, { sig: string; frames: Frame[] }>): Frame[] {
  const sig = stepSignature(step, localResponses)
  const cached = stepCache?.get(step.Id)
  if (cached && cached.sig === sig) {
    return cached.frames
  }

  const status = step.Closed ? 'completed' as const : 'running' as const
  const contentBlocks = step.Content ?? []

  // Compaction blocks take precedence over the generic error override so that
  // compaction failures still render inside the compaction UI with status=error.
  if (step.Type === 'text') {
    const compactionBlocks = contentBlocks.filter(b => b.Type === 'compaction')
    const compactionBlock = compactionBlocks[compactionBlocks.length - 1]
    if (compactionBlock?.Text) {
      try {
        const data = JSON.parse(compactionBlock.Text)
        // The last block's own status is authoritative for the compaction's
        // outcome: the backend stamps "completed" on the final frame when the
        // run ends, so the frame flips without waiting for step.closed.
        const compactionStatus = data.error ? 'error' as const
          : data.status === 'completed' ? 'completed' as const
          : status
        const frames = [{
          id: step.Id,
          type: 'compaction' as const,
          status: compactionStatus,
          beforeTokens: data.beforeTokens ?? 0,
          afterTokens: data.afterTokens ?? 0,
          contextWindowSize: data.contextWindowSize ?? 0,
          trigger: data.trigger ?? 'user',
          beforeLayout: data.beforeLayout ?? [],
          afterLayout: data.afterLayout ?? [],
          rounds: data.rounds ?? [],
          model: data.model,
          error: data.error,
        }]
        stepCache?.set(step.Id, { sig, frames })
        return frames
      } catch { /* fall through to text */ }
    }
  }

  // Error overrides type-based rendering.
  if (step.Error || step.Type === 'error') {
    // When the failed step was a tool_call, name the failing call in the
    // error block title. Reuse the tool_use extraction from the tool path
    // below so the title matches what the tool frame would have shown.
    // llm_call / turn-level errors carry no tool_use block and stay unnamed,
    // falling back to the generic error title in ErrorBlock.
    let toolName: string | undefined
    if (step.Type === 'tool_call') {
      const toolUse = contentBlocks.find(b => b.Type === 'tool_use')
      if (toolUse) {
        toolName = inferToolNameFromInput(toolUse.Input, toolUse.ToolName ?? step.Type)
      }
    }
    const frames: Frame[] = [{ id: step.Id, type: 'error', status: 'error', message: step.Error || contentBlocks[0]?.Text || step.Type, toolName }]
    stepCache?.set(step.Id, { sig, frames })
    return frames
  }

  let frames: Frame[] = []
  switch (step.Type) {
    case 'turn_start': {
      const frames = [{ id: step.Id, type: 'text' as const, status: 'running' as const, content: '' }]
      stepCache?.set(step.Id, { sig, frames })
      return frames
    }
    case 'llm_call': {
      const text = contentBlocks.map(b => b.Text ?? '').join('')
      frames = [{ id: step.Id, type: 'text', status, content: text, model: step.Model }]
      break
    }
    case 'text': {
      const text = contentBlocks.map(b => b.Text ?? '').join('')
      // Legacy sessions persisted skill mounts as system-role text steps
      // carrying a <!-- loaded skill: xxx --> marker. Render them as a
      // skill_use frame so they appear inside the assistant turn.
      if (step.Role === 'system') {
        const skillMountMatch = text.match(/^<!--\s*loaded skill:\s*(\S+)\s*-->\n?(.*)$/s)
        if (skillMountMatch) {
          const [, skillName] = skillMountMatch
          frames = [{
            id: step.Id,
            type: 'skill_use',
            status,
            skillName,
            isError: false,
          } as SkillUseFrame]
          break
        }
      }
      frames = [{ id: step.Id, type: 'text', status, content: text, model: step.Model }]
      break
    }
    case 'reasoning': {
      const text = step.ReasoningContent ?? ''
      frames = [{ id: step.Id, type: 'reasoning', status, content: text, model: step.Model }]
      break
    }
    case 'tool_call': {
      const toolUse = contentBlocks.find(b => b.Type === 'tool_use')
      // Use last tool_result / memory_result block — resolveForkStep
      // appends the real child summary after processBatchResults emitted
      // a placeholder. memory.save results are emitted as memory_result
      // (kept out of the compiled LLM context but still shown in the UI).
      const toolResult = (() => {
        for (let i = contentBlocks.length - 1; i >= 0; i--) {
          if (contentBlocks[i]!.Type === 'tool_result' || contentBlocks[i]!.Type === 'memory_result') return contentBlocks[i]
        }
        return undefined
      })()
      const toolName = inferToolNameFromInput(toolUse?.Input, toolUse?.ToolName ?? step.Type)
      const input = toolUse?.Input ?? ''
      const output = toolResult?.Text ?? undefined

      // project.read_base64 → image frame
      if (toolName === 'project.read_base64') {
        const path = parseReadBase64Path(input)
        if (path) {
          if (isImageExt(path)) {
            frames = [{
              id: step.Id,
              type: 'image',
              status: 'completed',
              url: `data:${mimeFromExt(path)};base64,${output ?? ''}`,
              alt: path,
            }]
            break
          }
          frames = [{
            id: step.Id,
            type: 'tool',
            status,
            toolName,
            input,
            output: `<binary, ${approxBase64ByteSize(output ?? '')} bytes>`,
          }]
          break
        }
      }

      // computeruse.screenshot → image frame. The response carries base64
      // ImageBytes + Format; surface it as an image instead of raw JSON.
      // Responses without image bytes (e.g. Unchanged/Message-only) fall
      // through to the generic tool frame.
      if (toolName === 'computeruse.screenshot') {
        const parsed = output ? parseJsonObject(output) : null
        const imageBytes = parsed ? firstString(parsed, ['ImageBytes']) : undefined
        if (parsed && imageBytes) {
          const format = (firstString(parsed, ['Format']) ?? 'png').toLowerCase().replace('jpg', 'jpeg')
          frames = [{
            id: step.Id,
            type: 'image',
            status: 'completed',
            url: `data:image/${format};base64,${imageBytes}`,
            alt: 'computeruse.screenshot',
          }]
          break
        }
      }

      // memory_save → memory_save frame. The input JSON carries {content, layer?};
      // the memory_result output carries {Node:{Id,Layer,Content,...}}. Parsed
      // here so the dedicated UI can show what was remembered at a glance.
      if (toolName === 'memory_save' || toolUse?.ToolName === 'memory_save') {
        const parsedInput = parseJsonObject(input) as { content?: string; layer?: string } | null
        const content = parsedInput?.content ?? ''
        const layer = parsedInput?.layer ?? 'session'
        let nodeId: string | undefined
        if (output) {
          const parsedOut = parseJsonObject(output) as { Node?: { Id?: string } } | null
          nodeId = parsedOut?.Node?.Id
        }
        frames = [{
          id: step.Id,
          type: 'memory_save',
          status,
          content,
          layer,
          nodeId,
          isError: toolResult?.IsError === true,
        } as MemorySaveFrame]
        break
      }

      // ask_user tool_call → ask_user frame (step is source of truth).
      // Parse questions from the LLM's tool_use input JSON. The tool_use
      // block's ToolUseId is the same ID the backend uses as resumeRequestID,
      // so the TurnEvent is redundant — reconnection recovers from steps.
      if (toolName === 'ask_user' || toolUse?.ToolName === 'ask_user') {
        const questions = parseAskUserQuestions(input)
        if (questions.length === 0) {
          console.warn('[stepContentToFrames] ask_user tool_call produced 0 questions', {
            stepId: step.Id,
            toolName,
            toolUseToolName: toolUse?.ToolName,
            inputLen: input.length,
            inputPreview: input.slice(0, 200),
          })
        }
        // If tool_result is present the user already answered.
        const askRequestId = toolUse?.ToolUseId || step.Id
        let answers: Record<number, string> | undefined
        try {
          if (output) {
            const ans = JSON.parse(output)
            if (ans && typeof ans === 'object' && !Array.isArray(ans)) {
              answers = {}
              for (const [k, v] of Object.entries(ans)) {
                if (/^\d+$/.test(k)) answers[Number(k)] = String(v)
              }
            }
          }
        } catch { /* ignore */ }
        // Check for a local interaction response (optimistic UI submit).
        const localAsk = localResponses?.get(askRequestId)
        if (!answers && localAsk?.kind === 'ask_answered' && localAsk.answers) {
          answers = localAsk.answers
        }
        frames = [{
          id: step.Id,
          type: 'ask_user',
          status: answers ? 'completed' : status,
          questions,
          answers,
          requestId: askRequestId,
        }]
        break
      }

      // plan_submit produces no step frame — the plan text was already
      // streamed as an assistant text frame, and the approval card arrives
      // via the turn.plan_approval_requested event (pendingPlanApproval).
      // Rendering a fallback here caused "no buttons" because the frame
      // lacked requestId and the PlanBlock footer requires it.
      // ToolName is "plan_submit" (tool name and callable ID are now the same).
      if (/^plan[._]submit$/.test(toolName)) {
        stepCache?.set(step.Id, { sig, frames: [] })
        return []
      }

      // agent_skill_use → skill_use frame. Both the slash path (system-
      // synthesized) and the LLM path produce the same shape: tool_use +
      // tool_result pair. Only the skill name is surfaced; the body is
      // intentionally not shown. isError mirrors the tool_result flag.
      if (toolUse?.ToolName === 'agent_skill_use') {
        const parsedInput = parseJsonObject(input)
        const skillName = (parsedInput && firstString(parsedInput, ['skillId', 'SkillId'])) ?? ''
        const isError = toolResult?.IsError === true
        frames = [{
          id: step.Id,
          type: 'skill_use',
          status: isError ? 'error' : status,
          skillName,
          isError,
        } as SkillUseFrame]
        break
      }

      // Large project.read outputs arrive as a __snapshotRef placeholder.
      // Extract it so ReadToolView shows the "Load file preview" button
      // instead of rendering the raw JSON.
      const snapshotRef = parseSnapshotRef(output)

      // Live execution/explore progress is carried on step.Progress as JSON.
      // Streamed stdout/stderr text is accumulated off-string in a symbol-keyed
      // field on the Step (maintained by step-reducer) so per-chunk cost stays
      // O(1); step.Progress holds the latest delta verbatim (protocol unchanged).
      // For steps without a live accumulator (loaded history / replayed sessions)
      // fall back to parsing step.Progress, preserving the legacy behavior.
      let progress: ToolFrame['progress'] | undefined
      let runningOutput: ToolFrame['runningOutput'] | undefined
      const progressAcc = getExecutionProgressAcc(step)
      if (progressAcc) {
        runningOutput = {
          stdout: typeof progressAcc.stdout === 'string' ? progressAcc.stdout : '',
          stderr: typeof progressAcc.stderr === 'string' ? progressAcc.stderr : '',
        }
      }
      try {
        if (step.Progress) {
          const parsed = JSON.parse(step.Progress)
          if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
            if (!runningOutput && typeof parsed.stream === 'string' && typeof parsed[parsed.stream] === 'string') {
              runningOutput = {
                stdout: typeof parsed.stdout === 'string' ? parsed.stdout : '',
                stderr: typeof parsed.stderr === 'string' ? parsed.stderr : '',
              }
            }
            progress = {
              phase: typeof parsed.phase === 'string' ? parsed.phase : undefined,
              searchCount: typeof parsed.searchCount === 'number' ? parsed.searchCount : undefined,
              readCount: typeof parsed.readCount === 'number' ? parsed.readCount : undefined,
              inputTokens: typeof parsed.inputTokens === 'number' ? parsed.inputTokens : undefined,
              outputTokens: typeof parsed.outputTokens === 'number' ? parsed.outputTokens : undefined,
              activeWork: Array.isArray(parsed.activeWork)
                ? parsed.activeWork.map((w: any) => ({
                    Type: typeof w?.Type === 'string' ? w.Type : typeof w?.type === 'string' ? w.type : '',
                    Target: typeof w?.Target === 'string' ? w.Target : typeof w?.target === 'string' ? w.target : '',
                  }))
                : undefined,
              summaryText: typeof parsed.summaryText === 'string' ? parsed.summaryText : undefined,
            }
          }
        }
      } catch { /* ignore malformed progress JSON */ }

      frames = [{
        id: step.Id,
        type: 'tool',
        status,
        toolName,
        input,
        output: snapshotRef ? undefined : output,
        snapshotRef,
        progress,
        runningOutput,
      }]
      break
    }
    case 'ask_user': {
      const askText = contentBlocks[0]?.Text ?? ''
      const questions = parseAskUserQuestions(askText)
      if (questions.length === 0) {
        console.warn('[stepContentToFrames] ask_user step produced 0 questions', {
          stepId: step.Id,
          textLen: askText.length,
          textPreview: askText.slice(0, 200),
        })
      }
      const reqId = step.RequestId ?? step.Id
      const askLocal = localResponses?.get(reqId)
      const askAnswers = askLocal?.kind === 'ask_answered'
        ? askLocal.answers
        : step.InteractionStatus === 'resolved'
          ? parseResolvedAskUserAnswers(contentBlocks)
          : undefined
      frames = [{
        id: step.Id,
        type: 'ask_user',
        status: askAnswers ? 'completed' : status,
        questions,
        answers: askAnswers,
        requestId: reqId,
      }]
      break
    }
    case 'permission': {
      let toolCalls: Array<{ id: string; callableId: string; input?: string; appId?: string; appName?: string; permissions?: string[]; permissionNote?: string }> = []
      let reason: string | undefined
      try {
        const text = contentBlocks[0]?.Text ?? ''
        if (text) {
          const parsed = JSON.parse(text)
          // emitPermissionEvent sends {toolCalls, reason}; the bare array is
          // the legacy snapshot/replay shape still accepted for history.
          const rawCalls = Array.isArray(parsed)
            ? parsed
            : Array.isArray(parsed?.toolCalls)
              ? parsed.toolCalls
              : []
          toolCalls = rawCalls.map((call: any) => ({
            id: call?.id ?? call?.Id ?? '',
            callableId: call?.callableId ?? call?.CallableId ?? '',
            input: call?.input ?? call?.Input,
            appId: call?.appId ?? call?.AppId,
            appName: call?.appName ?? call?.AppName,
            permissions: call?.permissions ?? call?.Permissions,
            permissionNote: call?.permissionNote ?? call?.PermissionNote,
          }))
          if (parsed && typeof parsed.reason === 'string') reason = parsed.reason
        }
      } catch { /* ignore */ }
      const reqId = step.RequestId ?? step.Id
      const permLocal = localResponses?.get(reqId)
      const permAllowed = permLocal?.kind === 'permission_answered'
        ? permLocal.allowed
        : step.InteractionStatus === 'resolved'
          ? parseResolvedPermissionDecision(contentBlocks)
          : undefined
      frames = [{
        id: step.Id,
        type: 'permission_request',
        status: permAllowed !== undefined ? 'completed' : status,
        toolCalls,
        reason: reason ?? contentBlocks[0]?.Text,
        requestId: reqId,
        allowed: permAllowed,
      }]
      break
    }
    case 'user_inject': {
      // Screenshot observations (engine-emitted, Meta 'screenshot') carry
      // image blocks: render them as image ai-steps in the assistant turn
      // envelope instead of queued-message items. Processing still flows as
      // a user message on the LLM side; only the display lives here.
      const injectImages = contentBlocks
        .filter(b => b.Type === 'image')
        .map(b => ({ url: b.ImageUrl ?? '', alt: b.MimeType ?? '' }))
        .filter(img => img.url)
      if (injectImages.length > 0) {
        const caption = contentBlocks.map(b => b.Text ?? '').join('')
        frames = injectImages.map((img, i) => ({
          id: `${step.Id}-img${i}`,
          type: 'image' as const,
          status,
          url: img.url,
          alt: caption || img.alt,
        }))
        break
      }
      const text = contentBlocks.map(b => b.Text ?? '').join('')
      const uf: UserInjectFrame = {
        id: step.Id,
        type: 'user_inject',
        status,
        items: [{ id: step.Id, text }],
      }
      frames = [uf]
      break
    }
    case 'plan_approval': {
      let planContent = ''
      let tasks: TaskEntry[] = []
      let planEditable = false
      let planGoalActive = false
      try {
        const text = contentBlocks[0]?.Text ?? ''
        if (text) {
          const parsed = JSON.parse(text)
          planContent = parsed.plan ?? ''
          planEditable = parsed.editable === true
          planGoalActive = parsed.goalActive === true
          tasks = (parsed.tasks ?? []).map((t: any) => ({
            id: t.id ?? t.ID ?? '',
            subject: t.subject ?? t.Subject ?? '',
            status: (t.status ?? t.Status ?? 'pending') as TaskEntry['status'],
            activeForm: t.activeForm ?? t.ActiveForm,
          }))
        }
      } catch { /* ignore */ }
      const planReqId = step.RequestId ?? step.Id
      const planLocal = localResponses?.get(planReqId)
      const planResolved = step.InteractionStatus === 'resolved' || planLocal?.kind === 'plan_approval_answered'
      const planDecision = planLocal?.kind === 'plan_approval_answered'
        ? planLocal.decision
        : parsePlanDecision(step.Content)
      // If the backend closes the step without ever resolving it (e.g. after
      // the turn is cancelled/failed/lifecycle-done), detect that
      // "closed but never resolved" shape and render as cancelled so the
      // card drops its buttons instead of looking perpetually actionable.
      const planApprovalStatus: PlanFrame['approvalStatus'] =
        planResolved
          ? (planDecision === 'reject' ? 'rejected' : 'approved')
          : step.Closed
            ? 'cancelled'
            : 'pending'
      frames = [{
        id: step.Id,
        type: 'plan',
        status: (planResolved ? 'completed' : 'running') as Frame['status'],
        content: planDecision === 'edit' && planLocal?.editedPlan ? planLocal.editedPlan : planContent,
        tasks,
        requestId: planReqId,
        approvalStatus: planApprovalStatus,
        editable: planEditable,
        goalActive: planGoalActive,
        policy: planDecision === 'edit' && planLocal?.selectedPolicy ? planLocal.selectedPolicy : undefined,
      }]
      break
    }
    case 'goal_review': {
      // The backend emits goal_review as two content blocks:
      //   [0] "Reviewing goal: <condition>"
      //   [1] {"condition":"...","achieved":true|false,"reason":"...","turnCount":N,"maxTurns":M,"aborted":true|false}
      // Parse the last block as the authoritative verdict.
      let condition = ''
      let verdict: { achieved?: boolean; reason?: string; turnCount?: number; maxTurns?: number; aborted?: boolean; planCardCount?: number } = {}
      for (const block of contentBlocks) {
        const text = block.Text ?? ''
        if (!text) continue
        try {
          const parsed = JSON.parse(text)
          if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && 'achieved' in parsed) {
            verdict = {
              achieved: parsed.achieved === true,
              reason: typeof parsed.reason === 'string' ? parsed.reason : undefined,
              turnCount: typeof parsed.turnCount === 'number' ? parsed.turnCount : undefined,
              maxTurns: typeof parsed.maxTurns === 'number' ? parsed.maxTurns : undefined,
              aborted: parsed.aborted === true,
              planCardCount: typeof parsed.planCardCount === 'number' ? parsed.planCardCount : undefined,
            }
            if (typeof parsed.condition === 'string') {
              condition = parsed.condition
            }
          }
        } catch {
          if (!condition) condition = text.replace(/^Reviewing goal:\s*/i, '').trim()
        }
      }
      if (!condition) {
        condition = contentBlocks[0]?.Text?.replace(/^Reviewing goal:\s*/i, '').trim() ?? ''
      }
      // Live reviewer tool activity is carried by step.execution_progress and
      // persisted in step.Progress.
      let progress: GoalReviewFrame['progress'] = undefined
      if (step.Progress) {
        try {
          const parsed = JSON.parse(step.Progress)
          if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
            progress = {
              phase: typeof parsed.phase === 'string' ? parsed.phase : undefined,
              searchCount: typeof parsed.searchCount === 'number' ? parsed.searchCount : undefined,
              readCount: typeof parsed.readCount === 'number' ? parsed.readCount : undefined,
              summaryText: typeof parsed.summaryText === 'string' ? parsed.summaryText : undefined,
              activeWork: Array.isArray(parsed.activeWork)
                ? parsed.activeWork.map((w: any) => ({
                    Type: typeof w?.Type === 'string' ? w.Type : typeof w?.type === 'string' ? w.type : '',
                    Target: typeof w?.Target === 'string' ? w.Target : typeof w?.target === 'string' ? w.target : '',
                  }))
                : undefined,
            }
          }
        } catch { /* ignore malformed progress JSON */ }
      }
      frames = [{
        id: step.Id,
        type: 'goal_review',
        status: step.Closed ? 'completed' : status,
        condition,
        achieved: verdict.achieved,
        reason: verdict.reason,
        turnCount: verdict.turnCount,
        maxTurns: verdict.maxTurns,
        aborted: verdict.aborted,
        planCardCount: verdict.planCardCount,
        progress,
      } as GoalReviewFrame]
      break
    }
    case 'goal_submit': {
      // The backend emits goal_submit as an interaction step with JSON content:
      //   {"condition":"<original phrase>","interpretedGoal":"<LLM interpretation>"}
      // Scan every text block: after resolution the step also carries the
      // decision payload (and the raw goal_submit tool blocks), so the request
      // payload is not guaranteed to be the first block.
      let condition = ''
      let interpretedGoal = ''
      for (const block of contentBlocks) {
        if (block.Type !== 'text' || !block.Text) continue
        try {
          const parsed = JSON.parse(block.Text)
          if (parsed && typeof parsed === 'object' &&
              (typeof parsed.condition === 'string' || typeof parsed.interpretedGoal === 'string')) {
            condition = typeof parsed.condition === 'string' ? parsed.condition : ''
            interpretedGoal = typeof parsed.interpretedGoal === 'string' ? parsed.interpretedGoal : ''
            break
          }
        } catch { /* not the goal payload — keep scanning */ }
      }
      const goalReqId = step.RequestId ?? step.Id
      // Mirror plan approval: consult the optimistic local response so the
      // card flips immediately on click, and parse the resolved decision so
      // a rejection doesn't render as approved.
      const goalLocal = localResponses?.get(goalReqId)
      const goalResolved = step.InteractionStatus === 'resolved' || goalLocal?.kind === 'goal_submit_answered'
      const goalDecision = goalLocal?.kind === 'goal_submit_answered'
        ? goalLocal.decision
        : parsePlanDecision(step.Content)
      // A step closed without resolution (turn cancelled / failed) renders as
      // cancelled so the card drops its buttons instead of looking actionable.
      const goalApprovalStatus: GoalSubmitFrame['approvalStatus'] =
        goalResolved
          ? (goalDecision === 'reject' ? 'rejected' : 'approved')
          : step.Closed
            ? 'cancelled'
            : 'pending'
      frames = [{
        id: step.Id,
        type: 'goal_submit',
        status: goalResolved || step.Closed ? 'completed' : status,
        condition,
        interpretedGoal,
        requestId: goalReqId,
        approvalStatus: goalApprovalStatus,
      } as GoalSubmitFrame]
      break
    }
    case 'goal_card_submit': {
      // The backend emits goal_card_submit as an interaction step with a
      // JSON task payload:
      //   {"cardId":"<existing task card id>","interpretedGoal":"<LLM interpretation>"}
      // Scan every text block (after resolution the step also carries the
      // decision payload), mirroring goal_submit.
      let cardId = ''
      let interpretedGoal = ''
      for (const block of contentBlocks) {
        if (block.Type !== 'text' || !block.Text) continue
        try {
          const parsed = JSON.parse(block.Text)
          if (parsed && typeof parsed === 'object' &&
              (typeof parsed.cardId === 'string' || typeof parsed.interpretedGoal === 'string')) {
            cardId = typeof parsed.cardId === 'string' ? parsed.cardId : ''
            interpretedGoal = typeof parsed.interpretedGoal === 'string' ? parsed.interpretedGoal : ''
            break
          }
        } catch { /* not the goal card payload — keep scanning */ }
      }
      const cardReqId = step.RequestId ?? step.Id
      // Mirror goal_submit: consult the optimistic local response so the card
      // flips immediately on click, and parse the resolved decision so a
      // rejection doesn't render as approved.
      const cardLocal = localResponses?.get(cardReqId)
      const cardResolved = step.InteractionStatus === 'resolved' || cardLocal?.kind === 'goal_card_submit_answered'
      const cardDecision = cardLocal?.kind === 'goal_card_submit_answered'
        ? cardLocal.decision
        : parsePlanDecision(step.Content)
      // A step closed without resolution (turn cancelled / failed) renders as
      // cancelled so the card drops its buttons instead of looking actionable.
      const cardApprovalStatus: GoalCardSubmitFrame['approvalStatus'] =
        cardResolved
          ? (cardDecision === 'reject' ? 'rejected' : 'approved')
          : step.Closed
            ? 'cancelled'
            : 'pending'
      frames = [{
        id: step.Id,
        type: 'goal_card_submit',
        status: cardResolved || step.Closed ? 'completed' : status,
        cardId,
        interpretedGoal: interpretedGoal || undefined,
        requestId: cardReqId,
        approvalStatus: cardApprovalStatus,
      } as GoalCardSubmitFrame]
      break
    }
    default:
      frames = []
  }

  stepCache?.set(step.Id, { sig, frames })
  return frames
}

/** Parse a step Meta of the form "<kind>|<id>|<name>" (kind = agent | user)
 *  into a peerSender identity. Returns undefined for bare markers ("agent",
 *  "user") and unrelated metas — mirror of the backend parseSenderMeta. */
export function parseSenderMeta(meta: string | undefined): { kind: 'agent' | 'user'; id: string; name: string } | undefined {
  if (!meta) return undefined
  let kind: 'agent' | 'user'
  if (meta.startsWith('agent|')) kind = 'agent'
  else if (meta.startsWith('user|')) kind = 'user'
  else return undefined
  const parts = meta.slice(kind.length + 1).split('|', 2)
  const id = parts[0] ?? ''
  const name = parts[1] ?? ''
  if (!id && !name) return undefined
  return { kind, id, name }
}

/** Convert a user Step into a user TurnEnvelope. */
function userStepToEnvelope(step: Step): TurnEnvelope {
  const text = step.Content.map(b => b.Text ?? '').join('')
  const userImages = step.Content
    ?.filter(b => b.Type === 'image')
    .map(b => ({ url: b.ImageUrl ?? '', alt: b.MimeType ?? '' }))
    .filter(img => img.url) ?? []
  // Meta="goal" / "workflow" mark system-originated continuation messages
  // (goal-mode continue prompts and workflow updater notifications).
  // Human messages carry Meta "user" or "user|<id>|<name>", which maps to
  // no systemOrigin and keeps the plain user bubble.
  const systemOrigin = step.Meta === 'goal' || step.Meta === 'workflow' ? step.Meta : undefined
  // Peer agent messages (agent|<id>|<name>) and human injects
  // (user|<id>|<name>) surface the sender for the avatar rendering. The
  // legacy self-stamp (user|<subject>|<subject>, identical halves, written
  // before the backend stopped stamping direct submits) is the local
  // operator's own message and must not render a foreign-sender avatar.
  const parsedSender = parseSenderMeta(step.Meta)
  const peerSender = parsedSender && !(parsedSender.kind === 'user' && parsedSender.id && parsedSender.id === parsedSender.name)
    ? parsedSender
    : undefined
  return {
    id: step.Id,
    clientKey: step.Id,
    role: 'user',
    userContent: text,
    userImages: userImages.length > 0 ? userImages : undefined,
    frames: text
      ? [{ id: `${step.Id}-text`, type: 'text', status: 'completed', content: text }]
      : [],
    timestamp: step.Timestamp || new Date().toISOString(),
    completed: true,
    seq: step.Seq ?? undefined,
    metadata: { turnId: step.TurnId || step.Id },
    systemOrigin,
    peerSender,
  }
}

/** Build an assistant envelope from consecutive assistant steps.
 *  When turnId collides with a user envelope ID (compact flow where
 *  both the user step and the compaction step share the same TurnID),
 *  fall back to the first step's actual ID to avoid merging in dedup. */
function buildAssistantEnvelope(asstSteps: Step[], userIds?: Set<string>, localResponses?: ReadonlyMap<string, LocalInteractionResponse>, stepCache?: Map<string, { sig: string; frames: Frame[] }>): TurnEnvelope {
  const turnId = asstSteps[0]!.TurnId ?? asstSteps[0]!.Id
  const frames: Frame[] = []
  let inputTokens = 0
  let outputTokens = 0
  let totalTokens = 0
  for (const s of asstSteps) {
    frames.push(...stepContentToFrames(s, localResponses, stepCache))
    if (s.Usage) {
      inputTokens += s.Usage.InputTokens ?? 0
      outputTokens += s.Usage.OutputTokens ?? 0
      totalTokens += s.Usage.TotalTokens ?? 0
    }
  }

  // The backend emits both a tool_call step (source of truth) and an
  // interaction step for ask_user. Deduplicate by question content so only
  // one ask_user card renders per turn. Prefer the first (tool_call) frame.
  const askUserKeys = new Set<string>()
  const hasNonEmptyAskUser = frames.some(
    f => f.type === 'ask_user' && (f as AskUserQuestionFrame).questions.length > 0,
  )
  const dedupedFrames = frames.filter(f => {
    if (f.type !== 'ask_user') return true
    const af = f as AskUserQuestionFrame
    if (hasNonEmptyAskUser && af.questions.length === 0) return false
    const key = JSON.stringify(af.questions)
    if (askUserKeys.has(key)) return false
    askUserKeys.add(key)
    return true
  })

  // The LLM streams the plan as visible assistant text, then the backend emits
  // a plan_approval step carrying the same content plus approval metadata.
  // Drop the duplicate text frame so the plan renders as a single node with
  // the approval UI appended, rather than showing the text once and the plan
  // card again.
  const mergedPlanFrames: Frame[] = []
  for (let i = 0; i < dedupedFrames.length; i++) {
    const frame = dedupedFrames[i]!
    if (frame.type === 'plan' && i > 0) {
      const planFrame = frame as PlanFrame
      const prev = dedupedFrames[i - 1]!
      if (prev.type === 'text') {
        const textFrame = prev as TextFrame
        const planContent = planFrame.content.trim()
        const textContent = textFrame.content.trim()
        if (
          textContent &&
          planContent &&
          (textContent === planContent || planContent.startsWith(textContent))
        ) {
          mergedPlanFrames.pop()
        }
      }
    }
    mergedPlanFrames.push(frame)
  }

  const hasDispatchStep = asstSteps.some(s => s.Type !== 'turn_start')
  const allClosed = hasDispatchStep && asstSteps.every(s => s.Closed)
  const id = userIds?.has(turnId) ? asstSteps[0]!.Id : turnId
  return {
    id,
    role: 'assistant',
    frames: mergedPlanFrames,
    timestamp: asstSteps[0]!.Timestamp || new Date().toISOString(),
    completed: allClosed,
    seq: asstSteps[0]!.Seq ?? undefined,
    metadata: {
      turnId,
      usage: totalTokens > 0 ? { inputTokens, outputTokens, totalTokens } : undefined,
    },
  }
}

/** Convert Step[] into TurnEnvelope[] for rendering compatibility.
 *  Processes steps in array order: user steps become individual user envelopes,
 *  consecutive assistant steps are merged into one assistant envelope.
 *  This preserves the natural chronological ordering of the step stream. */
export function stepsToEnvelopes(
  steps: Step[],
  localResponses?: ReadonlyMap<string, LocalInteractionResponse>,
  reservedUserIds?: Set<string>,
  stepCache?: Map<string, { sig: string; frames: Frame[] }>,
): TurnEnvelope[] {
  // Prune stale entries from the step frame cache so memory doesn't grow
  // unbounded during long sessions.
  if (stepCache) {
    const activeStepIds = new Set(steps.map(s => s.Id))
    for (const id of stepCache.keys()) {
      if (!activeStepIds.has(id)) {
        stepCache.delete(id)
      }
    }
  }

  // Sort by Seq (or timestamp for legacy data) before grouping so envelope
  // order is deterministic even when the input array arrives out of order.
  const sortedSteps = sortStepsBySeq(steps)

  const envelopes: TurnEnvelope[] = []
  const userIds = new Set<string>(reservedUserIds ?? [])
  let asstBuffer: Step[] = []

  for (const step of sortedSteps) {
    if (step.Discarded) {
      continue
    }
    if (step.Role === 'user') {
      if (asstBuffer.length > 0) {
        envelopes.push(buildAssistantEnvelope(asstBuffer, userIds, localResponses, stepCache))
        asstBuffer = []
      }
      const ue = userStepToEnvelope(step)
      userIds.add(ue.id)
      envelopes.push(ue)
    } else {
      // Different TurnId means a new assistant turn — flush the buffer.
      if (asstBuffer.length > 0 && asstBuffer[0]!.TurnId !== step.TurnId) {
        envelopes.push(buildAssistantEnvelope(asstBuffer, userIds, localResponses, stepCache))
        asstBuffer = []
      }
      asstBuffer.push(step)
    }
  }

  if (asstBuffer.length > 0) {
    envelopes.push(buildAssistantEnvelope(asstBuffer, userIds, localResponses, stepCache))
  }

  return envelopes
}

/** Merge steps-derived envelopes into an existing envelope list.
 *  Replaces assistant envelopes for turns that have step data,
 *  preserving other envelopes (e.g. from history loading). */
export function mergeStepEnvelopes(
  existing: TurnEnvelope[],
  stepEnvelopes: TurnEnvelope[],
): TurnEnvelope[] {
  const stepTurnIds = new Set(stepEnvelopes.map(e => e.metadata?.turnId).filter(Boolean))
  const filtered = existing.filter(e => !(e.role === 'assistant' && stepTurnIds.has(e.metadata?.turnId)))
  return [...filtered, ...stepEnvelopes]
}

/** A frame is "empty" if it carries no renderable content. Used by dedupe to
 *  decide whether to keep the existing frames when an incoming envelope shows
 *  up empty (e.g. context_budget stubs, partial compaction markers). AUDIT 5.4:
 *  the prior check only inspected `text` frames; reasoning / tool /
 *  permission_request / ask_user / plan frames could slip through as "non-
 *  empty" with no actual payload, overwriting real history frames. */
function isFrameEmpty(f: Frame): boolean {
  switch (f.type) {
    case 'text':
      return !(f as any).content
    case 'reasoning':
      return !(f as any).content
    case 'tool': {
      const tf = f as any
      return !tf.toolName && !tf.input && !tf.output
    }
    case 'permission_request': {
      const pf = f as any
      return !pf.toolCalls || pf.toolCalls.length === 0
    }
    case 'ask_user': {
      const af = f as any
      return !af.questions || af.questions.length === 0
    }
    case 'plan': {
      const pf = f as any
      return !pf.content && (!pf.tasks || pf.tasks.length === 0)
    }
    case 'user_inject': {
      const uf = f as any
      return !uf.items || uf.items.length === 0
    }
    case 'goal_review': {
      const gf = f as any
      return !gf.condition && gf.achieved === undefined
    }
    case 'goal_submit': {
      const gf = f as any
      return !gf.condition && !gf.interpretedGoal
    }
    case 'goal_card_submit': {
      const gf = f as any
      return !gf.cardId && !gf.interpretedGoal
    }
    default:
      return false
  }
}

function isEnvelopeEmpty(env: TurnEnvelope): boolean {
  return env.frames.length === 0 || env.frames.every(isFrameEmpty)
}

/** Remove duplicate envelopes by id, preserving the last occurrence.
 *  This is a defensive guard against overlapping history + step data. */
/** Returns true if any step in the array has been discarded. */
export function hasDiscardedSteps(steps: Step[]): boolean {
  return steps.some(s => s.Discarded)
}

export function dedupeEnvelopes(envelopes: TurnEnvelope[]): TurnEnvelope[] {
  const seen = new Map<string, TurnEnvelope>()
  for (const env of envelopes) {
    const existing = seen.get(env.id)
    if (existing) {
      const incomingEmpty = isEnvelopeEmpty(env)
      const existingEmpty = isEnvelopeEmpty(existing)
      // When the incoming (last) occurrence has empty frames but the existing
      // one has content, prefer the existing frames. This guards against
      // context_budget / compaction stubs overwriting step-derived content.
      const frames = incomingEmpty && !existingEmpty ? existing.frames : env.frames
      // An explicit live state from the incoming projection must be allowed
      // to reopen a stale history envelope. In particular, a paused turn is
      // resumable and must not inherit completed=true from history.
      const incomingState = env.metadata?.turnState
      const incomingLive = incomingState === 'running' || incomingState === 'paused'
      const existingBudget = existing.metadata?.contextBudget
      const incomingBudget = env.metadata?.contextBudget
      const contextBudget = existingBudget && existingBudget.estimatedTokens > 0 &&
        (!incomingBudget || incomingBudget.estimatedTokens === 0)
        ? existingBudget
        : incomingBudget ?? existingBudget
      seen.set(env.id, {
        ...env,
        frames,
        idx: env.idx ?? existing.idx,
        seq: env.seq ?? existing.seq,
        clientKey: env.clientKey || existing.clientKey,
        completed: incomingLive ? false : env.completed || existing.completed,
        userImages: env.userImages ?? existing.userImages,
        userAttachments: env.userAttachments ?? existing.userAttachments,
        fileChanges: env.fileChanges ?? existing.fileChanges,
        tasks: env.tasks ?? existing.tasks,
        metadata: { ...existing.metadata, ...env.metadata, contextBudget },
      })
    } else {
      seen.set(env.id, env)
    }
  }
  // Keep first occurrence position to preserve causal order (user before
  // assistant). Use merged data from `seen` so the last (freshest) occurrence's
  // frames/metadata win, with idx/clientKey backfilled from earlier entries.
  const result: TurnEnvelope[] = []
  const added = new Set<string>()
  for (const env of envelopes) {
    if (!added.has(env.id)) {
      result.push(seen.get(env.id)!)
      added.add(env.id)
    }
  }
  return result
}
