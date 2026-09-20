import { describe, expect, it } from 'vitest'
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

describe('json-schema helpers', () => {
  describe('isObjectSchema', () => {
    it('returns true when type is object', () => {
      expect(isObjectSchema({ type: 'object' })).toBe(true)
    })

    it('returns true when type is an array containing object', () => {
      expect(isObjectSchema({ type: ['object', 'null'] })).toBe(true)
    })

    it('returns true when properties are declared without a type', () => {
      expect(isObjectSchema({ properties: { foo: { type: 'string' } } })).toBe(true)
    })

    it('returns false for scalars and arrays', () => {
      expect(isObjectSchema({ type: 'string' })).toBe(false)
      expect(isObjectSchema({ type: 'array' })).toBe(false)
      expect(isObjectSchema({ items: { type: 'string' } })).toBe(false)
    })

    it('returns false for undefined', () => {
      expect(isObjectSchema(undefined)).toBe(false)
    })
  })

  describe('isArraySchema', () => {
    it('returns true when type is array', () => {
      expect(isArraySchema({ type: 'array', items: { type: 'string' } })).toBe(true)
    })

    it('returns true when items is declared without a type', () => {
      expect(isArraySchema({ items: { type: 'string' } })).toBe(true)
    })

    it('returns false for objects and primitives', () => {
      expect(isArraySchema({ type: 'object' })).toBe(false)
      expect(isArraySchema({ type: 'string' })).toBe(false)
    })
  })

  describe('primitiveType', () => {
    it('returns the primitive type for scalar schemas', () => {
      expect(primitiveType({ type: 'string' })).toBe('string')
      expect(primitiveType({ type: 'integer' })).toBe('integer')
      expect(primitiveType({ type: 'number' })).toBe('number')
      expect(primitiveType({ type: 'boolean' })).toBe('boolean')
    })

    it('prefers string for enum schemas', () => {
      expect(primitiveType({ enum: ['a', 'b'] })).toBe('string')
    })

    it('returns null for composite types', () => {
      expect(primitiveType({ type: 'object' })).toBeNull()
      expect(primitiveType({ type: 'array' })).toBeNull()
    })

    it('picks the first primitive from a type array', () => {
      expect(primitiveType({ type: ['string', 'null'] })).toBe('string')
      expect(primitiveType({ type: ['null', 'integer'] })).toBe('integer')
    })
  })

  describe('isFreeFormObject', () => {
    it('detects free-form objects (no declared properties)', () => {
      expect(isFreeFormObject({ type: 'object' })).toBe(true)
      expect(isFreeFormObject({ type: 'object', additionalProperties: true })).toBe(true)
    })

    it('returns false when properties are declared', () => {
      const schema: JSONSchema = {
        type: 'object',
        properties: { name: { type: 'string' } },
      }
      expect(isFreeFormObject(schema)).toBe(false)
    })

    it('returns false for non-objects', () => {
      expect(isFreeFormObject({ type: 'array' })).toBe(false)
      expect(isFreeFormObject(undefined)).toBe(false)
      // Without an explicit `type: 'object'` marker, additionalProperties
      // alone is not enough to treat a schema as a free-form object.
      expect(isFreeFormObject({ additionalProperties: true })).toBe(false)
    })
  })

  describe('mapValueSchema', () => {
    it('returns the value schema of an object with typed additionalProperties', () => {
      const valueSchema: JSONSchema = { type: 'string' }
      expect(mapValueSchema({ type: 'object', additionalProperties: valueSchema })).toEqual(valueSchema)
    })

    it('returns undefined when additionalProperties is boolean or absent', () => {
      expect(mapValueSchema({ type: 'object' })).toBeUndefined()
      expect(mapValueSchema({ type: 'object', additionalProperties: true })).toBeUndefined()
    })
  })

  describe('requiredProperties', () => {
    it('returns the declared required list', () => {
      expect(requiredProperties({ type: 'object', required: ['a', 'b'] })).toEqual(['a', 'b'])
    })

    it('returns an empty array when no required list is set', () => {
      expect(requiredProperties({ type: 'object' })).toEqual([])
    })

    it('returns an empty array for undefined', () => {
      expect(requiredProperties(undefined)).toEqual([])
    })
  })

  describe('declaredProperties', () => {
    it('returns property names in insertion order', () => {
      const schema: JSONSchema = {
        type: 'object',
        properties: {
          first: { type: 'string' },
          second: { type: 'integer' },
          third: { type: 'boolean' },
        },
      }
      expect(declaredProperties(schema)).toEqual(['first', 'second', 'third'])
    })

    it('returns an empty array when no properties are declared', () => {
      expect(declaredProperties({ type: 'object' })).toEqual([])
      expect(declaredProperties(undefined)).toEqual([])
    })
  })

  describe('propertySchema', () => {
    it('returns the property schema by name', () => {
      const stringSchema: JSONSchema = { type: 'string' }
      const schema: JSONSchema = {
        type: 'object',
        properties: { name: stringSchema },
      }
      expect(propertySchema(schema, 'name')).toEqual(stringSchema)
    })

    it('returns undefined for unknown keys', () => {
      const schema: JSONSchema = {
        type: 'object',
        properties: { name: { type: 'string' } },
      }
      expect(propertySchema(schema, 'unknown')).toBeUndefined()
    })

    it('returns undefined when no properties are declared', () => {
      expect(propertySchema(undefined, 'name')).toBeUndefined()
    })
  })
})