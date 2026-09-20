import { describe, expect, it } from 'vitest'
import { jsonSchemaFromCallableParams } from './spore-converter'

describe('jsonSchemaFromCallableParams', () => {
  it('builds a flat object schema from a shallow Params list', () => {
    const schema = jsonSchemaFromCallableParams([
      { Name: 'Title', Type: 'string', Required: true },
      { Name: 'Count', Type: 'int' },
      { Name: 'Enabled', Type: 'bool' },
    ])
    expect(schema.type).toBe('object')
    expect(schema.required).toEqual(['Title'])
    expect(schema.properties?.Title).toEqual({ type: 'string' })
    expect(schema.properties?.Count).toEqual({ type: 'integer' })
    expect(schema.properties?.Enabled).toEqual({ type: 'boolean' })
  })

  it('attaches descriptions to property schemas', () => {
    const schema = jsonSchemaFromCallableParams([
      { Name: 'Title', Type: 'string', Description: 'the title field' },
    ])
    expect(schema.properties?.Title?.description).toBe('the title field')
  })

  it('handles array<...> and map<...,...> shorthand', () => {
    const schema = jsonSchemaFromCallableParams([
      { Name: 'Items', Type: 'array<string>' },
      { Name: 'Headers', Type: 'map<string,string>' },
    ])
    expect(schema.properties?.Items).toEqual({
      type: 'array',
      items: { type: 'string' },
    })
    expect(schema.properties?.Headers).toEqual({
      type: 'object',
      additionalProperties: { type: 'string' },
    })
  })

  it('falls back to free-form when the type is unrecognised', () => {
    const schema = jsonSchemaFromCallableParams([
      { Name: 'Notes', Type: 'something-weird' },
    ])
    expect(schema.properties?.Notes).toEqual({})
  })

  it('emits an empty required array when no field is required', () => {
    const schema = jsonSchemaFromCallableParams([
      { Name: 'Foo', Type: 'string' },
      { Name: 'Bar', Type: 'string' },
    ])
    expect(schema.required).toBeUndefined()
  })
})