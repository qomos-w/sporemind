import { describe, expect, it } from 'vitest'
import { defaultValueForSchema } from './SchemaForm'
import type { JSONSchema } from './json-schema'

describe('defaultValueForSchema', () => {
  it('returns the schema default when present', () => {
    const schema: JSONSchema = { type: 'string', default: 'hello' }
    expect(defaultValueForSchema(schema)).toBe('hello')
  })

  it('returns the first enum member when declared', () => {
    const schema: JSONSchema = { enum: ['a', 'b', 'c'] }
    expect(defaultValueForSchema(schema)).toBe('a')
  })

  it('returns an empty object for object schemas with declared properties', () => {
    const schema: JSONSchema = {
      type: 'object',
      properties: {
        name: { type: 'string' },
        age: { type: 'integer' },
      },
    }
    const result = defaultValueForSchema(schema) as Record<string, unknown>
    expect(result).toBeTypeOf('object')
    expect(result.name).toBe('')
    expect(result.age).toBe(0)
  })

  it('returns an empty array for arrays', () => {
    expect(defaultValueForSchema({ type: 'array', items: { type: 'string' } })).toEqual([])
  })

  it('returns sensible primitive defaults', () => {
    expect(defaultValueForSchema({ type: 'boolean' })).toBe(false)
    expect(defaultValueForSchema({ type: 'number' })).toBe(0)
    expect(defaultValueForSchema({ type: 'integer' })).toBe(0)
    expect(defaultValueForSchema({ type: 'string' })).toBe('')
  })

  it('returns undefined for an unrenderable schema', () => {
    expect(defaultValueForSchema({})).toBeUndefined()
  })
})