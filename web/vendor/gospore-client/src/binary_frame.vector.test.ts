import { describe, it, expect } from "vitest";
import {
  marshalWireFrame,
  unmarshalWireFrame,
  FrameError,
  FrameType,
  type WireFrame,
} from "./binary_frame";
import vectorJSON from "../testdata/frame_vector.json";

/**
 * Golden cross-language vector: every case was marshaled by the Go
 * implementation (gateway/frame_binary.go via internal/wiregen). This
 * test pins the TypeScript twin to the exact same bytes, so format
 * drift fails tests on either side. Regenerate the JSON from Go with:
 *   go test ./internal/wiregen -update
 */

interface TSFrameJSON {
  flags: number;
  type: number;
  corId: string;
  seq: number;
  transId: string;
  callId: string;
  subId: string;
  target: string;
  from: string;
  errorMsg: string;
  payload: string | null; // hex; null = empty
  headers: [string, string][] | null;
}

interface VectorJSON {
  version: number;
  cases: { name: string; frame: TSFrameJSON; wire: string }[];
  errors: { name: string; wire: string; error: string }[];
}

const vector = vectorJSON as unknown as VectorJSON;

function hexToBytes(hex: string): Uint8Array {
  if (!hex) return new Uint8Array(0);
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function frameFromJSON(j: TSFrameJSON): WireFrame {
  const headers = new Map<string, string>();
  for (const [k, v] of j.headers ?? []) headers.set(k, v);
  return {
    flags: j.flags,
    type: j.type as FrameType,
    corId: BigInt(j.corId),
    seq: j.seq,
    transId: BigInt(j.transId),
    callId: j.callId,
    subId: j.subId,
    target: j.target,
    from: j.from,
    errorMsg: j.errorMsg,
    payload: hexToBytes(j.payload ?? ""),
    headers,
  };
}

function framesEqual(a: WireFrame, b: WireFrame): boolean {
  if (
    a.flags !== b.flags ||
    a.type !== b.type ||
    a.corId !== b.corId ||
    a.seq !== b.seq ||
    a.transId !== b.transId ||
    a.callId !== b.callId ||
    a.subId !== b.subId ||
    a.target !== b.target ||
    a.from !== b.from ||
    a.errorMsg !== b.errorMsg
  ) {
    return false;
  }
  if (a.payload.length !== b.payload.length) return false;
  for (let i = 0; i < a.payload.length; i++) {
    if (a.payload[i] !== b.payload[i]) return false;
  }
  if (a.headers.size !== b.headers.size) return false;
  for (const [k, v] of a.headers) {
    if (b.headers.get(k) !== v) return false;
  }
  return true;
}

describe("frame_vector (Go-generated golden)", () => {
  it("vector is present and versioned", () => {
    expect(vector.version).toBe(2);
    expect(vector.cases.length).toBeGreaterThan(0);
  });

  for (const tc of vector.cases) {
    it(`marshals byte-identical to Go: ${tc.name}`, () => {
      const bytes = marshalWireFrame(frameFromJSON(tc.frame));
      expect(Array.from(bytes)).toEqual(Array.from(hexToBytes(tc.wire)));
    });

    it(`decodes Go bytes back: ${tc.name}`, () => {
      const decoded = unmarshalWireFrame(hexToBytes(tc.wire));
      expect(framesEqual(decoded, frameFromJSON(tc.frame))).toBe(true);
    });
  }

  for (const ec of vector.errors) {
    it(`rejects malformed frame: ${ec.name}`, () => {
      expect(() => unmarshalWireFrame(hexToBytes(ec.wire))).toThrow(FrameError);
    });
  }
});
