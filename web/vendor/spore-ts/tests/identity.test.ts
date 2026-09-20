import { describe, expect, it } from "vitest";
import {
  fromHex,
  incarnation,
  isZero,
  newCanonicalID,
  runtimeSlot,
  sequence,
  timestampMs,
  toHex,
} from "../src/identity.js";

describe("CanonicalID", () => {
  it("constructs and round-trips through hex", () => {
    const id = newCanonicalID(0x123456789abcn, 0xfeed, 0x1234, 0xa1b2c3d4e5f6n);
    expect(id).toHaveLength(16);

    expect(timestampMs(id)).toBe(0x123456789abcn);
    expect(runtimeSlot(id)).toBe(0xfeed);
    expect(incarnation(id)).toBe(0x1234);
    expect(sequence(id)).toBe(0xa1b2c3d4e5f6n);

    const hex = toHex(id);
    expect(hex).toHaveLength(32);
    const restored = fromHex(hex);
    expect(restored).toEqual(id);
  });

  it("rejects out-of-range inputs", () => {
    expect(() => newCanonicalID(0x1000000000000n, 0, 0, 0n)).toThrow(/timestamp/);
    expect(() => newCanonicalID(0n, 0, 0, 0x1000000000000n)).toThrow(/sequence/);
    expect(() => newCanonicalID(0n, 0x10000, 0, 0n)).toThrow(/runtimeSlot/);
    expect(() => newCanonicalID(0n, 0, 0x10000, 0n)).toThrow(/incarnation/);
  });

  it("rejects malformed hex strings", () => {
    expect(() => fromHex("short")).toThrow(/length/);
    expect(() => fromHex("g".repeat(32))).toThrow(/hex/);
  });

  it("recognises the zero id", () => {
    const zero = new Uint8Array(16);
    expect(isZero(zero)).toBe(true);
    const nonZero = newCanonicalID(1n, 0, 0, 0n);
    expect(isZero(nonZero)).toBe(false);
  });
});
