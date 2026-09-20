const MAGIC = new Uint8Array([0x47, 0x53, 0x46, 0x01]);
const VERSION = 2;

/** How the Payload body is encoded. */
export enum PayloadEncoding {
  JSON = 0,
  Binary = 1,
}

/** Whether the Payload is compressed. */
export enum PayloadCompression {
  None = 0,
  Gzip = 1,
}

/** Semantic type of a wire frame. */
export enum FrameType {
  Invoke = 1,
  Reply = 2,
  Error = 3,
  Chunk = 4,
  End = 5,
  Subscribe = 6,
  Unsubscribe = 7,
  Auth = 8,
  AuthOk = 9,
  Ping = 10,
  Pong = 11,
}

/** Flags layout: bits 0-1 = PayloadEncoding, bits 2-3 = PayloadCompression. */
export function makeFlags(enc: PayloadEncoding, comp: PayloadCompression): number {
  return enc | (comp << 2);
}

export function getEncoding(flags: number): PayloadEncoding {
  return flags & 0x03;
}

export function getCompression(flags: number): PayloadCompression {
  return (flags >> 2) & 0x03;
}

/** A decoded wire frame. */
export interface WireFrame {
  flags: number;
  type: FrameType;
  corId: bigint;
  seq: number;
  transId: bigint;
  callId: string;
  subId: string;
  target: string;
  /** Originating actor. Trailing optional field: absent on v1-era
   *  frames, so decoders must tolerate EOF before reading it. */
  from: string;
  errorMsg: string;
  payload: Uint8Array;
  headers: Map<string, string>;
}

/** Encode a WireFrame to its canonical binary form. */
export function marshalWireFrame(f: WireFrame): Uint8Array {
  const w = new BinaryWriter();
  for (const b of MAGIC) {
    w.writeByte(b);
  }
  w.writeByte(VERSION);
  w.writeByte(f.flags);
  w.writeByte(f.type);
  w.writeUint64(f.corId);
  w.writeUint32(f.seq);
  w.writeUint64(f.transId);
  w.writeString(f.callId);
  w.writeString(f.subId);
  w.writeString(f.target);
  w.writeString(f.errorMsg);
  w.writeBytes(f.payload);

  // Byte-wise sort — matches gateway/frame_binary.go's SortedKeys.
  // localeCompare is locale-aware and orders non-ASCII keys differently.
  const entries = Array.from(f.headers.entries()).sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  w.writeUvarint(BigInt(entries.length));
  for (const [k, v] of entries) {
    w.writeString(k);
    w.writeString(v);
  }

  // `from` is appended after headers (v2 trailing field, v1 compat).
  w.writeString(f.from ?? "");

  return w.finish();
}

/** Decode a WireFrame from its canonical binary form. */
export function unmarshalWireFrame(data: Uint8Array): WireFrame {
  const r = new BinaryReader(data);

  for (let i = 0; i < MAGIC.length; i++) {
    if (r.readByte() !== MAGIC[i]) {
      throw new FrameError("invalid magic");
    }
  }

  const version = r.readByte();
  if (version !== VERSION) {
    throw new FrameError(`unsupported version: ${version}`);
  }

  const flags = r.readByte();
  const type = r.readByte() as FrameType;
  const corId = r.readUint64();
  const seq = r.readUint32();
  const transId = r.readUint64();
  const callId = r.readString();
  const subId = r.readString();
  const target = r.readString();
  const errorMsg = r.readString();
  const payload = r.readBytes();

  const headerCount = Number(r.readUvarint());
  const headers = new Map<string, string>();
  for (let i = 0; i < headerCount; i++) {
    const k = r.readString();
    const v = r.readString();
    headers.set(k, v);
  }

  // Trailing `from` (v2): tolerate v1-era frames that end after headers.
  const from = r.remaining() > 0 ? r.readString() : "";

  return { flags, type, corId, seq, transId, callId, subId, target, from, errorMsg, payload, headers };
}

/** Error raised during frame marshal/unmarshal. */
export class FrameError extends Error {
  constructor(message: string) {
    super(`binary_frame: ${message}`);
    this.name = "FrameError";
  }
}

// ---------------------------------------------------------------------------
// BinaryWriter
// ---------------------------------------------------------------------------

class BinaryWriter {
  private chunks: Uint8Array[] = [];
  private len = 0;

  writeByte(b: number): void {
    this.chunks.push(new Uint8Array([b & 0xff]));
    this.len += 1;
  }

  writeBytes(data: Uint8Array): void {
    this.writeUvarint(BigInt(data.length));
    if (data.length === 0) return;
    const copy = new Uint8Array(data.byteLength);
    copy.set(data);
    this.chunks.push(copy);
    this.len += copy.byteLength;
  }

  writeUvarint(v: bigint): void {
    if (v < 0n) throw new FrameError("negative uvarint");
    const out: number[] = [];
    while (v >= 0x80n) {
      out.push(Number(v & 0x7fn) | 0x80);
      v >>= 7n;
    }
    out.push(Number(v));
    this.chunks.push(Uint8Array.from(out));
    this.len += out.length;
  }

  writeUint32(v: number): void {
    const buf = new ArrayBuffer(4);
    new DataView(buf).setUint32(0, v >>> 0, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 4;
  }

  writeUint64(v: bigint): void {
    const buf = new ArrayBuffer(8);
    new DataView(buf).setBigUint64(0, v < 0n ? BigInt.asUintN(64, v) : v, false);
    this.chunks.push(new Uint8Array(buf));
    this.len += 8;
  }

  writeString(s: string): void {
    const bytes = new TextEncoder().encode(s);
    this.writeUvarint(BigInt(bytes.length));
    if (bytes.length > 0) {
      this.chunks.push(bytes);
      this.len += bytes.byteLength;
    }
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

// ---------------------------------------------------------------------------
// BinaryReader
// ---------------------------------------------------------------------------

class BinaryReader {
  private view: DataView;
  private off: number;
  private end: number;
  private decoder = new TextDecoder("utf-8", { fatal: true });

  constructor(private data: Uint8Array) {
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    this.off = 0;
    this.end = data.byteLength;
  }

  remaining(): number {
    return this.end - this.off;
  }

  readByte(): number {
    if (this.off >= this.end) throw new FrameError("unexpected EOF");
    return this.view.getUint8(this.off++);
  }

  readUvarint(): bigint {
    let result = 0n;
    let shift = 0n;
    for (;;) {
      if (this.off >= this.end) throw new FrameError("unexpected EOF in uvarint");
      const b = this.view.getUint8(this.off++);
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) return result;
      shift += 7n;
      if (shift >= 70n) throw new FrameError("invalid varint");
    }
  }

  readUint32(): number {
    if (this.off + 4 > this.end) throw new FrameError("unexpected EOF");
    const v = this.view.getUint32(this.off, false);
    this.off += 4;
    return v >>> 0;
  }

  readUint64(): bigint {
    if (this.off + 8 > this.end) throw new FrameError("unexpected EOF");
    const v = this.view.getBigUint64(this.off, false);
    this.off += 8;
    return v;
  }

  readBytes(): Uint8Array {
    const len = Number(this.readUvarint());
    if (len === 0) return new Uint8Array(0);
    if (this.off + len > this.end) throw new FrameError("unexpected EOF");
    const out = new Uint8Array(len);
    out.set(new Uint8Array(this.data.buffer, this.data.byteOffset + this.off, len));
    this.off += len;
    return out;
  }

  readString(): string {
    const len = Number(this.readUvarint());
    if (len === 0) return "";
    if (this.off + len > this.end) throw new FrameError("unexpected EOF");
    const slice = new Uint8Array(this.data.buffer, this.data.byteOffset + this.off, len);
    const s = this.decoder.decode(slice);
    this.off += len;
    return s;
  }
}
