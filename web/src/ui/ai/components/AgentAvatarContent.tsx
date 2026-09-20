import { Pause } from 'lucide-react'
import { agentAvatarLabel } from '../lib/agent-avatar'
import { MushroomIcon } from './MushroomIcon'

interface AgentAvatarContentProps {
  agent: { DisplayName: string; AgentKind: string; Status?: string }
  /** Mushroom icon size in px. */
  size: number
  /** Pause glyph size in px (defaults to 10). */
  pauseSize?: number
  /** Artwork scale for the coordinator mushroom (defaults to 1, uncropped). */
  mushroomScale?: number
  /** Stem length for the mushroom (defaults to 548 short; 578 is full). */
  mushroomStemEnd?: number
  /** Mushroom stroke width in viewBox units (defaults to 80). */
  mushroomStrokeWidth?: number
}

// Avatar content: paused agents show a pause glyph, coordinators show the
// brand mushroom instead of initials, everyone else gets two-letter initials.
export function AgentAvatarContent({ agent, size, pauseSize = 10, mushroomScale = 1, mushroomStemEnd = 548, mushroomStrokeWidth = 80 }: AgentAvatarContentProps) {
  if (agent.Status === 'paused') return <Pause size={pauseSize} strokeWidth={3} aria-label="paused" />
  if ((agent.AgentKind ?? '').toLowerCase() === 'coordinator') return <MushroomIcon size={size} artScale={mushroomScale} stemEnd={mushroomStemEnd} strokeWidth={mushroomStrokeWidth} />
  return <>{agentAvatarLabel(agent.DisplayName, agent.AgentKind)}</>
}
