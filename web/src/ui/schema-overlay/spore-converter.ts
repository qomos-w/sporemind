import { schemaEntries } from '../../gen-clients/system/registry'
import type { ObjectDesc, TypeDesc } from '@qomos/spore-ts/schema'
import type { JSONSchema } from './json-schema'
import { isObjectSchema } from './json-schema'

/**
 * Conversion helpers between spore-ts descriptors (ObjectDesc / TypeDesc)
 * and the JSONSchema subset the modal renders. We deliberately only handle
 * the types the modal can render: scalars (string / number / integer /
 * boolean), arrays, maps, and structs. Anything else (class with methods,
 * `any`, `void`) degrades to a free-form JSON textarea via a `{}` schema.
 *
 * The conversion is a best-effort render: the field layout of a spore
 * struct/class maps cleanly to JSON Schema's `properties` table. Private
 * fields are dropped (the runtime hides them too).
 */

const SCALAR_BOOL = new Set(['bool', 'boolean'])
const SCALAR_INTEGER = new Set(['int', 'int8', 'int16', 'int32', 'int64', 'long', 'uint', 'uint8', 'uint16', 'uint32', 'uint64', 'ulong'])
const SCALAR_NUMBER = new Set(['float', 'float32', 'float64', 'double'])
const SCALAR_STRING = new Set(['string', 'bytes'])

/** Convert a spore TypeDesc to a JSONSchema. The element/key/value children
 *  are followed for arrays, maps, and nested structs. */
export function typeDescToJSONSchema(
  td: TypeDesc | undefined,
  objectFor: (name: string) => ObjectDesc | undefined,
): JSONSchema {
  if (!td) return {}
  // Recursive type guard: anything that resolves to a struct/class becomes
  // an object schema (with its fields expanded).
  const object = objectFor(td.className ?? td.name ?? '')
  if (object) {
    return objectDescToJSONSchema(object, objectFor)
  }
  switch (td.kind) {
    case 'scalar': {
      const name = (td.name ?? '').toLowerCase()
      if (SCALAR_BOOL.has(name)) return { type: 'boolean' }
      if (SCALAR_INTEGER.has(name)) return { type: 'integer' }
      if (SCALAR_NUMBER.has(name)) return { type: 'number' }
      if (SCALAR_STRING.has(name)) return { type: 'string' }
      return { type: 'string' }
    }
    case 'array': {
      return {
        type: 'array',
        items: typeDescToJSONSchema(td.element, objectFor),
      }
    }
    case 'map': {
      // JSON objects are string-keyed; the spore map's `key` is metadata only.
      return {
        type: 'object',
        additionalProperties: typeDescToJSONSchema(td.value, objectFor),
      }
    }
    case 'struct':
    case 'class': {
      // The lookup above should have caught this; cover the edge case
      // where the class isn't registered.
      return { type: 'object' }
    }
    case 'void':
    case 'invalid':
    default:
      return {}
  }
}

/** Convert a spore ObjectDesc to a JSONSchema (object with `properties`
 *  and `required`). */
export function objectDescToJSONSchema(
  object: ObjectDesc,
  objectFor: (name: string) => ObjectDesc | undefined,
): JSONSchema {
  const properties: Record<string, JSONSchema> = {}
  const required: string[] = []
  for (const field of object.fields ?? []) {
    if (field.private) continue
    const child = typeDescToJSONSchema(field.type, objectFor)
    if (field.description) child.description = field.description
    if (field.name) child.title = prettifyTitle(field.name)
    properties[field.name] = child
    if (!field.optional) required.push(field.name)
  }
  const result: JSONSchema = {
    type: 'object',
    properties,
  }
  if (required.length > 0) result.required = required
  if (object.name) result.title = object.name
  return result
}

/** Build the system schema registry the modal can read from. The registry is
 *  the same one binary-codec uses, so any entry registered for the wire is
 *  resolvable by the modal — keeping schema and modal rendering in sync. */
let cachedRegistry: ReturnType<typeof buildSystemSchemaRegistry> | null = null
function buildSystemSchemaRegistry() {
  // Lazy import to avoid pulling spore-ts into the schema-overlay bundle if
  // nothing actually queries the system registry.
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const { SchemaRegistry } = require('@qomos/spore-ts/registry') as typeof import('@qomos/spore-ts/registry')
  const reg = new SchemaRegistry()
  for (const entry of schemaEntries) {
    try {
      reg.register(entry)
    } catch {
      // Duplicate (namespace, id) or (namespace, name) — happens when an
      // app-defined schema shadows a system one. The first register wins;
      // subsequent ones for the same key are ignored so the modal can still
      // resolve the system entry.
    }
  }
  return reg
}

function registry() {
  if (!cachedRegistry) cachedRegistry = buildSystemSchemaRegistry()
  return cachedRegistry
}

/** Look up a schema entry by its uint64 ID. Returns undefined when the ID
 *  isn't in the system registry (e.g. a runtime schema not present in the
 *  generated client). */
export function findSchemaEntryById(schemaId: number | undefined): { entry: { object?: ObjectDesc; name: string }; objectFor: (name: string) => ObjectDesc | undefined } | undefined {
  if (typeof schemaId !== 'number' || !Number.isFinite(schemaId)) return undefined
  const entry = registry().getById(schemaId)
  if (!entry) return undefined
  return {
    entry,
    objectFor: registry().objectFor(entry.namespace),
  }
}

/** Convert a schema ID to a JSONSchema. Returns undefined when the schema
 *  cannot be located or has no object shape (e.g. a scalar response type). */
export function jsonSchemaFromSchemaId(schemaId: number | undefined): JSONSchema | undefined {
  const resolved = findSchemaEntryById(schemaId)
  if (!resolved || !resolved.entry.object) return undefined
  return objectDescToJSONSchema(resolved.entry.object, resolved.objectFor)
}

/** Build a flat JSONSchema from the shallow `Params` list returned by
 *  `agent.list_callables` (or by the inspect actor). Used when the deep
 *  schema is unavailable — every parameter becomes a top-level property
 *  with the type derived from the Params.Type field. Required-ness is
 *  preserved (Params.Required). */
export function jsonSchemaFromCallableParams(
  params: ReadonlyArray<{ Name: string; Type: string; Description?: string; Required?: boolean }>,
): JSONSchema {
  const properties: Record<string, JSONSchema> = {}
  const required: string[] = []
  for (const param of params) {
    properties[param.Name] = paramTypeToJSONSchema(param.Type, param.Description)
    if (param.Required) required.push(param.Name)
  }
  const schema: JSONSchema = { type: 'object', properties }
  if (required.length > 0) schema.required = required
  return schema
}

function paramTypeToJSONSchema(typeName: string, description?: string): JSONSchema {
  const lowered = typeName.toLowerCase().trim()
  const schema = baseSchemaForTypeName(lowered)
  if (description) schema.description = description
  return schema
}

function baseSchemaForTypeName(name: string): JSONSchema {
  if (SCALAR_BOOL.has(name)) return { type: 'boolean' }
  if (SCALAR_INTEGER.has(name)) return { type: 'integer' }
  if (SCALAR_NUMBER.has(name)) return { type: 'number' }
  if (SCALAR_STRING.has(name)) return { type: 'string' }
  // Look for the "list of" / "map of" suffixes the inspect uses for
  // collections (`array<string>` → string array).
  const arrayMatch = /^(array|list|slice)\s*<?\s*([^>]+)>?$/.exec(name)
  if (arrayMatch) {
    return { type: 'array', items: baseSchemaForTypeName(arrayMatch[2]!.trim()) }
  }
  const mapMatch = /^map\s*<?\s*([^,>]+),\s*([^>]+)>?$/.exec(name)
  if (mapMatch) {
    return { type: 'object', additionalProperties: baseSchemaForTypeName(mapMatch[2]!.trim()) }
  }
  // Fallback: free-form object so the user can paste JSON.
  if (isObjectSchema({})) return {}
  return {}
}

function prettifyTitle(name: string): string {
  const spaced = name.replace(/[_\-.]+/g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2')
  return spaced.length === 0 ? name : spaced.charAt(0).toUpperCase() + spaced.slice(1)
}
