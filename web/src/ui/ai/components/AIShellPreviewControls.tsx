import React from 'react'
import { Play, RotateCcw } from 'lucide-react'
import type { AIPreviewScenario } from '../model/ai-preview-envelopes'

interface AIShellPreviewControlsProps {
  scenarios: AIPreviewScenario[]
  activeScenarioId: string
  onScenarioChange: (id: string) => void
  isPreviewStreaming: boolean
  onStartPreview?: () => void
  onResetPreview?: () => void
}

export const AIShellPreviewControls: React.FC<AIShellPreviewControlsProps> = ({
  scenarios,
  activeScenarioId,
  onScenarioChange,
  isPreviewStreaming,
  onStartPreview,
  onResetPreview,
}) => {
  return (
    <div className="ai-shell-mock-controls">
      <select
        className="ai-shell-preview-select"
        value={activeScenarioId}
        onChange={(e) => onScenarioChange(e.target.value)}
        disabled={isPreviewStreaming}
        aria-label="Preview scenario"
      >
        {scenarios.map((scenario) => (
          <option key={scenario.id} value={scenario.id}>
            {scenario.label}
          </option>
        ))}
      </select>
      <button
        className="ai-shell-mock-icon-btn"
        onClick={onStartPreview}
        disabled={isPreviewStreaming || !onStartPreview}
        title="Start preview"
        aria-label="Start preview"
        type="button"
      >
        <Play size={12} />
      </button>
      <button
        className="ai-shell-mock-icon-btn"
        onClick={onResetPreview}
        disabled={!onResetPreview}
        title="Reset preview"
        aria-label="Reset preview"
        type="button"
      >
        <RotateCcw size={12} />
      </button>
    </div>
  )
}
