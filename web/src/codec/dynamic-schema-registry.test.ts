import { beforeEach, describe, expect, it } from 'vitest'
import { decodeAppPayload, encodeAppPayload, resolveSchema } from './binary-codec'
import { removeAppSchemas, replaceAllAppSchemas, replaceAppSchemas } from './dynamic-schema-registry'
import type { AppObjectDescriptor } from '../gen-types/app'

function descriptor(schemaId: number): AppObjectDescriptor {
  return {
    Kind: 'struct',
    Name: 'DynamicPayload',
    SchemaId: schemaId,
    Fields: [{ Name: 'Value', Type: { Kind: 'scalar', Name: 'string' } }],
  }
}

describe('dynamic app schema registry', () => {
  beforeEach(() => replaceAllAppSchemas([]))

  it('encodes and decodes a descriptor loaded for one app', () => {
    replaceAppSchemas('app.one', 'one', { DynamicPayload: descriptor(9001) })
    const bytes = encodeAppPayload('DynamicPayload', { Value: 'hello' })
    expect(decodeAppPayload('DynamicPayload', bytes)).toEqual({ Value: 'hello' })
  })

  it('isolates duplicate names by namespace', () => {
    replaceAppSchemas('app.one', 'one', { DynamicPayload: descriptor(9001) })
    replaceAppSchemas('app.two', 'two', { DynamicPayload: descriptor(9001) })
    expect(resolveSchema('DynamicPayload')).toBeUndefined()
    expect(resolveSchema('DynamicPayload', 'one')).toBeDefined()
    expect(resolveSchema('DynamicPayload', 'two')).toBeDefined()
  })

  it('prefers a namespace-qualified app schema over a static schema with the same name', () => {
    const appDescriptor = descriptor(9003)
    appDescriptor.Name = 'AuthLoginReq'
    replaceAppSchemas('app.one', 'one', { AuthLoginReq: appDescriptor })
    expect(resolveSchema('AuthLoginReq', 'one')?.type.name).toBe('AuthLoginReq')
    expect(encodeAppPayload('AuthLoginReq', { Value: 'app' }, 'one')).toBeInstanceOf(Uint8Array)
  })

  it('replaces snapshots and removes unloaded schemas', () => {
    replaceAppSchemas('app.one', 'one', { DynamicPayload: descriptor(9001) })
    expect(resolveSchema('DynamicPayload')).toBeDefined()
    removeAppSchemas('app.one')
    expect(resolveSchema('DynamicPayload')).toBeUndefined()

    replaceAllAppSchemas([{ id: 'app.two', namespace: 'two', schemaDescriptors: { DynamicPayload: descriptor(9002) } }])
    expect(resolveSchema('DynamicPayload', 'two')).toBeDefined()
    replaceAllAppSchemas([])
    expect(resolveSchema('DynamicPayload', 'two')).toBeUndefined()
  })
})
