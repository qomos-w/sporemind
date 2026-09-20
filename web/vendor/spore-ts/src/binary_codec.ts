/**
 * Binary codec for spore values.
 *
 * Wire format mirrors `transport.BinaryCodec` on the Go side: the fixed magic
 * `"TBC"` followed by an explicit wire-version byte (currently `0x03`), then a
 * self-describing tag stream. Tags identify the value's wire shape; the
 * supplied {@link TypeDesc} only chooses the JS representation during decode
 * (scalar `bytes` → Uint8Array vs number[], scalar `int` → number vs bigint,
 * etc.).
 *
 * Numeric range:
 *   - `int`, `byte`, `short`, `int32`, `uint32` decode to `number`
 *   - `int64`, `uint64`, `long`, `ulong` decode to `number` when |v| < 2^53,
 *     otherwise to `bigint` to preserve fidelity. Encode accepts either.
 *   - `float`, `double`, `float32`, `float64` always decode to `number`.
 *
 * Struct field order on the wire is alphabetical (matching Go's projection),
 * encoded as a map (tag 0x0A). ObjectDesc is required for struct/class
 * encode/decode; without it, a struct falls through as a plain string-keyed
 * map.
 */

import type { ObjectDesc, TypeDesc } from "./schema.js";
import { DecodeError, EncodeError } from "./codec.js";

const BINARY_MAGIC = new Uint8Array([0x54, 0x42, 0x43]); // "TBC"
// Wire version, written as the byte immediately after the magic. The version
// is an explicit header field (decoupled from the magic bytes) and is the sole
// determinant of frame acceptance — mirrors Go's binaryWireVersion.
const BINARY_WIRE_VERSION = 0x03;
// The version that used to be glued onto the magic ("TBC\x02"). Recognised only
// so the decoder can reject it with a precise error; no legacy frames are in
// flight (the only TBC consumer is the same-binary child-actor response path).
const BINARY_LEGACY_WIRE_VERSION = 0x02;

const TAG_NULL = 0x00;
const TAG_BOOL_FALSE = 0x01;
const TAG_BOOL_TRUE = 0x02;
const TAG_INT32 = 0x03;
const TAG_INT64 = 0x04;
const TAG_UINT64 = 0x05;
const TAG_FLOAT64 = 0x06;
const TAG_STRING = 0x07;
const TAG_BYTES = 0x08;
const TAG_ARRAY = 0x09;
const TAG_MAP = 0x0a;
const TAG_ENTRY_SEQUENCE = 0x0b;
const TAG_STRUCT = 0x0c;

const MAX_SAFE_BIG = BigInt(Number.MAX_SAFE_INTEGER);
const MIN_SAFE_BIG = BigInt(Number.MIN_SAFE_INTEGER);

/**
 * BinaryCodec. Stateless; safe to share across calls.
 *
 * Symmetric to `transport.BinaryCodec` (Go). The codec is schema-aware in
 * the same sense as JSONCodec: encode validates against the supplied
 * TypeDesc, decode canonicalises scalars by name (so `bytes` returns
 * Uint8Array, `string` returns string, etc.).
 */
export class BinaryCodec {
  encode(desc: TypeDesc, value: unknown, objectFor?: (name: string) => ObjectDesc | undefined): Uint8Array {
    const projected = projectForEncode(desc, value, "", objectFor);
    const w = new BinaryWriter();
    w.writeBytes(BINARY_MAGIC);
    w.writeByte(BINARY_WIRE_VERSION);
    encodeValue(w, desc, projected, "", objectFor);
    return w.finish();
  }

  decode(desc: TypeDesc, data: Uint8Array, objectFor?: (name: string) => ObjectDesc | undefined): unknown {
    const payload = parseHeader(data);
    const r = new BinaryReader(data, payload);
    const raw = decodeValue(r, "");
    if (!r.done()) {
      throw new DecodeError("", "binary decode: trailing bytes");
    }
    return canonicalize(desc, raw, "", objectFor);
  }
}

/**
 * Validate the fixed magic preamble and the explicit wire version, returning
 * the payload offset (header length). Frame acceptance is decided by the
 * declared version byte alone — there is no payload-shape sniffing.
 */
function parseHeader(data: Uint8Array): number {
  const headerLen = BINARY_MAGIC.length + 1;
  if (data.length < headerLen) {
    throw new DecodeError("", "binary decode: truncated header");
  }
  for (let i = 0; i < BINARY_MAGIC.length; i++) {
    if (data[i] !== BINARY_MAGIC[i]) {
      throw new DecodeError("", "binary decode: invalid magic");
    }
  }
  const version = data[BINARY_MAGIC.length];
  if (version === BINARY_WIRE_VERSION) {
    return headerLen;
  }
  if (version === BINARY_LEGACY_WIRE_VERSION) {
    throw new DecodeError("", `binary decode: unsupported legacy wire version ${version} (want ${BINARY_WIRE_VERSION})`);
  }
  throw new DecodeError("", `binary decode: unsupported wire version ${version} (want ${BINARY_WIRE_VERSION})`);
}

/** Build a mapping from field_N → logical field name for schema-typed structs. */
function buildFieldIndexMap(objectFor: ((name: string) => ObjectDesc | undefined) | undefined, desc: TypeDesc): Map<string, string> | undefined {
  if (!objectFor || !(desc.classId || desc.className)) return undefined;
  const objName = desc.className || desc.name;
  if (!objName) return undefined;
  const obj = objectFor(objName);
  if (!obj) return undefined;
  const fields = [...obj.fields].filter((f) => !f.private).sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
  const m = new Map<string, string>();
  for (let i = 0; i < fields.length; i++) {
    m.set(`field_${i}`, fields[i]!.name);
  }
  return m;
}

// ============================================================================
// Encode path
// ============================================================================

function projectForEncode(
  desc: TypeDesc,
  value: unknown,
  path: string,
  objectFor?: (name: string) => ObjectDesc | undefined,
): unknown {
  switch (desc.kind) {
    case "void":
      return null;
    case "scalar":
      return validateScalarForEncode(desc.name, value, path);
    case "array": {
      if (value === null || value === undefined) return value;
      if (!Array.isArray(value)) throw new EncodeError(path, "expected array");
      if (!desc.element) return value;
      return value.map((item, i) => projectForEncode(desc.element!, item, `${path}[${i}]`, objectFor));
    }
    case "map": {
      if (value === null || value === undefined) return value;
      // Map is encoded as TAG_MAP. Caller may pass:
      //   - Plain object {k: v, ...}
      //   - Array of {Key, Value} entries (ordered/sorted-ordered map projection)
      // Both shapes pass through; encodeValue picks the right tag.
      if (Array.isArray(value)) {
        // Entry sequence — leave as-is, encodeValue handles tag selection.
        return value;
      }
      if (!isPlainObject(value)) throw new EncodeError(path, "expected object or entry array");
      if (!desc.value) return value;
      const out: Record<string, unknown> = {};
      for (const k of Object.keys(value)) {
        out[k] = projectForEncode(desc.value, value[k], `${path}.${k}`, objectFor);
      }
      return out;
    }
    case "struct":
    case "class": {
      if (value === null || value === undefined) return value;
      if (!isPlainObject(value)) throw new EncodeError(path, "expected object");
      const objName = desc.className || desc.name;
      if (!objName || !objectFor) return value;
      const obj = objectFor(objName);
      if (!obj) return value;
      const out: Record<string, unknown> = {};
      for (const f of obj.fields) {
        if (f.private) continue;
        out[f.name] = projectForEncode(f.type, value[f.name], `${path}.${f.name}`, objectFor);
      }
      return out;
    }
    case "media": {
      if (value === null || value === undefined) return value;
      if (!isPlainObject(value)) throw new EncodeError(path, "expected {mime, src} object");
      return validateMediaForEncode(value, path);
    }
    default:
      return value;
  }
}

const MEDIA_INLINE_LIMIT = 1 << 20; // 1 MiB — mirrors schema.MediaInlineLimit

// validateMediaForEncode projects a media value to exactly {mime, src} and
// enforces the transport contract: type/subtype mime, data:/file:/https: src,
// and the 1 MiB inline data: cap. Mirrors schema.ValidateMediaValue.
function validateMediaForEncode(value: Record<string, unknown>, path: string): { mime: string; src: string } {
  const mime = value["mime"];
  const src = value["src"];
  if (typeof mime !== "string" || mime === "") {
    throw new EncodeError(path, 'media value is missing the "mime" field');
  }
  if (!mime.includes("/") || mime.startsWith("/") || mime.endsWith("/") || /[ \t\r\n]/.test(mime)) {
    throw new EncodeError(path, `media mime ${JSON.stringify(mime)} is not a valid type/subtype pair`);
  }
  if (typeof src !== "string" || src === "") {
    throw new EncodeError(path, 'media value is missing the "src" field');
  }
  if (src.startsWith("data:")) {
    if (src.length > MEDIA_INLINE_LIMIT) {
      throw new EncodeError(path, `inline media data: URL is ${src.length} bytes, exceeding the ${MEDIA_INLINE_LIMIT} byte limit`);
    }
  } else if (!src.startsWith("file:") && !src.startsWith("https:")) {
    throw new EncodeError(path, "media src must be a data:, file:, or https: reference");
  }
  return { mime, src };
}

function validateScalarForEncode(name: string | undefined, value: unknown, path: string): unknown {
  if (value === null || value === undefined) return null;
  switch (name) {
    case "bool":
      if (typeof value !== "boolean") throw new EncodeError(path, "expected boolean");
      return value;
    case "string":
      if (typeof value !== "string") throw new EncodeError(path, "expected string");
      return value;
    case "bytes":
      if (value instanceof Uint8Array) return value;
      if (Array.isArray(value)) return Uint8Array.from(value as number[]);
      if (typeof value === "string") {
        // For symmetry with JSONCodec which accepts string-as-bytes.
        return new TextEncoder().encode(value);
      }
      throw new EncodeError(path, "expected Uint8Array, number[], or string for bytes");
    case "float":
    case "double":
    case "float32":
    case "float64":
      if (typeof value !== "number") throw new EncodeError(path, "expected number");
      return value;
    case "byte":
    case "short":
    case "int":
    case "int8":
    case "int16":
    case "int32":
      if (typeof value === "number") return value | 0;
      if (typeof value === "bigint") return Number(value) | 0;
      throw new EncodeError(path, "expected number");
    case "ushort":
    case "uint":
    case "uint8":
    case "uint16":
    case "uint32":
      if (typeof value === "number") return value >>> 0;
      if (typeof value === "bigint") return Number(value) >>> 0;
      throw new EncodeError(path, "expected number");
    case "long":
    case "int64":
      if (typeof value === "bigint") return value;
      if (typeof value === "number") {
        if (!Number.isInteger(value)) throw new EncodeError(path, "expected integer, got non-integer number");
        return value;
      }
      throw new EncodeError(path, "expected number or bigint");
    case "ulong":
    case "uint64":
      if (typeof value === "bigint") return value;
      if (typeof value === "number") {
        if (!Number.isInteger(value) || value < 0) throw new EncodeError(path, "expected non-negative integer");
        return value;
      }
      throw new EncodeError(path, "expected number or bigint");
    case "any":
    case undefined:
    case "":
      return value;
    default:
      return value;
  }
}

function encodeValue(
  w: BinaryWriter,
  desc: TypeDesc,
  value: unknown,
  path: string,
  objectFor?: (name: string) => ObjectDesc | undefined,
): void {
  if (value === null || value === undefined) {
    w.writeByte(TAG_NULL);
    return;
  }

  if (desc.kind === "scalar") {
    encodeScalar(w, desc.name, value, path);
    return;
  }

  if (desc.kind === "void") {
    w.writeByte(TAG_NULL);
    return;
  }

  if (desc.kind === "array") {
    if (!Array.isArray(value)) throw new EncodeError(path, "expected array");
    w.writeByte(TAG_ARRAY);
    w.writeUvarint(BigInt(value.length));
    const elem = desc.element ?? { kind: "scalar", name: "any" };
    for (let i = 0; i < value.length; i++) {
      encodeValue(w, elem, value[i], `${path}[${i}]`, objectFor);
    }
    return;
  }

  if (desc.kind === "map") {
    // Entry-sequence shape: array of {Key, Value}.
    if (Array.isArray(value)) {
      w.writeByte(TAG_ENTRY_SEQUENCE);
      w.writeUvarint(BigInt(value.length));
      const keyDesc = desc.key ?? { kind: "scalar", name: "any" };
      const valDesc = desc.value ?? { kind: "scalar", name: "any" };
      for (let i = 0; i < value.length; i++) {
        const entry = value[i] as { Key: unknown; Value: unknown } | null;
        if (!entry || typeof entry !== "object" || !("Key" in entry) || !("Value" in entry)) {
          throw new EncodeError(`${path}[${i}]`, "expected {Key, Value} entry");
        }
        encodeValue(w, keyDesc, entry.Key, `${path}[${i}].Key`, objectFor);
        encodeValue(w, valDesc, entry.Value, `${path}[${i}].Value`, objectFor);
      }
      return;
    }
    if (!isPlainObject(value)) throw new EncodeError(path, "expected object");
    w.writeByte(TAG_MAP);
    const keys = Object.keys(value).sort();
    w.writeUvarint(BigInt(keys.length));
    const valDesc = desc.value ?? { kind: "scalar", name: "any" };
    for (const k of keys) {
      w.writeStringRaw(k);
      encodeValue(w, valDesc, value[k], `${path}.${k}`, objectFor);
    }
    return;
  }

  if (desc.kind === "struct" || desc.kind === "class") {
    if (!isPlainObject(value)) throw new EncodeError(path, "expected object");
    const objName = desc.className || desc.name;
    const obj = objName && objectFor ? objectFor(objName) : undefined;
    if (!obj) {
      // No schema → emit as untyped map (matches JSONCodec passthrough).
      encodeUntypedAny(w, value);
      return;
    }
    const fields = [...obj.fields].filter((f) => !f.private).sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
    const classId = desc.classId ?? 0;
    w.writeByte(TAG_STRUCT);
    w.writeUvarint(BigInt(classId));
    w.writeUvarint(BigInt(fields.length));
    if (classId === 0) {
      // Anonymous struct: string-key fallback.
      for (const f of fields) {
        w.writeStringRaw(f.name);
        encodeValue(w, f.type, value[f.name], `${path}.${f.name}`, objectFor);
      }
    } else {
      // Schema-typed struct: field-index encoding.
      for (let idx = 0; idx < fields.length; idx++) {
        const f = fields[idx]!;
        w.writeUvarint(BigInt(idx));
        encodeValue(w, f.type, value[f.name], `${path}.${f.name}`, objectFor);
      }
    }
    return;
  }

  // Fallback: untyped passthrough.
  encodeUntypedAny(w, value);
}

function encodeScalar(w: BinaryWriter, name: string | undefined, value: unknown, path: string): void {
  if (value === null || value === undefined) {
    w.writeByte(TAG_NULL);
    return;
  }
  switch (name) {
    case "bool":
      w.writeByte(value ? TAG_BOOL_TRUE : TAG_BOOL_FALSE);
      return;
    case "string":
      if (typeof value !== "string") throw new EncodeError(path, "expected string");
      w.writeByte(TAG_STRING);
      w.writeStringRaw(value);
      return;
    case "bytes": {
      if (!(value instanceof Uint8Array)) throw new EncodeError(path, "expected Uint8Array");
      w.writeByte(TAG_BYTES);
      w.writeUvarint(BigInt(value.length));
      w.writeBytes(value);
      return;
    }
    case "byte":
    case "short":
    case "int":
    case "int8":
    case "int16":
    case "int32": {
      const n = typeof value === "bigint" ? Number(value) : (value as number);
      if (!Number.isFinite(n)) throw new EncodeError(path, "non-finite number");
      w.writeByte(TAG_INT32);
      w.writeInt32(n | 0);
      return;
    }
    case "ushort":
    case "uint":
    case "uint8":
    case "uint16":
    case "uint32": {
      const n = typeof value === "bigint" ? Number(value) : (value as number);
      if (!Number.isFinite(n)) throw new EncodeError(path, "non-finite number");
      w.writeByte(TAG_UINT64);
      w.writeUint64Num(n >>> 0);
      return;
    }
    case "long":
    case "int64": {
      w.writeByte(TAG_INT64);
      if (typeof value === "bigint") w.writeInt64Big(value);
      else if (typeof value === "number") w.writeInt64Num(value as number);
      else throw new EncodeError(path, "expected number or bigint");
      return;
    }
    case "ulong":
    case "uint64": {
      w.writeByte(TAG_UINT64);
      if (typeof value === "bigint") w.writeUint64Big(value);
      else if (typeof value === "number") w.writeUint64Num(value as number);
      else throw new EncodeError(path, "expected number or bigint");
      return;
    }
    case "float":
    case "double":
    case "float32":
    case "float64": {
      if (typeof value !== "number") throw new EncodeError(path, "expected number");
      w.writeByte(TAG_FLOAT64);
      w.writeFloat64(value);
      return;
    }
    case "any":
    case undefined:
    case "":
      encodeUntypedAny(w, value);
      return;
    default:
      encodeUntypedAny(w, value);
      return;
  }
}

function encodeUntypedAny(w: BinaryWriter, value: unknown): void {
  if (value === null || value === undefined) {
    w.writeByte(TAG_NULL);
    return;
  }
  if (typeof value === "boolean") {
    w.writeByte(value ? TAG_BOOL_TRUE : TAG_BOOL_FALSE);
    return;
  }
  if (typeof value === "string") {
    w.writeByte(TAG_STRING);
    w.writeStringRaw(value);
    return;
  }
  if (value instanceof Uint8Array) {
    w.writeByte(TAG_BYTES);
    w.writeUvarint(BigInt(value.length));
    w.writeBytes(value);
    return;
  }
  if (typeof value === "bigint") {
    // Default bigint → int64.
    w.writeByte(TAG_INT64);
    w.writeInt64Big(value);
    return;
  }
  if (typeof value === "number") {
    if (Number.isInteger(value) && value >= -0x80000000 && value <= 0x7fffffff) {
      w.writeByte(TAG_INT32);
      w.writeInt32(value);
    } else {
      w.writeByte(TAG_FLOAT64);
      w.writeFloat64(value);
    }
    return;
  }
  if (Array.isArray(value)) {
    w.writeByte(TAG_ARRAY);
    w.writeUvarint(BigInt(value.length));
    for (const item of value) encodeUntypedAny(w, item);
    return;
  }
  if (isPlainObject(value)) {
    const keys = Object.keys(value).sort();
    w.writeByte(TAG_MAP);
    w.writeUvarint(BigInt(keys.length));
    for (const k of keys) {
      w.writeStringRaw(k);
      encodeUntypedAny(w, value[k]);
    }
    return;
  }
  w.writeByte(TAG_NULL);
}

// ============================================================================
// Decode path
// ============================================================================

function decodeValue(r: BinaryReader, path: string): unknown {
  const tag = r.readByte();
  switch (tag) {
    case TAG_NULL:
      return null;
    case TAG_BOOL_FALSE:
      return false;
    case TAG_BOOL_TRUE:
      return true;
    case TAG_INT32:
      return r.readInt32();
    case TAG_INT64:
      return r.readInt64();
    case TAG_UINT64:
      return r.readUint64();
    case TAG_FLOAT64:
      return r.readFloat64();
    case TAG_STRING:
      return r.readStringRaw();
    case TAG_BYTES:
      return r.readBytesRaw();
    case TAG_ARRAY: {
      const n = Number(r.readUvarint());
      const out = new Array<unknown>(n);
      for (let i = 0; i < n; i++) out[i] = decodeValue(r, `${path}[${i}]`);
      return out;
    }
    case TAG_MAP: {
      const n = Number(r.readUvarint());
      const out: Record<string, unknown> = {};
      for (let i = 0; i < n; i++) {
        const k = r.readStringRaw();
        out[k] = decodeValue(r, `${path}.${k}`);
      }
      return out;
    }
    case TAG_ENTRY_SEQUENCE: {
      const n = Number(r.readUvarint());
      const out = new Array<{ Key: unknown; Value: unknown }>(n);
      for (let i = 0; i < n; i++) {
        const k = decodeValue(r, `${path}[${i}].Key`);
        const v = decodeValue(r, `${path}[${i}].Value`);
        out[i] = { Key: k, Value: v };
      }
      return out;
    }
    case TAG_STRUCT: {
      const schemaId = Number(r.readUvarint());
      const fieldCount = Number(r.readUvarint());
      const out: Record<string, unknown> = {};
      if (schemaId === 0) {
        // Anonymous struct: string-key encoding.
        for (let i = 0; i < fieldCount; i++) {
          const key = r.readStringRaw();
          out[key] = decodeValue(r, `${path}.${key}`);
        }
      } else {
        // Schema-typed: field-index encoding (names synthesized).
        for (let i = 0; i < fieldCount; i++) {
          const fieldIdx = Number(r.readUvarint());
          out[`field_${fieldIdx}`] = decodeValue(r, `${path}.field_${fieldIdx}`);
        }
      }
      return out;
    }
    default:
      throw new DecodeError(path, `invalid tag 0x${tag.toString(16).padStart(2, "0")}`);
  }
}

function canonicalize(
  desc: TypeDesc,
  raw: unknown,
  path: string,
  objectFor?: (name: string) => ObjectDesc | undefined,
): unknown {
  switch (desc.kind) {
    case "void":
      return undefined;
    case "scalar":
      return canonicalizeScalar(desc.name, raw, path);
    case "array": {
      if (raw === null || raw === undefined) return null;
	      if (!Array.isArray(raw)) throw new DecodeError(path, "expected array");
      if (!desc.element) return raw;
      return raw.map((item, i) => canonicalize(desc.element!, item, `${path}[${i}]`, objectFor));
    }
    case "map": {
      if (raw === null || raw === undefined) return null;
      if (Array.isArray(raw)) {
        // Entry sequence — preserve as array of {Key, Value} with values
        // canonicalised against desc.value.
        if (!desc.value) return raw;
        return raw.map((entry, i) => {
          const e = entry as { Key: unknown; Value: unknown };
          return {
            Key: desc.key ? canonicalize(desc.key, e.Key, `${path}[${i}].Key`, objectFor) : e.Key,
            Value: canonicalize(desc.value!, e.Value, `${path}[${i}].Value`, objectFor),
          };
        });
      }
      if (!isPlainObject(raw)) throw new DecodeError(path, "expected object or entry array");
      if (!desc.value) return raw;
      const out: Record<string, unknown> = {};
      for (const k of Object.keys(raw)) {
        out[k] = canonicalize(desc.value, raw[k], `${path}.${k}`, objectFor);
      }
      return out;
    }
    case "struct":
    case "class": {
      if (raw === null || raw === undefined) return null;
      if (!isPlainObject(raw)) throw new DecodeError(path, "expected object");
      const objName = desc.className || desc.name;
      if (!objName || !objectFor) return raw;
      const obj = objectFor(objName);
      if (!obj) return raw;
      // Build field index map for TAG_STRUCT field_N → logical name resolution.
      const fieldMap = (desc.classId || desc.className) ? buildFieldIndexMap(objectFor, desc) : undefined;
      const out: Record<string, unknown> = {};
      for (const f of obj.fields) {
        if (f.private) continue;
        // Try logical name first, then field_N alias for TAG_STRUCT payloads.
        let val = raw[f.name];
        if (val === undefined && fieldMap) {
          for (const [alias, logical] of fieldMap) {
            if (logical === f.name) { val = raw[alias]; break; }
          }
        }
        out[f.name] = canonicalize(f.type, val, `${path}.${f.name}`, objectFor);
      }
      return out;
    }
    case "media": {
      if (raw === null || raw === undefined) return null;
      if (!isPlainObject(raw)) throw new DecodeError(path, "expected {mime, src} object");
      try {
        return validateMediaForEncode(raw, path);
      } catch (err) {
        throw new DecodeError(path, err instanceof EncodeError ? err.message : "invalid media value");
      }
    }
    default:
      return raw;
  }
}

function canonicalizeScalar(name: string | undefined, raw: unknown, path: string): unknown {
  if (raw === null || raw === undefined) return null;
  switch (name) {
    case "bool":
      if (typeof raw !== "boolean") throw new DecodeError(path, "expected bool");
      return raw;
    case "string":
      if (typeof raw !== "string") throw new DecodeError(path, "expected string");
      return raw;
    case "bytes":
      if (raw instanceof Uint8Array) return raw;
      throw new DecodeError(path, "expected bytes");
    case "byte":
    case "short":
    case "ushort":
    case "int":
    case "uint":
    case "int8":
    case "int16":
    case "int32":
    case "uint8":
    case "uint16":
    case "uint32":
      if (typeof raw === "number") return raw;
      if (typeof raw === "bigint") return Number(raw);
      throw new DecodeError(path, "expected int-compatible number");
    case "long":
    case "ulong":
    case "int64":
    case "uint64":
      // Keep bigint when out of safe range; downgrade to number otherwise.
      if (typeof raw === "bigint") {
        if (raw >= MIN_SAFE_BIG && raw <= MAX_SAFE_BIG) return Number(raw);
        return raw;
      }
      if (typeof raw === "number") return raw;
      throw new DecodeError(path, "expected int64-compatible value");
    case "float":
    case "double":
    case "float32":
    case "float64":
      if (typeof raw === "number") return raw;
      if (typeof raw === "bigint") return Number(raw);
      throw new DecodeError(path, "expected float-compatible number");
    case "any":
    case undefined:
    case "":
      return raw;
    default:
      return raw;
  }
}

// ============================================================================
// Binary primitives
// ============================================================================

class BinaryWriter {
  private chunks: Uint8Array[] = [];
  private len = 0;

  writeByte(b: number): void {
    this.chunks.push(new Uint8Array([b & 0xff]));
    this.len += 1;
  }

  writeBytes(data: Uint8Array): void {
    // Copy so subsequent caller mutations don't affect the output.
    const copy = new Uint8Array(data.byteLength);
    copy.set(data);
    this.chunks.push(copy);
    this.len += copy.byteLength;
  }

  writeUvarint(v: bigint): void {
    if (v < 0n) throw new EncodeError("", "negative uvarint");
    const out: number[] = [];
    while (v >= 0x80n) {
      out.push(Number(v & 0x7fn) | 0x80);
      v >>= 7n;
    }
    out.push(Number(v));
    this.chunks.push(Uint8Array.from(out));
    this.len += out.length;
  }

  writeInt32(v: number): void {
    const buf = new ArrayBuffer(4);
    new DataView(buf).setInt32(0, v, false); // big-endian
    this.chunks.push(new Uint8Array(buf));
    this.len += 4;
  }

  writeUint32(v: number): void {
    const buf = new ArrayBuffer(4);
    new DataView(buf).setUint32(0, v >>> 0, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 4;
  }

  writeInt64Big(v: bigint): void {
    const buf = new ArrayBuffer(8);
    new DataView(buf).setBigInt64(0, v, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 8;
  }

  writeInt64Num(v: number): void {
    this.writeInt64Big(BigInt(Math.trunc(v)));
  }

  writeUint64Big(v: bigint): void {
    const buf = new ArrayBuffer(8);
    new DataView(buf).setBigUint64(0, v < 0n ? BigInt.asUintN(64, v) : v, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 8;
  }

  writeUint64Num(v: number): void {
    this.writeUint64Big(BigInt(Math.trunc(v)));
  }

  writeFloat64(v: number): void {
    const buf = new ArrayBuffer(8);
    new DataView(buf).setFloat64(0, v, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 8;
  }

  /** Length-prefixed UTF-8 string. */
  writeStringRaw(s: string): void {
    const bytes = new TextEncoder().encode(s);
    this.writeUvarint(BigInt(bytes.length));
    this.chunks.push(bytes);
    this.len += bytes.byteLength;
  }

  finish(): Uint8Array {
    const out = new Uint8Array(this.len);
    let off = 0;
    for (const c of this.chunks) {
      out.set(c, off);
      off += c.byteLength;
    }
    return out;
  }
}

class BinaryReader {
  private view: DataView;
  private off: number;
  private end: number;
  private decoder = new TextDecoder("utf-8", { fatal: true });

  constructor(private data: Uint8Array, start: number) {
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    this.off = start;
    this.end = data.byteLength;
  }

  done(): boolean {
    return this.off >= this.end;
  }

  private ensure(n: number): void {
    if (this.off + n > this.end) throw new DecodeError("", "unexpected EOF");
  }

  readByte(): number {
    this.ensure(1);
    return this.view.getUint8(this.off++);
  }

  readUvarint(): bigint {
    let result = 0n;
    let shift = 0n;
    for (;;) {
      if (this.off >= this.end) throw new DecodeError("", "unexpected EOF in uvarint");
      const b = this.view.getUint8(this.off++);
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) return result;
      shift += 7n;
      if (shift >= 70n) throw new DecodeError("", "invalid varint");
    }
  }

  readInt32(): number {
    this.ensure(4);
    const v = this.view.getInt32(this.off, false);
    this.off += 4;
    return v;
  }

  readInt64(): number | bigint {
    this.ensure(8);
    const v = this.view.getBigInt64(this.off, false);
    this.off += 8;
    if (v >= MIN_SAFE_BIG && v <= MAX_SAFE_BIG) return Number(v);
    return v;
  }

  readUint64(): number | bigint {
    this.ensure(8);
    const v = this.view.getBigUint64(this.off, false);
    this.off += 8;
    if (v <= MAX_SAFE_BIG) return Number(v);
    return v;
  }

  readFloat64(): number {
    this.ensure(8);
    const v = this.view.getFloat64(this.off, false);
    this.off += 8;
    return v;
  }

  readStringRaw(): string {
    const len = Number(this.readUvarint());
    this.ensure(len);
    const slice = new Uint8Array(this.data.buffer, this.data.byteOffset + this.off, len);
    const s = this.decoder.decode(slice);
    this.off += len;
    return s;
  }

  readBytesRaw(): Uint8Array {
    const len = Number(this.readUvarint());
    this.ensure(len);
    // Copy out so the returned buffer is independent of the input.
    const out = new Uint8Array(len);
    out.set(new Uint8Array(this.data.buffer, this.data.byteOffset + this.off, len));
    this.off += len;
    return out;
  }
}

// ============================================================================
// Misc
// ============================================================================

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value) && !(value instanceof Uint8Array);
}
