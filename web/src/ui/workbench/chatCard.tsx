import { MessageCircle } from 'lucide-react'

import { useI18n } from '../../i18n'
import type { WorkbenchCardState } from '../../gen-clients/system/types'
import { requestOpenAgentChat } from '../ai/pendingAgentChat'
import type { WorkbenchCardDescriptor } from './cardTypes'

/**
 * Workbench chat card (kind 'chat') — one card per agent conversation the
 * workbench actor has raised from `step` / `turn` activity.
 *
 * The compact tile is host-side metadata (agent name + recent turn status /
 * session title); clicking it promotes the card into the main slot, where
 * {@link ChatCardPanel} renders the full session summary and an entry point
 * that switches the shell to the conversation flow (the established
 * `sporemind:open-agent-chat` navigation contract → ContentMode 'conversation'
 * + session select). Attention (score / slot / pin) stays with the workbench
 * actor projection; this module owns presentation only.
 */

/** Registry id prefix for a chat card (shared with the actor's `agent:` ids). */
export const CHAT_CARD_PREFIX = 'agent:'

/** Registry id for an agent's chat card. */
export function chatCardId(actorId: string): string {
  return `${CHAT_CARD_PREFIX}${actorId}`
}

/** Actor id encoded in a chat card id ('' when the id is not a chat card). */
export function chatActorId(cardId: string): string {
  return cardId.startsWith(CHAT_CARD_PREFIX) ? cardId.slice(CHAT_CARD_PREFIX.length) : ''
}

/** Target of a chat card's "open conversation" jump. */
export interface ChatConversationTarget {
  actorId: string
  projectId: string
}

/** Cap the compact summary so a long session title cannot overflow the tile. */
export const CHAT_STATUS_TEXT_MAX = 80

/**
 * One-line session summary for the compact chat tile: the recent turn status
 * plus the session title when it differs from the agent name (spec: the chat
 * card shows the current session summary — recent turn status / title).
 */
export function chatStatusText(input: { name?: string; status?: string; sessionTitle?: string }): string {
  const parts: string[] = []
  const status = (input.status ?? '').trim()
  if (status) parts.push(status)
  const title = (input.sessionTitle ?? '').trim()
  if (title && title !== (input.name ?? '').trim()) parts.push(title)
  const text = parts.join(' · ')
  return text.length > CHAT_STATUS_TEXT_MAX ? `${text.slice(0, CHAT_STATUS_TEXT_MAX)}…` : text
}

/** Coarse relative time for the session's last activity ('' when <1 min/unknown). */
export function formatRelativeTime(iso: string, now: number = Date.now()): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const diff = now - t
  if (diff < 0) return ''
  const min = Math.floor(diff / 60000)
  if (min < 1) return ''
  if (min < 60) return `${min}m`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h`
  return `${Math.floor(hr / 24)}d`
}

export interface ChatCardOptions {
  id?: string
  actorId?: string
  projectId?: string
  title?: string
  icon?: string
  color?: string
  /** Recent turn status (drives the compact summary). */
  status?: string
  /** Session title; shown in the summary when it differs from the agent name. */
  sessionTitle?: string
  /** ISO timestamp of the last turn / activity, shown in the expanded body. */
  lastActivity?: string
  /** Override the default `sporemind:open-agent-chat` jump (tests / hosts). */
  onOpenConversation?: (target: ChatConversationTarget) => void
}

/**
 * Build the workbench chat card descriptor. The card base registers this in its
 * card registry; `render` is null while compact (the wrapper renders the
 * host-metadata tile) and the interactive {@link ChatCardPanel} when expanded.
 */
export function createChatCardDescriptor(options: ChatCardOptions = {}): WorkbenchCardDescriptor {
  const actorId = options.actorId ?? ''
  const id = options.id ?? (actorId ? chatCardId(actorId) : '')
  const title = options.title?.trim() || actorId || 'Conversation'
  const projectId = options.projectId ?? ''

  return {
    id,
    kind: 'chat',
    title,
    icon: options.icon?.trim() || 'message-circle',
    ...(options.color ? { color: options.color } : {}),
    // Fallback attention only: the live workbench projection overlays this with
    // the actor's step/turn-weighted score.
    score: 10,
    pinned: false,
    compactMeta: {
      statusText: chatStatusText({ name: title, status: options.status, sessionTitle: options.sessionTitle }),
    },
    render: (expanded: boolean) =>
      expanded ? (
        <ChatCardPanel
          actorId={actorId}
          projectId={projectId}
          title={title}
          status={options.status}
          sessionTitle={options.sessionTitle}
          lastActivity={options.lastActivity}
          onOpenConversation={options.onOpenConversation}
        />
      ) : null,
  }
}

export interface ChatCardPanelProps {
  actorId: string
  projectId: string
  title: string
  status?: string
  sessionTitle?: string
  lastActivity?: string
  onOpenConversation?: (target: ChatConversationTarget) => void
}

/**
 * Expanded chat card body: the session summary (status / title / last activity)
 * and the jump into the conversation flow. The jump reuses the cross-component
 * `sporemind:open-agent-chat` contract, so AIShellLayout switches ContentMode
 * to 'conversation' and selects the agent's session.
 */
export function ChatCardPanel({
  actorId,
  projectId,
  title,
  status,
  sessionTitle,
  lastActivity,
  onOpenConversation,
}: ChatCardPanelProps) {
  const { t } = useI18n()

  const handleOpen = () => {
    if (onOpenConversation) {
      onOpenConversation({ actorId, projectId })
      return
    }
    requestOpenAgentChat(projectId, actorId)
  }

  const statusText = (status ?? '').trim() || 'idle'
  const session = (sessionTitle ?? '').trim()
  const time = formatRelativeTime(lastActivity ?? '')

  return (
    <div className="wb-chat" data-actor-id={actorId}>
      <div className="wb-chat-summary" data-status={statusText}>
        <span className="wb-chat-status">{statusText}</span>
        {session && session !== title && (
          <span className="wb-chat-session" title={session}>
            {session}
          </span>
        )}
        {time && (
          <span className="wb-chat-time" title={lastActivity}>
            {time}
          </span>
        )}
      </div>
      <button type="button" className="wb-chat-open" onClick={handleOpen}>
        <MessageCircle size={13} />
        <span>{t('ai.sender.openChat', { name: title })}</span>
      </button>
    </div>
  )
}

/** Session metadata the chat descriptor builder consumes (per actor id). */
export interface ChatSessionInfo {
  name?: string
  projectId?: string
  status?: string
  title?: string
  lastActivity?: string
}

/**
 * Build a chat card descriptor for every projected `chat` card, joined with the
 * resolved agent session summary. Registration is keyed by the projected id
 * (`agent:<actorId>`), so the board's `withProjection` overlay keeps the actor's
 * attention score / slot / pin while this supplies the render body.
 */
export function createChatCardDescriptors(
  states: readonly WorkbenchCardState[],
  sessions: ReadonlyMap<string, ChatSessionInfo>,
): WorkbenchCardDescriptor[] {
  const out: WorkbenchCardDescriptor[] = []
  for (const state of states) {
    if (state.Kind !== 'chat') continue
    const actorId = chatActorId(state.Id)
    if (!actorId) continue
    const session = sessions.get(actorId)
    out.push(
      createChatCardDescriptor({
        id: state.Id,
        actorId,
        projectId: session?.projectId,
        title: session?.name || state.Title || actorId,
        icon: state.Icon || undefined,
        color: state.Color || undefined,
        status: session?.status,
        sessionTitle: session?.title,
        lastActivity: session?.lastActivity,
      }),
    )
  }
  return out
}
