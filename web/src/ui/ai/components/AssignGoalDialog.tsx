import React, { useEffect, useMemo, useRef, useState } from 'react'
import { Loader2, X, UserPlus, UserCheck, FileText, AlertTriangle, ChevronDown } from 'lucide-react'
import { useI18n } from '../../../i18n'
import type { MonoCardListItem } from '../../../domain/mono-types'
import type { AgentInfo } from '../hooks/agentInfoStore'
import { agentStatusClass } from '../hooks/agentInfoStore'
import { avatarHue } from '../lib/agent-avatar'
import { AgentAvatarContent } from './AgentAvatarContent'
import { ProviderIcon, iconKeyForUnit } from './providerIcons'
import { useBrowserOverlay } from '../browserOverlay'
import { useDropdownVerticalFit } from '../hooks/useDropdownVerticalFit'
import './AssignGoalDialog.css'

export type AssignGoalMode = 'new' | 'existing'

export interface AssignGoalInput {
  mode: AssignGoalMode
  displayName: string
  agentKind: string
  agentActorId: string
}

export const ASSIGN_AGENT_KINDS = [
  { kind: 'worker', labelKey: 'assignGoal.kindWorker' },
  { kind: 'coder', labelKey: 'assignGoal.kindCoder' },
] as const

export interface AssignGoalKindOption {
  kind: string
  label: string
}

interface AssignGoalDialogProps {
  open: boolean
  card: MonoCardListItem | null
  /** Agents belonging to the current project, pre-ordered like the sidebar. */
  agents: AgentInfo[]
  submitting: boolean
  error: string | null
  onClose: () => void
  onAssign: (input: AssignGoalInput) => Promise<void>
  /** Label overrides for reuse outside the assign-goal flow (workflow start). */
  title?: string
  confirmLabel?: string
  submittingLabel?: string
  /**
   * Kinds offered when creating a new agent. Defaults to ASSIGN_AGENT_KINDS
   * (the assign-goal flow spawns system workers). The workflow-start flow
   * passes the user-creatable kinds so system-managed kinds (worker,
   * reviewer) never appear.
   */
  kindOptions?: AssignGoalKindOption[]
}

/** Combined trigger/option label: DisplayName with the Title appended when the
 * agent carries a real title distinct from its name (HasTitle is false when
 * Title fell back to DisplayName, and identical text is deduped). */
export function agentComboLabel(agent: AgentInfo): { name: string; title?: string } {
  const name = agent.DisplayName || agent.Title || agent.Id
  const title = agent.HasTitle && agent.Title && agent.Title !== name ? agent.Title : undefined
  return { name, title }
}

/** Brand icon for the agent's mounted primary model, resolved from the first
 * candidate unit (provider alias → model pattern). */
function primaryModelBadge(agent: AgentInfo): { iconKey?: string; tooltip?: string } {
  const unit = agent.Primary?.Candidates?.map(c => c.Unit).find(u => u?.model || u?.provider)
  if (!unit) return {}
  return {
    iconKey: iconKeyForUnit(unit.provider, unit.model),
    tooltip: [unit.model, unit.provider].filter(Boolean).join(' · '),
  }
}

function AgentAvatarChip({ agent, size }: { agent: AgentInfo; size: number }) {
  return (
    <span
      className={`assign-goal-agent-avatar ${agentStatusClass(agent.Status)}`}
      style={{ '--avatar-hue': avatarHue(agent.Id) } as React.CSSProperties}
    >
      <AgentAvatarContent agent={agent} size={size} pauseSize={8} />
    </span>
  )
}

interface AgentSelectFieldProps {
  agents: AgentInfo[]
  value: string
  onChange: (actorId: string) => void
  disabled: boolean
}

/** Existing-agent picker: a rich dropdown (avatar + status + mounted-model
 * icon) replacing the old native <select>, which could not render them and
 * showed only the DisplayName. Options render in the caller-provided
 * sidebar order. */
function AgentSelectField({ agents, value, onChange, disabled }: AgentSelectFieldProps) {
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  useDropdownVerticalFit(open, menuRef, 'down')
  useBrowserOverlay(open)

  const selected = agents.find(a => a.ActorId === value) ?? null
  const selectedLabel = selected ? agentComboLabel(selected) : null
  const selectedModel = selected ? primaryModelBadge(selected) : null

  // Escape closes the menu without closing the enclosing dialog: the keydown
  // stops here instead of reaching the dialog's document-level listener.
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape' && open) {
      e.stopPropagation()
      setOpen(false)
    }
  }

  return (
    <div className="assign-goal-agent-picker" onKeyDown={handleKeyDown}>
      <button
        type="button"
        id="assign-goal-agent"
        className={`assign-goal-agent-trigger${open ? ' open' : ''}`}
        onClick={() => setOpen(v => !v)}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        {selected && selectedLabel ? (
          <>
            <AgentAvatarChip agent={selected} size={15} />
            <span className="assign-goal-agent-trigger-label">
              <span className="assign-goal-agent-trigger-name">{selectedLabel.name}</span>
              {selectedLabel.title ? (
                <span className="assign-goal-agent-trigger-title">{selectedLabel.title}</span>
              ) : null}
            </span>
            {selectedModel?.iconKey ? (
              <span className="assign-goal-agent-model" title={selectedModel.tooltip}>
                <ProviderIcon id={selectedModel.iconKey} size={13} />
              </span>
            ) : null}
          </>
        ) : (
          <span className="assign-goal-agent-trigger-name assign-goal-agent-trigger-placeholder">—</span>
        )}
        <ChevronDown size={14} className="assign-goal-agent-chevron" />
      </button>
      {open && (
        <>
          <div className="assign-goal-agent-backdrop" onClick={() => setOpen(false)} />
          <div className="assign-goal-agent-menu" ref={menuRef} role="listbox" aria-label="agent">
            {agents.map(a => {
              const label = agentComboLabel(a)
              const model = primaryModelBadge(a)
              return (
                <button
                  key={a.ActorId}
                  type="button"
                  role="option"
                  aria-selected={a.ActorId === value}
                  className={`assign-goal-agent-option${a.ActorId === value ? ' selected' : ''}`}
                  onClick={() => {
                    onChange(a.ActorId)
                    setOpen(false)
                  }}
                >
                  <AgentAvatarChip agent={a} size={17} />
                  <span className="assign-goal-agent-option-info">
                    <span className="assign-goal-agent-option-name-row">
                      <span className="assign-goal-agent-option-name">{label.name}</span>
                      {model.iconKey ? (
                        <span className="assign-goal-agent-model" title={model.tooltip}>
                          <ProviderIcon id={model.iconKey} size={12} />
                        </span>
                      ) : null}
                    </span>
                    <span className="assign-goal-agent-option-meta">
                      {label.title ? (
                        <span className="assign-goal-agent-option-title">{label.title}</span>
                      ) : null}
                      <span className={`assign-goal-agent-status ${agentStatusClass(a.Status)}`}>
                        {a.StatusLabel}
                      </span>
                    </span>
                  </span>
                </button>
              )
            })}
          </div>
        </>
      )}
    </div>
  )
}

export const AssignGoalDialog: React.FC<AssignGoalDialogProps> = ({
  open,
  card,
  agents,
  submitting,
  error,
  onClose,
  onAssign,
  title,
  confirmLabel,
  submittingLabel,
  kindOptions,
}) => {
  const { t } = useI18n()
  const kinds = useMemo<AssignGoalKindOption[]>(
    () => kindOptions ?? ASSIGN_AGENT_KINDS.map(k => ({ kind: k.kind, label: t(k.labelKey) })),
    [kindOptions, t],
  )
  const [mode, setMode] = useState<AssignGoalMode>('new')
  const [displayName, setDisplayName] = useState('')
  const [agentKind, setAgentKind] = useState('')
  const [agentActorId, setAgentActorId] = useState('')

  const projectAgents = useMemo(() => agents.filter(a => a.ActorId), [agents])

  // Fall back to the first offered kind when the current selection is not in
  // the list (e.g. kinds load after the dialog opened).
  const effectiveKind = kinds.some(k => k.kind === agentKind) ? agentKind : (kinds[0]?.kind ?? '')

  // Reset local state whenever the dialog opens for a (new) card.
  useEffect(() => {
    if (!open) return
    setMode('new')
    setDisplayName('')
    setAgentKind('')
    setAgentActorId(projectAgents[0]?.ActorId ?? '')
  }, [open, card?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!open) return
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onClose])

  // The dialog itself floats over the right-panel browser window, so it must
  // register with the browser overlay manager like every other modal.
  useBrowserOverlay(Boolean(open))

  if (!open || !card) return null

  const canSubmit = submitting === false && (
    mode === 'existing'
      ? agentActorId !== ''
      : displayName.trim() !== '' && effectiveKind !== ''
  )

  const handleSubmit = () => {
    if (!canSubmit) return
    void onAssign({
      mode,
      displayName: displayName.trim(),
      agentKind: effectiveKind,
      agentActorId: mode === 'existing' ? agentActorId : '',
    })
  }

  return (
    <div className="assign-goal-dialog-overlay">
      <div className="assign-goal-dialog" role="dialog" aria-modal="true">
        <div className="assign-goal-dialog-header">
          <h3 className="assign-goal-dialog-title">{title ?? t('assignGoal.title')}</h3>
          <button type="button" className="assign-goal-dialog-close" onClick={onClose} disabled={submitting} aria-label={t('common.close')}>
            <X size={14} />
          </button>
        </div>

        <div className="assign-goal-dialog-body">
          <div className="assign-goal-dialog-card">
            <FileText size={14} />
            <div className="assign-goal-dialog-card-meta">
              <span className="assign-goal-dialog-card-label">{t('assignGoal.card')}</span>
              <span className="assign-goal-dialog-card-id">{card.id}</span>
            </div>
          </div>

          <div className="assign-goal-dialog-mode">
            <button
              type="button"
              className={`assign-goal-dialog-mode-btn${mode === 'new' ? ' active' : ''}`}
              onClick={() => setMode('new')}
              disabled={submitting}
            >
              <UserPlus size={14} />
              <span>{t('assignGoal.modeNew')}</span>
            </button>
            <button
              type="button"
              className={`assign-goal-dialog-mode-btn${mode === 'existing' ? ' active' : ''}`}
              onClick={() => setMode('existing')}
              disabled={submitting}
            >
              <UserCheck size={14} />
              <span>{t('assignGoal.modeExisting')}</span>
            </button>
          </div>

          {mode === 'new' ? (
            <div className="assign-goal-dialog-field">
              <label className="assign-goal-dialog-label" htmlFor="assign-goal-name">{t('assignGoal.newAgentName')}</label>
              <input
                id="assign-goal-name"
                className="assign-goal-dialog-input"
                type="text"
                value={displayName}
                placeholder={t('assignGoal.newAgentNamePlaceholder')}
                onChange={e => setDisplayName(e.target.value)}
                disabled={submitting}
                autoFocus
              />
              <label className="assign-goal-dialog-label" htmlFor="assign-goal-kind">{t('assignGoal.agentKind')}</label>
              <select
                id="assign-goal-kind"
                className="assign-goal-dialog-select"
                value={effectiveKind}
                onChange={e => setAgentKind(e.target.value)}
                disabled={submitting}
              >
                {kinds.map(k => (
                  <option key={k.kind} value={k.kind}>{k.label}</option>
                ))}
              </select>
            </div>
          ) : (
            <div className="assign-goal-dialog-field">
              <label className="assign-goal-dialog-label" htmlFor="assign-goal-agent">{t('assignGoal.selectAgent')}</label>
              {projectAgents.length === 0 ? (
                <div className="assign-goal-dialog-empty">
                  <AlertTriangle size={13} />
                  <span>{t('assignGoal.noAgents')}</span>
                </div>
              ) : (
                <AgentSelectField
                  agents={projectAgents}
                  value={agentActorId}
                  onChange={setAgentActorId}
                  disabled={submitting}
                />
              )}
            </div>
          )}

          {error ? (
            <div className="assign-goal-dialog-error">{error}</div>
          ) : null}
        </div>

        <div className="assign-goal-dialog-footer">
          <button type="button" className="assign-goal-dialog-btn cancel" onClick={onClose} disabled={submitting}>
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className="assign-goal-dialog-btn confirm"
            onClick={handleSubmit}
            disabled={!canSubmit}
          >
            {submitting ? (
              <>
                <Loader2 size={14} className="assign-goal-dialog-spinner" />
                {submittingLabel ?? t('assignGoal.assigning')}
              </>
            ) : (
              confirmLabel ?? t('assignGoal.assign')
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
