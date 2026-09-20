import { describe, expect, it } from "vitest";
import { BinaryCodec } from "../src/binary_codec.js";
import { DecodeError, EncodeError } from "../src/codec.js";
import { SchemaRegistry, type SchemaEntry } from "../src/registry.js";
import type { ObjectDesc, TypeDesc } from "../src/schema.js";

const MAGIC = new Uint8Array([0x54, 0x42, 0x43]); // "TBC"
const WIRE_VERSION = 0x03;

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const userObject: ObjectDesc = {
  kind: "struct",
  name: "User",
  fields: [
    { name: "ID", type: { kind: "scalar", name: "string" } },
    { name: "Age", type: { kind: "scalar", name: "int32" } },
  ],
};

const userTypeRef: TypeDesc = {
  kind: "struct",
  name: "User",
  className: "User",
};

function buildRegistry(): SchemaRegistry {
  const r = new SchemaRegistry();
  const entry: SchemaEntry = {
    namespace: "demo",
    schemaId: 1,
    name: "User",
    type: userTypeRef,
    object: userObject,
    visibility: "public",
  };
  r.register(entry);
  return r;
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

describe("BinaryCodec header", () => {
  const codec = new BinaryCodec();

  it("starts every payload with the TBC magic + explicit wire version", () => {
    const out = codec.encode({ kind: "scalar", name: "bool" }, true);
    expect(Array.from(out.slice(0, 3))).toEqual(Array.from(MAGIC));
    expect(out[3]).toBe(WIRE_VERSION);
  });

  it("rejects payloads with invalid magic", () => {
    const bad = new Uint8Array([0x00, 0x00, 0x00, WIRE_VERSION, 0x01]);
    expect(() => codec.decode({ kind: "scalar", name: "bool" }, bad)).toThrow(DecodeError);
  });

  it("rejects the legacy glued-on version (TBC\\x02)", () => {
    const legacy = new Uint8Array([0x54, 0x42, 0x43, 0x02, 0x02]);
    expect(() => codec.decode({ kind: "scalar", name: "bool" }, legacy)).toThrow(DecodeError);
  });

  it("rejects an unknown wire version", () => {
    const unknown = new Uint8Array([0x54, 0x42, 0x43, 0x7f, 0x02]);
    expect(() => codec.decode({ kind: "scalar", name: "bool" }, unknown)).toThrow(DecodeError);
  });

  it("rejects truncated payloads", () => {
    const bad = new Uint8Array([0x54, 0x42, 0x43]);
    expect(() => codec.decode({ kind: "scalar", name: "bool" }, bad)).toThrow(DecodeError);
  });

  it("rejects trailing bytes", () => {
    const valid = codec.encode({ kind: "scalar", name: "bool" }, true);
    const trailing = new Uint8Array(valid.byteLength + 1);
    trailing.set(valid);
    trailing[valid.byteLength] = 0x00;
    expect(() => codec.decode({ kind: "scalar", name: "bool" }, trailing)).toThrow(DecodeError);
  });
});

// ---------------------------------------------------------------------------
// Scalars — round-trip
// ---------------------------------------------------------------------------

describe("BinaryCodec scalars round-trip", () => {
  const codec = new BinaryCodec();

  it.each([
    ["bool true", { kind: "scalar", name: "bool" } as TypeDesc, true],
    ["bool false", { kind: "scalar", name: "bool" } as TypeDesc, false],
    ["int32 zero", { kind: "scalar", name: "int32" } as TypeDesc, 0],
    ["int32 positive", { kind: "scalar", name: "int32" } as TypeDesc, 12345],
    ["int32 negative", { kind: "scalar", name: "int32" } as TypeDesc, -42],
    ["float64 normal", { kind: "scalar", name: "float64" } as TypeDesc, 3.14159],
    ["float64 negative", { kind: "scalar", name: "float64" } as TypeDesc, -1.5e-20],
    ["string empty", { kind: "scalar", name: "string" } as TypeDesc, ""],
    ["string ascii", { kind: "scalar", name: "string" } as TypeDesc, "hello"],
    ["string utf-8", { kind: "scalar", name: "string" } as TypeDesc, "héllo 世界 🐉"],
  ])("%s", (_label, desc, value) => {
    const bytes = codec.encode(desc, value);
    expect(codec.decode(desc, bytes)).toEqual(value);
  });

  it("int64 small value decodes as number", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int64" };
    const bytes = codec.encode(desc, 42);
    expect(codec.decode(desc, bytes)).toBe(42);
  });

  it("int64 large value preserves bigint", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int64" };
    const v = (1n << 53n) + 7n; // outside safe integer range
    const bytes = codec.encode(desc, v);
    const decoded = codec.decode(desc, bytes);
    expect(decoded).toBe(v);
    expect(typeof decoded).toBe("bigint");
  });

  it("int64 negative bigint round-trips", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int64" };
    const v = -((1n << 53n) + 1n);
    const bytes = codec.encode(desc, v);
    expect(codec.decode(desc, bytes)).toBe(v);
  });

  it("uint64 large value preserves bigint", () => {
    const desc: TypeDesc = { kind: "scalar", name: "uint64" };
    const v = (1n << 60n) + 1n;
    const bytes = codec.encode(desc, v);
    expect(codec.decode(desc, bytes)).toBe(v);
  });

  it("bytes round-trips as Uint8Array", () => {
    const desc: TypeDesc = { kind: "scalar", name: "bytes" };
    const value = new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0x00, 0xff]);
    const bytes = codec.encode(desc, value);
    const decoded = codec.decode(desc, bytes);
    expect(decoded).toBeInstanceOf(Uint8Array);
    expect(Array.from(decoded as Uint8Array)).toEqual(Array.from(value));
  });

  it("bytes encodes empty Uint8Array", () => {
    const desc: TypeDesc = { kind: "scalar", name: "bytes" };
    const value = new Uint8Array(0);
    const bytes = codec.encode(desc, value);
    const decoded = codec.decode(desc, bytes) as Uint8Array;
    expect(decoded.byteLength).toBe(0);
  });

  it("bytes encode accepts number[] and string fallback", () => {
    const desc: TypeDesc = { kind: "scalar", name: "bytes" };
    const fromArray = codec.encode(desc, [1, 2, 3]);
    expect(Array.from(codec.decode(desc, fromArray) as Uint8Array)).toEqual([1, 2, 3]);

    const fromString = codec.encode(desc, "abc");
    expect(Array.from(codec.decode(desc, fromString) as Uint8Array)).toEqual([0x61, 0x62, 0x63]);
  });
});

// ---------------------------------------------------------------------------
// Composite
// ---------------------------------------------------------------------------

describe("BinaryCodec composites", () => {
  const codec = new BinaryCodec();

  it("array of strings", () => {
    const desc: TypeDesc = { kind: "array", element: { kind: "scalar", name: "string" } };
    const value = ["a", "b", "c"];
    const bytes = codec.encode(desc, value);
    expect(codec.decode(desc, bytes)).toEqual(value);
  });

  it("array empty", () => {
    const desc: TypeDesc = { kind: "array", element: { kind: "scalar", name: "int32" } };
    expect(codec.decode(desc, codec.encode(desc, []))).toEqual([]);
  });

  it("map plain object — alphabetical key order on wire", () => {
    const desc: TypeDesc = {
      kind: "map",
      key: { kind: "scalar", name: "string" },
      value: { kind: "scalar", name: "int32" },
    };
    const value = { z: 1, a: 2, m: 3 };
    const bytes = codec.encode(desc, value);

    // Skip 4-byte magic + 1-byte map tag + 1-byte uvarint length (count=3).
    const payload = bytes.slice(6);
    // First key field is a uvarint length (1 byte for short keys), then bytes.
    // Look for the first occurrence of each key in the payload.
    const text = new TextDecoder().decode(payload);
    expect(text.indexOf("a")).toBeLessThan(text.indexOf("m"));
    expect(text.indexOf("m")).toBeLessThan(text.indexOf("z"));

    expect(codec.decode(desc, bytes)).toEqual(value);
  });

  it("map empty", () => {
    const desc: TypeDesc = {
      kind: "map",
      key: { kind: "scalar", name: "string" },
      value: { kind: "scalar", name: "string" },
    };
    expect(codec.decode(desc, codec.encode(desc, {}))).toEqual({});
  });

  it("map as entry sequence — preserves ordering", () => {
    const desc: TypeDesc = {
      kind: "map",
      key: { kind: "scalar", name: "string" },
      value: { kind: "scalar", name: "int32" },
    };
    const entries = [
      { Key: "z", Value: 1 },
      { Key: "a", Value: 2 },
      { Key: "m", Value: 3 },
    ];
    const bytes = codec.encode(desc, entries);
    // Tag 0x0B == TAG_ENTRY_SEQUENCE; appears right after the 4-byte magic.
    expect(bytes[4]).toBe(0x0b);
    const decoded = codec.decode(desc, bytes);
    expect(decoded).toEqual(entries);
  });

  it("struct round-trip via ObjectDesc", () => {
    const registry = buildRegistry();
    const value = { ID: "alice", Age: 30 };
    const bytes = codec.encode(userTypeRef, value, registry.objectFor("demo"));
    expect(codec.decode(userTypeRef, bytes, registry.objectFor("demo"))).toEqual(value);
  });

  it("struct field order on wire is alphabetical", () => {
    const registry = buildRegistry();
    const value = { ID: "alice", Age: 30 };
    const bytes = codec.encode(userTypeRef, value, registry.objectFor("demo"));
    const text = new TextDecoder().decode(bytes);
    expect(text.indexOf("Age")).toBeLessThan(text.indexOf("ID"));
  });

  it("struct excludes private fields", () => {
    const obj: ObjectDesc = {
      kind: "struct",
      name: "Mixed",
      fields: [
        { name: "Public", type: { kind: "scalar", name: "string" } },
        { name: "Secret", type: { kind: "scalar", name: "string" }, private: true },
      ],
    };
    const ref: TypeDesc = { kind: "struct", name: "Mixed", className: "Mixed" };
    const lookup = (n: string) => (n === "Mixed" ? obj : undefined);
    const bytes = codec.encode(ref, { Public: "p", Secret: "s" }, lookup);
    const decoded = codec.decode(ref, bytes, lookup);
    expect(decoded).toEqual({ Public: "p" });
  });

  it("nested struct in struct round-trips", () => {
    const inner: ObjectDesc = {
      kind: "struct",
      name: "Inner",
      fields: [{ name: "N", type: { kind: "scalar", name: "int32" } }],
    };
    const outer: ObjectDesc = {
      kind: "struct",
      name: "Outer",
      fields: [{ name: "Child", type: { kind: "struct", name: "Inner", className: "Inner" } }],
    };
    const ref: TypeDesc = { kind: "struct", name: "Outer", className: "Outer" };
    const lookup = (n: string) => (n === "Inner" ? inner : n === "Outer" ? outer : undefined);
    const value = { Child: { N: 7 } };
    const bytes = codec.encode(ref, value, lookup);
    expect(codec.decode(ref, bytes, lookup)).toEqual(value);
  });

  it("nested struct with bytes field round-trips", () => {
    const audioObject: ObjectDesc = {
      kind: "struct",
      name: "VoiceAudio",
      fields: [
        { name: "AudioType", type: { kind: "scalar", name: "int" } },
        { name: "Data", type: { kind: "scalar", name: "bytes" } },
      ],
    };
    const reqObject: ObjectDesc = {
      kind: "struct",
      name: "VoiceRecognizeReq",
      fields: [
        { name: "Audio", type: { kind: "struct", name: "struct", className: "VoiceAudio" } },
      ],
    };
    const ref: TypeDesc = { kind: "struct", name: "VoiceRecognizeReq", className: "VoiceRecognizeReq" };
    const registry = new SchemaRegistry();
    registry.register({ namespace: "voice", schemaId: 1, name: "VoiceAudio", type: { kind: "struct", className: "VoiceAudio" }, object: audioObject, visibility: "public" });
    registry.register({ namespace: "voice", schemaId: 2, name: "VoiceRecognizeReq", type: ref, object: reqObject, visibility: "public" });
    const objFor = registry.objectFor("voice");

    const pcmData = new Uint8Array(1024);
    for (let i = 0; i < pcmData.length; i++) pcmData[i] = i & 0xff;
    const value = { Audio: { AudioType: 2, Data: pcmData } };
    const bytes = codec.encode(ref, value, objFor);
    const decoded = codec.decode(ref, bytes, objFor) as typeof value;

    expect(decoded.Audio.AudioType).toBe(2);
    expect(decoded.Audio.Data).toBeInstanceOf(Uint8Array);
    expect(decoded.Audio.Data.byteLength).toBe(1024);
    expect(Array.from(decoded.Audio.Data)).toEqual(Array.from(pcmData));
  });
});

// ---------------------------------------------------------------------------
// Validation / error paths
// ---------------------------------------------------------------------------

describe("BinaryCodec validation", () => {
  const codec = new BinaryCodec();

  it("rejects non-array against array desc", () => {
    const desc: TypeDesc = { kind: "array", element: { kind: "scalar", name: "string" } };
    expect(() => codec.encode(desc, "not array")).toThrow(EncodeError);
  });

  it("rejects wrong scalar type on encode", () => {
    expect(() => codec.encode({ kind: "scalar", name: "string" }, 42)).toThrow(EncodeError);
    expect(() => codec.encode({ kind: "scalar", name: "bool" }, "yes")).toThrow(EncodeError);
    expect(() => codec.encode({ kind: "scalar", name: "int32" }, "5")).toThrow(EncodeError);
  });

  it("rejects unknown tag during decode", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int32" };
    const bad = new Uint8Array([0x54, 0x42, 0x43, WIRE_VERSION, 0xff]); // header + bad tag
    expect(() => codec.decode(desc, bad)).toThrow(DecodeError);
  });
});

// ---------------------------------------------------------------------------
// Null / undefined
// ---------------------------------------------------------------------------

describe("BinaryCodec null handling", () => {
  const codec = new BinaryCodec();

  it("encodes null as TAG_NULL", () => {
    const desc: TypeDesc = { kind: "scalar", name: "string" };
    const bytes = codec.encode(desc, null);
    // After 4-byte magic, the first byte should be 0x00 (TAG_NULL).
    expect(bytes[4]).toBe(0x00);
    expect(codec.decode(desc, bytes)).toBeNull();
  });

  it("encodes undefined as TAG_NULL", () => {
    const desc: TypeDesc = { kind: "scalar", name: "string" };
    const bytes = codec.encode(desc, undefined);
    expect(bytes[4]).toBe(0x00);
    expect(codec.decode(desc, bytes)).toBeNull();
  });
});
