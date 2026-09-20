/**
 * Canonical 128-bit identity, mirroring `spore/identity/identity.go`.
 *
 * Wire layout (big-endian, 16 bytes):
 *
 *   bytes  0..5  — 48-bit millisecond timestamp
 *   bytes  6..7  — 16-bit runtime slot
 *   bytes  8..9  — 16-bit runtime incarnation
 *   bytes 10..15 — 48-bit local sequence
 *
 * The CanonicalID layout is intentionally aligned with `gospore.ActorID`,
 * so the same bytes can be reinterpreted as either identity at the boundary
 * without translation.
 */

/** Sentinel: the zero canonical id. */
export const CANONICAL_ID_LENGTH = 16;

/** Hex string form: 32 lowercase hex chars (no separators). */
export type CanonicalIDHex = string;

/** Raw byte form: exactly 16 bytes. */
export type CanonicalIDBytes = Uint8Array;

/** Construct a CanonicalID from its four logical segments. */
export function newCanonicalID(
  timestampMs: bigint,
  runtimeSlot: number,
  incarnation: number,
  sequence: bigint,
): CanonicalIDBytes {
  if (timestampMs < 0n || timestampMs > 0xffffffffffffn) {
    throw new RangeError("spore-ts: timestamp exceeds 48 bits");
  }
  if (sequence < 0n || sequence > 0xffffffffffffn) {
    throw new RangeError("spore-ts: sequence exceeds 48 bits");
  }
  if (runtimeSlot < 0 || runtimeSlot > 0xffff) {
    throw new RangeError("spore-ts: runtimeSlot exceeds 16 bits");
  }
  if (incarnation < 0 || incarnation > 0xffff) {
    throw new RangeError("spore-ts: incarnation exceeds 16 bits");
  }

  const out = new Uint8Array(16);
  writeUint48BE(out, 0, timestampMs);
  out[6] = (runtimeSlot >>> 8) & 0xff;
  out[7] = runtimeSlot & 0xff;
  out[8] = (incarnation >>> 8) & 0xff;
  out[9] = incarnation & 0xff;
  writeUint48BE(out, 10, sequence);
  return out;
}

/** Extract the 48-bit millisecond timestamp segment. */
export function timestampMs(id: CanonicalIDBytes): bigint {
  assertLen(id);
  return readUint48BE(id, 0);
}

/** Extract the 16-bit runtime slot segment. */
export function runtimeSlot(id: CanonicalIDBytes): number {
  assertLen(id);
  return (id[6]! << 8) | id[7]!;
}

/** Extract the 16-bit incarnation segment. */
export function incarnation(id: CanonicalIDBytes): number {
  assertLen(id);
  return (id[8]! << 8) | id[9]!;
}

/** Extract the 48-bit local sequence segment. */
export function sequence(id: CanonicalIDBytes): bigint {
  assertLen(id);
  return readUint48BE(id, 10);
}

/** Test whether an id is the zero value. */
export function isZero(id: CanonicalIDBytes): boolean {
  assertLen(id);
  for (let i = 0; i < 16; i++) {
    if (id[i] !== 0) return false;
  }
  return true;
}

/** Encode the identity as a 32-character lowercase hex string. */
export function toHex(id: CanonicalIDBytes): CanonicalIDHex {
  assertLen(id);
  let out = "";
  for (let i = 0; i < 16; i++) {
    out += id[i]!.toString(16).padStart(2, "0");
  }
  return out;
}

/** Decode a 32-character lowercase hex string produced by toHex. */
export function fromHex(s: CanonicalIDHex): CanonicalIDBytes {
  if (s.length !== 32) {
    throw new RangeError("spore-ts: invalid canonical id format (length)");
  }
  const out = new Uint8Array(16);
  for (let i = 0; i < 16; i++) {
    const byte = parseInt(s.substring(i * 2, i * 2 + 2), 16);
    if (Number.isNaN(byte)) {
      throw new RangeError("spore-ts: invalid canonical id format (hex)");
    }
    out[i] = byte;
  }
  return out;
}

function writeUint48BE(buf: Uint8Array, offset: number, value: bigint): void {
  buf[offset]     = Number((value >> 40n) & 0xffn);
  buf[offset + 1] = Number((value >> 32n) & 0xffn);
  buf[offset + 2] = Number((value >> 24n) & 0xffn);
  buf[offset + 3] = Number((value >> 16n) & 0xffn);
  buf[offset + 4] = Number((value >> 8n) & 0xffn);
  buf[offset + 5] = Number(value & 0xffn);
}

function readUint48BE(buf: Uint8Array, offset: number): bigint {
  return (
    (BigInt(buf[offset]!) << 40n) |
    (BigInt(buf[offset + 1]!) << 32n) |
    (BigInt(buf[offset + 2]!) << 24n) |
    (BigInt(buf[offset + 3]!) << 16n) |
    (BigInt(buf[offset + 4]!) << 8n) |
    BigInt(buf[offset + 5]!)
  );
}

function assertLen(id: CanonicalIDBytes): void {
  if (id.length !== 16) {
    throw new RangeError(`spore-ts: CanonicalID must be 16 bytes, got ${id.length}`);
  }
}
