import React from 'react'
import { useI18n } from '../../../i18n'
import { useAgentInfoList } from '../hooks/agentInfoStore'
import { avatarHue, agentDisplayName } from '../lib/agent-avatar'
import { requestOpenAgentChat } from '../pendingAgentChat'
import { AgentAvatarContent } from './AgentAvatarContent'

/** Sender identity carried on user envelopes whose step Meta is
 *  agent|<id>|<name> (peer agent) or user|<id>|<name> (human inject).
 *  The local operator's own messages never carry one. */
export interface SenderIdentity {
  kind: 'agent' | 'user'
  id: string
  name: string
}

interface SenderAvatarProps {
  sender: SenderIdentity
  /** Avatar diameter in px (defaults to 22). */
  size?: number
}

/** Small circular sender avatar for user-side message rows. Agent senders
 *  resolve through the agent info store (stable id first, actor id as
 *  fallback — the backend meta may carry either) and clicking them navigates
 *  to that agent's conversation via the shared open-agent-chat event. */
export function SenderAvatar({ sender, size = 22 }: SenderAvatarProps) {
  const { t } = useI18n()
  // Subscribe (not a bare snapshot read) so late-loading agent lists resolve.
  const snapshot = useAgentInfoList()
  const info = sender.kind === 'agent'
    ? (snapshot.byId.get(sender.id) ?? snapshot.byActorId.get(sender.id))
    : undefined

  const label = info
    ? agentDisplayName(info.Title, info.DisplayName)
    : (sender.name || sender.id || 'AG')
  const agentKind = info?.AgentKind ?? (sender.kind === 'agent' ? 'agent' : '')
  const hue = avatarHue(sender.id || sender.name || label)

  const canJump = sender.kind === 'agent' && !!(info?.ActorId && info?.ProjectId)
  const style = {
    '--avatar-hue': hue,
    width: size,
    height: size,
    fontSize: Math.max(8, Math.round(size * 0.4)),
  } as React.CSSProperties

  const content = (
    <AgentAvatarContent
      agent={{ DisplayName: label, AgentKind: agentKind }}
      size={Math.round(size * 0.8)}
      mushroomScale={0.9}
    />
  )

  if (canJump) {
    return (
      <button
        type="button"
        className="sender-avatar sender-avatar-jump"
        style={style}
        title={t('ai.sender.openChat', { name: label })}
        aria-label={t('ai.sender.openChat', { name: label })}
        onClick={() => requestOpenAgentChat(info!.ProjectId, info!.ActorId)}
      >
        {content}
      </button>
    )
  }
  return (
    <span className="sender-avatar" style={style} title={label} aria-label={label}>
      {content}
    </span>
  )
}
