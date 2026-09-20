import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { ToolBodyFrame, ToolRunning, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { parseJsonObject, firstString, normalizeToolName } from './tool-display.ts'

interface TaskToolViewProps {
  frame: ToolFrame
}

type TaskStatus = 'pending' | 'in_progress' | 'completed'

function statusClass(status: TaskStatus | undefined): string {
  switch (status) {
    case 'completed': return 'task--completed'
    case 'in_progress': return 'task--in_progress'
    default: return 'task--pending'
  }
}

function normalizeStatus(value: string | undefined): TaskStatus | undefined {
  if (!value) return undefined
  const v = value.toLowerCase()
  if (v === 'completed' || v === 'in_progress' || v === 'pending') return v
  return undefined
}

// The create/update responses wrap the task under a `Task`/`task` field; a
// bare object (or unparseable output) falls back to itself.
function extractTaskObject(parsed: Record<string, unknown> | null): Record<string, unknown> | null {
  if (!parsed) return null
  const nested = parsed.Task ?? parsed.task
  if (nested && typeof nested === 'object' && !Array.isArray(nested)) {
    return nested as Record<string, unknown>
  }
  return parsed
}

export const TaskToolView: React.FC<TaskToolViewProps> = ({ frame }) => {
  const isRunning = frame.status === 'running'
  const isError = frame.status === 'error'
  const isCreate = normalizeToolName(frame.toolName).includes('create')

  const input = parseJsonObject(frame.input)
  const taskObj = extractTaskObject(parseJsonObject(frame.output ?? ''))

  const subject = (taskObj ? firstString(taskObj, ['subject', 'Subject']) : undefined)
    ?? (input ? firstString(input, ['subject', 'Subject', 'description', 'Description']) : undefined)
  const activeForm = (taskObj ? firstString(taskObj, ['activeForm', 'ActiveForm', 'active_form']) : undefined)
    ?? (input ? firstString(input, ['activeForm', 'ActiveForm', 'active_form']) : undefined)
  // For create_task the resulting status lives in the response; for update_task
  // the requested status lives in the input (the response echoes it back).
  const status = normalizeStatus(
    isCreate
      ? (taskObj ? firstString(taskObj, ['status', 'Status']) : undefined)
      : (input ? firstString(input, ['status', 'Status']) : undefined)
  )

  return (
    <ToolBodyFrame frame={frame}>
      <div className={`ai-task-tool ${statusClass(status)}`}>
        <span className="ai-plan-task-status-dot" />
        <span className="ai-plan-task-subject">{subject ?? ''}</span>
        {activeForm && <span className="ai-plan-task-active-form">{activeForm}</span>}
      </div>
      {isRunning && <ToolRunning />}
      {isError && frame.output && <ToolCodeBlock error>{frame.output}</ToolCodeBlock>}
    </ToolBodyFrame>
  )
}
