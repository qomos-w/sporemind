import { BinaryCodec, SchemaRegistry } from '@qomos/spore-ts'
import { EncodeError, DecodeError } from '@qomos/spore-ts'
import { schemaEntries } from '../gen-clients/system/registry'
import type { ObjectDesc, TypeDesc } from '@qomos/spore-ts/schema'
import type { SchemaEntry } from '@qomos/spore-ts/registry'
import { resolveDynamicSchema } from './dynamic-schema-registry'

/**
 * Frontend BinaryCodec wrapper that encodes/decodes application payloads using
 * the generated client schema registry instead of JSON.stringify.
 *
 * The transport already uses a library-level BinaryCodec for the outer
 * callable request envelopes. This module is responsible for the inner app
 * payloads that travel inside `AppManagerInvokeReq.Payload`,
 * `AppManagerCastReq.Payload`, and `AppManagerEmitReq.Payload`.
 */

const registry = new SchemaRegistry()
for (const entry of schemaEntries) {
  registry.register(entry)
}

const codec = new BinaryCodec()

const entryByName = new Map<string, SchemaEntry>()
for (const entry of schemaEntries) {
  if (!entryByName.has(entry.name)) {
    entryByName.set(entry.name, entry)
  }
}

export { EncodeError, DecodeError }

export interface ResolvedSchema {
  type: TypeDesc
  objectFor: (name: string) => ObjectDesc | undefined
}

/** Resolve a schema by its logical name (e.g. "AuthLoginReq"). */
export function resolveSchema(schemaName: string, namespace?: string): ResolvedSchema | undefined {
  if (namespace) {
    const dynamic = resolveDynamicSchema(schemaName, namespace)
    if (dynamic) return dynamic
  }
  const entry = entryByName.get(schemaName)
  if (!entry) return resolveDynamicSchema(schemaName)
  return {
    type: entry.type,
    objectFor: registry.objectFor(entry.namespace),
  }
}

/** Encode a value using the named schema. Throws EncodeError on mismatch. */
export function encodeAppPayload(schemaName: string, value: unknown, namespace?: string): Uint8Array {
  const resolved = resolveSchema(schemaName, namespace)
  if (!resolved) {
    throw new EncodeError('', `schema ${schemaName} not found`)
  }
  return codec.encode(resolved.type, value, resolved.objectFor)
}

/** Decode TBC bytes using the named schema. Throws DecodeError on mismatch. */
export function decodeAppPayload(schemaName: string, bytes: Uint8Array, namespace?: string): unknown {
  const resolved = resolveSchema(schemaName, namespace)
  if (!resolved) {
    throw new DecodeError('', `schema ${schemaName} not found`)
  }
  return codec.decode(resolved.type, bytes, resolved.objectFor)
}

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder('utf-8', { fatal: true })

/**
 * Encode a payload for an app callable/event.
 *
 * If `schemaName` is provided and known, the value is validated and encoded
 * with the BinaryCodec. Otherwise a JSON text fallback is returned so the
 * backend can still decode the payload with its JSON fallback path.
 */
export function encodePayloadBytes(value: unknown, schemaName?: string, namespace?: string): Uint8Array {
  if (schemaName) {
    const resolved = resolveSchema(schemaName, namespace)
    if (resolved) {
      return codec.encode(resolved.type, value, resolved.objectFor)
    }
  }
  return textEncoder.encode(JSON.stringify(value ?? null))
}

/**
 * Decode payload bytes from an app callable/event response.
 *
 * If `schemaName` is provided and known, TBC decoding is attempted first.
 * Otherwise the bytes are decoded as JSON. Decoding a binary payload without
 * its response schema is intentionally not supported because the wire format
 * uses field indexes; callers should supply the response schema name when
 * binary responses are expected.
 */
export function decodePayloadBytes(bytes: Uint8Array, schemaName?: string, namespace?: string): unknown {
  if (bytes.length === 0) return undefined
  if (schemaName) {
    const resolved = resolveSchema(schemaName, namespace)
    if (resolved) {
      try {
        return codec.decode(resolved.type, bytes, resolved.objectFor)
      } catch {
        // fall through to JSON fallback
      }
    }
  }
  return JSON.parse(textDecoder.decode(bytes))
}

/**
 * Encode a payload honoring a manifest descriptor's declared Encoding.
 * 'binary' -> BinaryCodec if schema known; everything else -> JSON text.
 */
export function encodePayloadByDescriptor(
  value: unknown,
  schemaName: string | undefined,
  encoding: string | undefined,
  namespace?: string,
): Uint8Array {
  if (encoding === 'binary' && schemaName) {
    const resolved = resolveSchema(schemaName, namespace)
    if (resolved) {
      return codec.encode(resolved.type, value, resolved.objectFor)
    }
  }
  return textEncoder.encode(JSON.stringify(value ?? null))
}

/**
 * Decode payload bytes honoring a manifest descriptor's declared Encoding.
 * 'binary' -> BinaryCodec if schema known; everything else -> JSON.
 */
export function decodePayloadByDescriptor(
  bytes: Uint8Array,
  schemaName: string | undefined,
  encoding: string | undefined,
  namespace?: string,
): unknown {
  if (bytes.length === 0) return undefined
  if (encoding === 'binary' && schemaName) {
    const resolved = resolveSchema(schemaName, namespace)
    if (resolved) {
      try {
        return codec.decode(resolved.type, bytes, resolved.objectFor)
      } catch {
        // fall through to JSON fallback
      }
    }
  }
  return JSON.parse(textDecoder.decode(bytes))
}
