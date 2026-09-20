import { AlertTriangle, BookOpen, Clock, Database, Folder, Gauge, GitBranch, LayoutGrid, MessageCircle, Network, ShieldCheck, Terminal, Waypoints } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import type { ContentMode } from './components/AIShellSidebar'
import { useI18n } from '../../i18n'
import type { I18nKey } from '../../i18n/types'

export interface ModeMeta {
  id: ContentMode
  Icon: LucideIcon
  /** i18n key for the label; when empty, falls back to labelFallback. */
  labelKey: string
  labelFallback: string
  /** Whether the mode can be pinned to the sidebar. conversation is always shown. */
  pinnable: boolean
}

/** All content modes in onboarding-aligned order. conversation is first and non-pinnable. */
export const CONTENT_MODES: ModeMeta[] = [
  // Step 3 — first conversation (default mode)
  { id: 'conversation', Icon: MessageCircle, labelKey: 'shell.sidebar.conversationMode', labelFallback: 'Conversation', pinnable: false },
  // Step 4 — /workflow (core capability)
  { id: 'workflow', Icon: Waypoints, labelKey: 'shell.sidebar.workflowMode', labelFallback: 'Workflow', pinnable: true },
  // Step 4.6 — topology view
  { id: 'topology', Icon: Network, labelKey: 'shell.sidebar.topologyMode', labelFallback: 'Topology', pinnable: true },
  // Explore more — 知识库
  { id: 'notes', Icon: BookOpen, labelKey: 'shell.sidebar.knowledgeBase', labelFallback: 'Knowledge Base', pinnable: true },
  // Explore more — 文件浏览
  { id: 'files', Icon: Folder, labelKey: 'shell.sidebar.filesMode', labelFallback: 'Files', pinnable: true },
  // Explore more — 多控制台
  { id: 'multiconsole', Icon: LayoutGrid, labelKey: 'shell.sidebar.multiConsoleMode', labelFallback: 'Multi-Console', pinnable: true },
  // Remaining features (not in onboarding tour)
  { id: 'scheduled', Icon: Clock, labelKey: 'shell.sidebar.scheduledMode', labelFallback: 'Scheduled Tasks', pinnable: true },
  { id: 'ssh', Icon: Terminal, labelKey: 'shell.sidebar.sshMode', labelFallback: 'SSH', pinnable: true },
  { id: 'git', Icon: GitBranch, labelKey: 'shell.sidebar.gitMode', labelFallback: 'Git', pinnable: true },
  { id: 'db', Icon: Database, labelKey: 'shell.sidebar.dbMode', labelFallback: 'Databases', pinnable: true },
  // Bottom: diagnostics & stats
  { id: 'problems', Icon: AlertTriangle, labelKey: 'shell.contentMode.problems', labelFallback: 'Problems', pinnable: true },
  { id: 'approvals', Icon: ShieldCheck, labelKey: 'shell.sidebar.approvalsMode', labelFallback: 'Approvals', pinnable: true },
  { id: 'aistats', Icon: Gauge, labelKey: 'shell.contentMode.modelStats', labelFallback: 'Model Stats', pinnable: true },
]

export interface ResolvedMode {
  id: ContentMode
  Icon: LucideIcon
  label: string
  pinnable: boolean
}

/** Resolves i18n labels for all content modes. */
export function useResolvedContentModes(): ResolvedMode[] {
  const { t } = useI18n()
  return CONTENT_MODES.map(m => ({
    id: m.id,
    Icon: m.Icon,
    label: m.labelKey
      ? t(m.labelKey as I18nKey, { defaultValue: m.labelFallback })
      : m.labelFallback,
    pinnable: m.pinnable,
  }))
}
