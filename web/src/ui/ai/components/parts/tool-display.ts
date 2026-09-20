import React from 'react'
import {
  CheckSquare,
  FileText,
  Film,
  Globe,
  Image as ImageIcon,
  Inbox,
  Layers,
  Pencil,
  Plug,
  Search,
  Send,
  ShieldCheck,
  SquareTerminal,
  Trash2,
  Wifi,
  Wrench,
} from 'lucide-react'

import type { I18nKey } from '../../../../i18n/types'

// Placeholder text emitted by the backend converter when glob/grep returns
// zero results. The frontend must recognize it to avoid counting it as 1 item.
export const NO_MATCHES_TEXT = '(no matches)'

// ── Tool kind classification ──

export type ToolKind = 'bash' | 'read' | 'edit' | 'delete' | 'search' | 'glob' | 'web' | 'list' | 'explore' | 'review' | 'agent_review' | 'agent_message' | 'fork' | 'task' | 'card' | 'mcp' | 'page_preview' | 'media_gen' | 'ssh_session' | 'ssh_run' | 'generic'

/**
 * Classify a tool name into a semantic kind for display purposes.
 */
export function getToolKind(toolName: string): ToolKind {
  const n = normalizeToolName(toolName)
  // MCP tools are exposed to the LLM with an "mcp-" prefix (e.g.
  // mcp-filesystem-read_file); the legacy dotted "mcp." form is also accepted.
  // Check the raw prefix FIRST so names like mcp-files-read_file don't fall
  // through to the substring checks below (read_file would match 'read').
  const lower = toolName.toLowerCase()
  if (lower.startsWith('mcp.') || lower.startsWith('mcp-')) return 'mcp'
  // Wiki/card callables — check FIRST because names like list_cards, edit_summary
  // would otherwise be misclassified by the file-specific checks below.
  // All project.wiki_* callables contain 'wiki' in the normalized name.
  if (n.includes('wiki') || n.includes('card')) return 'card'
  // Agent-to-agent messaging (workspace.agent_send_message /
  // workspace.agent_read_message) — dedicated chat-bubble view; must be
  // checked BEFORE the generic 'read'/'search' substring matches below.
  if (n.includes('agentsendmessage') || n.includes('agentreadmessage')) return 'agent_message'
  // Deletion tools (project.rm, filesystem.rm, sshmanager.file_delete) —
  // segment-match the raw name so words merely containing "rm" (confirm,
  // transform) are not misclassified. Must stay after the card check so
  // wiki delete_card keeps its card view.
  const segments = toolName.toLowerCase().split(/[^a-z0-9]+/)
  if (segments.includes('rm') || segments.includes('delete') || segments.includes('remove')) return 'delete'
  // Page thumbnail cards — check BEFORE the generic 'web' catch-all so the
  // show_page_thumbnail tool gets its dedicated preview view.
  if (n.includes('pagethumbnail')) return 'page_preview'
  // Media generation tools (generate_image / image_generate / generate_video /
  // video_generate) — dedicated view renders the output file inline.
  if (n.includes('generateimage') || n.includes('imagegenerate')) return 'media_gen'
  if (n.includes('generatevideo') || n.includes('videogenerate')) return 'media_gen'
  // SSH session tools — check BEFORE the 'shell' catch-all in 'bash' below,
  // so shell_open / shell_run get their dedicated views instead of BashToolView.
  if (n.includes('shellopen') || n.includes('opensshsession')) return 'ssh_session'
  if (n.includes('shellrun')) return 'ssh_run'
  if (n.includes('bash') || n.includes('terminal') || n.includes('shell') || n.includes('command')) return 'bash'
  // System introspection tools that search by query (list_callables,
  // search_services, list_agents) — classify as search before the generic
  // 'list' catch-all below, since their semantics is querying, not listing
  // directory contents.
  if ((n.includes('callable') || n.includes('services') || n.includes('agents')) && (n.includes('list') || n.includes('search'))) return 'search'
  if (n.includes('list')) return 'list'
  if (n.includes('glob')) return 'glob'
      if (n.includes('read') || n.includes('openfile') || segments.includes('cat')) return 'read'
  if (n.includes('write') || n.includes('edit') || n.includes('replace') || n.includes('patch')) return 'edit'
  if (n.includes('grep') || n.includes('search') || n.includes('scan')) return 'search'
  if (n.includes('web') || n.includes('fetch') || n.includes('url') || n.includes('http')) return 'web'
  if (n.includes('explore')) return 'explore'
  // workspace.agent_review — dedicated structured view; check BEFORE the
  // generic 'review' kind so it is not routed to ExploreToolView.
  if (n.includes('agentreview')) return 'agent_review'
  if (n.includes('review')) return 'review'
  if (n.includes('subagent') || n.includes('sub_agent') || n.includes('fork')) return 'fork'
  if (n.includes('task')) return 'task'
  return 'generic'
}

// ── Display metadata ──

export interface ToolDisplayMeta {
  title: string
  subtitle?: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  kind: ToolKind
}

type TFunction = (key: I18nKey, params?: Record<string, string | number>) => string

/**
 * Build display metadata (title, subtitle, icon, kind) from tool name and raw input.
 */
export function buildToolDisplay(toolName: string, input: string, t: TFunction = k => String(k)): ToolDisplayMeta {
  const kind = getToolKind(toolName)
  const parsed = parseJsonObject(input)

  switch (kind) {
    case 'bash': {
      const command = parsed ? firstString(parsed, ['command', 'cmd']) : undefined
      const rawArgs = parsed ? parsed['Args'] ?? parsed['args'] : undefined
      const args = Array.isArray(rawArgs)
        ? rawArgs.filter((a): a is string => typeof a === 'string' && a.length > 0)
        : []
      const subtitle = command && args.length > 0
        // Same quoting rule as BashToolView's displayCommand so the collapsed
        // subtitle matches the expanded Command block.
        ? [command, ...args.map(a => (/[\s"']/u.test(a) ? JSON.stringify(a) : a))].join(' ')
        : command
      return {
        title: t('ai.tool.runCommand'),
        subtitle,
        icon: SquareTerminal,
        kind,
      }
    }
    case 'list':
      return {
        title: t('ai.tool.listDirectory'),
        subtitle: normalizePath(parsed ? firstString(parsed, ['path']) : undefined),
        icon: Search,
        kind,
      }
    case 'read':
      return {
        title: t('ai.tool.readFile'),
        subtitle: normalizePath(parsed ? firstString(parsed, ['file_path', 'filepath', 'path']) : undefined),
        icon: FileText,
        kind,
      }
    case 'edit': {
      const isWrite = normalizeToolName(toolName).includes('write')
      return {
        title: isWrite ? t('ai.tool.writeFile') : t('ai.tool.editFile'),
        subtitle: normalizePath(parsed ? firstString(parsed, ['file_path', 'filepath', 'path']) : undefined),
        icon: Pencil,
        kind,
      }
    }
    case 'delete':
      return {
        title: t('ai.tool.deleteFile'),
        subtitle: normalizePath(parsed ? firstString(parsed, ['file_path', 'filepath', 'path']) : undefined),
        icon: Trash2,
        kind,
      }
    case 'glob': {
      let path = parsed ? firstString(parsed, ['path']) : undefined
      let pat = parsed ? firstString(parsed, ['pattern']) : undefined
      if (!path && pat && !isRegexPattern(pat)) {
        const split = splitPatternPath(pat)
        if (split.path) {
          path = split.path
          pat = split.pattern
        }
      }
      const scope = [path, pat].filter(Boolean).join(' · ')
      return {
        title: t('ai.tool.searchFiles'),
        subtitle: scope || undefined,
        icon: Search,
        kind,
      }
    }
    case 'search': {
      let path = parsed ? firstString(parsed, ['path']) : undefined
      let glob = parsed ? firstString(parsed, ['glob']) : undefined
      if (!path && glob) {
        const split = splitPatternPath(glob)
        if (split.path) {
          path = split.path
          glob = split.pattern
        }
      }
      const pat = parsed ? firstString(parsed, ['pattern']) : undefined
      const query = parsed ? firstString(parsed, ['query', 'Query']) : undefined
      const scope = [path, pat, glob, query].filter(Boolean).join(' · ')
      const outputMode = parsed ? firstString(parsed, ['output_mode', 'OutputMode']) : undefined
      const title = outputMode === 'files_with_matches' || outputMode === 'files'
        ? t('ai.tool.searchFiles')
        : outputMode === 'count'
          ? t('ai.tool.searchCounts')
          : t('ai.tool.searchContent')
      return {
        title,
        subtitle: scope || undefined,
        icon: Search,
        kind,
      }
    }
    case 'web':
      return {
        title: t('ai.tool.fetchWebContent'),
        subtitle: parsed ? firstString(parsed, ['url']) : undefined,
        icon: Globe,
        kind,
      }
    case 'page_preview':
      return {
        title: t('ai.tool.pagePreview'),
        subtitle: parsed ? firstString(parsed, ['Title', 'title', 'Url', 'url']) : undefined,
        icon: Globe,
        kind,
      }
    case 'media_gen': {
      const normalized = normalizeToolName(toolName)
      const isVideo = normalized.includes('video')
      return {
        title: isVideo ? t('ai.tool.generateVideo') : t('ai.tool.generateImage'),
        subtitle: parsed ? firstString(parsed, ['Prompt', 'prompt']) : undefined,
        icon: isVideo ? Film : ImageIcon,
        kind,
      }
    }
    case 'ssh_session':
      return {
        title: t('ai.tool.openSshSession'),
        subtitle: parsed ? firstString(parsed, ['HostId', 'hostId']) : undefined,
        icon: Wifi,
        kind,
      }
    case 'ssh_run':
      return {
        title: t('ai.tool.sshRunCommand'),
        subtitle: parsed ? firstString(parsed, ['Command', 'command']) : undefined,
        icon: SquareTerminal,
        kind,
      }
    case 'explore':
      return {
        title: t('ai.tool.explore'),
        subtitle: parsed ? firstString(parsed, ['Prompt', 'prompt', 'Description', 'description']) : undefined,
        icon: Search,
        kind,
      }
    case 'review':
      return {
        title: t('ai.tool.reviewGoal'),
        subtitle: parsed ? firstString(parsed, ['ReviewText', 'reviewText']) : undefined,
        icon: Search,
        kind,
      }
    case 'agent_review': {
      const agentId = parsed ? firstString(parsed, ['AgentActorId', 'agentActorId']) : undefined
      const cardId = parsed ? firstString(parsed, ['TaskCardId', 'taskCardId']) : undefined
      // Input carries Decision (approve|reject); prefixing it makes the
      // outcome visible without expanding the card.
      const decision = parsed ? firstString(parsed, ['Decision', 'decision']) : undefined
      const decisionLabel = decision ? decision.charAt(0).toUpperCase() + decision.slice(1) : undefined
      const subtitle = [decisionLabel, agentId ?? cardId].filter(Boolean).join(' · ')
      return {
        title: t('ai.tool.agentReview'),
        subtitle: subtitle || undefined,
        icon: ShieldCheck,
        kind,
      }
    }
    case 'fork':
      return {
        title: t('ai.tool.runParallel'),
        subtitle: parsed ? firstString(parsed, ['Description', 'description', 'Prompt', 'prompt']) : undefined,
        icon: Search,
        kind,
      }
    case 'agent_message': {
      const isSend = normalizeToolName(toolName).includes('agentsendmessage')
      return {
        title: isSend ? t('ai.tool.agentMessage.send') : t('ai.tool.agentMessage.read'),
        subtitle: parsed ? firstString(parsed, ['ToAgentId', 'toAgentId']) : undefined,
        icon: isSend ? Send : Inbox,
        kind,
      }
    }
    case 'task': {
      const normalized = normalizeToolName(toolName)
      const isCreate = normalized.includes('create')
      const subject = parsed ? firstString(parsed, ['subject', 'Subject', 'description', 'Description']) : undefined
      // update_task input is {Id, Status} (no Subject); cancel_task is {Id}.
      // Fall back to "Id → Status" so these steps carry a meaningful subtitle.
      let subtitle = subject
      if (!subtitle) {
        const id = parsed ? firstString(parsed, ['id', 'Id']) : undefined
        const status = parsed ? firstString(parsed, ['status', 'Status']) : undefined
        subtitle = id && status ? `${id} → ${status}` : id
      }
      return {
        title: isCreate ? t('ai.tool.createTask') : t('ai.tool.updateTask'),
        subtitle,
        icon: CheckSquare,
        kind,
      }
    }
    case 'card': {
      const normalized = normalizeToolName(toolName)
      const cardId = parsed ? firstString(parsed, ['Id', 'id', 'CardId', 'cardId', 'TaskId', 'taskId']) : undefined
      let title: string
      if (normalized.includes('getcard') || normalized.includes('getsummary') || normalized.includes('getconstraints')) {
        title = t('ai.tool.readCard')
      } else if (normalized.includes('createcard')) {
        title = t('ai.tool.createCard')
      } else if (normalized.includes('editcard') || normalized.includes('editsummary') || normalized.includes('editconstraints')) {
        title = t('ai.tool.editCard')
      } else if (normalized.includes('deletecard')) {
        title = t('ai.tool.deleteCard')
      } else if (normalized.includes('opencard') || normalized.includes('closecard')) {
        title = normalized.includes('opencard') ? t('ai.tool.openCard') : t('ai.tool.closeCard')
      } else if (normalized.includes('listallcards') || normalized.includes('listcards') || normalized.includes('listallcard')) {
        title = t('ai.tool.listCards')
      } else if (normalized.includes('setstatus')) {
        title = t('ai.tool.setCardStatus')
      } else if (normalized.includes('hierarchy')) {
        title = t('ai.tool.cardHierarchy')
      } else {
        title = t('ai.tool.wiki')
      }
      // set_status input is {Id, Status}: surface the transition so the
      // outcome is visible without expanding. search_cards /
      // search_card_content carry Query (no Id): fall back to it.
      let subtitle: string | undefined
      if (normalized.includes('setstatus')) {
        const status = parsed ? firstString(parsed, ['Status', 'status']) : undefined
        subtitle = cardId && status ? `${cardId} → ${status}` : cardId
      } else {
        subtitle = cardId ?? (parsed ? firstString(parsed, ['Query', 'query']) : undefined)
      }
      return {
        title,
        subtitle,
        icon: Layers,
        kind,
      }
    }
    case 'mcp': {
      const { server, tool } = parseMCPToolName(toolName)
      const subtitle = [server, tool].filter(Boolean).join(' · ')
      return {
        title: t('ai.tool.mcp'),
        subtitle: subtitle || undefined,
        icon: Plug,
        kind,
      }
    }
    default: {
      const normalized = normalizeToolName(toolName)
      const title = toolName.replace(/[_-]+/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
      // invoke_callable input is {Service, CallID, Payload}: surface the
      // target so the step shows what was actually invoked, not an empty row.
      if (normalized === 'invokecallable') {
        const service = parsed ? firstString(parsed, ['Service', 'service']) : undefined
        const callId = parsed ? firstString(parsed, ['CallID', 'callID', 'callId']) : undefined
        const subtitle = [service, callId].filter(Boolean).join(' · ')
        return { title, subtitle: subtitle || undefined, icon: Wrench, kind }
      }
      return {
        title,
        subtitle: parsed
          ? firstString(parsed, [
            'file_path', 'filepath', 'path', 'command', 'pattern', 'url',
            // Scalar fields for tools without a dedicated kind:
            // inspect_actor (ActorPath), capture_profile (Profile),
            // frontend_debug (Operation), generate_image/video & memory_recall
            // (Prompt/Query), diagnostic filters (Source/Severity/Level/Message).
            'ActorPath', 'Profile', 'Operation',
            'Source', 'Severity', 'Level', 'Query', 'Prompt', 'Message',
          ])
          : undefined,
        icon: Wrench,
        kind,
      }
    }
  }
}

// ── Utility helpers ──

export function splitPatternPath(pattern: string | undefined): { path: string | undefined; pattern: string | undefined } {
  if (!pattern) return { path: undefined, pattern }
  const trimmed = pattern.trim()
  if (!trimmed) return { path: undefined, pattern: trimmed }
  const lastSlash = trimmed.lastIndexOf('/')
  if (lastSlash <= 0) return { path: undefined, pattern: trimmed }
  const prefix = trimmed.slice(0, lastSlash)
  if (/[\*\?\[]/.test(prefix)) return { path: undefined, pattern: trimmed }
  return { path: prefix, pattern: trimmed.slice(lastSlash + 1) }
}

export function isRegexPattern(p: string): boolean {
  if (/[|+\()$^]/.test(p)) return true
  if (/\{\d+[,\}]\d*\}/.test(p)) return true
  return false
}

export function normalizeToolName(toolName: string): string {
  return toolName.toLowerCase().replace(/[^a-z0-9]/g, '')
}

/**
 * Split an MCP tool name into its server and tool parts. Two shapes exist:
 *   - LLM-facing: "mcp-<server>-<tool>" (hyphen-delimited, since dots are not
 *     allowed in provider tool names); split on the first hyphen after the
 *     server segment.
 *   - Legacy dotted: "mcp.<server>.<tool>" (e.g. persisted frames); split on
 *     '.' only so segments keep internal hyphens like "srv-0".
 * The dotted tool remainder is preserved verbatim.
 */
export function parseMCPToolName(toolName: string): { server: string | undefined; tool: string | undefined } {
  const lower = toolName.toLowerCase()
  // Legacy dotted form: split on '.' only so segments keep internal hyphens.
  if (lower.startsWith('mcp.')) {
    const parts = toolName.slice(4).split('.')
    if (parts.length < 2) return { server: parts[0], tool: undefined }
    return { server: parts[0], tool: parts.slice(1).join('.') }
  }
  // LLM-facing hyphen form: split on the first '-' after the server segment.
  if (lower.startsWith('mcp-')) {
    const rest = toolName.slice(4)
    const nextSep = rest.indexOf('-')
    if (nextSep < 0) return { server: rest, tool: undefined }
    return { server: rest.slice(0, nextSep), tool: rest.slice(nextSep + 1) }
  }
  return { server: undefined, tool: undefined }
}

export function normalizePath(p: string | undefined): string | undefined {
  if (!p) return p
  return p.replace(/\\/g, '/')
}

export function parseJsonObject(input: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(input)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    // not valid JSON
  }
  return null
}

export function firstString(record: Record<string, unknown>, keys: string[]): string | undefined {
  for (const key of keys) {
    // Case-insensitive: try exact match first, then lowercase key.
    const value = record[key] ?? record[key.toLowerCase()] ?? record[key.charAt(0).toUpperCase() + key.slice(1).toLowerCase()]
    if (typeof value === 'string' && value.trim().length > 0) return value
  }
  return undefined
}

/**
 * Convert common escape sequences in a string to their actual characters.
 * Handles \n, \r, \t, \\, \" and strips trailing \r before \n.
 */
export function unescapeOutput(text: string): string {
  return text
    .replace(/\\r\\n/g, '\n')
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\r')
    .replace(/\\t/g, '\t')
    .replace(/\\\\/g, '\\')
    .replace(/\\"/g, '"')
}
