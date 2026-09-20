import { useAgentInfoList } from '../hooks/agentInfoStore'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { avatarHue, agentAvatarLabel, agentDisplayName } from '../lib/agent-avatar'
import './CardAgentAvatar.css'

interface CardAgentAvatarProps {
  agentRef: string
  /** Optional click handler.  When provided, the avatar becomes interactive:
   *  clicks are stopPropagation'd so they never trigger the host card's onClick.
   *  The resolved AgentInfo (or undefined if the agent is not in the store) and
   *  the raw agentRef are passed to the callback. */
  onClick?: (agent: AgentInfo | undefined, agentRef: string) => void
}

export function CardAgentAvatar({ agentRef, onClick }: CardAgentAvatarProps) {
  const snapshot = useAgentInfoList()
  const agent = snapshot.byActorId.get(agentRef) ?? snapshot.byId.get(agentRef)
  const title = agentDisplayName(agent?.Title, agent?.DisplayName ?? '', agentRef)
  const initials = agentAvatarLabel(agent?.DisplayName, agent?.AgentKind, agentRef.slice(0, 2).toUpperCase())
  const hue = avatarHue(agent?.Id ?? agentRef)
  const isWorking = agent?.IsWorking ?? false

  return (
    <span
      className={`card-agent-avatar${onClick ? ' card-agent-avatar--clickable' : ''}`}
      title={title}
      style={{ '--avatar-hue': hue } as React.CSSProperties}
      role={onClick ? 'button' : undefined}
      onClick={onClick
        ? (e: React.MouseEvent) => { e.stopPropagation(); onClick(agent, agentRef) }
        : undefined}
    >
      {initials}
      {isWorking && <span className="card-agent-avatar-ring" />}
    </span>
  )
}
