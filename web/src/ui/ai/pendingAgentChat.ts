let pending: { projectId: string; agentActorId: string } | null = null

export function setPendingAgentChat(info: { projectId: string; agentActorId: string }) {
  pending = info
}

export function consumePendingAgentChat() {
  const p = pending
  pending = null
  return p
}

/** Dispatch a `sporemind:open-agent-chat` CustomEvent so AIShellLayout's
 *  existing listener navigates to the agent's conversation.  This is the
 *  cross-component entry point for "click avatar → open chat" flows. */
export function requestOpenAgentChat(projectId: string, agentActorId: string): void {
  window.dispatchEvent(
    new CustomEvent('sporemind:open-agent-chat', {
      detail: { projectId, agentActorId },
    }),
  )
}
