import React, { useState } from 'react'
import { HelpCircle, CheckCircle } from 'lucide-react'
import type { Frame, AskUserQuestionFrame, TimelineConnectorMode } from '../../model/frame-types.ts'
import { useStepInteraction } from '../../hooks/useStepInteraction'
import { useI18n } from '../../../../i18n'
import { TimelineStep } from '../timeline'
import { AskCustomInput } from './AskCustomInput'

interface AskUserQuestionBlockProps {
  frame: AskUserQuestionFrame
  connectorMode?: TimelineConnectorMode
  onFrameSelect?: (frame: Frame) => void
}

export const AskUserQuestionBlock: React.FC<AskUserQuestionBlockProps> = ({
  frame,
  connectorMode = 'none',
  onFrameSelect,
}) => {
  const { t } = useI18n()
  const [localAnswers, setLocalAnswers] = useState<Record<number, string> | null>(null)
  const { submitAskAnswer } = useStepInteraction(frame.requestId ?? '')

  const resolvedAnswers = frame.answers ?? localAnswers
  const isComplete = frame.status === 'completed' || resolvedAnswers != null

  const handleAnswer = (answers: Record<number, string>) => {
    setLocalAnswers(answers)
    if (frame.requestId) submitAskAnswer(answers)
  }

  return (
    <TimelineStep
      slotId={frame.id}
      status={isComplete ? 'completed' : frame.status}
      icon={
        isComplete
          ? <CheckCircle size={12} className="ai-step-icon ai-step-icon-accent" />
          : <HelpCircle size={12} className="ai-step-icon" />
      }
      label={<span className="ai-step-label">{isComplete ? t('ai.step.answered') : t('ai.step.question')}</span>}
      connectorMode={connectorMode}
      expandable
      expansionMode={isComplete ? 'manual' : 'open'}
      onMaximize={onFrameSelect ? () => onFrameSelect(frame) : undefined}
    >
      <div className="ai-ask-body">
        {isComplete && resolvedAnswers ? (
          <AskAnswers frame={frame} answers={resolvedAnswers} />
        ) : (
          <AskForm frame={frame} onAnswer={handleAnswer} />
        )}
      </div>
    </TimelineStep>
  )
}

const AskForm: React.FC<{
  frame: AskUserQuestionFrame
  onAnswer: (answers: Record<number, string>) => void
}> = ({ frame, onAnswer }) => {
  const { t } = useI18n()
  const [currentQ, setCurrentQ] = useState(0)
  const [answers, setAnswers] = useState<Record<number, string>>({})
  const [customInput, setCustomInput] = useState('')
  const [selectedOptions, setSelectedOptions] = useState<Set<number>>(new Set())

  const question = frame.questions[currentQ]
  if (!question) {
    return (
      <div className="ai-ask-error">
        Question data could not be parsed — the agent has been notified to retry.
      </div>
    )
  }

  const isLast = currentQ >= frame.questions.length - 1

  const advance = (newAnswers: Record<number, string>) => {
    if (isLast) {
      onAnswer(newAnswers)
    } else {
      setAnswers(newAnswers)
      setCurrentQ(currentQ + 1)
      setSelectedOptions(new Set())
      setCustomInput('')
    }
  }

  const handleOptionClick = (optionIndex: number) => {
    if (question.multiSelect) {
      setSelectedOptions(prev => {
        const next = new Set(prev)
        if (next.has(optionIndex)) next.delete(optionIndex)
        else next.add(optionIndex)
        return next
      })
    } else {
      advance({ ...answers, [currentQ]: question.options[optionIndex]!.label })
    }
  }

  const handleSubmitCustom = () => {
    if (!customInput.trim()) return
    advance({ ...answers, [currentQ]: customInput.trim() })
  }

  const handleSubmitMulti = () => {
    const selected = Array.from(selectedOptions).map(i => question.options[i]!.label)
    advance({ ...answers, [currentQ]: selected.join(', ') })
  }

  return (
    <div className="ai-ask-form">
      {frame.questions.length > 1 && (
        <div className="ai-ask-progress">{currentQ + 1} / {frame.questions.length}</div>
      )}
      <div className="ai-ask-header">{question.header}</div>
      <div className="ai-ask-question">{question.question}</div>
      <div className="ai-ask-options">
        {question.options.map((opt, i) => (
          <button
            key={i}
            className={`ai-ask-option ${selectedOptions.has(i) ? 'selected' : ''}`}
            onClick={() => handleOptionClick(i)}
          >
            <span className="ai-ask-option-label">
              {opt.label}
              {opt.recommended && (
                <span className="ai-ask-option-recommended">{t('ai.step.recommended')}</span>
              )}
            </span>
            {opt.description && <span className="ai-ask-option-desc">{opt.description}</span>}
          </button>
        ))}
        <div className="ai-ask-custom">
          <AskCustomInput
            value={customInput}
            onChange={setCustomInput}
            onSubmit={handleSubmitCustom}
            title={question.header || t('ai.step.question')}
          />
        </div>
        {question.multiSelect && selectedOptions.size > 0 && (
          <button className="ai-ask-submit" onClick={handleSubmitMulti}>{t('common.confirm')}</button>
        )}
      </div>
    </div>
  )
}

const AskAnswers: React.FC<{ frame: AskUserQuestionFrame; answers: Record<number, string> }> = ({ frame, answers }) => (
  <div className="ai-ask-answers">
    {frame.questions.map((q, i) => (
      <div key={i} className="ai-ask-answer-row">
        <span className="ai-ask-answer-question">{q.question}</span>
        <span className="ai-ask-answer-value">{answers[i] ?? '—'}</span>
      </div>
    ))}
  </div>
)
