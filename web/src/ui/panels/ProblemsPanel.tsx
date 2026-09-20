import { useState, useEffect, useCallback } from 'react'
import { AlertTriangle, AlertCircle, Info, Trash2, Copy, ClipboardCopy } from 'lucide-react'
import * as oracle from '../../gen-clients/oracle/client'
import type { Diagnostic } from '../../gen-clients/system/types'
import { client } from '../../application/generated-client'

// ── Constants ──

const SEVERITY_ICON: Record<string, typeof AlertCircle> = {
  error: AlertCircle,
  warning: AlertTriangle,
  info: Info,
}

const SEVERITY_COLOR: Record<string, string> = {
  error: 'var(--status-failed)',
  warning: 'var(--status-drifted)',
  info: 'var(--text-secondary)',
}

// ── Helpers ──

function formatDiagnostic(d: Diagnostic): string {
  const lines: string[] = [`[${d.Severity}] ${d.CallableId || d.Source || 'unknown'}`]
  if (d.Message) lines.push(`Message: ${d.Message}`)
  if (d.Timestamp) lines.push(`Timestamp: ${d.Timestamp}`)
  if (d.Source) lines.push(`Source: ${d.Source}`)
  if (d.CallableId) lines.push(`Callable: ${d.CallableId}`)
  if (d.TargetService) lines.push(`Service: ${d.TargetService}`)
  if (d.ToolUseId) lines.push(`ToolUseId: ${d.ToolUseId}`)
  if (d.AgentId) lines.push(`Agent: ${d.AgentId}`)
  if (d.TurnId) lines.push(`Turn: ${d.TurnId}`)
  if (d.StepId) lines.push(`Step: ${d.StepId}`)
  if (d.Input) lines.push(`Input: ${d.Input}`)
  if (d.Output) lines.push(`Output: ${d.Output}`)
  if (d.Unit?.model) lines.push(`Model: ${d.Unit.model}`)
  if (d.Unit?.provider) lines.push(`Provider: ${d.Unit.provider}`)
  if (d.HttpStatus) lines.push(`HTTP: ${d.HttpStatus}`)
  if (d.RawData) lines.push(`Raw: ${d.RawData}`)
  return lines.join('\n')
}

// Frontend display cap for Input/Output fields. The backend already truncates
// to ~2000 chars for storage; this is a tighter UI cap so the expanded row stays
// scannable. The full payload lives in step.Content via block.appended.
const FIELD_DISPLAY_CAP = 200

function truncateField(s: string | undefined): string | undefined {
  if (!s) return s
  if (s.length <= FIELD_DISPLAY_CAP) return s
  return s.slice(0, FIELD_DISPLAY_CAP) + '… [truncated]'
}

// Bypass permission approvals live in their own Approvals tab, not here.
const EXCLUDED_SOURCES = new Set(['permission_bypass'])

// ── Panel ──

export function ProblemsPanel() {
  const [diagnostics, setDiagnostics] = useState<Diagnostic[]>([])
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [severityFilter, setSeverityFilter] = useState<string>('all')
  const [copiedAll, setCopiedAll] = useState(false)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  // Load history on mount
  useEffect(() => {
    let cancelled = false
    oracle.listDiagnostics(client, {}).then(resp => {
      if (!cancelled) setDiagnostics((resp.Items ?? []).filter(d => !EXCLUDED_SOURCES.has(d.Source ?? '')))
    }).catch(() => {})
    return () => { cancelled = true }
  }, [])

  // Subscribe to real-time diagnostics
  useEffect(() => {
    const off = oracle.OnDiagnostic(client, (diag: Diagnostic) => {
      if (EXCLUDED_SOURCES.has(diag.Source ?? '')) return
      setDiagnostics(prev => [diag, ...prev].slice(0, 200))
    })
    return off
  }, [])

  const toggleExpanded = useCallback((id: string) => {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const handleCopyAll = useCallback(() => {
    const items = severityFilter === 'all'
      ? diagnostics
      : diagnostics.filter(d => d.Severity === severityFilter)
    if (items.length === 0) return
    const text = items.map(formatDiagnostic).join('\n\n---\n\n')
    navigator.clipboard?.writeText(text).then(() => {
      setCopiedAll(true)
      setTimeout(() => setCopiedAll(false), 1200)
    }).catch(() => {})
  }, [diagnostics, severityFilter])

  const moveToTopAndCopy = useCallback((id: string) => {
    setDiagnostics(prev => {
      const idx = prev.findIndex(d => d.Id === id)
      if (idx < 0) return prev
      const item = prev[idx]
      if (!item) return prev
      navigator.clipboard?.writeText(formatDiagnostic(item)).then(() => {
        setCopiedId(id)
        setTimeout(() => setCopiedId(null), 1200)
      }).catch(() => {})
      return [item, ...prev.slice(0, idx), ...prev.slice(idx + 1)]
    })
  }, [])

  const filtered = severityFilter === 'all'
    ? diagnostics
    : diagnostics.filter(d => d.Severity === severityFilter)

  const counts = { error: 0, warning: 0, info: 0 }
  for (const d of diagnostics) {
    if (d.Severity in counts) counts[d.Severity as keyof typeof counts]++
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', flex: 1, minWidth: 0, minHeight: 0, height: '100%', fontSize: 12 }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '4px 8px',
        borderBottom: '1px solid var(--border-default)', flexShrink: 0,
      }}>
        <SeverityButton label="All" count={diagnostics.length}
          active={severityFilter === 'all'} onClick={() => setSeverityFilter('all')} />
        <SeverityButton label="Errors" count={counts.error} color="var(--status-failed)"
          active={severityFilter === 'error'} onClick={() => setSeverityFilter('error')} />
        <SeverityButton label="Warnings" count={counts.warning} color="var(--status-drifted)"
          active={severityFilter === 'warning'} onClick={() => setSeverityFilter('warning')} />
        <SeverityButton label="Info" count={counts.info} color="var(--text-secondary)"
          active={severityFilter === 'info'} onClick={() => setSeverityFilter('info')} />
        <div style={{ flex: 1 }} />
        <button onClick={handleCopyAll} title={copiedAll ? 'Copied' : 'Copy all'}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: copiedAll ? 'var(--status-success, var(--text-secondary))' : 'var(--text-tertiary)', padding: 2 }}>
          <Copy size={14} />
        </button>
        <button onClick={() => setDiagnostics([])} title="Clear all"
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-tertiary)', padding: 2 }}>
          <Trash2 size={14} />
        </button>
      </div>

      {/* List */}
      <div style={{ flex: 1, overflow: 'auto', paddingBottom: 'calc(var(--composer-card-top, var(--composer-frame-h, 120px)) + 16px)' }}>
        {filtered.length === 0 && (
          <div style={{ padding: 16, color: 'var(--text-tertiary)', textAlign: 'center' }}>
            No problems
          </div>
        )}
        {filtered.map(d => (
          <ProblemRow key={d.Id} diagnostic={d}
            expanded={expanded.has(d.Id)}
            copied={copiedId === d.Id}
            onToggle={() => toggleExpanded(d.Id)}
            onMoveTopAndCopy={() => moveToTopAndCopy(d.Id)} />
        ))}
      </div>
    </div>
  )
}

// ── Sub-components ──

function SeverityButton({ label, count, color, active, onClick }: {
  label: string, count: number, color?: string, active: boolean, onClick: () => void
}) {
  return (
    <button onClick={onClick} style={{
      background: active ? 'var(--bg-hover)' : 'none',
      border: 'none', borderRadius: 3, padding: '2px 6px', cursor: 'pointer',
      fontSize: 11, color: color ?? 'var(--text-secondary)',
      display: 'flex', alignItems: 'center', gap: 4,
    }}>
      {label} <span style={{ opacity: 0.7 }}>{count}</span>
    </button>
  )
}

function ProblemRow({ diagnostic: d, expanded, copied, onToggle, onMoveTopAndCopy }: {
  diagnostic: Diagnostic, expanded: boolean, copied: boolean, onToggle: () => void, onMoveTopAndCopy: () => void
}) {
  const Icon = SEVERITY_ICON[d.Severity] ?? AlertCircle
  const color = SEVERITY_COLOR[d.Severity] ?? 'var(--text-secondary)'
  const time = d.Timestamp ? (() => {
    const dt = new Date(d.Timestamp)
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())} ${pad(dt.getHours())}:${pad(dt.getMinutes())}:${pad(dt.getSeconds())}`
  })() : ''

  return (
    <div style={{ borderBottom: '1px solid var(--border-subtle)' }}>
      <div onClick={onToggle} style={{
        display: 'flex', alignItems: 'flex-start', gap: 6, padding: '4px 8px',
        cursor: 'pointer', lineHeight: 1.4,
      }}>
        <Icon size={14} style={{ color, flexShrink: 0, marginTop: 2 }} />
        <div style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ color, flexShrink: 0 }}>{d.CallableId || d.TargetService || d.Source || 'unknown'}</span>
          {d.Message && <MessageBadge message={d.Message} />}
        </div>
        <button
          onClick={(e) => { e.stopPropagation(); onMoveTopAndCopy() }}
          title={copied ? 'Copied' : 'Move to top and copy'}
          style={{
            background: 'none', border: 'none', cursor: 'pointer', padding: 0,
            color: copied ? 'var(--status-success, var(--text-secondary))' : 'var(--text-tertiary)',
            display: 'flex', alignItems: 'center', flexShrink: 0,
          }}
        >
          <ClipboardCopy size={12} />
        </button>
        <SourceBadge source={d.Source} />
        <span style={{ color: 'var(--text-tertiary)', fontSize: 10, flexShrink: 0, whiteSpace: 'nowrap' }}>
          {time}
        </span>
      </div>
      {expanded && (
        <div style={{ padding: '2px 8px 6px 28px', color: 'var(--text-tertiary)', fontSize: 11, lineHeight: 1.5 }}>
          {d.Message && <div style={{ color: 'var(--text-secondary)', marginBottom: 4 }}>{d.Message}</div>}
          {d.CallableId && <div>Callable: {d.CallableId}</div>}
          {d.TargetService && <div>Service: {d.TargetService}</div>}
          {d.ToolUseId && <div>ToolUseId: {d.ToolUseId}</div>}
          {d.AgentId && <div>Agent: {d.AgentId}</div>}
          {d.TurnId && <div>Turn: {d.TurnId}</div>}
          {d.StepId && <div>Step: {d.StepId}</div>}
          {d.Input && <RawDataBlock label="Input" raw={truncateField(d.Input) ?? ''} />}
          {d.Output && <RawDataBlock label="Output" raw={truncateField(d.Output) ?? ''} />}
          {d.Unit?.model && <div>Model: {d.Unit.model}</div>}
          {d.Unit?.provider && <div>Provider: {d.Unit.provider}</div>}
          {d.HttpStatus ? <div>HTTP: {d.HttpStatus}</div> : null}
          {d.RawData && <RawDataBlock raw={d.RawData} />}
        </div>
      )}
    </div>
  )
}

function SourceBadge({ source }: { source: string }) {
  return (
    <span style={{
      fontSize: 10, padding: '0 4px', borderRadius: 3,
      background: 'var(--bg-hover)', color: 'var(--text-tertiary)',
      flexShrink: 0, textTransform: 'capitalize',
    }}>
      {source}
    </span>
  )
}

function MessageBadge({ message }: { message: string }) {
  return (
    <span title={message} style={{
      fontSize: 11, padding: '0 4px', borderRadius: 3,
      background: 'var(--bg-subtle)', color: 'var(--text-secondary)',
      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
      flex: 1, minWidth: 0,
    }}>
      {message}
    </span>
  )
}

function RawDataBlock({ raw, label = 'Raw LLM output:' }: { raw: string, label?: string }) {
  let display = raw
  try {
    display = JSON.stringify(JSON.parse(raw), null, 2)
  } catch {}
  return (
    <div style={{ marginTop: 4 }}>
      <div style={{ color: 'var(--text-secondary)', marginBottom: 2 }}>{label}</div>
      <pre style={{
        margin: 0, padding: '4px 6px', fontSize: 10, lineHeight: 1.4,
        background: 'var(--bg-subtle)', borderRadius: 3,
        overflow: 'auto', maxWidth: '100%', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
      }}>
        {display}
      </pre>
    </div>
  )
}
