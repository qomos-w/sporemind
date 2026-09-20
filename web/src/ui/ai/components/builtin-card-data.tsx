import React, { useState, useSyncExternalStore } from 'react'
import { PauseCircle, Pencil, Sparkles } from 'lucide-react'
import { builtinCardDataKind } from '../../../domain/builtin-card-registry'
import { useI18n } from '../../../i18n'
import { monoStore } from '../../panels/mono-store'
import { useMonoStore } from '../hooks/useMonoStore'
import { subscribeAgentListStore, getAgentListItems } from '../hooks/agentListStore'
import { useBrowserOverlay } from '../browserOverlay'
import { ScheduleCronEditor } from './ScheduleCronEditor'
import { cronToDraft, draftToCron, resolveAgentStateAction } from './scheduledTasks'
import type { AgentActionItem, AgentStateAction, ScheduleDraft } from './scheduledTasks'

type Props = {
  tags: string[]
  type?: string
  data: Record<string, unknown>
  /** Card body markdown when the panel is rendered from a full card; used as
   *  the prompt-type scheduler's prompt preview. */
  body?: string
  editing?: boolean
  onChange?: (data: Record<string, unknown>) => void
  /** Live-update the card body while editing a prompt-type scheduler. When
   *  absent the prompt branch falls back to a read-only preview + hint. */
  onChangeBody?: (body: string) => void
  cardId?: string
  onTrigger?: (cardId: string) => Promise<void>
  /** Zoom-to-fit a workflow map id (used by the scheduler's current_instance
   *  row and clickable instance/scheduler provenance rows). */
  onNavigateToMap?: (mapId: string) => void
  /** Update the cron expression for a scheduler card (non-editing mode only). */
  onUpdateScheduleCron?: (cardId: string, cron: string) => Promise<boolean>
}

function parseReviewEvidence(value: unknown): string[] {
  if (typeof value !== 'string' || value.trim() === '') return []
  try {
    const parsed = JSON.parse(value)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string' && item.length > 0)
  } catch {
    return []
  }
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : value == null ? '' : String(value)
}

/**
 * Parse the agent-action rows for the scheduler editor, preserving rows whose
 * target is still empty. agentActionsOf (the shared reader) drops empty-target
 * rows because they are not fireable refs, but the editor must keep them so the
 * user can fill a target in. Falls back to the legacy single agent_action +
 * target_agent pair so pre-unification cards stay editable.
 */
function editableAgentActions(data: Record<string, unknown>): AgentActionItem[] {
  const raw = data.agent_actions
  let list: unknown
  if (typeof raw === 'string' && raw.trim() !== '') {
    try { list = JSON.parse(raw) } catch { list = undefined }
  } else if (Array.isArray(raw)) {
    list = raw
  }
  if (Array.isArray(list)) {
    const rows: AgentActionItem[] = []
    for (const item of list) {
      if (!item || typeof item !== 'object') continue
      const o = item as Record<string, unknown>
      const action = o.action ?? o.agent_action
      if (action !== 'pause' && action !== 'resume') continue
      const target = o.targetAgent ?? o.target ?? o.target_agent
      rows.push({ action: action as AgentStateAction, targetAgent: typeof target === 'string' ? target : '' })
    }
    if (rows.length > 0) return rows
  }
  const legacy = resolveAgentStateAction(
    typeof data.agent_action === 'string' ? data.agent_action : undefined,
    typeof data.schedule_type === 'string' ? data.schedule_type : undefined,
  )
  if (legacy) return [{ action: legacy, targetAgent: typeof data.target_agent === 'string' ? data.target_agent : '' }]
  return []
}

/**
 * Derive the currently-active workflow map card id for the given project from
 * the agent list store. "Current workflow" = the ActiveWorkflowMapCardId of any
 * agent working in this project. Returns undefined when no agent has an active
 * workflow map. Reactive via useSyncExternalStore.
 */
function useCurrentWorkflowMapId(projectId: string | null): string | undefined {
  const value = useSyncExternalStore(
    subscribeAgentListStore,
    () => {
      const items = getAgentListItems()
      const found = items.find(a => a.ProjectId === projectId && a.Mode?.ActiveWorkflowMapCardId)
      return found?.Mode?.ActiveWorkflowMapCardId ?? ''
    },
    () => '',
  )
  return value || undefined
}

/**
 * Workflow Template selector for the scheduler editor. Only mounted while
 * editing a scheduler card, so its hooks (template fetch, agent-list
 * subscription) never run for other card kinds or in view mode.
 *
 * - Dropdown lists existing templates (wiki_list_templates); selecting one
 *   writes the template id into data.workflow_template.
 * - "Bind from current workflow" snapshots the project's active workflow map
 *   via wiki_automation_bind and attaches the resulting template.
 */
const SchedulerTemplateSelector: React.FC<{
  cardId?: string
  data: Record<string, unknown>
  onChange?: (data: Record<string, unknown>) => void
}> = ({ cardId, data, onChange }) => {
  const { t } = useI18n()
  const projectId = useMonoStore(s => s.projectId)
  const currentMapId = useCurrentWorkflowMapId(projectId)
  const [templates, setTemplates] = React.useState<{ id: string }[]>([])
  const [binding, setBinding] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)

  React.useEffect(() => {
    let cancelled = false
    setError(null)
    monoStore.listTemplates()
      .then(list => { if (!cancelled) setTemplates(list.map(tpl => ({ id: tpl.id }))) })
      .catch(err => { if (!cancelled) setError(err instanceof Error ? err.message : String(err)) })
    return () => { cancelled = true }
  }, [])

  const set = (key: string, value: string) => onChange?.({ ...data, [key]: value })

  const handleBind = async () => {
    if (!cardId || !currentMapId) return
    setBinding(true)
    setError(null)
    try {
      const templateId = await monoStore.automationBind(currentMapId, cardId)
      if (templateId) onChange?.({ ...data, workflow_template: templateId })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBinding(false)
    }
  }

  return <>
    <label>{t('builtinCard.scheduler.workflowTemplate')}
      <select value={text(data.workflow_template)} onChange={e => set('workflow_template', e.target.value)}>
        <option value="">{t('builtinCard.scheduler.noTemplate')}</option>
        {templates.map(tpl => <option key={tpl.id} value={tpl.id}>{tpl.id}</option>)}
      </select>
    </label>
    {currentMapId && cardId && (
      <button
        type="button"
        className="mono-card-builtin-data-bind-btn"
        onClick={handleBind}
        disabled={binding}
        title={currentMapId}
      >
        {binding ? t('builtinCard.scheduler.binding') : t('builtinCard.scheduler.bindFromWorkflow')}
      </button>
    )}
    {error && <span className="mono-card-builtin-data-error">{error}</span>}
  </>
}

export const BuiltinCardDataPanel: React.FC<Props> = ({ tags, type, data, body, editing = false, onChange, onChangeBody, cardId, onTrigger, onNavigateToMap, onUpdateScheduleCron }) => {
  const [triggering, setTriggering] = React.useState(false)
  const { t } = useI18n()
  const [editingSchedule, setEditingSchedule] = useState(false)
  const [draft, setDraft] = useState<ScheduleDraft | null>(null)
  const [savingSchedule, setSavingSchedule] = useState(false)
  const [scheduleError, setScheduleError] = useState('')
  useBrowserOverlay(editingSchedule)
  const trigger = async () => {
    if (!cardId || !onTrigger) return
    setTriggering(true)
    try { await onTrigger(cardId) } finally { setTriggering(false) }
  }
  const kind = builtinCardDataKind(tags, type)
  if (!kind) return null

  const set = (key: string, value: string) => onChange?.({ ...data, [key]: value })
  const schedule = data.schedule && typeof data.schedule === 'object' ? data.schedule as Record<string, unknown> : {}
  const setSchedule = (key: string, value: string) => onChange?.({ ...data, schedule: { ...schedule, [key]: value } })
  const agentActions = editableAgentActions(data)
  const legacyAgentAction = resolveAgentStateAction(text(data.agent_action) || undefined, text(data.schedule_type) || undefined)
  // Unified model: an agent-action card is a schedule_type=task card carrying a
  // data.agent_actions list (a legacy agent_action + target_agent pair is
  // normalized into the same list). The type radio mirrors that; the task radio
  // is everything else. Switching type rewrites data through writeAgentActions
  // so the card always satisfies the backend's unified scheduler validation.
  const isAgentAction = agentActions.length > 0 || legacyAgentAction !== undefined
  const isTask = !isAgentAction
  const writeAgentActions = (next: AgentActionItem[]) => onChange?.({
    ...data,
    schedule_type: 'task',
    agent_action: '',
    target_agent: '',
    agent_actions: next.length
      ? JSON.stringify(next.map(a => ({ action: a.action, target: a.targetAgent })))
      : '',
  })
  const promptPreview = (() => {
    const collapsed = (body ?? '').replace(/\s+/g, ' ').trim()
    return collapsed.length > 200 ? `${collapsed.slice(0, 200)}…` : collapsed
  })()

  if (kind === 'scheduler') {
    return <section className="mono-card-builtin-data" data-builtin-card-kind="scheduler">
      <strong>Scheduler</strong>
      {!editing && cardId && onTrigger && <button type="button" onClick={trigger} disabled={triggering}>{triggering ? 'Running…' : 'Run now'}</button>}
      {!editing && cardId && onUpdateScheduleCron && !editingSchedule && (
        <button type="button" className="scheduled-edit-schedule" onClick={() => {
          setDraft(cronToDraft(text(schedule.cron || schedule.expression), text(schedule.expression)))
          setScheduleError('')
          setEditingSchedule(true)
        }}>
          <Pencil size={13} /> {t('scheduled.edit.button')}
        </button>
      )}
      {editing && (
        <fieldset className="mono-card-builtin-data-type">
          <legend>{t('builtinCard.scheduler.scheduleType')}</legend>
          <label className={isTask ? 'active' : ''}>
            <input type="radio" name="schedule_type" value="task" checked={isTask} onChange={() => writeAgentActions([])} />
            <Sparkles size={13} /> {t('builtinCard.scheduler.typeTask')}
          </label>
          <label className={isAgentAction ? 'active' : ''}>
            <input type="radio" name="schedule_type" value="agent_action" checked={isAgentAction} onChange={() => writeAgentActions(agentActions.length > 0 ? agentActions : [{ action: 'pause', targetAgent: '' }])} />
            <PauseCircle size={13} /> {t('builtinCard.scheduler.typeAgentAction')}
          </label>
        </fieldset>
      )}
      {editing ? <>
        <label>Expression<input value={text(schedule.cron || schedule.expression)} onChange={e => setSchedule('cron', e.target.value)} /></label>
        {isAgentAction && <>
          {agentActions.map((action, index) => (
            <div key={index} className="mono-card-builtin-data-agent-action">
              <label>{t('builtinCard.scheduler.agentAction')}
                <select
                  value={action.action}
                  onChange={e => writeAgentActions(agentActions.map((a, i) => i === index ? { ...a, action: e.target.value as AgentStateAction } : a))}
                >
                  <option value="pause">{t('builtinCard.scheduler.actionPause')}</option>
                  <option value="resume">{t('builtinCard.scheduler.actionResume')}</option>
                </select>
              </label>
              <label>{t('builtinCard.scheduler.targetAgent')}
                <input
                  value={action.targetAgent}
                  placeholder="agent:<name>"
                  onChange={e => writeAgentActions(agentActions.map((a, i) => i === index ? { ...a, targetAgent: e.target.value } : a))}
                />
              </label>
              <button
                type="button"
                data-guide-id={`schedule-action-remove-${index}`}
                onClick={() => writeAgentActions(agentActions.filter((_, i) => i !== index))}
              >
                -
              </button>
            </div>
          ))}
          <button
            type="button"
            data-guide-id="schedule-action-add"
            onClick={() => writeAgentActions([...agentActions, { action: 'pause', targetAgent: '' }])}
          >
            + {t('scheduled.agentAction.add')}
          </button>
        </>}
        <label>Reviewer<input value={text(data.reviewer)} onChange={e => set('reviewer', e.target.value)} /></label>
        {isTask ? (
          <>
            <SchedulerTemplateSelector cardId={cardId} data={data} onChange={onChange} />
            {onChangeBody ? (
              <div className="mono-card-builtin-data-prompt">
                <span className="mono-card-builtin-data-prompt-label">{t('builtinCard.scheduler.promptPreview')}</span>
                <textarea
                  className="mono-card-builtin-data-prompt-input"
                  value={body ?? ''}
                  onChange={e => onChangeBody(e.target.value)}
                />
                <span className="mono-card-builtin-data-prompt-hint">{t('builtinCard.scheduler.promptOptionalHint')}</span>
              </div>
            ) : (
              <div className="mono-card-builtin-data-prompt">
                <span className="mono-card-builtin-data-prompt-label">{t('builtinCard.scheduler.promptPreview')}</span>
                <p className="mono-card-builtin-data-prompt-body">{promptPreview || t('builtinCard.scheduler.promptEmpty')}</p>
                <span className="mono-card-builtin-data-prompt-hint">{t('builtinCard.scheduler.promptEditHint')}</span>
              </div>
            )}
          </>
        ) : (
          <span className="mono-card-builtin-data-hint">{t('builtinCard.scheduler.agentActionHint')}</span>
        )}
      </> : <dl>
        <dt>Schedule</dt><dd>{text(schedule.cron || schedule.expression) || 'Not configured'}</dd>
        <dt>{t('builtinCard.scheduler.scheduleType')}</dt><dd>{isAgentAction ? t('builtinCard.scheduler.typeAgentAction') : t('builtinCard.scheduler.typeTask')}</dd>
        {isAgentAction ? (
          <>
            <dt>{t('builtinCard.scheduler.agentAction')}</dt>
            <dd>{agentActions.length === 0
              ? 'Not configured'
              : agentActions.map((a, i) => (
                <div key={i}>
                  {a.action === 'resume' ? t('builtinCard.scheduler.actionResume') : t('builtinCard.scheduler.actionPause')}
                  {' → '}
                  {a.targetAgent || 'Not configured'}
                </div>
              ))}</dd>
          </>
        ) : (
          <>
            <dt>{t('scheduled.section.executor')}</dt>
            <dd>{text(data.agent_kind) ? t('scheduled.agent.newKind', { kind: text(data.agent_kind) }) : (text(data.bound_agent) || 'Not configured')}</dd>
            <dt>{t('builtinCard.scheduler.taskMode')}</dt>
            <dd>{text(data.workflow_template) ? t('scheduled.taskMode.template') : t('scheduled.taskMode.prompt')}</dd>
            <dt>{t('builtinCard.scheduler.workflowTemplate')}</dt><dd>{text(data.workflow_template) || t('builtinCard.scheduler.noTemplate')}</dd>
          </>
        )}
        <dt>Reviewer</dt><dd>{text(data.reviewer) || 'None'}</dd>
        <dt>Run status</dt><dd>{text(data.run_status) || 'idle'}</dd>
        {text(data.current_instance) && (
          <>
            <dt>{t('builtinCard.scheduler.currentInstance')}</dt>
            <dd>
              {onNavigateToMap ? (
                <a
                  role="link"
                  tabIndex={0}
                  className="mono-card-builtin-data-navigate"
                  title={text(data.current_instance)}
                  onClick={e => { e.preventDefault(); onNavigateToMap(text(data.current_instance)) }}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onNavigateToMap(text(data.current_instance)) } }}
                >
                  {text(data.current_instance)}
                </a>
              ) : (
                text(data.current_instance)
              )}
            </dd>
          </>
        )}
      </dl>}
      {!editing && editingSchedule && draft && (
        <ScheduleCronEditor
          draft={draft}
          onDraftChange={setDraft}
          onApply={async () => {
            if (!cardId || !onUpdateScheduleCron) return
            const newCron = draftToCron(draft)
            if (!newCron) { setScheduleError(t('scheduled.edit.invalid')); return }
            setSavingSchedule(true)
            setScheduleError('')
            try {
              const ok = await onUpdateScheduleCron(cardId, newCron)
              if (!ok) setScheduleError(t('scheduled.edit.saveFailed'))
              else setEditingSchedule(false)
            } finally {
              setSavingSchedule(false)
            }
          }}
          onCancel={() => { setEditingSchedule(false); setDraft(null); setScheduleError('') }}
          saving={savingSchedule}
          error={scheduleError}
          originalCron={text(schedule.cron || schedule.expression)}
        />
      )}
    </section>
  }

  if (kind === 'task') {
    const evidence = parseReviewEvidence(data.review_evidence)
    if (evidence.length === 0) return null
    return <section className="mono-card-builtin-data" data-builtin-card-kind="task">
      <strong>{t('builtinCard.task.reviewEvidence')}</strong>
      <ul className="mono-card-builtin-task-evidence">
        {evidence.map(path => (
          <li key={path} className="mono-card-builtin-task-evidence-item" title={path}>
            <span className="mono-card-builtin-task-evidence-path">{path}</span>
          </li>
        ))}
      </ul>
    </section>
  }

  return null
}
