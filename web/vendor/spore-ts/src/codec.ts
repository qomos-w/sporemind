/**
 * JSON codec for spore values.
 *
 * The wire format is plain JSON; struct objects are emitted with their
 * fields ordered alphabetically by name to match Go's `OrderedMap`-aware
 * BinaryCodec, ensuring fingerprint determinism for identical content.
 *
 * This MVP focuses on round-trip fidelity for primitives, struct objects,
 * arrays, and string-keyed maps; class shapes, interfaces, and streaming
 * descriptors are out of scope and round-trip via plain JSON.
 */

import type { ObjectDesc, TypeDesc } from "./schema.js";

/** Error thrown when a value does not conform to its TypeDesc on encode. */
export class EncodeError extends Error {
  constructor(
    public readonly path: string,
    public readonly cause?: unknown,
  ) {
    super(`spore-ts: encode error at ${path || "<root>"}${cause ? `: ${String(cause)}` : ""}`);
    this.name = "EncodeError";
  }
}

/** Error thrown when wire bytes cannot be decoded against a TypeDesc. */
export class DecodeError extends Error {
  constructor(
    public readonly path: string,
    public readonly cause?: unknown,
  ) {
    super(`spore-ts: decode error at ${path || "<root>"}${cause ? `: ${String(cause)}` : ""}`);
    this.name = "DecodeError";
  }
}

/**
 * JSON codec. Stateless; safe to share across calls.
 *
 * Struct values are matched against an ObjectDesc supplied externally
 * (typically from generated SchemaRegistry). Top-level non-struct values
 * decode to plain JS values that match the TypeDesc kind:
 *  - scalar  → number | string | boolean | null
 *  - array   → unknown[]
 *  - map     → Record<string, unknown>
 *  - struct  → object whose keys match ObjectDesc.fields
 */
export class JSONCodec {
  private readonly textEncoder = new TextEncoder();
  private readonly textDecoder = new TextDecoder("utf-8", { fatal: true });

  /** Encode a value to UTF-8 bytes. */
  encode(desc: TypeDesc, value: unknown, objectFor?: (name: string) => ObjectDesc | undefined): Uint8Array {
    const normalised = this.normaliseForEncode(desc, value, "", objectFor);
    const bytes = this.textEncoder.encode(JSON.stringify(normalised));
    // Defensive: return a Uint8Array whose byteOffset is 0 and whose buffer
    // length equals byteLength. The TextEncoder spec already guarantees this,
    // but some fetch wrappers (undici in Node, certain service-worker
    // interceptors) historically misread typed-array views with non-zero
    // byteOffset, so we copy unconditionally to keep the wire surface
    // predictable for callers passing the result to fetch.
    return new Uint8Array(bytes);
  }

  /** Decode UTF-8 bytes to a JS value. */
  decode(desc: TypeDesc, data: Uint8Array, objectFor?: (name: string) => ObjectDesc | undefined): unknown {
    let text: string;
    try {
      text = this.textDecoder.decode(data);
    } catch (err) {
      throw new DecodeError("", err);
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch (err) {
      throw new DecodeError("", err);
    }
    return this.normaliseForDecode(desc, parsed, "", objectFor);
  }

  // --- internals -----------------------------------------------------

  private normaliseForEncode(
    desc: TypeDesc,
    value: unknown,
    path: string,
    objectFor?: (name: string) => ObjectDesc | undefined,
  ): unknown {
    switch (desc.kind) {
      case "void":
        return null;
      case "scalar":
        return this.checkScalarForEncode(desc.name, value, path);
      case "array": {
        if (value === null || value === undefined) return value;
        if (!Array.isArray(value)) {
          throw new EncodeError(path, "expected array");
        }
        if (!desc.element) return value;
        return value.map((item, idx) =>
          this.normaliseForEncode(desc.element!, item, `${path}[${idx}]`, objectFor),
        );
      }
      case "map": {
        if (value === null || value === undefined) return value;
        if (!isPlainObject(value)) {
          throw new EncodeError(path, "expected object");
        }
        if (!desc.value) return value;
        const sorted = Object.keys(value).sort();
        const out: Record<string, unknown> = {};
        for (const k of sorted) {
          out[k] = this.normaliseForEncode(desc.value, value[k], `${path}.${k}`, objectFor);
        }
        return out;
      }
      case "struct":
      case "class": {
        if (value === null || value === undefined) return value;
        if (!isPlainObject(value)) {
          throw new EncodeError(path, "expected object");
        }
        const objName = desc.className || desc.name;
        if (!objName || !objectFor) {
          // Without an ObjectDesc we cannot enforce field shapes; pass
          // through and let JSON.stringify do the rest.
          return value;
        }
        const obj = objectFor(objName);
        if (!obj) {
          // Unknown struct — fall through with raw passthrough.
          return value;
        }
        const out: Record<string, unknown> = {};
        const sortedFields = [...obj.fields].sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
        for (const f of sortedFields) {
          if (f.private) continue;
          out[f.name] = this.normaliseForEncode(f.type, value[f.name], `${path}.${f.name}`, objectFor);
        }
        return out;
      }
      case "invalid":
        throw new EncodeError(path, "invalid TypeDesc");
      default:
        return value;
    }
  }

  private normaliseForDecode(
    desc: TypeDesc,
    value: unknown,
    path: string,
    objectFor?: (name: string) => ObjectDesc | undefined,
  ): unknown {
    switch (desc.kind) {
      case "void":
        return undefined;
      case "scalar":
        return this.checkScalarForDecode(desc.name, value, path);
      case "array": {
        if (!Array.isArray(value)) {
          throw new DecodeError(path, "expected array");
        }
        if (!desc.element) return value;
        return value.map((item, idx) =>
          this.normaliseForDecode(desc.element!, item, `${path}[${idx}]`, objectFor),
        );
      }
      case "map": {
        if (!isPlainObject(value)) {
          throw new DecodeError(path, "expected object");
        }
        if (!desc.value) return value;
        const out: Record<string, unknown> = {};
        for (const k of Object.keys(value).sort()) {
          out[k] = this.normaliseForDecode(desc.value, value[k], `${path}.${k}`, objectFor);
        }
        return out;
      }
      case "struct":
      case "class": {
        if (!isPlainObject(value)) {
          throw new DecodeError(path, "expected object");
        }
        const objName = desc.className || desc.name;
        if (!objName || !objectFor) {
          return value;
        }
        const obj = objectFor(objName);
        if (!obj) {
          return value;
        }
        const out: Record<string, unknown> = {};
        for (const f of obj.fields) {
          if (f.private) continue;
          out[f.name] = this.normaliseForDecode(f.type, value[f.name], `${path}.${f.name}`, objectFor);
        }
        return out;
      }
      case "invalid":
        throw new DecodeError(path, "invalid TypeDesc");
      default:
        return value;
    }
  }

  private checkScalarForEncode(name: string | undefined, value: unknown, path: string): unknown {
    return this.checkScalar(name, value, path, EncodeError);
  }

  private checkScalarForDecode(name: string | undefined, value: unknown, path: string): unknown {
    return this.checkScalar(name, value, path, DecodeError);
  }

  private checkScalar(
    name: string | undefined,
    value: unknown,
    path: string,
    ErrorType: typeof EncodeError | typeof DecodeError,
  ): unknown {
    if (value === null) return null;
    switch (name) {
      case "bool":
        if (typeof value !== "boolean") throw new ErrorType(path, "expected boolean");
        return value;
      case "string":
        if (typeof value !== "string") throw new ErrorType(path, "expected string");
        return value;
      case "byte":
      case "short":
      case "ushort":
      case "int":
      case "uint":
      case "long":
      case "ulong":
      case "float":
      case "double":
      case "int8":
      case "int16":
      case "int32":
      case "int64":
      case "uint8":
      case "uint16":
      case "uint32":
      case "uint64":
      case "float32":
      case "float64":
        if (typeof value !== "number") throw new ErrorType(path, "expected number");
        return value;
      case "bytes":
        if (typeof value === "string") return value;
        if (value instanceof Uint8Array) return Array.from(value);
        throw new ErrorType(path, "expected bytes");
      case "any":
      case undefined:
      case "":
        return value;
      default:
        return value;
    }
  }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
