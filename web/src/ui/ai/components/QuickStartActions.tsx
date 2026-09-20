import React, { useEffect } from 'react'
import { Waypoints, Target, Puzzle } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { requestModeMount } from './modeMountStore'
import './QuickStartActions.css'

interface QuickStartActionsProps {
  /** Actor the builtin mode mounts on. */
  actorId: string
  /** Fired when the cards mount (used by the shell to focus the composer). */
  onMounted?: () => void
}

/** Workflow/goal/plugin-dev quick-start cards. Split out of the conversation
 *  welcome so they float directly above the composer in a fresh conversation.
 *  The plugin card mounts the builtin:bundle:plugin-dev tool bundle (not a
 *  mode), giving the agent the native plugin development surface. */
export const QuickStartActions: React.FC<QuickStartActionsProps> = ({ actorId, onMounted }) => {
  const { t } = useI18n()

  useEffect(() => {
    onMounted?.()
  }, [onMounted])

  const start = (cardId: string) => {
    requestModeMount(cardId, actorId)
  }

  return (
    <div className="ai-quick-start-actions">
      <button
        type="button"
        className="ai-quick-start-action"
        style={{ '--qa-accent': '#2b9be6' } as React.CSSProperties}
        onClick={() => start('builtin:mode:workflow')}
      >
        <span className="ai-quick-start-action-icon">
          <Waypoints size={16} />
        </span>
        <span className="ai-quick-start-action-text">
          <span className="ai-quick-start-action-label">{t('quickStart.workflow.label')}</span>
          <span className="ai-quick-start-action-hint">{t('quickStart.workflow.hint')}</span>
        </span>
      </button>
      <button
        type="button"
        className="ai-quick-start-action"
        style={{ '--qa-accent': '#a371f7' } as React.CSSProperties}
        onClick={() => start('builtin:mode:goal')}
      >
        <span className="ai-quick-start-action-icon">
          <Target size={16} />
        </span>
        <span className="ai-quick-start-action-text">
          <span className="ai-quick-start-action-label">{t('quickStart.goal.label')}</span>
          <span className="ai-quick-start-action-hint">{t('quickStart.goal.hint')}</span>
        </span>
      </button>
      <button
        type="button"
        className="ai-quick-start-action"
        style={{ '--qa-accent': '#9333ea' } as React.CSSProperties}
        onClick={() => start('builtin:bundle:plugin-dev')}
      >
        <span className="ai-quick-start-action-icon">
          <Puzzle size={16} />
        </span>
        <span className="ai-quick-start-action-text">
          <span className="ai-quick-start-action-label">{t('quickStart.plugin.label')}</span>
          <span className="ai-quick-start-action-hint">{t('quickStart.plugin.hint')}</span>
        </span>
      </button>
    </div>
  )
}
