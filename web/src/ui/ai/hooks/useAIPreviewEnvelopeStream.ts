import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { aiPreviewScenarios, defaultAIPreviewScenarioId, getAIPreviewScenario } from '../model/ai-preview-envelopes'
import { buildEventSequence } from '../model/ai-preview-events'
import { stepReducerBatch } from './step-reducer'
import { stepsToEnvelopes } from './steps-to-envelopes'
import type { Step, StepEvent } from '../../../gen-types/aigen'
import type { LocalEvent } from './timeline-manager'

export function useAIPreviewEnvelopeStream() {
  const [scenarioId, setScenarioId] = useState(defaultAIPreviewScenarioId)
  const scenario = useMemo(() => getAIPreviewScenario(scenarioId), [scenarioId])
  const timersRef = useRef<ReturnType<typeof setTimeout>[]>([])
  const cancelledRef = useRef(false)
  const [steps, setSteps] = useState<Step[]>([])
  const [isStreaming, setIsStreaming] = useState(false)

  const dispatchEvent = useCallback((event: StepEvent | LocalEvent) => {
    if ('kind' in event) {
      // LocalEvent — no-op for preview (answers are visual only)
      return
    }
    setSteps(prev => stepReducerBatch(prev, [event]))
  }, [])

  const clearTimers = useCallback(() => {
    cancelledRef.current = true
    timersRef.current.forEach(clearTimeout)
    timersRef.current = []
  }, [])

  const startPreviewStream = useCallback(() => {
    clearTimers()
    cancelledRef.current = false

    const targetState = scenario.states[scenario.states.length - 1] ?? []
    const events = buildEventSequence(targetState)
    let index = 0

    setSteps([])
    setIsStreaming(true)

    const scheduleNext = () => {
      if (cancelledRef.current || index >= events.length) {
        if (!cancelledRef.current) {
          setIsStreaming(false)
        }
        return
      }

      const { delayMs, event } = events[index]!
      index++

      const timer = setTimeout(() => {
        if (cancelledRef.current) return
        if ('kind' in event) {
          // LocalEvent
          dispatchEvent(event)
        } else {
          setSteps(prev => stepReducerBatch(prev, [event]))
        }
        scheduleNext()
      }, delayMs)

      timersRef.current.push(timer)
    }

    scheduleNext()
  }, [clearTimers, scenario])

  const resetPreviewStream = useCallback(() => {
    clearTimers()
    setSteps([])
    setIsStreaming(false)
  }, [clearTimers])

  useEffect(() => {
    clearTimers()
    cancelledRef.current = false
    setSteps([])
    setIsStreaming(false)
  }, [clearTimers, scenario])

  useEffect(() => {
    return () => clearTimers()
  }, [clearTimers])

  const envelopes = useMemo(() => stepsToEnvelopes(steps), [steps])

  return {
    scenarios: aiPreviewScenarios,
    scenarioId,
    setScenarioId,
    envelopes,
    isStreaming,
    startPreviewStream,
    resetPreviewStream,
    dispatchEvent,
  }
}
