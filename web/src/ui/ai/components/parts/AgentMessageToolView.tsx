import React from 'react'
import { Check, CircleAlert } from 'lucide-react'
import type { ToolFrame, SlotStatus } from '../../model/frame-types.ts'
import { ToolBodyFrame, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { parseJsonObject, firstString, normalizeToolName } from './tool-display.ts'
import { useI18n } from '../../../../i18n/provider'
import type { I18nKey } from '../../../../i18n/types'

// ── Pure parsing helpers (unit-testable) ──

export interface AgentMessageItem {
  seq?: number
  role?: string
  content?: string
}

export interface AgentMessageData {
  /** 'send' = workspace.agent_send_message, 'read' = workspace.agent_read_message. */
  action: 'send' | 'read'
  /** Peer agent ref (e.g. "Build Weaver#635e") from the tool input. */
  targetAgentId?: string
  /** Outgoing message text (send only). */
  text?: string
  /** Delivery flag from the send result. */
  sent?: boolean
  /** Messages returned by the read result (chronological). */
  messages: AgentMessageItem[]
  /** Read result pagination flag. */
  hasMore?: boolean
}

type TFunction = (key: I18nKey, params?: Record<string, string | number>) => string

/**
 * Parse agent messaging tool input + output into a structured shape.
 * Send reads Text/ToAgentId from input and Sent from the result; read reads
 * ToAgentId from input and Items/HasMore from the result.
 */
export function parseAgentMessage(toolName: string, input: string, output?: string): AgentMessageData {
  const action: 'send' | 'read' = normalizeToolName(toolName).includes('agentsendmessage') ? 'send' : 'read'
  const inObj = parseJsonObject(input)
  const outObj = parseJsonObject(output ?? '')
  const rawItems = outObj && Array.isArray(outObj.Items) ? (outObj.Items as Record<string, unknown>[]) : []
  return {
    action,
    targetAgentId: inObj ? firstString(inObj, ['ToAgentId', 'toAgentId']) : undefined,
    text: inObj ? firstString(inObj, ['Text', 'text', 'Message', 'message']) : undefined,
    sent: outObj && typeof outObj.Sent === 'boolean' ? outObj.Sent : undefined,
    // Backend returns newest-first (paging contract via BeforeSeq); the chat
    // bubble stream must render chronologically. Reverse, then stable-sort by
    // Seq ascending; items missing Seq fall back to their post-reverse index.
    messages: [...rawItems].reverse().map((item, i) => ({
      seq: typeof item.Seq === 'number' ? item.Seq : i + 1,
      role: typeof item.Role === 'string' ? item.Role : undefined,
      content: typeof item.Content === 'string' ? item.Content : undefined,
    })).sort((a, b) => a.seq - b.seq),
    hasMore: outObj && typeof outObj.HasMore === 'boolean' ? outObj.HasMore : undefined,
  }
}

/** Whether the frame should render the failure row instead of the chat body. */
export function isAgentMessageFailed(data: AgentMessageData, status: SlotStatus): boolean {
  if (status === 'error') return true
  return data.action === 'send' && data.sent === false
}

// ── Component ──

function Bubble({ item, t }: { item: AgentMessageItem; t: TFunction }) {
  const outbound = item.role === 'user'
  return (
    <div className={`ai-agent-message-bubble-row ${outbound ? 'out' : 'in'}`}>
      <span className="ai-agent-message-bubble-role">
        {outbound ? t('ai.tool.agentMessage.me') : t('ai.tool.agentMessage.peer')}
      </span>
      <div className="ai-agent-message-bubble">{item.content ?? ''}</div>
    </div>
  )
}

export const AgentMessageToolView: React.FC<{ frame: ToolFrame }> = ({ frame }) => {
  const { t } = useI18n()
  const data = parseAgentMessage(frame.toolName, frame.input, frame.output)
  const running = frame.status === 'pending' || frame.status === 'running'
  const failed = isAgentMessageFailed(data, frame.status)

  if (running) {
    return (
      <ToolBodyFrame frame={frame}>
        <div className="ai-agent-message ai-agent-message--running">
          <span className="ai-agent-message-label">
            {data.action === 'send' ? t('ai.tool.agentMessage.sending') : t('ai.tool.agentMessage.reading')}
          </span>
          {data.targetAgentId && <span className="ai-agent-message-target">{data.targetAgentId}</span>}
        </div>
      </ToolBodyFrame>
    )
  }

  if (failed) {
    return (
      <ToolBodyFrame frame={frame}>
        <div className="ai-agent-message ai-agent-message--failed">
          <CircleAlert size={14} className="ai-agent-message-icon" />
          <span>{t('ai.tool.agentMessage.failed')}</span>
        </div>
        {frame.output && (
          <ToolCodeBlock error maxLines={8}>
            {frame.output}
          </ToolCodeBlock>
        )}
      </ToolBodyFrame>
    )
  }

  return (
    <ToolBodyFrame frame={frame}>
      <div className="ai-agent-message">
        <div className="ai-agent-message-header">
          {data.targetAgentId && <span className="ai-agent-message-target">{data.targetAgentId}</span>}
          {data.action === 'send' ? (
            <span className="ai-agent-message-badge">
              <Check size={12} />
              {t('ai.tool.agentMessage.sent')}
            </span>
          ) : (
            <span className="ai-agent-message-badge">
              {t('ai.tool.agentMessage.messages', { count: data.messages.length })}
              {data.hasMore ? '+' : ''}
            </span>
          )}
        </div>
        {data.action === 'send' ? (
          data.text ? (
            <div className="ai-agent-message-bubble-row out">
              <span className="ai-agent-message-bubble-role">{t('ai.tool.agentMessage.me')}</span>
              <div className="ai-agent-message-bubble">{data.text}</div>
            </div>
          ) : null
        ) : data.messages.length === 0 ? (
          <div className="ai-agent-message-empty">{t('ai.tool.agentMessage.empty')}</div>
        ) : (
          <div className="ai-agent-message-list">
            {data.messages.map((item, i) => (
              <Bubble key={item.seq ?? i} item={item} t={t} />
            ))}
          </div>
        )}
      </div>
    </ToolBodyFrame>
  )
}
