import React, { createContext, useCallback, useContext, useMemo, useState } from 'react'
import { SchemaInputModal } from './SchemaInputModal'
import type { JSONSchema } from './json-schema'

/** Caller-supplied schema modal request. The provider is a singleton: calling
 *  `open` while a previous modal is still mounted replaces the in-flight
 *  request (one modal at a time across the entire app — consistent with the
 *  browser overlay reference-count, which would otherwise need a new
 *  registration anyway). */
export interface SchemaOverlayRequest {
  /** Callable ID dispatched on submit (e.g. "workflow.gate.approve"). */
  callableId: string
  /** JSONSchema describing the request payload. */
  jsonSchema: JSONSchema
  /** Pre-filled values. Takes precedence over any persisted draft. */
  draft?: Record<string, unknown>
  /** Modal title (defaults to the callable id). */
  title?: string
  description?: string
  /** Optional scope for draft persistence; defaults to the callable id. */
  draftScope?: string
  /** Optional schema ids for the gateway envelope (override the codegen
   *  registry when the caller knows the request/response types). */
  invokeOptions?: { reqSchemaId?: number; resSchemaId?: number }
  /** Called after a successful submit; result is the gateway response. */
  onSubmitted?: (result: unknown) => void
}

export interface SchemaOverlayController {
  /** Open the modal with the given request. The previous request (if any)
   *  is replaced; the modal does not stack. */
  open: (request: SchemaOverlayRequest) => void
  /** Close the modal. Equivalent to the user dismissing without submitting. */
  close: () => void
  /** True while a modal is mounted. */
  isOpen: boolean
}

const SchemaOverlayContext = createContext<SchemaOverlayController | null>(null)

/** Mounts a single SchemaInputModal at the app level. Children can use
 *  useSchemaOverlay() to open the modal from any entry point (toast action,
 *  workflow card click, AI step click). The provider must live INSIDE the
 *  BrowserOverlayManager so the modal's open/close is properly ref-counted
 *  against the right-panel native browser windows. */
export const SchemaOverlayProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [request, setRequest] = useState<SchemaOverlayRequest | null>(null)

  const open = useCallback((req: SchemaOverlayRequest) => {
    setRequest({ ...req })
  }, [])

  const close = useCallback(() => {
    setRequest(null)
  }, [])

  const value = useMemo<SchemaOverlayController>(
    () => ({ open, close, isOpen: request !== null }),
    [open, close, request],
  )

  return (
    <SchemaOverlayContext.Provider value={value}>
      {children}
      {request && (
        <SchemaInputModal
          open
          callableId={request.callableId}
          jsonSchema={request.jsonSchema}
          {...(request.draft ? { draft: request.draft } : {})}
          {...(request.title ? { title: request.title } : {})}
          {...(request.description ? { description: request.description } : {})}
          {...(request.draftScope ? { draftScope: request.draftScope } : {})}
          {...(request.invokeOptions ? { invokeOptions: request.invokeOptions } : {})}
          {...(request.onSubmitted ? { onSubmitted: request.onSubmitted } : {})}
          onClose={close}
        />
      )}
    </SchemaOverlayContext.Provider>
  )
}

/** Access the schema overlay controller. Returns `null` (no-op controller)
 *  when the provider is absent — callers can opt into the modal in a guarded
 *  way without crashing in environments where the modal isn't mounted. */
export function useSchemaOverlay(): SchemaOverlayController {
  return useContext(SchemaOverlayContext) ?? NULL_CONTROLLER
}

const NULL_CONTROLLER: SchemaOverlayController = {
  open: () => {
    // No-op: provider absent. We deliberately do not throw — the toast
    // action hook and other entry points want to degrade silently rather
    // than blowing up the whole app.
  },
  close: () => {
    /* no-op */
  },
  isOpen: false,
}
