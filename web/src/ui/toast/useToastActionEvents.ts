import { useEffect } from 'react'
import { client } from '../../application/generated-client'
import * as toastApi from '../../gen-clients/toast/client'
import { useSchemaOverlay } from '../schema-overlay/useSchemaOverlay'
import {
  jsonSchemaFromCallableParams,
  jsonSchemaFromSchemaId,
} from '../schema-overlay/spore-converter'
import { isObjectSchema, type JSONSchema } from '../schema-overlay/json-schema'
import { useI18n } from '../../i18n'

/**
 * Main-window side of the toast action channel. The toast overlay renders one
 * action button per card carrying an ActionCallable/ActionLabel pair; clicking
 * it reports `toast.action` to the toast actor, which re-broadcasts
 * `toast.action_triggered` (callable ID + args) over the gateway to every
 * subscriber. The main window — not the overlay, not the actor — owns what
 * happens next.
 *
 * The action maps directly to the shared schema input modal: the toast
 * supplies `ActionCallable` and a JSONSchema (resolved from `ActionSchemaID`
 * via the schema registry, or built from the ActionArgs shape as a
 * fallback), the modal renders a form, the user submits, and the
 * callable is invoked through the standard gateway. Three entry points
 * (toast action / workflow card / AI step) share the same modal — see
 * [[schema-overlay-input-modal]].
 */
export function useToastActionEvents() {
  const { t } = useI18n()
  const overlay = useSchemaOverlay()
  useEffect(() => {
    const off = toastApi.OnToastActionTriggered(client, (payload) => {
      const schema = resolveActionSchema(payload)
      if (!schema) {
        // Without a JSONSchema the modal cannot render; the user can still
        // invoke the action via the legacy inline flow on the companion
        // window. Log so the failure is observable in the dev console.
        console.warn('[toast] action triggered but no schema available', payload)
        return
      }
      overlay.open({
        callableId: payload.ActionCallable,
        jsonSchema: schema,
        ...(payload.ActionArgs ? { draft: payload.ActionArgs } : {}),
        title: payload.ActionLabel || t('schemaOverlay.toastActionFallbackTitle'),
        description: t('schemaOverlay.toastActionFallbackTitle'),
        draftScope: `toast:${payload.Id}`,
      })
    })
    return () => off()
  }, [overlay, t])
}

/** Resolve the JSONSchema the modal will render for a triggered toast
 *  action. Three fallbacks, in order of specificity:
 *
 *  1. The `ActionSchemaID` (when present and known) → deep ObjectDesc →
 *     JSONSchema via the spore-converter.
 *  2. A free-form object schema derived from the ActionArgs shape
 *     (top-level keys become declared properties; their JSON values stay
 *     free-form). This is the best we can do for actions that don't
 *     declare a schema id.
 *  3. `undefined` — the caller logs and skips opening the modal. */
function resolveActionSchema(
  payload: { ActionSchemaID?: number; ActionArgs?: Record<string, unknown> },
): JSONSchema | undefined {
  if (typeof payload.ActionSchemaID === 'number' && payload.ActionSchemaID > 0) {
    const fromId = jsonSchemaFromSchemaId(payload.ActionSchemaID)
    if (fromId) return fromId
  }
  if (payload.ActionArgs && Object.keys(payload.ActionArgs).length > 0) {
    return jsonSchemaFromArgsShape(payload.ActionArgs)
  }
  // Last-ditch: render an empty object schema. The user types everything
  // by hand — better than a no-op.
  if (typeof payload.ActionSchemaID === 'number' && payload.ActionSchemaID > 0) {
    return { type: 'object', properties: {}, additionalProperties: true }
  }
  return undefined
}

/** Build a JSONSchema from the shape of a payload object: every top-level
 *  key becomes a free-form property so the form mirrors the example args. */
function jsonSchemaFromArgsShape(args: Record<string, unknown>): JSONSchema {
  const properties: Record<string, JSONSchema> = {}
  for (const [key, value] of Object.entries(args)) {
    properties[key] = jsonSchemaForValue(value)
  }
  // Preserve the supplied value as the default for the user to tweak.
  return {
    type: 'object',
    properties,
    additionalProperties: true,
  }
}

function jsonSchemaForValue(value: unknown): JSONSchema {
  if (value === null || value === undefined) return {}
  if (typeof value === 'string') return { type: 'string' }
  if (typeof value === 'boolean') return { type: 'boolean' }
  if (typeof value === 'number') {
    return Number.isInteger(value) ? { type: 'integer' } : { type: 'number' }
  }
  if (Array.isArray(value)) {
    return { type: 'array', items: jsonSchemaForValue(value[0]) }
  }
  if (typeof value === 'object') {
    return { type: 'object', additionalProperties: true }
  }
  return {}
}

// Re-export for callers that need a flat schema from a callable's shallow
// Params (used by the workflow-card / AI-step entry points).
export { jsonSchemaFromCallableParams, isObjectSchema }
