import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUp, Mic, Square, X, Loader2 } from 'lucide-react'
import { voiceAPI } from '../voice-api'
import { useI18n } from '../../../i18n'
import { client } from '../../../application/generated-client'
import * as agent_chat from '../../../gen-clients/local/client'
import { useBackgroundTimeline } from '../hooks/useTimelineManager'
import { useLongPress } from '../hooks/useLongPress'
import { AIStepText } from './parts/AIStepText'
import type { TurnEnvelope, TextFrame } from '../model/frame-types'
import './MiniComposer.css'

export interface MiniComposerAnchor {
  x: number
  y: number
  width: number
  height: number
}

interface MiniComposerProps {
  open: boolean
  anchor?: MiniComposerAnchor | null
  onClose: () => void
  /** The project's coder agent actor id. The MiniComposer holds its own
   *  conversation with this agent — independent of the active timeline agent. */
  targetActorId: string | null
  /** Text to seed into the composer when it opens (e.g. a `[[cardId]]` link).
   *  Only applied on a closed→open transition, so in-progress typing is kept. */
  initialValue?: string
}

function assistantText(env: TurnEnvelope): string {
  return env.frames
    .filter((f): f is TextFrame => f.type === 'text')
    .map(f => f.content)
    .join('\n')
    .trim()
}

export const MiniComposer: React.FC<MiniComposerProps> = ({
  open,
  anchor,
  onClose,
  targetActorId,
  initialValue,
}) => {
  const { t } = useI18n()
  // The MiniComposer owns its own conversation with the project's coder agent.
  // The background timeline loads/subscribes to the coder agent's envelopes
  // without hijacking the main timeline selection.
  const tl = useBackgroundTimeline(open ? targetActorId : null)
  const rootRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const threadRef = useRef<HTMLDivElement>(null)
  const valueRef = useRef('')
  const [value, setValue] = useState('')
  const [recording, setRecording] = useState(false)
  const [sending, setSending] = useState(false)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)

  valueRef.current = value
  const canSend = value.trim().length > 0 && !sending && !tl.isStreaming

  // Position below the anchor rect, clamped to the viewport. Recompute when the
  // thread grows (height changes); CSS caps the thread height so streaming text
  // within an existing envelope does not move the panel.
  useLayoutEffect(() => {
    if (!open || !rootRef.current) {
      setPos(null)
      return
    }
    const el = rootRef.current
    const rect = el.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    const padding = 8

    const a = anchor ?? { x: vw / 2, y: vh - 80, width: 0, height: 0 }
    const width = rect.width || 360

    let left = a.x
    if (left + width > vw - padding) left = vw - width - padding
    if (left < padding) left = padding

    let top = a.y + a.height + 8
    if (top + rect.height > vh - padding) {
      // Flip above the anchor if it doesn't fit below.
      top = a.y - rect.height - 8
    }
    if (top < padding) top = padding

    setPos({ left, top })
  }, [open, anchor, tl.envelopes.length])

  // Focus the textarea when opened, and seed the initial value (e.g. a card
  // link) only on a closed→open transition so in-progress typing is preserved.
  const prevOpenRef = useRef(open)
  useEffect(() => {
    if (open && !prevOpenRef.current && initialValue) {
      setValue(initialValue)
    }
    prevOpenRef.current = open
    if (open) {
      const id = window.setTimeout(() => textareaRef.current?.focus(), 50)
      return () => window.clearTimeout(id)
    }
  }, [open, initialValue])

  // Close on Escape.
  useEffect(() => {
    if (!open) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onClose])

  // Auto-scroll the thread to the bottom as content streams in.
  useEffect(() => {
    const el = threadRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [tl.envelopes, tl.isStreaming])

  const handleSend = useCallback(async () => {
    const text = valueRef.current.trim()
    if (!text || !targetActorId || sending || tl.isStreaming) return
    setSending(true)
    const tempId = tl.pushUserMessage(text)
    setValue('')
    try {
      const resp = await agent_chat.chatSubmit(client, { Text: text }, { target: targetActorId })
      if (!resp) return
      if (resp.TurnActorId) tl.seedActiveTurn(resp.TurnActorId)
      if (tempId && resp.MessageId) tl.replaceUserMessageId(tempId, resp.MessageId)
      if (resp.Idx != null && resp.Idx > 0 && resp.MessageId) tl.updateUserMessageIdx(resp.MessageId, resp.Idx)
      tl.reconcile()
    } catch {
      // Orphaned-turn recovery: the timeout may be client-side only — the
      // server may have accepted the submit. Reconcile pulls the summary so
      // a created turn's user step confirms the pending message; otherwise
      // the optimistic entry stays and TTL eventually marks it failed.
      tl.reconcile()
    } finally {
      setSending(false)
    }
  }, [targetActorId, sending, tl])

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }, [handleSend])

  // Voice input — mirrors AIComposer: a ref-guarded setter drives the shared
  // voiceAPI state machine via setActive, never raw start/stop calls.
  const isRecordingRef = useRef(false)
  const setVoiceActive = useCallback((active: boolean) => {
    if (isRecordingRef.current === active) return
    isRecordingRef.current = active
    setRecording(active)
  }, [])

  useEffect(() => {
    void voiceAPI.setActive(recording, recording ? {
      onResult: (text) => {
        const cur = valueRef.current
        setValue((cur ? cur + ' ' : '') + text)
      },
      onError: (err) => {
        console.error('[Voice]', err)
        setVoiceActive(false)
      },
      onStop: () => setVoiceActive(false),
    } : undefined).catch((err) => {
      console.error('[Voice] transition failed:', err)
      setVoiceActive(false)
    })
  }, [recording, setVoiceActive])

  // Stop voice when the panel closes or unmounts.
  useEffect(() => {
    if (!open) setVoiceActive(false)
  }, [open, setVoiceActive])

  useEffect(() => {
    return () => { void voiceAPI.setActive(false) }
  }, [])

  // Long-press on textarea is push-to-talk — same gesture as AIComposer.
  const voiceLongPress = useLongPress({
    onLongPress: () => setVoiceActive(true),
    onRelease: () => setVoiceActive(false),
    shouldStart: () => document.activeElement !== textareaRef.current,
  })

  const handleTextareaPointerDown = useCallback((e: React.PointerEvent<HTMLTextAreaElement>) => {
    voiceLongPress.onPointerDown(e)
    const el = e.currentTarget
    el.style.userSelect = 'none'
    el.style.webkitUserSelect = 'none'
  }, [voiceLongPress])

  const handleTextareaPointerUp = useCallback(() => {
    const wasRecording = recording
    voiceLongPress.onPointerUp()
    const el = textareaRef.current
    if (el) {
      el.style.userSelect = ''
      el.style.webkitUserSelect = ''
    }
    if (!wasRecording) el?.focus()
  }, [voiceLongPress, recording])

  if (!open) return null

  const envelopes = tl.envelopes

  return createPortal(
    <div
      ref={rootRef}
      className="mini-composer"
      style={pos ? { left: pos.left, top: pos.top } : undefined}
      role="dialog"
      aria-label={t('miniComposer.title')}
    >
      <div className="mini-composer-header">
        <span className="mini-composer-title">{t('miniComposer.title')}</span>
        <button type="button" className="mini-composer-close" onClick={onClose} aria-label={t('common.close')}>
          <X size={14} />
        </button>
      </div>

      <div className="mini-composer-thread" ref={threadRef}>
        {envelopes.length === 0 ? (
          <div className="mini-composer-empty">{t('miniComposer.placeholder')}</div>
        ) : envelopes.map((env: TurnEnvelope) => {
          const key = env.clientKey ?? env.id
          if (env.role === 'user') {
            return (
              <div key={key} className="mini-composer-msg mini-composer-msg-user">
                <div className="mini-composer-bubble mini-composer-bubble-user">{env.userContent || '…'}</div>
              </div>
            )
          }
          const text = assistantText(env)
          const streaming = tl.isStreaming && !env.completed
          return (
            <div key={key} className="mini-composer-msg mini-composer-msg-assistant">
              <div className="mini-composer-bubble mini-composer-bubble-assistant">
                {text ? <AIStepText>{text}</AIStepText> : null}
                {streaming ? <Loader2 size={12} className="mini-composer-stream-spinner" /> : null}
              </div>
            </div>
          )
        })}
      </div>

      <textarea
        ref={textareaRef}
        className="mini-composer-textarea"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={handleKeyDown}
        onPointerDown={handleTextareaPointerDown}
        onPointerMove={voiceLongPress.onPointerMove}
        onPointerUp={handleTextareaPointerUp}
        onPointerCancel={voiceLongPress.onPointerCancel}
        onPointerLeave={voiceLongPress.onPointerLeave}
        onContextMenu={(e) => {
          if (voiceLongPress.wasLongPress()) {
            e.preventDefault()
            e.stopPropagation()
          }
        }}
        placeholder={t('miniComposer.placeholder')}
        rows={2}
        readOnly={recording}
      />

      <div className="mini-composer-actions">
        <button
          type="button"
          className="mini-composer-action"
          data-active={recording}
          onClick={() => setVoiceActive(!isRecordingRef.current)}
          aria-label={t('miniComposer.voice')}
        >
          <Mic size={15} />
        </button>

        {tl.isStreaming ? (
          <button
            type="button"
            className="mini-composer-stop"
            onClick={tl.stop}
            aria-label={t('common.stop')}
          >
            <Square size={14} />
          </button>
        ) : (
          <button
            type="button"
            className="mini-composer-send"
            data-active={canSend}
            disabled={!canSend}
            onClick={() => void handleSend()}
            aria-label={t('common.send')}
          >
            <ArrowUp size={15} />
          </button>
        )}
      </div>
    </div>,
    document.body,
  )
}
