import { describe, expect, it } from "vitest";
import { JSONCodec, EncodeError, DecodeError } from "../src/codec.js";
import { SchemaRegistry, type SchemaEntry } from "../src/registry.js";
import type { ObjectDesc, TypeDesc } from "../src/schema.js";

const userObject: ObjectDesc = {
  kind: "struct",
  name: "User",
  fields: [
    { name: "ID", type: { kind: "scalar", name: "string" } },
    { name: "Age", type: { kind: "scalar", name: "int" } },
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

describe("JSONCodec round-trip", () => {
  const codec = new JSONCodec();
  const registry = buildRegistry();

  it("encodes and decodes a struct value", () => {
    const value = { ID: "alice", Age: 30 };
    const bytes = codec.encode(userTypeRef, value, registry.objectFor("demo"));
    expect(bytes).toBeInstanceOf(Uint8Array);
    const decoded = codec.decode(userTypeRef, bytes, registry.objectFor("demo"));
    expect(decoded).toEqual(value);
  });

  it("handles array of strings", () => {
    const desc: TypeDesc = {
      kind: "array",
      element: { kind: "scalar", name: "string" },
    };
    const value = ["a", "b", "c"];
    const bytes = codec.encode(desc, value);
    expect(codec.decode(desc, bytes)).toEqual(value);
  });

  it("orders map keys alphabetically", () => {
    const desc: TypeDesc = {
      kind: "map",
      key: { kind: "scalar", name: "string" },
      value: { kind: "scalar", name: "int" },
    };
    const value = { z: 1, a: 2, m: 3 };
    const bytes = codec.encode(desc, value);
    const text = new TextDecoder().decode(bytes);
    // Keys must appear in alphabetical order.
    const aIdx = text.indexOf('"a"');
    const mIdx = text.indexOf('"m"');
    const zIdx = text.indexOf('"z"');
    expect(aIdx).toBeLessThan(mIdx);
    expect(mIdx).toBeLessThan(zIdx);

    const decoded = codec.decode(desc, bytes);
    expect(decoded).toEqual(value);
  });

  it("orders struct fields alphabetically on encode", () => {
    const value = { ID: "alice", Age: 30 };
    const bytes = codec.encode(userTypeRef, value, registry.objectFor("demo"));
    const text = new TextDecoder().decode(bytes);
    expect(text.indexOf('"Age"')).toBeLessThan(text.indexOf('"ID"'));
  });

  it("rejects encoding a non-array against an array desc", () => {
    const desc: TypeDesc = {
      kind: "array",
      element: { kind: "scalar", name: "string" },
    };
    expect(() => codec.encode(desc, "not array")).toThrow(EncodeError);
  });

  it("rejects decoding malformed JSON", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int" };
    const garbled = new TextEncoder().encode("not json {{");
    expect(() => codec.decode(desc, garbled)).toThrow(DecodeError);
  });

  it("rejects decoding wrong scalar type", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int" };
    const bytes = new TextEncoder().encode('"string-not-int"');
    expect(() => codec.decode(desc, bytes)).toThrow(DecodeError);
  });

  it("rejects encoding wrong scalar type", () => {
    const desc: TypeDesc = { kind: "scalar", name: "int" };
    expect(() => codec.encode(desc, "string-not-int")).toThrow(EncodeError);
  });

  it("passes through scalar primitives", () => {
    const intDesc: TypeDesc = { kind: "scalar", name: "int" };
    const bytes = codec.encode(intDesc, 42);
    expect(codec.decode(intDesc, bytes)).toBe(42);

    const boolDesc: TypeDesc = { kind: "scalar", name: "bool" };
    const bb = codec.encode(boolDesc, true);
    expect(codec.decode(boolDesc, bb)).toBe(true);
  });

  it("falls through gracefully when no registry resolver is provided", () => {
    const value = { ID: "x", Age: 1 };
    const bytes = codec.encode(userTypeRef, value);
    // Without resolver, we trust JSON.stringify; structure preserved.
    expect(codec.decode(userTypeRef, bytes)).toEqual(value);
  });

  it("returns a Uint8Array with byteOffset=0 so fetch wrappers read the right slice", () => {
    // Some fetch implementations (undici in Node, certain service-worker
    // interceptors) misread typed-array views with non-zero byteOffset and
    // pull bytes from buffer[0] instead of buffer[byteOffset]. JSONCodec
    // encode() copies into a fresh buffer to make this consistently safe.
    const intDesc: TypeDesc = { kind: "scalar", name: "int" };
    const bytes = codec.encode(intDesc, 42);
    expect(bytes.byteOffset).toBe(0);
    expect(bytes.buffer.byteLength).toBe(bytes.byteLength);
  });
});

describe("SchemaRegistry", () => {
  it("registers and retrieves entries", () => {
    const r = new SchemaRegistry();
    const entry: SchemaEntry = {
      namespace: "auth",
      schemaId: 1,
      name: "LoginReq",
      type: { kind: "struct", name: "LoginReq", className: "LoginReq" },
      object: {
        kind: "struct",
        name: "LoginReq",
        fields: [{ name: "User", type: { kind: "scalar", name: "string" } }],
      },
    };
    r.register(entry);
    expect(r.get("auth", 1)).toBe(entry);
    expect(r.getByName("auth", "LoginReq")).toBe(entry);
    expect(r.namespaces()).toEqual(["auth"]);
  });

  it("rejects duplicate schema id", () => {
    const r = new SchemaRegistry();
    r.register({
      namespace: "x",
      schemaId: 1,
      name: "A",
      type: { kind: "struct", name: "A", className: "A" },
    });
    expect(() =>
      r.register({
        namespace: "x",
        schemaId: 1,
        name: "B",
        type: { kind: "struct", name: "B", className: "B" },
      }),
    ).toThrow(/collision/);
  });

  it("rejects duplicate name within namespace", () => {
    const r = new SchemaRegistry();
    r.register({
      namespace: "x",
      schemaId: 1,
      name: "A",
      type: { kind: "struct", name: "A", className: "A" },
    });
    expect(() =>
      r.register({
        namespace: "x",
        schemaId: 2,
        name: "A",
        type: { kind: "struct", name: "A", className: "A" },
      }),
    ).toThrow(/collision/);
  });
});
