import React, { useState } from 'react'
import { Shield, Target, HelpCircle, ChevronUp, ListChecks, Star } from 'lucide-react'
import type { PendingInteraction } from '../model/pending-interaction'
import type {
  AskUserQuestionFrame,
  PermissionRequestFrame,
  GoalSubmitFrame,
  GoalCardSubmitFrame,
  PlanFrame,
} from '../model/frame-types'
import { useStepInteraction } from '../hooks/useStepInteraction'
import { useI18n } from '../../../i18n'
import { AskCustomInput } from './parts/AskCustomInput'
import './PeekInteractionBar.css'

interface PeekInteractionBarProps {
  interaction: PendingInteraction
  /** Expands the drawer so the user can answer with the full component. */
  onExpand: () => void
}

/** Above this count, option chips would wrap the compact bar too tall —
 *  fall back to expanding the drawer. */
const MAX_OPTION_CHIPS = 6

function truncate(text: string, max = 60): string {
  return text.length > max ? text.slice(0, max) + '…' : text
}

/**
 * Compact interaction bar shown in the collapsed drawer peek area when an
 * agent interaction (ask_user / permission / goal / plan) awaits the user.
 * Simple interactions are answered inline; complex ones offer an expand
 * affordance that opens the full component in the drawer stream.
 */
export const PeekInteractionBar: React.FC<PeekInteractionBarProps> = ({ interaction, onExpand }) => {
  switch (interaction.kind) {
    case 'ask_user':
      return <AskPeek frame={interaction.frame} onExpand={onExpand} />
    case 'permission_request':
      return <PermissionPeek frame={interaction.frame} onExpand={onExpand} />
    case 'goal_submit':
    case 'goal_card_submit':
      return <GoalPeek frame={interaction.frame} kind={interaction.kind} onExpand={onExpand} />
    case 'plan':
      return <PlanPeek frame={interaction.frame} onExpand={onExpand} />
  }
}

// ── Shared shell: title row (click expands) + control row ──

const PeekShell: React.FC<{
  icon: React.ReactNode
  title: string
  onExpand: () => void
  children?: React.ReactNode
}> = ({ icon, title, onExpand, children }) => {
  const { t } = useI18n()
  return (
  <div className="peek-interaction-bar" onClick={e => e.stopPropagation()}>
    <div
      className="peek-interaction-header"
      onClick={onExpand}
      role="button"
      aria-label={t('ai.peek.expandToAnswer')}
    >
      {icon}
      <span className="peek-interaction-title">{title}</span>
      <ChevronUp size={14} className="peek-interaction-chevron" />
    </div>
    {children && <div className="peek-interaction-controls">{children}</div>}
  </div>
  )
}

// ── ask_user ──

const AskPeek: React.FC<{ frame: AskUserQuestionFrame; onExpand: () => void }> = ({ frame, onExpand }) => {
  const { t } = useI18n()
  const { submitAskAnswer } = useStepInteraction(frame.requestId ?? '')
  const [answered, setAnswered] = useState(false)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [customInput, setCustomInput] = useState('')

  const question = frame.questions[0]
  if (answered || !question) return null

  const submit = (answer: string) => {
    setAnswered(true)
    if (frame.requestId) submitAskAnswer({ 0: answer })
  }

  const title = truncate(question.header || question.question || t('ai.step.question'))

  // Multi-question forms exceed the compact bar — expand to the full form.
  if (frame.questions.length > 1) {
    return (
      <PeekShell icon={<HelpCircle size={13} className="peek-interaction-icon" />} title={title} onExpand={onExpand}>
        <span className="peek-interaction-meta">1 / {frame.questions.length}</span>
      </PeekShell>
    )
  }

  const tooManyOptions = question.options.length > MAX_OPTION_CHIPS

  const toggleOption = (index: number) => {
    if (!question.multiSelect) {
      submit(question.options[index]!.label)
      return
    }
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })
  }

  const submitMulti = () => {
    if (selected.size === 0) return
    submit(Array.from(selected).map(i => question.options[i]!.label).join(', '))
  }

  return (
    <PeekShell icon={<HelpCircle size={13} className="peek-interaction-icon" />} title={title} onExpand={onExpand}>
      {!tooManyOptions && question.options.map((opt, i) => (
        <button
          key={i}
          type="button"
          className={`peek-interaction-chip${selected.has(i) ? ' selected' : ''}`}
          title={opt.label}
          onClick={() => toggleOption(i)}
        >
          {opt.recommended && <Star size={11} className="peek-interaction-chip-star" />}
          {truncate(opt.label, 24)}
        </button>
      ))}
      {question.multiSelect && !tooManyOptions && selected.size > 0 && (
        <button type="button" className="peek-interaction-btn primary" onClick={submitMulti}>
          {t('common.confirm')}
        </button>
      )}
      <AskCustomInput
        value={customInput}
        onChange={setCustomInput}
        onSubmit={() => { if (customInput.trim()) submit(customInput.trim()) }}
        title={question.header || t('ai.step.question')}
        className="peek-interaction-input"
      />
    </PeekShell>
  )
}

// ── permission_request ──

const PermissionPeek: React.FC<{ frame: PermissionRequestFrame; onExpand: () => void }> = ({ frame, onExpand }) => {
  const { t } = useI18n()
  const { submitPermission } = useStepInteraction(frame.requestId ?? '')
  const [answered, setAnswered] = useState(false)
  if (answered) return null

  const handle = (allowed: boolean) => {
    setAnswered(true)
    if (frame.requestId) submitPermission(allowed)
  }

  const primary = frame.toolCalls[0]?.callableId ?? frame.reason ?? ''
  const extra = frame.toolCalls.length > 1 ? ` +${frame.toolCalls.length - 1}` : ''

  return (
    <PeekShell icon={<Shield size={13} className="peek-interaction-icon" />} title={truncate(primary + extra)} onExpand={onExpand}>
      <button type="button" className="peek-interaction-btn" onClick={() => handle(false)}>{t('ai.permission.deny')}</button>
      <button type="button" className="peek-interaction-btn primary" onClick={() => handle(true)}>{t('ai.permission.allow')}</button>
    </PeekShell>
  )
}

// ── goal_submit / goal_card_submit ──

const GoalPeek: React.FC<{
  frame: GoalSubmitFrame | GoalCardSubmitFrame
  kind: 'goal_submit' | 'goal_card_submit'
  onExpand: () => void
}> = ({ frame, kind, onExpand }) => {
  const { t } = useI18n()
  const { submitGoalApproval, submitGoalCardApproval } = useStepInteraction(frame.requestId ?? '')
  const [answered, setAnswered] = useState(false)
  if (answered) return null

  const handle = (decision: 'approve' | 'reject') => {
    setAnswered(true)
    if (!frame.requestId) return
    if (kind === 'goal_submit') submitGoalApproval(decision)
    else submitGoalCardApproval(decision)
  }

  const title = truncate(
    frame.interpretedGoal
    ?? (kind === 'goal_submit' ? (frame as GoalSubmitFrame).condition : (frame as GoalCardSubmitFrame).cardId)
  )

  return (
    <PeekShell icon={<Target size={13} className="peek-interaction-icon" />} title={title} onExpand={onExpand}>
      <button type="button" className="peek-interaction-btn" onClick={() => handle('reject')}>
        {t('ai.goalSubmit.reject')}
      </button>
      <button type="button" className="peek-interaction-btn primary" onClick={() => handle('approve')}>
        {t('ai.goalSubmit.confirm')}
      </button>
    </PeekShell>
  )
}

// ── plan approval ──

const PlanPeek: React.FC<{ frame: PlanFrame; onExpand: () => void }> = ({ frame, onExpand }) => {
  const { t } = useI18n()
  const { submitPlanApproval } = useStepInteraction(frame.requestId ?? '')
  const [answered, setAnswered] = useState(false)
  if (answered) return null

  const handle = (decision: 'approve' | 'reject') => {
    setAnswered(true)
    if (frame.requestId) submitPlanApproval(decision)
  }

  const firstLine = frame.content.split('\n').find(l => l.trim()) ?? ''
  const title = truncate(firstLine.replace(/^#+\s*/, '') || t('ai.step.planApproval'))

  return (
    <PeekShell icon={<ListChecks size={13} className="peek-interaction-icon" />} title={title} onExpand={onExpand}>
      <button type="button" className="peek-interaction-btn" onClick={() => handle('reject')}>
        {t('ai.goalSubmit.reject')}
      </button>
      <button type="button" className="peek-interaction-btn primary" onClick={() => handle('approve')}>
        {t('common.confirm')}
      </button>
    </PeekShell>
  )
}
