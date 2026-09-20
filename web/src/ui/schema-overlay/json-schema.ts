// Subset of JSON Schema (draft-07) used to drive the schema overlay input
// modal. We only model the shapes the modal can render and validate: object,
// primitive scalars (string/number/integer/boolean), array, map, enum
// (string + "enum" array), and the metadata fields the modal reads
// (description, default, required, title). Anything outside this subset
// degrades to a free-form JSON textarea so an exotic schema still works
// (rather than failing the render).
//
// We intentionally do NOT depend on @types/json-schema: the field is
// exercised by caller code that builds schemas from CallableInterface.Params
// (shallow) or from the runtime's request layout (deep), and we want a
// minimal surface that matches what `protocol.RequestLayout.JSONSchema()`
// emits on the Go side.

export type JSONSchemaType =
  | 'string'
  | 'number'
  | 'integer'
  | 'boolean'
  | 'object'
  | 'array'
  | 'null'

export interface JSONSchemaBase {
  // JSON Schema metadata. Optional — the modal falls back to a textarea when
  // neither "type" nor a discriminator ("properties" / "items" / "enum") is
  // present, so any unknown shape stays renderable.
  title?: string
  description?: string
  default?: unknown
  // For object types: declared property schemas.
  properties?: Record<string, JSONSchema>
  // For object types: keys that must be present in the payload.
  required?: string[]
  // For object types with free-form key/value pairs (additionalProperties is a
  // schema rather than `true`): the value schema.
  additionalProperties?: JSONSchema | boolean
  // For array types: the element schema.
  items?: JSONSchema
  // For string types: a closed member set. The modal renders a select when
  // present; without it, a free-text input.
  enum?: Array<string | number | boolean>
  // The "type" discriminator. The modal also accepts schemas without an
  // explicit type if `properties` (object) or `items` (array) is set.
  type?: JSONSchemaType | JSONSchemaType[]
}

export type JSONSchema = JSONSchemaBase

/** True when the schema declares an object shape (has properties or a
 *  type:object). */
export function isObjectSchema(schema: JSONSchema | undefined): boolean {
  if (!schema) return false
  if (schema.type === 'object') return true
  if (Array.isArray(schema.type) && schema.type.includes('object')) return true
  return schema.properties !== undefined
}

/** True when the schema declares an array shape. */
export function isArraySchema(schema: JSONSchema | undefined): boolean {
  if (!schema) return false
  if (schema.type === 'array') return true
  if (Array.isArray(schema.type) && schema.type.includes('array')) return true
  return schema.items !== undefined
}

/** Returns the effective primitive type of a schema, or `null` when it's a
 *  composite (object/array). `string | number | integer | boolean`. */
export function primitiveType(schema: JSONSchema | undefined): 'string' | 'number' | 'integer' | 'boolean' | null {
  if (!schema) return null
  if (schema.enum && schema.enum.length > 0) return 'string'
  if (schema.type === 'string') return 'string'
  if (schema.type === 'number') return 'number'
  if (schema.type === 'integer') return 'integer'
  if (schema.type === 'boolean') return 'boolean'
  if (Array.isArray(schema.type)) {
    if (schema.type.includes('string')) return 'string'
    if (schema.type.includes('integer')) return 'integer'
    if (schema.type.includes('number')) return 'number'
    if (schema.type.includes('boolean')) return 'boolean'
  }
  return null
}

/** True when the schema is a free-form object without declared properties —
 *  we render it as a JSON textarea (the typed-key/value map UI is only
 *  meaningful when the value type is also known). */
export function isFreeFormObject(schema: JSONSchema | undefined): boolean {
  if (!schema) return false
  if (!isObjectSchema(schema)) return false
  if (schema.properties && Object.keys(schema.properties).length > 0) return false
  return true
}

/** Resolve the value-schema of an object's `additionalProperties` when it is
 *  a schema (typed map). Returns `undefined` for boolean / absent / typed
 *  object with properties. */
export function mapValueSchema(schema: JSONSchema | undefined): JSONSchema | undefined {
  if (!schema) return undefined
  const ap = schema.additionalProperties
  if (!ap || typeof ap === 'boolean') return undefined
  return ap
}

/** Returns a list of required property names for an object schema, in the
 *  order declared in the schema (so the form field order is stable). When the
 *  schema has no explicit `required` array, the set is empty. */
export function requiredProperties(schema: JSONSchema | undefined): string[] {
  if (!schema) return []
  return Array.isArray(schema.required) ? schema.required.slice() : []
}

/** Walks a schema's `properties` to return the list of declared property
 *  names. Order is preserved. Properties that appear in `required` but are
 *  not declared are silently ignored (an exotic shape, not our problem to
 *  reshape). */
export function declaredProperties(schema: JSONSchema | undefined): string[] {
  if (!schema || !schema.properties) return []
  return Object.keys(schema.properties)
}

/** Returns the property schema, or `undefined` for unknown keys when the
 *  additionalProperties schema is not provided (free-form object). */
export function propertySchema(
  schema: JSONSchema | undefined,
  key: string,
): JSONSchema | undefined {
  if (!schema || !schema.properties) return undefined
  return schema.properties[key]
}
