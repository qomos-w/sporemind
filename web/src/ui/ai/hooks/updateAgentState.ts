import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import type { ModelSlot, WorkspaceUpdateAgentReq } from '../../../gen-clients/system/types'
import { getAgentInfoById } from './agentInfoStore'

/**
 * SSOT entry point for partial agent updates.
 *
 * Backend `workspace.update_agent` treats a missing (nil) optional field as
 * "clear this field", not "leave unchanged". Callers that only want to change
 * one field (e.g. switching the primary model) would therefore clobber
 * Execution/Fast that they did not intend to touch.
 *
 * This function merges the caller's patch over the agent's current state from
 * the store and sends a complete payload, so untouched fields keep their
 * existing values instead of being cleared.
 *
 * Fields the caller explicitly wants to clear must pass `null` (for nullable
 * ModelSlot fields). `undefined` means "do not touch".
 */
export interface AgentStatePatch {
  DisplayName?: string
  Primary?: ModelSlot | null
  Fast?: ModelSlot | null
  Execution?: ModelSlot | null
  Review?: ModelSlot | null
  Summary?: ModelSlot | null
}

export async function updateAgentState(agentId: string, patch: AgentStatePatch): Promise<void> {
  const current = getAgentInfoById(agentId)
  if (!current) {
    throw new Error(`updateAgentState: agent ${agentId} not found in store`)
  }

  const req: WorkspaceUpdateAgentReq = {
    AgentId: agentId,
  }

  if (patch.DisplayName !== undefined) {
    req.DisplayName = patch.DisplayName
  }

  // For nullable slot fields: undefined = keep current, null = clear, object = set.
  req.Primary = resolveSlot(patch.Primary, current.Primary)
  req.Fast = resolveSlot(patch.Fast, current.Fast)
  req.Execution = resolveSlot(patch.Execution, current.Execution)
  req.Review = resolveSlot(patch.Review, current.Review)
  req.Summary = resolveSlot(patch.Summary, current.Summary)

  await workspace.updateAgent(client, req)
}

/**
 * Resolve a nullable slot field patch.
 * - undefined: keep current value (send it back so backend doesn't clear it)
 * - null: explicitly clear (send undefined so backend omits/clears it)
 * - object: set new value
 */
function resolveSlot(
  patchSlot: ModelSlot | null | undefined,
  currentSlot: ModelSlot | undefined,
): ModelSlot | undefined {
  if (patchSlot === undefined) {
    return currentSlot ? { Candidates: currentSlot.Candidates } : undefined
  }
  if (patchSlot === null) {
    return undefined
  }
  return { Candidates: patchSlot.Candidates }
}
