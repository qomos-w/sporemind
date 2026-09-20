import React, { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUp, Mic, Square, X, Trash2 } from 'lucide-react'
import { voiceAPI } from '../voice-api'
import { useI18n } from '../../../i18n'
import { useLongPress } from '../hooks/useLongPress'
import './MobileCardComposer.css'

interface MobileCardComposerProps {
  open: boolean
  value: string
  onChange: (value: string) => void
  onSubmit: () => void
  onClose: () => void
  title?: string
  placeholder?: string
  submitLabel?: string
  multiline?: boolean
}

export const MobileCardComposer: React.FC<MobileCardComposerProps> = ({
  open,
  value,
  onChange,
  onSubmit,
  onClose,
  title,
  placeholder,
  submitLabel,
  multiline = true,
}) => {
  const { t } = useI18n()
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const valueRef = useRef(value)
  const [recording, setRecording] = useState(false)
  const isRecordingRef = useRef(false)

  valueRef.current = value
  const canSubmit = value.trim().length > 0

  // Auto-resize textarea as content grows.
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    const lineHeight = parseInt(getComputedStyle(el).lineHeight, 10) || 20
    const maxHeight = lineHeight * 8
    el.style.height = `${Math.min(el.scrollHeight, maxHeight)}px`
  }, [value])

  // Focus when opened.
  useEffect(() => {
    if (!open) return
    const id = window.setTimeout(() => textareaRef.current?.focus(), 50)
    return () => window.clearTimeout(id)
  }, [open])

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

  // Voice input — mirrors AIComposer: a ref-guarded setter drives the shared
  // voiceAPI state machine via setActive, never raw start/stop calls.
  const setVoiceActive = useCallback((active: boolean) => {
    if (isRecordingRef.current === active) return
    isRecordingRef.current = active
    setRecording(active)
  }, [])

  useEffect(() => {
    void voiceAPI.setActive(recording, recording ? {
      onResult: (text) => {
        const cur = valueRef.current
        onChange((cur ? cur + ' ' : '') + text)
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
  }, [recording, onChange, setVoiceActive])

  // Stop voice when the sheet closes or unmounts.
  useEffect(() => {
    if (!open) setVoiceActive(false)
  }, [open, setVoiceActive])

  useEffect(() => {
    return () => { void voiceAPI.setActive(false) }
  }, [])

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (canSubmit) onSubmit()
    }
  }, [canSubmit, onSubmit])

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

  const handleClear = useCallback(() => {
    onChange('')
    textareaRef.current?.focus()
  }, [onChange])

  const handleBackdropClick = useCallback((e: React.MouseEvent) => {
    if (e.target === e.currentTarget) onClose()
  }, [onClose])

  if (!open) return null

  return createPortal(
    <div className="mobile-card-composer-backdrop" onClick={handleBackdropClick} role="presentation">
      <div className="mobile-card-composer" role="dialog" aria-label={title || t('mobileCardComposer.title')}>
        <div className="mobile-card-composer-header">
          <span className="mobile-card-composer-title">{title || t('mobileCardComposer.title')}</span>
          <button
            type="button"
            className="mobile-card-composer-close"
            onClick={onClose}
            aria-label={t('common.close')}
          >
            <X size={16} />
          </button>
        </div>

        <textarea
          ref={textareaRef}
          className="mobile-card-composer-textarea"
          value={value}
          onChange={(e) => onChange(e.target.value)}
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
          placeholder={placeholder || t('mobileCardComposer.placeholder')}
          rows={multiline ? 3 : 1}
          readOnly={recording}
        />

        <div className="mobile-card-composer-actions">
          <button
            type="button"
            className="mobile-card-composer-action"
            data-active={recording}
            onClick={() => setVoiceActive(!isRecordingRef.current)}
            aria-label={t('mobileCardComposer.voice')}
          >
            {recording ? <Square size={14} /> : <Mic size={16} />}
          </button>

          <button
            type="button"
            className="mobile-card-composer-action"
            onClick={handleClear}
            disabled={value.length === 0}
            aria-label={t('mobileCardComposer.clear')}
          >
            <Trash2 size={16} />
          </button>

          <button
            type="button"
            className="mobile-card-composer-submit"
            data-active={canSubmit}
            disabled={!canSubmit}
            onClick={() => onSubmit()}
            aria-label={submitLabel || t('mobileCardComposer.submit')}
          >
            <span className="mobile-card-composer-submit-label">{submitLabel || t('mobileCardComposer.submit')}</span>
            <ArrowUp size={16} />
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
