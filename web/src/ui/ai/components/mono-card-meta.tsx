import {
  MessageSquareText,
  Wand2,
  Lightbulb,
  Terminal,
  Boxes,
  CalendarClock,
  CheckSquare,
  FileText,
  Waypoints,
  Archive,
  Circle,
  RefreshCw,
  CheckCircle2,
  ShieldAlert,
  Trash2,
  Package,
  Eye,
  Bot,
} from 'lucide-react'
import type { MonoCardType, ReminderPriority } from '../../../domain/mono-types'

export const TYPE_META: Record<MonoCardType, { icon: React.ReactNode; label: string }> = {
  agent: { icon: <Bot size={14} />, label: 'Agent' },
  prompt: { icon: <MessageSquareText size={14} />, label: 'Prompt' },
  skill: { icon: <Wand2 size={14} />, label: 'Skill' },
  concept: { icon: <Lightbulb size={14} />, label: 'Concept' },
  callable: { icon: <Terminal size={14} />, label: 'Callable' },
  capability_module: { icon: <Boxes size={14} />, label: 'Capability' },
  scheduler: { icon: <CalendarClock size={14} />, label: 'Scheduler' },
  task: { icon: <CheckSquare size={14} />, label: 'Task' },
  wiki: { icon: <FileText size={14} />, label: 'Wiki' },
  workflow: { icon: <Waypoints size={14} />, label: 'Workflow' },
  bundle: { icon: <Package size={14} />, label: 'Bundle' },
}

/** Ordered list of all canonical card types, for type selectors. */
export const CARD_TYPES: MonoCardType[] = [
  'wiki', 'task', 'workflow', 'scheduler', 'concept', 'prompt', 'skill', 'callable', 'capability_module', 'bundle', 'agent',
]

export function getCardTypeMeta(type: MonoCardType | undefined): { icon: React.ReactNode; label: string } {
  return TYPE_META[type ?? 'wiki'] ?? TYPE_META.wiki
}

export const PRIORITY_META: Record<ReminderPriority, { color: string; label: string }> = {
  low: { color: 'var(--text-tertiary)', label: 'Low' },
  medium: { color: 'var(--info)', label: 'Medium' },
  high: { color: 'var(--warning)', label: 'High' },
  urgent: { color: 'var(--error)', label: 'Urgent' },
}

export const STATUS_META: Record<string, { color: string; label: string; icon: React.ReactNode }> = {
  backlog: { color: 'var(--text-tertiary)', label: 'Backlog', icon: <Archive size={10} /> },
  todo: { color: 'var(--accent-primary)', label: 'To Do', icon: <Circle size={10} /> },
  doing: { color: 'var(--warning)', label: 'In Progress', icon: <RefreshCw size={10} /> },
  pending_review: { color: '#f97316', label: 'Pending Review', icon: <Eye size={10} /> },
  done: { color: 'var(--success)', label: 'Done', icon: <CheckCircle2 size={10} /> },
  blocked: { color: 'var(--error)', label: 'Blocked', icon: <ShieldAlert size={10} /> },
  cancelled: { color: '#9333ea', label: 'Cancelled', icon: <Trash2 size={10} /> },
}

export type TaskCategory = 'research' | 'explore' | 'execute' | 'code' | 'review'

export const CATEGORY_META: Record<TaskCategory, { color: string; label: string }> = {
  research: { color: '#6366f1', label: '研究' },
  explore: { color: '#06b6d4', label: '探索' },
  execute: { color: 'var(--warning)', label: '执行' },
  code: { color: 'var(--accent-primary)', label: '编码' },
  review: { color: 'var(--error)', label: '审核' },
}
