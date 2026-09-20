import React, { useState, useMemo, useContext, useEffect } from 'react'
import { ListChecks, Copy, Pencil, Check, X, Target, GitBranch } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { PlanFrame, TimelineConnectorMode, TaskEntry } from '../../model/frame-types.ts'
import { useStepInteraction } from '../../hooks/useStepInteraction'
import { TimelineStep } from '../timeline'
import { makeRehypePlugins, useFileReference, resolveFileReference } from './file-reference.tsx'
import { useI18n } from '../../../../i18n'
import { useViewportMode } from '../../../../application/useViewportMode'
import { AIShellContext } from '../../context/AIShellContext'
import { MobileCardComposer } from '../MobileCardComposer'
import { ConfirmDialog } from '../../../components/ConfirmDialog'

interface PlanBlockProps {
  frame: PlanFrame
  connectorMode?: TimelineConnectorMode
}

// Edit drafts and reject feedback live outside the component: a plan card can
// be unmounted mid-interaction (stream virtualization boundaries, view
// switches), and component-local state would silently destroy typed input.
interface PlanDraftEntry {
  draft?: string
  feedback?: string
  editing?: boolean
  rejecting?: boolean
}
const planDrafts = new Map<string, PlanDraftEntry>()

function persistPlanDraft(key: string, patch: PlanDraftEntry): void {
  planDrafts.set(key, { ...planDrafts.get(key), ...patch })
}

function statusClass(status: TaskEntry['status']): string {
  switch (status) {
    case 'completed': return 'task--completed'
    case 'in_progress': return 'task--in_progress'
    default: return 'task--pending'
  }
}

export const PlanBlock: React.FC<PlanBlockProps> = ({ frame, connectorMode = 'none' }) => {
  const { t } = useI18n()
  const mode = useViewportMode()
  const isMobile = mode === 'mobile'
  const { submitPlanApproval } = useStepInteraction(frame.requestId ?? '')
  const draftKey = frame.requestId ?? frame.id
  const saved = planDrafts.get(draftKey)
  const [editing, setEditingState] = useState(saved?.editing ?? false)
  const [draft, setDraftState] = useState(saved?.draft ?? frame.content)
  const [rejecting, setRejectingState] = useState(saved?.rejecting ?? false)
  const [feedback, setFeedbackState] = useState(saved?.feedback ?? '')
  const [editComposerOpen, setEditComposerOpen] = useState(false)
  const [rejectComposerOpen, setRejectComposerOpen] = useState(false)
  const [workflowConfirmOpen, setWorkflowConfirmOpen] = useState(false)

  const setEditing = (v: boolean) => { setEditingState(v); persistPlanDraft(draftKey, { editing: v }) }
  const setDraft = (v: string) => { setDraftState(v); persistPlanDraft(draftKey, { draft: v }) }
  const setRejecting = (v: boolean) => { setRejectingState(v); persistPlanDraft(draftKey, { rejecting: v }) }
  const setFeedback = (v: string) => { setFeedbackState(v); persistPlanDraft(draftKey, { feedback: v }) }

  useEffect(() => {
    if (frame.approvalStatus !== 'pending') planDrafts.delete(draftKey)
  }, [frame.approvalStatus, draftKey])
  const isPending = frame.approvalStatus === 'pending'
  const isCancelled = frame.approvalStatus === 'cancelled'
  const fileRefCtx = useFileReference()
  const aiShellCtx = useContext(AIShellContext)

  const rehypePlugins = useMemo(() => makeRehypePlugins({
    projectId: fileRefCtx?.projectId ?? null,
    projectRoot: fileRefCtx?.projectRoot ?? null,
  }), [fileRefCtx?.projectId, fileRefCtx?.projectRoot])

  const handleBodyClick = async (e: React.MouseEvent) => {
    const target = e.target as HTMLElement
    const fileRef = target.closest('.ai-file-ref') as HTMLElement | null
    if (!fileRef) return
    const filePath = fileRef.dataset.aiFilePath
    if (!filePath) return

    e.preventDefault()
    e.stopPropagation()

    const lineStr = fileRef.dataset.aiFileLine
    const line = lineStr ? parseInt(lineStr, 10) : undefined
    const lineEndStr = fileRef.dataset.aiFileLineEnd
    const lineEnd = lineEndStr ? parseInt(lineEndStr, 10) : undefined

    if (aiShellCtx?.onOpenFile) {
      const ref = {
        raw: filePath,
        path: filePath,
        ext: filePath.split('.').pop()?.toLowerCase() ?? '',
        line,
        lineEnd,
      }
      const resolved = await resolveFileReference(ref, fileRefCtx?.projectRoot ?? null)
      if (!resolved) return
      aiShellCtx.onOpenFile(resolved.filePath, undefined, resolved.line ?? line, resolved.lineEnd ?? lineEnd)
    }
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(frame.content)
    } catch {
      // ignore
    }
  }

  const handleEditApprove = () => {
    setEditing(false)
    setEditComposerOpen(false)
    if (frame.requestId) submitPlanApproval('edit', draft)
  }

  const handleReject = () => {
    setRejecting(false)
    setRejectComposerOpen(false)
    if (frame.requestId) submitPlanApproval('reject', undefined, feedback.trim() || undefined)
  }

  const openEditComposer = () => {
    setEditing(true)
    if (isMobile) setEditComposerOpen(true)
  }

  const closeEditComposer = () => {
    setEditing(false)
    setEditComposerOpen(false)
    setDraft(frame.content)
  }

  const openRejectComposer = () => {
    setRejecting(true)
    if (isMobile) setRejectComposerOpen(true)
  }

  const closeRejectComposer = () => {
    setRejecting(false)
    setRejectComposerOpen(false)
    setFeedback('')
  }

  const handleWorkflowConfirm = () => {
    setWorkflowConfirmOpen(false)
    if (frame.requestId) submitPlanApproval('start_workflow')
  }

  const label = isPending ? t('ai.step.planApproval') : isCancelled ? t('ai.step.planExpired') : t('ai.step.plan')

  return (
    <TimelineStep
      key={frame.id}
      slotId={frame.id}
      status={frame.status}
      icon={<ListChecks size={12} className="ai-step-icon" />}
      label={<span className="ai-step-label">{label}</span>}
      connectorMode={connectorMode}
      expandable
      expansionMode={isPending ? 'open' : 'manual'}
    >
      <div className="ai-plan-content">
        <div className="ai-plan-header">
          <span className="ai-plan-status">
            {frame.approvalStatus === 'approved' && <><Check size={12} /> Approved</>}
            {frame.approvalStatus === 'rejected' && <><X size={12} /> Rejected</>}
            {isCancelled && <><X size={12} /> Expired</>}
          </span>
          <div className="ai-plan-actions-row">
            <button className="ai-plan-action" type="button" onClick={handleCopy} title="Copy plan">
              <Copy size={12} />
              <span>Copy</span>
            </button>
            {isPending && !editing && !rejecting && frame.editable && frame.requestId && (
              <button className="ai-plan-action" type="button" onClick={openEditComposer} title="Edit plan">
                <Pencil size={12} />
                <span>Edit</span>
              </button>
            )}
          </div>
        </div>

        {editing && !isMobile ? (
          <textarea
            className="ai-plan-editor"
            rows={12}
            value={draft}
            onChange={e => setDraft(e.target.value)}
          />
        ) : (
          <div className="ai-plan-body ai-text-content" onClick={handleBodyClick}>
            <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={rehypePlugins}>{frame.content}</Markdown>
          </div>
        )}
        <MobileCardComposer
          open={editComposerOpen}
          value={draft}
          onChange={setDraft}
          onSubmit={handleEditApprove}
          onClose={closeEditComposer}
          title={t('ai.step.planApproval')}
          placeholder={t('plan.placeholder.changePrompt')}
          submitLabel={t('common.save')}
          multiline={true}
        />

        {frame.tasks && frame.tasks.length > 0 && (
          <ul className="ai-plan-tasks">
            {frame.tasks.map(t => (
              <li key={t.id} className={`ai-plan-task ${statusClass(t.status)}`}>
                <span className="ai-plan-task-status-dot" />
                <span className="ai-plan-task-subject">{t.subject}</span>
                {t.activeForm && <span className="ai-plan-task-active-form">{t.activeForm}</span>}
              </li>
            ))}
          </ul>
        )}

        {isPending && frame.requestId && (
          <div className="ai-plan-footer">
            {rejecting && !editing && !isMobile && (
              <div className="ai-plan-reject-feedback">
                <textarea
                  className="ai-plan-editor ai-plan-feedback-editor"
                  rows={3}
                  placeholder={t('plan.placeholder.changePrompt')}
                  value={feedback}
                  onChange={e => setFeedback(e.target.value)}
                  autoFocus
                />
              </div>
            )}
            <MobileCardComposer
              open={rejectComposerOpen}
              value={feedback}
              onChange={setFeedback}
              onSubmit={handleReject}
              onClose={closeRejectComposer}
              title={t('ai.step.planApproval')}
              placeholder={t('plan.placeholder.changePrompt')}
              submitLabel={t('common.reject')}
              multiline={true}
            />
            <div className="ai-plan-approval-actions">
              {!editing && (
                <>
                  {rejecting ? (
                    <>
                      {!isMobile && (
                        <button
                          className="ai-plan-btn ai-plan-btn--secondary"
                          type="button"
                          onClick={() => { setRejecting(false); setFeedback('') }}
                        >
                          Cancel
                        </button>
                      )}
                      {!isMobile && (
                        <button
                          className="ai-plan-btn ai-plan-btn--secondary"
                          type="button"
                          onClick={handleReject}
                        >
                          Confirm Reject
                        </button>
                      )}
                    </>
                  ) : (
                    <>
                      <button
                        className="ai-plan-btn ai-plan-btn--secondary"
                        type="button"
                        onClick={openRejectComposer}
                      >
                        Reject
                      </button>
                      {frame.editable && (
                        <button
                          className="ai-plan-btn ai-plan-btn--secondary"
                          type="button"
                          onClick={openEditComposer}
                        >
                          Edit & Approve
                        </button>
                      )}
                      {!frame.goalActive && (
                        <button
                          className="ai-plan-btn ai-plan-btn--secondary"
                          type="button"
                          onClick={() => submitPlanApproval('confirm_goal')}
                          title={t('plan.confirmGoal.tooltip')}
                        >
                          <Target size={12} />
                          {t('plan.confirmGoal')}
                        </button>
                      )}
                      <button
                        className="ai-plan-btn ai-plan-btn--secondary"
                        type="button"
                        onClick={() => setWorkflowConfirmOpen(true)}
                        title={t('plan.startWorkflow.tooltip')}
                      >
                        <GitBranch size={12} />
                        {t('plan.startWorkflow')}
                      </button>
                      <button
                        className="ai-plan-btn ai-plan-btn--primary"
                        type="button"
                        onClick={() => submitPlanApproval('approve')}
                      >
                        Approve
                      </button>
                    </>
                  )}
                </>
              )}
              {editing && (
                <>
                  <button
                    className="ai-plan-btn ai-plan-btn--secondary"
                    type="button"
                    onClick={closeEditComposer}
                  >
                    Cancel
                  </button>
                  <button
                    className="ai-plan-btn ai-plan-btn--primary"
                    type="button"
                    onClick={handleEditApprove}
                  >
                    Save & Approve
                  </button>
                </>
              )}
            </div>
          </div>
        )}
        {isCancelled && (
          <div className="ai-plan-footer">
            <div className="ai-plan-expired-note">
              Turn ended — plan was not approved.
            </div>
          </div>
        )}
      </div>
      <ConfirmDialog
        open={workflowConfirmOpen}
        title={t('plan.startWorkflow.confirm.title')}
        description={t('plan.startWorkflow.confirm.desc')}
        confirmLabel={t('plan.startWorkflow.confirm.confirmLabel')}
        onConfirm={handleWorkflowConfirm}
        onCancel={() => setWorkflowConfirmOpen(false)}
      />
    </TimelineStep>
  )
}
