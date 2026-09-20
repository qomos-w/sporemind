import { useState, useEffect, useCallback } from 'react'
import { CheckCircle2, XCircle, Trash2, Copy, ClipboardCopy } from 'lucide-react'
import * as oracle from '../../gen-clients/oracle/client'
import type { Diagnostic } from '../../gen-clients/system/types'
import { client } from '../../application/generated-client'
import { useI18n } from '../../i18n/provider'

/** Diagnostics with this Source are bypass (fast model) permission approvals;
 * the backend reports both outcomes (approve → info, deny → warning). */
export const PERMISSION_BYPASS_SOURCE = 'permission_bypass'

function isBypassApproval(d: Diagnostic): boolean {
  return d.Source === PERMISSION_BYPASS_SOURCE
}

function isApproved(d: Diagnostic): boolean {
  return d.Severity === 'info'
}

// ── Helpers ──

function formatApproval(d: Diagnostic): string {
  const lines: string[] = [`[${isApproved(d) ? 'approved' : 'denied'}] ${d.CallableId || 'unknown'}`]
  if (d.Message) lines.push(`Message: ${d.Message}`)
  if (d.Timestamp) lines.push(`Timestamp: ${d.Timestamp}`)
  if (d.CallableId) lines.push(`Callable: ${d.CallableId}`)
  if (d.AgentId) lines.push(`Agent: ${d.AgentId}`)
  if (d.TurnId) lines.push(`Turn: ${d.TurnId}`)
  if (d.Input) lines.push(`Input: ${d.Input}`)
  if (d.Unit?.model) lines.push(`Model: ${d.Unit.model}`)
  if (d.Unit?.provider) lines.push(`Provider: ${d.Unit.provider}`)
  return lines.join('\n')
}

const FIELD_DISPLAY_CAP = 400

function truncateField(s: string | undefined, truncatedMarker: string): string | undefined {
  if (!s) return s
  if (s.length <= FIELD_DISPLAY_CAP) return s
  return s.slice(0, FIELD_DISPLAY_CAP) + '… ' + truncatedMarker
}

// ── Panel ──

export function ApprovalAuditPanel() {
  const { t } = useI18n()
  const [approvals, setApprovals] = useState<Diagnostic[]>([])
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [decisionFilter, setDecisionFilter] = useState<'all' | 'approved' | 'denied'>('all')
  const [copiedAll, setCopiedAll] = useState(false)
  const [copiedId, setCopiedId] = useState<string | null>(null)

  // Load history on mount; only bypass approval records belong here.
  useEffect(() => {
    let cancelled = false
    oracle.listDiagnostics(client, {}).then(resp => {
      if (!cancelled) setApprovals((resp.Items ?? []).filter(isBypassApproval))
    }).catch(() => {})
    return () => { cancelled = true }
  }, [])

  // Subscribe to real-time diagnostics; ignore non-approval diagnostics.
  useEffect(() => {
    const off = oracle.OnDiagnostic(client, (diag: Diagnostic) => {
      if (!isBypassApproval(diag)) return
      setApprovals(prev => [diag, ...prev].slice(0, 200))
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
    const items = decisionFilter === 'all'
      ? approvals
      : approvals.filter(d => (decisionFilter === 'approved') === isApproved(d))
    if (items.length === 0) return
    const text = items.map(formatApproval).join('\n\n---\n\n')
    navigator.clipboard?.writeText(text).then(() => {
      setCopiedAll(true)
      setTimeout(() => setCopiedAll(false), 1200)
    }).catch(() => {})
  }, [approvals, decisionFilter])

  const moveToTopAndCopy = useCallback((id: string) => {
    setApprovals(prev => {
      const idx = prev.findIndex(d => d.Id === id)
      if (idx < 0) return prev
      const item = prev[idx]
      if (!item) return prev
      navigator.clipboard?.writeText(formatApproval(item)).then(() => {
        setCopiedId(id)
        setTimeout(() => setCopiedId(null), 1200)
      }).catch(() => {})
      return [item, ...prev.slice(0, idx), ...prev.slice(idx + 1)]
    })
  }, [])

  const filtered = decisionFilter === 'all'
    ? approvals
    : approvals.filter(d => (decisionFilter === 'approved') === isApproved(d))

  const counts = { approved: 0, denied: 0 }
  for (const d of approvals) {
    if (isApproved(d)) counts.approved++
    else counts.denied++
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', flex: 1, minWidth: 0, minHeight: 0, height: '100%', fontSize: 12 }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '4px 8px',
        borderBottom: '1px solid var(--border-default)', flexShrink: 0,
      }}>
        <FilterButton label={t('approvalAudit.filter.all')} count={approvals.length}
          active={decisionFilter === 'all'} onClick={() => setDecisionFilter('all')} />
        <FilterButton label={t('approvalAudit.filter.approved')} count={counts.approved} color="var(--status-success, var(--text-secondary))"
          active={decisionFilter === 'approved'} onClick={() => setDecisionFilter('approved')} />
        <FilterButton label={t('approvalAudit.filter.denied')} count={counts.denied} color="var(--status-failed)"
          active={decisionFilter === 'denied'} onClick={() => setDecisionFilter('denied')} />
        <div style={{ flex: 1 }} />
        <button onClick={handleCopyAll} title={copiedAll ? t('approvalAudit.copied') : t('approvalAudit.copyAll')}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: copiedAll ? 'var(--status-success, var(--text-secondary))' : 'var(--text-tertiary)', padding: 2 }}>
          <Copy size={14} />
        </button>
        <button onClick={() => setApprovals([])} title={t('approvalAudit.clearAll')}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text-tertiary)', padding: 2 }}>
          <Trash2 size={14} />
        </button>
      </div>

      {/* List */}
      <div style={{ flex: 1, overflow: 'auto', paddingBottom: 'calc(var(--composer-card-top, var(--composer-frame-h, 120px)) + 16px)' }}>
        {filtered.length === 0 && (
          <div style={{ padding: 16, color: 'var(--text-tertiary)', textAlign: 'center' }}>
            {t('approvalAudit.empty')}
          </div>
        )}
        {filtered.map(d => (
          <ApprovalRow key={d.Id} record={d}
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

function FilterButton({ label, count, color, active, onClick }: {
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

function ApprovalRow({ record: d, expanded, copied, onToggle, onMoveTopAndCopy }: {
  record: Diagnostic, expanded: boolean, copied: boolean, onToggle: () => void, onMoveTopAndCopy: () => void
}) {
  const { t } = useI18n()
  const approved = isApproved(d)
  const Icon = approved ? CheckCircle2 : XCircle
  const color = approved ? 'var(--status-success, var(--text-secondary))' : 'var(--status-failed)'
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
          <span style={{ color, flexShrink: 0 }}>{approved ? t('approvalAudit.status.approved') : t('approvalAudit.status.denied')}</span>
          <span style={{ color: 'var(--text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flexShrink: 1 }}>
            {d.CallableId || t('approvalAudit.unknownCallable')}
          </span>
          {d.Message && (
            <span title={d.Message} style={{
              fontSize: 11, padding: '0 4px', borderRadius: 3,
              background: 'var(--bg-subtle)', color: 'var(--text-secondary)',
              overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              flex: 1, minWidth: 0,
            }}>
              {d.Message}
            </span>
          )}
        </div>
        <button
          onClick={(e) => { e.stopPropagation(); onMoveTopAndCopy() }}
          title={copied ? t('approvalAudit.copied') : t('approvalAudit.moveTopAndCopy')}
          style={{
            background: 'none', border: 'none', cursor: 'pointer', padding: 0,
            color: copied ? 'var(--status-success, var(--text-secondary))' : 'var(--text-tertiary)',
            display: 'flex', alignItems: 'center', flexShrink: 0,
          }}
        >
          <ClipboardCopy size={12} />
        </button>
        <span style={{ color: 'var(--text-tertiary)', fontSize: 10, flexShrink: 0, whiteSpace: 'nowrap' }}>
          {time}
        </span>
      </div>
      {expanded && (
        <div style={{ padding: '2px 8px 6px 28px', color: 'var(--text-tertiary)', fontSize: 11, lineHeight: 1.5 }}>
          {d.Message && <div style={{ color: 'var(--text-secondary)', marginBottom: 4 }}>{d.Message}</div>}
          {d.CallableId && <div>{t('approvalAudit.field.callable')}: {d.CallableId}</div>}
          {d.AgentId && <div>{t('approvalAudit.field.agent')}: {d.AgentId}</div>}
          {d.TurnId && <div>{t('approvalAudit.field.turn')}: {d.TurnId}</div>}
          {d.Input && (
            <div style={{ marginTop: 4 }}>
              <div style={{ color: 'var(--text-secondary)', marginBottom: 2 }}>{t('approvalAudit.field.input')}</div>
              <pre style={{
                margin: 0, padding: '4px 6px', fontSize: 10, lineHeight: 1.4,
                background: 'var(--bg-subtle)', borderRadius: 3,
                overflow: 'auto', maxWidth: '100%', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
              }}>
                {truncateField(d.Input, t('approvalAudit.truncated'))}
              </pre>
            </div>
          )}
          {d.Unit?.model && <div>{t('approvalAudit.field.model')}: {d.Unit.model}</div>}
          {d.Unit?.provider && <div>{t('approvalAudit.field.provider')}: {d.Unit.provider}</div>}
        </div>
      )}
    </div>
  )
}
