import React from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  Field,
  FieldLabel,
  FieldDescription,
  Input,
  Textarea,
  Checkbox,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
} from '../settings/shadcn/ui'
import {
  declaredProperties,
  isArraySchema,
  isFreeFormObject,
  isObjectSchema,
  mapValueSchema,
  primitiveType,
  propertySchema,
  requiredProperties,
  type JSONSchema,
} from './json-schema'

/** Single-row path used in error messages — the field path is built up by
 *  nested SchemaForm instances as the user drills into objects/arrays. */
export type FieldPath = ReadonlyArray<string | number>

/** Errors raised during validation. The `path` is a FieldPath; `message`
 *  describes the violation. Empty errors means the form is valid. */
export interface SchemaFormError {
  path: FieldPath
  message: string
}

export interface SchemaFormProps {
  schema: JSONSchema
  value: unknown
  onChange: (next: unknown) => void
  onError?: (errors: SchemaFormError[]) => void
  /** Free-form root object: callers can override the label (e.g. a more
   *  descriptive title from the parent modal). */
  rootLabel?: string
  rootDescription?: string
  /** Path into the value tree (used for aria + error reporting). */
  path?: FieldPath
}

/** Coerce a default value matching the schema when the caller has not
 *  supplied a value yet. Returns `undefined` when the schema is
 *  unrenderable and the form will degrade to a textarea. */
export function defaultValueForSchema(schema: JSONSchema): unknown {
  if (schema.default !== undefined) return schema.default
  if (schema.enum && schema.enum.length > 0) return schema.enum[0]
  if (isObjectSchema(schema)) {
    if (isFreeFormObject(schema)) return {}
    const obj: Record<string, unknown> = {}
    for (const key of declaredProperties(schema)) {
      const child = propertySchema(schema, key)
      if (child) obj[key] = defaultValueForSchema(child)
    }
    return obj
  }
  if (isArraySchema(schema)) return []
  const t = primitiveType(schema)
  if (t === 'boolean') return false
  if (t === 'number' || t === 'integer') return 0
  if (t === 'string') return ''
  return undefined
}

/** Build a shallow object containing every declared property with a
 *  sensible default. Used when the caller hands us an empty value but the
 *  schema has many fields — keeps the form responsive. */
export function ensureObjectShape(schema: JSONSchema, value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return { ...(value as Record<string, unknown>) }
  }
  const seed = defaultValueForSchema(schema)
  if (seed && typeof seed === 'object' && !Array.isArray(seed)) return { ...(seed as Record<string, unknown>) }
  return {}
}

/** Ensure an array shape. Returns the existing array reference when input
 *  already is one, otherwise a fresh empty array. */
export function ensureArrayShape(value: unknown): unknown[] {
  return Array.isArray(value) ? value.slice() : []
}

/** Ensure a string. Used for the text/number inputs that always coerce. */
function ensureString(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

/** Coerce a string the user typed into a number/integer. Returns
 *  `undefined` when the string is empty, otherwise the parsed value
 *  (NaN passes through so the form can surface it via a custom message). */
function parseNumericInput(raw: string, kind: 'number' | 'integer'): number | undefined {
  if (raw === '') return undefined
  const parsed = kind === 'integer' ? parseInt(raw, 10) : Number(raw)
  return Number.isNaN(parsed) ? Number.NaN : parsed
}

export const SchemaForm: React.FC<SchemaFormProps> = ({
  schema,
  value,
  onChange,
  rootLabel,
  rootDescription,
  path = [],
}) => {
  // Composite: object with declared properties.
  if (isObjectSchema(schema) && !isFreeFormObject(schema)) {
    const obj = ensureObjectShape(schema, value)
    const declared = declaredProperties(schema)
    const required = requiredProperties(schema)
    const requiredSet = new Set(required)

    const updateKey = (key: string, next: unknown) => {
      onChange({ ...obj, [key]: next })
    }

    return (
      <div className="schema-overlay-form schema-overlay-form--object">
        {(rootLabel || schema.title) && (
          <div className="schema-overlay-form__header">
            <FieldLabel className="schema-overlay-form__label">
              {rootLabel ?? schema.title}
            </FieldLabel>
            {(rootDescription || schema.description) && (
              <FieldDescription>
                {rootDescription ?? schema.description}
              </FieldDescription>
            )}
          </div>
        )}
        <div className="schema-overlay-form__fields">
          {declared.length === 0 && (
            <div className="schema-overlay-form__empty">No fields to fill.</div>
          )}
          {declared.map((key) => {
            const child = propertySchema(schema, key)!
            const isRequired = requiredSet.has(key)
            const childPath = [...path, key]
            return (
              <SchemaField
                key={key}
                name={key}
                required={isRequired}
                schema={child}
                value={obj[key]}
                onChange={(next) => updateKey(key, next)}
                path={childPath}
              />
            )
          })}
        </div>
      </div>
    )
  }

  // Composite: array of items.
  if (isArraySchema(schema)) {
    const items = ensureArrayShape(value)
    const elementSchema = schema.items ?? {}

    const updateIndex = (idx: number, next: unknown) => {
      const copy = items.slice()
      copy[idx] = next
      onChange(copy)
    }
    const removeAt = (idx: number) => {
      const copy = items.slice()
      copy.splice(idx, 1)
      onChange(copy)
    }
    const append = () => {
      onChange([...items, defaultValueForSchema(elementSchema)])
    }

    return (
      <div className="schema-overlay-form schema-overlay-form--array">
        {(rootLabel || schema.title) && (
          <div className="schema-overlay-form__header">
            <FieldLabel className="schema-overlay-form__label">
              {rootLabel ?? schema.title}
            </FieldLabel>
            {(rootDescription || schema.description) && (
              <FieldDescription>
                {rootDescription ?? schema.description}
              </FieldDescription>
            )}
          </div>
        )}
        <div className="schema-overlay-form__items">
          {items.length === 0 && (
            <div className="schema-overlay-form__empty">No items yet.</div>
          )}
          {items.map((item, idx) => (
            <div className="schema-overlay-form__item" key={idx}>
              <div className="schema-overlay-form__item-head">
                <span className="schema-overlay-form__item-index">#{idx + 1}</span>
                <button
                  type="button"
                  className="schema-overlay-form__remove"
                  onClick={() => removeAt(idx)}
                  aria-label={`Remove item ${idx + 1}`}
                >
                  <Trash2 size={12} />
                </button>
              </div>
              <SchemaForm
                schema={elementSchema}
                value={item}
                onChange={(next) => updateIndex(idx, next)}
                path={[...path, idx]}
              />
            </div>
          ))}
        </div>
        <button
          type="button"
          className="schema-overlay-form__add"
          onClick={append}
        >
          <Plus size={12} /> Add item
        </button>
      </div>
    )
  }

  // Composite: free-form object (no properties + additionalProperties absent
  // or boolean true). Render a JSON textarea.
  if (isFreeFormObject(schema)) {
    const text = value === undefined ? '' : safeStringify(value)
    return (
      <Field className="schema-overlay-form schema-overlay-form--freeform">
        {(rootLabel || schema.title) && (
          <FieldLabel>{rootLabel ?? schema.title}</FieldLabel>
        )}
        {(rootDescription || schema.description) && (
          <FieldDescription>{rootDescription ?? schema.description}</FieldDescription>
        )}
        <Textarea
          className="schema-overlay-form__json"
          rows={6}
          value={text}
          onChange={(e) => {
            const next = e.target.value
            if (next === '') {
              onChange({})
              return
            }
            try {
              onChange(JSON.parse(next))
            } catch {
              onChange(next)
            }
          }}
          placeholder='{"key": "value"}'
        />
      </Field>
    )
  }

  // Primitive.
  return (
    <SchemaField
      name="value"
      required={false}
      schema={schema}
      value={value}
      onChange={onChange}
      path={path}
      label={rootLabel}
      description={rootDescription}
    />
  )
}

interface SchemaFieldProps {
  name: string
  required: boolean
  schema: JSONSchema
  value: unknown
  onChange: (next: unknown) => void
  path: FieldPath
  label?: string
  description?: string
}

const SchemaField: React.FC<SchemaFieldProps> = ({
  name,
  required,
  schema,
  value,
  onChange,
  label,
  description,
}) => {
  const labelText = label ?? schema.title ?? prettify(name)
  const descriptionText = description ?? schema.description
  const requiredMark = required ? <span className="schema-overlay-form__required">*</span> : null

  // Enum (string members) → select.
  if (schema.enum && schema.enum.length > 0) {
    const current = ensureString(value)
    return (
      <Field>
        <FieldLabel>
          {labelText}
          {requiredMark}
        </FieldLabel>
        {descriptionText && <FieldDescription>{descriptionText}</FieldDescription>}
        <SelectRoot
          value={current}
          onValueChange={(next) => {
            if (next === null) return
            onChange(next)
          }}
        >
          <SelectTrigger className="w-full">
            <SelectValue placeholder="Choose…">
              {current || <span className="schema-overlay-form__placeholder">Choose…</span>}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {schema.enum.map((option) => {
              const s = String(option)
              return (
                <SelectItem key={s} value={s}>
                  <SelectItemText>{s}</SelectItemText>
                </SelectItem>
              )
            })}
          </SelectContent>
        </SelectRoot>
      </Field>
    )
  }

  // Map (object with typed additionalProperties).
  if (
    isObjectSchema(schema) &&
    schema.additionalProperties &&
    typeof schema.additionalProperties !== 'boolean'
  ) {
    const obj = value && typeof value === 'object' && !Array.isArray(value)
      ? { ...(value as Record<string, unknown>) }
      : {}
    const valueSchema = mapValueSchema(schema)!
    const updateKey = (k: string, next: unknown) => {
      const copy = { ...obj }
      if (next === undefined) delete copy[k]
      else copy[k] = next
      onChange(copy)
    }
    const removeKey = (k: string) => {
      const copy = { ...obj }
      delete copy[k]
      onChange(copy)
    }
    const addKey = () => {
      const copy = { ...obj }
      let i = 0
      let k = 'key'
      while (k in copy) {
        i += 1
        k = `key${i}`
      }
      copy[k] = defaultValueForSchema(valueSchema)
      onChange(copy)
    }
    return (
      <div className="schema-overlay-form schema-overlay-form--map">
        <FieldLabel>
          {labelText}
          {requiredMark}
        </FieldLabel>
        {descriptionText && <FieldDescription>{descriptionText}</FieldDescription>}
        <div className="schema-overlay-form__map-rows">
          {Object.keys(obj).length === 0 && (
            <div className="schema-overlay-form__empty">No entries yet.</div>
          )}
          {Object.keys(obj).map((k) => (
            <div className="schema-overlay-form__map-row" key={k}>
              <Input
                className="schema-overlay-form__map-key"
                value={k}
                onChange={(e) => {
                  const next = e.target.value
                  if (!next || next === k) return
                  const copy = { ...obj }
                  copy[next] = copy[k]
                  delete copy[k]
                  onChange(copy)
                }}
                aria-label="Map key"
              />
              <SchemaForm
                schema={valueSchema}
                value={obj[k]}
                onChange={(next) => updateKey(k, next)}
                rootLabel=""
              />
              <button
                type="button"
                className="schema-overlay-form__remove"
                onClick={() => removeKey(k)}
                aria-label={`Remove entry ${k}`}
              >
                <Trash2 size={12} />
              </button>
            </div>
          ))}
        </div>
        <button
          type="button"
          className="schema-overlay-form__add"
          onClick={addKey}
        >
          <Plus size={12} /> Add entry
        </button>
      </div>
    )
  }

  const primitive = primitiveType(schema)
  if (primitive === 'boolean') {
    return (
      <Field>
        <div className="schema-overlay-form__checkbox-row">
          <Checkbox
            checked={value === true}
            onCheckedChange={(checked) => onChange(checked === true)}
            aria-label={labelText}
          />
          <FieldLabel className="schema-overlay-form__checkbox-label">
            {labelText}
            {requiredMark}
          </FieldLabel>
        </div>
        {descriptionText && <FieldDescription>{descriptionText}</FieldDescription>}
      </Field>
    )
  }

  if (primitive === 'number' || primitive === 'integer') {
    const raw = value === undefined || value === null ? '' : String(value)
    return (
      <Field>
        <FieldLabel>
          {labelText}
          {requiredMark}
        </FieldLabel>
        {descriptionText && <FieldDescription>{descriptionText}</FieldDescription>}
        <Input
          type="number"
          step={primitive === 'integer' ? 1 : 'any'}
          value={raw}
          onChange={(e) => {
            const parsed = parseNumericInput(e.target.value, primitive)
            onChange(parsed)
          }}
          aria-label={labelText}
        />
      </Field>
    )
  }

  // Default: string input (or fall-back textarea for very long descriptions).
  const long = (schema.description?.length ?? 0) > 80
  return (
    <Field>
      <FieldLabel>
        {labelText}
        {requiredMark}
      </FieldLabel>
      {descriptionText && <FieldDescription>{descriptionText}</FieldDescription>}
      {long ? (
        <Textarea
          rows={3}
          value={ensureString(value)}
          onChange={(e) => onChange(e.target.value)}
          aria-label={labelText}
        />
      ) : (
        <Input
          value={ensureString(value)}
          onChange={(e) => onChange(e.target.value)}
          aria-label={labelText}
        />
      )}
    </Field>
  )
}

function safeStringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return ''
  }
}

/** Convert `snake_case` / `camelCase` property names to a presentable label
 *  for the form field. The result is a stable fallback when the schema
 *  carries no `title`. */
function prettify(name: string): string {
  const spaced = name.replace(/[_\-.]+/g, ' ').replace(/([a-z])([A-Z])/g, '$1 $2')
  return spaced.length === 0 ? name : spaced.charAt(0).toUpperCase() + spaced.slice(1)
}