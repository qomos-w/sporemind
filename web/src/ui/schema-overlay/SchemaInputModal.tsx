import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, Loader2, X } from 'lucide-react'
import { Modal } from '../components/Modal'
import { useI18n } from '../../i18n'
import { client } from '../../application/generated-client'
import { loadPreference, savePreference } from '../../application/theme-persist'
import { SchemaForm, defaultValueForSchema } from './SchemaForm'
import { isObjectSchema, type JSONSchema } from './json-schema'
import './SchemaInputModal.css'

export interface SchemaInputModalProps {
  open: boolean
  /** Callable ID to invoke on submit. */
  callableId: string
  /** JSONSchema describing the request payload. */
  jsonSchema: JSONSchema
  /** Pre-filled values (e.g. ActionArgs from a toast). */
  draft?: Record<string, unknown>
  /** Modal title (defaults to the callable id). */
  title?: string
  description?: string
  /** Confirmation button label. */
  submitLabel?: string
  /** Cancel button label. */
  cancelLabel?: string
  /** Optional scope identifier; when set, drafts are persisted under this key
   *  so two entry points (toast / workflow / step) targeting the same
   *  callable share drafts. Defaults to the callable id. */
  draftScope?: string
  /** Invoke the callable through a specific service route (otherwise the
   *  callable id is dispatched as-is over the gateway). */
  invokeOptions?: { reqSchemaId?: number; resSchemaId?: number; serviceHint?: string }
  /** Called after a successful submission. */
  onSubmitted?: (result: unknown) => void
  /** Called when the user closes the modal without submitting. */
  onClose: () => void
}

const DRAFT_KEY_PREFIX = 'schema_overlay.draft.v1'
/** The current "generation" tag — bump to invalidate existing drafts on
 *  schema-shape changes that would otherwise silently corrupt (e.g. a field
 *  renamed). */
const DRAFT_GENERATION = 'g1'

/** Build the persisted draft key. The scope defaults to the callable id;
 *  callers can use a wider scope to merge drafts across entry points. */
function draftKey(scope: string): string {
  return `${DRAFT_KEY_PREFIX}.${DRAFT_GENERATION}.${scope}`
}

function normalizePayload(schema: JSONSchema, value: unknown): Record<string, unknown> {
  // Re-shape under an object schema: when the JSONSchema describes a single
  // value (string/number/etc) we wrap the primitive under a synthetic root
  // so the callable's request struct still receives a real object. Most
  // callables in this codebase are object-typed; the wrapper is a no-op for
  // those.
  if (!isObjectSchema(schema)) {
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      return value as Record<string, unknown>
    }
    return { value }
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

export const SchemaInputModal: React.FC<SchemaInputModalProps> = ({
  open,
  callableId,
  jsonSchema,
  draft,
  title,
  description,
  submitLabel,
  cancelLabel,
  draftScope,
  invokeOptions,
  onSubmitted,
  onClose,
}) => {
  const { t } = useI18n()
  const scope = draftScope ?? callableId
  const key = useMemo(() => draftKey(scope), [scope])

  // Initial value: caller-provided draft wins; otherwise load from the
  // actor-owned preferences backend; otherwise fall back to a schema-shaped
  // default.
  const [value, setValue] = useState<unknown>(() => {
    if (draft && Object.keys(draft).length > 0) return draft
    return defaultValueForSchema(jsonSchema)
  })
  const [hydrated, setHydrated] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string>('')
  const initialDraftRef = useRef<Record<string, unknown> | undefined>(draft)

  // Reset state on every open/close cycle so a fresh submission starts with
  // a clean form.
  useEffect(() => {
    if (!open) {
      setSubmitting(false)
      setError('')
      setHydrated(false)
      return
    }
    initialDraftRef.current = draft
    setValue(draft && Object.keys(draft).length > 0 ? draft : defaultValueForSchema(jsonSchema))
    setError('')
  }, [open, draft, jsonSchema])

  // Async hydrate from actor-owned preferences (only when no caller draft
  // is present). This pulls a persisted draft the user typed earlier and
  // abandoned. The hydrate only overwrites the value if the caller's draft
  // is absent — explicit caller drafts always win.
  useEffect(() => {
    if (!open) return
    if (draft && Object.keys(draft).length > 0) {
      setHydrated(true)
      return
    }
    let cancelled = false
    loadPreference(key)
      .then((raw) => {
        if (cancelled) return
        if (raw) {
          try {
            const parsed = JSON.parse(raw) as Record<string, unknown>
            setValue(parsed)
          } catch {
            // Persisted draft is malformed; ignore and keep the schema default.
          }
        }
      })
      .catch(() => {
        // Backend unavailable; operate without persistence.
      })
      .finally(() => {
        if (!cancelled) setHydrated(true)
      })
    return () => {
      cancelled = true
    }
  }, [open, key, draft])

  // Persist draft on every change (debounced lightly via microtask; a fully
  // synchronous call is fine here because the request is fire-and-forget).
  useEffect(() => {
    if (!open || !hydrated) return
    if (submitting) return
    const payload = normalizePayload(jsonSchema, value)
    // Skip the write when the value is the schema default — no point persisting
    // an empty draft.
    const defaults = normalizePayload(jsonSchema, defaultValueForSchema(jsonSchema))
    if (shallowEqual(payload, defaults)) return
    const id = `${callableId}-${Date.now()}`
    void savePreference(key, JSON.stringify(payload), id, { v: 0 }).catch(() => {})
  }, [value, open, hydrated, submitting, key, callableId, jsonSchema])

  const handleSubmit = useCallback(async () => {
    if (submitting) return
    setSubmitting(true)
    setError('')
    const payload = normalizePayload(jsonSchema, value)
    try {
      const opts = invokeOptions ?? {}
      const result = await client.invoke<Record<string, unknown>, unknown>(
        callableId,
        payload,
        {
          ...(opts.reqSchemaId !== undefined ? { reqSchemaId: opts.reqSchemaId } : {}),
          ...(opts.resSchemaId !== undefined ? { resSchemaId: opts.resSchemaId } : {}),
        },
      )
      onSubmitted?.(result)
      // Clear the persisted draft on success so the next open starts clean.
      void savePreference(key, '', `${callableId}-clear`, { v: 0 }).catch(() => {})
      onClose()
    } catch (e) {
      const message = formatInvokeError(e)
      setError(message)
      // Do not close; the user can correct the inputs and retry.
    } finally {
      setSubmitting(false)
    }
  }, [submitting, value, callableId, invokeOptions, onSubmitted, onClose, key, jsonSchema])

  const schemaTitle = title ?? jsonSchema.title ?? callableId
  const submitText = submitLabel ?? t('schemaOverlay.submit')
  const cancelText = cancelLabel ?? t('common.cancel')

  return (
    <Modal
      open={open}
      title={
        <div className="schema-overlay-modal__title">
          <span className="schema-overlay-modal__title-label">{schemaTitle}</span>
          {callableId !== schemaTitle && (
            <span className="schema-overlay-modal__title-sublabel">{callableId}</span>
          )}
        </div>
      }
      onClose={onClose}
      size="lg"
      closeOnEscape={!submitting}
      closeOnOverlayClick={!submitting}
      disableClose={submitting}
    >
      <div className="schema-overlay-modal">
        {(description ?? jsonSchema.description) && (
          <p className="schema-overlay-modal__description">{description ?? jsonSchema.description}</p>
        )}
        <SchemaForm
          schema={jsonSchema}
          value={value}
          onChange={setValue}
          rootLabel=""
          rootDescription=""
        />
        {error && (
          <div className="schema-overlay-modal__error" role="alert">
            <AlertCircle size={14} className="schema-overlay-modal__error-icon" />
            <span>{error}</span>
            <button
              type="button"
              className="schema-overlay-modal__error-dismiss"
              onClick={() => setError('')}
              aria-label="Dismiss error"
            >
              <X size={12} />
            </button>
          </div>
        )}
        <div className="schema-overlay-modal__footer">
          <button
            type="button"
            className="modal-action modal-action--secondary"
            onClick={onClose}
            disabled={submitting}
          >
            {cancelText}
          </button>
          <button
            type="button"
            className="modal-action modal-action--primary"
            onClick={() => {
              void handleSubmit()
            }}
            disabled={submitting}
            data-guide-id="schema-overlay-submit"
          >
            {submitting && <Loader2 size={12} className="schema-overlay-modal__spinner" />}
            {submitText}
          </button>
        </div>
      </div>
    </Modal>
  )
}

function formatInvokeError(e: unknown): string {
  if (!e) return 'Invocation failed'
  if (e instanceof Error) return e.message || 'Invocation failed'
  if (typeof e === 'string') return e
  try {
    return JSON.stringify(e)
  } catch {
    return 'Invocation failed'
  }
}

function shallowEqual(a: Record<string, unknown>, b: Record<string, unknown>): boolean {
  const ak = Object.keys(a)
  const bk = Object.keys(b)
  if (ak.length !== bk.length) return false
  for (const k of ak) {
    if (!Object.is(a[k], b[k])) return false
  }
  return true
}